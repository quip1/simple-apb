// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
	appsync "github.com/quip1/simple-apb/server/internal/sync"
)

func progressFromRow(r *store.ProgressRow) Progress {
	return Progress{BookId: mustUUID(r.BookID), PositionMs: r.PositionMS, DeviceId: uuidOrNil(r.DeviceID),
		DeviceName: strPtr(r.DeviceName), ClientTs: msTime(r.ClientTS), UpdatedAt: msTime(r.UpdatedAt),
		Speed: float32(r.Speed), FinishedAt: msTimePtr(r.FinishedAt)}
}

// ── Progress ──

func (s *Server) GetProgress(ctx context.Context, req GetProgressRequestObject) (GetProgressResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	if _, err := s.visibleOrAdmin(ctx, p, id); err != nil {
		return nil, err
	}
	row, err := store.GetProgress(ctx, s.App().DB, p.User.ID, id)
	if err != nil {
		return nil, err
	}
	return GetProgress200JSONResponse(progressFromRow(row)), nil
}

func (s *Server) PutProgress(ctx context.Context, req PutProgressRequestObject) (PutProgressResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	it, err := s.visibleOrAdmin(ctx, p, bookID)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b.Speed < 0.5 || b.Speed > 3.5 {
		return nil, badRequest("invalid_speed", "speed must be between 0.5 and 3.5")
	}
	if b.PositionMs < 0 {
		return nil, badRequest("invalid_position", "position must not be negative")
	}
	pos := min(b.PositionMs, it.Book.DurationMS)
	now := time.Now()
	in := appsync.Incoming{PositionMS: pos, DeviceID: b.DeviceId.String(), ClientTS: appsync.ClampClientTS(b.ClientTs, now)}
	if b.LastSyncedAt != nil {
		in.LastSyncedAt = *b.LastSyncedAt
	}
	threshold := time.Duration(p.User.ConflictThresholdMS) * time.Millisecond

	db := s.App().DB
	var (
		outcome    appsync.Outcome
		stored     *store.ProgressRow
		conflictID string
	)
	err = db.InTx(ctx, func(tx *store.Tx) error {
		var err error
		stored, err = store.GetProgress(ctx, tx, p.User.ID, bookID)
		if errors.Is(err, store.ErrNotFound) {
			stored = nil
		} else if err != nil {
			return err
		}
		if b.Resolution != nil && *b.Resolution != "" {
			// The user chose: this write wins, and the choice is recorded.
			if err := store.ResolveConflict(ctx, tx, *b.Resolution, p.User.ID, bookID, pos); err != nil {
				return badRequest("invalid_resolution", "unknown or already resolved conflict")
			}
			outcome = appsync.Apply
			if stored != nil && in.ClientTS.UnixMilli() <= stored.ClientTS {
				in.ClientTS = time.UnixMilli(stored.ClientTS + 1)
			}
		} else {
			var st *appsync.Stored
			if stored != nil {
				st = &appsync.Stored{PositionMS: stored.PositionMS, DeviceID: stored.DeviceID,
					ClientTS: time.UnixMilli(stored.ClientTS), ReceivedAt: time.UnixMilli(stored.UpdatedAt)}
			}
			outcome = appsync.Decide(st, in, threshold)
		}
		switch outcome {
		case appsync.Conflict:
			conflictID, err = store.CreateConflict(ctx, tx, p.User.ID, bookID, stored.PositionMS, stored.DeviceID, pos, in.DeviceID)
			return err
		case appsync.Ignore:
			return nil
		}
		w := store.ProgressWrite{UserID: p.User.ID, BookID: bookID, DeviceID: in.DeviceID, PositionMS: pos,
			ClientTS: in.ClientTS.UnixMilli(), Speed: float64(b.Speed), Finished: b.Finished}
		if err := store.UpsertProgress(ctx, tx, w); err != nil {
			return err
		}
		from, started := pos, w.ClientTS
		if stored != nil && stored.DeviceID == in.DeviceID {
			from, started = stored.PositionMS, stored.ClientTS
		}
		if err := store.AddProgressEvent(ctx, tx, p.User.ID, bookID, in.DeviceID, from, pos, started, w.ClientTS); err != nil {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityProgress, bookID, "upsert")
	})
	if err != nil {
		return nil, err
	}
	switch outcome {
	case appsync.Conflict:
		incoming := Progress{BookId: req.BookId, PositionMs: pos, DeviceId: b.DeviceId, ClientTs: in.ClientTS.UTC(),
			UpdatedAt: now.UTC(), Speed: b.Speed, DeviceName: strPtr(deviceName(ctx, s, p, in.DeviceID))}
		return PutProgress409JSONResponse(ProgressConflict{Code: ProgressConflictCodeProgressConflict, ConflictId: conflictID,
			Server: progressFromRow(stored), Incoming: incoming}), nil
	case appsync.Ignore:
		return PutProgress200JSONResponse(ProgressWriteResult{Applied: false, Progress: progressFromRow(stored)}), nil
	}
	row, err := store.GetProgress(ctx, db, p.User.ID, bookID)
	if err != nil {
		return nil, err
	}
	return PutProgress200JSONResponse(ProgressWriteResult{Applied: true, Progress: progressFromRow(row)}), nil
}

