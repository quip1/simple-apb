// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// Book tools (brief §6.13): edited chapters, duplicate review, and moving a
// duplicate's user data to the book that is kept.

// LockedChapters is the locked-field name for hand-edited chapters: a
// re-scan keeps them instead of reading chapters from the files again.
const LockedChapters = "chapters"

// ReplaceChapters swaps a book's chapters.
func ReplaceChapters(ctx context.Context, q Querier, bookID string, chapters []Chapter) error {
	if _, err := q.Exec(ctx, `DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	now := NowMS()
	for _, c := range chapters {
		if _, err := q.Exec(ctx, `INSERT INTO chapters (id, book_id, idx, title, start_ms, end_ms, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, NewID(), bookID, c.Index, c.Title, c.StartMS, c.EndMS, now, now); err != nil {
			return fmt.Errorf("insert chapter: %w", err)
		}
	}
	_, err := q.Exec(ctx, `UPDATE books SET updated_at = ? WHERE id = ?`, now, bookID)
	return err
}

// LockField marks a field as edited by hand.
func LockField(ctx context.Context, q Querier, id, field string) error {
	locked, err := LockedFields(ctx, q, id)
	if err != nil {
		return err
	}
	if slices.Contains(locked, field) {
		return nil
	}
	raw, _ := json.Marshal(sortFields(append(locked, field)))
	_, err = q.Exec(ctx, `UPDATE books SET locked_fields = ? WHERE id = ?`, string(raw), id)
	return err
}

// ── duplicates ──

// DuplicateBook is the data the duplicate finder compares.
type DuplicateBook struct {
	ID, Title, Author, Checksum string
	DurationMS                  int64
}

