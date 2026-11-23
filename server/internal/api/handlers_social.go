// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	items, next := trimPage(items, offset, limit)
	page := ReviewPage{Items: make([]Review, 0, len(items)), NextCursor: next}
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
