// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
)

// TrashRetention is how long trashed books stay restorable (brief §6.5 rule 7).
const TrashRetention = 30 * 24 * time.Hour

// Job types for the periodic maintenance jobs.
const (
	JobScan         = "library_scan"
	JobPurgeTrash   = "purge_trash"
	JobPurgeRemoved = "purge_removed_entries"
)

// Trash moves a book to the admin Trash (hidden everywhere, files kept).
func (l *Library) Trash(ctx context.Context, actor, bookID, ip string) error {
	return l.DB.InTx(ctx, func(tx *store.Tx) error {
		b, err := store.GetBook(ctx, tx, bookID)
		if err != nil {
			return err
		}
		if b.Status == "trashed" {
			return nil
		}
		if err := store.SetBookStatus(ctx, tx, bookID, "trashed", "deleted by admin"); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: actor, Action: "book.trash", ObjectType: "book", ObjectID: bookID, IP: ip,
			Details: map[string]any{"title": b.Title}})
	})
}

// Restore takes a book out of the Trash.
func (l *Library) Restore(ctx context.Context, actor, bookID, ip string) error {
	return l.DB.InTx(ctx, func(tx *store.Tx) error {
		b, err := store.GetBook(ctx, tx, bookID)
		if err != nil {
			return err
		}
		if b.Status != "trashed" {
			return store.ErrConflict
		}
		if err := store.SetBookStatus(ctx, tx, bookID, "ready", ""); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: actor, Action: "book.restore", ObjectType: "book", ObjectID: bookID, IP: ip})
	})
}

// ErrNotTrashed: only trashed books can be purged.
var ErrNotTrashed = errors.New("book is not in the trash")

// Purge permanently deletes a trashed book's files and record. Files go first:
// a crash in between leaves a trashed row pointing at nothing, which the next
// purge finishes.
func (l *Library) Purge(ctx context.Context, actor, bookID, ip string) error {
	b, err := store.GetBook(ctx, l.DB, bookID)
	if err != nil {
		return err
	}
	if b.Status != "trashed" {
		return ErrNotTrashed
	}
	if err := l.removeFiles(b); err != nil {
		return err
	}
	return l.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.DeleteBook(ctx, tx, bookID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: actor, Action: "book.purge", ObjectType: "book", ObjectID: bookID, IP: ip,
			Details: map[string]any{"title": b.Title, "folder": b.FolderPath, "size_bytes": b.SizeBytes}})
	})
}

func (l *Library) removeFiles(b *store.Book) error {
	if b.FolderPath == "" || strings.Contains(b.FolderPath, "..") {
		return fmt.Errorf("refusing to delete suspicious path %q", b.FolderPath)
	}
	abs := filepath.Join(l.Root, filepath.FromSlash(b.FolderPath))
	if !strings.HasPrefix(abs, filepath.Clean(l.Root)+string(filepath.Separator)) {
		return fmt.Errorf("refusing to delete outside the library: %q", b.FolderPath)
	}
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("delete files: %w", err)
	}
	_ = os.RemoveAll(filepath.Join(l.Paths.Cache, "covers", b.ID))
	return nil
}

// EmptyTrash purges every trashed book; returns how many.
func (l *Library) EmptyTrash(ctx context.Context, actor, ip string) (int, error) {
	books, err := store.TrashedBefore(ctx, l.DB, time.Now().Add(time.Hour).UnixMilli())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range books {
		if err := l.Purge(ctx, actor, b.ID, ip); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// PurgeExpired purges books trashed more than TrashRetention ago (daily job).
func (l *Library) PurgeExpired(ctx context.Context, now time.Time) (int, error) {
	books, err := store.TrashedBefore(ctx, l.DB, now.Add(-TrashRetention).UnixMilli())
	if err != nil {
		return 0, err
	}
	for i, b := range books {
		if err := l.Purge(ctx, "", b.ID, ""); err != nil {
			return i, err
		}
	}
	return len(books), nil
}
