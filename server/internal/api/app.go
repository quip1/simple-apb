// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/backup"
	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/metadata"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/stream"
)

// JobSyncPrune trims the delta-sync change log; cursors older than
// SyncLogRetention get 410 and do a full sync.
const JobSyncPrune = "sync_prune"

// SyncLogRetention is how long the change log is kept.
const SyncLogRetention = 90 * 24 * time.Hour

// JobHLSPrune evicts least-recently-used transcodes above the cache cap.
const JobHLSPrune = "hls_prune"

// App is everything that exists once the instance is set up.
type App struct {
	DB      *store.DB
	Auth    *auth.Service
	Lib     *library.Library
	Jobs    *jobs.Runner
	Uploads http.Handler
	Shaper  *stream.Shaper
	HLS     *stream.HLS
	Signer  stream.Signer
	Hub     *Hub
	Push    *push.Notifier
	Backup  *backup.Service
	Meta    *metadata.Service
	hlsCap  int64
	// StartedAt is when the server started (dashboard uptime).
	StartedAt time.Time
	log       *slog.Logger
}

// Settings returns a settings store on the app DB.
func (a *App) Settings() settings.Store { return settings.Store{Q: a.DB, Enc: a.Auth.Enc} }

// AppOptions configures NewApp.
type AppOptions struct {
	Paths          config.Paths
	Tools          media.Tools
	TrustedProxies bool // whether X-Forwarded-* may be honored (tus Location URLs)
	JobWorkers     int
	// Metadata are the provider endpoints (zero value: the public services).
	Metadata metadata.Endpoints
}

// NewApp wires the services for an open, set-up database.
func NewApp(ctx context.Context, db *store.DB, secrets *config.Secrets, log *slog.Logger, o AppOptions) (*App, error) {
	a := &App{DB: db, log: log, StartedAt: time.Now(), Auth: &auth.Service{
		DB: db, Enc: secrets, Tokens: auth.NewTokens(secrets.Derive("jwt")), Limits: auth.DefaultLimits(), Log: log,
		ResetKey: secrets.Derive("password-reset"),
	}}
	libSet, err := a.Settings().Library(ctx)
	if err != nil {
		return nil, err
	}
	a.Lib = &library.Library{DB: db, Tools: o.Tools, Paths: o.Paths, Root: libSet.Path, Log: log}
	if err := os.MkdirAll(a.Lib.IncomingPath(), 0o750); err != nil {
		return nil, fmt.Errorf("create upload staging dir: %w", err)
	}
	bw, err := a.Settings().Bandwidth(ctx)
	if err != nil {
		return nil, err
	}
	a.Shaper = stream.NewShaper(bw.EgressMbps)
	a.HLS = stream.NewHLS(o.Tools.FFmpeg, o.Paths.Cache, log)
	a.Signer = stream.Signer{Key: secrets.Derive("stream-url")}
	a.hlsCap = int64(max(bw.HLSCacheGB, 1)) << 30
	workers := o.JobWorkers
	if workers <= 0 {
		workers = 2
	}
	a.Jobs = jobs.NewRunner(db, log, workers)
	a.Hub = NewHub(ctx, db)
	a.Push = &push.Notifier{DB: db, Enc: secrets, Log: log}
	a.Auth.OnSignup = func(ctx context.Context, q store.Querier, requestID string) {
		_ = push.Enqueue(ctx, q, push.Recipients{Admins: true}, push.Event{Type: push.SignupRequest, ID: requestID})
	}
	endpoints := o.Metadata
	if endpoints.OpenLibrary == "" {
		endpoints = metadata.DefaultEndpoints()
	}
	a.Meta = &metadata.Service{DB: db, Endpoints: endpoints, SaveCover: a.Lib.SaveCover, Log: log,
		Config: func(ctx context.Context) (metadata.Config, error) {
			m, err := a.Settings().Metadata(ctx)
			return metadata.Config{OpenLibrary: m.OpenLibrary, GoogleBooks: m.GoogleBooks, GoogleAPIKey: m.GoogleAPIKey,
				Audnexus: m.Audnexus, Threshold: m.Threshold}, err
		}}
	a.Backup = newBackupService(a, o.Paths.Backups)
	a.Backup.PGDump = os.Getenv("SIMPLEAPB_PG_DUMP")
	a.registerJobs()
	a.registerOpsJobs()
	if a.Uploads, err = newUploadHandler(a, o.TrustedProxies); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) registerJobs() {
	a.Jobs.Handle(library.JobImportUpload, func(ctx context.Context, j *jobs.Job) (any, error) {
		var p library.UploadPayload
		if err := j.Decode(&p); err != nil {
			return nil, jobs.Permanent(err)
		}
		res, err := a.Lib.ProcessUpload(ctx, p.UploadID)
		if err == nil {
			a.enqueueUploadPush(ctx, p.UploadID, false)
		} else if jobs.IsPermanent(err) {
			a.enqueueUploadPush(ctx, p.UploadID, true)
		}
		return res, err
	})
	a.Jobs.Handle(library.JobScan, func(ctx context.Context, _ *jobs.Job) (any, error) {
		res, err := a.Lib.Scan(ctx, time.Now())
		if err == nil && res.Pending > 0 {
			// Something is still being copied; look again once it has settled.
			_, _ = jobs.Enqueue(context.WithoutCancel(ctx), a.DB, library.JobScan, nil, time.Now().Add(library.SettleTime+5*time.Second), "")
		}
		return res, err
	})
	a.Jobs.Handle(metadata.JobMatch, func(ctx context.Context, j *jobs.Job) (any, error) {
		var p metadata.MatchPayload
		if err := j.Decode(&p); err != nil {
			return nil, jobs.Permanent(err)
		}
		res, err := a.Meta.Match(ctx, p.BookID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, jobs.Permanent(err) // purged since
		}
		return res, err
	})
	a.registerToolJobs()
	a.Jobs.Handle(JobHLSPrune, func(_ context.Context, _ *jobs.Job) (any, error) {
		n, err := a.HLS.Prune(a.hlsCap)
		return map[string]int{"books_evicted": n}, err
	})
	a.Jobs.Handle(JobSyncPrune, func(ctx context.Context, _ *jobs.Job) (any, error) {
		low, err := store.PruneChanges(ctx, a.DB, a.Auth.Enc, time.Now().Add(-SyncLogRetention).UnixMilli())
		return map[string]int64{"low_water_seq": low}, err
	})
	a.Jobs.Handle(library.JobPurgeTrash, func(ctx context.Context, _ *jobs.Job) (any, error) {
		n, err := a.Lib.PurgeExpired(ctx, time.Now())
		return map[string]int{"purged": n}, err
	})
	a.Jobs.Handle(library.JobPurgeRemoved, func(ctx context.Context, _ *jobs.Job) (any, error) {
		n, err := store.PurgeRemovedEntries(ctx, a.DB, store.NowMS()-store.RecentlyRemovedWindowMS)
		return map[string]int64{"purged": n}, err
	})
}

