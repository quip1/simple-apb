// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Metadata fields that can be edited and locked (brief §6.12).
const (
	FieldTitle         = "title"
	FieldSubtitle      = "subtitle"
	FieldDescription   = "description"
	FieldPublisher     = "publisher"
	FieldPublishedYear = "published_year"
	FieldLanguage      = "language"
	FieldISBN          = "isbn"
	FieldASIN          = "asin"
	FieldAuthors       = "authors"
	FieldNarrators     = "narrators"
	FieldSeries        = "series"
	FieldGenres        = "genres"
)

// MetaFields lists every editable field in a stable order.
var MetaFields = []string{FieldTitle, FieldSubtitle, FieldDescription, FieldPublisher, FieldPublishedYear, FieldLanguage,
	FieldISBN, FieldASIN, FieldAuthors, FieldNarrators, FieldSeries, FieldGenres}

// SeriesValue is a series name and position ("" name = no series).
type SeriesValue struct{ Name, Index string }

// MetaPatch changes some metadata fields; nil means "leave alone".
type MetaPatch struct {
	Title, Subtitle, Description, Publisher, Language, ISBN, ASIN *string
	PublishedYear                                                 *int
	Authors, Narrators, Genres                                    *[]string
	Series                                                        *SeriesValue
}

// Fields lists the fields the patch sets.
func (p MetaPatch) Fields() []string {
	var out []string
	add := func(set bool, f string) {
		if set {
			out = append(out, f)
		}
	}
	add(p.Title != nil, FieldTitle)
	add(p.Subtitle != nil, FieldSubtitle)
	add(p.Description != nil, FieldDescription)
	add(p.Publisher != nil, FieldPublisher)
	add(p.PublishedYear != nil, FieldPublishedYear)
	add(p.Language != nil, FieldLanguage)
	add(p.ISBN != nil, FieldISBN)
	add(p.ASIN != nil, FieldASIN)
	add(p.Authors != nil, FieldAuthors)
	add(p.Narrators != nil, FieldNarrators)
	add(p.Series != nil, FieldSeries)
	add(p.Genres != nil, FieldGenres)
	return out
}

