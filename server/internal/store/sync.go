// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ── Change log (ADR-0005) ──

// Change entities.
const (
	EntityBook         = "book"          // visibility/metadata of a book changed (user may be NULL = everyone)
	EntityLibraryEntry = "library_entry" // the user's own view of a book changed
	EntityProgress     = "progress"
	EntityBookmark     = "bookmark"
	EntityCollection   = "collection"
)

// RecordChange appends to the change log. userID "" means "everyone who can
// see the object". Call it in the same transaction as the change.
func RecordChange(ctx context.Context, q Querier, userID, entity, entityID, op string) error {
	_, err := q.Exec(ctx, `INSERT INTO sync_changes (user_id, entity, entity_id, op, at) VALUES (?, ?, ?, ?, ?)`,
		nullStr(userID), entity, entityID, op, NowMS())
	if err != nil {
		return fmt.Errorf("record change: %w", err)
	}
	return nil
}

// RecordBookChange notes that a book's visibility or metadata changed for everyone.
func RecordBookChange(ctx context.Context, q Querier, bookID string) error {
	return RecordChange(ctx, q, "", EntityBook, bookID, "upsert")
}

// RecordGroupChange notes, for one user, every book distributed to a group
// they just joined or left.
func RecordGroupChange(ctx context.Context, q Querier, groupID, userID string) error {
	// Select then insert (instead of INSERT … SELECT ?) so parameter types are
	// unambiguous on both dialects.
	rows, err := q.Query(ctx, `SELECT DISTINCT book_id FROM distributions
		WHERE target_type = 'group' AND target_id = ? AND revoked_at IS NULL`, groupID)
	if err != nil {
		return err
	}
	var books []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			_ = rows.Close()
			return err
		}
		books = append(books, b)
	}
	_ = rows.Close()
	for _, b := range books {
		if err := RecordChange(ctx, q, userID, EntityBook, b, "upsert"); err != nil {
			return err
		}
	}
	return nil
}

// Change is one change-log row.
type Change struct {
	Seq              int64
	Entity, EntityID string
	Op               string
}

