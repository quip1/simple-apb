// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/quip1/simple-apb/server/internal/fsutil"
	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Book tools (brief §6.13, ADR-0048). Each change to a book's files runs as
// a job and holds the library lock while it swaps files, so a scan never
// sees half a change. Every job can run again after a crash and finish the
// same change.

const (
	JobWriteChapters  = "book.write_chapters"
	JobDetectChapters = "book.detect_chapters"
	JobMerge          = "book.merge_m4b"
	JobFindDuplicates = "library.find_duplicates"
)

// ChapterInput is an edited chapter: its title and where it starts.
type ChapterInput struct {
	Title   string
	StartMS int64
}

// ErrBadChapters is a chapter list that can't be saved.
var ErrBadChapters = errors.New("bad chapters")

// ErrNotInPair: the book to keep isn't one of the duplicate pair.
var ErrNotInPair = errors.New("book is not one of the pair")

// bookChecksum is the whole-book checksum: the hash of the ordered file hashes.
func bookChecksum(fileSums []string) string {
	whole := sha256.Sum256([]byte(strings.Join(fileSums, "")))
	return hex.EncodeToString(whole[:])
}

// NormalizeChapters sorts the chapters by start, makes the first start at
// 0, and ends each where the next starts (the last at the end of the book).
func NormalizeChapters(in []ChapterInput, durationMS int64) ([]store.Chapter, error) {
	if len(in) == 0 || durationMS <= 0 {
		return nil, ErrBadChapters
	}
	cs := slices.Clone(in)
	slices.SortStableFunc(cs, func(a, b ChapterInput) int { return int(min(max(a.StartMS-b.StartMS, -1), 1)) })
	out := make([]store.Chapter, 0, len(cs))
	for i, c := range cs {
		title := strings.TrimSpace(c.Title)
		if title == "" || len([]rune(title)) > 200 || c.StartMS < 0 || c.StartMS >= durationMS {
			return nil, ErrBadChapters
		}
		start := c.StartMS
		if i == 0 {
			start = 0
		} else if start == out[i-1].StartMS {
			return nil, ErrBadChapters // two chapters can't start together
		}
		out = append(out, store.Chapter{Index: i, Title: title, StartMS: start})
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].EndMS = out[i+1].StartMS
		} else {
			out[i].EndMS = durationMS
		}
	}
	return out, nil
}

// chaptersWritable: one m4b/m4a file, where chapters live inside the file.
func chaptersWritable(files []store.BookFile) bool {
	if len(files) != 1 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(files[0].Path))
	return ext == ".m4b" || ext == ".m4a"
}

// SaveChapters stores edited chapters and keeps them across re-scans. It
// reports whether a job should also write them into the file.
func (l *Library) SaveChapters(ctx context.Context, actorID, bookID string, in []ChapterInput) ([]store.Chapter, bool, error) {
	b, err := store.GetBook(ctx, l.DB, bookID)
	if err != nil {
		return nil, false, err
	}
	files, err := store.GetBookFiles(ctx, l.DB, bookID)
	if err != nil {
		return nil, false, err
	}
	cs, err := NormalizeChapters(in, b.DurationMS)
	if err != nil {
		return nil, false, err
	}
	err = l.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.ReplaceChapters(ctx, tx, bookID, cs); err != nil {
			return err
		}
		if err := store.LockField(ctx, tx, bookID, store.LockedChapters); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: actorID, Action: "book.chapters_edit", ObjectType: "book", ObjectID: bookID,
			Details: map[string]any{"chapters": len(cs)}})
	})
	return cs, err == nil && chaptersWritable(files), err
}

// BookPayload names the book a tool job works on.
type BookPayload struct {
	BookID string `json:"book_id"`
}

