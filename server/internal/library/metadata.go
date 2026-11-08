// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/quip1/simple-apb/server/internal/fsutil"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/metadata"
	"github.com/quip1/simple-apb/server/internal/store"
)

// queueMatch asks the metadata pipeline to look a new book up online.
func (l *Library) queueMatch(ctx context.Context, bookID string) {
	if _, err := jobs.Enqueue(context.WithoutCancel(ctx), l.DB, metadata.JobMatch, metadata.MatchPayload{BookID: bookID}, time.Time{}, ""); err != nil {
		l.Log.Warn("queue metadata match", "book", bookID, "err", err)
	}
}

// SaveCover stores a JPEG as a book's cover (brief §6.12): cover.jpg in the
// book folder, crash-safe; if the folder can't be written (a read-only
// library mount) it goes to the cache instead. A cover.jpg that is already
// there is replaced only when the book's cover points at it.
func (l *Library) SaveCover(ctx context.Context, bookID string, jpeg []byte) error {
	b, err := store.GetBook(ctx, l.DB, bookID)
	if err != nil {
		return err
	}
	rel := filepath.ToSlash(filepath.Join(b.FolderPath, "cover.jpg"))
	abs := filepath.Join(l.Root, filepath.FromSlash(rel))
	path := ""
	_, statErr := os.Stat(abs)
	if errors.Is(statErr, os.ErrNotExist) || b.CoverPath == CoverInLibrary+rel {
		if err := fsutil.WriteFileAtomic(abs, jpeg, 0o640); err == nil {
			path = CoverInLibrary + rel
		} else {
			l.Log.Warn("cover: book folder not writable, using the cache", "book", bookID, "err", err)
		}
	}
	if path == "" {
		cacheRel := filepath.ToSlash(filepath.Join("covers", bookID, "cover.jpg"))
		if err := os.MkdirAll(filepath.Join(l.Paths.Cache, "covers", bookID), 0o750); err != nil {
			return err
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(l.Paths.Cache, filepath.FromSlash(cacheRel)), jpeg, 0o640); err != nil {
			return err
		}
		path = CoverInCache + cacheRel
	}
	return l.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetCoverPath(ctx, tx, bookID, path); err != nil {
			return err
		}
		return store.RecordBookChange(ctx, tx, bookID)
	})
}
