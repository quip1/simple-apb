// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/stream"
	"github.com/quip1/simple-apb/server/internal/version"
)

var errBadSig = apiErr(http.StatusUnauthorized, "invalid_signature", "the stream link is invalid or has expired")

// mediaAccess is who may fetch a book's media: a signed-in user or a valid
// signed stream token, either way only for a book that user can see right now
// (a takedown immediately cuts off even an already-issued cast URL).
type mediaAccess struct {
	userID string
	role   string
	sig    string // the token used, echoed into HLS segment URIs
	book   *store.Book
}

func (s *Server) access(ctx context.Context, bookID string) (*mediaAccess, error) {
	app := s.App()
	if p, err := requireCan(ctx, authz.Read); err == nil {
		it, err := s.visibleOrAdmin(ctx, p, bookID)
		if err != nil {
			return nil, err
		}
		return &mediaAccess{userID: p.User.ID, role: p.User.Role, book: it.Book}, nil
	} else if authFrom(ctx).principal != nil || authFrom(ctx).presentedInvalid {
		return nil, err
	}
	r := requestFrom(ctx)
	sig := ""
	if r != nil {
		sig = r.URL.Query().Get("sig")
	}
	if sig == "" {
		return nil, errUnauthenticated
	}
	userID, err := app.Signer.Verify(sig, bookID, time.Now())
	if err != nil {
		return nil, errBadSig
	}
	u, err := store.GetUser(ctx, app.DB, userID)
	if err != nil || (u.Status != "active" && u.Status != "restricted") {
		return nil, errBadSig
	}
	var book *store.Book
	if it, err := store.VisibleBook(ctx, app.DB, userID, bookID); err == nil {
		book = it.Book
	} else if u.Role == "admin" {
		if book, err = store.GetBook(ctx, app.DB, bookID); err != nil {
			return nil, err
		}
	} else {
		return nil, store.ErrNotFound
	}
	return &mediaAccess{userID: userID, role: u.Role, sig: sig, book: book}, nil
}

var errUnauthenticated = apiErr(http.StatusUnauthorized, "unauthenticated", "sign in required")

func (s *Server) classFor(ctx context.Context, a *mediaAccess) stream.Class {
	if a.role == "admin" {
		return stream.ClassAdmin
	}
	if hp, err := store.UserHighPriority(ctx, s.App().DB, a.userID); err == nil && hp {
		return stream.ClassPriority
	}
	return stream.ClassNormal
}

// shapedFile serves a file with Range support through the bandwidth shaper.
type shapedFile struct {
	s           *Server
	r           *http.Request
	path, mime  string
	flow        *stream.Flow
	cacheMaxAge int
}

func (f *shapedFile) visit(w http.ResponseWriter) error {
	defer f.flow.Close()
	file, err := os.Open(f.path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "file missing")
		return nil
	}
	defer func() { _ = file.Close() }()
	st, err := file.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", f.mime)
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", f.cacheMaxAge))
	sw := &stream.Writer{ResponseWriter: w, Flow: f.flow, Ctx: f.r.Context()}
	http.ServeContent(sw, f.r, filepath.Base(f.path), st.ModTime(), file)
	return nil
}

func (f *shapedFile) VisitGetBookFileResponse(w http.ResponseWriter) error   { return f.visit(w) }
func (f *shapedFile) VisitGetHlsSegmentResponse(w http.ResponseWriter) error { return f.visit(w) }

func (s *Server) GetBookFile(ctx context.Context, req GetBookFileRequestObject) (GetBookFileResponseObject, error) {
	a, err := s.access(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	files, err := store.GetBookFiles(ctx, app.DB, a.book.ID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.ID != req.FileId.String() {
			continue
		}
		live := req.Params.Download == nil || !*req.Params.Download
		flow := app.Shaper.Open(a.userID, a.book.ID, s.classFor(ctx, a), live, f.BitrateKbps)
		return &shapedFile{s: s, r: requestFrom(ctx), path: bookFilePath(app, a.book, f), mime: f.MimeType, flow: flow, cacheMaxAge: 3600}, nil
	}
	return nil, store.ErrNotFound
}

func bookFilePath(app *App, b *store.Book, f store.BookFile) string {
	root := app.Lib.Root
	folder := filepath.FromSlash(b.FolderPath)
	// A single-file book whose folder_path is the file itself (several m4b per folder).
	if strings.EqualFold(filepath.Ext(folder), filepath.Ext(f.Path)) && filepath.Base(folder) == f.Path {
		return filepath.Join(root, folder)
	}
	return filepath.Join(root, folder, filepath.FromSlash(f.Path))
}

func (s *Server) hlsSource(ctx context.Context, b *store.Book) (stream.Source, error) {
	app := s.App()
	files, err := store.GetBookFiles(ctx, app.DB, b.ID)
	if err != nil {
		return stream.Source{}, err
	}
	key := b.Checksum
	if len(key) > 16 {
		key = key[:16]
	}
	src := stream.Source{BookID: b.ID, Key: key, DurationMS: b.DurationMS}
	for _, f := range files {
		src.Files = append(src.Files, stream.SourceFile{Path: bookFilePath(app, b, f), OffsetMS: f.OffsetMS, DurationMS: f.DurationMS})
	}
	return src, nil
}

type playlistResponse struct{ body []byte }

func (p playlistResponse) VisitGetHlsPlaylistResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, err := w.Write(p.body)
	return err
}

