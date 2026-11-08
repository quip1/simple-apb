// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/quip1/simple-apb/server/internal/store"
)

// JobMatch is the background job that looks a book up online.
const JobMatch = "metadata_match"

// MatchPayload names the book to match.
type MatchPayload struct {
	BookID string `json:"book_id"`
}

// Service runs the online part of the pipeline (brief §6.12): embedded tags
// came first (at import), providers fill only empty, unlocked fields, and
// hand edits (locked fields) always win.
type Service struct {
	DB        *store.DB
	Endpoints Endpoints
	// Config returns the enabled providers and threshold (instance settings).
	Config func(ctx context.Context) (Config, error)
	// SaveCover stores a JPEG as the book's cover.
	SaveCover func(ctx context.Context, bookID string, jpeg []byte) error
	Log       *slog.Logger
}

// Result describes one matching run.
type Result struct {
	Provider    string   `json:"provider,omitempty"`
	Score       float64  `json:"score"`
	Filled      []string `json:"filled,omitempty"`
	Cover       bool     `json:"cover,omitempty"`
	NeedsReview bool     `json:"needs_review"`
	Skipped     string   `json:"skipped,omitempty"`
}

// QueryFor builds the lookup query for a book.
func QueryFor(ctx context.Context, q store.Querier, b *store.Book) (Query, error) {
	authors, err := store.NamesOf(ctx, q, "authors", b.ID)
	if err != nil {
		return Query{}, err
	}
	out := Query{Title: b.Title, ISBN: b.ISBN, ASIN: b.ASIN}
	if len(authors) > 0 {
		out.Author = authors[0]
	}
	return out, nil
}

// Candidates looks a book up with the enabled providers (q overrides the
// book's own title/author when set).
func (s *Service) Candidates(ctx context.Context, bookID string, override Query) ([]Candidate, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	b, err := store.GetBook(ctx, s.DB, bookID)
	if err != nil {
		return nil, err
	}
	q, err := QueryFor(ctx, s.DB, b)
	if err != nil {
		return nil, err
	}
	if override.Title != "" || override.Author != "" {
		q = Query{Title: override.Title, Author: override.Author}
	}
	return Search(ctx, s.Endpoints.Providers(cfg), q)
}

