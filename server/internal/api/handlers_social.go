// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Social features and moderation (brief §6.5 rules 5–6, §6.16, §6.19 "Social").
// Permission decisions come from authz; handlers only gather the facts.

const (
	maxReviewText    = 500
	maxReportDetails = 1000
	maxRecommendNote = 300
	maxOpenReports   = 50 // per reporter, so one account can't flood the inbox
	maxRecommendRecv = 20 // recipients per request
	maxListeners     = 50 // people shown in "who's listening"
	maxRecommended   = 50 // recommendations shown to a user
	listenerWindow   = 30 * 24 * time.Hour
)

var reportReasons = []ReportReason{"copyright", "inappropriate", "spam", "harassment", "wrong_metadata", "other"}

func nameRef(u *store.UserName) *UserRef {
	if u == nil {
		return nil
	}
	return &UserRef{Id: mustUUID(u.ID), Username: u.Username, DisplayName: u.DisplayName}
}

// cleanText trims s and checks it is at most max characters.
func cleanText(s *string, max int, field string) (string, error) {
	v := strings.TrimSpace(deref(s))
	if utf8.RuneCountInString(v) > max {
		return "", badRequest("too_long", field+" is too long")
	}
	return v, nil
}

func (s *Server) queuePush(ctx context.Context, to push.Recipients, ev push.Event) {
	app := s.App()
	if err := push.Enqueue(ctx, app.DB, to, ev); err != nil && !errors.Is(err, context.Canceled) {
		s.log().WarnContext(ctx, "queue push", "err", err)
		return
	}
	app.Jobs.Wake()
}

// ── ratings and reviews ──

func reviewOut(r *store.Review, p *auth.Principal) Review {
	out := Review{Id: mustUUID(r.ID), BookId: mustUUID(r.BookID), Author: nameRef(r.Author), Stars: r.Stars, Text: strPtr(r.Text),
		Mine: r.Author != nil && r.Author.ID == p.User.ID, Hidden: r.Hidden(), CreatedAt: msTime(r.CreatedAt), UpdatedAt: msTime(r.UpdatedAt)}
	if p.Can(authz.Admin) {
		out.HiddenReason = strPtr(r.HiddenReason)
	}
	return out
}

func ratingSummary(c [5]int64) RatingSummary {
	out := RatingSummary{Histogram: make([]int, 5)}
	var sum int64
	for i, n := range c {
		out.Histogram[i] = int(n)
		out.Count += int(n)
		sum += n * int64(i+1)
	}
	if out.Count > 0 {
		out.Average = float32(sum) / float32(out.Count)
	}
	return out
}

func (s *Server) ListReviews(ctx context.Context, req ListReviewsRequestObject) (ListReviewsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	if _, err := s.visibleOrAdmin(ctx, p, bookID); err != nil {
		return nil, err
	}
	offset, err := decodeCursor(req.Params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(req.Params.Limit, 50)
	db := s.App().DB
	items, err := store.ListReviews(ctx, db, bookID, p.User.ID, authz.SeeHiddenReviews(p.Subject, false), limit+1, offset)
	if err != nil {
		return nil, err
	}
	page := ReviewPage{Items: make([]Review, 0, len(items))}
	if len(items) > limit {
		items = items[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
	for _, r := range items {
		page.Items = append(page.Items, reviewOut(r, p))
	}
	counts, err := store.RatingCounts(ctx, db, bookID)
	if err != nil {
		return nil, err
	}
	page.Summary = ratingSummary(counts)
	switch mine, err := store.MyReview(ctx, db, p.User.ID, bookID); {
	case err == nil:
		m := reviewOut(mine, p)
		page.Mine = &m
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	return ListReviews200JSONResponse(page), nil
}

func (s *Server) PutMyReview(ctx context.Context, req PutMyReviewRequestObject) (PutMyReviewResponseObject, error) {
	p, err := requireCan(ctx, authz.Review)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	if _, err := s.visibleOrAdmin(ctx, p, bookID); err != nil {
		return nil, err
	}
	if req.Body.Stars < 1 || req.Body.Stars > 5 {
		return nil, badRequest("invalid_stars", "stars must be 1 to 5")
	}
	text, err := cleanText(req.Body.Text, maxReviewText, "the review")
	if err != nil {
		return nil, err
	}
	var out *store.Review
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		r, created, err := store.UpsertReview(ctx, tx, p.User.ID, bookID, req.Body.Stars, text)
		if err != nil {
			return err
		}
		out = r
		action := "review.update"
		if created {
			action = "review.create"
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: action, ObjectType: "review", ObjectID: r.ID,
			IP: clientIP(ctx), Details: map[string]any{"book_id": bookID, "stars": req.Body.Stars}})
	})
	if err != nil {
		return nil, err
	}
	return PutMyReview200JSONResponse(reviewOut(out, p)), nil
}

func (s *Server) DeleteMyReview(ctx context.Context, req DeleteMyReviewRequestObject) (DeleteMyReviewResponseObject, error) {
	// Deleting your own words is always allowed, even after losing can_review.
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		r, err := store.MyReview(ctx, tx, p.User.ID, bookID)
		if err != nil {
			return err
		}
		if err := store.DeleteReview(ctx, tx, r.ID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "review.delete", ObjectType: "review", ObjectID: r.ID,
			IP: clientIP(ctx), Details: map[string]any{"book_id": bookID}})
	})
	if err != nil {
		return nil, err
	}
	return DeleteMyReview204Response{}, nil
}

