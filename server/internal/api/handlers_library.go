// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/version"
)

// Cursors are opaque to clients; today they wrap an offset (ADR-0014).
type pageCursor struct {
	Offset int `json:"o"`
}

func encodeCursor(offset int) *string {
	b, _ := json.Marshal(pageCursor{Offset: offset})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(c *string) (int, error) {
	if c == nil || *c == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(*c)
	var pc pageCursor
	if err != nil || json.Unmarshal(b, &pc) != nil || pc.Offset < 0 {
		return 0, badRequest("invalid_cursor", "the cursor is not valid")
	}
	return pc.Offset, nil
}

func limitOf(l *int, def int) int {
	if l == nil || *l <= 0 {
		return def
	}
	return min(*l, 200)
}

func coverURL(id string) string { return "/api/" + version.APIVersion + "/books/" + id + "/cover" }

func personRefs(ns []store.Named) []PersonRef {
	out := make([]PersonRef, 0, len(ns))
	for _, n := range ns {
		out = append(out, PersonRef{Id: mustUUID(n.ID), Name: n.Name})
	}
	return out
}

func seriesRef(b *store.Book) *SeriesRef {
	if b.SeriesID == "" {
		return nil
	}
	return &SeriesRef{Id: mustUUID(b.SeriesID), Name: b.SeriesName, Index: strPtr(b.SeriesIndex)}
}

func progressOut(bookID string, it *store.LibraryItem) *Progress {
	if it.ProgressMS == nil {
		return nil
	}
	p := &Progress{BookId: mustUUID(bookID), PositionMs: *it.ProgressMS, DeviceId: uuidOrNil(it.ProgressDevice),
		Speed: float32(it.ProgressSpeed), FinishedAt: msTimePtr(it.ProgressFinished)}
	if it.ProgressClientTS != nil {
		p.ClientTs = msTime(*it.ProgressClientTS)
	}
	if it.ProgressUpdatedAt != nil {
		p.UpdatedAt = msTime(*it.ProgressUpdatedAt)
	}
	return p
}

func (s *Server) summaries(ctx context.Context, items []*store.LibraryItem) ([]BookSummary, error) {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.Book.ID
	}
	db := s.App().DB
	authors, err := store.PeopleFor(ctx, db, "authors", ids)
	if err != nil {
		return nil, err
	}
	narrators, err := store.PeopleFor(ctx, db, "narrators", ids)
	if err != nil {
		return nil, err
	}
	out := make([]BookSummary, 0, len(items))
	for _, it := range items {
		b := it.Book
		hidden := it.HiddenAt != nil
		added := msTime(it.AddedAt)
		out = append(out, BookSummary{
			Id: mustUUID(b.ID), Title: b.Title, Subtitle: strPtr(b.Subtitle), Authors: personRefs(authors[b.ID]),
			Narrators: personRefs(narrators[b.ID]), Series: seriesRef(b), DurationMs: b.DurationMS, CoverUrl: coverURL(b.ID), HasCover: b.CoverPath != "",
			Source: LibrarySource(it.Source), Progress: progressOut(b.ID, it), AddedAt: &added, Hidden: &hidden,
			RemovedAt: msTimePtr(it.RemovedAt), UpdatedAt: msTime(b.UpdatedAt),
		})
	}
	return out, nil
}