func deviceName(ctx context.Context, s *Server, p *auth.Principal, deviceID string) string {
	sessions, err := store.ListUserSessions(ctx, s.App().DB, p.User.ID)
	if err != nil {
		return ""
	}
	for _, sess := range sessions {
		if sess.DeviceID == deviceID {
			return sess.DeviceName
		}
	}
	return ""
}

// ── Delta sync ──

const syncCursorPrefix = "seq:"

func encodeSyncCursor(seq int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(syncCursorPrefix + strconv.FormatInt(seq, 10)))
}

func decodeSyncCursor(c string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || !strings.HasPrefix(string(b), syncCursorPrefix) {
		return 0, badRequest("invalid_cursor", "the sync cursor is not valid")
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(string(b), syncCursorPrefix), 10, 64)
	if err != nil || n < 0 {
		return 0, badRequest("invalid_cursor", "the sync cursor is not valid")
	}
	return n, nil
}

func bookmarkOut(b *store.Bookmark) Bookmark {
	return Bookmark{Id: mustUUID(b.ID), BookId: mustUUID(b.BookID), PositionMs: b.PositionMS, Note: strPtr(b.Note), UpdatedAt: msTime(b.UpdatedAt)}
}

func collectionOut(c *store.Collection) Collection {
	ids := make([]openapi_types.UUID, 0, len(c.BookIDs))
	for _, id := range c.BookIDs {
		ids = append(ids, mustUUID(id))
	}
	return Collection{Id: mustUUID(c.ID), Name: c.Name, Description: strPtr(c.Description), BookIds: ids, UpdatedAt: msTime(c.UpdatedAt)}
}

func (s *Server) GetSyncDelta(ctx context.Context, req GetSyncDeltaRequestObject) (GetSyncDeltaResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	out := SyncDelta{Library: []BookSummary{}, Progress: []Progress{}, Bookmarks: []Bookmark{}, Collections: []Collection{}, Tombstones: []Tombstone{}}
	if req.Params.Since == nil || *req.Params.Since == "" {
		return s.fullSnapshot(ctx, p, out)
	}
	since, err := decodeSyncCursor(*req.Params.Since)
	if err != nil {
		return nil, err
	}
	low, err := store.LowWater(ctx, db, s.App().Auth.Enc)
	if err != nil {
		return nil, err
	}
	if since < low {
		return GetSyncDelta410JSONResponse{Code: "cursor_expired", Message: "too old; sync again without a cursor"}, nil
	}
	limit := limitOf(req.Params.Limit, 500)
	changes, err := store.ChangesSince(ctx, db, p.User.ID, since, limit+1)
	if err != nil {
		return nil, err
	}
	if len(changes) > limit {
		changes, out.HasMore = changes[:limit], true
	}
	cursor := since
	type key struct{ entity, id string }
	seen := map[key]bool{}
	var visibleItems []*store.LibraryItem
	now := time.Now().UTC()
	tomb := func(e TombstoneEntity, id string) {
		out.Tombstones = append(out.Tombstones, Tombstone{Entity: e, Id: id, DeletedAt: now})
	}
	// Newest first so each object is reported once, in its latest state.
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		cursor = max(cursor, c.Seq)
		entity := c.Entity
		if entity == store.EntityBook {
			entity = store.EntityLibraryEntry // both mean "re-check this book for me"
		}
		k := key{entity, c.EntityID}
		if seen[k] {
			continue
		}
		seen[k] = true
		switch entity {
		case store.EntityLibraryEntry:
			if it, err := store.VisibleBook(ctx, db, p.User.ID, c.EntityID); err == nil {
				visibleItems = append(visibleItems, it)
			} else if errors.Is(err, store.ErrNotFound) {
				tomb(TombstoneEntityLibraryEntry, c.EntityID)
			} else {
				return nil, err
			}
		case store.EntityProgress:
			if row, err := store.GetProgress(ctx, db, p.User.ID, c.EntityID); err == nil {
				out.Progress = append(out.Progress, progressFromRow(row))
			} else if errors.Is(err, store.ErrNotFound) {
				tomb(TombstoneEntityProgress, c.EntityID)
			} else {
				return nil, err
			}
		case store.EntityBookmark:
			if bm, err := store.GetBookmark(ctx, db, p.User.ID, c.EntityID); err == nil {
				if bm.DeletedAt != nil {
					tomb(TombstoneEntityBookmark, bm.ID)
				} else {
					out.Bookmarks = append(out.Bookmarks, bookmarkOut(bm))
				}
			} else if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		case store.EntityCollection:
			if col, err := store.GetCollection(ctx, db, p.User.ID, c.EntityID); err == nil {
				if col.DeletedAt != nil {
					tomb(TombstoneEntityCollection, col.ID)
				} else {
					out.Collections = append(out.Collections, collectionOut(col))
				}
			} else if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
	}
	if out.Library, err = s.summaries(ctx, visibleItems); err != nil {
		return nil, err
	}
	out.Cursor = encodeSyncCursor(cursor)
	return GetSyncDelta200JSONResponse(out), nil
}

