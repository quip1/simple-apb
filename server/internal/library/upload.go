// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/quip1/simple-apb/server/internal/fsutil"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/store"
)

// JobImportUpload is the job type that turns a finished upload into a book.
const JobImportUpload = "import_upload"

// UploadPayload is the job payload.
type UploadPayload struct {
	UploadID string `json:"upload_id"`
}

// UploadResult is stored as the job result and summarizes the outcome.
type UploadResult struct {
	BookID    string `json:"book_id"`
	Duplicate bool   `json:"duplicate"`
}

// IncomingPath is the absolute staging directory for uploads.
func (l *Library) IncomingPath() string { return filepath.Join(l.Root, IncomingDir) }

// UploadAccepts reports whether an upload filename is an accepted type.
func UploadAccepts(name string) bool {
	return IsAudio(name) || strings.EqualFold(filepath.Ext(name), ".zip")
}

type uploadRow struct {
	ID, UserID, Filename, Status, BookID string
}

func (l *Library) loadUpload(ctx context.Context, id string) (*uploadRow, error) {
	var (
		u    uploadRow
		book sql.NullString
	)
	err := l.DB.QueryRow(ctx, `SELECT id, user_id, filename, status, book_id FROM uploads WHERE id = ?`, id).
		Scan(&u.ID, &u.UserID, &u.Filename, &u.Status, &book)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	u.BookID = book.String
	return &u, err
}

func (l *Library) setUpload(ctx context.Context, q store.Querier, id, status, errMsg, bookID string) error {
	var b any
	if bookID != "" {
		b = bookID
	}
	var e any
	if errMsg != "" {
		e = errMsg
	}
	_, err := q.Exec(ctx, `UPDATE uploads SET status = ?, error = ?, book_id = COALESCE(?, book_id), updated_at = ? WHERE id = ?`,
		status, e, b, store.NowMS(), id)
	return err
}

// failUpload marks an upload failed, cleans up its files, and returns a permanent job error.
func (l *Library) failUpload(ctx context.Context, u *uploadRow, cause error) error {
	_ = l.setUpload(ctx, l.DB, u.ID, "failed", cause.Error(), "")
	l.cleanupUpload(u.ID)
	_ = store.Audit(ctx, l.DB, store.AuditEntry{ActorID: u.UserID, Action: "upload.failed", ObjectType: "upload", ObjectID: u.ID,
		Details: map[string]any{"error": cause.Error(), "filename": u.Filename}})
	return jobs.Permanent(cause)
}

func (l *Library) cleanupUpload(id string) {
	in := l.IncomingPath()
	for _, p := range []string{id, id + ".info", id + ".lock", id + ".d", id + ".d.tmp"} {
		_ = os.RemoveAll(filepath.Join(in, p))
	}
}

