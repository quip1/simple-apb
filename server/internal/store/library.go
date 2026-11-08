// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ── User library entries ──

// AddEntry gives a user a library entry (idempotent; re-adding restores it).
func AddEntry(ctx context.Context, q Querier, userID, bookID, source string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO user_library_entries (id, user_id, book_id, source, added_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET removed_at = NULL, updated_at = excluded.updated_at`,
		NewID(), userID, bookID, source, now, now, now)
	if err != nil {
		return fmt.Errorf("add library entry: %w", err)
	}
	return nil
}

// SetEntryFlag sets removed_at or hidden_at for the user's view of a book.
// A distributed book has no entry yet, so one is created (source distribution).
func SetEntryFlag(ctx context.Context, q Querier, userID, bookID, flag string, on bool) error {
	if flag != "removed_at" && flag != "hidden_at" {
		return fmt.Errorf("bad flag %q", flag)
	}
	now := NowMS()
	var val any
	if on {
		val = now
	}
	res, err := q.Exec(ctx, `UPDATE user_library_entries SET `+flag+` = ?, updated_at = ? WHERE user_id = ? AND book_id = ?`,
		val, now, userID, bookID)
	if err != nil {
		return notFound(err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if !on {
		return ErrNotFound
	}
	_, err = q.Exec(ctx, `INSERT INTO user_library_entries (id, user_id, book_id, source, `+flag+`, added_at, created_at, updated_at)
		VALUES (?, ?, ?, 'distribution', ?, ?, ?, ?)`, NewID(), userID, bookID, now, now, now, now)
	return err
}

// RestoreEntry clears removed_at and hidden_at.
func RestoreEntry(ctx context.Context, q Querier, userID, bookID string) error {
	res, err := q.Exec(ctx, `UPDATE user_library_entries SET removed_at = NULL, hidden_at = NULL, updated_at = ?
		WHERE user_id = ? AND book_id = ?`, NowMS(), userID, bookID)
	return mustAffect(res, err)
}

// EntryRemovedAt returns removed_at for a user's entry (nil if none or not removed).
func EntryRemovedAt(ctx context.Context, q Querier, userID, bookID string) (*int64, error) {
	var r sql.NullInt64
	err := q.QueryRow(ctx, `SELECT removed_at FROM user_library_entries WHERE user_id = ? AND book_id = ?`, userID, bookID).Scan(&r)
	if err != nil {
		return nil, notFound(err)
	}
	return ptr(r), nil
}

// PurgeRemovedEntries hard-deletes entries removed before the cutoff (30-day restore window).
func PurgeRemovedEntries(ctx context.Context, q Querier, cutoffMS int64) (int64, error) {
	res, err := q.Exec(ctx, `DELETE FROM user_library_entries WHERE removed_at IS NOT NULL AND removed_at <= ?`, cutoffMS)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UsedBytes is the user's quota usage: books they uploaded whose entry isn't
// removed (deduplicated copies count for the original uploader only).
func UsedBytes(ctx context.Context, q Querier, userID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(b.size_bytes), 0) FROM books b
		JOIN user_library_entries e ON e.book_id = b.id AND e.user_id = b.uploader_id
		WHERE b.uploader_id = ? AND e.removed_at IS NULL AND b.status <> 'trashed'`, userID).Scan(&n)
	return n, err
}

// PendingUploadBytes sums uploads still being received or processed.
func PendingUploadBytes(ctx context.Context, q Querier, userID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM uploads WHERE user_id = ? AND status IN ('receiving', 'processing')`,
		userID).Scan(&n)
	return n, err
}

// ── Distributions ──

// Distribution is an admin action making a book visible to a group or user.
type Distribution struct {
	ID, BookID, TargetType, TargetID, TargetName, DistributedBy, Note string
	CreatedAt                                                         int64
}

// CreateDistribution adds a live distribution (idempotent per target).
func CreateDistribution(ctx context.Context, q Querier, bookID, targetType, targetID, by, note string) (string, error) {
	var existing string
	err := q.QueryRow(ctx, `SELECT id FROM distributions WHERE book_id = ? AND target_type = ? AND target_id = ? AND revoked_at IS NULL`,
		bookID, targetType, targetID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if notFound(err) != ErrNotFound {
		return "", err
	}
	id, now := NewID(), NowMS()
	_, err = q.Exec(ctx, `INSERT INTO distributions (id, book_id, target_type, target_id, distributed_by, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, bookID, targetType, targetID, nullStr(by), nullStr(note), now, now)
	if err != nil {
		return "", fmt.Errorf("create distribution: %w", err)
	}
	return id, nil
}

