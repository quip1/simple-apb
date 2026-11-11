// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/metadata"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Book tools (brief §6.13, ADR-0048): chapters, silence detection, merge to
// m4b, the cover picker and the duplicate finder.

// bookToolUser returns the book if the user may use the book tools on it
// (404 when they can't see it, 403 when they can but may not).
func (s *Server) bookToolUser(ctx context.Context, bookID string) (*auth.Principal, *store.Book, error) {
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
	if p.MustEnrollTOTP || !authz.UseBookTools(p.Subject, isUploader(p, it.Book)) {
		return nil, nil, errForbidden
	}
	return p, it.Book, nil
}

func isUploader(p *auth.Principal, b *store.Book) bool {
	return b.UploaderID != "" && b.UploaderID == p.User.ID
}

func chaptersOut(cs []store.Chapter) []Chapter {
	out := make([]Chapter, len(cs))
	for i, c := range cs {
		out[i] = Chapter{Index: c.Index, Title: c.Title, StartMs: c.StartMS, EndMs: c.EndMS}
	}
	return out
}

func (s *Server) SetBookChapters(ctx context.Context, req SetBookChaptersRequestObject) (SetBookChaptersResponseObject, error) {
	p, b, err := s.bookToolUser(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	in := make([]library.ChapterInput, len(req.Body.Chapters))
	for i, c := range req.Body.Chapters {
		in[i] = library.ChapterInput{Title: c.Title, StartMS: c.StartMs}
	}
	app := s.App()
	cs, write, err := app.Lib.SaveChapters(ctx, p.User.ID, b.ID, in)
	if errors.Is(err, library.ErrBadChapters) {
		return nil, badRequest("invalid_chapters", "each chapter needs a title and a different start inside the book")
	}
	if err != nil {
		return nil, err
	}
	out := ChaptersSaved{Chapters: chaptersOut(cs)}
	if write {
		id, err := jobs.Enqueue(ctx, app.DB, library.JobWriteChapters, library.BookPayload{BookID: b.ID}, time.Time{}, p.User.ID)
		if err != nil {
			return nil, err
		}
		app.Jobs.Wake()
		jid := mustUUID(id)
		out.JobId = &jid
	}
	return SetBookChapters200JSONResponse(out), nil
}

func (s *Server) DetectBookChapters(ctx context.Context, req DetectBookChaptersRequestObject) (DetectBookChaptersResponseObject, error) {
	p, b, err := s.bookToolUser(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	payload := library.DetectPayload{BookID: b.ID, NoiseDB: -35, MinSilenceMS: 2000}
	if v := req.Body.NoiseDb; v != nil {
		if *v < -80 || *v > -10 {
			return nil, badRequest("invalid_noise_db", "noise_db must be between -80 and -10")
		}
		payload.NoiseDB = float64(*v)
	}
	if v := req.Body.MinSilenceMs; v != nil {
		if *v < 300 || *v > 10000 {
			return nil, badRequest("invalid_min_silence", "min_silence_ms must be between 300 and 10000")
		}
		payload.MinSilenceMS = int64(*v)
	}
	app := s.App()
	id, err := jobs.Enqueue(ctx, app.DB, library.JobDetectChapters, payload, time.Time{}, p.User.ID)
	if err != nil {
		return nil, err
	}
	app.Jobs.Wake()
	return DetectBookChapters202JSONResponse{JobId: mustUUID(id)}, nil
}

func (s *Server) MergeBookToM4b(ctx context.Context, req MergeBookToM4bRequestObject) (MergeBookToM4bResponseObject, error) {
	p, b, err := s.bookToolUser(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	files, err := store.GetBookFiles(ctx, app.DB, b.ID)
	if err != nil {
		return nil, err
	}
	if !library.Mergeable(files) {
		return MergeBookToM4b409JSONResponse{Code: "not_mergeable", Message: "the book is already one m4b file"}, nil
	}
	payload := library.MergePayload{BookID: b.ID, ActorID: p.User.ID}
	if req.Body.Reencode != nil {
		payload.Reencode = *req.Body.Reencode
	}
	if req.Body.KeepOriginal != nil && *req.Body.KeepOriginal {
		if !authz.MergeAsNewBook(p.Subject, isUploader(p, b)) {
			return nil, errForbidden
		}
		payload.KeepOriginal = true
	}
	id, created, err := jobs.EnqueueUnique(ctx, app.DB, library.JobMerge, payload, time.Time{})
	if err != nil {
		return nil, err
	}
	if !created {
		return MergeBookToM4b409JSONResponse{Code: "busy", Message: "a merge is already running for this book"}, nil
	}
	// EnqueueUnique doesn't record who asked; the job status page needs it.
	if _, err := app.DB.Exec(ctx, `UPDATE jobs SET created_by = ? WHERE id = ?`, p.User.ID, id); err != nil {
		return nil, err
	}
	app.Jobs.Wake()
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: p.User.ID, Action: "book.merge_requested", ObjectType: "book", ObjectID: b.ID,
		IP: clientIP(ctx), Details: map[string]any{"job_id": id, "keep_original": payload.KeepOriginal, "reencode": payload.Reencode}})
	return MergeBookToM4b202JSONResponse{JobId: mustUUID(id)}, nil
}

func (s *Server) UploadBookCover(ctx context.Context, req UploadBookCoverRequestObject) (UploadBookCoverResponseObject, error) {
	p, b, err := s.bookToolUser(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, metadata.MaxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > metadata.MaxCoverBytes {
		return UploadBookCover413JSONResponse{Code: "too_large", Message: "the image is larger than 10 MB"}, nil
	}
	img, err := metadata.ToJPEG(data)
	if err != nil {
		return nil, badRequest("bad_image", "use a JPEG or PNG image at least 32 pixels wide")
	}
	app := s.App()
	if err := app.Lib.SaveCover(ctx, b.ID, img); err != nil {
		return nil, err
	}
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: p.User.ID, Action: "book.cover_set", ObjectType: "book", ObjectID: b.ID,
		IP: clientIP(ctx), Details: map[string]any{"source": "upload"}})
	return UploadBookCover204Response{}, nil
}

