// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Admin-panel data access (Phase 2 milestone 4).

// ── user tags ──

// UserTag is a user_tags row with its member count.
type UserTag struct {
	ID, Name, Color string
	RequireTOTP     bool
	Overrides       map[string]bool
	Members         int64
}

const userTagCols = `t.id, t.name, t.color, t.require_totp, t.capability_overrides,
	(SELECT COUNT(*) FROM user_tag_assignments a WHERE a.user_tag_id = t.id)`

func scanUserTag(row interface{ Scan(...any) error }) (*UserTag, error) {
	var (
		t     UserTag
		color sql.NullString
		raw   string
	)
	if err := row.Scan(&t.ID, &t.Name, &color, &t.RequireTOTP, &raw, &t.Members); err != nil {
		return nil, notFound(err)
	}
	t.Color = color.String
	t.Overrides = map[string]bool{}
	if err := json.Unmarshal([]byte(raw), &t.Overrides); err != nil {
		return nil, fmt.Errorf("user tag overrides: %w", err)
	}
	return &t, nil
}

func scanUserTags(rows *sql.Rows, err error) ([]*UserTag, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*UserTag
	for rows.Next() {
		t, err := scanUserTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListUserTags returns all user tags by name.
func ListUserTags(ctx context.Context, q Querier) ([]*UserTag, error) {
	return scanUserTags(q.Query(ctx, `SELECT `+userTagCols+` FROM user_tags t ORDER BY lower(t.name)`))
}

// TagsOfUser returns the tags assigned to a user.
func TagsOfUser(ctx context.Context, q Querier, userID string) ([]*UserTag, error) {
	return scanUserTags(q.Query(ctx, `SELECT `+userTagCols+` FROM user_tags t
		JOIN user_tag_assignments ua ON ua.user_tag_id = t.id WHERE ua.user_id = ? ORDER BY lower(t.name)`, userID))
}

// GetUserTag returns one tag.
func GetUserTag(ctx context.Context, q Querier, id string) (*UserTag, error) {
	return scanUserTag(q.QueryRow(ctx, `SELECT `+userTagCols+` FROM user_tags t WHERE t.id = ?`, id))
}

func overridesJSON(m map[string]bool) (string, error) {
	if m == nil {
		m = map[string]bool{}
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// CreateUserTag inserts a tag; a name clash → ErrConflict.
func CreateUserTag(ctx context.Context, q Querier, name, color string, requireTOTP bool, overrides map[string]bool) (string, error) {
	raw, err := overridesJSON(overrides)
	if err != nil {
		return "", err
	}
	id, now := NewID(), NowMS()
	_, err = q.Exec(ctx, `INSERT INTO user_tags (id, name, color, capability_overrides, require_totp, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, name, nullStr(color), raw, requireTOTP, now, now)
	if IsUniqueViolation(err) {
		return "", ErrConflict
	}
	return id, err
}

// UserTagPatch holds the fields to change.
type UserTagPatch struct {
	Name, Color *string
	RequireTOTP *bool
	Overrides   map[string]bool // nil = unchanged
}

// UpdateUserTag applies a patch; a name clash → ErrConflict.
func UpdateUserTag(ctx context.Context, q Querier, id string, p UserTagPatch) error {
	sets, args := []string{}, []any{}
	add := func(col string, v any) { sets, args = append(sets, col+" = ?"), append(args, v) }
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.Color != nil {
		add("color", nullStr(*p.Color))
	}
	if p.RequireTOTP != nil {
		add("require_totp", *p.RequireTOTP)
	}
	if p.Overrides != nil {
		raw, err := overridesJSON(p.Overrides)
		if err != nil {
			return err
		}
		add("capability_overrides", raw)
	}
	add("updated_at", NowMS())
	res, err := q.Exec(ctx, `UPDATE user_tags SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...)
	if IsUniqueViolation(err) {
		return ErrConflict
	}
	return mustAffect(res, err)
}

// DeleteUserTag removes a tag (and its assignments).
func DeleteUserTag(ctx context.Context, q Querier, id string) error {
	return mustAffect(q.Exec(ctx, `DELETE FROM user_tags WHERE id = ?`, id))
}

// AssignUserTag gives a user a tag (idempotent).
func AssignUserTag(ctx context.Context, q Querier, userID, tagID, by string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO user_tag_assignments (id, user_id, user_tag_id, assigned_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (user_id, user_tag_id) DO NOTHING`, NewID(), userID, tagID, nullStr(by), now, now)
	return err
}

// UnassignUserTag takes a tag off a user.
func UnassignUserTag(ctx context.Context, q Querier, userID, tagID string) error {
	return mustAffect(q.Exec(ctx, `DELETE FROM user_tag_assignments WHERE user_id = ? AND user_tag_id = ?`, userID, tagID))
}

// ── permission overrides ──

// SetPermissionOverrides replaces a user's explicit overrides (nil = not set).
func SetPermissionOverrides(ctx context.Context, q Querier, userID string, p PermissionOverrides) error {
	b := func(v *bool) any {
		if v == nil {
			return nil
		}
		return *v
	}
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO user_permissions (user_id, can_upload, can_edit_metadata, can_delete_own, can_share,
			can_review, can_post_public, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET can_upload = excluded.can_upload, can_edit_metadata = excluded.can_edit_metadata,
			can_delete_own = excluded.can_delete_own, can_share = excluded.can_share, can_review = excluded.can_review,
			can_post_public = excluded.can_post_public, updated_at = excluded.updated_at`,
		userID, b(p.CanUpload), b(p.CanEditMetadata), b(p.CanDeleteOwn), b(p.CanShare), b(p.CanReview), b(p.CanPostPublic), now, now)
	return err
}

// LastLogin returns when a user last signed in (nil if never).
func LastLogin(ctx context.Context, q Querier, userID string) (*int64, error) {
	var at sql.NullInt64
	if err := q.QueryRow(ctx, `SELECT last_login_at FROM users WHERE id = ?`, userID).Scan(&at); err != nil {
		return nil, notFound(err)
	}
	return ptr(at), nil
}

// ── groups ──

// GroupsOfUser returns the groups a user belongs to.
func GroupsOfUser(ctx context.Context, q Querier, userID string) ([]*Group, error) {
	rows, err := q.Query(ctx, `SELECT g.id, g.name, g.builtin, g.high_priority, g.created_at,
		(SELECT COUNT(*) FROM group_members m2 WHERE m2.group_id = g.id)
		FROM groups g JOIN group_members m ON m.group_id = g.id WHERE m.user_id = ? ORDER BY lower(g.name)`, userID)
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

// UpdateGroup renames a group or changes its priority; a name clash → ErrConflict.
func UpdateGroup(ctx context.Context, q Querier, id string, name *string, highPriority *bool) error {
	sets, args := []string{}, []any{}
	if name != nil {
		sets, args = append(sets, "name = ?"), append(args, *name)
	}
	if highPriority != nil {
		sets, args = append(sets, "high_priority = ?"), append(args, *highPriority)
	}
	sets, args = append(sets, "updated_at = ?"), append(args, NowMS())
	res, err := q.Exec(ctx, `UPDATE groups SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...)
	if IsUniqueViolation(err) {
		return ErrConflict
	}
	return mustAffect(res, err)
}

// UserRef is a user's id and names.
type UserRef struct{ ID, Username, DisplayName string }

// ListGroupMembers returns a group's members by username.
func ListGroupMembers(ctx context.Context, q Querier, groupID string) ([]UserRef, error) {
	rows, err := q.Query(ctx, `SELECT u.id, u.username, u.display_name FROM users u
		JOIN group_members m ON m.user_id = u.id WHERE m.group_id = ? ORDER BY lower(u.username)`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UserRef
	for rows.Next() {
		var r UserRef
		if err := rows.Scan(&r.ID, &r.Username, &r.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── takedowns (brief §6.5 rule 8) ──

// Takedown is an active takedown.
type Takedown struct {
	ID, Reason, TakenBy string
	TakenAt             int64
}

// ActiveTakedown returns the book's active takedown, or ErrNotFound.
func ActiveTakedown(ctx context.Context, q Querier, bookID string) (*Takedown, error) {
	var (
		t  Takedown
		by sql.NullString
	)
	err := q.QueryRow(ctx, `SELECT id, reason, taken_by, taken_at FROM takedowns
		WHERE book_id = ? AND lifted_at IS NULL ORDER BY taken_at DESC LIMIT 1`, bookID).Scan(&t.ID, &t.Reason, &by, &t.TakenAt)
	if err != nil {
		return nil, notFound(err)
	}
	t.TakenBy = by.String
	return &t, nil
}

// CreateTakedown records a takedown.
func CreateTakedown(ctx context.Context, q Querier, bookID, reason, by string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO takedowns (id, book_id, reason, taken_by, taken_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, NewID(), bookID, reason, nullStr(by), now, now, now)
	return err
}

// LiftTakedown lifts the book's active takedown (ErrNotFound if none).
func LiftTakedown(ctx context.Context, q Querier, bookID string) error {
	now := NowMS()
	return mustAffect(q.Exec(ctx, `UPDATE takedowns SET lifted_at = ?, updated_at = ? WHERE book_id = ? AND lifted_at IS NULL`,
		now, now, bookID))
}

// ── audit log ──

// AuditFilter selects audit entries.
type AuditFilter struct {
	ActorID, Action, ObjectType, ObjectID string
	FromMS, ToMS                          int64 // 0 = open
	Limit, Offset                         int
}

// AuditRow is a stored audit entry with the actor's username.
type AuditRow struct {
	ID, ActorID, ActorName, Action, ObjectType, ObjectID, Details, IP string
	At                                                                int64
}

// ListAudit returns entries newest first.
func ListAudit(ctx context.Context, q Querier, f AuditFilter) ([]*AuditRow, error) {
	where, args := []string{"1 = 1"}, []any{}
	if f.ActorID != "" {
		where, args = append(where, "a.actor_id = ?"), append(args, f.ActorID)
	}
	if f.Action != "" {
		if strings.HasSuffix(f.Action, ".") {
			where, args = append(where, `a.action LIKE ? ESCAPE '\'`), append(args, likePrefix(f.Action))
		} else {
			where, args = append(where, "a.action = ?"), append(args, f.Action)
		}
	}
	if f.ObjectType != "" {
		where, args = append(where, "a.object_type = ?"), append(args, f.ObjectType)
	}
	if f.ObjectID != "" {
		where, args = append(where, "a.object_id = ?"), append(args, f.ObjectID)
	}
	if f.FromMS > 0 {
		where, args = append(where, "a.at >= ?"), append(args, f.FromMS)
	}
	if f.ToMS > 0 {
		where, args = append(where, "a.at < ?"), append(args, f.ToMS)
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := q.Query(ctx, `SELECT a.id, a.actor_id, u.username, a.action, a.object_type, a.object_id, a.details, a.ip, a.at
		FROM audit_log a LEFT JOIN users u ON CAST(u.id AS TEXT) = a.actor_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY a.at DESC, a.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*AuditRow
	for rows.Next() {
		var (
			r                                 AuditRow
			actor, name, otype, oid, ip, dets sql.NullString
		)
		if err := rows.Scan(&r.ID, &actor, &name, &r.Action, &otype, &oid, &dets, &ip, &r.At); err != nil {
			return nil, err
		}
		r.ActorID, r.ActorName, r.ObjectType, r.ObjectID, r.Details, r.IP = actor.String, name.String, otype.String, oid.String, dets.String, ip.String
		out = append(out, &r)
	}
	return out, rows.Err()
}

func likePrefix(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s) + "%"
}

// ── dashboard counts ──

// InboxCounts are the open items of the moderation inbox.
type InboxCounts struct{ Signups, Reviews, Reports int64 }

// CountInbox counts pending sign-ups, review-queue items and open reports.
func CountInbox(ctx context.Context, q Querier) (InboxCounts, error) {
	var c InboxCounts
	err := q.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM signup_requests WHERE status = 'pending'),
		(SELECT COUNT(*) FROM review_queue WHERE status = 'pending'),
		(SELECT COUNT(*) FROM reports WHERE status = 'open')`).Scan(&c.Signups, &c.Reviews, &c.Reports)
	return c, err
}

// JobQueueDepth counts running jobs and queued jobs that are due (scheduled
// future runs such as the nightly backup don't count).
func JobQueueDepth(ctx context.Context, q Querier) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM jobs WHERE status = 'running'
		OR (status = 'queued' AND run_after <= ?)`, NowMS()).Scan(&n)
	return n, err
}

// DBSize returns the database size in bytes.
func DBSize(ctx context.Context, db *DB) (int64, error) {
	var n int64
	var err error
	if db.Dialect() == Postgres {
		err = db.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&n)
	} else {
		err = db.QueryRow(ctx, `SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&n)
	}
	return n, err
}

// ── growth ──

// GrowthAt counts users and books (with storage) as of a moment.
func GrowthAt(ctx context.Context, q Querier, atMS int64) (users, books, storage int64, err error) {
	err = q.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM users WHERE created_at < ? AND status NOT IN ('pending', 'deleted')),
		(SELECT COUNT(*) FROM books WHERE created_at < ? AND status <> 'trashed'),
		(SELECT COALESCE(SUM(size_bytes), 0) FROM books WHERE created_at < ? AND status <> 'trashed')`,
		atMS, atMS, atMS).Scan(&users, &books, &storage)
	return users, books, storage, err
}

// AllListenEvents returns every user's events that ended in [fromMS, toMS).
func AllListenEvents(ctx context.Context, q Querier, fromMS, toMS int64) ([]ListenEvent, error) {
	rows, err := q.Query(ctx, `SELECT book_id, from_ms, to_ms, started_at, ended_at FROM progress_events
		WHERE ended_at >= ? AND ended_at < ?`, fromMS, toMS)
	if err != nil {
		return nil, fmt.Errorf("listen events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ListenEvent
	for rows.Next() {
		var e ListenEvent
		if err := rows.Scan(&e.BookID, &e.FromMS, &e.ToMS, &e.StartedAt, &e.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
