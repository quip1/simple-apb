// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/testdb"
)

func TestNormalizeChapters(t *testing.T) {
	cs, err := NormalizeChapters([]ChapterInput{{"Two", 5000}, {"One", 300}, {"Three", 9000}}, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.Chapter{{0, "One", 0, 5000}, {1, "Two", 5000, 9000}, {2, "Three", 9000, 10_000}}
	for i, c := range cs {
		if c != want[i] {
			t.Fatalf("chapter %d: %+v, want %+v", i, c, want[i])
		}
	}
	for name, in := range map[string][]ChapterInput{
		"empty":          nil,
		"blank title":    {{" ", 0}},
		"past the end":   {{"A", 0}, {"B", 10_000}},
		"same start":     {{"A", 0}, {"B", 4000}, {"C", 4000}},
		"negative":       {{"A", -1}},
		"title too long": {{strings.Repeat("x", 201), 0}},
	} {
		if _, err := NormalizeChapters(in, 10_000); !errors.Is(err, ErrBadChapters) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestChaptersFromSilences(t *testing.T) {
	files := []store.BookFile{{OffsetMS: 0, DurationMS: 600_000}, {OffsetMS: 600_000, DurationMS: 300_000}}
	silences := [][]media.Silence{
		{{StartMS: 0, EndMS: 1500}, {StartMS: 200_000, EndMS: 202_000}, {StartMS: 205_000, EndMS: 207_000}, {StartMS: 598_500, EndMS: 600_000}},
		{{StartMS: 100_000, EndMS: 103_000}},
	}
	cs := ChaptersFromSilences(files, silences, 900_000)
	starts := []int64{}
	for _, c := range cs {
		starts = append(starts, c.StartMS)
	}
	// File starts, plus silences inside files; the one 5 s after another is dropped.
	want := []int64{0, 201_500, 600_000, 702_500}
	if len(starts) != len(want) {
		t.Fatalf("starts = %v, want %v", starts, want)
	}
	for i := range want {
		if starts[i] != want[i] {
			t.Fatalf("starts = %v, want %v", starts, want)
		}
	}
	if cs[3].EndMS != 900_000 || cs[0].EndMS != 201_500 || cs[1].Title != "Chapter 2" {
		t.Fatalf("chapters: %+v", cs)
	}
}

func TestFindDuplicatePairs(t *testing.T) {
	pairs := FindDuplicatePairs([]store.DuplicateBook{
		{ID: "a", Title: "The Lantern Road", Author: "Mara Lindqvist", Checksum: "x", DurationMS: 100_000},
		{ID: "b", Title: "Lantern Road (Unabridged)", Author: "mara lindqvist", Checksum: "y", DurationMS: 101_500},
		{ID: "c", Title: "Lantern Road", Author: "Mara Lindqvist", Checksum: "z", DurationMS: 110_000}, // 10 % longer
		{ID: "d", Title: "Something Else", Author: "Other", Checksum: "x", DurationMS: 5},              // same bytes as a
	})
	got := map[string]string{}
	for _, p := range pairs {
		got[p.A+p.B] = p.Kind
	}
	if got["ad"] != "exact" || got["ab"] != "probable" || len(got) != 2 {
		t.Fatalf("pairs: %+v", pairs)
	}
}

func scanned(t *testing.T, l *Library) {
	t.Helper()
	if _, err := l.Scan(context.Background(), future); err != nil {
		t.Fatal(err)
	}
}

// reimportBook re-reads a book from its files, as a scan does when they change.
func reimportBook(t *testing.T, l *Library, b *store.Book) {
	t.Helper()
	cands, err := Detect(l.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.Rel == b.FolderPath {
			if err := l.reimport(context.Background(), b.ID, c); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no candidate for %s", b.FolderPath)
}

func chapterTitles(t *testing.T, db *store.DB, id string) []string {
	t.Helper()
	cs, err := store.GetChapters(context.Background(), db, id)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Title
	}
	return out
}

// The Phase 5 acceptance test: chapter edits survive a re-scan.
func TestChapterEditsSurviveRescan(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)
		scanned(t, l)

		// An m4b: the edit is written into the file itself.
		b := findBook(t, db, "First Book")
		cs, write, err := l.SaveChapters(ctx, "", b.ID, []ChapterInput{{"Opening", 0}, {"Middle", 1500}, {"Close", 3000}})
		if err != nil || !write || len(cs) != 3 {
			t.Fatalf("save: %v %v %+v", err, write, cs)
		}
		if err := l.WriteChapters(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		files, _ := store.GetBookFiles(ctx, db, b.ID)
		path := l.FilePath(b, files[0])
		probe, err := l.Tools.ProbeFile(ctx, path)
		if err != nil || len(probe.Chapters) != 3 || probe.Chapters[1].Title != "Middle" || probe.Chapters[1].StartMS != 1500 {
			t.Fatalf("chapters in the file: %+v %v", probe, err)
		}
		if sum, _, _ := HashFile(path); sum != files[0].Checksum {
			t.Fatal("the file's checksum wasn't updated after the remux")
		}
		if res, err := l.Scrub(ctx, 0); err != nil || res.Mismatches != 0 {
			t.Fatalf("scrub after remux: %+v %v", res, err)
		}
		// Even unlocked, a re-scan reads the edited chapters back from the file.
		if err := store.UnlockFields(ctx, db, b.ID, []string{store.LockedChapters}); err != nil {
			t.Fatal(err)
		}
		reimportBook(t, l, b)
		if got := chapterTitles(t, db, b.ID); strings.Join(got, ",") != "Opening,Middle,Close" {
			t.Fatalf("m4b chapters after re-scan: %v", got)
		}

		// An mp3 folder: chapters can't live in the files; the lock keeps them.
		fb := findBook(t, db, "Folder Book")
		if _, write, err := l.SaveChapters(ctx, "", fb.ID, []ChapterInput{{"Part A", 0}, {"Part B", 2500}}); err != nil || write {
			t.Fatalf("save mp3 chapters: %v %v", err, write)
		}
		extra := media.FixtureBook{Title: "Folder Book", Author: "Sam Writer", Seconds: 2, Frequency: 300}
		if err := l.Tools.MakeMP3(ctx, filepath.Join(l.Root, "Sam Writer", "Folder Book", "11 - Extra.mp3"), extra, "Extra", 4); err != nil {
			t.Fatal(err)
		}
		if res, err := l.Scan(ctx, future); err != nil || res.Updated != 1 {
			t.Fatalf("rescan: %+v %v", res, err)
		}
		if got := chapterTitles(t, db, fb.ID); strings.Join(got, ",") != "Part A,Part B" {
			t.Fatalf("mp3 chapters after re-scan: %v", got)
		}
		cs2, _ := store.GetChapters(ctx, db, fb.ID)
		nb, _ := store.GetBook(ctx, db, fb.ID)
		if cs2[1].EndMS != nb.DurationMS {
			t.Fatalf("last chapter should end with the (longer) book: %d vs %d", cs2[1].EndMS, nb.DurationMS)
		}
	})
}

func TestMergeReplacesFiles(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)
		scanned(t, l)
		b := findBook(t, db, "Folder Book")
		before, _ := store.GetChapters(ctx, db, b.ID)
		id, err := l.Merge(ctx, MergePayload{BookID: b.ID})
		if err != nil || id != b.ID {
			t.Fatalf("merge: %v %s", err, id)
		}
		files, _ := store.GetBookFiles(ctx, db, b.ID)
		if len(files) != 1 || !strings.HasSuffix(files[0].Path, ".m4b") {
			t.Fatalf("files after merge: %+v", files)
		}
		nb, _ := store.GetBook(ctx, db, b.ID)
		probe, err := l.Tools.ProbeFile(ctx, l.FilePath(nb, files[0]))
		if err != nil || len(probe.Chapters) != len(before) || probe.Codec != "mp3" {
			t.Fatalf("merged file: %+v %v (mp3 audio is copied, not converted)", probe, err)
		}
		if d := nb.DurationMS - b.DurationMS; d < -500 || d > 500 {
			t.Fatalf("duration changed by %d ms", d)
		}
		left, _ := os.ReadDir(filepath.Join(l.Root, "Sam Writer", "Folder Book"))
		if len(left) != 1 {
			t.Fatalf("old files left behind: %v", left)
		}
		if res, err := l.Scan(ctx, future); err != nil || res.Updated != 0 || res.Added != 0 {
			t.Fatalf("scan after merge should find nothing new: %+v %v", res, err)
		}
		if res, err := l.Scrub(ctx, 0); err != nil || res.Mismatches != 0 {
			t.Fatalf("scrub after merge: %+v %v", res, err)
		}

		// A merge interrupted after the database switched: the re-run finishes it.
		dir := filepath.Join(l.Root, "Sam Writer", "Folder Book")
		_ = os.WriteFile(filepath.Join(dir, "01 - Start.mp3"), []byte("left over"), 0o600)
		_ = os.WriteFile(filepath.Join(dir, mergeNote), []byte("01 - Start.mp3"), 0o600)
		if res, err := l.Scan(ctx, future); err != nil || res.Updated != 0 {
			t.Fatalf("a scan must leave a folder with a pending merge alone: %+v %v", res, err)
		}
		if _, err := l.Merge(ctx, MergePayload{BookID: b.ID}); err != nil {
			t.Fatal(err)
		}
		if left, _ := os.ReadDir(dir); len(left) != 1 {
			t.Fatalf("re-run didn't finish the merge: %v", left)
		}
	})
}

