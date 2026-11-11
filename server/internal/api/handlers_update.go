// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/update"
	"github.com/quip1/simple-apb/server/internal/version"
)

// JobUpdateCheck looks for a new release (daily).
const JobUpdateCheck = "update_check"

// Updates (brief §6.21, ADR-0049): the admin panel shows new releases and,
// with the updater container, updates the server in one click.

func (s *Server) updateStatus() UpdateStatus {
	app := s.App()
	out := UpdateStatus{CurrentVersion: version.Version, UpdaterPresent: update.UpdaterPresent(app.UpdaterDir)}
	rel, at, msg := app.Updates.State()
	if !at.IsZero() {
		out.CheckedAt = &at
	}
	out.CheckError = strPtr(msg)
	if rel != nil {
		out.Latest = &ReleaseInfo{Version: rel.Version, Url: rel.URL, Notes: rel.Notes, PublishedAt: rel.PublishedAt}
		out.UpdateAvailable = update.Newer(rel.Version, version.Version)
	}
	if a := update.Last(app.UpdaterDir); a != nil {
		out.LastUpdate = &UpdateAttempt{FromVersion: strPtr(a.From), ToVersion: a.To, Status: UpdateAttemptStatus(a.Status),
			Message: strPtr(a.Message), At: a.At}
	}
	return out
}

func (s *Server) GetUpdateStatus(ctx context.Context, _ GetUpdateStatusRequestObject) (GetUpdateStatusResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return GetUpdateStatus200JSONResponse(s.updateStatus()), nil
}

func (s *Server) CheckForUpdate(ctx context.Context, _ CheckForUpdateRequestObject) (CheckForUpdateResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	_ = s.App().Updates.Check(ctx) // the error is part of the status
	return CheckForUpdate200JSONResponse(s.updateStatus()), nil
}

func (s *Server) StartUpdate(ctx context.Context, req StartUpdateRequestObject) (StartUpdateResponseObject, error) {
	actor, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	app := s.App()
	to := req.Body.Version
	if !update.Valid(to) {
		return nil, badRequest("invalid_version", "not a version number")
	}
	if !update.Newer(to, version.Version) {
		return nil, badRequest("not_newer", "that version isn't newer than this server")
	}
	if !update.UpdaterPresent(app.UpdaterDir) {
		return StartUpdate409JSONResponse{Code: "no_updater", Message: "the updater container isn't running"}, nil
	}
	if a := update.Last(app.UpdaterDir); a != nil && (a.Status == "requested" || a.Status == "running") {
		return StartUpdate409JSONResponse{Code: "busy", Message: "an update is already under way"}, nil
	}
	// A backup first: the way back if the new version's data changes go wrong.
	name, err := app.Backup.Run(ctx)
	if err != nil {
		s.log().ErrorContext(ctx, "backup before update failed", "err", err)
		return StartUpdate500JSONResponse{Code: "backup_failed", Message: "the backup before the update failed; nothing was changed"}, nil
	}
	if err := update.Request(app.UpdaterDir, version.Version, to); err != nil {
		if errors.Is(err, update.ErrBusy) {
			return StartUpdate409JSONResponse{Code: "busy", Message: "an update is already under way"}, nil
		}
		return nil, err
	}
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: actor, Action: "server.update_requested", ObjectType: "server",
		IP: clientIP(ctx), Details: map[string]any{"from": version.Version, "to": to, "backup": name}})
	return StartUpdate202JSONResponse(s.updateStatus()), nil
}
