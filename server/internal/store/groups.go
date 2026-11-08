// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// PublicGroupName is the built-in group every approved user joins.
const PublicGroupName = "Public"

// Group is a named set of users.
type Group struct {
	ID           string
	Name         string
	Builtin      bool
	HighPriority bool
	Members      int64
	CreatedAt    int64
}

// EnsurePublicGroup creates the built-in Public group if missing and returns its id.
func EnsurePublicGroup(ctx context.Context, q Querier) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM groups WHERE builtin = ? AND lower(name) = ?`, true, "public").Scan(&id)
	if err == nil {
		return id, nil
	}
	if notFound(err) != ErrNotFound {
		return "", err
	}
	id, now := NewID(), NowMS()
	_, err = q.Exec(ctx, `INSERT INTO groups (id, name, builtin, high_priority, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, PublicGroupName, true, false, now, now)
	return id, err
}

// CreateGroup inserts a non-builtin group.
func CreateGroup(ctx context.Context, q Querier, name string, highPriority bool) (string, error) {
	id, now := NewID(), NowMS()
	_, err := q.Exec(ctx, `INSERT INTO groups (id, name, builtin, high_priority, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, false, highPriority, now, now)
	if IsUniqueViolation(err) {
		return "", ErrConflict
	}
	return id, err
}

// GetGroup by id, with member count.
func GetGroup(ctx context.Context, q Querier, id string) (*Group, error) {
	var g Group
	err := q.QueryRow(ctx, `SELECT g.id, g.name, g.builtin, g.high_priority, g.created_at,
		(SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id) FROM groups g WHERE g.id = ?`, id).
		Scan(&g.ID, &g.Name, &g.Builtin, &g.HighPriority, &g.CreatedAt, &g.Members)
	if err != nil {
		return nil, notFound(err)
	}
	return &g, nil
}

// ListGroups returns all groups by name.
func ListGroups(ctx context.Context, q Querier) ([]*Group, error) {
	rows, err := q.Query(ctx, `SELECT g.id, g.name, g.builtin, g.high_priority, g.created_at,
		(SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id) FROM groups g ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Builtin, &g.HighPriority, &g.CreatedAt, &g.Members); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// DeleteGroup removes a non-builtin group.
func DeleteGroup(ctx context.Context, q Querier, id string) error {
	res, err := q.Exec(ctx, `DELETE FROM groups WHERE id = ? AND builtin = ?`, id, false)
	return mustAffect(res, err)
}

// AddGroupMember adds a user to a group (idempotent).
func AddGroupMember(ctx context.Context, q Querier, groupID, userID string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO group_members (id, group_id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (group_id, user_id) DO NOTHING`, NewID(), groupID, userID, now, now)
	if err != nil {
		return fmt.Errorf("add group member: %w", err)
	}
	return nil
}

// RemoveGroupMember removes a user from a group.
func RemoveGroupMember(ctx context.Context, q Querier, groupID, userID string) error {
	res, err := q.Exec(ctx, `DELETE FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID)
	return mustAffect(res, err)
}

// UserGroupIDs returns the ids of the groups a user belongs to.
func UserGroupIDs(ctx context.Context, q Querier, userID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT group_id FROM group_members WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ── Sign-up requests ──

// SignupRequest is a pending account request.
type SignupRequest struct {
	ID, UserID, Username, Email, Message, Status string
	CreatedAt                                    int64
	DecidedAt                                    *int64
}

// CreateSignupRequest records a request for a pending user.
func CreateSignupRequest(ctx context.Context, q Querier, userID, username, email, message, ip string) (string, error) {
	id, now := NewID(), NowMS()
	_, err := q.Exec(ctx, `INSERT INTO signup_requests (id, user_id, username, email, message, ip, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, id, userID, username, nullStr(email), nullStr(message), nullStr(ip), now, now)
	return id, err
}

// ListSignupRequests by status ("" = all), oldest first.
func ListSignupRequests(ctx context.Context, q Querier, status string) ([]*SignupRequest, error) {
	query := `SELECT id, user_id, username, email, message, status, created_at, decided_at FROM signup_requests`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	rows, err := q.Query(ctx, query+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*SignupRequest
	for rows.Next() {
		var (
			r              SignupRequest
			email, message sql.NullString
			decided        sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &email, &message, &r.Status, &r.CreatedAt, &decided); err != nil {
			return nil, err
		}
		r.Email, r.Message, r.DecidedAt = email.String, message.String, ptr(decided)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// GetSignupRequest by id.
func GetSignupRequest(ctx context.Context, q Querier, id string) (*SignupRequest, error) {
	var (
		r              SignupRequest
		email, message sql.NullString
		decided        sql.NullInt64
	)
	err := q.QueryRow(ctx, `SELECT id, user_id, username, email, message, status, created_at, decided_at
		FROM signup_requests WHERE id = ?`, id).
		Scan(&r.ID, &r.UserID, &r.Username, &email, &message, &r.Status, &r.CreatedAt, &decided)
	if err != nil {
		return nil, notFound(err)
	}
	r.Email, r.Message, r.DecidedAt = email.String, message.String, ptr(decided)
	return &r, nil
}

// DecideSignupRequest sets approved/denied, only if still pending.
func DecideSignupRequest(ctx context.Context, q Querier, id, status, decidedBy string) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE signup_requests SET status = ?, decided_by = ?, decided_at = ?, updated_at = ?
		WHERE id = ? AND status = 'pending'`, status, decidedBy, now, now, id)
	return mustAffect(res, err)
}
