// SPDX-License-Identifier: AGPL-3.0-or-later

// Package jobs is the persisted background job queue (the `jobs` table).
// Jobs survive restarts and crashes: a job left in `running` by a crash is
// requeued at startup (RequeueStale), and failures retry with exponential
// backoff until max_attempts.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
)

// Handler runs one job. Returning an error schedules a retry; wrap it with
// Permanent to fail immediately.
type Handler func(ctx context.Context, job *Job) (result any, err error)

// Job is a claimed job.
type Job struct {
	ID       string
	Type     string
	Payload  json.RawMessage
	Attempts int64
}

// Decode unmarshals the payload.
func (j *Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

type permanentErr struct{ error }

func (p permanentErr) Unwrap() error { return p.error }

// Permanent marks an error as not worth retrying.
func Permanent(err error) error { return permanentErr{err} }

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanentErr
	return errors.As(err, &p)
}

// Enqueue adds a job. runAfter zero = now.
func Enqueue(ctx context.Context, q store.Querier, typ string, payload any, runAfter time.Time, createdBy string) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if runAfter.IsZero() {
		runAfter = time.Now()
	}
	id, now := store.NewID(), store.NowMS()
	var by any
	if createdBy != "" {
		by = createdBy
	}
	_, err = q.Exec(ctx, `INSERT INTO jobs (id, type, payload, status, priority, attempts, max_attempts, run_after, created_by, created_at, updated_at)
		VALUES (?, ?, ?, 'queued', 0, 0, 5, ?, ?, ?, ?)`, id, typ, string(b), runAfter.UnixMilli(), by, now, now)
	if err != nil {
		return "", fmt.Errorf("enqueue %s: %w", typ, err)
	}
	return id, nil
}

