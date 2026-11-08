// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// User is a users row (secrets included; never serialize it to clients directly).
type User struct {
	ID                  string
	Username            string
	DisplayName         string
	Email               string
	PasswordHash        string
	TOTPSecretEnc       string // encrypted; "" = TOTP off
	TOTPEnabledAt       *int64
	Status              string
	StatusReason        string
	StatusUntil         *int64
	Notice              string
	Role                string
	QuotaBytes          int64
	Locale              string
	ListeningVisible    bool
	ConflictThresholdMS int64
	FailedLogins        int64
	LockedUntil         *int64
	CreatedAt           int64
	UpdatedAt           int64
}

const userCols = `id, username, display_name, email, password_hash, totp_secret, totp_enabled_at,
	status, status_reason, status_until, notice, role, quota_bytes, locale, listening_visible,
	conflict_threshold_ms, failed_logins, locked_until, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var (
		u                               User
		email, pw, totp, reason, notice sql.NullString
		totpAt, until, locked           sql.NullInt64
	)
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &email, &pw, &totp, &totpAt,
		&u.Status, &reason, &until, &notice, &u.Role, &u.QuotaBytes, &u.Locale, &u.ListeningVisible,
		&u.ConflictThresholdMS, &u.FailedLogins, &locked, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	u.Email, u.PasswordHash, u.TOTPSecretEnc = email.String, pw.String, totp.String
	u.StatusReason, u.Notice = reason.String, notice.String
	u.TOTPEnabledAt, u.StatusUntil, u.LockedUntil = ptr(totpAt), ptr(until), ptr(locked)
	return &u, nil
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// NewUser holds fields for CreateUser.
type NewUser struct {
	Username, DisplayName, Email, PasswordHash string
	Status, Role                               string
	QuotaBytes                                 int64
	TOTPSecretEnc                              string
	AgeConfirmed, TermsAccepted                bool
}

// CreateUser inserts a user and returns its id. Username/email clashes → ErrConflict.
func CreateUser(ctx context.Context, q Querier, n NewUser) (string, error) {
	id, now := NewID(), NowMS()
	var totpAt, ageAt, termsAt any
	if n.TOTPSecretEnc != "" {
		totpAt = now
	}
	if n.AgeConfirmed {
		ageAt = now
	}
	if n.TermsAccepted {
		termsAt = now
	}
	display := n.DisplayName
	if display == "" {
		display = n.Username
	}
	_, err := q.Exec(ctx, `
		INSERT INTO users (id, username, display_name, email, password_hash, totp_secret, totp_enabled_at,
			status, role, quota_bytes, birthdate_confirmed_at, terms_accepted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.Username, display, nullStr(n.Email), nullStr(n.PasswordHash), nullStr(n.TOTPSecretEnc), totpAt,
		n.Status, n.Role, n.QuotaBytes, ageAt, termsAt, now, now)
	if IsUniqueViolation(err) {
		return "", ErrConflict
	}
	if err != nil {
		return "", fmt.Errorf("create user: %w", err)
	}
	return id, nil
}