// ChangesSince returns changes after seq relevant to the user, oldest first.
func ChangesSince(ctx context.Context, q Querier, userID string, since int64, limit int) ([]Change, error) {
	rows, err := q.Query(ctx, `SELECT seq, entity, entity_id, op FROM sync_changes
		WHERE seq > ? AND (user_id = ? OR user_id IS NULL) ORDER BY seq LIMIT ?`, since, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Seq, &c.Entity, &c.EntityID, &c.Op); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MaxSeq is the latest change-log position.
func MaxSeq(ctx context.Context, q Querier) (int64, error) {
	var n sql.NullInt64
	err := q.QueryRow(ctx, `SELECT MAX(seq) FROM sync_changes`).Scan(&n)
	return n.Int64, err
}

// UsersChangedSince returns users with changes after seq, and whether any
// change was for everyone (used by the /ws notifier).
func UsersChangedSince(ctx context.Context, q Querier, since int64) (map[string]bool, bool, int64, error) {
	rows, err := q.Query(ctx, `SELECT seq, user_id FROM sync_changes WHERE seq > ? ORDER BY seq LIMIT 5000`, since)
	if err != nil {
		return nil, false, since, err
	}
	defer func() { _ = rows.Close() }()
	users, all, max := map[string]bool{}, false, since
	for rows.Next() {
		var (
			seq int64
			u   sql.NullString
		)
		if err := rows.Scan(&seq, &u); err != nil {
			return nil, false, since, err
		}
		if u.Valid {
			users[u.String] = true
		} else {
			all = true
		}
		max = seq
	}
	return users, all, max, rows.Err()
}

// PruneChanges deletes change-log rows older than cutoff and returns the new
// low-water mark (cursors below it get 410 and must do a full sync).
func PruneChanges(ctx context.Context, q Querier, enc Encrypter, cutoffMS int64) (int64, error) {
	var maxDel sql.NullInt64
	if err := q.QueryRow(ctx, `SELECT MAX(seq) FROM sync_changes WHERE at < ?`, cutoffMS).Scan(&maxDel); err != nil {
		return 0, err
	}
	if !maxDel.Valid {
		return LowWater(ctx, q, enc)
	}
	if _, err := q.Exec(ctx, `DELETE FROM sync_changes WHERE seq <= ?`, maxDel.Int64); err != nil {
		return 0, err
	}
	return maxDel.Int64, SetSetting(ctx, q, enc, "sync_low_water_seq", maxDel.Int64, false)
}

// LowWater is the highest pruned seq (0 if never pruned).
func LowWater(ctx context.Context, q Querier, enc Encrypter) (int64, error) {
	var n int64
	err := GetSetting(ctx, q, enc, "sync_low_water_seq", &n)
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	return n, err
}

// ── Progress ──

// ProgressRow is a user's position in a book.
type ProgressRow struct {
	BookID, DeviceID, DeviceName string
	PositionMS                   int64
	ClientTS, UpdatedAt          int64
	Speed                        float64
	FinishedAt                   *int64
}

const progressCols = `p.book_id, p.device_id, COALESCE(s.device_name, ''), p.position_ms, p.client_ts, p.updated_at, p.speed, p.finished_at`

// The device name comes from the user's most recent session on that device.
const progressFrom = ` FROM progress p LEFT JOIN (
		SELECT device_id, user_id, MAX(device_name) AS device_name FROM sessions GROUP BY device_id, user_id
	) s ON s.device_id = p.device_id AND s.user_id = p.user_id`

func scanProgress(row interface{ Scan(...any) error }) (*ProgressRow, error) {
	var (
		p   ProgressRow
		fin sql.NullInt64
	)
	if err := row.Scan(&p.BookID, &p.DeviceID, &p.DeviceName, &p.PositionMS, &p.ClientTS, &p.UpdatedAt, &p.Speed, &fin); err != nil {
		return nil, notFound(err)
	}
	p.FinishedAt = ptr(fin)
	return &p, nil
}

// GetProgress returns the user's progress for a book.
func GetProgress(ctx context.Context, q Querier, userID, bookID string) (*ProgressRow, error) {
	return scanProgress(q.QueryRow(ctx, `SELECT `+progressCols+progressFrom+` WHERE p.user_id = ? AND p.book_id = ?`, userID, bookID))
}

// ListProgress returns all of the user's progress records.
func ListProgress(ctx context.Context, q Querier, userID string) ([]*ProgressRow, error) {
	rows, err := q.Query(ctx, `SELECT `+progressCols+progressFrom+` WHERE p.user_id = ? ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*ProgressRow
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProgressWrite is a write to apply.
type ProgressWrite struct {
	UserID, BookID, DeviceID string
	PositionMS, ClientTS     int64
	Speed                    float64
	// Finished: nil keeps the current state; true marks finished now; false clears it.
	Finished *bool
}

// UpsertProgress stores a write.
func UpsertProgress(ctx context.Context, q Querier, w ProgressWrite) error {
	now := NowMS()
	finishedExpr := "progress.finished_at"
	var fin any
	if w.Finished != nil {
		finishedExpr = "excluded.finished_at"
		if *w.Finished {
			// When it was finished on the device (an offline write arrives
			// later), never in the future.
			fin = min(w.ClientTS, now)
		}
	}
	_, err := q.Exec(ctx, `INSERT INTO progress (id, user_id, book_id, position_ms, device_id, client_ts, speed, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET position_ms = excluded.position_ms, device_id = excluded.device_id,
			client_ts = excluded.client_ts, speed = excluded.speed, finished_at = `+finishedExpr+`, updated_at = excluded.updated_at`,
		NewID(), w.UserID, w.BookID, w.PositionMS, w.DeviceID, w.ClientTS, w.Speed, fin, now, now)
	if err != nil {
		return fmt.Errorf("upsert progress: %w", err)
	}
	return nil
}

// AddProgressEvent appends listening history.
func AddProgressEvent(ctx context.Context, q Querier, userID, bookID, deviceID string, fromMS, toMS, startedAt, endedAt int64) error {
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO progress_events (id, user_id, book_id, device_id, from_ms, to_ms, started_at, ended_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, NewID(), userID, bookID, deviceID, fromMS, toMS, startedAt, endedAt, now, now)
	return err
}

// CreateConflict records a 409 and returns its id.
func CreateConflict(ctx context.Context, q Querier, userID, bookID string, serverPos int64, serverDev string, inPos int64, inDev string) (string, error) {
	id, now := NewID(), NowMS()
	_, err := q.Exec(ctx, `INSERT INTO progress_conflicts (id, user_id, book_id, server_position, server_device_id, incoming_position,
		incoming_device_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, bookID, serverPos, serverDev, inPos, inDev, now, now)
	return id, err
}

// ResolveConflict marks an open conflict resolved with the chosen position.
// Returns ErrNotFound if it doesn't exist, isn't the user's, or was resolved.
func ResolveConflict(ctx context.Context, q Querier, id, userID, bookID string, chosen int64) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE progress_conflicts SET chosen_position = ?, resolved_at = ?, updated_at = ?
		WHERE id = ? AND user_id = ? AND book_id = ? AND resolved_at IS NULL`, chosen, now, now, id, userID, bookID)
	return mustAffect(res, err)
}

// ── Bookmarks ──

// Bookmark is a saved position with a note.
type Bookmark struct {
	ID, BookID, Note string
	PositionMS       int64
	UpdatedAt        int64
	DeletedAt        *int64
}

func scanBookmark(row interface{ Scan(...any) error }) (*Bookmark, error) {
	var (
		b    Bookmark
		note sql.NullString
		del  sql.NullInt64
	)
	if err := row.Scan(&b.ID, &b.BookID, &b.PositionMS, &note, &b.UpdatedAt, &del); err != nil {
		return nil, notFound(err)
	}
	b.Note, b.DeletedAt = note.String, ptr(del)
	return &b, nil
}

const bookmarkCols = `id, book_id, position_ms, note, updated_at, deleted_at`

// GetBookmark returns a user's bookmark, including tombstoned ones.
func GetBookmark(ctx context.Context, q Querier, userID, id string) (*Bookmark, error) {
	return scanBookmark(q.QueryRow(ctx, `SELECT `+bookmarkCols+` FROM bookmarks WHERE id = ? AND user_id = ?`, id, userID))
}

// CreateBookmark inserts (idempotent on id: an existing bookmark is returned as-is).
func CreateBookmark(ctx context.Context, q Querier, userID string, b *Bookmark) (*Bookmark, bool, error) {
	if b.ID == "" {
		b.ID = NewID()
	} else if existing, err := GetBookmark(ctx, q, userID, b.ID); err == nil {
		return existing, false, nil
	}
	_, err := q.Exec(ctx, `INSERT INTO bookmarks (id, user_id, book_id, position_ms, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, b.ID, userID, b.BookID, b.PositionMS, nullStr(b.Note), NowMS(), b.UpdatedAt)
	if IsUniqueViolation(err) {
		return nil, false, ErrConflict // id taken by another user
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// UpdateBookmark applies an edit if its clock is not older than the stored one (LWW).
func UpdateBookmark(ctx context.Context, q Querier, userID, id string, pos *int64, note *string, clock int64) (bool, error) {
	res, err := q.Exec(ctx, `UPDATE bookmarks SET position_ms = COALESCE(?, position_ms), note = COALESCE(?, note), updated_at = ?
		WHERE id = ? AND user_id = ? AND deleted_at IS NULL AND updated_at <= ?`, nullInt(pos), note, clock, id, userID, clock)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeleteBookmark tombstones a bookmark.
func DeleteBookmark(ctx context.Context, q Querier, userID, id string) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE bookmarks SET deleted_at = ?, updated_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		now, now, id, userID)
	return mustAffect(res, err)
}

// ListBookmarks returns live bookmarks (optionally for one book).
func ListBookmarks(ctx context.Context, q Querier, userID, bookID string) ([]*Bookmark, error) {
	query := `SELECT ` + bookmarkCols + ` FROM bookmarks WHERE user_id = ? AND deleted_at IS NULL`
	args := []any{userID}
	if bookID != "" {
		query += ` AND book_id = ?`
		args = append(args, bookID)
	}
	rows, err := q.Query(ctx, query+` ORDER BY book_id, position_ms`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Bookmark
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ── Collections ──

// Collection is a user's named list of books.
type Collection struct {
	ID, Name, Description string
	UpdatedAt             int64
	DeletedAt             *int64
	BookIDs               []string
}

// GetCollection returns a user's collection (including tombstoned), with the
// books the user can currently see.
func GetCollection(ctx context.Context, q Querier, userID, id string) (*Collection, error) {
	var (
		c    Collection
		desc sql.NullString
		del  sql.NullInt64
	)
	err := q.QueryRow(ctx, `SELECT id, name, description, updated_at, deleted_at FROM collections WHERE id = ? AND owner_id = ?`, id, userID).
		Scan(&c.ID, &c.Name, &desc, &c.UpdatedAt, &del)
	if err != nil {
		return nil, notFound(err)
	}
	c.Description, c.DeletedAt = desc.String, ptr(del)
	if c.BookIDs, err = collectionBooks(ctx, q, userID, id); err != nil {
		return nil, err
	}
	return &c, nil
}

func collectionBooks(ctx context.Context, q Querier, userID, id string) ([]string, error) {
	rows, err := q.Query(ctx, visibleCTE+` SELECT cb.book_id FROM collection_books cb
		JOIN vis v ON v.book_id = cb.book_id JOIN books b ON b.id = cb.book_id
		WHERE cb.collection_id = ? AND v.removed_at IS NULL AND `+visibleFilter+` ORDER BY cb.position, cb.created_at`,
		userID, userID, userID, userID, id, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListCollections returns the user's live collections.
func ListCollections(ctx context.Context, q Querier, userID string) ([]*Collection, error) {
	rows, err := q.Query(ctx, `SELECT id FROM collections WHERE owner_id = ? AND deleted_at IS NULL ORDER BY lower(name), id`, userID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := make([]*Collection, 0, len(ids))
	for _, id := range ids {
		c, err := GetCollection(ctx, q, userID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// CreateCollection inserts (idempotent on id).
func CreateCollection(ctx context.Context, q Querier, userID string, c *Collection) (bool, error) {
	if c.ID == "" {
		c.ID = NewID()
	} else if _, err := GetCollection(ctx, q, userID, c.ID); err == nil {
		return false, nil
	}
	_, err := q.Exec(ctx, `INSERT INTO collections (id, owner_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, userID, c.Name, nullStr(c.Description), NowMS(), c.UpdatedAt)
	if IsUniqueViolation(err) {
		return false, ErrConflict
	}
	return err == nil, err
}

// UpdateCollection applies an edit if not older than the stored clock (LWW).
func UpdateCollection(ctx context.Context, q Querier, userID, id string, name, desc *string, clock int64) (bool, error) {
	res, err := q.Exec(ctx, `UPDATE collections SET name = COALESCE(?, name), description = COALESCE(?, description), updated_at = ?
		WHERE id = ? AND owner_id = ? AND deleted_at IS NULL AND updated_at <= ?`, name, desc, clock, id, userID, clock)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeleteCollection tombstones a collection.
func DeleteCollection(ctx context.Context, q Querier, userID, id string) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE collections SET deleted_at = ?, updated_at = ? WHERE id = ? AND owner_id = ? AND deleted_at IS NULL`,
		now, now, id, userID)
	return mustAffect(res, err)
}

// SetCollectionBook adds or removes a book.
func SetCollectionBook(ctx context.Context, q Querier, collectionID, bookID string, add bool) error {
	now := NowMS()
	if add {
		var pos int64
		_ = q.QueryRow(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM collection_books WHERE collection_id = ?`, collectionID).Scan(&pos)
		_, err := q.Exec(ctx, `INSERT INTO collection_books (id, collection_id, book_id, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (collection_id, book_id) DO NOTHING`, NewID(), collectionID, bookID, pos, now, now)
		if err == nil {
			_, err = q.Exec(ctx, `UPDATE collections SET updated_at = ? WHERE id = ? AND updated_at < ?`, now, collectionID, now)
		}
		return err
	}
	res, err := q.Exec(ctx, `DELETE FROM collection_books WHERE collection_id = ? AND book_id = ?`, collectionID, bookID)
	if err := mustAffect(res, err); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE collections SET updated_at = ? WHERE id = ? AND updated_at < ?`, now, collectionID, now)
	return err
}