// Start runs background work until ctx is cancelled: crashed jobs are
// requeued, workers start, periodic jobs are scheduled and the library is watched.
func (a *App) Start(ctx context.Context) {
	if n, err := jobs.RequeueStale(ctx, a.DB); err != nil {
		a.log.Error("requeue stale jobs", "err", err)
	} else if n > 0 {
		a.log.Warn("requeued jobs interrupted by a previous shutdown", "count", n)
	}
	a.Shaper.Start(ctx)
	go a.Hub.Run(ctx)
	a.Jobs.Every(ctx, JobSyncPrune, 24*time.Hour, nil)
	go func() { <-ctx.Done(); a.HLS.StopAll() }()
	a.Jobs.Start(ctx)
	a.Jobs.Every(ctx, JobHLSPrune, time.Hour, nil)
	a.scheduleOpsJobs(ctx)
	a.Jobs.Every(ctx, library.JobScan, time.Hour, nil)
	a.Jobs.Every(ctx, library.JobPurgeTrash, 24*time.Hour, nil)
	a.Jobs.Every(ctx, library.JobPurgeRemoved, 24*time.Hour, nil)
	a.Jobs.At(ctx, library.JobFindDuplicates, jobs.WeeklyAt(time.Saturday, 4))
	if err := a.Lib.Watch(ctx, a.TriggerScan); err != nil {
		a.log.Warn("library watcher unavailable; relying on periodic rescans", "err", err)
	}
}

// TriggerScan queues a library scan (deduplicated) and wakes the workers.
func (a *App) TriggerScan() {
	if _, _, err := jobs.EnqueueUnique(context.Background(), a.DB, library.JobScan, nil, time.Time{}); err != nil {
		a.log.Error("queue scan", "err", err)
		return
	}
	a.Jobs.Wake()
}
