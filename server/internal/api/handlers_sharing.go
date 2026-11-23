// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Sharing books and collections with people and groups (brief §6.5 rules 5–6).
// Permission decisions come from authz; handlers only gather the facts.

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

// ownCollection checks that the collection is the user's and not deleted
// (store.ErrNotFound otherwise).
func ownCollection(ctx context.Context, q store.Querier, userID, id string) error {
	c, err := store.GetCollection(ctx, q, userID, id)
	if err != nil {
		return err
	}
	if c.DeletedAt != nil {
		return store.ErrNotFound
	}
	return nil
}

func (s *Server) ListCollectionShares(ctx context.Context, req ListCollectionSharesRequestObject) (ListCollectionSharesResponseObject, error) {
	p, err := requireCan(ctx, authz.Read)
	if err != nil {
		return nil, err
	}
	db := s.App().DB
	id := req.CollectionId.String()
	if err := ownCollection(ctx, db, p.User.ID, id); err != nil {
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
		if err := ownCollection(ctx, tx, p.User.ID, id); err != nil {
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
		if err := ownCollection(ctx, tx, p.User.ID, id); err != nil {
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