// Match runs the pipeline for one book.
func (s *Service) Match(ctx context.Context, bookID string) (*Result, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	providers := s.Endpoints.Providers(cfg)
	if len(providers) == 0 {
		return &Result{Skipped: "no providers enabled"}, nil
	}
	b, err := store.GetBook(ctx, s.DB, bookID)
	if err != nil {
		return nil, err
	}
	if b.Status != "ready" {
		return &Result{Skipped: "book is " + b.Status}, nil
	}
	q, err := QueryFor(ctx, s.DB, b)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(q.Title) == "" && q.ISBN == "" && q.ASIN == "" {
		return &Result{Skipped: "nothing to search for"}, nil
	}
	cands, err := Search(ctx, providers, q)
	if err != nil {
		return nil, err // every provider failed: the job retries later
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultThreshold
	}
	res := &Result{}
	if len(cands) > 0 {
		res.Score, res.Provider = cands[0].Score, cands[0].Provider
	}
	var accepted []Candidate
	for _, c := range cands {
		if c.Score >= threshold {
			accepted = append(accepted, c)
		}
	}
	res.NeedsReview = len(accepted) == 0
	state, err := store.GetMetadataState(ctx, s.DB, bookID)
	if err != nil {
		return nil, err
	}
	var patch store.MetaPatch
	if !res.NeedsReview {
		if patch, err = fillPatch(ctx, s.DB, b, state.Locked, accepted); err != nil {
			return nil, err
		}
		res.Filled = patch.Fields()
	}
	score := res.Score
	err = s.DB.InTx(ctx, func(tx *store.Tx) error {
		if len(res.Filled) > 0 {
			if err := store.UpdateBookMeta(ctx, tx, bookID, patch, false); err != nil {
				return err
			}
			if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
				return err
			}
		}
		if err := store.SetMetadataReview(ctx, tx, bookID, res.NeedsReview, &score); err != nil {
			return err
		}
		action := "book.metadata_match"
		if res.NeedsReview {
			action = "book.metadata_needs_review"
		}
		return store.Audit(ctx, tx, store.AuditEntry{Action: action, ObjectType: "book", ObjectID: bookID,
			Details: map[string]any{"provider": res.Provider, "score": res.Score, "filled": res.Filled}})
	})
	if err != nil {
		return nil, err
	}
	if !res.NeedsReview && b.CoverPath == "" && s.SaveCover != nil {
		for _, c := range accepted {
			if c.CoverURL == "" {
				continue
			}
			img, err := s.Endpoints.FetchCover(ctx, c.CoverURL)
			if err == nil {
				err = s.SaveCover(ctx, bookID, img)
			}
			if err == nil {
				res.Cover = true
				break
			}
			s.log().Warn("cover from provider failed", "book", bookID, "provider", c.Provider, "err", err)
		}
	}
	return res, nil
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// fillPatch takes, for every empty and unlocked field, the value from the
// best accepted candidate that has one.
func fillPatch(ctx context.Context, q store.Querier, b *store.Book, locked []string, cands []Candidate) (store.MetaPatch, error) {
	var p store.MetaPatch
	authors, err := store.NamesOf(ctx, q, "authors", b.ID)
	if err != nil {
		return p, err
	}
	narrators, err := store.NamesOf(ctx, q, "narrators", b.ID)
	if err != nil {
		return p, err
	}
	genres, err := store.GenresOf(ctx, q, b.ID)
	if err != nil {
		return p, err
	}
	open := func(field string, empty bool) bool { return empty && !slices.Contains(locked, field) }
	pickStr := func(field, current string, get func(Record) string) *string {
		if !open(field, strings.TrimSpace(current) == "") {
			return nil
		}
		for _, c := range cands {
			if v := strings.TrimSpace(get(c.Record)); v != "" {
				return &v
			}
		}
		return nil
	}
	pickList := func(field string, current []string, get func(Record) []string) *[]string {
		if !open(field, len(current) == 0) {
			return nil
		}
		for _, c := range cands {
			if v := get(c.Record); len(v) > 0 {
				return &v
			}
		}
		return nil
	}
	p.Subtitle = pickStr(store.FieldSubtitle, b.Subtitle, func(r Record) string { return r.Subtitle })
	p.Description = pickStr(store.FieldDescription, b.Description, func(r Record) string { return r.Description })
	p.Publisher = pickStr(store.FieldPublisher, b.Publisher, func(r Record) string { return r.Publisher })
	p.Language = pickStr(store.FieldLanguage, b.Language, func(r Record) string { return r.Language })
	p.ISBN = pickStr(store.FieldISBN, b.ISBN, func(r Record) string { return r.ISBN })
	p.ASIN = pickStr(store.FieldASIN, b.ASIN, func(r Record) string { return r.ASIN })
	p.Authors = pickList(store.FieldAuthors, authors, func(r Record) []string { return r.Authors })
	p.Narrators = pickList(store.FieldNarrators, narrators, func(r Record) []string { return r.Narrators })
	p.Genres = pickList(store.FieldGenres, genres, func(r Record) []string { return r.Genres })
	if open(store.FieldPublishedYear, b.PublishedYear == 0) {
		for _, c := range cands {
			if c.PublishedYear > 0 {
				y := c.PublishedYear
				p.PublishedYear = &y
				break
			}
		}
	}
	if open(store.FieldSeries, b.SeriesID == "") {
		for _, c := range cands {
			if c.Series != "" {
				p.Series = &store.SeriesValue{Name: c.Series, Index: c.SeriesIndex}
				break
			}
		}
	}
	return p, nil
}