func (s *Server) ExtractBookCover(ctx context.Context, req ExtractBookCoverRequestObject) (ExtractBookCoverResponseObject, error) {
	p, b, err := s.bookToolUser(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	files, err := store.GetBookFiles(ctx, app.DB, b.ID)
	if err != nil {
		return nil, err
	}
	none := ExtractBookCover422JSONResponse{Code: "no_embedded_cover", Message: "the audio has no embedded cover"}
	for _, f := range files {
		path := app.Lib.FilePath(b, f)
		probe, err := app.Lib.Tools.ProbeFile(ctx, path)
		if err != nil || !probe.HasCover {
			continue
		}
		tmp := filepath.Join(app.Lib.Paths.Cache, "tools", b.ID+"-embedded.jpg")
		if err := app.Lib.Tools.ExtractCover(ctx, path, tmp); err != nil {
			s.log().WarnContext(ctx, "embedded cover extraction failed", "book", b.ID, "err", err)
			return none, nil
		}
		img, err := readAndRemove(tmp)
		if err != nil {
			return nil, err
		}
		if err := app.Lib.SaveCover(ctx, b.ID, img); err != nil {
			return nil, err
		}
		_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: p.User.ID, Action: "book.cover_set", ObjectType: "book", ObjectID: b.ID,
			IP: clientIP(ctx), Details: map[string]any{"source": "embedded"}})
		return ExtractBookCover204Response{}, nil
	}
	return none, nil
}

// ── duplicates (admin) ──

func (s *Server) ListDuplicates(ctx context.Context, req ListDuplicatesRequestObject) (ListDuplicatesResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	offset, err := decodeCursor(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(req.Params.Limit, 20)
	db := s.App().DB
	ds, err := store.ListOpenDuplicates(ctx, db, limit+1, offset)
	if err != nil {
		return nil, err
	}
	page := DuplicatePage{Items: []DuplicateCandidate{}}
	if len(ds) > limit {
		ds = ds[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
	for _, d := range ds {
		a, err := store.GetBook(ctx, db, d.BookA)
		if err != nil {
			return nil, err
		}
		b, err := store.GetBook(ctx, db, d.BookB)
		if err != nil {
			return nil, err
		}
		ma, err := s.masterOut(ctx, a, true)
		if err != nil {
			return nil, err
		}
		mb, err := s.masterOut(ctx, b, true)
		if err != nil {
			return nil, err
		}
		item := DuplicateCandidate{Id: mustUUID(d.ID), Kind: DuplicateCandidateKind(d.Kind), BookA: ma, BookB: mb, CreatedAt: msTime(d.CreatedAt)}
		if d.Kind == "probable" {
			score := float32(d.Score)
			item.Score = &score
		}
		page.Items = append(page.Items, item)
	}
	return ListDuplicates200JSONResponse(page), nil
}

func (s *Server) ScanDuplicates(ctx context.Context, _ ScanDuplicatesRequestObject) (ScanDuplicatesResponseObject, error) {
	actor, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	app := s.App()
	id, err := jobs.Enqueue(ctx, app.DB, library.JobFindDuplicates, nil, time.Time{}, actor)
	if err != nil {
		return nil, err
	}
	app.Jobs.Wake()
	return ScanDuplicates202JSONResponse{JobId: mustUUID(id)}, nil
}

func (s *Server) ResolveDuplicate(ctx context.Context, req ResolveDuplicateRequestObject) (ResolveDuplicateResponseObject, error) {
	actor, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	app := s.App()
	id := req.DuplicateId.String()
	switch req.Body.Action {
	case Keep:
		if _, _, err := store.GetDuplicate(ctx, app.DB, id); err != nil {
			return nil, err
		}
		if err := store.DecideDuplicate(ctx, app.DB, id, "kept", actor); err != nil && !errors.Is(err, store.ErrConflict) {
			return nil, err
		}
		_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: actor, Action: "book.duplicate_keep", ObjectType: "duplicate", ObjectID: id, IP: clientIP(ctx)})
	case Merge:
		if req.Body.KeepBookId == nil {
			return nil, badRequest("keep_book_required", "say which book to keep")
		}
		err := app.Lib.MergeDuplicate(ctx, actor, id, req.Body.KeepBookId.String(), clientIP(ctx))
		switch {
		case errors.Is(err, library.ErrNotInPair):
			return nil, badRequest("not_in_pair", "keep_book_id must be one of the two books")
		case errors.Is(err, store.ErrConflict):
			return nil, apiErr(http.StatusConflict, "already_decided", "this pair was already decided")
		case err != nil:
			return nil, err
		}
	default:
		return nil, badRequest("invalid_action", "action must be keep or merge")
	}
	return ResolveDuplicate204Response{}, nil
}

func readAndRemove(path string) ([]byte, error) {
	defer func() { _ = os.Remove(path) }()
	return os.ReadFile(path)
}
