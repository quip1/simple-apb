// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Reports and the review queue: the moderation side of social features.
// Permission decisions come from authz; handlers only gather the facts.

// ── reports ──

func (s *Server) CreateReport(ctx context.Context, req CreateReportRequestObject) (CreateReportResponseObject, error) {
	p, err := requireCan(ctx, authz.Report)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if !slices.Contains(reportReasons, b.Reason) {
		return nil, badRequest("invalid_reason", "unknown report reason")
	}
	details, err := cleanText(b.Details, maxReportDetails, "the details")
	if err != nil {
		return nil, err
	}
	objID := b.ObjectId.String()
	db := s.App().DB
	switch b.ObjectType {
	case "book":
		if _, err := s.visibleOrAdmin(ctx, p, objID); err != nil {
			return nil, err
		}
	case "review":
		r, err := store.GetReview(ctx, db, objID)
		if err != nil {
			return nil, err
		}
		if r.Author != nil && r.Author.ID == p.User.ID {
			return nil, badRequest("own_review", "you can't report your own review")
		}
		if _, err := s.visibleOrAdmin(ctx, p, r.BookID); err != nil {
			return nil, err
		}
	case "user":
		if objID == p.User.ID {
			return nil, badRequest("self_report", "you can't report yourself")
		}
		u, err := store.GetUser(ctx, db, objID)
		if err != nil {
			return nil, err
		}
		if u.Status == "deleted" {
			return nil, store.ErrNotFound
		}
	default:
		return nil, badRequest("invalid_object", "object_type must be book, review or user")
	}
	n, err := store.OpenReportsBy(ctx, db, p.User.ID)
	if err != nil {
		return nil, err
	}
	if n >= maxOpenReports {
		return nil, apiErr(http.StatusTooManyRequests, "too_many_reports", "you have many open reports; wait until the admins have looked at them")
	}
	var (
		id      string
		created bool
	)
	err = db.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if id, created, err = store.FileReport(ctx, tx, p.User.ID, string(b.ObjectType), objID, string(b.Reason), details); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "report.create", ObjectType: string(b.ObjectType),
			ObjectID: objID, IP: clientIP(ctx), Details: map[string]any{"report_id": id, "reason": string(b.Reason)}})
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.queuePush(ctx, push.Recipients{Admins: true}, push.Event{Type: push.ReportFiled, ID: id})
	}
	return CreateReport201JSONResponse{Id: mustUUID(id)}, nil
}

func (s *Server) ListReports(ctx context.Context, req ListReportsRequestObject) (ListReportsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	status := "open"
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	if !slices.Contains([]string{"open", "resolved", "dismissed"}, status) {
		return nil, badRequest("invalid_status", "unknown status")
	}
	offset, err := decodeCursor(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(req.Params.Limit, 50)
	db := s.App().DB
	reps, err := store.ListReports(ctx, db, status, limit+1, offset)
	if err != nil {
		return nil, err
	}
	reps, next := trimPage(reps, offset, limit)
	page := ReportPage{Items: make([]AdminReport, 0, len(reps)), NextCursor: next}
	for _, r := range reps {
		out, err := s.reportOut(ctx, r)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, out)
	}
	return ListReports200JSONResponse(page), nil
}

