// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/stats"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/stream"
	"github.com/quip1/simple-apb/server/internal/sysinfo"
	"github.com/quip1/simple-apb/server/internal/version"
)

// Admin panel (Phase 2 milestone 4, brief §6.23): dashboard, growth, users,
// user tags, groups and takedowns. Audit log and settings: handlers_settings.go.

// ── dashboard ──

func (s *Server) GetAdminDashboard(ctx context.Context, _ GetAdminDashboardRequestObject) (GetAdminDashboardResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	app := s.App()
	db := app.DB
	now := time.Now()
	snap := app.Shaper.Snapshot()
	out := AdminDashboard{Version: version.Version, StartedAt: app.StartedAt.UTC(), UptimeS: int64(now.Sub(app.StartedAt).Seconds()),
		Egress: DashboardEgress{CapBps: snap.RateBps * 8}, Live: []LiveStream{}}

	// Live streams with names.
	users, books := map[string]string{}, map[string]string{}
	for _, f := range snap.Flows {
		if _, ok := users[f.User]; !ok {
			if u, err := store.GetUser(ctx, db, f.User); err == nil {
				users[f.User] = u.Username
			}
		}
		if _, ok := books[f.BookID]; !ok {
			if b, err := store.GetBook(ctx, db, f.BookID); err == nil {
				books[f.BookID] = b.Title
			}
		}
		secs := max(now.Sub(f.Since).Seconds(), 1)
		rate := int64(float64(f.SentBytes) * 8 / secs)
		out.Egress.UsedBps += rate
		ls := LiveStream{UserId: mustUUID(f.User), Username: users[f.User], BookId: mustUUID(f.BookID), BookTitle: books[f.BookID],
			Kind: LiveStreamKind("download"), Priority: LiveStreamPriorityNormal, RateBps: rate, SentBytes: f.SentBytes, Since: f.Since.UTC()}
		if f.Live {
			ls.Kind = LiveStreamKind("live")
		}
		switch f.Class {
		case stream.ClassAdmin:
			ls.Priority = LiveStreamPriorityAdmin
		case stream.ClassPriority:
			ls.Priority = LiveStreamPriorityPriority
		}
		if f.BitrateKbps > 0 {
			br := f.BitrateKbps
			ls.BitrateKbps = &br
		}
		out.Live = append(out.Live, ls)
	}

	// Health.
	h := ServerHealth{Disks: []DiskUsage{}, Zfs: "unknown", ProcessMemoryBytes: int64(sysinfo.ProcessMemory())}
	cpus := runtime.NumCPU()
	h.Cpus = &cpus
	var err error
	if h.DbBytes, err = store.DBSize(ctx, db); err != nil {
		return nil, err
	}
	depth, err := store.JobQueueDepth(ctx, db)
	if err != nil {
		return nil, err
	}
	h.JobQueueDepth = int(depth)
	paths := app.Lib.Paths
	for _, v := range []struct {
		name DiskUsageName
		path string
	}{{"config", paths.Config}, {"library", app.Lib.Root}, {"cache", paths.Cache}, {"backups", paths.Backups}} {
		if v.path == "" {
			continue
		}
		if d, err := sysinfo.DiskUsage(v.path); err == nil {
			h.Disks = append(h.Disks, DiskUsage{Name: v.name, FreeBytes: int64(d.Free), TotalBytes: int64(d.Total)})
		}
	}
	if m := sysinfo.HostMemory(); m.OK {
		total, avail := int64(m.Total), int64(m.Available)
		h.MemoryTotalBytes, h.MemoryAvailableBytes = &total, &avail
	}
	if l, ok := sysinfo.LoadAverage(); ok {
		f := float32(l)
		h.LoadAverage = &f
	}
	out.Health = h
	out.LastBackup = opOut(app.getStatus(ctx, statusBackup))
	out.LastScrub = opOut(app.getStatus(ctx, statusScrub))
	c, err := store.CountInbox(ctx, db)
	if err != nil {
		return nil, err
	}
	out.Inbox = InboxCounts{SignupRequests: int(c.Signups), ReviewQueue: int(c.Reviews), Reports: int(c.Reports)}
	return GetAdminDashboard200JSONResponse(out), nil
}