func (s *Server) HideReview(ctx context.Context, req HideReviewRequestObject) (HideReviewResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(deref(req.Body.Reason))
	if reason == "" {
		return nil, badRequest("reason_required", "hiding a review needs a reason")
	}
	id := req.ReviewId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetReviewHidden(ctx, tx, id, admin, reason); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "review.hide", ObjectType: "review", ObjectID: id,
			IP: clientIP(ctx), Details: map[string]any{"reason": reason}})
	})
	if err != nil {
		return nil, err
	}
	return HideReview204Response{}, nil
}

func (s *Server) UnhideReview(ctx context.Context, req UnhideReviewRequestObject) (UnhideReviewResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.ReviewId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.SetReviewHidden(ctx, tx, id, "", ""); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "review.unhide", ObjectType: "review", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return UnhideReview204Response{}, nil
}

// ── recommendations ──

func (s *Server) RecommendBook(ctx context.Context, req RecommendBookRequestObject) (RecommendBookResponseObject, error) {
	p, err := requireCan(ctx, authz.Recommend)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	if _, err := s.visibleOrAdmin(ctx, p, bookID); err != nil {
		return nil, err
	}
	note, err := cleanText(req.Body.Note, maxRecommendNote, "the note")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, u := range req.Body.UserIds {
		if id := u.String(); id != p.User.ID && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > maxRecommendRecv {
		return nil, badRequest("invalid_recipients", "pick 1 to 20 other people")
	}
	db := s.App().DB
	var sent []string
	err = db.InTx(ctx, func(tx *store.Tx) error {
		for _, to := range ids {
			// Only people you could find (they share a group with you) who
			// can already see the book; a recommendation never grants access.
			ok, err := store.SharesGroup(ctx, tx, p.User.ID, to)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if _, err := store.VisibleBook(ctx, tx, to, bookID); errors.Is(err, store.ErrNotFound) {
				continue
			} else if err != nil {
				return err
			}
			if err := store.UpsertRecommendation(ctx, tx, p.User.ID, to, bookID, note); err != nil {
				return err
			}
			sent = append(sent, to)
		}
		if len(sent) == 0 {
			return nil
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "book.recommend", ObjectType: "book", ObjectID: bookID,
			IP: clientIP(ctx), Details: map[string]any{"recipients": len(sent)}})
	})
	if err != nil {
		return nil, err
	}
	if len(sent) > 0 {
		s.queuePush(ctx, push.Recipients{Users: sent}, push.Event{Type: push.LibraryAdded, ID: bookID})
	}
	return RecommendBook200JSONResponse{Sent: len(sent), Skipped: len(ids) - len(sent)}, nil
}