func (s *Server) fullSnapshot(ctx context.Context, p *auth.Principal, out SyncDelta) (GetSyncDeltaResponseObject, error) {
	db := s.App().DB
	// Read the cursor first: anything that changes while we gather is sent again next time.
	seq, err := store.MaxSeq(ctx, db)
	if err != nil {
		return nil, err
	}
	items, err := store.ListLibrary(ctx, db, p.User.ID, store.LibraryFilter{Shelf: "any", Limit: 1 << 30})
	if err != nil {
		return nil, err
	}
	if out.Library, err = s.summaries(ctx, items); err != nil {
		return nil, err
	}
	rows, err := store.ListProgress(ctx, db, p.User.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Progress = append(out.Progress, progressFromRow(r))
	}
	bms, err := store.ListBookmarks(ctx, db, p.User.ID, "")
	if err != nil {
		return nil, err
	}
	for _, b := range bms {
		out.Bookmarks = append(out.Bookmarks, bookmarkOut(b))
	}
	cols, err := store.ListCollections(ctx, db, p.User.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		out.Collections = append(out.Collections, collectionOut(c))
	}
	full := true
	out.Full, out.Cursor = &full, encodeSyncCursor(seq)
	return GetSyncDelta200JSONResponse(out), nil
}

// ── Bookmarks ──

func clockMS(ts *time.Time) int64 {
	now := time.Now()
	if ts == nil {
		return now.UnixMilli()
	}
	return appsync.ClampClientTS(*ts, now).UnixMilli()
}

func (s *Server) ListBookmarks(ctx context.Context, req ListBookmarksRequestObject) (ListBookmarksResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	book := ""
	if req.Params.BookId != nil {
		book = req.Params.BookId.String()
	}
	list, err := store.ListBookmarks(ctx, s.App().DB, p.User.ID, book)
	if err != nil {
		return nil, err
	}
	out := make(ListBookmarks200JSONResponse, 0, len(list))
	for _, b := range list {
		out = append(out, bookmarkOut(b))
	}
	return out, nil
}

func (s *Server) CreateBookmark(ctx context.Context, req CreateBookmarkRequestObject) (CreateBookmarkResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	b := req.Body
	it, err := s.visibleOrAdmin(ctx, p, b.BookId.String())
	if err != nil {
		return nil, err
	}
	bm := &store.Bookmark{BookID: it.Book.ID, PositionMS: max(0, min(b.PositionMs, it.Book.DurationMS)), Note: deref(b.Note), UpdatedAt: clockMS(b.ClientTs)}
	if b.Id != nil {
		bm.ID = b.Id.String()
	}
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		var created bool
		if bm, created, err = store.CreateBookmark(ctx, tx, p.User.ID, bm); err != nil {
			return err
		}
		if !created {
			return nil
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityBookmark, bm.ID, "upsert")
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "id_taken", "that id is already in use")
	}
	if err != nil {
		return nil, err
	}
	return CreateBookmark201JSONResponse(bookmarkOut(bm)), nil
}

func (s *Server) UpdateBookmark(ctx context.Context, req UpdateBookmarkRequestObject) (UpdateBookmarkResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookmarkId.String()
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		cur, err := store.GetBookmark(ctx, tx, p.User.ID, id)
		if err != nil || cur.DeletedAt != nil {
			return store.ErrNotFound
		}
		applied, err := store.UpdateBookmark(ctx, tx, p.User.ID, id, req.Body.PositionMs, req.Body.Note, clockMS(req.Body.ClientTs))
		if err != nil || !applied {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityBookmark, id, "upsert")
	})
	if err != nil {
		return nil, err
	}
	bm, err := store.GetBookmark(ctx, db, p.User.ID, id)
	if err != nil {
		return nil, err
	}
	return UpdateBookmark200JSONResponse(bookmarkOut(bm)), nil
}

