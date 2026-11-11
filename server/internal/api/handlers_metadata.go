// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/metadata"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Metadata editing and the online pipeline (brief §6.12, ADR-0035).

var (
	asinRe     = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	languageRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

	errProvidersDown = apiErr(http.StatusBadGateway, "providers_unavailable", "the metadata providers can't be reached right now")
)

// canEditBook reports whether the principal may edit a book's metadata.
func canEditBook(p *auth.Principal, b *store.Book) bool {
	return !p.MustEnrollTOTP && authz.EditBookMetadata(p.Subject, b.UploaderID != "" && b.UploaderID == p.User.ID)
}

// bookEditor returns the book if the user may edit its metadata. People
// who can't see the book get 404, people who can but may not edit get 403.
func (s *Server) bookEditor(ctx context.Context, bookID string) (*auth.Principal, *store.Book, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, nil, err
	}
	it, err := s.visibleOrAdmin(ctx, p, bookID)
	if err != nil {
		return nil, nil, err
	}
	if it.Book.Status == "trashed" {
		return nil, nil, store.ErrNotFound
	}
	if !canEditBook(p, it.Book) {
		return nil, nil, errForbidden
	}
	return p, it.Book, nil
}

func tooLong(s *string, n int) bool { return s != nil && utf8.RuneCountInString(*s) > n }

// metaPatchFrom validates the request and builds the store patch.
func metaPatchFrom(b *BookMetadataPatch) (store.MetaPatch, []string, error) {
	var p store.MetaPatch
	trim := func(v *string) *string {
		if v == nil {
			return nil
		}
		t := strings.TrimSpace(*v)
		return &t
	}
	p.Title, p.Subtitle, p.Description, p.Publisher = trim(b.Title), trim(b.Subtitle), trim(b.Description), trim(b.Publisher)
	if p.Title != nil && *p.Title == "" {
		return p, nil, badRequest("title_required", "a book needs a title")
	}
	if tooLong(p.Title, 500) || tooLong(p.Subtitle, 500) || tooLong(p.Description, 20000) || tooLong(p.Publisher, 300) {
		return p, nil, badRequest("too_long", "that text is too long")
	}
	if b.PublishedYear != nil {
		if *b.PublishedYear < 0 || *b.PublishedYear > 2100 {
			return p, nil, badRequest("invalid_year", "the year must be between 0 and 2100")
		}
		p.PublishedYear = b.PublishedYear
	}
	if v := trim(b.Language); v != nil {
		l := strings.ToLower(*v)
		if l != "" && !languageRe.MatchString(l) {
			return p, nil, badRequest("invalid_language", "use a language code such as en or pt-BR")
		}
		p.Language = &l
	}
	if v := trim(b.Isbn); v != nil {
		n := ""
		if *v != "" {
			if n = metadata.NormalizeISBN(*v); n == "" {
				return p, nil, badRequest("invalid_isbn", "that isn't a valid ISBN")
			}
		}
		p.ISBN = &n
	}
	if v := trim(b.Asin); v != nil {
		a := strings.ToUpper(*v)
		if a != "" && !asinRe.MatchString(a) {
			return p, nil, badRequest("invalid_asin", "an ASIN has 10 letters and digits")
		}
		p.ASIN = &a
	}
	names := func(in *[]string, maxLen int) (*[]string, error) {
		if in == nil {
			return nil, nil
		}
		if len(*in) > 20 {
			return nil, badRequest("too_many", "at most 20 names")
		}
		out := []string{}
		for _, n := range *in {
			n = strings.Join(strings.Fields(n), " ")
			if utf8.RuneCountInString(n) > maxLen {
				return nil, badRequest("too_long", "that name is too long")
			}
			if n != "" {
				out = append(out, n)
			}
		}
		return &out, nil
	}
	var err error
	if p.Authors, err = names(b.Authors, 200); err != nil {
		return p, nil, err
	}
	if p.Narrators, err = names(b.Narrators, 200); err != nil {
		return p, nil, err
	}
	if p.Genres, err = names(b.Genres, 100); err != nil {
		return p, nil, err
	}
	if b.Series != nil {
		name, idx := strings.TrimSpace(b.Series.Name), strings.TrimSpace(deref(b.Series.Index))
		if utf8.RuneCountInString(name) > 300 || utf8.RuneCountInString(idx) > 20 {
			return p, nil, badRequest("too_long", "that text is too long")
		}
		p.Series = &store.SeriesValue{Name: name, Index: idx}
	}
	var unlock []string
	if b.Unlock != nil {
		for _, f := range *b.Unlock {
			if !slices.Contains(store.MetaFields, string(f)) {
				return p, nil, badRequest("invalid_field", "unknown field "+string(f))
			}
			unlock = append(unlock, string(f))
		}
	}
	return p, unlock, nil
}