// ── growth (brief §6.17: users, books, storage, listening hours over time) ──

func (s *Server) GetAdminGrowth(ctx context.Context, req GetAdminGrowthRequestObject) (GetAdminGrowthResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	months := 12
	if req.Params.Months != nil {
		months = *req.Params.Months
	}
	if months < 1 || months > 36 {
		return nil, badRequest("bad_months", "months must be 1–36")
	}
	tz := "UTC"
	if req.Params.Tz != nil && *req.Params.Tz != "" {
		tz = *req.Params.Tz
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "Local" {
		return nil, badRequest("bad_timezone", "unknown time zone")
	}
	db := s.App().DB
	now := time.Now().In(loc)
	first := time.Date(now.Year(), now.Month()-time.Month(months-1), 1, 0, 0, 0, 0, loc)
	events, err := store.AllListenEvents(ctx, db, first.UnixMilli(), now.UnixMilli()+1)
	if err != nil {
		return nil, err
	}
	perMonth := map[string]int64{}
	for _, e := range events {
		perMonth[time.UnixMilli(e.EndedAt).In(loc).Format("2006-01")] += stats.ListenedMS(e)
	}
	out := make(GetAdminGrowth200JSONResponse, 0, months)
	for i := range months {
		start := first.AddDate(0, i, 0)
		end := start.AddDate(0, 1, 0)
		if end.After(now) {
			end = now
		}
		users, books, storage, err := store.GrowthAt(ctx, db, end.UnixMilli())
		if err != nil {
			return nil, err
		}
		key := start.Format("2006-01")
		out = append(out, GrowthPoint{Month: key, Users: int(users), Books: int(books), StorageBytes: storage, ListeningMs: perMonth[key]})
	}
	return out, nil
}

// ── users ──

func overridesOut(p store.PermissionOverrides) PermissionOverridesInput {
	return PermissionOverridesInput{CanUpload: p.CanUpload, CanEditMetadata: p.CanEditMetadata, CanDeleteOwn: p.CanDeleteOwn,
		CanShare: p.CanShare, CanReview: p.CanReview, CanPostPublic: p.CanPostPublic}
}

func overridesIn(p PermissionOverridesInput) store.PermissionOverrides {
	return store.PermissionOverrides{CanUpload: p.CanUpload, CanEditMetadata: p.CanEditMetadata, CanDeleteOwn: p.CanDeleteOwn,
		CanShare: p.CanShare, CanReview: p.CanReview, CanPostPublic: p.CanPostPublic}
}

// overridesMap is the JSON form stored on user tags and permission defaults.
func overridesMap(p PermissionOverridesInput) map[string]bool {
	m := map[string]bool{}
	set := func(a authz.Action, v *bool) {
		if v != nil {
			m[string(a)] = *v
		}
	}
	set(authz.Upload, p.CanUpload)
	set(authz.EditMetadata, p.CanEditMetadata)
	set(authz.DeleteOwn, p.CanDeleteOwn)
	set(authz.Share, p.CanShare)
	set(authz.Review, p.CanReview)
	set(authz.PostPublic, p.CanPostPublic)
	return m
}

func overridesFromMap(m map[string]bool) PermissionOverridesInput {
	get := func(a authz.Action) *bool {
		if v, ok := m[string(a)]; ok {
			return &v
		}
		return nil
	}
	return PermissionOverridesInput{CanUpload: get(authz.Upload), CanEditMetadata: get(authz.EditMetadata),
		CanDeleteOwn: get(authz.DeleteOwn), CanShare: get(authz.Share), CanReview: get(authz.Review), CanPostPublic: get(authz.PostPublic)}
}

func effectiveOut(sub authz.Subject) EffectivePermissions {
	eff := authz.Effective(sub)
	return EffectivePermissions{CanUpload: eff[authz.Upload], CanEditMetadata: eff[authz.EditMetadata], CanDeleteOwn: eff[authz.DeleteOwn],
		CanShare: eff[authz.Share], CanReview: eff[authz.Review], CanPostPublic: eff[authz.PostPublic]}
}

func userTagOut(t *store.UserTag) UserTag {
	return UserTag{Id: mustUUID(t.ID), Name: t.Name, Color: strPtr(t.Color), RequireTotp: t.RequireTOTP,
		CapabilityOverrides: overridesFromMap(t.Overrides), Members: int(t.Members)}
}

func (s *Server) GetAdminUser(ctx context.Context, req GetAdminUserRequestObject) (GetAdminUserResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	app := s.App()
	db := app.DB
	id := req.UserId.String()
	u, err := store.GetUser(ctx, db, id)
	if err != nil {
		return nil, err
	}
	base := adminUserOut(u)
	d := AdminUserDetail{Id: base.Id, Username: base.Username, DisplayName: base.DisplayName, Email: base.Email, Role: base.Role,
		Status: base.Status, StatusReason: base.StatusReason, StatusUntil: base.StatusUntil, TotpEnabled: base.TotpEnabled,
		QuotaBytes: base.QuotaBytes, CreatedAt: base.CreatedAt, Notice: strPtr(u.Notice), Tags: []UserTag{}, Groups: []Group{}}
	if d.UsedBytes, err = store.UsedBytes(ctx, db, id); err != nil {
		return nil, err
	}
	if at, err := store.LastLogin(ctx, db, id); err != nil {
		return nil, err
	} else {
		d.LastLoginAt = msTimePtr(at)
	}
	tags, err := store.TagsOfUser(ctx, db, id)
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		d.Tags = append(d.Tags, userTagOut(t))
	}
	groups, err := store.GroupsOfUser(ctx, db, id)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		d.Groups = append(d.Groups, groupOut(g))
	}
	ov, err := store.GetPermissionOverrides(ctx, db, id)
	if err != nil {
		return nil, err
	}
	d.Permissions = overridesOut(ov)
	p, err := app.Auth.PrincipalFor(ctx, u)
	if err != nil {
		return nil, err
	}
	d.Effective = effectiveOut(p.Subject)
	return GetAdminUser200JSONResponse(d), nil
}

