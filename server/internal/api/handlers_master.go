// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/store"
)

func (s *Server) masterOut(ctx context.Context, b *store.Book, withDist bool) (MasterBook, error) {
	db := s.App().DB
	authors, err := store.PeopleFor(ctx, db, "authors", []string{b.ID})
	if err != nil {
		return MasterBook{}, err
	}
	m := MasterBook{Id: mustUUID(b.ID), Title: b.Title, Authors: personRefs(authors[b.ID]), Series: seriesRef(b),
		Status: BookStatus(b.Status), StatusReason: strPtr(b.StatusReason), Origin: MasterBookOrigin(b.Origin),
		FolderPath: b.FolderPath, SizeBytes: b.SizeBytes, DurationMs: b.DurationMS, CreatedAt: msTime(b.CreatedAt),
		TrashedAt: msTimePtr(b.TrashedAt), Distributions: []Distribution{}}
	if b.UploaderID != "" {
		u := mustUUID(b.UploaderID)
		m.UploaderId = &u
	}
	if b.TrashedAt != nil {
		t := msTime(*b.TrashedAt).Add(library.TrashRetention)
		m.PurgeAt = &t
	}
	if t, err := store.ActiveTakedown(ctx, db, b.ID); err == nil {
		td := Takedown{Reason: t.Reason, TakenAt: msTime(t.TakenAt)}
		if t.TakenBy != "" {
			by := mustUUID(t.TakenBy)
			td.TakenBy = &by
		}
		m.Takedown = &td
	} else if !errors.Is(err, store.ErrNotFound) {
		return MasterBook{}, err
	}
	if withDist {
		ds, err := store.ListDistributions(ctx, db, b.ID)
		if err != nil {
			return MasterBook{}, err
		}
		for _, d := range ds {
			m.Distributions = append(m.Distributions, distOut(d))
		}
	}
	return m, nil
}

func distOut(d *store.Distribution) Distribution {
	return Distribution{Id: mustUUID(d.ID), BookId: mustUUID(d.BookID), TargetType: DistributionTargetType(d.TargetType),
		TargetId: uuidOrNil(d.TargetID), TargetName: d.TargetName, Note: strPtr(d.Note), CreatedAt: msTime(d.CreatedAt)}
}

func (s *Server) ListMasterBooks(ctx context.Context, req ListMasterBooksRequestObject) (ListMasterBooksResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	offset, err := decodeCursor(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(req.Params.Limit, 50)
	f := store.MasterFilter{Query: deref(req.Params.Q), Limit: limit + 1, Offset: offset}
	if req.Params.Status != nil {
		f.Status = string(*req.Params.Status)
	}
	books, err := store.ListMasterBooks(ctx, s.App().DB, f)
	if err != nil {
		return nil, err
	}
	page := MasterBookPage{Items: []MasterBook{}}
	if len(books) > limit {
		books = books[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
	for _, b := range books {
		m, err := s.masterOut(ctx, b, true)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, m)
	}
	return ListMasterBooks200JSONResponse(page), nil
}

func (s *Server) GetMasterBook(ctx context.Context, req GetMasterBookRequestObject) (GetMasterBookResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := store.GetBook(ctx, s.App().DB, req.BookId.String())
	if err != nil {
		return nil, err
	}
	m, err := s.masterOut(ctx, b, true)
	if err != nil {
		return nil, err
	}
	return GetMasterBook200JSONResponse(m), nil
}

func (s *Server) TrashBook(ctx context.Context, req TrashBookRequestObject) (TrashBookResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.App().Lib.Trash(ctx, admin, req.BookId.String(), clientIP(ctx)); err != nil {
		return nil, err
	}
	return TrashBook204Response{}, nil
}

func (s *Server) RestoreBook(ctx context.Context, req RestoreBookRequestObject) (RestoreBookResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	err = s.App().Lib.Restore(ctx, admin, req.BookId.String(), clientIP(ctx))
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "not_trashed", "this book is not in the Trash")
	}
	if err != nil {
		return nil, err
	}
	return RestoreBook204Response{}, nil
}

func (s *Server) PurgeBook(ctx context.Context, req PurgeBookRequestObject) (PurgeBookResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	err = s.App().Lib.Purge(ctx, admin, req.BookId.String(), clientIP(ctx))
	if errors.Is(err, library.ErrNotTrashed) {
		return nil, apiErr(http.StatusConflict, "not_trashed", "move the book to the Trash first")
	}
	if err != nil {
		return nil, err
	}
	return PurgeBook204Response{}, nil
}