// GetUser by id.
func GetUser(ctx context.Context, q Querier, id string) (*User, error) {
	return scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// GetUserByUsername, case-insensitive.
func GetUserByUsername(ctx context.Context, q Querier, username string) (*User, error) {
	return scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username) = ?`,
		strings.ToLower(username)))
}

// CountUsers returns the number of users (any status).
func CountUsers(ctx context.Context, q Querier) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// ListUsers returns users, optionally filtered by status, ordered by creation.
func ListUsers(ctx context.Context, q Querier, status string, limit int) ([]*User, error) {
	query := `SELECT ` + userCols + ` FROM users`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at, id LIMIT ?`
	args = append(args, limit)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserStatus changes status (with reason and optional end time).
func SetUserStatus(ctx context.Context, q Querier, id, status, reason string, until *int64) error {
	res, err := q.Exec(ctx, `UPDATE users SET status = ?, status_reason = ?, status_until = ?, updated_at = ? WHERE id = ?`,
		status, nullStr(reason), nullInt(until), NowMS(), id)
	return mustAffect(res, err)
}

// UserPatch holds admin-editable user fields (nil = unchanged).
type UserPatch struct {
	Role       *string
	QuotaBytes *int64
	Notice     *string
}

// PatchUser applies non-nil fields.
func PatchUser(ctx context.Context, q Querier, id string, p UserPatch) error {
	sets, args := []string{}, []any{}
	if p.Role != nil {
		sets, args = append(sets, "role = ?"), append(args, *p.Role)
	}
	if p.QuotaBytes != nil {
		sets, args = append(sets, "quota_bytes = ?"), append(args, *p.QuotaBytes)
	}
	if p.Notice != nil {
		sets, args = append(sets, "notice = ?"), append(args, nullStr(*p.Notice))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, NowMS(), id)
	res, err := q.Exec(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return mustAffect(res, err)
}

// RecordLoginFailure increments the failure counter and sets the lockout.
func RecordLoginFailure(ctx context.Context, q Querier, id string, failures int64, lockedUntil *int64) error {
	_, err := q.Exec(ctx, `UPDATE users SET failed_logins = ?, locked_until = ?, updated_at = ? WHERE id = ?`,
		failures, nullInt(lockedUntil), NowMS(), id)
	return err
}

// RecordLoginSuccess clears the failure counter and stamps last login.
func RecordLoginSuccess(ctx context.Context, q Querier, id string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id)
	return err
}

// SetTOTP stores an encrypted TOTP secret ("" disables TOTP).
func SetTOTP(ctx context.Context, q Querier, id, secretEnc string) error {
	var at any
	if secretEnc != "" {
		at = NowMS()
	}
	res, err := q.Exec(ctx, `UPDATE users SET totp_secret = ?, totp_enabled_at = ?, updated_at = ? WHERE id = ?`,
		nullStr(secretEnc), at, NowMS(), id)
	return mustAffect(res, err)
}

// ClearNotice removes the one-time moderator notice.
func ClearNotice(ctx context.Context, q Querier, id string) error {
	_, err := q.Exec(ctx, `UPDATE users SET notice = NULL, updated_at = ? WHERE id = ?`, NowMS(), id)
	return err
}

// ── Recovery codes ──

// ReplaceRecoveryCodes deletes old codes and stores new hashes.
func ReplaceRecoveryCodes(ctx context.Context, q Querier, userID string, hashes []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	now := NowMS()
	for _, h := range hashes {
		if _, err := q.Exec(ctx, `INSERT INTO recovery_codes (id, user_id, code_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			NewID(), userID, h, now, now); err != nil {
			return err
		}
	}
	return nil
}

// UseRecoveryCode marks a matching unused code as used; false if none matched.
func UseRecoveryCode(ctx context.Context, q Querier, userID, hash string) (bool, error) {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE recovery_codes SET used_at = ?, updated_at = ?
		WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`, now, now, userID, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ── Permissions and tags ──

// PermissionOverrides are explicit per-user values; nil = not set.
type PermissionOverrides struct {
	CanUpload, CanEditMetadata, CanDeleteOwn, CanShare, CanReview, CanPostPublic *bool
}

// GetPermissionOverrides returns the user's explicit overrides (all nil if none).
func GetPermissionOverrides(ctx context.Context, q Querier, userID string) (PermissionOverrides, error) {
	var vals [6]sql.NullBool
	err := q.QueryRow(ctx, `SELECT can_upload, can_edit_metadata, can_delete_own, can_share, can_review, can_post_public
		FROM user_permissions WHERE user_id = ?`, userID).
		Scan(&vals[0], &vals[1], &vals[2], &vals[3], &vals[4], &vals[5])
	if err != nil {
		if notFound(err) == ErrNotFound {
			return PermissionOverrides{}, nil
		}
		return PermissionOverrides{}, err
	}
	b := func(n sql.NullBool) *bool {
		if !n.Valid {
			return nil
		}
		v := n.Bool
		return &v
	}
	return PermissionOverrides{b(vals[0]), b(vals[1]), b(vals[2]), b(vals[3]), b(vals[4]), b(vals[5])}, nil
}

// TagOverrides lists capability overrides from every user tag assigned to the user.
func TagOverrides(ctx context.Context, q Querier, userID string) ([]map[string]bool, bool, error) {
	rows, err := q.Query(ctx, `SELECT t.capability_overrides, t.require_totp FROM user_tags t
		JOIN user_tag_assignments a ON a.user_tag_id = t.id WHERE a.user_id = ?`, userID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var (
		out         []map[string]bool
		requireTOTP bool
	)
	for rows.Next() {
		var (
			raw string
			req bool
		)
		if err := rows.Scan(&raw, &req); err != nil {
			return nil, false, err
		}
		m := map[string]bool{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, false, fmt.Errorf("user tag overrides: %w", err)
		}
		out = append(out, m)
		requireTOTP = requireTOTP || req
	}
	return out, requireTOTP, rows.Err()
}

func mustAffect(res sql.Result, err error) error {
	if err != nil {
		return notFound(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimTOTPStep records step as the user's last accepted TOTP step, only if
// it is newer than the stored one. False means the code was already used
// (or an older one), so the login must be refused. Atomic: two concurrent
// requests with the same code can't both win.
func ClaimTOTPStep(ctx context.Context, q Querier, userID string, step int64) (bool, error) {
	res, err := q.Exec(ctx, `UPDATE users SET totp_last_step = ?, updated_at = ?
		WHERE id = ? AND (totp_last_step IS NULL OR totp_last_step < ?)`, step, NowMS(), userID, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