// UpdateBookMeta applies a patch. With lock, the changed fields join the
// book's locked fields so providers never overwrite them.
func UpdateBookMeta(ctx context.Context, q Querier, id string, p MetaPatch, lock bool) error {
	sets, args := []string{}, []any{}
	str := func(col string, v *string) {
		if v != nil {
			sets, args = append(sets, col+" = ?"), append(args, nullStr(strings.TrimSpace(*v)))
		}
	}
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		sets, args = append(sets, "title = ?", "sort_title = ?"), append(args, t, SortTitle(t))
	}
	str("subtitle", p.Subtitle)
	str("description", p.Description)
	str("publisher", p.Publisher)
	str("language", p.Language)
	str("isbn", p.ISBN)
	str("asin", p.ASIN)
	if p.PublishedYear != nil {
		var y any
		if *p.PublishedYear > 0 {
			y = *p.PublishedYear
		}
		sets, args = append(sets, "published_year = ?"), append(args, y)
	}
	if p.Series != nil {
		var sid any
		if name := strings.TrimSpace(p.Series.Name); name != "" {
			id, err := ensureNamed(ctx, q, "series", name)
			if err != nil {
				return err
			}
			sid = id
		}
		idx := ""
		if sid != nil {
			idx = strings.TrimSpace(p.Series.Index)
		}
		sets, args = append(sets, "series_id = ?", "series_index = ?", "series_order = ?"), append(args, sid, nullStr(idx), seriesOrderArg(idx))
	}
	if lock {
		locked, err := LockedFields(ctx, q, id)
		if err != nil {
			return err
		}
		for _, f := range p.Fields() {
			if !slices.Contains(locked, f) {
				locked = append(locked, f)
			}
		}
		raw, _ := json.Marshal(sortFields(locked))
		sets, args = append(sets, "locked_fields = ?"), append(args, string(raw))
	}
	sets, args = append(sets, "updated_at = ?"), append(args, NowMS(), id)
	res, err := q.Exec(ctx, `UPDATE books SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err := mustAffect(res, err); err != nil {
		return err
	}
	if p.Authors != nil {
		if err := setPeople(ctx, q, id, "authors", "book_authors", "author_id", *p.Authors); err != nil {
			return err
		}
	}
	if p.Narrators != nil {
		if err := setPeople(ctx, q, id, "narrators", "book_narrators", "narrator_id", *p.Narrators); err != nil {
			return err
		}
	}
	if p.Genres != nil {
		if _, err := q.Exec(ctx, `DELETE FROM book_tag_assignments WHERE book_id = ?
			AND book_tag_id IN (SELECT id FROM book_tags WHERE kind = 'genre')`, id); err != nil {
			return err
		}
		for _, g := range *p.Genres {
			if err := addGenre(ctx, q, id, g); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortFields(fs []string) []string {
	out := []string{}
	for _, f := range MetaFields {
		if slices.Contains(fs, f) {
			out = append(out, f)
		}
	}
	// Other locks (the book tools' "chapters") keep their place after them.
	var rest []string
	for _, f := range fs {
		if !slices.Contains(MetaFields, f) && !slices.Contains(rest, f) {
			rest = append(rest, f)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// UnlockFields lets providers fill the given fields again.
func UnlockFields(ctx context.Context, q Querier, id string, fields []string) error {
	locked, err := LockedFields(ctx, q, id)
	if err != nil {
		return err
	}
	locked = slices.DeleteFunc(locked, func(f string) bool { return slices.Contains(fields, f) })
	raw, _ := json.Marshal(sortFields(locked))
	_, err = q.Exec(ctx, `UPDATE books SET locked_fields = ?, updated_at = ? WHERE id = ?`, string(raw), NowMS(), id)
	return err
}

// MetadataState is a book's matching state.
type MetadataState struct {
	Locked      []string
	NeedsReview bool
	Score       *float64
}

// GetMetadataState returns locked fields, the needs-review flag and the last score.
func GetMetadataState(ctx context.Context, q Querier, id string) (*MetadataState, error) {
	var (
		raw    string
		review bool
		score  sql.NullFloat64
	)
	if err := q.QueryRow(ctx, `SELECT locked_fields, metadata_review, metadata_score FROM books WHERE id = ?`, id).
		Scan(&raw, &review, &score); err != nil {
		return nil, notFound(err)
	}
	s := &MetadataState{Locked: []string{}, NeedsReview: review}
	if err := json.Unmarshal([]byte(raw), &s.Locked); err != nil {
		return nil, fmt.Errorf("locked_fields: %w", err)
	}
	if score.Valid {
		s.Score = &score.Float64
	}
	return s, nil
}

// SetMetadataReview sets the needs-review flag and, when given, the match score.
func SetMetadataReview(ctx context.Context, q Querier, id string, review bool, score *float64) error {
	if score != nil {
		_, err := q.Exec(ctx, `UPDATE books SET metadata_review = ?, metadata_score = ? WHERE id = ?`, review, *score, id)
		return err
	}
	_, err := q.Exec(ctx, `UPDATE books SET metadata_review = ? WHERE id = ?`, review, id)
	return err
}

// ReviewBook is a book flagged "needs review".
type ReviewBook struct {
	Book
	Score *float64
}

// ListMetadataReview returns a page of books needing a metadata review, oldest first.
func ListMetadataReview(ctx context.Context, q Querier, limit, offset int) ([]*ReviewBook, error) {
	rows, err := q.Query(ctx, `SELECT `+bookCols+`, b.metadata_score`+bookFrom+`
		WHERE b.metadata_review = ? AND b.status IN ('ready', 'processing') ORDER BY b.created_at, b.id LIMIT ? OFFSET ?`, true, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*ReviewBook
	for rows.Next() {
		var score sql.NullFloat64
		b, err := scanBook(rows, &score)
		if err != nil {
			return nil, err
		}
		rb := &ReviewBook{Book: *b}
		if score.Valid {
			rb.Score = &score.Float64
		}
		out = append(out, rb)
	}
	return out, rows.Err()
}

// GenresOf returns a book's genre names.
func GenresOf(ctx context.Context, q Querier, bookID string) ([]string, error) {
	tags, err := TagsFor(ctx, q, bookID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, t := range tags {
		if t.Kind == "genre" {
			out = append(out, t.Name)
		}
	}
	return out, nil
}

// NamesOf returns a book's authors or narrators in order (kind "authors" or "narrators").
func NamesOf(ctx context.Context, q Querier, kind, bookID string) ([]string, error) {
	m, err := PeopleFor(ctx, q, kind, []string{bookID})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, n := range m[bookID] {
		out = append(out, n.Name)
	}
	return out, nil
}
