// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"strings"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
)

func requireAdmin(ctx context.Context) (string, error) {
	p, err := requireCan(ctx, authz.Admin)
	if err != nil {
		return "", err
	}
	return p.User.ID, nil
}

func signupOut(r *store.SignupRequest) SignupRequestItem {
	return SignupRequestItem{Id: mustUUID(r.ID), UserId: mustUUID(r.UserID), Username: r.Username,
		Email: strPtr(r.Email), Message: strPtr(r.Message), Status: SignupRequestItemStatus(r.Status),
		CreatedAt: msTime(r.CreatedAt), DecidedAt: msTimePtr(r.DecidedAt)}
}

func (s *Server) ListSignupRequests(ctx context.Context, req ListSignupRequestsRequestObject) (ListSignupRequestsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	status := "pending"
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	list, err := store.ListSignupRequests(ctx, s.App().DB, status)
	if err != nil {
		return nil, err
	}
	out := make(ListSignupRequests200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, signupOut(r))
	}
	return out, nil
}

func (s *Server) ApproveSignupRequest(ctx context.Context, req ApproveSignupRequestRequestObject) (ApproveSignupRequestResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		r, err := store.GetSignupRequest(ctx, tx, req.RequestId.String())
		if err != nil {
			return err
		}
		if err := store.DecideSignupRequest(ctx, tx, r.ID, "approved", admin); err != nil {
			return errConflict // already decided
		}
		if err := store.SetUserStatus(ctx, tx, r.UserID, "active", "signup approved", nil); err != nil {
			return err
		}
		pub, err := store.EnsurePublicGroup(ctx, tx)
		if err != nil {
			return err
		}
		if err := store.AddGroupMember(ctx, tx, pub, r.UserID); err != nil {
			return err
		}
		if err := store.RecordGroupChange(ctx, tx, pub, r.UserID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user.approve", ObjectType: "user", ObjectID: r.UserID, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	// Email notification (when SMTP is set up) arrives with the mail templates.
	return ApproveSignupRequest204Response{}, nil
}

func (s *Server) DenySignupRequest(ctx context.Context, req DenySignupRequestRequestObject) (DenySignupRequestResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	reason := ""
	if req.Body != nil {
		reason = deref(req.Body.Reason)
	}
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		r, err := store.GetSignupRequest(ctx, tx, req.RequestId.String())
		if err != nil {
			return err
		}
		if err := store.DecideSignupRequest(ctx, tx, r.ID, "denied", admin); err != nil {
			return errConflict
		}
		// The user row stays (status deleted, reason signup_denied) so their
		// next login attempt can say the request was declined.
		if err := store.SetUserStatus(ctx, tx, r.UserID, "deleted", "signup_denied", nil); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user.deny", ObjectType: "user", ObjectID: r.UserID,
			IP: clientIP(ctx), Details: map[string]any{"reason": reason}})
	})
	if err != nil {
		return nil, err
	}
	return DenySignupRequest204Response{}, nil
}

func adminUserOut(u *store.User) AdminUser {
	return AdminUser{Id: mustUUID(u.ID), Username: u.Username, DisplayName: u.DisplayName, Email: strPtr(u.Email),
		Role: UserRole(u.Role), Status: UserStatus(u.Status), StatusReason: strPtr(u.StatusReason),
		StatusUntil: msTimePtr(u.StatusUntil), TotpEnabled: u.TOTPSecretEnc != "", QuotaBytes: u.QuotaBytes,
		CreatedAt: msTime(u.CreatedAt)}
}

func (s *Server) ListUsers(ctx context.Context, req ListUsersRequestObject) (ListUsersResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	status, limit := "", 200
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	list, err := store.ListUsers(ctx, s.App().DB, status, limit)
	if err != nil {
		return nil, err
	}
	out := make(ListUsers200JSONResponse, 0, len(list))
	for _, u := range list {
		out = append(out, adminUserOut(u))
	}
	return out, nil
}