func (s *Server) ListMyRecommendations(ctx context.Context, _ ListMyRecommendationsRequestObject) (ListMyRecommendationsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	recs, err := store.ListRecommendationsFor(ctx, db, p.User.ID, maxRecommended)
	if err != nil {
		return nil, err
	}
	var (
		keep  []*store.Recommendation
		items []*store.LibraryItem
	)
	for _, r := range recs {
		it, err := store.VisibleBook(ctx, db, p.User.ID, r.BookID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		if it.RemovedAt != nil {
			continue
		}
		keep, items = append(keep, r), append(items, it)
	}
	sums, err := s.summaries(ctx, items)
	if err != nil {
		return nil, err
	}
	out := make(ListMyRecommendations200JSONResponse, 0, len(keep))
	for i, r := range keep {
		from := r.From
		out = append(out, Recommendation{Id: mustUUID(r.ID), Book: sums[i], From: *nameRef(&from), Note: strPtr(r.Note), CreatedAt: msTime(r.CreatedAt)})
	}
	return out, nil
}

func (s *Server) DismissRecommendation(ctx context.Context, req DismissRecommendationRequestObject) (DismissRecommendationResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	if err := store.DeleteRecommendation(ctx, s.App().DB, req.RecommendationId.String(), p.User.ID); err != nil {
		return nil, err
	}
	return DismissRecommendation204Response{}, nil
}

// ── who's listening ──

func (s *Server) ListListeners(ctx context.Context, req ListListenersRequestObject) (ListListenersResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	if _, err := s.visibleOrAdmin(ctx, p, bookID); err != nil {
		return nil, err
	}
	since := time.Now().Add(-listenerWindow).UnixMilli()
	ls, err := store.ListListeners(ctx, s.App().DB, bookID, p.User.ID, since, maxListeners)
	if err != nil {
		return nil, err
	}
	out := make(ListListeners200JSONResponse, 0, len(ls))
	for _, l := range ls {
		state := ListenerState("listening")
		if l.Finished {
			state = "finished"
		}
		u := l.User
		out = append(out, Listener{User: *nameRef(&u), State: state, UpdatedAt: msTime(l.UpdatedAt)})
	}
	return out, nil
}

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
	page := ReportPage{Items: make([]AdminReport, 0, len(reps))}
	if len(reps) > limit {
		reps = reps[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
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

// ── people and groups ──

func (s *Server) SearchPeople(ctx context.Context, req SearchPeopleRequestObject) (SearchPeopleResponseObject, error) {
	p, err := requireCan(ctx, authz.Recommend)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(req.Params.Q)
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		return nil, badRequest("invalid_query", "type at least 2 characters")
	}
	people, err := store.SearchPeople(ctx, s.App().DB, p.User.ID, q, 10)
	if err != nil {
		return nil, err
	}
	out := make(SearchPeople200JSONResponse, 0, len(people))
	for _, u := range people {
		out = append(out, *nameRef(u))
	}
	return out, nil
}

func (s *Server) ListMyGroups(ctx context.Context, _ ListMyGroupsRequestObject) (ListMyGroupsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	gs, err := store.GroupsOf(ctx, s.App().DB, p.User.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListMyGroups200JSONResponse, 0, len(gs))
	for _, g := range gs {
		out = append(out, GroupRef{Id: mustUUID(g.ID), Name: g.Name, Public: g.Builtin})
	}
	return out, nil
}

// ── sharing books (brief §6.5 rules 5–6) ──

// shareTarget classifies a share target for authz and returns its name.
func (s *Server) shareTarget(ctx context.Context, q store.Querier, p *auth.Principal, targetType, targetID string) (authz.ShareTarget, string, error) {
	switch targetType {
	case "user":
		if targetID == p.User.ID {
			return 0, "", badRequest("self_share", "you can't share with yourself")
		}
		ok, err := store.SharesGroup(ctx, q, p.User.ID, targetID)
		if err != nil {
			return 0, "", err
		}
		if !ok && !p.Can(authz.Admin) {
			return 0, "", badRequest("unknown_person", "that person isn't in any of your groups")
		}
		u, err := store.GetUser(ctx, q, targetID)
		if err != nil {
			return 0, "", err
		}
		return authz.TargetUser, u.DisplayName, nil
	case "group":
		g, err := store.GetGroup(ctx, q, targetID)
		if err != nil {
			return 0, "", err
		}
		if g.Builtin {
			return authz.TargetPublic, g.Name, nil
		}
		mine, err := store.UserGroupIDs(ctx, q, p.User.ID)
		if err != nil {
			return 0, "", err
		}
		if slices.Contains(mine, targetID) {
			return authz.TargetMemberGroup, g.Name, nil
		}
		return authz.TargetOtherGroup, g.Name, nil
	}
	return 0, "", badRequest("invalid_target", "target_type must be group or user")
}

func (s *Server) ListBookShares(ctx context.Context, req ListBookSharesRequestObject) (ListBookSharesResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID := req.BookId.String()
	db := s.App().DB
	b, err := store.GetBook(ctx, db, bookID)
	if err != nil {
		return nil, err
	}
	if b.UploaderID != p.User.ID {
		return nil, errForbidden
	}
	ds, err := store.ListDistributions(ctx, db, bookID)
	if err != nil {
		return nil, err
	}
	out := BookShares{Shares: []Share{}, Requests: []ShareRequest{}}
	for _, d := range ds {
		if d.DistributedBy == p.User.ID {
			out.Shares = append(out.Shares, Share{Id: mustUUID(d.ID), TargetType: ShareTargetType(d.TargetType), TargetId: uuidOrNil(d.TargetID),
				TargetName: d.TargetName, CreatedAt: msTime(d.CreatedAt)})
		}
	}
	reqs, err := store.ShareRequestsForBook(ctx, db, bookID, p.User.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		out.Requests = append(out.Requests, ShareRequest{Id: mustUUID(r.ID), TargetId: mustUUID(r.GroupID), TargetName: r.GroupName,
			Status: ShareRequestStatus(r.Status), Reason: strPtr(r.Reason), CreatedAt: msTime(r.CreatedAt), DecidedAt: msTimePtr(r.DecidedAt)})
	}
	return ListBookShares200JSONResponse(out), nil
}

func (s *Server) ShareBook(ctx context.Context, req ShareBookRequestObject) (ShareBookResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID, targetType, targetID := req.BookId.String(), string(req.Body.TargetType), req.Body.TargetId.String()
	db := s.App().DB
	var (
		result ShareResult
		direct bool
	)
	err = db.InTx(ctx, func(tx *store.Tx) error {
		it, err := store.VisibleBook(ctx, tx, p.User.ID, bookID)
		if err != nil {
			return err
		}
		target, name, err := s.shareTarget(ctx, tx, p, targetType, targetID)
		if err != nil {
			return err
		}
		switch authz.BookShare(p.Subject, it.Book.UploaderID == p.User.ID, target) {
		case authz.ShareDirect:
			id, err := store.CreateDistribution(ctx, tx, bookID, targetType, targetID, p.User.ID, "")
			if err != nil {
				return err
			}
			if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
				return err
			}
			result, direct = ShareResult{Id: mustUUID(id), Status: "shared"}, true
			return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "share.create", ObjectType: "book", ObjectID: bookID,
				IP: clientIP(ctx), Details: map[string]any{"target_type": targetType, "target_id": targetID, "target": name}})
		case authz.ShareNeedsReview:
			ds, err := store.ListDistributions(ctx, tx, bookID)
			if err != nil {
				return err
			}
			for _, d := range ds {
				if d.TargetType == "group" && d.TargetID == targetID {
					return apiErr(http.StatusConflict, "already_shared", "this book is already shared with that group")
				}
			}
			id, err := store.RequestShare(ctx, tx, bookID, p.User.ID, targetID)
			if err != nil {
				return err
			}
			result = ShareResult{Id: mustUUID(id), Status: "pending_review"}
			return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "share.request", ObjectType: "book", ObjectID: bookID,
				IP: clientIP(ctx), Details: map[string]any{"group_id": targetID, "request_id": id}})
		}
		return errForbidden
	})
	if err != nil {
		return nil, err
	}
	if direct {
		s.notifyDistribution(ctx, bookID, targetType, targetID)
	} else {
		s.queuePush(ctx, push.Recipients{Admins: true}, push.Event{Type: push.ReviewRequest, ID: result.Id.String()})
	}
	return ShareBook201JSONResponse(result), nil
}

