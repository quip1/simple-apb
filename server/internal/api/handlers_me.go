// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/stats"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Account self-service (Phase 2 milestone 3): profile and preferences,
// password, 2FA management, sessions and listening stats.

const maxPrefsBytes = 8 << 10

var localeRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z]{2})?$`)

func validDisplayName(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 64 {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

func (s *Server) UpdateMe(ctx context.Context, req UpdateMeRequestObject) (UpdateMeResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	b := req.Body
	var patch store.ProfilePatch
	if b.DisplayName != nil {
		name := strings.TrimSpace(*b.DisplayName)
		if !validDisplayName(name) {
			return nil, badRequest("bad_display_name", "display name must be 1–64 characters")
		}
		patch.DisplayName = &name
	}
	if b.Email != nil {
		email := strings.TrimSpace(*b.Email)
		if email != "" {
			a, err := mail.ParseAddress(email)
			if err != nil || a.Address != email || len(email) > 254 {
				return nil, badRequest("bad_email", "that doesn't look like an email address")
			}
		}
		patch.Email = &email
	}
	if b.Locale != nil {
		if !localeRE.MatchString(*b.Locale) {
			return nil, badRequest("bad_locale", "unknown locale")
		}
		patch.Locale = b.Locale
	}
	if b.ConflictThresholdMinutes != nil {
		m := *b.ConflictThresholdMinutes
		if m < 1 || m > 30 {
			return nil, badRequest("bad_conflict_threshold", "the threshold must be 1–30 minutes")
		}
		ms := int64(m) * 60_000
		patch.ConflictThresholdMS = &ms
	}
	patch.ListeningVisible = b.ListeningVisible
	if b.Prefs != nil {
		raw, err := json.Marshal(*b.Prefs)
		if err != nil || len(raw) > maxPrefsBytes {
			return nil, badRequest("prefs_too_large", "preferences are limited to 8 KB")
		}
		str := string(raw)
		patch.Prefs = &str
	}
	db := s.App().DB
	if err := store.UpdateProfile(ctx, db, p.User.ID, patch); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, apiErr(http.StatusConflict, "email_taken", "that email is used by another account")
		}
		return nil, err
	}
	u, err := store.GetUser(ctx, db, p.User.ID)
	if err != nil {
		return nil, err
	}
	np := *p
	np.User = u
	me, err := s.meFor(ctx, &np)
	if err != nil {
		return nil, err
	}
	return UpdateMe200JSONResponse(*me), nil
}

func (s *Server) ChangeMyPassword(ctx context.Context, req ChangeMyPasswordRequestObject) (ChangeMyPasswordResponseObject, error) {
	p, err := requireUser(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := s.App().Auth.ChangePassword(ctx, p, req.Body.CurrentPassword, req.Body.NewPassword, clientIP(ctx)); err != nil {
		return nil, err
	}
	return ChangeMyPassword204Response{}, nil
}

func (s *Server) DisableMyTotp(ctx context.Context, req DisableMyTotpRequestObject) (DisableMyTotpResponseObject, error) {
	p, err := requireUser(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := s.App().Auth.DisableTOTP(ctx, p, req.Body.Password, req.Body.Code, clientIP(ctx)); err != nil {
		return nil, err
	}
	return DisableMyTotp204Response{}, nil
}

func (s *Server) RegenerateRecoveryCodes(ctx context.Context, req RegenerateRecoveryCodesRequestObject) (RegenerateRecoveryCodesResponseObject, error) {
	p, err := requireUser(ctx, false)
	if err != nil {
		return nil, err
	}
	codes, err := s.App().Auth.RegenerateRecoveryCodes(ctx, p, req.Body.Code, clientIP(ctx))
	if err != nil {
		return nil, err
	}
	return RegenerateRecoveryCodes200JSONResponse{Codes: codes}, nil
}

func (s *Server) RevokeMyOtherSessions(ctx context.Context, _ RevokeMyOtherSessionsRequestObject) (RevokeMyOtherSessionsResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	if err := store.RevokeOtherSessions(ctx, db, p.User.ID, p.SessionID); err != nil {
		return nil, err
	}
	_ = store.Audit(ctx, db, store.AuditEntry{ActorID: p.User.ID, Action: "session.revoke_others", ObjectType: "user",
		ObjectID: p.User.ID, IP: clientIP(ctx)})
	return RevokeMyOtherSessions204Response{}, nil
}

// ── stats (brief §6.17) ──

const (
	statsTopBooks   = 50
	statsTopAuthors = 20
)

func (s *Server) GetMyStats(ctx context.Context, req GetMyStatsRequestObject) (GetMyStatsResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	tz := "UTC"
	if req.Params.Tz != nil && *req.Params.Tz != "" {
		tz = *req.Params.Tz
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		return nil, badRequest("bad_timezone", "unknown time zone")
	}
	year := time.Now().In(loc).Year()
	if req.Params.Year != nil {
		year = *req.Params.Year
	}
	if year < 2000 || year > 2200 {
		return nil, badRequest("bad_year", "year out of range")
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, loc).UnixMilli()
	to := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc).UnixMilli()

	db := s.App().DB
	uid := p.User.ID
	events, err := store.ListenEvents(ctx, db, uid, from, to)
	if err != nil {
		return nil, err
	}
	sum := stats.Summarize(events, loc)
	finished, err := store.FinishedBetween(ctx, db, uid, from, to)
	if err != nil {
		return nil, err
	}

	out := MyStats{Year: year, Years: []int{}, TotalMs: sum.TotalMS, ActiveDays: len(sum.Days),
		LongestStreakDays: sum.LongestStreak, BooksFinished: len(finished),
		Days: make([]StatsDay, 0, len(sum.Days)), Books: []StatsBook{}, Authors: []StatsAuthor{}, Finished: []StatsBook{}}
	if lo, hi, err := store.ListenSpan(ctx, db, uid); err != nil {
		return nil, err
	} else if lo != nil && hi != nil {
		for y := time.UnixMilli(*hi).In(loc).Year(); y >= time.UnixMilli(*lo).In(loc).Year(); y-- {
			out.Years = append(out.Years, y)
		}
	}
	for _, d := range sum.Days {
		t, _ := time.Parse(time.DateOnly, d.Date)
		out.Days = append(out.Days, StatsDay{Date: openapiDate(t), ListenedMs: d.MS})
	}

	// Book details for the top list and the finished list.
	top := stats.Top(sum.PerBook, statsTopBooks)
	ids := make([]string, 0, len(top)+len(finished))
	for _, r := range top {
		ids = append(ids, r.ID)
	}
	for _, f := range finished {
		ids = append(ids, f.BookID)
	}
	authors, err := store.PeopleFor(ctx, db, "authors", ids)
	if err != nil {
		return nil, err
	}
	books := map[string]*store.Book{}
	for _, id := range ids {
		if _, ok := books[id]; ok {
			continue
		}
		b, err := store.GetBook(ctx, db, id)
		if errors.Is(err, store.ErrNotFound) {
			continue // purged since
		} else if err != nil {
			return nil, err
		}
		books[id] = b
	}
	statsBook := func(id string, ms int64) (StatsBook, bool) {
		b, ok := books[id]
		if !ok {
			return StatsBook{}, false
		}
		return StatsBook{BookId: mustUUID(id), Title: b.Title, Authors: personRefs(authors[id]),
			CoverUrl: coverURL(b), HasCover: b.CoverPath != "", ListenedMs: ms}, true
	}
	for _, r := range top {
		if sb, ok := statsBook(r.ID, r.MS); ok {
			out.Books = append(out.Books, sb)
		}
	}
	for _, f := range finished {
		if sb, ok := statsBook(f.BookID, sum.PerBook[f.BookID]); ok {
			at := msTime(f.FinishedAt)
			sb.FinishedAt = &at
			out.Finished = append(out.Finished, sb)
		}
	}

	// Authors get the full time of each of their books.
	perAuthor := map[string]int64{}
	names := map[string]string{}
	bookAuthors, err := store.PeopleFor(ctx, db, "authors", keys(sum.PerBook))
	if err != nil {
		return nil, err
	}
	for bookID, ms := range sum.PerBook {
		for _, a := range bookAuthors[bookID] {
			perAuthor[a.ID] += ms
			names[a.ID] = a.Name
		}
	}
	for _, r := range stats.Top(perAuthor, statsTopAuthors) {
		out.Authors = append(out.Authors, StatsAuthor{Id: mustUUID(r.ID), Name: names[r.ID], ListenedMs: r.MS})
	}
	return GetMyStats200JSONResponse(out), nil
}

func openapiDate(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

func keys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