// ProcessUpload imports a completed upload. It is idempotent: a crash at any
// step is finished by the retried job.
//
// Order: stage files → analyze → (dedup) → DB insert (book status
// "processing", upload.book_id set) → atomic rename into the library →
// status "ready". A crash after the insert is recovered by redoing the rename.
func (l *Library) ProcessUpload(ctx context.Context, id string) (*UploadResult, error) {
	u, err := l.loadUpload(ctx, id)
	if err != nil {
		return nil, jobs.Permanent(err)
	}
	if u.Status == "done" || u.Status == "failed" || u.Status == "cancelled" {
		return &UploadResult{BookID: u.BookID}, nil
	}
	staging := filepath.Join(l.IncomingPath(), id+".d")
	if u.BookID != "" {
		return l.finishMove(ctx, u, staging)
	}
	if _, err := os.Stat(staging); errors.Is(err, os.ErrNotExist) {
		if err := l.stage(u, staging); err != nil {
			return nil, l.failUpload(ctx, u, err)
		}
	}
	cands, err := DetectUpload(staging)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, l.failUpload(ctx, u, errors.New("the upload contains no supported audio files (m4b, m4a, mp3)"))
	}
	if len(cands) > 1 {
		return nil, l.failUpload(ctx, u, errors.New("the upload contains more than one book; upload one book per zip"))
	}
	c := cands[0]
	c.Hint = strings.TrimSuffix(u.Filename, filepath.Ext(u.Filename))
	a := l.Analyze(ctx, staging, c)
	if a.Problem != nil {
		return nil, l.failUpload(ctx, u, fmt.Errorf("invalid audio file: %w", a.Problem))
	}

	// Deduplicate: identical content links to the existing book.
	if dup, err := store.FindBookByChecksum(ctx, l.DB, a.Book.Checksum); err == nil {
		err = l.DB.InTx(ctx, func(tx *store.Tx) error {
			if err := store.AddEntry(ctx, tx, u.UserID, dup.ID, "own_upload"); err != nil {
				return err
			}
			if err := store.RecordChange(ctx, tx, u.UserID, store.EntityLibraryEntry, dup.ID, "upsert"); err != nil {
				return err
			}
			if err := l.setUpload(ctx, tx, u.ID, "done", "", dup.ID); err != nil {
				return err
			}
			return store.Audit(ctx, tx, store.AuditEntry{ActorID: u.UserID, Action: "upload.duplicate", ObjectType: "book",
				ObjectID: dup.ID, Details: map[string]any{"upload_id": u.ID}})
		})
		if err != nil {
			return nil, err
		}
		l.cleanupUpload(u.ID)
		return &UploadResult{BookID: dup.ID, Duplicate: true}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	target, err := l.targetFolder(a.Book.Meta)
	if err != nil {
		return nil, err
	}
	a.Book.FolderPath, a.Book.Origin, a.Book.Status, a.Book.UploaderID = target, "upload", "processing", u.UserID
	srcRel := c.Rel
	var bookID string
	err = l.DB.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if bookID, err = store.InsertBook(ctx, tx, &a.Book); err != nil {
			return err
		}
		if err := store.AddEntry(ctx, tx, u.UserID, bookID, "own_upload"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE uploads SET book_id = ?, status = 'processing', updated_at = ? WHERE id = ?`,
			bookID, store.NowMS(), u.ID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: u.UserID, Action: "upload.import", ObjectType: "book", ObjectID: bookID,
			Details: map[string]any{"upload_id": u.ID, "folder": target}})
	})
	if err != nil {
		return nil, err
	}
	if err := writeMoveNote(staging, srcRel); err != nil {
		return nil, err
	}
	u.BookID = bookID
	return l.finishMove(ctx, u, staging)
}

// The staged sub-path to move is remembered in a small note so a retried job
// (after a crash) moves the same thing.
const moveNote = ".simpleapb-move"

func writeMoveNote(staging, srcRel string) error {
	return fsutil.WriteFileAtomic(filepath.Join(staging, moveNote), []byte(srcRel), 0o640)
}

func (l *Library) finishMove(ctx context.Context, u *uploadRow, staging string) (*UploadResult, error) {
	b, err := store.GetBook(ctx, l.DB, u.BookID)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(l.Root, filepath.FromSlash(b.FolderPath))
	if b.Status == "processing" {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			note, err := os.ReadFile(filepath.Join(staging, moveNote))
			if err != nil {
				return nil, fmt.Errorf("upload %s: staging lost: %w", u.ID, err)
			}
			_ = os.Remove(filepath.Join(staging, moveNote))
			src := staging
			if rel := string(note); rel != "." && rel != "" {
				src = filepath.Join(staging, filepath.FromSlash(rel))
			}
			if err := fsutil.RenameDurable(src, target); err != nil {
				return nil, err
			}
		}
		_ = os.Remove(filepath.Join(target, moveNote))
		if err := store.SetBookStatus(ctx, l.DB, b.ID, "ready", ""); err != nil {
			return nil, err
		}
		if err := store.RecordChange(ctx, l.DB, u.UserID, store.EntityLibraryEntry, b.ID, "upsert"); err != nil {
			return nil, err
		}
		l.coverAfterMove(ctx, b.ID, b.FolderPath)
		l.queueMatch(ctx, b.ID)
	}
	if err := l.setUpload(ctx, l.DB, u.ID, "done", "", b.ID); err != nil {
		return nil, err
	}
	l.cleanupUpload(u.ID)
	return &UploadResult{BookID: b.ID}, nil
}

func (l *Library) coverAfterMove(ctx context.Context, bookID, folder string) {
	files, err := store.GetBookFiles(ctx, l.DB, bookID)
	if err != nil || len(files) == 0 {
		return
	}
	c := Candidate{Rel: folder}
	for _, f := range files {
		c.Files = append(c.Files, path.Join(folder, f.Path))
	}
	a := &Analysis{Cover: findCoverFile(l.Root, c)}
	if a.Cover == "" {
		abs := filepath.Join(l.Root, filepath.FromSlash(c.Files[0]))
		if p, err := l.Tools.ProbeFile(ctx, abs); err == nil && p.HasCover {
			a.Embed = abs
		}
	}
	l.storeCover(ctx, bookID, a)
}

// targetFolder picks /<Author>/<Series?>/<Title>/ (relative), unique on disk.
func (l *Library) targetFolder(m store.BookMeta) (string, error) {
	author := "Unknown Author"
	if len(m.Authors) > 0 {
		author = m.Authors[0]
	}
	parts := []string{SafeName(author, "Unknown Author")}
	if m.Series != "" {
		parts = append(parts, SafeName(m.Series, "Series"))
	}
	title := SafeName(m.Title, "Untitled")
	for i := 1; i < 1000; i++ {
		name := title
		if i > 1 {
			name = fmt.Sprintf("%s (%d)", title, i)
		}
		rel := path.Join(append(append([]string(nil), parts...), name)...)
		if !Exists(l.Root, rel) {
			if err := os.MkdirAll(filepath.Join(l.Root, filepath.FromSlash(path.Dir(rel))), 0o750); err != nil {
				return "", err
			}
			return rel, nil
		}
	}
	return "", errors.New("could not find a free folder name")
}

// stage moves (or unzips) the uploaded data into the staging directory.
func (l *Library) stage(u *uploadRow, staging string) error {
	data := filepath.Join(l.IncomingPath(), u.ID)
	if err := verifySHA256(data, filepath.Join(l.IncomingPath(), u.ID+".info")); err != nil {
		return err
	}
	tmp := staging + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(u.Filename), ".zip") {
		if err := unzip(data, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	} else {
		name := SafeName(filepath.Base(u.Filename), "audio"+filepath.Ext(u.Filename))
		if err := os.Rename(data, filepath.Join(tmp, name)); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	return fsutil.RenameDurable(tmp, staging)
}

// verifySHA256 checks the client-declared sha256 (tus metadata), if any.
func verifySHA256(data, infoPath string) error {
	raw, err := os.ReadFile(infoPath)
	if err != nil {
		return nil // no info file: nothing declared
	}
	var info struct {
		MetaData map[string]string `json:"MetaData"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(info.MetaData["sha256"]))
	if want == "" {
		return nil
	}
	got, _, err := HashFile(data)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("checksum mismatch: the file was corrupted during upload, please retry")
	}
	return nil
}