func (s *Server) PatchUser(ctx context.Context, req PatchUserRequestObject) (PatchUserResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if strings.TrimSpace(b.Reason) == "" {
		return nil, badRequest("reason_required", "every moderation action needs a reason")
	}
	id := req.UserId.String()
	if id == admin && (b.Status != nil || b.Role != nil) {
		return nil, badRequest("cannot_modify_self", "you cannot change your own status or role")
	}
	db := s.App().DB
	err = db.InTx(ctx, func(tx *store.Tx) error {
		u, err := store.GetUser(ctx, tx, id)
		if err != nil {
			return err
		}
		if u.Status == "pending" || u.Status == "deleted" {
			return badRequest("user_not_active", "approve or restore the account first")
		}
		details := map[string]any{"reason": b.Reason}
		if b.Status != nil {
			var until *int64
			if *b.Status == AdminUserPatchStatusSuspended && b.StatusUntil != nil {
				ms := b.StatusUntil.UnixMilli()
				until = &ms
			}
			if err := store.SetUserStatus(ctx, tx, id, string(*b.Status), b.Reason, until); err != nil {
				return err
			}
			if *b.Status == AdminUserPatchStatusBanned || *b.Status == AdminUserPatchStatusSuspended {
				if err := store.RevokeUserSessions(ctx, tx, id); err != nil {
					return err
				}
			}
			details["status"] = string(*b.Status)
		}
		patch := store.UserPatch{QuotaBytes: b.QuotaBytes, Notice: b.Notice}
		if b.Role != nil {
			role := string(*b.Role)
			patch.Role = &role
			details["role"] = role
		}
		if b.QuotaBytes != nil {
			details["quota_bytes"] = *b.QuotaBytes
		}
		if b.Notice != nil {
			details["notice"] = *b.Notice
		}
		if err := store.PatchUser(ctx, tx, id, patch); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "user.update", ObjectType: "user", ObjectID: id,
			IP: clientIP(ctx), Details: details})
	})
	if err != nil {
		return nil, err
	}
	u, err := store.GetUser(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return PatchUser200JSONResponse(adminUserOut(u)), nil
}

func groupOut(g *store.Group) Group {
	return Group{Id: mustUUID(g.ID), Name: g.Name, Builtin: g.Builtin, HighPriority: g.HighPriority, Members: int(g.Members)}
}

func (s *Server) ListGroups(ctx context.Context, _ ListGroupsRequestObject) (ListGroupsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	list, err := store.ListGroups(ctx, s.App().DB)
	if err != nil {
		return nil, err
	}
	out := make(ListGroups200JSONResponse, 0, len(list))
	for _, g := range list {
		out = append(out, groupOut(g))
	}
	return out, nil
}

func (s *Server) CreateGroup(ctx context.Context, req CreateGroupRequestObject) (CreateGroupResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Body.Name)
	if name == "" {
		return nil, badRequest("invalid_name", "group name is required")
	}
	hp := req.Body.HighPriority != nil && *req.Body.HighPriority
	db := s.App().DB
	var id string
	err = db.InTx(ctx, func(tx *store.Tx) error {
		var err error
		if id, err = store.CreateGroup(ctx, tx, name, hp); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "group.create", ObjectType: "group", ObjectID: id,
			IP: clientIP(ctx), Details: map[string]any{"name": name, "high_priority": hp}})
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, apiErr(409, "group_exists", "a group with that name exists")
	}
	if err != nil {
		return nil, err
	}
	g, err := store.GetGroup(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return CreateGroup201JSONResponse(groupOut(g)), nil
}

func (s *Server) DeleteGroup(ctx context.Context, req DeleteGroupRequestObject) (DeleteGroupResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.GroupId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.DeleteGroup(ctx, tx, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "group.delete", ObjectType: "group", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return DeleteGroup204Response{}, nil
}

func (s *Server) AddGroupMember(ctx context.Context, req AddGroupMemberRequestObject) (AddGroupMemberResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	gid, uid := req.GroupId.String(), req.UserId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if _, err := store.GetGroup(ctx, tx, gid); err != nil {
			return err
		}
		if _, err := store.GetUser(ctx, tx, uid); err != nil {
			return err
		}
		if err := store.AddGroupMember(ctx, tx, gid, uid); err != nil {
			return err
		}
		if err := store.RecordGroupChange(ctx, tx, gid, uid); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "group.member_add", ObjectType: "group", ObjectID: gid,
			IP: clientIP(ctx), Details: map[string]any{"user_id": uid}})
	})
	if err != nil {
		return nil, err
	}
	return AddGroupMember204Response{}, nil
}

func (s *Server) RemoveGroupMember(ctx context.Context, req RemoveGroupMemberRequestObject) (RemoveGroupMemberResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	gid, uid := req.GroupId.String(), req.UserId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		g, err := store.GetGroup(ctx, tx, gid)
		if err != nil {
			return err
		}
		if g.Builtin {
			return badRequest("builtin_group", "every approved user stays in the Public group")
		}
		if err := store.RemoveGroupMember(ctx, tx, gid, uid); err != nil {
			return err
		}
		if err := store.RecordGroupChange(ctx, tx, gid, uid); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "group.member_remove", ObjectType: "group", ObjectID: gid,
			IP: clientIP(ctx), Details: map[string]any{"user_id": uid}})
	})
	if err != nil {
		return nil, err
	}
	return RemoveGroupMember204Response{}, nil
}

func (s *Server) AdminRevokeSession(ctx context.Context, req AdminRevokeSessionRequestObject) (AdminRevokeSessionResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id := req.SessionId.String()
	err = s.App().DB.InTx(ctx, func(tx *store.Tx) error {
		if err := store.RevokeSession(ctx, tx, id, ""); err != nil {
			return err
		}
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "session.admin_revoke", ObjectType: "session", ObjectID: id, IP: clientIP(ctx)})
	})
	if err != nil {
		return nil, err
	}
	return AdminRevokeSession204Response{}, nil
}