func (s *Server) DeleteBookmark(ctx context.Context, req DeleteBookmarkRequestObject) (DeleteBookmarkResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.BookmarkId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.DeleteBookmark(ctx, tx, p.User.ID, id); err != nil {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityBookmark, id, "delete")
	})
	if err != nil {
		return nil, err
	}
	return DeleteBookmark204Response{}, nil
}

// ── Collections ──

func (s *Server) ListCollections(ctx context.Context, _ ListCollectionsRequestObject) (ListCollectionsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	list, err := store.ListCollections(ctx, s.App().DB, p.User.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListCollections200JSONResponse, 0, len(list))
	for _, c := range list {
		out = append(out, collectionOut(c))
	}
	return out, nil
}

func (s *Server) CreateCollection(ctx context.Context, req CreateCollectionRequestObject) (CreateCollectionResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Body.Name)
	if name == "" {
		return nil, badRequest("invalid_name", "collection name is required")
	}
	c := &store.Collection{Name: name, Description: deref(req.Body.Description), UpdatedAt: clockMS(req.Body.ClientTs)}
	if req.Body.Id != nil {
		c.ID = req.Body.Id.String()
	}
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		created, err := store.CreateCollection(ctx, tx, p.User.ID, c)
		if err != nil || !created {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityCollection, c.ID, "upsert")
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "id_taken", "that id is already in use")
	}
	if err != nil {
		return nil, err
	}
	got, err := store.GetCollection(ctx, db, p.User.ID, c.ID)
	if err != nil {
		return nil, err
	}
	return CreateCollection201JSONResponse(collectionOut(got)), nil
}

func (s *Server) UpdateCollection(ctx context.Context, req UpdateCollectionRequestObject) (UpdateCollectionResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.CollectionId.String()
	b := req.Body
	if b.Name != nil && strings.TrimSpace(*b.Name) == "" {
		return nil, badRequest("invalid_name", "collection name is required")
	}
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		cur, err := store.GetCollection(ctx, tx, p.User.ID, id)
		if err != nil || cur.DeletedAt != nil {
			return store.ErrNotFound
		}
		applied, err := store.UpdateCollection(ctx, tx, p.User.ID, id, b.Name, b.Description, clockMS(b.ClientTs))
		if err != nil || !applied {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityCollection, id, "upsert")
	})
	if err != nil {
		return nil, err
	}
	c, err := store.GetCollection(ctx, db, p.User.ID, id)
	if err != nil {
		return nil, err
	}
	return UpdateCollection200JSONResponse(collectionOut(c)), nil
}

func (s *Server) DeleteCollection(ctx context.Context, req DeleteCollectionRequestObject) (DeleteCollectionResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id := req.CollectionId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.DeleteCollection(ctx, tx, p.User.ID, id); err != nil {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityCollection, id, "delete")
	})
	if err != nil {
		return nil, err
	}
	return DeleteCollection204Response{}, nil
}

func (s *Server) setCollectionBook(ctx context.Context, collectionID, bookID string, add bool) error {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return err
	}
	if add {
		if _, err := s.visibleOrAdmin(ctx, p, bookID); err != nil {
			return err
		}
	}
	return s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		c, err := store.GetCollection(ctx, tx, p.User.ID, collectionID)
		if err != nil || c.DeletedAt != nil {
			return store.ErrNotFound
		}
		if err := store.SetCollectionBook(ctx, tx, collectionID, bookID, add); err != nil {
			return err
		}
		return store.RecordChange(ctx, tx, p.User.ID, store.EntityCollection, collectionID, "upsert")
	})
}

func (s *Server) AddToCollection(ctx context.Context, req AddToCollectionRequestObject) (AddToCollectionResponseObject, error) {
	if err := s.setCollectionBook(ctx, req.CollectionId.String(), req.BookId.String(), true); err != nil {
		return nil, err
	}
	return AddToCollection204Response{}, nil
}

func (s *Server) RemoveFromCollection(ctx context.Context, req RemoveFromCollectionRequestObject) (RemoveFromCollectionResponseObject, error) {
	if err := s.setCollectionBook(ctx, req.CollectionId.String(), req.BookId.String(), false); err != nil {
		return nil, err
	}
	return RemoveFromCollection204Response{}, nil
}