func (s *Server) UpdateBookMetadata(ctx context.Context, req UpdateBookMetadataRequestObject) (UpdateBookMetadataResponseObject, error) {
	p, b, err := s.bookEditor(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	patch, unlock, err := metaPatchFrom(req.Body)
	if err != nil {
		return nil, err
	}
	fields := patch.Fields()
	if len(fields) == 0 && len(unlock) == 0 {
		return nil, badRequest("nothing_to_change", "send at least one field")
	}
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if len(unlock) > 0 {
			if err := store.UnlockFields(ctx, tx, b.ID, unlock); err != nil {
				return err
			}
		}
		if len(fields) > 0 {
			if err := store.UpdateBookMeta(ctx, tx, b.ID, patch, true); err != nil {
				return err
			}
			// Someone looked at it and decided: no longer "needs review".
			if err := store.SetMetadataReview(ctx, tx, b.ID, false, nil); err != nil {
				return err
			}
		}
		if err := store.RecordBookChange(ctx, tx, b.ID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "book.metadata_edit", ObjectType: "book", ObjectID: b.ID,
			IP: clientIP(ctx), Details: map[string]any{"fields": fields, "unlocked": unlock}})
	})
	if err != nil {
		return nil, err
	}
	res, err := s.GetBook(ctx, GetBookRequestObject{BookId: req.BookId})
	if err != nil {
		return nil, err
	}
	d, ok := res.(GetBook200JSONResponse)
	if !ok {
		return nil, store.ErrNotFound
	}
	return UpdateBookMetadata200JSONResponse(d), nil
}

// bookEditState adds the editor-only fields to a book detail.
func (s *Server) bookEditState(ctx context.Context, p *auth.Principal, b *store.Book, d *BookDetail) error {
	can := canEditBook(p, b)
	d.CanEdit = &can
	if !can {
		return nil
	}
	st, err := store.GetMetadataState(ctx, s.App().DB, b.ID)
	if err != nil {
		return err
	}
	locked := make([]MetadataField, 0, len(st.Locked))
	for _, f := range st.Locked {
		// Metadata fields only: the book tools' "chapters" lock isn't one.
		if slices.Contains(store.MetaFields, f) {
			locked = append(locked, MetadataField(f))
		}
	}
	d.LockedFields, d.NeedsReview = &locked, &st.NeedsReview
	return nil
}

