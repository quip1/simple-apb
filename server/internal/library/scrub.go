// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/store"
)

// JobScrub re-hashes library files against stored checksums.
const JobScrub = "integrity_scrub"

// ScrubPayload limits a scrub to recently modified files (startup check).
type ScrubPayload struct {
	ModifiedWithin time.Duration `json:"modified_within,omitempty"`
}

// ScrubResult summarizes a scrub.
type ScrubResult struct {
	Books       int      `json:"books"`
	Files       int      `json:"files"`
	Mismatches  int      `json:"mismatches"`
	Missing     int      `json:"missing"`
	Quarantined []string `json:"quarantined,omitempty"` // book ids
}

const reasonMismatch = "checksum mismatch"

// Scrub verifies files of ready books (brief §6.18). A mismatch or missing
// file quarantines the book (hidden, flagged for the admin). With
// modifiedWithin > 0 only files changed that recently are checked.
func (l *Library) Scrub(ctx context.Context, modifiedWithin time.Duration) (*ScrubResult, error) {
	res := &ScrubResult{}
	rows, err := l.DB.Query(ctx, `SELECT id FROM books WHERE status = 'ready' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	cutoff := time.Now().Add(-modifiedWithin)
	for _, id := range ids {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		b, err := store.GetBook(ctx, l.DB, id)
		if err != nil {
			continue
		}
		files, err := store.GetBookFiles(ctx, l.DB, id)
		if err != nil {
			return res, err
		}
		res.Books++
		var problem string
		if len(files) == 0 {
			// A playable book with no audio on record is broken, whatever the cause.
			res.Missing++
			problem = "no audio files on record"
		}
		for _, f := range files {
			p := l.FilePath(b, f)
			st, err := os.Stat(p)
			if err != nil {
				res.Missing++
				problem = reasonMissing
				break
			}
			if modifiedWithin > 0 && st.ModTime().Before(cutoff) {
				continue
			}
			res.Files++
			sum, _, err := HashFile(p)
			if err != nil {
				return res, err
			}
			if sum != f.Checksum {
				res.Mismatches++
				problem = fmt.Sprintf("%s: %s", reasonMismatch, f.Path)
				break
			}
			if _, err := l.DB.Exec(ctx, `UPDATE book_files SET verified_at = ? WHERE id = ?`, store.NowMS(), f.ID); err != nil {
				return res, err
			}
		}
		if problem == "" {
			continue
		}
		err = l.DB.InTx(ctx, func(tx *store.Tx) error {
			if err := store.SetBookStatus(ctx, tx, id, "quarantined", problem); err != nil {
				return err
			}
			if err := store.RecordBookChange(ctx, tx, id); err != nil {
				return err
			}
			return store.Audit(ctx, tx, store.AuditEntry{Action: "book.integrity_failed", ObjectType: "book", ObjectID: id,
				Details: map[string]any{"problem": problem}})
		})
		if err != nil {
			return res, err
		}
		res.Quarantined = append(res.Quarantined, id)
	}
	return res, nil
}

// FilePath is the absolute path of a book file.
func (l *Library) FilePath(b *store.Book, f store.BookFile) string {
	folder := filepath.FromSlash(b.FolderPath)
	// A single-file book whose folder_path is the file itself (several m4b per folder).
	if strings.EqualFold(filepath.Ext(folder), filepath.Ext(f.Path)) && filepath.Base(folder) == f.Path {
		return filepath.Join(l.Root, folder)
	}
	return filepath.Join(l.Root, folder, filepath.FromSlash(f.Path))
}

// RecoverUploads runs after an unclean shutdown: staging files with no upload
// record are deleted, and uploads stuck in "processing" without a pending job
// get their import job back. Uploads still "receiving" are left alone — tus
// clients resume them.
func (l *Library) RecoverUploads(ctx context.Context) (requeued, removed int, err error) {
	known := map[string]string{}
	rows, err := l.DB.Query(ctx, `SELECT id, status FROM uploads`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		known[id] = st
	}
	_ = rows.Close()
	entries, err := os.ReadDir(l.IncomingPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, 0, err
	}
	for _, e := range entries {
		id := e.Name()
		for _, suf := range []string{".info", ".lock", ".d.tmp", ".d"} {
			id = strings.TrimSuffix(id, suf)
		}
		if st, ok := known[id]; !ok || st == "done" || st == "failed" || st == "cancelled" {
			_ = os.RemoveAll(filepath.Join(l.IncomingPath(), e.Name()))
			removed++
		}
	}
	for id, st := range known {
		if st != "processing" {
			continue
		}
		var n int
		err := l.DB.QueryRow(ctx, `SELECT COUNT(*) FROM jobs WHERE type = ? AND status IN ('queued', 'running') AND payload LIKE ?`,
			JobImportUpload, "%"+id+"%").Scan(&n)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return requeued, removed, err
		}
		if n == 0 {
			if _, err := jobs.Enqueue(ctx, l.DB, JobImportUpload, UploadPayload{UploadID: id}, time.Time{}, ""); err != nil {
				return requeued, removed, err
			}
			requeued++
		}
	}
	return requeued, removed, nil
}

// ExpireStaleUploads cancels uploads that have been "receiving" for longer
// than maxAge (abandoned) and frees their staging space.
func (l *Library) ExpireStaleUploads(ctx context.Context, maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge).UnixMilli()
	rows, err := l.DB.Query(ctx, `SELECT id FROM uploads WHERE status = 'receiving' AND updated_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	for _, id := range ids {
		if _, err := l.DB.Exec(ctx, `UPDATE uploads SET status = 'cancelled', error = 'abandoned', updated_at = ? WHERE id = ?`, store.NowMS(), id); err != nil {
			return 0, err
		}
		l.cleanupUpload(id)
	}
	return len(ids), nil
}