// WriteChapters writes the book's stored chapters into its m4b file (a
// remux; the audio is untouched) and updates the file's checksum.
func (l *Library) WriteChapters(ctx context.Context, bookID string) error {
	l.ops.Lock()
	defer l.ops.Unlock()
	b, err := store.GetBook(ctx, l.DB, bookID)
	if err != nil {
		return err
	}
	files, err := store.GetBookFiles(ctx, l.DB, bookID)
	if err != nil || !chaptersWritable(files) {
		return err
	}
	cs, err := store.GetChapters(ctx, l.DB, bookID)
	if err != nil {
		return err
	}
	src := l.FilePath(b, files[0])
	tmp := filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".chapters.tmp")
	defer func() { _ = os.Remove(tmp) }()
	if err := l.Tools.WriteChapters(ctx, src, tmp, marks(cs)); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	sum, size, err := HashFile(tmp)
	if err != nil {
		return err
	}
	if err := fsutil.RenameDurable(tmp, src); err != nil {
		return err
	}
	return l.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := setFileHash(ctx, tx, files[0].ID, sum, size); err != nil {
			return err
		}
		return store.UpdateBookTotals(ctx, tx, bookID, b.DurationMS, size, bookChecksum([]string{sum}))
	})
}

func marks(cs []store.Chapter) []media.ChapterMark {
	out := make([]media.ChapterMark, len(cs))
	for i, c := range cs {
		out[i] = media.ChapterMark{Title: c.Title, StartMS: c.StartMS, EndMS: c.EndMS}
	}
	return out
}

func syncFile(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

func setFileHash(ctx context.Context, q store.Querier, fileID, sum string, size int64) error {
	now := store.NowMS()
	_, err := q.Exec(ctx, `UPDATE book_files SET checksum = ?, size_bytes = ?, verified_at = ?, updated_at = ? WHERE id = ?`,
		sum, size, now, now, fileID)
	return err
}

// ── silence detection ──

// DetectPayload is a silence-detection job.
type DetectPayload struct {
	BookID       string  `json:"book_id"`
	NoiseDB      float64 `json:"noise_db"`
	MinSilenceMS int64   `json:"min_silence_ms"`
}

// DetectChapters proposes chapters from the book's silences (nothing is saved).
func (l *Library) DetectChapters(ctx context.Context, p DetectPayload) ([]store.Chapter, error) {
	b, err := store.GetBook(ctx, l.DB, p.BookID)
	if err != nil {
		return nil, err
	}
	files, err := store.GetBookFiles(ctx, l.DB, p.BookID)
	if err != nil {
		return nil, err
	}
	found := make([][]media.Silence, len(files))
	for i, f := range files {
		s, err := l.Tools.DetectSilences(ctx, l.FilePath(b, f), p.NoiseDB, time.Duration(p.MinSilenceMS)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		found[i] = s
	}
	return ChaptersFromSilences(files, found, b.DurationMS), nil
}

// Chapter breaks closer than this to the previous one are dropped.
const minChapterMS = 10_000

// ChaptersFromSilences turns silences into chapter starts: each file starts
// a chapter, and so does the end of each silence inside a file (half a
// second early, so speech isn't clipped).
func ChaptersFromSilences(files []store.BookFile, silences [][]media.Silence, durationMS int64) []store.Chapter {
	starts := []int64{0}
	for i, f := range files {
		if i > 0 {
			starts = append(starts, f.OffsetMS)
		}
		for _, s := range silences[i] {
			// Silences at a file's very start or end are the file boundary itself.
			if s.EndMS <= 3000 || s.StartMS >= f.DurationMS-3000 {
				continue
			}
			starts = append(starts, f.OffsetMS+max(s.StartMS, s.EndMS-500))
		}
	}
	slices.Sort(starts)
	kept := []int64{}
	for _, s := range starts {
		if s >= durationMS-minChapterMS && s != 0 {
			continue
		}
		if len(kept) > 0 && s-kept[len(kept)-1] < minChapterMS {
			continue
		}
		kept = append(kept, s)
	}
	out := make([]store.Chapter, len(kept))
	for i, s := range kept {
		end := durationMS
		if i+1 < len(kept) {
			end = kept[i+1]
		}
		out[i] = store.Chapter{Index: i, Title: "Chapter " + strconv.Itoa(i+1), StartMS: s, EndMS: end}
	}
	return out
}

// ── merge to m4b ──

// MergePayload is a merge job.
type MergePayload struct {
	BookID       string `json:"book_id"`
	ActorID      string `json:"actor_id"`
	Reencode     bool   `json:"reencode"`
	KeepOriginal bool   `json:"keep_original"`
}

// mergeNote, in a book folder, lists the old files of a merge whose new m4b
// is already the book's file: a re-run removes them, and scans leave the
// folder alone until it's gone.
const mergeNote = ".simpleapb-merge"

// finishMerge removes the old files a merge left behind (after a crash).
func finishMerge(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, mergeNote))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, rel := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		f := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.Remove(f)
		removeEmptyParents(filepath.Dir(f), dir)
	}
	if err := os.Remove(filepath.Join(dir, mergeNote)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fsutil.SyncDir(dir)
}

