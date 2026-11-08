// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
)

// Password reset links (brief §6.6: "an admin generates a one-time reset
// link"). A token is userID.expiry.mac where the MAC also covers the user's
// current password hash: it needs no table, and it stops working as soon as
// the password changes, so it can be used only once.

// ResetTTL is how long a reset link works.
const ResetTTL = 24 * time.Hour

var ErrBadResetToken = errf(http.StatusBadRequest, "invalid_reset_token", "this reset link is invalid, used or expired")

func (s *Service) resetMAC(userID string, exp int64, pwHash string) []byte {
	m := hmac.New(sha256.New, s.ResetKey)
	m.Write([]byte(userID + "\x00" + strconv.FormatInt(exp, 10) + "\x00" + pwHash))
	return m.Sum(nil)
}

// NewResetToken returns a reset token for a user and when it expires.
func (s *Service) NewResetToken(u *store.User) (string, time.Time, error) {
	if len(s.ResetKey) == 0 {
		return "", time.Time{}, errors.New("reset key not configured")
	}
	exp := s.now().Add(ResetTTL)
	mac := base64.RawURLEncoding.EncodeToString(s.resetMAC(u.ID, exp.Unix(), u.PasswordHash))
	return u.ID + "." + strconv.FormatInt(exp.Unix(), 10) + "." + mac, exp, nil
}

// ResetPassword sets a new password with a token and signs out every session.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword, ip string) error {
	if ok, wait := s.Limits.ResetIP.Allow(ip); !ok {
		return rateLimited(wait)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(s.ResetKey) == 0 {
		return ErrBadResetToken
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || s.now().Unix() > exp {
		return ErrBadResetToken
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrBadResetToken
	}
	u, err := store.GetUser(ctx, s.DB, parts[0])
	if err != nil {
		return ErrBadResetToken
	}
	if !hmac.Equal(mac, s.resetMAC(u.ID, exp, u.PasswordHash)) {
		return ErrBadResetToken
	}
	if u.Status == "deleted" || u.Status == "banned" {
		return ErrBadResetToken
	}
	if prob := CheckPassword(newPassword, u.Username); prob != PasswordAcceptable {
		return errf(http.StatusBadRequest, string(prob), "password does not meet the policy")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetPasswordHash(ctx, tx, u.ID, hash); err != nil {
			return err
		}
		if err := store.RevokeUserSessions(ctx, tx, u.ID); err != nil {
			return err
		}
		// A reset also clears a lockout from failed attempts.
		if err := store.RecordLoginFailure(ctx, tx, u.ID, 0, nil); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: u.ID, Action: "user.password_reset", ObjectType: "user", ObjectID: u.ID, IP: ip})
	})
}

// RequestReset handles "forgot my password" (brief §6.6, email reset). It
// returns the account and a reset token when one should be emailed, or a nil
// user when there is nobody to email (unknown login, no address, an account
// that can't sign in, or the per-account limit). Callers answer the same way
// in every case so the response never reveals whether an account exists.
func (s *Service) RequestReset(ctx context.Context, login, ip string) (*store.User, string, error) {
	if ok, wait := s.Limits.ResetRequestIP.Allow(ip); !ok {
		return nil, "", rateLimited(wait)
	}
	u, err := store.GetUserByLogin(ctx, s.DB, login)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", nil
	} else if err != nil {
		return nil, "", err
	}
	if u.Email == "" || (u.Status != "active" && u.Status != "restricted") {
		return nil, "", nil
	}
	if ok, _ := s.Limits.ResetRequestAccount.Allow(u.ID); !ok {
		return nil, "", nil
	}
	token, _, err := s.NewResetToken(u)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}
