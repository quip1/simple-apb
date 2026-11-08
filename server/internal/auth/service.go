// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Error is an auth failure with a stable code and HTTP status.
type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

var (
	ErrInvalidCredentials = errf(http.StatusUnauthorized, "invalid_credentials", "wrong username or password")
	ErrInvalidTOTP        = errf(http.StatusUnauthorized, "invalid_totp", "wrong or expired code")
	ErrUnauthenticated    = errf(http.StatusUnauthorized, "unauthenticated", "sign in required")
	ErrSessionInvalid     = errf(http.StatusUnauthorized, "invalid_token", "token expired or revoked")
	ErrAccountPending     = errf(http.StatusForbidden, "account_pending", "your account is waiting for approval")
	ErrAccountDenied      = errf(http.StatusForbidden, "account_denied", "your sign-up request was declined")
	ErrSignupClosed       = errf(http.StatusForbidden, "signup_closed", "sign-ups are closed on this instance")
	ErrUsernameTaken      = errf(http.StatusConflict, "username_taken", "that username is taken")
	ErrTOTPAlreadyOn      = errf(http.StatusConflict, "totp_already_enabled", "two-factor authentication is already on")
	ErrTOTPNotStarted     = errf(http.StatusBadRequest, "totp_not_started", "start enrollment first")
)

func rateLimited(wait time.Duration) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many attempts", RetryAfter: wait}
}

// Device identifies the client creating a session.
type Device struct{ ID, Name, Platform, AppVersion string }

// TokenSet is what a successful login/refresh returns.
type TokenSet struct {
	SessionID  string
	Access     string
	AccessExp  time.Time
	Refresh    string
	RefreshExp time.Time
}

// Principal is the authenticated caller of a request.
type Principal struct {
	User      *store.User
	SessionID string
	Subject   authz.Subject
	// MustEnrollTOTP: TOTP is required for this account but not set up yet;
	// only enrollment endpoints, /me and logout are allowed.
	MustEnrollTOTP bool
	// TOTPRequired: admin, a tag, or the instance requires 2FA for this account.
	TOTPRequired bool
}

// Can is shorthand for authz.Allowed on the principal.
func (p *Principal) Can(a authz.Action) bool {
	return p != nil && !p.MustEnrollTOTP && authz.Allowed(p.Subject, a)
}

// Service implements the account flows.
type Service struct {
	DB     *store.DB
	Enc    store.Encrypter
	Tokens *Tokens
	Limits *Limits
	// ResetKey signs password reset links.
	ResetKey []byte
	Log      *slog.Logger
	Now      func() time.Time
	// OnSignup runs inside the sign-up transaction (queues the admin push).
	OnSignup func(ctx context.Context, q store.Querier, requestID string)
	pending  sync.Map // userID → pendingTOTP
}

type pendingTOTP struct {
	secret  string
	expires time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) settings() settings.Store { return settings.Store{Q: s.DB, Enc: s.Enc} }

// ── Sign-up ──

// SignupInput is a sign-up form.
type SignupInput struct {
	Username, Password, Email, Message string
	AgeConfirmed, TermsAccepted        bool
	IP                                 string
}

// Signup creates a pending user and a sign-up request for admins.
func (s *Service) Signup(ctx context.Context, in SignupInput) error {
	if ok, wait := s.Limits.SignupIP.Allow(in.IP); !ok {
		return rateLimited(wait)
	}
	inst, err := s.settings().Instance(ctx)
	if err != nil {
		return err
	}
	if !inst.SignupOpen {
		return ErrSignupClosed
	}
	if !in.AgeConfirmed || !in.TermsAccepted {
		return errf(http.StatusBadRequest, "consent_required", "you must confirm your age and accept the terms")
	}
	if p := CheckPassword(in.Password, in.Username); p != PasswordAcceptable {
		return errf(http.StatusBadRequest, string(p), "password does not meet the policy")
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return err
	}
	return s.DB.InTx(ctx, func(tx *store.Tx) error {
		id, err := store.CreateUser(ctx, tx, store.NewUser{
			Username: in.Username, Email: strings.TrimSpace(in.Email), PasswordHash: hash,
			Status: "pending", Role: "user", AgeConfirmed: true, TermsAccepted: true,
		})
		if errors.Is(err, store.ErrConflict) {
			return ErrUsernameTaken
		}
		if err != nil {
			return err
		}
		reqID, err := store.CreateSignupRequest(ctx, tx, id, in.Username, in.Email, in.Message, in.IP)
		if err != nil {
			return err
		}
		if s.OnSignup != nil {
			s.OnSignup(ctx, tx, reqID)
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: id, Action: "user.signup", ObjectType: "user", ObjectID: id, IP: in.IP})
	})
}

