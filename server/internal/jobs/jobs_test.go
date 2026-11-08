// SPDX-License-Identifier: AGPL-3.0-or-later

package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.OpenDB(context.Background(), store.SQLite, filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRunSucceedRetryFail(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	r := NewRunner(db, slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	calls := 0
	r.Handle("ok", func(_ context.Context, j *Job) (any, error) {
		var p struct{ N int }
		_ = j.Decode(&p)
		return map[string]int{"double": p.N * 2}, nil
	})
	r.Handle("flaky", func(context.Context, *Job) (any, error) { calls++; return nil, errors.New("try again") })
	r.Handle("broken", func(context.Context, *Job) (any, error) { return nil, Permanent(errors.New("bad input")) })
	r.Handle("panics", func(context.Context, *Job) (any, error) { panic("boom") })

	okID, _ := Enqueue(ctx, db, "ok", map[string]int{"N": 21}, time.Time{}, "")
	flakyID, _ := Enqueue(ctx, db, "flaky", nil, time.Time{}, "")
	brokenID, _ := Enqueue(ctx, db, "broken", nil, time.Time{}, "")
	panicID, _ := Enqueue(ctx, db, "panics", nil, time.Time{}, "")
	laterID, _ := Enqueue(ctx, db, "ok", nil, time.Now().Add(time.Hour), "")

	for {
		ran, err := r.RunOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	check := func(id, want string) *Status {
		s, err := Get(ctx, db, id)
		if err != nil || s.Status != want {
			t.Fatalf("job %s: %+v %v, want %s", id, s, err, want)
		}
		return s
	}
	if s := check(okID, "succeeded"); string(s.Result) != `{"double":42}` {
		t.Fatalf("result %s", s.Result)
	}
	if s := check(flakyID, "queued"); s.LastError != "try again" || s.Attempts != 1 || calls != 1 {
		t.Fatalf("flaky should be rescheduled: %+v calls=%d", s, calls)
	}
	check(brokenID, "failed")
	if s := check(panicID, "failed"); s.LastError == "" {
		t.Fatal("panic not recorded")
	}
	check(laterID, "queued")
}

func TestRequeueStaleAndUnique(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	id, created, err := EnqueueUnique(ctx, db, "scan", nil, time.Time{})
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, created, _ := EnqueueUnique(ctx, db, "scan", nil, time.Time{}); created {
		t.Fatal("duplicate scan job created")
	}
	// Simulate a crash mid-job.
	if _, err := db.Exec(ctx, `UPDATE jobs SET status = 'running' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	n, err := RequeueStale(ctx, db)
	if err != nil || n != 1 {
		t.Fatalf("requeue: %d %v", n, err)
	}
	if s, _ := Get(ctx, db, id); s.Status != "queued" {
		t.Fatalf("status %s", s.Status)
	}
}
