// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/store"
)

// autoShare shares a book just found in the library folder with the group
// the admin chose in Admin → Settings → Library (ADR-0053), inside the
// import's transaction. It is the admin's own standing distribution, so it
// is audited like one, without an actor. A group that has since been
// deleted shares with nobody rather than failing the import.
func autoShare(ctx context.Context, tx *store.Tx, log *slog.Logger, bookID string) error {
	lib, err := settings.Store{Q: tx}.Library(ctx)
	if err != nil || lib.AutoShareGroup == "" {
		return err
	}
	if _, err := store.GetGroup(ctx, tx, lib.AutoShareGroup); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			log.WarnContext(ctx, "auto-share group no longer exists; new book not shared", "book", bookID)
			return nil
		}
		return err
	}
	if _, err := store.CreateDistribution(ctx, tx, bookID, "group", lib.AutoShareGroup, "", ""); err != nil {
		return err
	}
	if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
		return err
	}
	if err := store.Audit(ctx, tx, store.AuditEntry{Action: "distribution.auto", ObjectType: "book", ObjectID: bookID,
		Details: map[string]any{"target_type": "group", "target_id": lib.AutoShareGroup}}); err != nil {
		return err
	}
	return push.Enqueue(ctx, tx, push.Recipients{Group: lib.AutoShareGroup}, push.Event{Type: push.LibraryAdded, ID: bookID})
}