func TestMergeKeepsOriginal(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)
		scanned(t, l)
		b := findBook(t, db, "Folder Book")
		id, err := l.Merge(ctx, MergePayload{BookID: b.ID, KeepOriginal: true, Reencode: true})
		if err != nil || id == b.ID {
			t.Fatalf("merge: %v %s", err, id)
		}
		if files, _ := store.GetBookFiles(ctx, db, b.ID); len(files) != 3 {
			t.Fatalf("original changed: %d files", len(files))
		}
		nb, err := store.GetBook(ctx, db, id)
		if err != nil || nb.Title != "Folder Book" || nb.Status != "ready" {
			t.Fatalf("new book: %+v %v", nb, err)
		}
		files, _ := store.GetBookFiles(ctx, db, id)
		probe, err := l.Tools.ProbeFile(ctx, l.FilePath(nb, files[0]))
		if err != nil || probe.Codec != "aac" {
			t.Fatalf("re-encoded file: %+v %v", probe, err)
		}
		if res, err := l.Scan(ctx, future); err != nil || res.Added != 0 {
			t.Fatalf("the new book's folder must already be known: %+v %v", res, err)
		}
	})
}

func TestDetectChapters(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)
		scanned(t, l)
		b := findBook(t, db, "Folder Book")
		// Fixture tones have no silences: each file still starts a chapter.
		cs, err := l.DetectChapters(ctx, DetectPayload{BookID: b.ID, NoiseDB: -35, MinSilenceMS: 500})
		if err != nil || len(cs) == 0 || cs[0].StartMS != 0 {
			t.Fatalf("detect: %+v %v", cs, err)
		}
	})
}

