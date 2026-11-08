// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// UserName is a user's id and names ("" id = a former, anonymized user).
type UserName struct {
	ID, Username, DisplayName string
}

func userName(id, username, display sql.NullString) *UserName {
	if !id.Valid {
		return nil
	}
	return &UserName{ID: id.String, Username: username.String, DisplayName: display.String}
}

// ── Ratings and reviews ──

// Review is one user's rating (and optional text) of a book. A review is
// hidden by a moderator when HiddenReason is set.
type Review struct {
	ID, BookID   string
	Author       *UserName
	Stars        int
	Text         string
	HiddenReason string
	CreatedAt    int64
	UpdatedAt    int64
}

// Hidden reports whether a moderator hid the review.
func (r *Review) Hidden() bool { return r.HiddenReason != "" }

const reviewCols = `r.id, r.book_id, r.user_id, u.username, u.display_name, r.stars, r.text, r.hidden_reason, r.created_at, r.updated_at`

const reviewFrom = ` FROM ratings_reviews r LEFT JOIN users u ON u.id = r.user_id`

func scanReview(row interface{ Scan(...any) error }) (*Review, error) {
	var (
		r                      Review
		uid, uname, udisp      sql.NullString
		text, hiddenReasonNull sql.NullString
	)
	if err := row.Scan(&r.ID, &r.BookID, &uid, &uname, &udisp, &r.Stars, &text, &hiddenReasonNull, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Author, r.Text, r.HiddenReason = userName(uid, uname, udisp), text.String, hiddenReasonNull.String
	return &r, nil
}

// GetReview returns a live (not deleted) review.
func GetReview(ctx context.Context, q Querier, id string) (*Review, error) {
	r, err := scanReview(q.QueryRow(ctx, `SELECT `+reviewCols+reviewFrom+` WHERE r.id = ? AND r.deleted_at IS NULL`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return r, nil
}

// MyReview returns the user's live review of a book.
func MyReview(ctx context.Context, q Querier, userID, bookID string) (*Review, error) {
	r, err := scanReview(q.QueryRow(ctx, `SELECT `+reviewCols+reviewFrom+`
		WHERE r.user_id = ? AND r.book_id = ? AND r.deleted_at IS NULL`, userID, bookID))
	if err != nil {
		return nil, notFound(err)
	}
	return r, nil
}

// ListReviews returns a page of a book's reviews, newest first. Hidden
// reviews are included only when includeHidden is set, or for their author.
func ListReviews(ctx context.Context, q Querier, bookID, viewerID string, includeHidden bool, limit, offset int) ([]*Review, error) {
	rows, err := q.Query(ctx, `SELECT `+reviewCols+reviewFrom+`
		WHERE r.book_id = ? AND r.deleted_at IS NULL AND (r.hidden_reason IS NULL OR ? OR r.user_id = ?)
		ORDER BY r.updated_at DESC, r.id LIMIT ? OFFSET ?`, bookID, includeHidden, viewerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RatingCounts returns how many visible (live, not hidden) ratings a book
// has per star value; index 0 is one star.
func RatingCounts(ctx context.Context, q Querier, bookID string) ([5]int64, error) {
	var out [5]int64
	rows, err := q.Query(ctx, `SELECT stars, COUNT(*) FROM ratings_reviews
		WHERE book_id = ? AND deleted_at IS NULL AND hidden_reason IS NULL GROUP BY stars`, bookID)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stars int
		var n int64
		if err := rows.Scan(&stars, &n); err != nil {
			return out, err
		}
		if stars >= 1 && stars <= 5 {
			out[stars-1] = n
		}
	}
	return out, rows.Err()
}

// UpsertReview creates or replaces the user's review. A hidden review stays
// hidden when its author edits it.
func UpsertReview(ctx context.Context, q Querier, userID, bookID string, stars int, text string) (*Review, bool, error) {
	now := NowMS()
	existing, err := MyReview(ctx, q, userID, bookID)
	switch {
	case err == nil:
		if _, err := q.Exec(ctx, `UPDATE ratings_reviews SET stars = ?, text = ?, updated_at = ? WHERE id = ?`,
			stars, nullStr(text), now, existing.ID); err != nil {
			return nil, false, fmt.Errorf("update review: %w", err)
		}
		r, err := GetReview(ctx, q, existing.ID)
		return r, false, err
	case err != ErrNotFound:
		return nil, false, err
	}
	id := NewID()
	if _, err := q.Exec(ctx, `INSERT INTO ratings_reviews (id, user_id, book_id, stars, text, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, userID, bookID, stars, nullStr(text), now, now); err != nil {
		if IsUniqueViolation(err) {
			return nil, false, ErrConflict
		}
		return nil, false, fmt.Errorf("insert review: %w", err)
	}
	r, err := GetReview(ctx, q, id)
	return r, true, err
}

// DeleteReview soft-deletes a review.
func DeleteReview(ctx context.Context, q Querier, id string) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE ratings_reviews SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	return mustAffect(res, err)
}

// SetReviewHidden hides (reason != "") or shows (reason == "") a review.
func SetReviewHidden(ctx context.Context, q Querier, id, by, reason string) error {
	// updated_at is the author's last edit (shown to readers), so moderation leaves it alone.
	res, err := q.Exec(ctx, `UPDATE ratings_reviews SET hidden_by_mod = ?, hidden_reason = ?
		WHERE id = ? AND deleted_at IS NULL`, nullStr(by), nullStr(reason), id)
	return mustAffect(res, err)
}

// ── Recommendations ──

// Recommendation is a book one user suggested to another.
type Recommendation struct {
	ID, BookID, Note string
	From             UserName
	CreatedAt        int64
}

// UpsertRecommendation records (or refreshes) a recommendation.
func UpsertRecommendation(ctx context.Context, q Querier, from, to, bookID, note string) error {
	now := NowMS()
	_, err := q.Exec(ctx, `INSERT INTO recommendations (id, from_user, to_user, book_id, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (from_user, to_user, book_id) DO UPDATE SET note = excluded.note, created_at = excluded.created_at,
			updated_at = excluded.updated_at`, NewID(), from, to, bookID, nullStr(note), now, now)
	if err != nil {
		return fmt.Errorf("upsert recommendation: %w", err)
	}
	return nil
}

// ListRecommendationsFor returns the newest recommendations to a user (the
// caller filters them by what the user can see).
func ListRecommendationsFor(ctx context.Context, q Querier, userID string, limit int) ([]*Recommendation, error) {
	rows, err := q.Query(ctx, `SELECT r.id, r.book_id, r.note, r.created_at, u.id, u.username, u.display_name
		FROM recommendations r JOIN users u ON u.id = r.from_user
		WHERE r.to_user = ? AND u.status <> 'deleted' ORDER BY r.created_at DESC, r.id LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Recommendation
	for rows.Next() {
		var (
			r    Recommendation
			note sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.BookID, &note, &r.CreatedAt, &r.From.ID, &r.From.Username, &r.From.DisplayName); err != nil {
			return nil, err
		}
		r.Note = note.String
		out = append(out, &r)
	}
	return out, rows.Err()
}

// DeleteRecommendation removes a recommendation addressed to the user.
func DeleteRecommendation(ctx context.Context, q Querier, id, toUser string) error {
	res, err := q.Exec(ctx, `DELETE FROM recommendations WHERE id = ? AND to_user = ?`, id, toUser)
	return mustAffect(res, err)
}

// ── Who's listening ──

// Listener is someone with progress on a book.
type Listener struct {
	User      UserName
	Finished  bool
	UpdatedAt int64
}

// ListListeners returns people listening to a book (progress in the last
// activeSinceMS) or who finished it, most recent first. People who opted
// out of "show my listening", inactive accounts and excludeUser are left out.
func ListListeners(ctx context.Context, q Querier, bookID, excludeUser string, activeSinceMS int64, limit int) ([]*Listener, error) {
	rows, err := q.Query(ctx, `SELECT u.id, u.username, u.display_name, p.finished_at, p.updated_at
		FROM progress p JOIN users u ON u.id = p.user_id
		WHERE p.book_id = ? AND p.user_id <> ? AND u.status = 'active' AND u.listening_visible = ?
			AND (p.finished_at IS NOT NULL OR (p.position_ms > 0 AND p.updated_at >= ?))
		ORDER BY p.updated_at DESC, u.id LIMIT ?`, bookID, excludeUser, true, activeSinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Listener
	for rows.Next() {
		var (
			l   Listener
			fin sql.NullInt64
		)
		if err := rows.Scan(&l.User.ID, &l.User.Username, &l.User.DisplayName, &fin, &l.UpdatedAt); err != nil {
			return nil, err
		}
		l.Finished = fin.Valid
		out = append(out, &l)
	}
	return out, rows.Err()
}

// ── People and groups ──

// SearchPeople finds active users who share a group with the user.
func SearchPeople(ctx context.Context, q Querier, userID, query string, limit int) ([]*UserName, error) {
	pat := likePattern(query)
	rows, err := q.Query(ctx, `SELECT u.id, u.username, u.display_name FROM users u
		WHERE u.id <> ? AND u.status = 'active'
			AND EXISTS (SELECT 1 FROM group_members m JOIN group_members mine ON mine.group_id = m.group_id
				WHERE m.user_id = u.id AND mine.user_id = ?)
			AND (lower(u.username) LIKE ? ESCAPE '\' OR lower(u.display_name) LIKE ? ESCAPE '\')
		ORDER BY lower(u.display_name), u.id LIMIT ?`, userID, userID, pat, pat, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*UserName
	for rows.Next() {
		var u UserName
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

// SharesGroup reports whether target is an active user who shares a group with userID.
func SharesGroup(ctx context.Context, q Querier, userID, target string) (bool, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM users u WHERE u.id = ? AND u.status = 'active'
		AND EXISTS (SELECT 1 FROM group_members m JOIN group_members mine ON mine.group_id = m.group_id
			WHERE m.user_id = u.id AND mine.user_id = ?)`, target, userID).Scan(&n)
	if isInvalidUUID(err) {
		return false, nil
	}
	return n > 0, err
}

// GroupsOf returns the groups a user belongs to, by name.
func GroupsOf(ctx context.Context, q Querier, userID string) ([]*Group, error) {
	rows, err := q.Query(ctx, `SELECT g.id, g.name, g.builtin FROM groups g JOIN group_members m ON m.group_id = g.id
		WHERE m.user_id = ? ORDER BY g.builtin DESC, lower(g.name)`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Builtin); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// ── Reports ──

// Report is a user's complaint about a book, review or user.
type Report struct {
	ID, ObjectType, ObjectID, Reason, Details string
	Status, Resolution, ResolutionNote        string
	Reporter, ResolvedBy                      *UserName
	CreatedAt                                 int64
	ResolvedAt                                *int64
}

const reportCols = `r.id, r.object_type, r.object_id, r.reason, r.details, r.status, r.resolution, r.resolution_note,
	rep.id, rep.username, rep.display_name, res.id, res.username, res.display_name, r.created_at, r.resolved_at`

const reportFrom = ` FROM reports r LEFT JOIN users rep ON rep.id = r.reporter_id LEFT JOIN users res ON res.id = r.resolved_by`

func scanReport(row interface{ Scan(...any) error }) (*Report, error) {
	var (
		r                            Report
		details, resolution, resNote sql.NullString
		repID, repName, repDisp      sql.NullString
		resID, resName, resDisp      sql.NullString
		resolvedAt                   sql.NullInt64
	)
	if err := row.Scan(&r.ID, &r.ObjectType, &r.ObjectID, &r.Reason, &details, &r.Status, &resolution, &resNote,
		&repID, &repName, &repDisp, &resID, &resName, &resDisp, &r.CreatedAt, &resolvedAt); err != nil {
		return nil, err
	}
	r.Details, r.Resolution, r.ResolutionNote = details.String, resolution.String, resNote.String
	r.Reporter, r.ResolvedBy, r.ResolvedAt = userName(repID, repName, repDisp), userName(resID, resName, resDisp), ptr(resolvedAt)
	return &r, nil
}

// FileReport creates a report, or updates the reporter's open report on the
// same object. It returns the id and whether it is new.
func FileReport(ctx context.Context, q Querier, reporter, objectType, objectID, reason, details string) (string, bool, error) {
	now := NowMS()
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM reports WHERE reporter_id = ? AND object_type = ? AND object_id = ? AND status = 'open'`,
		reporter, objectType, objectID).Scan(&id)
	if err == nil {
		_, err = q.Exec(ctx, `UPDATE reports SET reason = ?, details = ?, updated_at = ? WHERE id = ?`, reason, nullStr(details), now, id)
		return id, false, err
	}
	if notFound(err) != ErrNotFound {
		return "", false, err
	}
	id = NewID()
	_, err = q.Exec(ctx, `INSERT INTO reports (id, reporter_id, object_type, object_id, reason, details, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?)`, id, reporter, objectType, objectID, reason, nullStr(details), now, now)
	if err != nil {
		return "", false, fmt.Errorf("insert report: %w", err)
	}
	return id, true, nil
}

// OpenReportsBy counts a reporter's open reports.
func OpenReportsBy(ctx context.Context, q Querier, reporter string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM reports WHERE reporter_id = ? AND status = 'open'`, reporter).Scan(&n)
	return n, err
}

// GetReport returns one report.
func GetReport(ctx context.Context, q Querier, id string) (*Report, error) {
	r, err := scanReport(q.QueryRow(ctx, `SELECT `+reportCols+reportFrom+` WHERE r.id = ?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return r, nil
}

// ListReports returns a page of reports with a status: open ones oldest
// first (a queue), closed ones newest first.
func ListReports(ctx context.Context, q Querier, status string, limit, offset int) ([]*Report, error) {
	order := `r.created_at, r.id`
	if status != "open" {
		order = `r.resolved_at DESC, r.id`
	}
	rows, err := q.Query(ctx, `SELECT `+reportCols+reportFrom+` WHERE r.status = ? ORDER BY `+order+` LIMIT ? OFFSET ?`,
		status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CloseReports closes every open report about an object and returns how many.
func CloseReports(ctx context.Context, q Querier, objectType, objectID, status, resolution, note, by string) (int64, error) {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE reports SET status = ?, resolution = ?, resolution_note = ?, resolved_by = ?, resolved_at = ?, updated_at = ?
		WHERE object_type = ? AND object_id = ? AND status = 'open'`, status, resolution, nullStr(note), by, now, now, objectType, objectID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Review queue (sharing with Public, brief §6.5 rule 5) ──

// ShareRequest is a review-queue item: a request to share a book with a group.
type ShareRequest struct {
	ID, BookID, BookTitle, GroupID, GroupName, Status, Reason string
	BookHasCover, GroupBuiltin                                bool
	Uploader, DecidedBy                                       *UserName
	CreatedAt                                                 int64
	DecidedAt                                                 *int64
}

const shareRequestCols = `q.id, q.book_id, b.title, b.cover_path, q.target_group, g.name, g.builtin, q.status, q.reason,
	up.id, up.username, up.display_name, dec.id, dec.username, dec.display_name, q.created_at, q.decided_at`

const shareRequestFrom = ` FROM review_queue q JOIN books b ON b.id = q.book_id JOIN groups g ON g.id = q.target_group
	LEFT JOIN users up ON up.id = q.uploader_id LEFT JOIN users dec ON dec.id = q.decided_by`

func scanShareRequest(row interface{ Scan(...any) error }) (*ShareRequest, error) {
	var (
		r                       ShareRequest
		cover, reason           sql.NullString
		upID, upName, upDisp    sql.NullString
		decID, decName, decDisp sql.NullString
		decided                 sql.NullInt64
	)
	if err := row.Scan(&r.ID, &r.BookID, &r.BookTitle, &cover, &r.GroupID, &r.GroupName, &r.GroupBuiltin, &r.Status, &reason,
		&upID, &upName, &upDisp, &decID, &decName, &decDisp, &r.CreatedAt, &decided); err != nil {
		return nil, err
	}
	r.BookHasCover, r.Reason = cover.String != "", reason.String
	r.Uploader, r.DecidedBy, r.DecidedAt = userName(upID, upName, upDisp), userName(decID, decName, decDisp), ptr(decided)
	return &r, nil
}

func listShareRequests(ctx context.Context, q Querier, tail string, args ...any) ([]*ShareRequest, error) {
	rows, err := q.Query(ctx, `SELECT `+shareRequestCols+shareRequestFrom+` `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*ShareRequest
	for rows.Next() {
		r, err := scanShareRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RequestShare queues a request (idempotent while one is pending).
func RequestShare(ctx context.Context, q Querier, bookID, uploaderID, groupID string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM review_queue WHERE book_id = ? AND target_group = ? AND status = 'pending'`, bookID, groupID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if notFound(err) != ErrNotFound {
		return "", err
	}
	id, now := NewID(), NowMS()
	_, err = q.Exec(ctx, `INSERT INTO review_queue (id, book_id, uploader_id, target_group, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'pending', ?, ?)`, id, bookID, uploaderID, groupID, now, now)
	if err != nil {
		return "", fmt.Errorf("queue share request: %w", err)
	}
	return id, nil
}

// GetShareRequest returns one review-queue item.
func GetShareRequest(ctx context.Context, q Querier, id string) (*ShareRequest, error) {
	out, err := listShareRequests(ctx, q, `WHERE q.id = ?`, id)
	if err != nil {
		return nil, notFound(err)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out[0], nil
}

// ShareRequestsForBook lists an uploader's requests for a book, newest first.
func ShareRequestsForBook(ctx context.Context, q Querier, bookID, uploaderID string) ([]*ShareRequest, error) {
	return listShareRequests(ctx, q, `WHERE q.book_id = ? AND q.uploader_id = ? ORDER BY q.created_at DESC, q.id LIMIT 50`, bookID, uploaderID)
}

// ListShareRequests returns a page of the review queue: pending oldest
// first, decided newest first.
func ListShareRequests(ctx context.Context, q Querier, status string, limit, offset int) ([]*ShareRequest, error) {
	order := `q.created_at, q.id`
	if status != "pending" {
		order = `q.decided_at DESC, q.id`
	}
	return listShareRequests(ctx, q, `WHERE q.status = ? ORDER BY `+order+` LIMIT ? OFFSET ?`, status, limit, offset)
}

// DecideShareRequest approves or rejects a pending request.
func DecideShareRequest(ctx context.Context, q Querier, id, status, reason, by string) error {
	now := NowMS()
	res, err := q.Exec(ctx, `UPDATE review_queue SET status = ?, reason = ?, decided_by = ?, decided_at = ?, updated_at = ?
		WHERE id = ? AND status = 'pending'`, status, nullStr(reason), by, now, now, id)
	if err := mustAffect(res, err); err != nil {
		if err == ErrNotFound {
			return ErrConflict
		}
		return err
	}
	return nil
}

// WithdrawShareRequest deletes an uploader's pending request.
func WithdrawShareRequest(ctx context.Context, q Querier, id, bookID, uploaderID string) error {
	res, err := q.Exec(ctx, `DELETE FROM review_queue WHERE id = ? AND book_id = ? AND uploader_id = ? AND status = 'pending'`,
		id, bookID, uploaderID)
	return mustAffect(res, err)
}

// GetDistribution returns one live distribution.
func GetDistribution(ctx context.Context, q Querier, id string) (*Distribution, error) {
	var (
		d        Distribution
		by, note sql.NullString
	)
	err := q.QueryRow(ctx, `SELECT id, book_id, target_type, target_id, distributed_by, note, created_at
		FROM distributions WHERE id = ? AND revoked_at IS NULL`, id).
		Scan(&d.ID, &d.BookID, &d.TargetType, &d.TargetID, &by, &note, &d.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	d.DistributedBy, d.Note = by.String, note.String
	return &d, nil
}

// ── Collection sharing ──

// CollectionShare is a collection shared with a user or group.
type CollectionShare struct {
	ID, CollectionID, TargetType, TargetID, TargetName string
	CreatedAt                                          int64
}

// ListCollectionShares lists who a collection is shared with.
func ListCollectionShares(ctx context.Context, q Querier, collectionID string) ([]*CollectionShare, error) {
	rows, err := q.Query(ctx, `SELECT s.id, s.collection_id, s.target_type, s.target_id, COALESCE(g.name, u.display_name, ''), s.created_at
		FROM collection_shares s
		LEFT JOIN groups g ON s.target_type = 'group' AND CAST(g.id AS TEXT) = s.target_id
		LEFT JOIN users u ON s.target_type = 'user' AND CAST(u.id AS TEXT) = s.target_id
		WHERE s.collection_id = ? ORDER BY s.created_at, s.id`, collectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*CollectionShare
	for rows.Next() {
		var s CollectionShare
		if err := rows.Scan(&s.ID, &s.CollectionID, &s.TargetType, &s.TargetID, &s.TargetName, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// AddCollectionShare shares a collection (idempotent per target) and returns the share id.
func AddCollectionShare(ctx context.Context, q Querier, collectionID, targetType, targetID string) (string, error) {
	now := NowMS()
	if _, err := q.Exec(ctx, `INSERT INTO collection_shares (id, collection_id, target_type, target_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (collection_id, target_type, target_id) DO NOTHING`,
		NewID(), collectionID, targetType, targetID, now, now); err != nil {
		return "", fmt.Errorf("share collection: %w", err)
	}
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM collection_shares WHERE collection_id = ? AND target_type = ? AND target_id = ?`,
		collectionID, targetType, targetID).Scan(&id)
	return id, err
}

// RemoveCollectionShare stops sharing a collection with one target.
func RemoveCollectionShare(ctx context.Context, q Querier, collectionID, shareID string) error {
	res, err := q.Exec(ctx, `DELETE FROM collection_shares WHERE id = ? AND collection_id = ?`, shareID, collectionID)
	return mustAffect(res, err)
}

// SharedCollection is someone else's collection shared with the user.
type SharedCollection struct {
	Collection
	Owner UserName
}

// SharedCollectionsFor lists live collections shared with a user directly
// or through their groups, with only the books the user can see.
func SharedCollectionsFor(ctx context.Context, q Querier, userID string) ([]*SharedCollection, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT c.id, c.name, c.description, c.updated_at, u.id, u.username, u.display_name
		FROM collections c JOIN collection_shares s ON s.collection_id = c.id JOIN users u ON u.id = c.owner_id
		WHERE c.deleted_at IS NULL AND c.owner_id <> ? AND u.status <> 'deleted'
			AND ((s.target_type = 'user' AND s.target_id = ?)
				OR (s.target_type = 'group' AND s.target_id IN (SELECT CAST(group_id AS TEXT) FROM group_members WHERE user_id = ?)))
		ORDER BY c.updated_at DESC, c.id LIMIT 200`, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	var out []*SharedCollection
	for rows.Next() {
		var (
			c    SharedCollection
			desc sql.NullString
		)
		if err := rows.Scan(&c.ID, &c.Name, &desc, &c.UpdatedAt, &c.Owner.ID, &c.Owner.Username, &c.Owner.DisplayName); err != nil {
			_ = rows.Close()
			return nil, err
		}
		c.Description = desc.String
		out = append(out, &c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for _, c := range out {
		ids, err := collectionBooks(ctx, q, userID, c.ID)
		if err != nil {
			return nil, err
		}
		c.BookIDs = ids
	}
	return out, nil
}

// CanSeeCollection reports whether a collection is the user's own or shared with them.
func CanSeeCollection(ctx context.Context, q Querier, userID, collectionID string) (bool, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM collections c WHERE c.id = ? AND c.deleted_at IS NULL AND (c.owner_id = ?
		OR EXISTS (SELECT 1 FROM collection_shares s WHERE s.collection_id = c.id AND ((s.target_type = 'user' AND s.target_id = ?)
			OR (s.target_type = 'group' AND s.target_id IN (SELECT CAST(group_id AS TEXT) FROM group_members WHERE user_id = ?)))))`,
		collectionID, userID, userID, userID).Scan(&n)
	if isInvalidUUID(err) {
		return false, nil
	}
	return n > 0, err
}