// RevokeDistribution ends a distribution and returns its book id.
func RevokeDistribution(ctx context.Context, q Querier, id string) (string, error) {
	var bookID string
	if err := q.QueryRow(ctx, `SELECT book_id FROM distributions WHERE id = ? AND revoked_at IS NULL`, id).Scan(&bookID); err != nil {
		return "", notFound(err)
	}
	now := NowMS()
	_, err := q.Exec(ctx, `UPDATE distributions SET revoked_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return bookID, err
}

// ListDistributions lists live distributions of a book, with target names.
func ListDistributions(ctx context.Context, q Querier, bookID string) ([]*Distribution, error) {
	rows, err := q.Query(ctx, `SELECT d.id, d.book_id, d.target_type, d.target_id,
			COALESCE(g.name, u.username, ''), d.distributed_by, d.note, d.created_at
		FROM distributions d
		LEFT JOIN groups g ON d.target_type = 'group' AND CAST(g.id AS TEXT) = d.target_id
		LEFT JOIN users u ON d.target_type = 'user' AND CAST(u.id AS TEXT) = d.target_id
		WHERE d.book_id = ? AND d.revoked_at IS NULL ORDER BY d.created_at`, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Distribution
	for rows.Next() {
		var (
			d        Distribution
			by, note sql.NullString
		)
		if err := rows.Scan(&d.ID, &d.BookID, &d.TargetType, &d.TargetID, &d.TargetName, &by, &note, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.DistributedBy, d.Note = by.String, note.String
		out = append(out, &d)
	}
	return out, rows.Err()
}

// ── Visibility (brief §6.5 rule 4) ──
//
// A user sees: their own entries ∪ books distributed to them or their groups,
// minus removed/hidden (depending on shelf), minus books carrying a tag in
// their content filter, minus active takedowns, minus non-ready books.

// visibleCTE yields (book_id, source, added_at, hidden_at, removed_at) for a
// user; it binds the user id 4 times.
const visibleCTE = `WITH my_groups AS (
		SELECT CAST(group_id AS TEXT) AS gid FROM group_members WHERE user_id = ?
	), dist AS (
		SELECT d.book_id, MIN(d.created_at) AS added_at FROM distributions d
		WHERE d.revoked_at IS NULL AND ((d.target_type = 'user' AND d.target_id = ?)
			OR (d.target_type = 'group' AND d.target_id IN (SELECT gid FROM my_groups)))
		GROUP BY d.book_id
	), vis AS (
		SELECT e.book_id, e.source, e.added_at, e.hidden_at, e.removed_at FROM user_library_entries e
		WHERE e.user_id = ? AND (e.source <> 'distribution' OR e.book_id IN (SELECT book_id FROM dist))
		UNION ALL
		SELECT d.book_id, 'distribution', d.added_at, NULL, NULL FROM dist d
		WHERE NOT EXISTS (SELECT 1 FROM user_library_entries e WHERE e.user_id = ? AND e.book_id = d.book_id)
	)`

// visibleFilter excludes filtered-tag, taken-down and non-ready books; binds the user id once.
const visibleFilter = ` b.status = 'ready'
	AND NOT EXISTS (SELECT 1 FROM takedowns t WHERE t.book_id = b.id AND t.lifted_at IS NULL)
	AND NOT EXISTS (SELECT 1 FROM book_tag_assignments a JOIN content_filters cf ON cf.book_tag_id = a.book_tag_id
		WHERE a.book_id = b.id AND cf.user_id = ?)`

// LibraryFilter selects a page of a user's library.
type LibraryFilter struct {
	Shelf                                               string // all|in_progress|not_started|finished|archived|recently_removed
	SeriesID, AuthorID, NarratorID, TagID, CollectionID string
	Query, Sort                                         string
	Limit, Offset                                       int
}

// LibraryItem is a visible book plus the user's relation to it.
type LibraryItem struct {
	Book                                *Book
	Source                              string
	AddedAt                             int64
	HiddenAt, RemovedAt                 *int64
	ProgressMS, ProgressClientTS        *int64
	ProgressDevice                      string
	ProgressSpeed                       float64
	ProgressUpdatedAt, ProgressFinished *int64
}

// RecentlyRemovedWindowMS is how long a user can restore a removed book.
const RecentlyRemovedWindowMS = 30 * 24 * 3600 * 1000

// ListLibrary returns a page of a user's visible books.
func ListLibrary(ctx context.Context, q Querier, userID string, f LibraryFilter) ([]*LibraryItem, error) {
	where := []string{visibleFilter}
	args := []any{userID, userID, userID, userID, userID}
	switch f.Shelf {
	case "", "all":
		where = append(where, "v.removed_at IS NULL AND v.hidden_at IS NULL")
	case "archived":
		where = append(where, "v.removed_at IS NULL AND v.hidden_at IS NOT NULL")
	case "recently_removed":
		where, args = append(where, "v.removed_at IS NOT NULL AND v.removed_at > ?"), append(args, NowMS()-RecentlyRemovedWindowMS)
	case "in_progress":
		where = append(where, "v.removed_at IS NULL AND v.hidden_at IS NULL AND p.position_ms > 0 AND p.finished_at IS NULL")
	case "not_started":
		where = append(where, "v.removed_at IS NULL AND v.hidden_at IS NULL AND (p.id IS NULL OR (p.position_ms = 0 AND p.finished_at IS NULL))")
	case "finished":
		where = append(where, "v.removed_at IS NULL AND p.finished_at IS NOT NULL")
	default:
		return nil, fmt.Errorf("unknown shelf %q", f.Shelf)
	}
	if f.SeriesID != "" {
		where, args = append(where, "b.series_id = ?"), append(args, f.SeriesID)
	}
	if f.AuthorID != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM book_authors ba WHERE ba.book_id = b.id AND ba.author_id = ?)"), append(args, f.AuthorID)
	}
	if f.NarratorID != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM book_narrators bn WHERE bn.book_id = b.id AND bn.narrator_id = ?)"), append(args, f.NarratorID)
	}
	if f.TagID != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM book_tag_assignments ta WHERE ta.book_id = b.id AND ta.book_tag_id = ?)"), append(args, f.TagID)
	}
	if f.CollectionID != "" {
		where, args = append(where, "EXISTS (SELECT 1 FROM collection_books cb WHERE cb.book_id = b.id AND cb.collection_id = ?)"), append(args, f.CollectionID)
	}
	if f.Query != "" {
		pat := likePattern(f.Query)
		where = append(where, `(lower(b.title) LIKE ? ESCAPE '\' OR lower(COALESCE(b.subtitle, '')) LIKE ? ESCAPE '\'
			OR lower(COALESCE(s.name, '')) LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM book_authors ba JOIN authors au ON au.id = ba.author_id WHERE ba.book_id = b.id AND lower(au.name) LIKE ? ESCAPE '\')
			OR EXISTS (SELECT 1 FROM book_narrators bn JOIN narrators na ON na.id = bn.narrator_id WHERE bn.book_id = b.id AND lower(na.name) LIKE ? ESCAPE '\'))`)
		args = append(args, pat, pat, pat, pat, pat)
	}
	order := "v.added_at DESC, b.id"
	switch f.Sort {
	case "title":
		order = "b.sort_title, b.id"
	case "recent_played":
		order = "CASE WHEN p.updated_at IS NULL THEN 1 ELSE 0 END, p.updated_at DESC, b.id"
	case "duration":
		order = "b.duration_ms DESC, b.id"
	case "series":
		order = "CASE WHEN s.name IS NULL THEN 1 ELSE 0 END, lower(s.name), b.series_index, b.sort_title, b.id"
	case "author":
		order = `CASE WHEN fa.name IS NULL THEN 1 ELSE 0 END, lower(fa.name), b.sort_title, b.id`
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	query := visibleCTE + ` SELECT ` + bookCols + `, v.source, v.added_at, v.hidden_at, v.removed_at,
			p.position_ms, p.client_ts, p.device_id, p.speed, p.updated_at, p.finished_at
		FROM vis v JOIN books b ON b.id = v.book_id
		LEFT JOIN series s ON s.id = b.series_id
		LEFT JOIN progress p ON p.book_id = b.id AND p.user_id = ?
		LEFT JOIN (SELECT ba.book_id, au.name FROM book_authors ba JOIN authors au ON au.id = ba.author_id WHERE ba.position = 0) fa
			ON fa.book_id = b.id
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	// The progress join binds one more user id, positioned before the WHERE args.
	args = append([]any{args[0], args[1], args[2], args[3], userID}, args[4:]...)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list library: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*LibraryItem
	for rows.Next() {
		it, err := scanLibraryItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanLibraryItem(row interface{ Scan(...any) error }) (*LibraryItem, error) {
	var (
		it                                   LibraryItem
		hidden, removed, pos, cts, pupd, fin sql.NullInt64
		dev                                  sql.NullString
		speed                                sql.NullFloat64
	)
	b, err := scanBook(row, &it.Source, &it.AddedAt, &hidden, &removed, &pos, &cts, &dev, &speed, &pupd, &fin)
	if err != nil {
		return nil, err
	}
	it.Book, it.HiddenAt, it.RemovedAt = b, ptr(hidden), ptr(removed)
	it.ProgressMS, it.ProgressClientTS, it.ProgressDevice = ptr(pos), ptr(cts), dev.String
	it.ProgressSpeed, it.ProgressUpdatedAt, it.ProgressFinished = speed.Float64, ptr(pupd), ptr(fin)
	return &it, nil
}

// VisibleBook returns one book if the user can see it (any shelf, including
// archived and recently removed), else ErrNotFound.
func VisibleBook(ctx context.Context, q Querier, userID, bookID string) (*LibraryItem, error) {
	query := visibleCTE + ` SELECT ` + bookCols + `, v.source, v.added_at, v.hidden_at, v.removed_at,
			p.position_ms, p.client_ts, p.device_id, p.speed, p.updated_at, p.finished_at
		FROM vis v JOIN books b ON b.id = v.book_id
		LEFT JOIN series s ON s.id = b.series_id
		LEFT JOIN progress p ON p.book_id = b.id AND p.user_id = ?
		WHERE b.id = ? AND ` + visibleFilter + ` LIMIT 1`
	return scanLibraryItem(q.QueryRow(ctx, query, userID, userID, userID, userID, userID, bookID, userID))
}