func TestMergeDuplicateMovesUserData(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)
		scanned(t, l)
		keep, drop := findBook(t, db, "First Book"), findBook(t, db, "Folder Book")
		u := newUser(t, db, "listener")
		if err := store.AddEntry(ctx, db, u, drop.ID, "own_upload"); err != nil {
			t.Fatal(err)
		}
		now := store.NowMS()
		if _, err := db.Exec(ctx, `INSERT INTO progress (id, user_id, book_id, position_ms, device_id, client_ts, speed, created_at, updated_at)
			VALUES (?, ?, ?, 1234, 'dev', ?, 1.0, ?, ?)`, store.NewID(), u, drop.ID, now, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddDuplicateCandidate(ctx, db, keep.ID, drop.ID, "probable", 0.99); err != nil {
			t.Fatal(err)
		}
		ds, err := store.ListOpenDuplicates(ctx, db, 10, 0)
		if err != nil || len(ds) != 1 {
			t.Fatalf("open pairs: %+v %v", ds, err)
		}
		if err := l.MergeDuplicate(ctx, "", ds[0].ID, "not-in-pair", ""); !errors.Is(err, ErrNotInPair) {
			t.Fatalf("keeping an outside book: %v", err)
		}
		if err := l.MergeDuplicate(ctx, "", ds[0].ID, keep.ID, ""); err != nil {
			t.Fatal(err)
		}
		var pos int64
		if err := db.QueryRow(ctx, `SELECT position_ms FROM progress WHERE user_id = ? AND book_id = ?`, u, keep.ID).Scan(&pos); err != nil || pos != 1234 {
			t.Fatalf("progress not moved: %d %v", pos, err)
		}
		var n int
		_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM user_library_entries WHERE user_id = ? AND book_id = ?`, u, keep.ID).Scan(&n)
		if n != 1 {
			t.Fatal("library entry not moved")
		}
		if gone, _ := store.GetBook(ctx, db, drop.ID); gone.Status != "trashed" {
			t.Fatalf("the other book should be in the Trash: %s", gone.Status)
		}
		if ds, _ := store.ListOpenDuplicates(ctx, db, 10, 0); len(ds) != 0 {
			t.Fatalf("decided pair still open: %+v", ds)
		}
		if added, _ := store.AddDuplicateCandidate(ctx, db, drop.ID, keep.ID, "exact", 1); added {
			t.Fatal("a decided pair was raised again")
		}
	})
}
