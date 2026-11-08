// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
)

// SettleTime: a new folder is imported only once it hasn't changed for this
// long (brief §6.4), so half-copied books aren't imported.
const SettleTime = 30 * time.Second

// ScanResult summarizes one scan.
type ScanResult struct {
	Added    int      `json:"added"`
	Updated  int      `json:"updated"`
	Missing  int      `json:"missing"`
	Restored int      `json:"restored"`
	Pending  int      `json:"pending"`
	Errors   []string `json:"errors,omitempty"`
}

const reasonMissing = "files missing from the library folder"

// Scan reconciles the library folder with the master store.
func (l *Library) Scan(ctx context.Context, now time.Time) (*ScanResult, error) {
	res := &ScanResult{}
	cands, err := Detect(l.Root)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", l.Root, err)
	}
	known, err := store.ListBookFolders(ctx, l.DB)
	if err != nil {
		return nil, err
	}
	// A candidate inside a known single-file or multi-disc book belongs to it.
	seen := map[string]bool{}
	for _, c := range cands {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		kb := known[c.Rel]
		if kb == nil {
			if parent := knownAncestor(known, c.Rel); parent != nil {
				seen[parent.FolderPath] = true
				continue
			}
			if now.Sub(c.Newest) < SettleTime {
				res.Pending++
				continue
			}
			if err := l.importNew(ctx, c); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", c.Rel, err))
				continue
			}
			res.Added++
			continue
		}
		seen[c.Rel] = true
		if kb.Status == "trashed" || kb.Status == "processing" {
			continue
		}
		if kb.Status == "quarantined" && l.reasonIsMissing(ctx, kb.ID) {
			if err := store.SetBookStatus(ctx, l.DB, kb.ID, "ready", ""); err == nil {
				_ = store.RecordBookChange(ctx, l.DB, kb.ID)
				res.Restored++
			}
		}
		if changed(kb, c) && now.Sub(c.Newest) >= SettleTime {
			if err := l.reimport(ctx, kb.ID, c); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", c.Rel, err))
				continue
			}
			res.Updated++
		}
	}
	for folder, kb := range known {
		if seen[folder] || kb.Status != "ready" {
			continue
		}
		if Exists(l.Root, folder) {
			continue // present but no audio detected right now; leave it alone
		}
		if err := store.SetBookStatus(ctx, l.DB, kb.ID, "quarantined", reasonMissing); err != nil {
			return res, err
		}
		_ = store.RecordBookChange(ctx, l.DB, kb.ID)
		_ = store.Audit(ctx, l.DB, store.AuditEntry{Action: "book.files_missing", ObjectType: "book", ObjectID: kb.ID,
			Details: map[string]any{"folder": folder}})
		res.Missing++
	}
	return res, nil
}

func knownAncestor(known map[string]*store.BookFolder, rel string) *store.BookFolder {
	for p := path.Dir(rel); p != "." && p != "/"; p = path.Dir(p) {
		if kb := known[p]; kb != nil {
			return kb
		}
	}
	return nil
}

func (l *Library) reasonIsMissing(ctx context.Context, id string) bool {
	b, err := store.GetBook(ctx, l.DB, id)
	return err == nil && b.StatusReason == reasonMissing
}

func changed(kb *store.BookFolder, c Candidate) bool {
	if len(kb.FileSizes) != len(c.Files) {
		return true
	}
	for _, f := range c.Files {
		inBook := strings.TrimPrefix(f, strings.TrimSuffix(c.Rel, "/")+"/")
		if f == c.Rel {
			inBook = path.Base(f)
		}
		if _, ok := kb.FileSizes[inBook]; !ok {
			return true
		}
	}
	return false
}

// importNew adds a scanned book to the master store (admin-owned, not
// visible to anyone until distributed).
func (l *Library) importNew(ctx context.Context, c Candidate) error {
	a := l.Analyze(ctx, l.Root, c)
	if a.Problem != nil {
		return a.Problem
	}
	a.Book.Origin, a.Book.Status = "scan", "ready"
	var id string
	err := l.DB.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if id, err = store.InsertBook(ctx, tx, &a.Book); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{Action: "book.scan_import", ObjectType: "book", ObjectID: id,
			Details: map[string]any{"folder": c.Rel}})
	})
	if errors.Is(err, store.ErrConflict) {
		return nil // imported concurrently
	}
	if err != nil {
		return err
	}
	l.storeCover(ctx, id, a)
	l.queueMatch(ctx, id)
	return nil
}

// reimport refreshes files/chapters/totals of a changed book, keeping its metadata.
func (l *Library) reimport(ctx context.Context, id string, c Candidate) error {
	a := l.Analyze(ctx, l.Root, c)
	if a.Problem != nil {
		return a.Problem
	}
	return l.DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.ReplaceBookMedia(ctx, tx, id, a.Book.Files, a.Book.Chapters); err != nil {
			return err
		}
		if err := store.UpdateBookTotals(ctx, tx, id, a.Book.DurationMS, a.Book.SizeBytes, a.Book.Checksum); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{Action: "book.rescan_update", ObjectType: "book", ObjectID: id})
	})
}