func (s *Server) SetUserPermissions(ctx context.Context, req SetUserPermissionsRequestObject) (SetUserPermissionsResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.UserId.String()
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := store.GetUser(ctx, tx, id); err != nil {
			return err
		}
		if err := store.SetPermissionOverrides(ctx, tx, id, overridesIn(*req.Body)); err != nil {
			return err
		}
		details := map[string]any{}
		for k, v := range overridesMap(*req.Body) {
			details[k] = v
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user.permissions", ObjectType: "user", ObjectID: id,
			IP: clientIP(ctx), Details: details})
	})
	if err != nil {
		return nil, err
	}
	return SetUserPermissions204Response{}, nil
}

func (s *Server) AddUserTag(ctx context.Context, req AddUserTagRequestObject) (AddUserTagResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	uid, tid := req.UserId.String(), req.TagId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if _, err := store.GetUser(ctx, tx, uid); err != nil {
			return err
		}
		if _, err := store.GetUserTag(ctx, tx, tid); err != nil {
			return err
		}
		if err := store.AssignUserTag(ctx, tx, uid, tid, admin); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user.tag_add", ObjectType: "user", ObjectID: uid,
			IP: clientIP(ctx), Details: map[string]any{"tag_id": tid}})
	})
	if err != nil {
		return nil, err
	}
	return AddUserTag204Response{}, nil
}

func (s *Server) RemoveUserTag(ctx context.Context, req RemoveUserTagRequestObject) (RemoveUserTagResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	uid, tid := req.UserId.String(), req.TagId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.UnassignUserTag(ctx, tx, uid, tid); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user.tag_remove", ObjectType: "user", ObjectID: uid,
			IP: clientIP(ctx), Details: map[string]any{"tag_id": tid}})
	})
	if err != nil {
		return nil, err
	}
	return RemoveUserTag204Response{}, nil
}