func uuidStr(u *openapi_types.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func (s *Server) ListLibrary(ctx context.Context, req ListLibraryRequestObject) (ListLibraryResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	pr := req.Params
	offset, err := decodeCursor(pr.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(pr.Limit, 50)
	f := store.LibraryFilter{SeriesID: uuidStr(pr.Series), AuthorID: uuidStr(pr.Author), NarratorID: uuidStr(pr.Narrator),
		TagID: uuidStr(pr.Tag), CollectionID: uuidStr(pr.Collection), Query: deref(pr.Q), Limit: limit + 1, Offset: offset}
	if pr.Shelf != nil {
		f.Shelf = string(*pr.Shelf)
	}
	if pr.Sort != nil {
		f.Sort = string(*pr.Sort)
	}
	items, err := store.ListLibrary(ctx, s.App().DB, p.User.ID, f)
	if err != nil {
		return nil, err
	}
	page := LibraryPage{}
	if len(items) > limit {
		items = items[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
	if page.Items, err = s.summaries(ctx, items); err != nil {
		return nil, err
	}
	return ListLibrary200JSONResponse(page), nil
}

// visibleOrAdmin returns the user's view of a book; admins may open any
// master-store book that isn't visible to them.
func (s *Server) visibleOrAdmin(ctx context.Context, p *auth.Principal, bookID string) (*store.LibraryItem, error) {
	db := s.App().DB
	it, err := store.VisibleBook(ctx, db, p.User.ID, bookID)
	if errors.Is(err, store.ErrNotFound) && p.Can(authz.Admin) {
		b, err := store.GetBook(ctx, db, bookID)
		if err != nil {
			return nil, err
		}
		return &store.LibraryItem{Book: b, Source: "distribution", AddedAt: b.CreatedAt}, nil
	}
	return it, err
}

func (s *Server) GetBook(ctx context.Context, req GetBookRequestObject) (GetBookResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	it, err := s.visibleOrAdmin(ctx, p, id)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	sums, err := s.summaries(ctx, []*store.LibraryItem{it})
	if err != nil {
		return nil, err
	}
	files, err := store.GetBookFiles(ctx, db, id)
	if err != nil {
		return nil, err
	}
	chapters, err := store.GetChapters(ctx, db, id)
	if err != nil {
		return nil, err
	}
	tags, err := store.TagsFor(ctx, db, id)
	if err != nil {
		return nil, err
	}
	sum, b := sums[0], it.Book
	d := BookDetail{
		Id: sum.Id, Title: sum.Title, Subtitle: sum.Subtitle, Authors: sum.Authors, Narrators: sum.Narrators, Series: sum.Series,
		DurationMs: sum.DurationMs, CoverUrl: sum.CoverUrl, Source: sum.Source, Progress: sum.Progress, AddedAt: sum.AddedAt,
		Hidden: sum.Hidden, RemovedAt: sum.RemovedAt, UpdatedAt: sum.UpdatedAt,
		Description: strPtr(b.Description), Publisher: strPtr(b.Publisher), Language: strPtr(b.Language),
		Isbn: strPtr(b.ISBN), Asin: strPtr(b.ASIN), SizeBytes: b.SizeBytes, HasEpub: b.EpubPath != "",
		Files: make([]BookFile, 0, len(files)), Chapters: make([]Chapter, 0, len(chapters)), Tags: make([]TagRef, 0, len(tags)),
	}
	if b.PublishedYear > 0 {
		y := int(b.PublishedYear)
		d.PublishedYear = &y
	}
	for _, f := range files {
		bf := BookFile{Id: mustUUID(f.ID), Index: f.Index, OffsetMs: f.OffsetMS, DurationMs: f.DurationMS, SizeBytes: f.SizeBytes,
			Codec: f.Codec, MimeType: f.MimeType, Sha256: f.Checksum}
		if f.BitrateKbps > 0 {
			br := f.BitrateKbps
			bf.BitrateKbps = &br
		}
		d.Files = append(d.Files, bf)
	}
	for _, c := range chapters {
		d.Chapters = append(d.Chapters, Chapter{Index: c.Index, Title: c.Title, StartMs: c.StartMS, EndMs: c.EndMS})
	}
	for _, t := range tags {
		d.Tags = append(d.Tags, TagRef{Id: mustUUID(t.ID), Name: t.Name, Kind: TagRefKind(t.Kind)})
	}
	return GetBook200JSONResponse(d), nil
}

func (s *Server) RemoveFromMyLibrary(ctx context.Context, req RemoveFromMyLibraryRequestObject) (RemoveFromMyLibraryResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	db := s.App().DB
	it, err := store.VisibleBook(ctx, db, p.User.ID, id)
	if err != nil {
		return nil, err
	}
	if it.Source == "own_upload" && it.Book.UploaderID == p.User.ID && !p.Can(authz.DeleteOwn) {
		return nil, errForbidden
	}
	err = db.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetEntryFlag(ctx, tx, p.User.ID, id, "removed_at", true); err != nil {
			return err
		}
		if err := store.RecordChange(ctx, tx, p.User.ID, store.EntityLibraryEntry, id, "upsert"); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "library.remove", ObjectType: "book", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return RemoveFromMyLibrary204Response{}, nil
}

func (s *Server) RestoreToMyLibrary(ctx context.Context, req RestoreToMyLibraryRequestObject) (RestoreToMyLibraryResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	db := s.App().DB
	if _, err := store.VisibleBook(ctx, db, p.User.ID, id); err != nil {
		return nil, err
	}
	removed, err := store.EntryRemovedAt(ctx, db, p.User.ID, id)
	if err != nil {
		return nil, err
	}
	if removed != nil && store.NowMS()-*removed > store.RecentlyRemovedWindowMS {
		return nil, apiErr(http.StatusGone, "restore_window_passed", "this book was removed more than 30 days ago")
	}
	err = db.InTx(ctx, func(tx *store.Tx) error {
		if err := store.RestoreEntry(ctx, tx, p.User.ID, id); err != nil {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityLibraryEntry, id, "upsert")
	})
	if err != nil {
		return nil, err
	}
	return RestoreToMyLibrary204Response{}, nil
}

func (s *Server) HideInMyLibrary(ctx context.Context, req HideInMyLibraryRequestObject) (HideInMyLibraryResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	db := s.App().DB
	if _, err := store.VisibleBook(ctx, db, p.User.ID, id); err != nil {
		return nil, err
	}
	err = db.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetEntryFlag(ctx, tx, p.User.ID, id, "hidden_at", true); err != nil {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityLibraryEntry, id, "upsert")
	})
	if err != nil {
		return nil, err
	}
	return HideInMyLibrary204Response{}, nil
}

func (s *Server) personPage(ctx context.Context, table, id string, f store.LibraryFilter) (string, []BookSummary, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return "", nil, err
	}
	db := s.App().DB
	n, err := store.GetNamed(ctx, db, table, id)
	if err != nil {
		return "", nil, err
	}
	f.Limit = 200
	items, err := store.ListLibrary(ctx, db, p.User.ID, f)
	if err != nil {
		return "", nil, err
	}
	books, err := s.summaries(ctx, items)
	return n.Name, books, err
}

func (s *Server) GetAuthor(ctx context.Context, req GetAuthorRequestObject) (GetAuthorResponseObject, error) {
	id := req.PersonId.String()
	name, books, err := s.personPage(ctx, "authors", id, store.LibraryFilter{AuthorID: id, Sort: "series"})
	if err != nil {
		return nil, err
	}
	return GetAuthor200JSONResponse{Id: req.PersonId, Name: name, Books: books}, nil
}

func (s *Server) GetNarrator(ctx context.Context, req GetNarratorRequestObject) (GetNarratorResponseObject, error) {
	id := req.PersonId.String()
	name, books, err := s.personPage(ctx, "narrators", id, store.LibraryFilter{NarratorID: id, Sort: "title"})
	if err != nil {
		return nil, err
	}
	return GetNarrator200JSONResponse{Id: req.PersonId, Name: name, Books: books}, nil
}

func (s *Server) GetSeries(ctx context.Context, req GetSeriesRequestObject) (GetSeriesResponseObject, error) {
	id := req.SeriesId.String()
	name, books, err := s.personPage(ctx, "series", id, store.LibraryFilter{SeriesID: id, Sort: "series"})
	if err != nil {
		return nil, err
	}
	return GetSeries200JSONResponse{Id: req.SeriesId, Name: name, Books: books}, nil
}

func (s *Server) ListLibraryFacets(ctx context.Context, req ListLibraryFacetsRequestObject) (ListLibraryFacetsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	if !req.Params.Kind.Valid() {
		return nil, badRequest("bad_request", "unknown facet kind")
	}
	fs, err := store.LibraryFacets(ctx, s.App().DB, p.User.ID, string(req.Params.Kind))
	if err != nil {
		return nil, err
	}
	out := make(ListLibraryFacets200JSONResponse, 0, len(fs))
	for _, f := range fs {
		item := LibraryFacet{Id: mustUUID(f.ID), Name: f.Name, Count: int(f.Count)}
		if f.Kind != "" {
			k := LibraryFacetKind(f.Kind)
			item.Kind = &k
		}
		out = append(out, item)
	}
	return out, nil
}
