// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// BookMeta is descriptive metadata.
type BookMeta struct {
	Title, Subtitle, Description, Publisher, Language, ISBN, ASIN string
	PublishedYear                                                 int
	Authors, Narrators, Genres                                    []string
	Series, SeriesIndex                                           string
}

// BookFile is one audio file of a book.
type BookFile struct {
	ID, Path, Codec, MimeType, Checksum string
	Index                               int
	OffsetMS, DurationMS, SizeBytes     int64
	BitrateKbps                         int
}

// Chapter is a chapter on the global book timeline.
type Chapter struct {
	Index          int
	Title          string
	StartMS, EndMS int64
}

// NewBook is everything needed to insert a master-store book.
type NewBook struct {
	Meta                                            BookMeta
	FolderPath, CoverPath, EpubPath, Origin, Status string
	UploaderID, Checksum                            string
	DurationMS, SizeBytes                           int64
	Files                                           []BookFile
	Chapters                                        []Chapter
}

// SortTitle drops a leading article for sorting.
func SortTitle(t string) string {
	l := strings.ToLower(strings.TrimSpace(t))
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, a) && len(l) > len(a) {
			return strings.TrimSpace(l[len(a):])
		}
	}
	return l
}

// InsertBook writes a book with its files, chapters, people, series and genres.
func InsertBook(ctx context.Context, q Querier, b *NewBook) (string, error) {
	id, now := NewID(), NowMS()
	var seriesID any
	if b.Meta.Series != "" {
		sid, err := ensureNamed(ctx, q, "series", b.Meta.Series)
		if err != nil {
			return "", err
		}
		seriesID = sid
	}
	var year any
	if b.Meta.PublishedYear > 0 {
		year = b.Meta.PublishedYear
	}
	status := b.Status
	if status == "" {
		status = "ready"
	}
	_, err := q.Exec(ctx, `INSERT INTO books (id, title, sort_title, subtitle, series_id, series_index, series_order, description, publisher,
		published_year, language, isbn, asin, duration_ms, size_bytes, cover_path, uploader_id, origin, status,
		checksum_sha256, folder_path, epub_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, b.Meta.Title, SortTitle(b.Meta.Title), nullStr(b.Meta.Subtitle), seriesID, nullStr(b.Meta.SeriesIndex), seriesOrderArg(b.Meta.SeriesIndex),
		nullStr(b.Meta.Description), nullStr(b.Meta.Publisher), year, nullStr(b.Meta.Language), nullStr(b.Meta.ISBN),
		nullStr(b.Meta.ASIN), b.DurationMS, b.SizeBytes, nullStr(b.CoverPath), nullStr(b.UploaderID), b.Origin, status,
		nullStr(b.Checksum), b.FolderPath, nullStr(b.EpubPath), now, now)
	if IsUniqueViolation(err) {
		return "", ErrConflict
	}
	if err != nil {
		return "", fmt.Errorf("insert book: %w", err)
	}
	if err := setPeople(ctx, q, id, "authors", "book_authors", "author_id", b.Meta.Authors); err != nil {
		return "", err
	}
	if err := setPeople(ctx, q, id, "narrators", "book_narrators", "narrator_id", b.Meta.Narrators); err != nil {
		return "", err
	}
	for _, g := range b.Meta.Genres {
		if err := addGenre(ctx, q, id, g); err != nil {
			return "", err
		}
	}
	if err := ReplaceBookMedia(ctx, q, id, b.Files, b.Chapters); err != nil {
		return "", err
	}
	return id, nil
}

// ReplaceBookMedia rewrites a book's files and chapters (used on import and re-scan).
func ReplaceBookMedia(ctx context.Context, q Querier, bookID string, files []BookFile, chapters []Chapter) error {
	if _, err := q.Exec(ctx, `DELETE FROM book_files WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	now := NowMS()
	for i := range files {
		f := &files[i]
		if f.ID == "" {
			f.ID = NewID()
		}
		var br any
		if f.BitrateKbps > 0 {
			br = f.BitrateKbps
		}
		if _, err := q.Exec(ctx, `INSERT INTO book_files (id, book_id, path, idx, offset_ms, duration_ms, size_bytes, codec, bitrate,
			mime_type, checksum, verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.ID, bookID, f.Path, f.Index, f.OffsetMS, f.DurationMS, f.SizeBytes, f.Codec, br, f.MimeType, f.Checksum, now, now, now); err != nil {
			return fmt.Errorf("insert book file: %w", err)
		}
	}
	for _, c := range chapters {
		if _, err := q.Exec(ctx, `INSERT INTO chapters (id, book_id, idx, title, start_ms, end_ms, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, NewID(), bookID, c.Index, c.Title, c.StartMS, c.EndMS, now, now); err != nil {
			return fmt.Errorf("insert chapter: %w", err)
		}
	}
	return nil
}

// ensureNamed finds or creates a row by case-insensitive name in series/authors/narrators.
func ensureNamed(ctx context.Context, q Querier, table, name string) (string, error) {
	name = strings.TrimSpace(name)
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM `+table+` WHERE lower(name) = ?`, strings.ToLower(name)).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id, now := NewID(), NowMS()
	_, err = q.Exec(ctx, `INSERT INTO `+table+` (id, name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, strings.ToLower(name), now, now)
	if err != nil {
		return "", fmt.Errorf("insert %s: %w", table, err)
	}
	return id, nil
}

func setPeople(ctx context.Context, q Querier, bookID, table, join, col string, names []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM `+join+` WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	seen := map[string]bool{}
	pos := 0
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		pid, err := ensureNamed(ctx, q, table, n)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO `+join+` (book_id, `+col+`, position) VALUES (?, ?, ?)`, bookID, pid, pos); err != nil {
			return err
		}
		pos++
	}
	return nil
}

func addGenre(ctx context.Context, q Querier, bookID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var tagID string
	err := q.QueryRow(ctx, `SELECT id FROM book_tags WHERE kind = 'genre' AND lower(name) = ?`, strings.ToLower(name)).Scan(&tagID)
	now := NowMS()
	if errors.Is(err, sql.ErrNoRows) {
		tagID = NewID()
		if _, err := q.Exec(ctx, `INSERT INTO book_tags (id, name, kind, created_at, updated_at) VALUES (?, ?, 'genre', ?, ?)`,
			tagID, name, now, now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO book_tag_assignments (id, book_id, book_tag_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (book_id, book_tag_id) DO NOTHING`, NewID(), bookID, tagID, now, now)
	return err
}

// Book is a master-store row.
type Book struct {
	ID, Title, Subtitle, Description, Publisher, Language, ISBN, ASIN string
	SeriesID, SeriesName, SeriesIndex                                 string
	PublishedYear                                                     int64
	DurationMS, SizeBytes                                             int64
	CoverPath, UploaderID, Origin, Status, StatusReason               string
	Checksum, FolderPath, EpubPath                                    string
	TrashedAt                                                         *int64
	CreatedAt, UpdatedAt                                              int64
}

const bookCols = `b.id, b.title, b.subtitle, b.description, b.publisher, b.language, b.isbn, b.asin, b.series_id, s.name,
	b.series_index, b.published_year, b.duration_ms, b.size_bytes, b.cover_path, b.uploader_id, b.origin, b.status,
	b.status_reason, b.checksum_sha256, b.folder_path, b.epub_path, b.trashed_at, b.created_at, b.updated_at`

const bookFrom = ` FROM books b LEFT JOIN series s ON s.id = b.series_id`

func scanBook(row interface{ Scan(...any) error }, extra ...any) (*Book, error) {
	var (
		b                                                    Book
		sub, desc, pub, lang, isbn, asin, sid, sname, sindex sql.NullString
		cover, uploader, reason, checksum, epub              sql.NullString
		year, trashed                                        sql.NullInt64
	)
	dest := []any{&b.ID, &b.Title, &sub, &desc, &pub, &lang, &isbn, &asin, &sid, &sname, &sindex, &year,
		&b.DurationMS, &b.SizeBytes, &cover, &uploader, &b.Origin, &b.Status, &reason, &checksum, &b.FolderPath, &epub,
		&trashed, &b.CreatedAt, &b.UpdatedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, notFound(err)
	}
	b.Subtitle, b.Description, b.Publisher, b.Language = sub.String, desc.String, pub.String, lang.String
	b.ISBN, b.ASIN, b.SeriesID, b.SeriesName, b.SeriesIndex = isbn.String, asin.String, sid.String, sname.String, sindex.String
	b.PublishedYear, b.CoverPath, b.UploaderID, b.StatusReason = year.Int64, cover.String, uploader.String, reason.String
	b.Checksum, b.EpubPath, b.TrashedAt = checksum.String, epub.String, ptr(trashed)
	return &b, nil
}

// GetBook returns a master-store book (any status).
func GetBook(ctx context.Context, q Querier, id string) (*Book, error) {
	return scanBook(q.QueryRow(ctx, `SELECT `+bookCols+bookFrom+` WHERE b.id = ?`, id))
}

// FindBookByChecksum returns a non-trashed book with this whole-book checksum.
func FindBookByChecksum(ctx context.Context, q Querier, checksum string) (*Book, error) {
	return scanBook(q.QueryRow(ctx, `SELECT `+bookCols+bookFrom+` WHERE b.checksum_sha256 = ? AND b.status <> 'trashed'
		ORDER BY b.created_at LIMIT 1`, checksum))
}

// BookFolder is a known folder with its file sizes (for the scanner).
type BookFolder struct {
	ID, FolderPath, Status, Origin string
	FileSizes                      map[string]int64 // path → size
}

// ListBookFolders returns every book's folder and file sizes.
func ListBookFolders(ctx context.Context, q Querier) (map[string]*BookFolder, error) {
	rows, err := q.Query(ctx, `SELECT b.id, b.folder_path, b.status, b.origin, f.path, f.size_bytes
		FROM books b LEFT JOIN book_files f ON f.book_id = b.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]*BookFolder{}
	for rows.Next() {
		var (
			id, folder, status, origin string
			path                       sql.NullString
			size                       sql.NullInt64
		)
		if err := rows.Scan(&id, &folder, &status, &origin, &path, &size); err != nil {
			return nil, err
		}
		bf := out[folder]
		if bf == nil {
			bf = &BookFolder{ID: id, FolderPath: folder, Status: status, Origin: origin, FileSizes: map[string]int64{}}
			out[folder] = bf
		}
		if path.Valid {
			bf.FileSizes[path.String] = size.Int64
		}
	}
	return out, rows.Err()
}

// SetBookStatus changes a book's status (with reason). trashed sets trashed_at.
func SetBookStatus(ctx context.Context, q Querier, id, status, reason string) error {
	var trashed any
	if status == "trashed" {
		trashed = NowMS()
	}
	res, err := q.Exec(ctx, `UPDATE books SET status = ?, status_reason = ?, trashed_at = ?, updated_at = ? WHERE id = ?`,
		status, nullStr(reason), trashed, NowMS(), id)
	return mustAffect(res, err)
}

// UpdateBookTotals sets duration/size/checksum after a media re-import.
func UpdateBookTotals(ctx context.Context, q Querier, id string, durationMS, size int64, checksum string) error {
	_, err := q.Exec(ctx, `UPDATE books SET duration_ms = ?, size_bytes = ?, checksum_sha256 = ?, updated_at = ? WHERE id = ?`,
		durationMS, size, nullStr(checksum), NowMS(), id)
	return err
}

// SetCoverPath stores the cached cover path.
func SetCoverPath(ctx context.Context, q Querier, id, path string) error {
	_, err := q.Exec(ctx, `UPDATE books SET cover_path = ?, updated_at = ? WHERE id = ?`, nullStr(path), NowMS(), id)
	return err
}

// DeleteBook removes a book row (cascades files, chapters, entries…). Files on disk are the caller's job.
func DeleteBook(ctx context.Context, q Querier, id string) error {
	res, err := q.Exec(ctx, `DELETE FROM books WHERE id = ?`, id)
	return mustAffect(res, err)
}

// TrashedBefore lists trashed books older than a cutoff.
func TrashedBefore(ctx context.Context, q Querier, cutoffMS int64) ([]*Book, error) {
	return listBooks(ctx, q, ` WHERE b.status = 'trashed' AND b.trashed_at IS NOT NULL AND b.trashed_at <= ? ORDER BY b.trashed_at`, cutoffMS)
}

// MasterFilter filters the admin master-store browser.
type MasterFilter struct {
	Status, Query string
	Limit, Offset int
}

// ListMasterBooks lists master-store books for admins.
func ListMasterBooks(ctx context.Context, q Querier, f MasterFilter) ([]*Book, error) {
	where, args := []string{"1 = 1"}, []any{}
	if f.Status != "" {
		where, args = append(where, "b.status = ?"), append(args, f.Status)
	}
	if f.Query != "" {
		where, args = append(where, "lower(b.title) LIKE ? ESCAPE '\\'"), append(args, likePattern(f.Query))
	}
	args = append(args, f.Limit, f.Offset)
	return listBooks(ctx, q, ` WHERE `+strings.Join(where, " AND ")+` ORDER BY b.created_at DESC, b.id LIMIT ? OFFSET ?`, args...)
}

func listBooks(ctx context.Context, q Querier, tail string, args ...any) ([]*Book, error) {
	rows, err := q.Query(ctx, `SELECT `+bookCols+bookFrom+tail, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// likePattern escapes LIKE wildcards and wraps in %…%.
func likePattern(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
	return "%" + s + "%"
}

// GetBookFiles returns files in play order.
func GetBookFiles(ctx context.Context, q Querier, bookID string) ([]BookFile, error) {
	rows, err := q.Query(ctx, `SELECT id, path, idx, offset_ms, duration_ms, size_bytes, codec, bitrate, mime_type, checksum
		FROM book_files WHERE book_id = ? ORDER BY idx`, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []BookFile
	for rows.Next() {
		var (
			f  BookFile
			br sql.NullInt64
		)
		if err := rows.Scan(&f.ID, &f.Path, &f.Index, &f.OffsetMS, &f.DurationMS, &f.SizeBytes, &f.Codec, &br, &f.MimeType, &f.Checksum); err != nil {
			return nil, err
		}
		f.BitrateKbps = int(br.Int64)
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetChapters returns chapters in order.
func GetChapters(ctx context.Context, q Querier, bookID string) ([]Chapter, error) {
	rows, err := q.Query(ctx, `SELECT idx, title, start_ms, end_ms FROM chapters WHERE book_id = ? ORDER BY idx`, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Chapter
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.Index, &c.Title, &c.StartMS, &c.EndMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Named is an id + name (author, narrator, series, tag).
type Named struct{ ID, Name, Kind string }

// PeopleFor returns authors or narrators for a set of books, in position order.
func PeopleFor(ctx context.Context, q Querier, kind string, bookIDs []string) (map[string][]Named, error) {
	table, join, col := "authors", "book_authors", "author_id"
	if kind == "narrators" {
		table, join, col = "narrators", "book_narrators", "narrator_id"
	}
	out := map[string][]Named{}
	if len(bookIDs) == 0 {
		return out, nil
	}
	ph, args := inList(bookIDs)
	rows, err := q.Query(ctx, `SELECT j.book_id, p.id, p.name FROM `+join+` j JOIN `+table+` p ON p.id = j.`+col+`
		WHERE j.book_id IN (`+ph+`) ORDER BY j.book_id, j.position`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var bid string
		var n Named
		if err := rows.Scan(&bid, &n.ID, &n.Name); err != nil {
			return nil, err
		}
		out[bid] = append(out[bid], n)
	}
	return out, rows.Err()
}

// TagsFor returns book tags for a book.
func TagsFor(ctx context.Context, q Querier, bookID string) ([]Named, error) {
	rows, err := q.Query(ctx, `SELECT t.id, t.name, t.kind FROM book_tags t JOIN book_tag_assignments a ON a.book_tag_id = t.id
		WHERE a.book_id = ? ORDER BY t.kind, lower(t.name)`, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Named
	for rows.Next() {
		var n Named
		if err := rows.Scan(&n.ID, &n.Name, &n.Kind); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNamed returns an author/narrator/series row by id.
func GetNamed(ctx context.Context, q Querier, table, id string) (*Named, error) {
	switch table {
	case "authors", "narrators", "series":
	default:
		return nil, fmt.Errorf("bad table %q", table)
	}
	var n Named
	if err := q.QueryRow(ctx, `SELECT id, name FROM `+table+` WHERE id = ?`, id).Scan(&n.ID, &n.Name); err != nil {
		return nil, notFound(err)
	}
	return &n, nil
}

func inList(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", "), args
}

// ── Locked metadata (manual edits win) ──

// LockedFields returns the hand-edited fields of a book.
func LockedFields(ctx context.Context, q Querier, id string) ([]string, error) {
	var raw string
	if err := q.QueryRow(ctx, `SELECT locked_fields FROM books WHERE id = ?`, id).Scan(&raw); err != nil {
		return nil, notFound(err)
	}
	var out []string
	err := json.Unmarshal([]byte(raw), &out)
	return out, err
}