func candidateOut(c metadata.Candidate) MetadataCandidate {
	out := MetadataCandidate{Provider: MetadataCandidateProvider(c.Provider), Score: float32(c.Score), Title: c.Title,
		Subtitle: strPtr(c.Subtitle), Authors: nonNil(c.Authors), Narrators: nonNil(c.Narrators), Genres: nonNil(c.Genres),
		Series: strPtr(c.Series), SeriesIndex: strPtr(c.SeriesIndex), Publisher: strPtr(c.Publisher), Description: strPtr(c.Description),
		Language: strPtr(c.Language), Isbn: strPtr(c.ISBN), Asin: strPtr(c.ASIN), CoverUrl: strPtr(c.CoverURL)}
	if c.PublishedYear > 0 {
		y := c.PublishedYear
		out.PublishedYear = &y
	}
	return out
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func (s *Server) ListMetadataCandidates(ctx context.Context, req ListMetadataCandidatesRequestObject) (ListMetadataCandidatesResponseObject, error) {
	_, b, err := s.bookEditor(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	title, author := strings.TrimSpace(deref(req.Params.Title)), strings.TrimSpace(deref(req.Params.Author))
	if utf8.RuneCountInString(title) > 300 || utf8.RuneCountInString(author) > 200 {
		return nil, badRequest("too_long", "that text is too long")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cands, err := s.App().Meta.Candidates(cctx, b.ID, metadata.Query{Title: title, Author: author})
	if err != nil {
		s.log().WarnContext(ctx, "metadata lookup failed", "book", b.ID, "err", err)
		return nil, errProvidersDown
	}
	if len(cands) > 15 {
		cands = cands[:15]
	}
	out := make(ListMetadataCandidates200JSONResponse, 0, len(cands))
	for _, c := range cands {
		out = append(out, candidateOut(c))
	}
	return out, nil
}

func (s *Server) SetBookCoverFromUrl(ctx context.Context, req SetBookCoverFromUrlRequestObject) (SetBookCoverFromUrlResponseObject, error) {
	p, b, err := s.bookEditor(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	u := strings.TrimSpace(req.Body.Url)
	e := app.Meta.Endpoints
	if len(u) > 2000 || !metadata.AllowedCoverURL(u, e.OpenLibraryCovers, e.GoogleBooks, e.Audnexus) {
		return nil, badRequest("bad_cover_url", "covers can only come from a metadata provider")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	img, err := e.FetchCover(cctx, u)
	if err != nil {
		if errors.Is(err, metadata.ErrCoverHost) {
			return nil, badRequest("bad_cover_url", "covers can only come from a metadata provider")
		}
		s.log().WarnContext(ctx, "cover download failed", "book", b.ID, "err", err)
		return nil, apiErr(http.StatusBadGateway, "cover_unavailable", "the cover couldn't be downloaded")
	}
	if err := app.Lib.SaveCover(ctx, b.ID, img); err != nil {
		return nil, err
	}
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: p.User.ID, Action: "book.cover_set", ObjectType: "book", ObjectID: b.ID,
		IP: clientIP(ctx), Details: map[string]any{"source": u}})
	return SetBookCoverFromUrl204Response{}, nil
}

func (s *Server) RefreshBookMetadata(ctx context.Context, req RefreshBookMetadataRequestObject) (RefreshBookMetadataResponseObject, error) {
	p, b, err := s.bookEditor(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	id, err := jobs.Enqueue(ctx, app.DB, metadata.JobMatch, metadata.MatchPayload{BookID: b.ID}, time.Time{}, p.User.ID)
	if err != nil {
		return nil, err
	}
	app.Jobs.Wake()
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: p.User.ID, Action: "book.metadata_refresh", ObjectType: "book", ObjectID: b.ID,
		IP: clientIP(ctx), Details: map[string]any{"job_id": id}})
	return RefreshBookMetadata202JSONResponse{JobId: mustUUID(id)}, nil
}

func (s *Server) ListMetadataReview(ctx context.Context, req ListMetadataReviewRequestObject) (ListMetadataReviewResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	offset, err := decodeCursor(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(req.Params.Limit, 50)
	db := s.App().DB
	books, err := store.ListMetadataReview(ctx, db, limit+1, offset)
	if err != nil {
		return nil, err
	}
	page := MetadataReviewPage{Items: make([]MetadataReviewItem, 0, len(books))}
	if len(books) > limit {
		books = books[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
	ids := make([]string, len(books))
	for i, b := range books {
		ids[i] = b.ID
	}
	authors, err := store.PeopleFor(ctx, db, "authors", ids)
	if err != nil {
		return nil, err
	}
	for _, b := range books {
		names := []string{}
		for _, a := range authors[b.ID] {
			names = append(names, a.Name)
		}
		it := MetadataReviewItem{Book: BookRef{Id: mustUUID(b.ID), Title: b.Title, HasCover: b.CoverPath != ""}, Authors: names,
			CreatedAt: msTime(b.CreatedAt)}
		if b.Score != nil {
			sc := float32(*b.Score)
			it.Score = &sc
		}
		page.Items = append(page.Items, it)
	}
	return ListMetadataReview200JSONResponse(page), nil
}

func (s *Server) DismissMetadataReview(ctx context.Context, req DismissMetadataReviewRequestObject) (DismissMetadataReviewResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if _, err := store.GetBook(ctx, tx, id); err != nil {
			return err
		}
		if err := store.SetMetadataReview(ctx, tx, id, false, nil); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "book.metadata_review_dismiss", ObjectType: "book", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return DismissMetadataReview204Response{}, nil
}