func (s *Server) UnshareBook(ctx context.Context, req UnshareBookRequestObject) (UnshareBookResponseObject, error) {
	// Taking a share back is always allowed, even after losing can_share.
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	bookID, shareID := req.BookId.String(), req.ShareId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		d, err := store.GetDistribution(ctx, tx, shareID)
		if err == nil && d.BookID == bookID && d.DistributedBy == p.User.ID {
			if _, err := store.RevokeDistribution(ctx, tx, shareID); err != nil {
				return err
			}
			if err := store.RecordBookChange(ctx, tx, bookID); err != nil {
				return err
			}
			return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "share.revoke", ObjectType: "book", ObjectID: bookID,
				IP: clientIP(ctx), Details: map[string]any{"distribution_id": shareID}})
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.WithdrawShareRequest(ctx, tx, shareID, bookID, p.User.ID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "share.withdraw", ObjectType: "book", ObjectID: bookID,
			IP: clientIP(ctx), Details: map[string]any{"request_id": shareID}})
	})
	if err != nil {
		return nil, err
	}
	return UnshareBook204Response{}, nil
}

// ── sharing collections ──

func collectionShareOut(c *store.CollectionShare) CollectionShare {
	return CollectionShare{Id: mustUUID(c.ID), TargetType: CollectionShareTargetType(c.TargetType), TargetId: uuidOrNil(c.TargetID),
		TargetName: c.TargetName, CreatedAt: msTime(c.CreatedAt)}
}