// ── Login ──

// LoginResult is either tokens or a TOTP challenge.
type LoginResult struct {
	Tokens    *TokenSet
	Challenge string // set when TOTP is required
	User      *store.User
}

// Login checks a password and starts a session (or a TOTP challenge).
func (s *Service) Login(ctx context.Context, username, password string, dev Device, ip string) (*LoginResult, error) {
	if ok, wait := s.Limits.LoginIP.Allow(ip); !ok {
		return nil, rateLimited(wait)
	}
	if ok, wait := s.Limits.LoginAccount.Allow(strings.ToLower(username)); !ok {
		return nil, rateLimited(wait)
	}
	u, err := store.GetUserByUsername(ctx, s.DB, username)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(dummyHash(), password) // equalize timing
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if u.LockedUntil != nil && *u.LockedUntil > now.UnixMilli() {
		e := errf(http.StatusTooManyRequests, "account_locked", "too many failed attempts; try again later")
		e.RetryAfter = time.UnixMilli(*u.LockedUntil).Sub(now)
		return nil, e
	}
	ok := false
	if u.PasswordHash != "" {
		if ok, err = VerifyPassword(u.PasswordHash, password); err != nil {
			return nil, fmt.Errorf("verify password: %w", err)
		}
	}
	if !ok {
		return nil, s.recordFailure(ctx, u, ip)
	}
	if err := s.checkLoginStatus(ctx, u); err != nil {
		return nil, err
	}
	if u.TOTPSecretEnc != "" {
		ch, err := s.Tokens.IssueChallenge(u.ID, dev)
		if err != nil {
			return nil, err
		}
		return &LoginResult{Challenge: ch, User: u}, nil
	}
	return s.finishLogin(ctx, u, dev, ip)
}

func (s *Service) recordFailure(ctx context.Context, u *store.User, ip string) error {
	failures := u.FailedLogins + 1
	var until *int64
	if d := LockoutDuration(failures); d > 0 {
		t := s.now().Add(d).UnixMilli()
		until = &t
	}
	if err := store.RecordLoginFailure(ctx, s.DB, u.ID, failures, until); err != nil {
		return err
	}
	if until != nil {
		_ = store.Audit(ctx, s.DB, store.AuditEntry{ActorID: u.ID, Action: "user.locked", ObjectType: "user", ObjectID: u.ID,
			IP: ip, Details: map[string]any{"failures": failures}})
	}
	return ErrInvalidCredentials
}

// checkLoginStatus decides whether a user with a correct password may get a
// session. Suspended/banned users may (authz then limits them to export,
// delete and logout); pending/denied users may not.
func (s *Service) checkLoginStatus(ctx context.Context, u *store.User) error {
	switch u.Status {
	case "pending":
		return ErrAccountPending
	case "deleted":
		if u.StatusReason == "signup_denied" {
			return ErrAccountDenied
		}
		return ErrInvalidCredentials
	case "suspended":
		if u.StatusUntil != nil && *u.StatusUntil <= s.now().UnixMilli() {
			if err := store.SetUserStatus(ctx, s.DB, u.ID, "active", "suspension ended", nil); err != nil {
				return err
			}
			u.Status = "active"
		}
	}
	return nil
}

// VerifyTOTP completes a login started with a TOTP challenge.
func (s *Service) VerifyTOTP(ctx context.Context, challenge, code, recovery, ip string) (*LoginResult, error) {
	if ok, wait := s.Limits.LoginIP.Allow(ip); !ok {
		return nil, rateLimited(wait)
	}
	c, err := s.Tokens.ParseChallenge(challenge)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	if ok, wait := s.Limits.LoginAccount.Allow("totp:" + c.Subject); !ok {
		return nil, rateLimited(wait)
	}
	u, err := store.GetUser(ctx, s.DB, c.Subject)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	if err := s.checkLoginStatus(ctx, u); err != nil {
		return nil, err
	}
	secret, err := s.Enc.Decrypt(u.TOTPSecretEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt totp secret: %w", err)
	}
	valid := false
	switch {
	case code != "":
		if step, ok := MatchTOTPStep(secret, code, s.now()); ok {
			if valid, err = store.ClaimTOTPStep(ctx, s.DB, u.ID, step); err != nil {
				return nil, err
			}
		}
	case recovery != "":
		if valid, err = store.UseRecoveryCode(ctx, s.DB, u.ID, HashRecoveryCode(recovery)); err != nil {
			return nil, err
		}
		if valid {
			_ = store.Audit(ctx, s.DB, store.AuditEntry{ActorID: u.ID, Action: "user.recovery_code_used", ObjectType: "user", ObjectID: u.ID, IP: ip})
		}
	}
	if !valid {
		_ = s.recordFailure(ctx, u, ip)
		return nil, ErrInvalidTOTP
	}
	dev := Device{ID: c.DeviceID, Name: c.DeviceName, Platform: c.Platform, AppVersion: c.AppVersion}
	return s.finishLogin(ctx, u, dev, ip)
}

