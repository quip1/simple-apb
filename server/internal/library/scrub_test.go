// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/testdb"
)

func TestScrubQuarantinesCorruptFiles(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		l := newLib(t, db)
		makeTestBooks(t, l)
		if _, err := l.Scan(ctx, future); err != nil {
			t.Fatal(err)
		}
		res, err := l.Scrub(ctx, 0)
		if err != nil || res.Books != 2 || res.Mismatches != 0 || res.Files != 4 {
			t.Fatalf("clean scrub: %+v %v", res, err)
		}
		// Flip one byte in the middle of an mp3 (same size, so the scanner can't notice).
		p := filepath.Join(l.Root, "Sam Writer", "Folder Book", "02 - Middle.mp3")
		b, _ := os.ReadFile(p)
		b[len(b)/2] ^= 0xFF
		_ = os.WriteFile(p, b, 0o600)

		// A "recently modified" scrub with a window that excludes it finds nothing…
		old := time.Now().Add(-48 * time.Hour)
		_ = os.Chtimes(p, old, old)
		if res, _ := l.Scrub(ctx, 24*time.Hour); res.Mismatches != 0 {
			t.Fatalf("windowed scrub checked an old file: %+v", res)
		}
		// …the full scrub catches it and quarantines the book.
		res, err = l.Scrub(ctx, 0)
		if err != nil || res.Mismatches != 1 || len(res.Quarantined) != 1 {
			t.Fatalf("scrub: %+v %v", res, err)
		}
		bk, _ := store.GetBook(ctx, db, res.Quarantined[0])
		if bk.Status != "quarantined" || !strings.Contains(bk.StatusReason, "02 - Middle.mp3") {
			t.Fatalf("book after scrub: %s %q", bk.Status, bk.StatusReason)
		}
	})
}

func TestRecoverUploads(t *testing.T) {
	db := testdb.SQLite(t)
	ctx := context.Background()
	l := newLib(t, db)
	alice := newUser(t, db, "alice")
	// An orphan staging file (no upload row) and a "processing" upload with no job.
	_ = os.WriteFile(filepath.Join(l.IncomingPath(), "orphan-123"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(l.IncomingPath(), "orphan-123.info"), []byte("{}"), 0o600)
	stuck := fakeUpload(t, l, alice, "a.m4b", []byte("data"), "")
	requeued, removed, err := l.RecoverUploads(ctx)
	if err != nil || requeued != 1 || removed != 2 {
		t.Fatalf("recover: requeued %d removed %d err %v", requeued, removed, err)
	}
	if _, err := os.Stat(filepath.Join(l.IncomingPath(), stuck)); err != nil {
		t.Fatal("a live upload's data was removed")
	}
	// Idempotent: the job now exists, so nothing is requeued again.
	if requeued, _, _ := l.RecoverUploads(ctx); requeued != 0 {
		t.Fatalf("requeued twice: %d", requeued)
	}
	// Abandoned "receiving" uploads expire.
	_, _ = db.Exec(ctx, `UPDATE uploads SET status = 'receiving', updated_at = 0 WHERE id = ?`, stuck)
	if n, _ := l.ExpireStaleUploads(ctx, 7*24*time.Hour); n != 1 {
		t.Fatalf("expired %d", n)
	}
}