// ownCollection returns the user's live collection.
func ownCollection(ctx context.Context, q store.Querier, userID, id string) (*store.Collection, error) {
	c, err := store.GetCollection(ctx, q, userID, id)
	if err != nil {
		return nil, err
	}
	if c.DeletedAt != nil {
		return nil, store.ErrNotFound
	}
	return c, nil
}

func (s *Server) ListCollectionShares(ctx context.Context, req ListCollectionSharesRequestObject) (ListCollectionSharesResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	id := req.CollectionId.String()
	if _, err := ownCollection(ctx, db, p.User.ID, id); err != nil {
		return nil, err
	}
	shares, err := store.ListCollectionShares(ctx, db, id)
	if err != nil {
		return nil, err
	}
	out := make(ListCollectionShares200JSONResponse, 0, len(shares))
	for _, c := range shares {
		out = append(out, collectionShareOut(c))
	}
	return out, nil
}

func (s *Server) ShareCollection(ctx context.Context, req ShareCollectionRequestObject) (ShareCollectionResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id, targetType, targetID := req.CollectionId.String(), string(req.Body.TargetType), req.Body.TargetId.String()
	var out *store.CollectionShare
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if _, err := ownCollection(ctx, tx, p.User.ID, id); err != nil {
			return err
		}
		target, name, err := s.shareTarget(ctx, tx, p, targetType, targetID)
		if err != nil {
			return err
		}
		if !authz.CollectionShare(p.Subject, target) {
			return errForbidden
		}
		shareID, err := store.AddCollectionShare(ctx, tx, id, targetType, targetID)
		if err != nil {
			return err
		}
		shares, err := store.ListCollectionShares(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, c := range shares {
			if c.ID == shareID {
				out = c
			}
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "collection.share", ObjectType: "collection", ObjectID: id,
			IP: clientIP(ctx), Details: map[string]any{"target_type": targetType, "target_id": targetID, "target": name}})
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, store.ErrNotFound
	}
	return ShareCollection201JSONResponse(collectionShareOut(out)), nil
}

func (s *Server) UnshareCollection(ctx context.Context, req UnshareCollectionRequestObject) (UnshareCollectionResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	id, shareID := req.CollectionId.String(), req.ShareId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if _, err := ownCollection(ctx, tx, p.User.ID, id); err != nil {
			return err
		}
		if err := store.RemoveCollectionShare(ctx, tx, id, shareID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: p.User.ID, Action: "collection.unshare", ObjectType: "collection", ObjectID: id,
			IP: clientIP(ctx), Details: map[string]any{"share_id": shareID}})
	})
	if err != nil {
		return nil, err
	}
	return UnshareCollection204Response{}, nil
}

func (s *Server) ListSharedCollections(ctx context.Context, _ ListSharedCollectionsRequestObject) (ListSharedCollectionsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	cs, err := store.SharedCollectionsFor(ctx, s.App().DB, p.User.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListSharedCollections200JSONResponse, 0, len(cs))
	for _, c := range cs {
		ids := make([]openapi_types.UUID, 0, len(c.BookIDs))
		for _, b := range c.BookIDs {
			ids = append(ids, mustUUID(b))
		}
		owner := c.Owner
		out = append(out, SharedCollection{Id: mustUUID(c.ID), Name: c.Name, Description: strPtr(c.Description), Owner: *nameRef(&owner),
			BookIds: ids, UpdatedAt: msTime(c.UpdatedAt)})
	}
	return out, nil
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
	page := ReviewQueuePage{Items: make([]ReviewQueueItem, 0, len(items))}
	if len(items) > limit {
		items = items[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
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
