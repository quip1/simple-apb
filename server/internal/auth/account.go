// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/quip1/simple-apb/server/internal/store"
)

var (
	ErrWrongPassword = errf(http.StatusUnauthorized, "wrong_password", "the current password is wrong")
	ErrTOTPRequired  = errf(http.StatusForbidden, "totp_required", "two-factor authentication is required for this account")
	ErrTOTPOff       = errf(http.StatusConflict, "totp_not_enabled", "two-factor authentication is off")
)

// reauth rate-limits password/code re-checks per account (same budget as logins).
func (s *Service) reauth(userID string) error {
	if ok, wait := s.Limits.LoginAccount.Allow("reauth:" + userID); !ok {
		return rateLimited(wait)
	}
	return nil
}

func (s *Service) checkPassword(u *store.User, pw string) error {
	if u.PasswordHash == "" {
		return ErrWrongPassword
	}
	ok, err := VerifyPassword(u.PasswordHash, pw)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return ErrWrongPassword
	}
	return nil
}

// ChangePassword sets a new password after checking the current one, and
// signs out every other session of the user.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next, ip string) error {
	if err := s.reauth(p.User.ID); err != nil {
		return err
	}
	if err := s.checkPassword(p.User, current); err != nil {
		return err
	}
	if prob := CheckPassword(next, p.User.Username); prob != PasswordAcceptable {
		return errf(http.StatusBadRequest, string(prob), "password does not meet the policy")
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetPasswordHash(ctx, tx, p.User.ID, hash); err != nil {
			return err
		}
		if err := store.RevokeOtherSessions(ctx, tx, p.User.ID, p.SessionID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "user.password_changed",
			ObjectType: "user", ObjectID: p.User.ID, IP: ip})
	})
}

// DisableTOTP turns 2FA off (password + current code), unless it's required.
func (s *Service) DisableTOTP(ctx context.Context, p *Principal, password, code, ip string) error {
	if p.TOTPRequired {
		return ErrTOTPRequired
	}
	if p.User.TOTPSecretEnc == "" {
		return ErrTOTPOff
	}
	if err := s.reauth(p.User.ID); err != nil {
		return err
	}
	if err := s.checkPassword(p.User, password); err != nil {
		return err
	}
	if ok, err := s.CheckTOTP(ctx, p.User, code); err != nil {
		return err
	} else if !ok {
		return ErrInvalidTOTP
	}
	return s.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetTOTP(ctx, tx, p.User.ID, ""); err != nil {
			return err
		}
		if err := store.ReplaceRecoveryCodes(ctx, tx, p.User.ID, nil); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "user.totp_disabled",
			ObjectType: "user", ObjectID: p.User.ID, IP: ip})
	})
}

// RegenerateRecoveryCodes replaces the recovery codes after a current code.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, p *Principal, code, ip string) ([]string, error) {
	if p.User.TOTPSecretEnc == "" {
		return nil, ErrTOTPOff
	}
	if err := s.reauth(p.User.ID); err != nil {
		return nil, err
	}
	if ok, err := s.CheckTOTP(ctx, p.User, code); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrInvalidTOTP
	}
	codes, hashes, err := NewRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	err = s.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.ReplaceRecoveryCodes(ctx, tx, p.User.ID, hashes); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "user.recovery_codes_regenerated",
			ObjectType: "user", ObjectID: p.User.ID, IP: ip})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}
