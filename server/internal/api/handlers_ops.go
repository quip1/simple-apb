// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

func (s *Server) RegisterPush(ctx context.Context, req RegisterPushRequestObject) (RegisterPushResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	app := s.App()
	ntfy, err := app.Settings().Ntfy(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(deref(req.Body.Endpoint))
	if endpoint != "" {
		if err := push.ValidateEndpoint(endpoint, push.AllowedHosts(ntfy)); err != nil {
			return nil, badRequest("unsafe_endpoint", err.Error())
		}
	}
	device := req.Body.DeviceId.String()
	topic, err := push.Register(ctx, app.DB, p.User.ID, device, endpoint)
	if err != nil {
		return nil, err
	}
	out := RegisterPush200JSONResponse{DeviceId: req.Body.DeviceId, Topic: topic, Endpoint: strPtr(endpoint)}
	if ntfy.PublicURL != "" {
		u := strings.TrimRight(ntfy.PublicURL, "/") + "/" + topic
		out.NtfyUrl = &u
	}
	return out, nil
}

func (s *Server) UnregisterPush(ctx context.Context, req UnregisterPushRequestObject) (UnregisterPushResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := push.Unregister(ctx, s.App().DB, p.User.ID, req.DeviceId.String()); err != nil {
		return nil, err
	}
	return UnregisterPush204Response{}, nil
}

func opOut(s *opStatus) *OpStatus {
	if s == nil {
		return nil
	}
	at, ok := msTime(s.AtMS), s.OK
	return &OpStatus{At: &at, Ok: &ok, Message: strPtr(s.Message)}
}

func (s *Server) GetBackups(ctx context.Context, _ GetBackupsRequestObject) (GetBackupsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	app := s.App()
	files, err := app.Backup.List()
	if err != nil {
		return nil, err
	}
	out := GetBackups200JSONResponse{Backups: []BackupFile{},
		LastBackup: opOut(app.getStatus(ctx, statusBackup)), LastScrub: opOut(app.getStatus(ctx, statusScrub)),
		LastUncleanShutdown: opOut(app.getStatus(ctx, statusUnclean))}
	for _, f := range files {
		out.Backups = append(out.Backups, BackupFile{Name: f.Name, SizeBytes: f.SizeBytes, CreatedAt: f.CreatedAt.UTC()})
	}
	return out, nil
}

func (s *Server) queueAdminJob(ctx context.Context, typ string, payload any, action string) (string, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return "", err
	}
	app := s.App()
	id, created, err := jobs.EnqueueUnique(ctx, app.DB, typ, payload, time.Time{})
	if err != nil {
		return "", err
	}
	if !created {
		// A scheduled run is queued for later: run now instead.
		if _, err := app.DB.Exec(ctx, `UPDATE jobs SET run_after = ? WHERE id = ? AND status = 'queued'`, store.NowMS(), id); err != nil {
			return "", err
		}
	}
	app.Jobs.Wake()
	_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: admin, Action: action, ObjectType: "job", ObjectID: id, IP: clientIP(ctx)})
	return id, nil
}

func (s *Server) RunBackup(ctx context.Context, _ RunBackupRequestObject) (RunBackupResponseObject, error) {
	id, err := s.queueAdminJob(ctx, JobBackup, nil, "backup.requested")
	if err != nil {
		return nil, err
	}
	return RunBackup202JSONResponse{JobId: mustUUID(id)}, nil
}

func (s *Server) RunIntegrityCheck(ctx context.Context, _ RunIntegrityCheckRequestObject) (RunIntegrityCheckResponseObject, error) {
	id, err := s.queueAdminJob(ctx, library.JobScrub, library.ScrubPayload{}, "integrity.requested")
	if err != nil {
		return nil, err
	}
	return RunIntegrityCheck202JSONResponse{JobId: mustUUID(id)}, nil
}

// notifyDistribution tells the new audience about a book (after commit).
func (s *Server) notifyDistribution(ctx context.Context, bookID, targetType, targetID string) {
	to := push.Recipients{Users: []string{targetID}}
	if targetType == "group" {
		to = push.Recipients{Group: targetID}
	}
	app := s.App()
	if err := push.Enqueue(ctx, app.DB, to, push.Event{Type: push.LibraryAdded, ID: bookID}); err != nil && !errors.Is(err, context.Canceled) {
		s.log().WarnContext(ctx, "queue push", "err", err)
	}
	app.Jobs.Wake()
}