// mergePending reports whether a folder has a merge to finish.
func mergePending(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, mergeNote))
	return err == nil
}

// Mergeable reports whether the merge tool has anything to do.
func Mergeable(files []store.BookFile) bool {
	return len(files) > 1 || (len(files) == 1 && !chaptersWritable(files))
}

// copyable: every file has the same codec and format, one mp4 can carry.
func copyable(probes []*media.Probe) bool {
	first := probes[0]
	if first.Codec != "aac" && first.Codec != "mp3" && first.Codec != "alac" {
		return false
	}
	for _, p := range probes[1:] {
		if p.Codec != first.Codec || p.SampleRate != first.SampleRate || p.Channels != first.Channels {
			return false
		}
	}
	return true
}

// Merge concatenates a book's files into one chaptered m4b. It replaces the
// book's files, or (KeepOriginal) adds the m4b as a new book. It returns the
// id of the book that has the m4b.
func (l *Library) Merge(ctx context.Context, p MergePayload) (string, error) {
	b, err := store.GetBook(ctx, l.DB, p.BookID)
	if err != nil {
		return "", err
	}
	files, err := store.GetBookFiles(ctx, l.DB, p.BookID)
	if err != nil {
		return "", err
	}
	if !Mergeable(files) {
		// Done already: a re-run after a crash only clears the old files.
		l.ops.Lock()
		defer l.ops.Unlock()
		dir := filepath.Join(l.Root, filepath.FromSlash(b.FolderPath))
		if len(files) == 1 && filepath.Base(b.FolderPath) == files[0].Path {
			dir = filepath.Dir(dir)
		}
		return b.ID, finishMerge(dir)
	}
	cs, err := store.GetChapters(ctx, l.DB, p.BookID)
	if err != nil {
		return "", err
	}
	inputs := make([]string, len(files))
	probes := make([]*media.Probe, len(files))
	for i, f := range files {
		inputs[i] = l.FilePath(b, f)
		if probes[i], err = l.Tools.ProbeFile(ctx, inputs[i]); err != nil {
			return "", err
		}
	}
	tags, err := l.bookTags(ctx, b)
	if err != nil {
		return "", err
	}
	work := filepath.Join(l.Paths.Cache, "tools", b.ID)
	if err := os.MkdirAll(work, 0o750); err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()
	out := filepath.Join(work, "book.m4b")
	cover := ""
	if c := l.coverFile(b); c != "" {
		cover = c
	}
	opts := media.MergeOptions{Inputs: inputs, Chapters: marks(cs), Tags: tags, Cover: cover,
		Reencode: p.Reencode || !copyable(probes), Channels: probes[0].Channels}
	if err := l.Tools.MergeToM4B(ctx, out, opts); err != nil && cover != "" {
		// A cover ffmpeg can't read shouldn't stop the merge.
		opts.Cover = ""
		err = l.Tools.MergeToM4B(ctx, out, opts)
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	probe, err := l.Tools.ProbeFile(ctx, out)
	if err != nil {
		return "", fmt.Errorf("merged file: %w", err)
	}
	name := SafeName(b.Title, "Book") + ".m4b"

	l.ops.Lock()
	defer l.ops.Unlock()
	if p.KeepOriginal {
		return l.addMergedBook(ctx, b, p, out, name, probe, cs)
	}
	return b.ID, l.replaceWithMerged(ctx, b, files, p, out, name, probe, cs)
}

func (l *Library) bookTags(ctx context.Context, b *store.Book) (media.BookTags, error) {
	people := func(kind string) (string, error) {
		m, err := store.PeopleFor(ctx, l.DB, kind, []string{b.ID})
		if err != nil {
			return "", err
		}
		names := make([]string, 0, len(m[b.ID]))
		for _, n := range m[b.ID] {
			names = append(names, n.Name)
		}
		return strings.Join(names, ", "), nil
	}
	authors, err := people("authors")
	if err != nil {
		return media.BookTags{}, err
	}
	narrators, err := people("narrators")
	if err != nil {
		return media.BookTags{}, err
	}
	year := ""
	if b.PublishedYear > 0 {
		year = strconv.FormatInt(b.PublishedYear, 10)
	}
	return media.BookTags{Title: b.Title, Authors: authors, Narrators: narrators, Series: b.SeriesName,
		SeriesPart: b.SeriesIndex, Year: year, Description: b.Description}, nil
}

// coverFile is the book's cover image on disk, if any.
func (l *Library) coverFile(b *store.Book) string {
	var p string
	switch {
	case strings.HasPrefix(b.CoverPath, CoverInLibrary):
		p = filepath.Join(l.Root, filepath.FromSlash(strings.TrimPrefix(b.CoverPath, CoverInLibrary)))
	case strings.HasPrefix(b.CoverPath, CoverInCache):
		p = filepath.Join(l.Paths.Cache, filepath.FromSlash(strings.TrimPrefix(b.CoverPath, CoverInCache)))
	default:
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// copyInto copies src to dst crash-safely (temp → fsync → rename → fsync dir).
func copyInto(src, dst string) (sum string, size int64, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()
	w, err := fsutil.NewAtomicWriter(dst, 0o640)
	if err != nil {
		return "", 0, err
	}
	if _, err := io.Copy(w, in); err != nil {
		w.Abort()
		return "", 0, err
	}
	if err := w.Commit(); err != nil {
		return "", 0, err
	}
	sum, size, err = HashFile(dst)
	return sum, size, err
}

func mergedFile(name, sum string, size int64, probe *media.Probe) store.BookFile {
	return store.BookFile{Path: name, Index: 0, OffsetMS: 0, DurationMS: probe.DurationMS, SizeBytes: size,
		Codec: probe.Codec, BitrateKbps: probe.BitrateKbps, MimeType: probe.MimeType, Checksum: sum}
}

// clampChapters fits chapters to a (slightly different) new duration.
func clampChapters(cs []store.Chapter, durationMS int64) []store.Chapter {
	out := make([]store.Chapter, 0, len(cs))
	for _, c := range cs {
		if c.StartMS >= durationMS {
			break
		}
		c.Index = len(out)
		out = append(out, c)
	}
	if len(out) == 0 {
		return []store.Chapter{{Index: 0, Title: "Chapter 1", StartMS: 0, EndMS: durationMS}}
	}
	out[len(out)-1].EndMS = durationMS
	for i := range out[:len(out)-1] {
		out[i].EndMS = out[i+1].StartMS
	}
	return out
}

func (l *Library) replaceWithMerged(ctx context.Context, b *store.Book, files []store.BookFile, p MergePayload,
	out, name string, probe *media.Probe, cs []store.Chapter) error {
	// Into the book's own folder (a single-file book's folder is its parent).
	dir := filepath.Join(l.Root, filepath.FromSlash(b.FolderPath))
	folderIsFile := len(files) == 1 && filepath.Base(b.FolderPath) == files[0].Path
	if folderIsFile {
		dir = filepath.Dir(dir)
	}
	old := make([]string, len(files))
	for i, f := range files {
		old[i] = l.FilePath(b, f)
	}
	dst := filepath.Join(dir, name)
	for i := 2; slices.Contains(old, dst) || (fileExists(dst) && !slices.Contains(old, dst)); i++ {
		dst = filepath.Join(dir, fmt.Sprintf("%s (%d).m4b", strings.TrimSuffix(name, ".m4b"), i))
	}
	sum, size, err := copyInto(out, dst)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(dir, dst)
	if err != nil {
		return err
	}
	folder := b.FolderPath
	if folderIsFile {
		folder = path.Dir(b.FolderPath)
	}
	merged := mergedFile(filepath.ToSlash(rel), sum, size, probe)
	// The note goes in first: from the moment the database names the m4b,
	// the old files must go, even if the server stops right now.
	var note []string
	for _, f := range old {
		if f == dst {
			continue
		}
		r, err := filepath.Rel(dir, f)
		if err != nil {
			return err
		}
		note = append(note, filepath.ToSlash(r))
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, mergeNote), []byte(strings.Join(note, "\n")), 0o640); err != nil {
		return err
	}
	err = l.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.ReplaceBookMedia(ctx, tx, b.ID, []store.BookFile{merged}, clampChapters(cs, probe.DurationMS)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE books SET folder_path = ? WHERE id = ?`, folder, b.ID); err != nil {
			return err
		}
		if err := store.UpdateBookTotals(ctx, tx, b.ID, probe.DurationMS, size, bookChecksum([]string{sum})); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, b.ID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.ActorID, Action: "book.merge_m4b", ObjectType: "book", ObjectID: b.ID,
			Details: map[string]any{"files": len(files), "reencoded": p.Reencode}})
	})
	if err != nil {
		_ = os.Remove(dst)
		_ = os.Remove(filepath.Join(dir, mergeNote))
		return err
	}
	return finishMerge(dir)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func removeEmptyParents(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func (l *Library) addMergedBook(ctx context.Context, b *store.Book, p MergePayload, out, name string, probe *media.Probe, cs []store.Chapter) (string, error) {
	meta, err := l.bookMeta(ctx, b)
	if err != nil {
		return "", err
	}
	meta.Title = b.Title
	folder, err := l.targetFolder(meta)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(l.Root, filepath.FromSlash(folder), name)
	sum, size, err := copyInto(out, dst)
	if err != nil {
		return "", err
	}
	nb := store.NewBook{Meta: meta, FolderPath: folder, CoverPath: b.CoverPath, Origin: b.Origin, Status: "ready",
		UploaderID: b.UploaderID, Checksum: bookChecksum([]string{sum}), DurationMS: probe.DurationMS, SizeBytes: size,
		Files: []store.BookFile{mergedFile(name, sum, size, probe)}, Chapters: clampChapters(cs, probe.DurationMS)}
	var id string
	err = l.DB.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if id, err = store.InsertBook(ctx, tx, &nb); err != nil {
			return err
		}
		if b.UploaderID != "" {
			if err := store.AddEntry(ctx, tx, b.UploaderID, id, "own_upload"); err != nil {
				return err
			}
			if err := store.RecordChange(ctx, tx, b.UploaderID, store.EntityLibraryEntry, id, "upsert"); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.ActorID, Action: "book.merge_m4b", ObjectType: "book", ObjectID: id,
			Details: map[string]any{"from_book": b.ID, "kept_original": true}})
	})
	if err != nil {
		_ = os.RemoveAll(filepath.Join(l.Root, filepath.FromSlash(folder)))
		return "", err
	}
	return id, nil
}

// bookMeta reads a book's metadata back into the import form.
func (l *Library) bookMeta(ctx context.Context, b *store.Book) (store.BookMeta, error) {
	m := store.BookMeta{Title: b.Title, Subtitle: b.Subtitle, Description: b.Description, Publisher: b.Publisher,
		Language: b.Language, ISBN: b.ISBN, ASIN: b.ASIN, PublishedYear: int(b.PublishedYear), Series: b.SeriesName,
		SeriesIndex: b.SeriesIndex}
	for _, kind := range []string{"authors", "narrators"} {
		people, err := store.PeopleFor(ctx, l.DB, kind, []string{b.ID})
		if err != nil {
			return m, err
		}
		for _, n := range people[b.ID] {
			if kind == "authors" {
				m.Authors = append(m.Authors, n.Name)
			} else {
				m.Narrators = append(m.Narrators, n.Name)
			}
		}
	}
	return m, nil
}

// ── duplicate finder ──

var notWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// normalizeTitle lowercases, drops punctuation, a leading article and
// bracketed asides ("(Unabridged)").
func normalizeTitle(s string) string {
	s = strings.ToLower(s)
	for _, open := range []string{"(", "["} {
		if i := strings.Index(s, open); i > 0 {
			s = s[:i]
		}
	}
	s = strings.TrimSpace(notWord.ReplaceAllString(s, " "))
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, a) {
			s = s[len(a):]
			break
		}
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// DuplicatePair is a match found by FindDuplicatePairs.
type DuplicatePair struct {
	A, B, Kind string
	Score      float64
}

// FindDuplicatePairs finds exact (same checksum) and probable (same
// normalized title and first author, durations within 2 %) matches.
func FindDuplicatePairs(books []store.DuplicateBook) []DuplicatePair {
	var out []DuplicatePair
	seen := map[[2]string]bool{}
	add := func(a, b, kind string, score float64) {
		if a > b {
			a, b = b, a
		}
		if !seen[[2]string{a, b}] {
			seen[[2]string{a, b}] = true
			out = append(out, DuplicatePair{A: a, B: b, Kind: kind, Score: score})
		}
	}
	bySum := map[string][]string{}
	for _, b := range books {
		if b.Checksum != "" {
			bySum[b.Checksum] = append(bySum[b.Checksum], b.ID)
		}
	}
	for _, ids := range bySum {
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				add(ids[i], ids[j], "exact", 1)
			}
		}
	}
	byName := map[string][]store.DuplicateBook{}
	for _, b := range books {
		if t := normalizeTitle(b.Title); t != "" {
			key := t + "\x00" + normalizeTitle(b.Author)
			byName[key] = append(byName[key], b)
		}
	}
	for _, group := range byName {
		for i := range group {
			for j := i + 1; j < len(group); j++ {
				a, b := group[i], group[j]
				longer := max(a.DurationMS, b.DurationMS)
				if longer <= 0 {
					continue
				}
				diff := math.Abs(float64(a.DurationMS-b.DurationMS)) / float64(longer)
				if diff <= 0.02 {
					add(a.ID, b.ID, "probable", math.Round((1-diff)*1000)/1000)
				}
			}
		}
	}
	return out
}

// FindDuplicates records new duplicate pairs for review; it returns how many.
func (l *Library) FindDuplicates(ctx context.Context) (int, error) {
	books, err := store.DuplicateScanBooks(ctx, l.DB)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range FindDuplicatePairs(books) {
		added, err := store.AddDuplicateCandidate(ctx, l.DB, p.A, p.B, p.Kind, p.Score)
		if err != nil {
			return n, err
		}
		if added {
			n++
		}
	}
	return n, nil
}

// MergeDuplicate keeps one book of a pair: the other's user data moves to
// it, and the other goes to the Trash.
func (l *Library) MergeDuplicate(ctx context.Context, actor, dupID, keepID, ip string) error {
	d, status, err := store.GetDuplicate(ctx, l.DB, dupID)
	if err != nil {
		return err
	}
	if status != "open" {
		return store.ErrConflict
	}
	drop := d.BookA
	if keepID == d.BookA {
		drop = d.BookB
	} else if keepID != d.BookB {
		return ErrNotInPair
	}
	err = l.DB.InTx(ctx, func(tx *store.Tx) error {
		users, err := store.MoveBookData(ctx, tx, drop, keepID)
		if err != nil {
			return err
		}
		for _, u := range users {
			if err := store.RecordChange(ctx, tx, u, store.EntityLibraryEntry, keepID, "upsert"); err != nil {
				return err
			}
			if err := store.RecordChange(ctx, tx, u, store.EntityProgress, keepID, "upsert"); err != nil {
				return err
			}
		}
		if err := store.DecideDuplicate(ctx, tx, dupID, "merged", actor); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: actor, Action: "book.duplicate_merge", ObjectType: "book", ObjectID: keepID,
			IP: ip, Details: map[string]any{"trashed": drop}})
	})
	if err != nil {
		return err
	}
	return l.Trash(ctx, actor, drop, ip)
}