// reportOut describes a report and what it is about (the object may be gone).
func (s *Server) reportOut(ctx context.Context, r *store.Report) (AdminReport, error) {
	db := s.App().DB
	out := AdminReport{Id: mustUUID(r.ID), ObjectType: AdminReportObjectType(r.ObjectType), ObjectId: uuidOrNil(r.ObjectID),
		Reason: ReportReason(r.Reason), Details: strPtr(r.Details), Status: AdminReportStatus(r.Status),
		ResolutionNote: strPtr(r.ResolutionNote), Reporter: nameRef(r.Reporter), ResolvedBy: nameRef(r.ResolvedBy),
		CreatedAt: msTime(r.CreatedAt), ResolvedAt: msTimePtr(r.ResolvedAt)}
	if r.Resolution != "" {
		res := AdminReportResolution(r.Resolution)
		out.Resolution = &res
	}
	bookRef := func(id string) error {
		b, err := store.GetBook(ctx, db, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		out.Book = &BookRef{Id: mustUUID(b.ID), Title: b.Title, HasCover: b.CoverPath != ""}
		out.ObjectLabel = b.Title
		return nil
	}
	switch r.ObjectType {
	case "book":
		return out, bookRef(r.ObjectID)
	case "review":
		rv, err := store.GetReview(ctx, db, r.ObjectID)
		if errors.Is(err, store.ErrNotFound) {
			return out, nil
		} else if err != nil {
			return out, err
		}
		out.Review = &ReportedReview{Id: mustUUID(rv.ID), Stars: rv.Stars, Text: strPtr(rv.Text), Author: nameRef(rv.Author), Hidden: rv.Hidden()}
		return out, bookRef(rv.BookID)
	case "user":
		u, err := store.GetUser(ctx, db, r.ObjectID)
		if errors.Is(err, store.ErrNotFound) {
			return out, nil
		} else if err != nil {
			return out, err
		}
		out.User = &UserRef{Id: mustUUID(u.ID), Username: u.Username, DisplayName: u.DisplayName}
		out.ObjectLabel = u.Username
	}
	return out, nil
}

func (s *Server) ResolveReport(ctx context.Context, req ResolveReportRequestObject) (ResolveReportResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	note := strings.TrimSpace(req.Body.Note)
	if note == "" {
		return nil, badRequest("note_required", "closing a report needs a note")
	}
	if utf8.RuneCountInString(note) > maxReportDetails {
		return nil, badRequest("too_long", "the note is too long")
	}
	action := string(req.Body.Action)
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		r, err := store.GetReport(ctx, tx, req.ReportId.String())
		if err != nil {
			return err
		}
		if r.Status != "open" {
			return apiErr(http.StatusConflict, "already_closed", "this report is already closed")
		}
		status := "resolved"
		switch action {
		case "dismiss":
			status = "dismissed"
		case "resolved":
		case "hide_review":
			if r.ObjectType != "review" {
				return badRequest("wrong_action", "only a review can be hidden")
			}
			if err := store.SetReviewHidden(ctx, tx, r.ObjectID, admin, note); err != nil {
				return err
			}
			if err := store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "review.hide", ObjectType: "review",
				ObjectID: r.ObjectID, IP: clientIP(ctx), Details: map[string]any{"reason": note, "report_id": r.ID}}); err != nil {
				return err
			}
		case "takedown":
			bookID := r.ObjectID
			switch r.ObjectType {
			case "review":
				rv, err := store.GetReview(ctx, tx, r.ObjectID)
				if err != nil {
					return err
				}
				bookID = rv.BookID
			case "user":
				return badRequest("wrong_action", "a person can't be taken down; act on them from their user page")
			}
			if err := s.takeDown(ctx, tx, admin, bookID, note); err != nil {
				return err
			}
		default:
			return badRequest("invalid_action", "unknown action")
		}
		n, err := store.CloseReports(ctx, tx, r.ObjectType, r.ObjectID, status, action, note, admin)
		if err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "report.resolve", ObjectType: r.ObjectType,
			ObjectID: r.ObjectID, IP: clientIP(ctx), Details: map[string]any{"report_id": r.ID, "action": action, "closed": n, "note": note}})
	})
	if err != nil {
		return nil, err
	}
	return ResolveReport204Response{}, nil
}

// takeDown hides a book from everyone (no-op if it is already taken down).
func (s *Server) takeDown(ctx context.Context, tx *store.Tx, admin, bookID, reason string) error {
	if _, err := store.GetBook(ctx, tx, bookID); err != nil {
		return err
	}
	if _, err := store.ActiveTakedown(ctx, tx, bookID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := store.CreateTakedown(ctx, tx, bookID, reason, admin); err != nil {
		return err
	}
	if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
		return err
	}
	return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "book.takedown", ObjectType: "book", ObjectID: bookID,
		IP: clientIP(ctx), Details: map[string]any{"reason": reason}})
}

