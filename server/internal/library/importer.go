// SPDX-License-Identifier: AGPL-3.0-or-later

// Package library manages the master store: detecting and importing books
// from the library folder, processing uploads into the canonical layout,
// deduplication, and the scan/purge jobs.
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Library wires the master-store services.
type Library struct {
	DB    *store.DB
	Tools media.Tools
	Paths config.Paths
	Root  string // library root (from settings, usually /library)
	Log   *slog.Logger
}

// Analysis is a probed candidate, ready to insert.
type Analysis struct {
	Book    store.NewBook
	Cover   string // library-relative cover image file, if any
	Embed   string // absolute audio path with an embedded cover, if any
	Problem error  // set when the candidate can't be imported
}

// Analyze probes and hashes a candidate's files (paths relative to root).
func (l *Library) Analyze(ctx context.Context, root string, c Candidate) *Analysis {
	a := &Analysis{}
	var (
		offset, size int64
		probes       []*media.Probe
		hashes       strings.Builder
	)
	for i, rel := range c.Files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		p, err := l.Tools.ProbeFile(ctx, abs)
		if err != nil {
			a.Problem = fmt.Errorf("%s: %w", filepath.Base(rel), err)
			return a
		}
		sum, n, err := HashFile(abs)
		if err != nil {
			a.Problem = err
			return a
		}
		probes = append(probes, p)
		hashes.WriteString(sum)
		inBook := strings.TrimPrefix(rel, strings.TrimSuffix(c.Rel, "/")+"/")
		if inBook == rel && c.Rel == rel {
			inBook = filepath.Base(rel) // single-file book: path is the file itself
		}
		a.Book.Files = append(a.Book.Files, store.BookFile{Path: inBook, Index: i, OffsetMS: offset, DurationMS: p.DurationMS,
			SizeBytes: n, Codec: p.Codec, MimeType: p.MimeType, Checksum: sum, BitrateKbps: p.BitrateKbps})
		if p.HasCover && a.Embed == "" {
			a.Embed = abs
		}
		offset += p.DurationMS
		size += n
	}
	whole := sha256.Sum256([]byte(hashes.String()))
	a.Book.Checksum = hex.EncodeToString(whole[:])
	a.Book.DurationMS, a.Book.SizeBytes, a.Book.FolderPath = offset, size, c.Rel
	a.Book.Meta = metaFrom(probes[0], c)
	a.Book.Chapters = chaptersFrom(probes, a.Book.Files, a.Book.Meta.Title)
	a.Cover = findCoverFile(root, c)
	return a
}

// HashFile returns the SHA-256 hex and size of a file.
func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func chaptersFrom(probes []*media.Probe, files []store.BookFile, title string) []store.Chapter {
	// Single file with embedded chapters: use them.
	if len(probes) == 1 && len(probes[0].Chapters) > 0 {
		out := make([]store.Chapter, 0, len(probes[0].Chapters))
		for i, c := range probes[0].Chapters {
			end := min(c.EndMS, files[0].DurationMS)
			if end <= c.StartMS {
				continue
			}
			out = append(out, store.Chapter{Index: i, Title: c.Title, StartMS: c.StartMS, EndMS: end})
		}
		if len(out) > 0 {
			reindex(out)
			return out
		}
	}
	// Otherwise one chapter per file (mp3 folders), or one for a chapterless file.
	out := make([]store.Chapter, 0, len(files))
	for i, f := range files {
		t := probes[i].Tag("title")
		if t == "" || len(files) == 1 && t == title {
			t = strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
		}
		if len(files) == 1 {
			t = title
		}
		out = append(out, store.Chapter{Index: i, Title: t, StartMS: f.OffsetMS, EndMS: f.OffsetMS + f.DurationMS})
	}
	return out
}

func reindex(cs []store.Chapter) {
	for i := range cs {
		cs[i].Index = i
	}
}

var yearRe = regexp.MustCompile(`\b(1[5-9]\d\d|20\d\d)\b`)