func (s *Server) GetHlsPlaylist(ctx context.Context, req GetHlsPlaylistRequestObject) (GetHlsPlaylistResponseObject, error) {
	a, err := s.access(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	prof, ok := stream.Profiles[string(req.Profile)]
	if !ok {
		return nil, store.ErrNotFound
	}
	src, err := s.hlsSource(ctx, a.book)
	if err != nil {
		return nil, err
	}
	q := ""
	if a.sig != "" {
		q = "sig=" + url.QueryEscape(a.sig)
	}
	return playlistResponse{body: s.App().HLS.Playlist(src, prof, q)}, nil
}

func (s *Server) GetHlsSegment(ctx context.Context, req GetHlsSegmentRequestObject) (GetHlsSegmentResponseObject, error) {
	a, err := s.access(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	prof, ok := stream.Profiles[string(req.Profile)]
	if !ok {
		return nil, store.ErrNotFound
	}
	numStr := strings.TrimSuffix(strings.TrimPrefix(req.Segment, "seg_"), filepath.Ext(req.Segment))
	n, err := strconv.Atoi(numStr)
	if err != nil || filepath.Ext(req.Segment) != ".ts" {
		return nil, store.ErrNotFound
	}
	src, err := s.hlsSource(ctx, a.book)
	if err != nil {
		return nil, err
	}
	app := s.App()
	path, err := app.HLS.Segment(ctx, src, prof, n)
	if errors.Is(err, stream.ErrBadSegment) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	flow := app.Shaper.Open(a.userID, a.book.ID, s.classFor(ctx, a), true, prof.BitrateKbps)
	return &shapedFile{s: s, r: requestFrom(ctx), path: path, mime: "video/mp2t", flow: flow, cacheMaxAge: 86400}, nil
}

type coverResponse struct {
	r    *http.Request
	path string
}

func (c coverResponse) VisitGetBookCoverResponse(w http.ResponseWriter) error {
	f, err := os.Open(c.path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no cover")
		return nil
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, st.ModTime().UnixNano(), st.Size()))
	http.ServeContent(w, c.r, "cover.jpg", st.ModTime(), f)
	return nil
}

var errNoCover = apiErr(http.StatusNotFound, "no_cover", "this book has no cover")

func (s *Server) GetBookCover(ctx context.Context, req GetBookCoverRequestObject) (GetBookCoverResponseObject, error) {
	a, err := s.access(ctx, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	srcPath := coverSource(app, a.book.CoverPath)
	if srcPath == "" {
		return nil, errNoCover
	}
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return nil, errNoCover
	}
	size := 512
	if req.Params.Size != nil {
		size = int(*req.Params.Size)
	}
	dest := filepath.Join(app.Lib.Paths.Cache, "covers", a.book.ID, fmt.Sprintf("%d.jpg", size))
	if st, err := os.Stat(dest); err != nil || st.ModTime().Before(srcInfo.ModTime()) {
		if err := app.Lib.Tools.ResizeCover(ctx, srcPath, dest, size); err != nil {
			s.log().WarnContext(ctx, "cover resize failed", "book", a.book.ID, "err", err)
			dest = srcPath // fall back to the original
		}
	}
	return coverResponse{r: requestFrom(ctx), path: dest}, nil
}

func coverSource(app *App, p string) string {
	switch {
	case strings.HasPrefix(p, library.CoverInLibrary):
		return filepath.Join(app.Lib.Root, filepath.FromSlash(strings.TrimPrefix(p, library.CoverInLibrary)))
	case strings.HasPrefix(p, library.CoverInCache):
		return filepath.Join(app.Lib.Paths.Cache, filepath.FromSlash(strings.TrimPrefix(p, library.CoverInCache)))
	}
	return ""
}

func (s *Server) CreateStreamUrl(ctx context.Context, req CreateStreamUrlRequestObject) (CreateStreamUrlResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	it, err := s.visibleOrAdmin(ctx, p, req.BookId.String())
	if err != nil {
		return nil, err
	}
	app := s.App()
	profile := "original"
	if req.Body != nil && req.Body.Profile != nil {
		profile = string(*req.Body.Profile)
	}
	exp := time.Now().Add(stream.SignedTTL)
	sig := url.QueryEscape(app.Signer.Sign(p.User.ID, it.Book.ID, exp))
	base := s.publicBase(ctx)
	prefix := base + "/api/" + version.APIVersion
	out := StreamUrls{ExpiresAt: exp.UTC()}
	if it.Book.CoverPath != "" {
		c := prefix + "/books/" + it.Book.ID + "/cover?sig=" + sig
		out.CoverUrl = &c
	}
	if profile == "original" {
		files, err := store.GetBookFiles(ctx, app.DB, it.Book.ID)
		if err != nil {
			return nil, err
		}
		list := make([]struct {
			FileId uuid.UUID `json:"file_id"`
			Url    string    `json:"url"`
		}, 0, len(files))
		for _, f := range files {
			list = append(list, struct {
				FileId uuid.UUID `json:"file_id"`
				Url    string    `json:"url"`
			}{FileId: mustUUID(f.ID), Url: prefix + "/books/" + it.Book.ID + "/files/" + f.ID + "?sig=" + sig})
		}
		out.Files = &list
	} else {
		if _, ok := stream.Profiles[profile]; !ok {
			return nil, badRequest("invalid_profile", "profile must be original, low or medium")
		}
		u := prefix + "/stream/" + it.Book.ID + "/hls/" + profile + "/index.m3u8?sig=" + sig
		out.HlsUrl = &u
	}
	return CreateStreamUrl200JSONResponse(out), nil
}

// publicBase is the configured public base URL, else the request's origin.
func (s *Server) publicBase(ctx context.Context) string {
	if inst, err := s.App().Settings().Instance(ctx); err == nil && inst.BaseURL != "" {
		return strings.TrimRight(inst.BaseURL, "/")
	}
	r := requestFrom(ctx)
	scheme := "http"
	if r != nil && r.TLS != nil {
		scheme = "https"
	}
	host := ""
	if r != nil {
		host = r.Host
	}
	return scheme + "://" + host
}