func (s *Server) EmptyTrash(ctx context.Context, req EmptyTrashRequestObject) (EmptyTrashResponseObject, error) {
	p, err := requireCan(ctx, authz.Admin)
	if err != nil {
		return nil, err
	}
	app := s.App()
	// Re-authenticate: password and TOTP, because this cannot be undone.
	ok, err := auth.VerifyPassword(p.User.PasswordHash, req.Body.Password)
	if err != nil || !ok {
		return nil, auth.ErrInvalidCredentials
	}
	trashed, err := store.TrashedBefore(ctx, app.DB, time.Now().Add(time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	if req.Body.ConfirmCount != len(trashed) {
		return nil, badRequest("confirm_count_mismatch", "type the number of books currently in the Trash to confirm")
	}
	// TOTP last: a mistyped count must not use up the code.
	if ok, err := app.Auth.CheckTOTP(ctx, p.User, req.Body.TotpCode); err != nil || !ok {
		return nil, auth.ErrInvalidTOTP
	}
	n, err := app.Lib.EmptyTrash(ctx, p.User.ID, clientIP(ctx))
	if err != nil {
		return nil, err
	}
	return EmptyTrash200JSONResponse{Purged: n}, nil
}

func (s *Server) CreateDistribution(ctx context.Context, req CreateDistributionRequestObject) (CreateDistributionResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	bookID, targetID := b.BookId.String(), b.TargetId.String()
	db := s.App().DB
	var id string
	err = db.InTx(ctx, func(tx *store.Tx) error {
		book, err := store.GetBook(ctx, tx, bookID)
		if err != nil {
			return err
		}
		if book.Status == "trashed" {
			return badRequest("book_trashed", "restore the book from the Trash first")
		}
		switch b.TargetType {
		case DistributionCreateTargetTypeGroup:
			_, err = store.GetGroup(ctx, tx, targetID)
		case DistributionCreateTargetTypeUser:
			_, err = store.GetUser(ctx, tx, targetID)
		default:
			return badRequest("invalid_target", "target_type must be group or user")
		}
		if err != nil {
			return err
		}
		// Admins distribute directly, even to Public (brief §6.5 rule 5).
		if id, err = store.CreateDistribution(ctx, tx, bookID, string(b.TargetType), targetID, admin, deref(b.Note)); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "distribution.create", ObjectType: "book", ObjectID: bookID,
			IP: clientIP(ctx), Details: map[string]any{"target_type": string(b.TargetType), "target_id": targetID}})
	})
	if err != nil {
		return nil, err
	}
	s.notifyDistribution(ctx, bookID, string(b.TargetType), targetID)
	ds, err := store.ListDistributions(ctx, db, bookID)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.ID == id {
			return CreateDistribution201JSONResponse(distOut(d)), nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Server) RevokeDistribution(ctx context.Context, req RevokeDistributionRequestObject) (RevokeDistributionResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.DistributionId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		bookID, err := store.RevokeDistribution(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "distribution.revoke", ObjectType: "book", ObjectID: bookID,
			IP: clientIP(ctx), Details: map[string]any{"distribution_id": id}})
	})
	if err != nil {
		return nil, err
	}
	return RevokeDistribution204Response{}, nil
}

func (s *Server) ScanLibrary(ctx context.Context, _ ScanLibraryRequestObject) (ScanLibraryResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	app := s.App()
	id, _, err := jobs.EnqueueUnique(ctx, app.DB, library.JobScan, nil, time.Time{})
	if err != nil {
		return nil, err
	}
	app.Jobs.Wake()
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: admin, Action: "library.scan", ObjectType: "job", ObjectID: id, IP: clientIP(ctx)})
	return ScanLibrary202JSONResponse{JobId: mustUUID(id)}, nil
}

func (s *Server) GetJob(ctx context.Context, req GetJobRequestObject) (GetJobResponseObject, error) {
	p, err := requireUser(ctx, false)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	j, err := jobs.Get(ctx, db, req.JobId.String())
	if err != nil {
		return nil, err
	}
	if !p.Can(authz.Admin) {
		var by *string
		if err := db.QueryRow(ctx, `SELECT created_by FROM jobs WHERE id = ?`, j.ID).Scan(&by); err != nil || by == nil || *by != p.User.ID {
			return nil, store.ErrNotFound
		}
	}
	out := JobStatus{Id: mustUUID(j.ID), Type: j.Type, Status: JobStatusStatus(j.Status), Attempts: int(j.Attempts),
		LastError: strPtr(j.LastError), CreatedAt: msTime(j.CreatedAt), UpdatedAt: msTime(j.UpdatedAt)}
	if len(j.Result) > 0 {
		var m map[string]any
		if json.Unmarshal(j.Result, &m) == nil {
			out.Result = &m
		}
	}
	return GetJob200JSONResponse(out), nil
}

func (s *Server) ListMyUploads(ctx context.Context, req ListMyUploadsRequestObject) (ListMyUploadsResponseObject, error) {
	p, err := requireUser(ctx, false)
	if err != nil {
		return nil, err
	}
	app := s.App()
	rows, err := app.DB.Query(ctx, `SELECT id, filename, size_bytes, received, status, error, book_id, created_at, updated_at
		FROM uploads WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`, p.User.ID, limitOf(req.Params.Limit, 50))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := ListMyUploads200JSONResponse{}
	for rows.Next() {
		var (
			u            UploadItem
			status       string
			errMsg, book *string
			created, upd int64
		)
		if err := rows.Scan(&u.Id, &u.Filename, &u.SizeBytes, &u.ReceivedBytes, &status, &errMsg, &book, &created, &upd); err != nil {
			return nil, err
		}
		u.Status, u.Error, u.CreatedAt, u.UpdatedAt = UploadItemStatus(status), errMsg, msTime(created), msTime(upd)
		if book != nil {
			b := mustUUID(*book)
			u.BookId = &b
		}
		if status == "receiving" {
			u.ReceivedBytes = app.receivedBytes(u.Id)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