func (s *Service) finishLogin(ctx context.Context, u *store.User, dev Device, ip string) (*LoginResult, error) {
	refresh, hash, err := NewOpaqueToken()
	if err != nil {
		return nil, err
	}
	refreshExp := s.now().Add(RefreshTTL)
	sess := &store.Session{UserID: u.ID, DeviceID: dev.ID, DeviceName: dev.Name, Platform: dev.Platform,
		AppVersion: dev.AppVersion, IP: ip, RefreshTokenHash: hash, ExpiresAt: refreshExp.UnixMilli()}
	err = s.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.CreateSession(ctx, tx, sess); err != nil {
			return err
		}
		if err := store.RecordLoginSuccess(ctx, tx, u.ID); err != nil {
			return err
		}
		if u.Role == "admin" {
			return store.Audit(ctx, tx, store.AuditEntry{ActorID: u.ID, Action: "admin.login", ObjectType: "session", ObjectID: sess.ID, IP: ip})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	access, accessExp, err := s.Tokens.IssueAccess(u.ID, sess.ID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{User: u, Tokens: &TokenSet{SessionID: sess.ID, Access: access, AccessExp: accessExp,
		Refresh: refresh, RefreshExp: refreshExp}}, nil
}

// refreshRaceGrace: a refresh token reused within this window after rotation
// is treated as a client race (two requests refreshing at once), not theft.
const refreshRaceGrace = 30 * time.Second

// Refresh rotates a refresh token. Reusing an old token outside the race
// grace window revokes the session (stolen-token detection).
func (s *Service) Refresh(ctx context.Context, refresh, ip string) (*TokenSet, error) {
	if refresh == "" {
		return nil, ErrSessionInvalid
	}
	hash := HashOpaque(refresh)
	sess, err := store.FindSessionByRefresh(ctx, s.DB, hash)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if sess.RevokedAt != nil || sess.ExpiresAt <= now.UnixMilli() {
		return nil, ErrSessionInvalid
	}
	if sess.RefreshTokenHash != hash { // matched the previous token: reuse
		if now.Sub(time.UnixMilli(sess.LastSeenAt)) > refreshRaceGrace {
			_ = store.RevokeSession(ctx, s.DB, sess.ID, "")
			_ = store.Audit(ctx, s.DB, store.AuditEntry{ActorID: sess.UserID, Action: "session.refresh_reuse_revoked",
				ObjectType: "session", ObjectID: sess.ID, IP: ip})
		}
		return nil, ErrSessionInvalid
	}
	u, err := store.GetUser(ctx, s.DB, sess.UserID)
	if err != nil || u.Status == "deleted" || u.Status == "pending" {
		return nil, ErrSessionInvalid
	}
	newRefresh, newHash, err := NewOpaqueToken()
	if err != nil {
		return nil, err
	}
	exp := now.Add(RefreshTTL)
	ok, err := store.RotateRefresh(ctx, s.DB, sess.ID, hash, newHash, exp.UnixMilli(), ip)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionInvalid // lost a concurrent rotation
	}
	access, accessExp, err := s.Tokens.IssueAccess(u.ID, sess.ID)
	if err != nil {
		return nil, err
	}
	return &TokenSet{SessionID: sess.ID, Access: access, AccessExp: accessExp, Refresh: newRefresh, RefreshExp: exp}, nil
}

// Logout revokes the caller's session.
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return store.RevokeSession(ctx, s.DB, p.SessionID, p.User.ID)
}