// DuplicateScanBooks lists every book that may be a duplicate (not trashed).
func DuplicateScanBooks(ctx context.Context, q Querier) ([]DuplicateBook, error) {
	rows, err := q.Query(ctx, `SELECT b.id, b.title, b.checksum_sha256, b.duration_ms,
		(SELECT a.name FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1)
		FROM books b WHERE b.status IN ('ready', 'quarantined') ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DuplicateBook
	for rows.Next() {
		var d DuplicateBook
		var sum, author sql.NullString
		if err := rows.Scan(&d.ID, &d.Title, &sum, &d.DurationMS, &author); err != nil {
			return nil, err
		}
		d.Checksum, d.Author = sum.String, author.String
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDuplicateCandidate records a pair (a < b) unless it is already known,
// open or decided. Reports whether it was new.
func AddDuplicateCandidate(ctx context.Context, q Querier, a, b, kind string, score float64) (bool, error) {
	if a > b {
		a, b = b, a
	}
	now := NowMS()
	res, err := q.Exec(ctx, `INSERT INTO duplicate_candidates (id, book_a_id, book_b_id, kind, score, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'open', ?, ?) ON CONFLICT (book_a_id, book_b_id) DO NOTHING`, NewID(), a, b, kind, score, now, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DuplicateCandidate is an open pair for review.
type DuplicateCandidate struct {
	ID, Kind, BookA, BookB string
	Score                  float64
	CreatedAt              int64
}

// ListOpenDuplicates returns open pairs whose books are both still in the
// library, exact matches first.
func ListOpenDuplicates(ctx context.Context, q Querier, limit, offset int) ([]DuplicateCandidate, error) {
	rows, err := q.Query(ctx, `SELECT d.id, d.kind, d.book_a_id, d.book_b_id, d.score, d.created_at
		FROM duplicate_candidates d
		JOIN books a ON a.id = d.book_a_id AND a.status <> 'trashed'
		JOIN books b ON b.id = d.book_b_id AND b.status <> 'trashed'
		WHERE d.status = 'open'
		ORDER BY CASE d.kind WHEN 'exact' THEN 0 ELSE 1 END, d.created_at, d.id
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DuplicateCandidate
	for rows.Next() {
		var d DuplicateCandidate
		var score sql.NullFloat64
		if err := rows.Scan(&d.ID, &d.Kind, &d.BookA, &d.BookB, &score, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Score = score.Float64
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDuplicate returns one pair.
func GetDuplicate(ctx context.Context, q Querier, id string) (*DuplicateCandidate, string, error) {
	var d DuplicateCandidate
	var status string
	var score sql.NullFloat64
	err := q.QueryRow(ctx, `SELECT id, kind, book_a_id, book_b_id, score, created_at, status FROM duplicate_candidates WHERE id = ?`, id).
		Scan(&d.ID, &d.Kind, &d.BookA, &d.BookB, &score, &d.CreatedAt, &status)
	if err != nil {
		return nil, "", notFound(err)
	}
	d.Score = score.Float64
	return &d, status, nil
}

// DecideDuplicate closes a pair as kept or merged.
func DecideDuplicate(ctx context.Context, q Querier, id, status, by string) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE duplicate_candidates SET status = ?, decided_by = ?, decided_at = ?, updated_at = ? WHERE id = ? AND status = 'open'`,
		status, nullStr(by), now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

// MoveBookData moves what users have of book `from` to book `to`, where `to`
// doesn't already have it: library entries, distributions, collections,
// progress (the newer of the two wins), bookmarks, listening history,
// reviews, recommendations and upload records. It returns the users whose
// view changed, for sync.
func MoveBookData(ctx context.Context, q Querier, from, to string) ([]string, error) {
	var users []string
	rows, err := q.Query(ctx, `SELECT DISTINCT user_id FROM user_library_entries WHERE book_id = ?
		UNION SELECT DISTINCT user_id FROM progress WHERE book_id = ?
		UNION SELECT DISTINCT user_id FROM bookmarks WHERE book_id = ?`, from, from, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			_ = rows.Close()
			return nil, err
		}
		users = append(users, u)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stmts := []string{
		`UPDATE user_library_entries SET book_id = ? WHERE book_id = ?
			AND NOT EXISTS (SELECT 1 FROM user_library_entries k WHERE k.book_id = ? AND k.user_id = user_library_entries.user_id)`,
		`UPDATE distributions SET book_id = ? WHERE book_id = ? AND revoked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM distributions k WHERE k.book_id = ? AND k.revoked_at IS NULL
				AND k.target_type = distributions.target_type AND k.target_id = distributions.target_id)`,
		`UPDATE collection_books SET book_id = ? WHERE book_id = ?
			AND NOT EXISTS (SELECT 1 FROM collection_books k WHERE k.book_id = ? AND k.collection_id = collection_books.collection_id)`,
		`UPDATE ratings_reviews SET book_id = ? WHERE book_id = ? AND (user_id IS NULL OR deleted_at IS NOT NULL
			OR NOT EXISTS (SELECT 1 FROM ratings_reviews k WHERE k.book_id = ? AND k.user_id = ratings_reviews.user_id AND k.deleted_at IS NULL))`,
		`UPDATE recommendations SET book_id = ? WHERE book_id = ?
			AND NOT EXISTS (SELECT 1 FROM recommendations k WHERE k.book_id = ? AND k.from_user = recommendations.from_user
				AND k.to_user = recommendations.to_user)`,
	}
	for _, s := range stmts {
		if _, err := q.Exec(ctx, s, to, from, to); err != nil {
			return nil, fmt.Errorf("move book data: %w", err)
		}
	}
	// Progress: the newer record wins.
	if _, err := q.Exec(ctx, `DELETE FROM progress WHERE book_id = ? AND EXISTS (SELECT 1 FROM progress d
		WHERE d.book_id = ? AND d.user_id = progress.user_id AND d.updated_at > progress.updated_at)`, to, from); err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE progress SET book_id = ? WHERE book_id = ?
		AND NOT EXISTS (SELECT 1 FROM progress k WHERE k.book_id = ? AND k.user_id = progress.user_id)`, to, from, to); err != nil {
		return nil, err
	}
	for _, s := range []string{
		`UPDATE bookmarks SET book_id = ? WHERE book_id = ?`,
		`UPDATE progress_events SET book_id = ? WHERE book_id = ?`,
		`UPDATE uploads SET book_id = ? WHERE book_id = ?`,
	} {
		if _, err := q.Exec(ctx, s, to, from); err != nil {
			return nil, err
		}
	}
	if _, err := q.Exec(ctx, `DELETE FROM progress_conflicts WHERE book_id = ?`, from); err != nil {
		return nil, err
	}
	return users, nil
}

// ErrNotMergeable means the merge tool has nothing to do for the book.
var ErrNotMergeable = errors.New("book is already a single m4b")