func (s *Server) ListUserSessions(ctx context.Context, req ListUserSessionsRequestObject) (ListUserSessionsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	db := s.App().DB
	id := req.UserId.String()
	if _, err := store.GetUser(ctx, db, id); err != nil {
		return nil, err
	}
	list, err := store.ListUserSessions(ctx, db, id)
	if err != nil {
		return nil, err
	}
	out := make(ListUserSessions200JSONResponse, 0, len(list))
	for _, sess := range list {
		out = append(out, sessionOut(sess, ""))
	}
	return out, nil
}

func (s *Server) CreatePasswordResetLink(ctx context.Context, req CreatePasswordResetLinkRequestObject) (CreatePasswordResetLinkResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	app := s.App()
	u, err := store.GetUser(ctx, app.DB, req.UserId.String())
	if err != nil {
		return nil, err
	}
	if u.Status == "pending" || u.Status == "deleted" || u.Status == "banned" {
		return nil, apiErr(http.StatusConflict, "user_not_active", "this account can't sign in")
	}
	token, exp, err := app.Auth.NewResetToken(u)
	if err != nil {
		return nil, err
	}
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: admin, Action: "user.reset_link", ObjectType: "user", ObjectID: u.ID, IP: clientIP(ctx)})
	return CreatePasswordResetLink200JSONResponse{Url: s.publicBase(ctx) + "/reset-password#" + token, ExpiresAt: exp.UTC()}, nil
}

func (s *Server) ResetPassword(ctx context.Context, req ResetPasswordRequestObject) (ResetPasswordResponseObject, error) {
	if err := s.App().Auth.ResetPassword(ctx, req.Body.Token, req.Body.NewPassword, clientIP(ctx)); err != nil {
		return nil, err
	}
	return ResetPassword204Response{}, nil
}

// ── user tags ──

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validTagName(n string) bool {
	c := utf8.RuneCountInString(n)
	return c >= 1 && c <= 50
}

func (s *Server) ListUserTags(ctx context.Context, _ ListUserTagsRequestObject) (ListUserTagsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	tags, err := store.ListUserTags(ctx, s.App().DB)
	if err != nil {
		return nil, err
	}
	out := make(ListUserTags200JSONResponse, 0, len(tags))
	for _, t := range tags {
		out = append(out, userTagOut(t))
	}
	return out, nil
}

func (s *Server) CreateUserTag(ctx context.Context, req CreateUserTagRequestObject) (CreateUserTagResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	name := strings.TrimSpace(b.Name)
	if !validTagName(name) {
		return nil, badRequest("bad_name", "the name must be 1–50 characters")
	}
	color := deref(b.Color)
	if color != "" && !colorRE.MatchString(color) {
		return nil, badRequest("bad_color", "colors look like #3366ff")
	}
	var ov map[string]bool
	if b.CapabilityOverrides != nil {
		ov = overridesMap(*b.CapabilityOverrides)
	}
	db := s.App().DB
	var id string
	err = db.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if id, err = store.CreateUserTag(ctx, tx, name, color, b.RequireTotp != nil && *b.RequireTotp, ov); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user_tag.create", ObjectType: "user_tag", ObjectID: id,
			IP: clientIP(ctx), Details: map[string]any{"name": name}})
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "name_taken", "a tag with that name exists")
	} else if err != nil {
		return nil, err
	}
	t, err := store.GetUserTag(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return CreateUserTag201JSONResponse(userTagOut(t)), nil
}