// ── review queue (admin) ──

func (s *Server) ListReviewQueue(ctx context.Context, req ListReviewQueueRequestObject) (ListReviewQueueResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	status := "pending"
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	if !slices.Contains([]string{"pending", "approved", "rejected"}, status) {
		return nil, badRequest("invalid_status", "unknown status")
	}
	offset, err := decodeCursor(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(req.Params.Limit, 50)
	items, err := store.ListShareRequests(ctx, s.App().DB, status, limit+1, offset)
	if err != nil {
		return nil, err
	}
	items, next := trimPage(items, offset, limit)
	page := ReviewQueuePage{Items: make([]ReviewQueueItem, 0, len(items)), NextCursor: next}
	for _, r := range items {
		page.Items = append(page.Items, ReviewQueueItem{Id: mustUUID(r.ID),
			Book:     BookRef{Id: mustUUID(r.BookID), Title: r.BookTitle, HasCover: r.BookHasCover},
			Target:   GroupRef{Id: mustUUID(r.GroupID), Name: r.GroupName, Public: r.GroupBuiltin},
			Uploader: nameRef(r.Uploader), DecidedBy: nameRef(r.DecidedBy), Status: ReviewQueueItemStatus(r.Status),
			Reason: strPtr(r.Reason), CreatedAt: msTime(r.CreatedAt), DecidedAt: msTimePtr(r.DecidedAt)})
	}
	return ListReviewQueue200JSONResponse(page), nil
}

func (s *Server) ApproveReviewItem(ctx context.Context, req ApproveReviewItemRequestObject) (ApproveReviewItemResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	var item *store.ShareRequest
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if item, err = store.GetShareRequest(ctx, tx, req.ItemId.String()); err != nil {
			return err
		}
		if item.Status != "pending" {
			return apiErr(http.StatusConflict, "already_decided", "this request was already decided")
		}
		b, err := store.GetBook(ctx, tx, item.BookID)
		if err != nil {
			return err
		}
		if b.Status == "trashed" {
			return badRequest("book_trashed", "the book is in the Trash")
		}
		if err := store.DecideShareRequest(ctx, tx, item.ID, "approved", "", admin); err != nil {
			return err
		}
		// The share stays the uploader's, so they can stop sharing it later.
		by := admin
		if item.Uploader != nil {
			by = item.Uploader.ID
		}
		if _, err := store.CreateDistribution(ctx, tx, item.BookID, "group", item.GroupID, by, ""); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, item.BookID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "review_queue.approve", ObjectType: "book", ObjectID: item.BookID,
			IP: clientIP(ctx), Details: map[string]any{"request_id": item.ID, "group_id": item.GroupID}})
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, apiErr(http.StatusConflict, "already_decided", "this request was already decided")
		}
		return nil, err
	}
	s.notifyDistribution(ctx, item.BookID, "group", item.GroupID)
	return ApproveReviewItem204Response{}, nil
}

func (s *Server) RejectReviewItem(ctx context.Context, req RejectReviewItemRequestObject) (RejectReviewItemResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(deref(req.Body.Reason))
	if reason == "" {
		return nil, badRequest("reason_required", "rejecting needs a reason (the uploader sees it)")
	}
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		item, err := store.GetShareRequest(ctx, tx, req.ItemId.String())
		if err != nil {
			return err
		}
		if err := store.DecideShareRequest(ctx, tx, item.ID, "rejected", reason, admin); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "review_queue.reject", ObjectType: "book", ObjectID: item.BookID,
			IP: clientIP(ctx), Details: map[string]any{"request_id": item.ID, "reason": reason}})
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "already_decided", "this request was already decided")
	}
	if err != nil {
		return nil, err
	}
	return RejectReviewItem204Response{}, nil
}