// EnqueueUnique adds a job unless one of the same type is already queued or running.
func EnqueueUnique(ctx context.Context, q store.Querier, typ string, payload any, runAfter time.Time) (string, bool, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM jobs WHERE type = ? AND status IN ('queued', 'running') LIMIT 1`, typ).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	id, err = Enqueue(ctx, q, typ, payload, runAfter, "")
	return id, err == nil, err
}

// Status is a job's state for API callers.
type Status struct {
	ID, Type, Status, LastError string
	Attempts                    int64
	Result                      json.RawMessage
	CreatedAt, UpdatedAt        int64
}

// Get returns a job's status.
func Get(ctx context.Context, q store.Querier, id string) (*Status, error) {
	var (
		s       Status
		lastErr sql.NullString
		result  sql.NullString
	)
	err := q.QueryRow(ctx, `SELECT id, type, status, attempts, last_error, result, created_at, updated_at FROM jobs WHERE id = ?`, id).
		Scan(&s.ID, &s.Type, &s.Status, &s.Attempts, &lastErr, &result, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.LastError = lastErr.String
	if result.Valid {
		s.Result = json.RawMessage(result.String)
	}
	return &s, nil
}

// RequeueStale puts jobs stuck in `running` (a crash mid-job) back in the queue.
func RequeueStale(ctx context.Context, db *store.DB) (int64, error) {
	res, err := db.Exec(ctx, `UPDATE jobs SET status = 'queued', locked_at = NULL, updated_at = ? WHERE status = 'running'`, store.NowMS())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Runner executes queued jobs with a fixed number of workers.
type Runner struct {
	DB       *store.DB
	Log      *slog.Logger
	Workers  int
	Poll     time.Duration
	handlers map[string]Handler
	wake     chan struct{}
	wg       sync.WaitGroup
}

// NewRunner creates a runner.
func NewRunner(db *store.DB, log *slog.Logger, workers int) *Runner {
	return &Runner{DB: db, Log: log, Workers: max(1, workers), Poll: 2 * time.Second,
		handlers: map[string]Handler{}, wake: make(chan struct{}, 1)}
}

// Handle registers a handler for a job type.
func (r *Runner) Handle(typ string, h Handler) { r.handlers[typ] = h }

// Wake nudges idle workers (call after Enqueue for low latency).
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Start launches workers; they stop when ctx is cancelled. Call Wait to drain.
func (r *Runner) Start(ctx context.Context) {
	for range r.Workers {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.loop(ctx)
		}()
	}
}

// Wait blocks until workers have exited.
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) loop(ctx context.Context) {
	t := time.NewTicker(r.Poll)
	defer t.Stop()
	for {
		for {
			ran, err := r.RunOne(ctx)
			if err != nil && ctx.Err() == nil {
				r.Log.Error("job runner", "err", err)
			}
			if !ran || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

// RunOne claims and runs one ready job. Reports whether a job ran.
func (r *Runner) RunOne(ctx context.Context) (bool, error) {
	job, maxAttempts, err := r.claim(ctx)
	if err != nil || job == nil {
		return false, err
	}
	h, ok := r.handlers[job.Type]
	var (
		result any
		runErr error
	)
	if !ok {
		runErr = Permanent(fmt.Errorf("no handler for job type %q", job.Type))
	} else {
		result, runErr = safeRun(ctx, h, job)
	}
	return true, r.finish(context.WithoutCancel(ctx), job, maxAttempts, result, runErr)
}

func safeRun(ctx context.Context, h Handler, job *Job) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = Permanent(fmt.Errorf("job panicked: %v", p))
		}
	}()
	return h(ctx, job)
}

func (r *Runner) claim(ctx context.Context) (*Job, int64, error) {
	now := store.NowMS()
	var (
		j           Job
		payload     string
		maxAttempts int64
	)
	// The status check in the outer UPDATE makes the claim atomic even if two
	// workers pick the same id: only one UPDATE matches.
	err := r.DB.QueryRow(ctx, `UPDATE jobs SET status = 'running', locked_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE status = 'queued' AND id = (SELECT id FROM jobs WHERE status = 'queued' AND run_after <= ?
			ORDER BY priority DESC, run_after, id LIMIT 1)
		RETURNING id, type, payload, attempts, max_attempts`, now, now, now).
		Scan(&j.ID, &j.Type, &payload, &j.Attempts, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("claim job: %w", err)
	}
	j.Payload = json.RawMessage(payload)
	return &j, maxAttempts, nil
}

func (r *Runner) finish(ctx context.Context, j *Job, maxAttempts int64, result any, runErr error) error {
	now := store.NowMS()
	if runErr == nil {
		var res any
		if result != nil {
			b, err := json.Marshal(result)
			if err != nil {
				return err
			}
			res = string(b)
		}
		_, err := r.DB.Exec(ctx, `UPDATE jobs SET status = 'succeeded', result = ?, last_error = NULL, locked_at = NULL, updated_at = ? WHERE id = ?`,
			res, now, j.ID)
		return err
	}
	var perm permanentErr
	if errors.As(runErr, &perm) || j.Attempts >= maxAttempts {
		r.Log.Warn("job failed", "type", j.Type, "id", j.ID, "attempts", j.Attempts, "err", runErr)
		_, err := r.DB.Exec(ctx, `UPDATE jobs SET status = 'failed', last_error = ?, locked_at = NULL, updated_at = ? WHERE id = ?`,
			runErr.Error(), now, j.ID)
		return err
	}
	backoff := time.Duration(math.Pow(4, float64(j.Attempts-1))) * 15 * time.Second // 15s, 1m, 4m, 16m…
	_, err := r.DB.Exec(ctx, `UPDATE jobs SET status = 'queued', last_error = ?, locked_at = NULL, run_after = ?, updated_at = ? WHERE id = ?`,
		runErr.Error(), now+backoff.Milliseconds(), now, j.ID)
	return err
}

// Every schedules a job type at an interval (deduplicated with EnqueueUnique).
func (r *Runner) Every(ctx context.Context, typ string, interval time.Duration, payload any) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if _, created, err := EnqueueUnique(ctx, r.DB, typ, payload, time.Time{}); err != nil && ctx.Err() == nil {
				r.Log.Error("schedule job", "type", typ, "err", err)
			} else if created {
				r.Wake()
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