// metaFrom builds metadata from tags, falling back to the folder layout
// /<Author>/<Series?>/<Title>/.
func metaFrom(p *media.Probe, c Candidate) store.BookMeta {
	segs := strings.Split(strings.TrimSuffix(c.Rel, "/"), "/")
	if len(c.Files) == 1 && c.Rel == c.Files[0] {
		last := segs[len(segs)-1]
		segs[len(segs)-1] = strings.TrimSuffix(last, filepath.Ext(last))
	}
	var pathAuthor, pathSeries, pathTitle string
	if c.Rel == "." {
		segs = []string{c.Hint}
	}
	switch len(segs) {
	case 1:
		pathTitle = segs[0]
	case 2:
		pathAuthor, pathTitle = segs[0], segs[1]
	default:
		pathAuthor, pathSeries, pathTitle = segs[len(segs)-3], segs[len(segs)-2], segs[len(segs)-1]
	}
	m := store.BookMeta{
		Title:       first(p.Tag("album"), p.Tag("title"), pathTitle),
		Subtitle:    p.Tag("subtitle"),
		Description: first(p.Tag("description"), p.Tag("synopsis"), p.Tag("comment")),
		Publisher:   first(p.Tag("publisher"), p.Tag("label")),
		Language:    language(p.Tag("language", "lang")),
		ISBN:        p.Tag("isbn"),
		ASIN:        p.Tag("asin", "audible_asin"),
		Series:      first(p.Tag("series"), p.Tag("mvnm"), p.Tag("grouping"), pathSeries),
		SeriesIndex: first(p.Tag("series-part"), p.Tag("series_part"), p.Tag("mvin")),
	}
	if len(c.Files) == 1 && p.Tag("album") == "" {
		m.Title = first(p.Tag("title"), pathTitle)
	}
	m.Authors = splitPeople(first(p.Tag("album_artist"), p.Tag("artist"), p.Tag("author"), pathAuthor))
	m.Narrators = splitPeople(first(p.Tag("narrator"), p.Tag("composer"), p.Tag("performer")))
	if y := yearRe.FindString(first(p.Tag("date"), p.Tag("year"), p.Tag("originaldate"))); y != "" {
		m.PublishedYear, _ = strconv.Atoi(y)
	}
	for _, g := range strings.FieldsFunc(p.Tag("genre"), func(r rune) bool { return r == ';' || r == ',' || r == '/' }) {
		if g = strings.TrimSpace(g); g != "" {
			m.Genres = append(m.Genres, g)
		}
	}
	if m.SeriesIndex != "" && m.Series == "" {
		m.SeriesIndex = ""
	}
	return m
}

func first(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// splitPeople splits "A, B & C" / "A; B" / "A and B" into names.
func splitPeople(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.NewReplacer(" & ", ";", " and ", ";", ", ", ";", ",", ";").Replace(s)
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var coverNames = []string{"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg", "folder.png"}

func findCoverFile(root string, c Candidate) string {
	dir := c.Rel
	if len(c.Files) == 1 && c.Rel == c.Files[0] {
		dir = filepath.ToSlash(filepath.Dir(c.Rel))
	}
	for _, n := range coverNames {
		rel := filepath.ToSlash(filepath.Join(dir, n))
		if Exists(root, rel) {
			return rel
		}
	}
	return ""
}

// Cover paths stored in books.cover_path carry a prefix saying which volume
// they live on, so moving a volume doesn't break them.
const (
	CoverInLibrary = "library:"
	CoverInCache   = "cache:"
)

// storeCover records the cover: a cover file in the book folder wins, else the
// embedded cover is extracted into /cache/covers/<book-id>/cover.jpg.
func (l *Library) storeCover(ctx context.Context, bookID string, a *Analysis) {
	path := ""
	switch {
	case a.Cover != "":
		path = CoverInLibrary + a.Cover
	case a.Embed != "":
		rel := filepath.ToSlash(filepath.Join("covers", bookID, "cover.jpg"))
		if err := l.Tools.ExtractCover(ctx, a.Embed, filepath.Join(l.Paths.Cache, filepath.FromSlash(rel))); err != nil {
			l.Log.Warn("cover extraction failed", "book", bookID, "err", err)
			return
		}
		path = CoverInCache + rel
	default:
		return
	}
	if err := store.SetCoverPath(ctx, l.DB, bookID, path); err != nil {
		l.Log.Warn("saving cover path failed", "book", bookID, "err", err)
	}
}

// language drops ISO 639 "undetermined"/"no linguistic content" codes.
func language(s string) string {
	switch strings.ToLower(s) {
	case "und", "zxx", "mis", "mul":
		return ""
	}
	return s
}
