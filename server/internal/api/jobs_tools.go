// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/library"
	"github.com/quip1/simple-apb/server/internal/store"
)

// registerToolJobs wires the book tools' background jobs (ADR-0048).
func (a *App) registerToolJobs() {
	// A book that's gone since the job was queued: nothing to retry.
	gone := func(err error) error {
		if errors.Is(err, store.ErrNotFound) {
			return jobs.Permanent(err)
		}
		return err
	}
	a.Jobs.Handle(library.JobWriteChapters, func(ctx context.Context, j *jobs.Job) (any, error) {
		var p library.BookPayload
		if err := j.Decode(&p); err != nil {
			return nil, jobs.Permanent(err)
		}
		if err := a.Lib.WriteChapters(ctx, p.BookID); err != nil {
			return nil, gone(err)
		}
		return map[string]string{"book_id": p.BookID}, nil
	})
	a.Jobs.Handle(library.JobDetectChapters, func(ctx context.Context, j *jobs.Job) (any, error) {
		var p library.DetectPayload
		if err := j.Decode(&p); err != nil {
			return nil, jobs.Permanent(err)
		}
		cs, err := a.Lib.DetectChapters(ctx, p)
		if err != nil {
			return nil, gone(err)
		}
		return map[string]any{"chapters": chaptersOut(cs)}, nil
	})
	a.Jobs.Handle(library.JobMerge, func(ctx context.Context, j *jobs.Job) (any, error) {
		var p library.MergePayload
		if err := j.Decode(&p); err != nil {
			return nil, jobs.Permanent(err)
		}
		id, err := a.Lib.Merge(ctx, p)
		if err != nil {
			return nil, gone(err)
		}
		// HLS segments are cached per book checksum: the new file gets new ones.
		return map[string]string{"book_id": id}, nil
	})
	a.Jobs.Handle(library.JobFindDuplicates, func(ctx context.Context, _ *jobs.Job) (any, error) {
		n, err := a.Lib.FindDuplicates(ctx)
		return map[string]int{"new_pairs": n}, err
	})
}