// Authenticate turns an access token into a Principal.
func (s *Service) Authenticate(ctx context.Context, access string) (*Principal, error) {
	c, err := s.Tokens.ParseAccess(access)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	sess, err := store.GetSession(ctx, s.DB, c.SessionID)
	if err != nil || sess.RevokedAt != nil || sess.UserID != c.Subject {
		return nil, ErrSessionInvalid
	}
	u, err := store.GetUser(ctx, s.DB, c.Subject)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	if u.Status == "pending" || u.Status == "deleted" {
		return nil, ErrSessionInvalid
	}
	if s.now().Sub(time.UnixMilli(sess.LastSeenAt)) > time.Minute {
		_ = store.TouchSession(ctx, s.DB, sess.ID)
	}
	return s.principal(ctx, u, sess.ID)
}

// PrincipalFor evaluates a user's permissions without a session (admin views).
func (s *Service) PrincipalFor(ctx context.Context, u *store.User) (*Principal, error) {
	return s.principal(ctx, u, "")
}

func (s *Service) principal(ctx context.Context, u *store.User, sessionID string) (*Principal, error) {
	overrides, err := store.GetPermissionOverrides(ctx, s.DB, u.ID)
	if err != nil {
		return nil, err
	}
	tags, tagRequiresTOTP, err := store.TagOverrides(ctx, s.DB, u.ID)
	if err != nil {
		return nil, err
	}
	st := s.settings()
	defaults, err := st.PermissionDefaults(ctx)
	if err != nil {
		return nil, err
	}
	authSet, err := st.Auth(ctx)
	if err != nil {
		return nil, err
	}
	subj := authz.Subject{
		Status: u.Status, Role: u.Role, TagOverrides: tags, Defaults: defaults,
		UserOverrides: map[authz.Action]*bool{
			authz.Upload: overrides.CanUpload, authz.EditMetadata: overrides.CanEditMetadata,
			authz.DeleteOwn: overrides.CanDeleteOwn, authz.Share: overrides.CanShare,
			authz.Review: overrides.CanReview, authz.PostPublic: overrides.CanPostPublic,
		},
	}
	required := u.Role == "admin" || tagRequiresTOTP || authSet.RequireTOTPAll
	return &Principal{User: u, SessionID: sessionID, Subject: subj, TOTPRequired: required, MustEnrollTOTP: required && u.TOTPSecretEnc == ""}, nil
}

// ── TOTP enrollment for signed-in users ──

// EnrollTOTP starts enrollment; the secret is held in memory for 10 minutes.
func (s *Service) EnrollTOTP(ctx context.Context, p *Principal, issuerName string) (secret, uri string, err error) {
	if p.User.TOTPSecretEnc != "" {
		return "", "", ErrTOTPAlreadyOn
	}
	secret, uri, err = NewTOTPSecret(issuerName, p.User.Username)
	if err != nil {
		return "", "", err
	}
	s.pending.Store(p.User.ID, pendingTOTP{secret: secret, expires: s.now().Add(10 * time.Minute)})
	return secret, uri, nil
}

// ConfirmTOTP activates TOTP and returns fresh recovery codes.
func (s *Service) ConfirmTOTP(ctx context.Context, p *Principal, code, ip string) ([]string, error) {
	v, ok := s.pending.Load(p.User.ID)
	pt, _ := v.(pendingTOTP)
	if !ok || s.now().After(pt.expires) {
		return nil, ErrTOTPNotStarted
	}
	step, ok := MatchTOTPStep(pt.secret, code, s.now())
	if !ok {
		return nil, ErrInvalidTOTP
	}
	enc, err := s.Enc.Encrypt(pt.secret)
	if err != nil {
		return nil, err
	}
	codes, hashes, err := NewRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	err = s.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetTOTP(ctx, tx, p.User.ID, enc); err != nil {
			return err
		}
		if err := store.ReplaceRecoveryCodes(ctx, tx, p.User.ID, hashes); err != nil {
			return err
		}
		if _, err := store.ClaimTOTPStep(ctx, tx, p.User.ID, step); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "user.totp_enabled", ObjectType: "user", ObjectID: p.User.ID, IP: ip})
	})
	if err != nil {
		return nil, err
	}
	s.pending.Delete(p.User.ID)
	return codes, nil
}

// CheckTOTP re-verifies a signed-in user's TOTP code (for dangerous actions),
// with the same reuse protection as login.
func (s *Service) CheckTOTP(ctx context.Context, u *store.User, code string) (bool, error) {
	if u.TOTPSecretEnc == "" {
		return false, nil
	}
	secret, err := s.Enc.Decrypt(u.TOTPSecretEnc)
	if err != nil {
		return false, err
	}
	step, ok := MatchTOTPStep(secret, code, s.now())
	if !ok {
		return false, nil
	}
	return store.ClaimTOTPStep(ctx, s.DB, u.ID, step)
}