func (s *Server) UpdateUserTag(ctx context.Context, req UpdateUserTagRequestObject) (UpdateUserTagResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	var p store.UserTagPatch
	if b.Name != nil {
		name := strings.TrimSpace(*b.Name)
		if !validTagName(name) {
			return nil, badRequest("bad_name", "the name must be 1–50 characters")
		}
		p.Name = &name
	}
	if b.Color != nil {
		if *b.Color != "" && !colorRE.MatchString(*b.Color) {
			return nil, badRequest("bad_color", "colors look like #3366ff")
		}
		p.Color = b.Color
	}
	p.RequireTOTP = b.RequireTotp
	if b.CapabilityOverrides != nil {
		p.Overrides = overridesMap(*b.CapabilityOverrides)
	}
	id := req.TagId.String()
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		if err := store.UpdateUserTag(ctx, tx, id, p); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user_tag.update", ObjectType: "user_tag", ObjectID: id, IP: clientIP(ctx)})
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "name_taken", "a tag with that name exists")
	} else if err != nil {
		return nil, err
	}
	t, err := store.GetUserTag(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return UpdateUserTag200JSONResponse(userTagOut(t)), nil
}

func (s *Server) DeleteUserTag(ctx context.Context, req DeleteUserTagRequestObject) (DeleteUserTagResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.TagId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.DeleteUserTag(ctx, tx, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user_tag.delete", ObjectType: "user_tag", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return DeleteUserTag204Response{}, nil
}

// ── groups ──

func (s *Server) UpdateGroup(ctx context.Context, req UpdateGroupRequestObject) (UpdateGroupResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.GroupId.String()
	db := s.App().DB
	b := req.Body
	var name *string
	if b.Name != nil {
		n := strings.TrimSpace(*b.Name)
		if c := utf8.RuneCountInString(n); c < 1 || c > 100 {
			return nil, badRequest("bad_name", "the name must be 1–100 characters")
		}
		name = &n
	}
	err = db.InTx(ctx, func(tx *store.Tx) error {
		g, err := store.GetGroup(ctx, tx, id)
		if err != nil {
			return err
		}
		if g.Builtin && name != nil && *name != g.Name {
			return apiErr(http.StatusForbidden, "builtin_group", "the Public group can't be renamed")
		}
		if err := store.UpdateGroup(ctx, tx, id, name, b.HighPriority); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "group.update", ObjectType: "group", ObjectID: id, IP: clientIP(ctx)})
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(http.StatusConflict, "name_taken", "a group with that name exists")
	} else if err != nil {
		return nil, err
	}
	g, err := store.GetGroup(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return UpdateGroup200JSONResponse(groupOut(g)), nil
}

func (s *Server) ListGroupMembers(ctx context.Context, req ListGroupMembersRequestObject) (ListGroupMembersResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	db := s.App().DB
	id := req.GroupId.String()
	if _, err := store.GetGroup(ctx, db, id); err != nil {
		return nil, err
	}
	members, err := store.ListGroupMembers(ctx, db, id)
	if err != nil {
		return nil, err
	}
	out := make(ListGroupMembers200JSONResponse, 0, len(members))
	for _, m := range members {
		out = append(out, UserRef{Id: mustUUID(m.ID), Username: m.Username, DisplayName: m.DisplayName})
	}
	return out, nil
}

// ── takedowns (brief §6.5 rule 8) ──

func (s *Server) TakeDownBook(ctx context.Context, req TakeDownBookRequestObject) (TakeDownBookResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(deref(req.Body.Reason))
	if reason == "" {
		return nil, badRequest("reason_required", "a takedown needs a reason")
	}
	id := req.BookId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if _, err := store.GetBook(ctx, tx, id); err != nil {
			return err
		}
		if _, err := store.ActiveTakedown(ctx, tx, id); err == nil {
			return apiErr(http.StatusConflict, "already_taken_down", "this book is already taken down")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.CreateTakedown(ctx, tx, id, reason, admin); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "book.takedown", ObjectType: "book", ObjectID: id,
			IP: clientIP(ctx), Details: map[string]any{"reason": reason}})
	})
	if err != nil {
		return nil, err
	}
	return TakeDownBook204Response{}, nil
}

func (s *Server) LiftTakedown(ctx context.Context, req LiftTakedownRequestObject) (LiftTakedownResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.BookId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.LiftTakedown(ctx, tx, id); err != nil {
			return err
		}
		if err := store.RecordBookChange(ctx, tx, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "book.takedown_lift", ObjectType: "book", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return LiftTakedown204Response{}, nil
}
