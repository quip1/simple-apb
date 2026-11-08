// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/quip1/simple-apb/server/internal/backup"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Job types for operations (brief §6.18).
const (
	JobBackup        = "db_backup"
	JobExpireUploads = "expire_stale_uploads"
)

// Status keys in the settings table.
const (
	statusBackup  = "status_backup"
	statusScrub   = "status_scrub"
	statusUnclean = "status_unclean_shutdown"
)

// opStatus is the latest outcome of an operation.
type opStatus struct {
	AtMS    int64  `json:"at"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (a *App) setStatus(ctx context.Context, key string, ok bool, msg string) {
	if err := store.SetSetting(ctx, a.DB, a.Auth.Enc, key, opStatus{AtMS: store.NowMS(), OK: ok, Message: msg}, false); err != nil {
		a.log.Error("save status", "key", key, "err", err)
	}
}

func (a *App) getStatus(ctx context.Context, key string) *opStatus {
	var s opStatus
	if err := store.GetSetting(ctx, a.DB, a.Auth.Enc, key, &s); err != nil {
		return nil
	}
	return &s
}

func (a *App) registerOpsJobs() {
	a.Jobs.Handle(push.JobPush, a.Push.Handle)
	a.Jobs.Handle(JobBackup, func(ctx context.Context, _ *jobs.Job) (any, error) {
		name, err := a.Backup.Run(ctx)
		if err != nil {
			a.setStatus(ctx, statusBackup, false, err.Error())
			_ = push.Enqueue(ctx, a.DB, push.Recipients{Admins: true}, push.Event{Type: push.BackupFailed})
			_ = store.Audit(ctx, a.DB, store.AuditEntry{Action: "backup.failed", Details: map[string]any{"error": err.Error()}})
			if errors.Is(err, backup.ErrNoPGDump) {
				return nil, jobs.Permanent(err) // configuration problem: retrying won't help
			}
			return nil, err
		}
		a.setStatus(ctx, statusBackup, true, name)
		_ = store.Audit(ctx, a.DB, store.AuditEntry{Action: "backup.ok", Details: map[string]any{"file": name}})
		return map[string]string{"file": name}, nil
	})
	a.Jobs.Handle(library.JobScrub, func(ctx context.Context, j *jobs.Job) (any, error) {
		var p library.ScrubPayload
		_ = j.Decode(&p)
		res, err := a.Lib.Scrub(ctx, p.ModifiedWithin)
		if err != nil {
			a.setStatus(ctx, statusScrub, false, err.Error())
			return nil, err
		}
		ok := len(res.Quarantined) == 0
		msg := fmt.Sprintf("%d files in %d books checked; %d mismatched, %d missing", res.Files, res.Books, res.Mismatches, res.Missing)
		a.setStatus(ctx, statusScrub, ok, msg)
		if !ok {
			_ = push.Enqueue(ctx, a.DB, push.Recipients{Admins: true}, push.Event{Type: push.IntegrityFailed})
		}
		return res, nil
	})
	a.Jobs.Handle(JobExpireUploads, func(ctx context.Context, _ *jobs.Job) (any, error) {
		n, err := a.Lib.ExpireStaleUploads(ctx, 7*24*time.Hour)
		return map[string]int{"expired": n}, err
	})
}

func (a *App) scheduleOpsJobs(ctx context.Context) {
	a.Jobs.At(ctx, JobBackup, jobs.DailyAt(3))
	a.Jobs.At(ctx, library.JobScrub, jobs.WeeklyAt(time.Sunday, 4))
	a.Jobs.Every(ctx, JobExpireUploads, 24*time.Hour, nil)
}

// RecoverAfterCrash runs the startup check after an unclean shutdown
// (brief §6.18): DB integrity, orphaned uploads, a quick checksum pass over
// files modified in the last 24 h, and a notice to admins. Jobs stuck in
// "running" are requeued by Start.
func (a *App) RecoverAfterCrash(ctx context.Context) {
	problems := []string{}
	if a.DB.Dialect() == store.SQLite {
		var res string
		if err := a.DB.QueryRow(ctx, `PRAGMA integrity_check`).Scan(&res); err != nil || res != "ok" {
			problems = append(problems, fmt.Sprintf("database integrity check: %v %s", err, res))
		}
	} else if err := a.DB.SQL.PingContext(ctx); err != nil {
		problems = append(problems, "database unreachable: "+err.Error())
	}
	requeued, removed, err := a.Lib.RecoverUploads(ctx)
	if err != nil {
		problems = append(problems, "upload recovery: "+err.Error())
	}
	if _, err := jobs.Enqueue(ctx, a.DB, library.JobScrub, library.ScrubPayload{ModifiedWithin: 24 * time.Hour}, time.Time{}, ""); err != nil {
		problems = append(problems, "queue quick checksum check: "+err.Error())
	}
	msg := fmt.Sprintf("recovered: %d uploads resumed, %d orphaned staging files removed; quick checksum check queued", requeued, removed)
	ok := len(problems) == 0
	if !ok {
		msg = fmt.Sprintf("%v; %s", problems, msg)
		a.log.Error("startup check after unclean shutdown found problems", "problems", problems)
	} else {
		a.log.Warn("unclean shutdown detected; startup check passed", "uploads_resumed", requeued, "orphans_removed", removed)
	}
	a.setStatus(ctx, statusUnclean, ok, msg)
	_ = store.Audit(ctx, a.DB, store.AuditEntry{Action: "system.unclean_shutdown", Details: map[string]any{"ok": ok, "summary": msg}})
	_ = push.Enqueue(ctx, a.DB, push.Recipients{Admins: true}, push.Event{Type: push.UncleanShutdown})
	a.Jobs.Wake()
}

func newBackupService(a *App, backupsDir string) *backup.Service {
	return &backup.Service{DB: a.DB, Dir: filepath.Join(backupsDir, "db")}
}

// enqueueUploadPush notifies the uploader when an import job ends.
func (a *App) enqueueUploadPush(ctx context.Context, uploadID string, failed bool) {
	var user string
	if err := a.DB.QueryRow(ctx, `SELECT user_id FROM uploads WHERE id = ?`, uploadID).Scan(&user); err != nil {
		return
	}
	t := push.UploadDone
	if failed {
		t = push.UploadFailed
	}
	_ = push.Enqueue(context.WithoutCancel(ctx), a.DB, push.Recipients{Users: []string{user}}, push.Event{Type: t, ID: uploadID})
	a.Jobs.Wake()
}
