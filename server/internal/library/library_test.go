// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/testdb"
)

func newLib(t *testing.T, db *store.DB) *Library {
	t.Helper()
	tl := media.DefaultTools()
	if !tl.Available() {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	l := &Library{DB: db, Tools: tl, Root: filepath.Join(dir, "library"),
		Paths: config.Paths{Cache: filepath.Join(dir, "cache")}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := os.MkdirAll(filepath.Join(l.Root, IncomingDir), 0o750); err != nil {
		t.Fatal(err)
	}
	return l
}

var future = time.Now().Add(time.Hour) // "now" for scans, so files count as settled

func makeTestBooks(t *testing.T, l *Library) {
	t.Helper()
	ctx := context.Background()
	if err := l.Tools.MakeM4B(ctx, filepath.Join(l.Root, "Jane Author", "Saga", "First Book", "first.m4b"), media.FixtureBook{
		Title: "First Book", Author: "Jane Author", Narrator: "Nick Narrator", Series: "Saga", SeriesPart: "1",
		Genre: "Fantasy; Adventure", Seconds: 4, Cover: true,
		Chapters: []media.FixtureChapter{{Title: "One", StartMS: 0, EndMS: 2000}, {Title: "Two", StartMS: 2000, EndMS: 4000}}}); err != nil {
		t.Fatal(err)
	}
	mp3 := media.FixtureBook{Title: "Folder Book", Author: "Sam Writer", Seconds: 2, Frequency: 300}
	for i, name := range []string{"01 - Start.mp3", "02 - Middle.mp3", "10 - End.mp3"} {
		if err := l.Tools.MakeMP3(ctx, filepath.Join(l.Root, "Sam Writer", "Folder Book", name), mp3, strings.TrimSuffix(name[5:], ".mp3"), i+1); err != nil {
			t.Fatal(err)
		}
	}
	bad := filepath.Join(l.Root, "Broken", "Bad Book", "bad.m4b")
	_ = os.MkdirAll(filepath.Dir(bad), 0o750)
	_ = os.WriteFile(bad, []byte("not audio"), 0o600)
}

func findBook(t *testing.T, db *store.DB, title string) *store.Book {
	t.Helper()
	books, err := store.ListMasterBooks(context.Background(), db, store.MasterFilter{Query: title, Limit: 10})
	if err != nil || len(books) == 0 {
		t.Fatalf("book %q not found: %v", title, err)
	}
	return books[0]
}

func TestScanImport(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)

		res, err := l.Scan(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if res.Pending != 3 || res.Added != 0 {
			t.Fatalf("freshly written files must wait to settle: %+v", res)
		}
		res, err = l.Scan(ctx, future)
		if err != nil {
			t.Fatal(err)
		}
		if res.Added != 2 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "bad.m4b") {
			t.Fatalf("scan: %+v", res)
		}

		b := findBook(t, db, "First Book")
		if b.Origin != "scan" || b.UploaderID != "" || b.Status != "ready" || b.SeriesName != "Saga" {
			t.Fatalf("m4b book: %+v", b)
		}
		if !strings.HasPrefix(b.CoverPath, CoverInCache) {
			t.Fatalf("embedded cover not extracted: %q", b.CoverPath)
		}
		chs, _ := store.GetChapters(ctx, db, b.ID)
		if len(chs) != 2 || chs[1].Title != "Two" || chs[1].StartMS != 2000 {
			t.Fatalf("chapters: %+v", chs)
		}
		people, _ := store.PeopleFor(ctx, db, "narrators", []string{b.ID})
		if len(people[b.ID]) != 1 || people[b.ID][0].Name != "Nick Narrator" {
			t.Fatalf("narrators: %+v", people)
		}
		tags, _ := store.TagsFor(ctx, db, b.ID)
		if len(tags) != 2 {
			t.Fatalf("genres: %+v", tags)
		}

		f := findBook(t, db, "Folder Book")
		files, _ := store.GetBookFiles(ctx, db, f.ID)
		if len(files) != 3 || files[2].Path != "10 - End.mp3" || files[1].OffsetMS != files[0].DurationMS {
			t.Fatalf("mp3 files: %+v", files)
		}
		chs, _ = store.GetChapters(ctx, db, f.ID)
		if len(chs) != 3 || chs[0].Title != "Start" || chs[2].EndMS != f.DurationMS {
			t.Fatalf("mp3 chapters: %+v (duration %d)", chs, f.DurationMS)
		}

		// Re-scan is a no-op; removing a folder quarantines its book; restoring brings it back.
		if res, _ := l.Scan(ctx, future); res.Added != 0 || res.Updated != 0 || res.Missing != 0 {
			t.Fatalf("rescan: %+v", res)
		}
		moved := filepath.Join(t.TempDir(), "away")
		if err := os.Rename(filepath.Join(l.Root, "Sam Writer"), moved); err != nil {
			t.Fatal(err)
		}
		if res, _ := l.Scan(ctx, future); res.Missing != 1 {
			t.Fatalf("missing: %+v", res)
		}
		if b, _ := store.GetBook(ctx, db, f.ID); b.Status != "quarantined" {
			t.Fatalf("status %s", b.Status)
		}
		if err := os.Rename(moved, filepath.Join(l.Root, "Sam Writer")); err != nil {
			t.Fatal(err)
		}
		if res, _ := l.Scan(ctx, future); res.Restored != 1 {
			t.Fatalf("restore: %+v", res)
		}
	})
}