var zipKeep = map[string]bool{".m4b": true, ".m4a": true, ".mp3": true, ".jpg": true, ".jpeg": true, ".png": true,
	".epub": true, ".txt": true, ".nfo": true, ".cue": true}

// unzip extracts a zip safely: no paths outside dest, only known file types,
// bounded entry count and total size (zip-bomb guard).
func unzip(src, dest string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("not a valid zip file: %w", err)
	}
	defer func() { _ = zr.Close() }()
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	// Audio barely compresses, so a legitimate zip expands little.
	limit := st.Size()*2 + 64<<20
	if len(zr.File) > 5000 {
		return errors.New("zip has too many entries")
	}
	var total int64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(path.Base(name), ".") {
			continue
		}
		clean := path.Clean("/" + name)[1:]
		if clean == "" || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || path.IsAbs(name) {
			return fmt.Errorf("zip entry %q has an unsafe path", f.Name)
		}
		if !zipKeep[strings.ToLower(path.Ext(clean))] {
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > limit {
			return errors.New("zip expands to far more data than expected")
		}
		segs := strings.Split(clean, "/")
		for i := range segs {
			segs[i] = SafeName(segs[i], "_")
		}
		out := filepath.Join(dest, filepath.FromSlash(strings.Join(segs, "/")))
		if err := extractOne(f, out, int64(f.UncompressedSize64)); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(f *zip.File, out string, declared int64) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("read zip entry %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	// Never write more than the header declared (a lying header can't bomb us).
	n, err := io.Copy(w, io.LimitReader(rc, declared+1))
	if err == nil && n > declared {
		err = errors.New("zip entry larger than declared")
	}
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}

// DetectUpload is Detect for a staging folder: loose files at the root form
// one book (a zipped mp3 folder without a top-level directory).
func DetectUpload(root string) ([]Candidate, error) {
	cands, err := Detect(root)
	if err != nil {
		return nil, err
	}
	var loose, other []Candidate
	for _, c := range cands {
		if !strings.Contains(c.Rel, "/") && len(c.Files) == 1 && c.Rel == c.Files[0] {
			loose = append(loose, c)
		} else {
			other = append(other, c)
		}
	}
	if len(loose) == 0 {
		return other, nil
	}
	merged := Candidate{Rel: "."}
	for _, c := range loose {
		merged.Files = append(merged.Files, c.Files...)
		if c.Newest.After(merged.Newest) {
			merged.Newest = c.Newest
		}
	}
	hasMP3 := false
	for _, f := range merged.Files {
		hasMP3 = hasMP3 || strings.EqualFold(filepath.Ext(f), ".mp3")
	}
	if len(loose) > 1 && !hasMP3 {
		return append(other, loose...), nil // several m4b at root = several books
	}
	return append(other, merged), nil
}
