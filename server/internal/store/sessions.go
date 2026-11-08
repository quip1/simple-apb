// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Session is one device session (refresh token holder).
type Session struct {
	ID               string
	UserID           string
	DeviceID         string
	DeviceName       string
	Platform         string
	AppVersion       string
	IP               string
	RefreshTokenHash string
	PrevRefreshHash  string
	ExpiresAt        int64
	LastSeenAt       int64
	RevokedAt        *int64
	CreatedAt        int64
}

const sessionCols = `id, user_id, device_id, device_name, platform, app_version, ip, refresh_token_hash,
	prev_refresh_hash, expires_at, last_seen_at, revoked_at, created_at`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var (
		s        Session
		ip, prev sql.NullString
		revoked  sql.NullInt64
	)
	if err := row.Scan(&s.ID, &s.UserID, &s.DeviceID, &s.DeviceName, &s.Platform, &s.AppVersion, &ip,
		&s.RefreshTokenHash, &prev, &s.ExpiresAt, &s.LastSeenAt, &revoked, &s.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	s.IP, s.PrevRefreshHash, s.RevokedAt = ip.String, prev.String, ptr(revoked)
	return &s, nil
}

// CreateSession inserts a session. An existing live session for the same
// user+device is revoked first (one session per device).
func CreateSession(ctx context.Context, q Querier, s *Session) error {
	now := NowMS()
	if _, err := q.Exec(ctx, `UPDATE sessions SET revoked_at = ?, updated_at = ?
		WHERE user_id = ? AND device_id = ? AND revoked_at IS NULL`, now, now, s.UserID, s.DeviceID); err != nil {
		return err
	}
	s.ID, s.CreatedAt, s.LastSeenAt = NewID(), now, now
	_, err := q.Exec(ctx, `INSERT INTO sessions (id, user_id, device_id, device_name, platform, app_version, ip,
		refresh_token_hash, expires_at, last_seen_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.DeviceID, s.DeviceName, s.Platform, s.AppVersion, nullStr(s.IP),
		s.RefreshTokenHash, s.ExpiresAt, now, now, now)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSession by id.
func GetSession(ctx context.Context, q Querier, id string) (*Session, error) {
	return scanSession(q.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
}

// FindSessionByRefresh looks up a session whose current OR previous refresh
// hash matches. The caller detects reuse by comparing which one matched.
func FindSessionByRefresh(ctx context.Context, q Querier, hash string) (*Session, error) {
	return scanSession(q.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE refresh_token_hash = ? OR prev_refresh_hash = ? ORDER BY created_at DESC LIMIT 1`, hash, hash))
}

// RotateRefresh swaps in a new refresh hash, keeping the old as "previous".
// It only succeeds if the current hash still matches (guards concurrent refresh).
func RotateRefresh(ctx context.Context, q Querier, id, oldHash, newHash string, expiresAt int64, ip string) (bool, error) {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE sessions SET prev_refresh_hash = refresh_token_hash, refresh_token_hash = ?,
		expires_at = ?, last_seen_at = ?, ip = ?, updated_at = ?
		WHERE id = ? AND refresh_token_hash = ? AND revoked_at IS NULL`,
		newHash, expiresAt, now, nullStr(ip), now, id, oldHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// TouchSession updates last_seen_at.
func TouchSession(ctx context.Context, q Querier, id string) error {
	_, err := q.Exec(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, NowMS(), id)
	return err
}

// RevokeSession marks one session revoked. userID "" = any user (admin).
func RevokeSession(ctx context.Context, q Querier, id, userID string) error {
	now := NowMS()
	query := `UPDATE sessions SET revoked_at = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL`
	args := []any{now, now, id}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := q.Exec(ctx, query, args...)
	return mustAffect(res, err)
}

// RevokeUserSessions revokes every live session of a user (ban, password change).
func RevokeUserSessions(ctx context.Context, q Querier, userID string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `UPDATE sessions SET revoked_at = ?, updated_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now, now, userID)
	return err
}

// ListUserSessions returns live sessions, most recently seen first.
func ListUserSessions(ctx context.Context, q Querier, userID string) ([]*Session, error) {
	rows, err := q.Query(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ? ORDER BY last_seen_at DESC`, userID, NowMS())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