// fakeUpload simulates a finished tus upload: data file + uploads row.
func fakeUpload(t *testing.T, l *Library, userID, filename string, data []byte, sha string) string {
	t.Helper()
	id := store.NewID()
	if err := os.WriteFile(filepath.Join(l.IncomingPath(), id), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if sha != "" {
		info := `{"MetaData":{"sha256":"` + sha + `"}}`
		_ = os.WriteFile(filepath.Join(l.IncomingPath(), id+".info"), []byte(info), 0o600)
	}
	now := store.NowMS()
	if _, err := l.DB.Exec(context.Background(), `INSERT INTO uploads (id, user_id, filename, size_bytes, received, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'processing', ?, ?)`, id, userID, filename, len(data), len(data), now, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func newUser(t *testing.T, db *store.DB, name string) string {
	t.Helper()
	id, err := store.CreateUser(context.Background(), db, store.NewUser{Username: name, Status: "active", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func uploadStatus(t *testing.T, db *store.DB, id string) (string, string) {
	var status string
	var msg *string
	if err := db.QueryRow(context.Background(), `SELECT status, error FROM uploads WHERE id = ?`, id).Scan(&status, &msg); err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		return status, ""
	}
	return status, *msg
}

func TestUploadPipeline(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		alice, bob := newUser(t, db, "alice"), newUser(t, db, "bob")

		src := filepath.Join(t.TempDir(), "src.m4b")
		if err := l.Tools.MakeM4B(ctx, src, media.FixtureBook{Title: "Uploaded: A Tale?", Author: "Upl Author", Series: "Ser",
			Seconds: 3, Frequency: 500, SeriesPart: "2"}); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(src)
		sum, _, _ := HashFile(src)

		// 1. Single m4b with a correct checksum → canonical folder, owned by alice.
		up := fakeUpload(t, l, alice, "my book.m4b", data, sum)
		res, err := l.ProcessUpload(ctx, up)
		if err != nil || res.Duplicate {
			t.Fatalf("process: %+v %v", res, err)
		}
		b, _ := store.GetBook(ctx, db, res.BookID)
		if b.FolderPath != "Upl Author/Ser/Uploaded_ A Tale_" || b.SeriesName != "Ser" || b.SeriesIndex != "2" || b.Status != "ready" || b.UploaderID != alice || b.Origin != "upload" {
			t.Fatalf("book: %+v", b)
		}
		if _, err := os.Stat(filepath.Join(l.Root, filepath.FromSlash(b.FolderPath), "my book.m4b")); err != nil {
			t.Fatal("file not in canonical folder:", err)
		}
		if st, _ := uploadStatus(t, db, up); st != "done" {
			t.Fatalf("upload status %s", st)
		}
		if leftovers, _ := os.ReadDir(l.IncomingPath()); len(leftovers) != 0 {
			t.Fatalf("staging not cleaned: %v", leftovers)
		}
		if used, _ := store.UsedBytes(ctx, db, alice); used != b.SizeBytes {
			t.Fatalf("alice quota %d, want %d", used, b.SizeBytes)
		}
		if it, err := store.VisibleBook(ctx, db, alice, b.ID); err != nil || it.Source != "own_upload" {
			t.Fatalf("alice should see her upload: %v", err)
		}
		if _, err := store.VisibleBook(ctx, db, bob, b.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("bob must not see alice's upload")
		}

		// 2. Bob uploads the same bytes → linked to the existing book, no quota for bob.
		dupUp := fakeUpload(t, l, bob, "copy.m4b", data, "")
		res2, err := l.ProcessUpload(ctx, dupUp)
		if err != nil || !res2.Duplicate || res2.BookID != b.ID {
			t.Fatalf("dedup: %+v %v", res2, err)
		}
		if used, _ := store.UsedBytes(ctx, db, bob); used != 0 {
			t.Fatalf("bob quota %d, want 0 (dedup counts for the original uploader)", used)
		}
		if _, err := store.VisibleBook(ctx, db, bob, b.ID); err != nil {
			t.Fatal("bob should now see the deduplicated book")
		}

		// 3. A zip of mp3s without a top folder → one book.
		zpath := filepath.Join(t.TempDir(), "book.zip")
		zf, _ := os.Create(zpath)
		zw := zip.NewWriter(zf)
		for i, n := range []string{"02.mp3", "01.mp3"} {
			tmp := filepath.Join(t.TempDir(), n)
			if err := l.Tools.MakeMP3(ctx, tmp, media.FixtureBook{Title: "Zipped", Author: "Zed", Seconds: 2, Frequency: 600 + i}, n, i+1); err != nil {
				t.Fatal(err)
			}
			w, _ := zw.Create(n)
			raw, _ := os.ReadFile(tmp)
			_, _ = w.Write(raw)
		}
		w, _ := zw.Create("__MACOSX/._01.mp3")
		_, _ = w.Write([]byte("junk"))
		_ = zw.Close()
		_ = zf.Close()
		zdata, _ := os.ReadFile(zpath)
		res3, err := l.ProcessUpload(ctx, fakeUpload(t, l, alice, "Zipped Book.zip", zdata, ""))
		if err != nil {
			t.Fatal(err)
		}
		zb, _ := store.GetBook(ctx, db, res3.BookID)
		zfiles, _ := store.GetBookFiles(ctx, db, zb.ID)
		if zb.Title != "Zipped" || len(zfiles) != 2 || zfiles[0].Path != "01.mp3" {
			t.Fatalf("zip book: %+v %+v", zb, zfiles)
		}

		// 4. Corrupt file and checksum mismatch fail cleanly, permanently.
		bad := fakeUpload(t, l, alice, "bad.m4b", []byte("garbage"), "")
		if _, err := l.ProcessUpload(ctx, bad); err == nil {
			t.Fatal("corrupt upload accepted")
		} else if !isPermanent(err) {
			t.Fatalf("corrupt upload error should be permanent: %v", err)
		}
		if st, msg := uploadStatus(t, db, bad); st != "failed" || !strings.Contains(msg, "invalid audio") {
			t.Fatalf("corrupt status %s %q", st, msg)
		}
		mism := fakeUpload(t, l, alice, "x.m4b", data, strings.Repeat("0", 64))
		if _, err := l.ProcessUpload(ctx, mism); err == nil {
			t.Fatal("checksum mismatch accepted")
		}
		if _, msg := uploadStatus(t, db, mism); !strings.Contains(msg, "checksum mismatch") {
			t.Fatalf("mismatch message %q", msg)
		}
		if leftovers, _ := os.ReadDir(l.IncomingPath()); len(leftovers) != 0 {
			t.Fatalf("failed uploads left files: %v", leftovers)
		}
	})
}

func isPermanent(err error) bool { return jobs.IsPermanent(err) }

func TestUploadCrashRecovery(t *testing.T) {
	db := testdb.SQLite(t)
	ctx := context.Background()
	l := newLib(t, db)
	alice := newUser(t, db, "alice")
	src := filepath.Join(t.TempDir(), "src.m4b")
	if err := l.Tools.MakeM4B(ctx, src, media.FixtureBook{Title: "Crashy", Author: "C", Seconds: 2, Frequency: 700}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(src)
	up := fakeUpload(t, l, alice, "crashy.m4b", data, "")

	// Simulate a crash right after the DB insert, before the rename: stage, analyze and
	// insert exactly as ProcessUpload does, then stop.
	u, _ := l.loadUpload(ctx, up)
	staging := filepath.Join(l.IncomingPath(), up+".d")
	if err := l.stage(u, staging); err != nil {
		t.Fatal(err)
	}
	cands, _ := DetectUpload(staging)
	a := l.Analyze(ctx, staging, cands[0])
	target, _ := l.targetFolder(a.Book.Meta)
	a.Book.FolderPath, a.Book.Origin, a.Book.Status, a.Book.UploaderID = target, "upload", "processing", alice
	id, err := store.InsertBook(ctx, db, &a.Book)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(ctx, `UPDATE uploads SET book_id = ? WHERE id = ?`, id, up)
	_ = store.AddEntry(ctx, db, alice, id, "own_upload")
	if err := writeMoveNote(staging, cands[0].Rel); err != nil {
		t.Fatal(err)
	}

	// The retried job finishes the move.
	res, err := l.ProcessUpload(ctx, up)
	if err != nil || res.BookID != id {
		t.Fatalf("recovery: %+v %v", res, err)
	}
	b, _ := store.GetBook(ctx, db, id)
	if b.Status != "ready" {
		t.Fatalf("status %s", b.Status)
	}
	if _, err := os.Stat(filepath.Join(l.Root, filepath.FromSlash(target), "crashy.m4b")); err != nil {
		t.Fatal("file not moved:", err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, filepath.FromSlash(target), moveNote)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("move note left behind")
	}
}
