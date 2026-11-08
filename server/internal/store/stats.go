// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ListenEvent is one progress_events row (brief §6.17: stats come from these).
type ListenEvent struct {
	BookID             string
	FromMS, ToMS       int64
	StartedAt, EndedAt int64
}

// ListenEvents returns a user's listening events that ended in [fromMS, toMS).
func ListenEvents(ctx context.Context, q Querier, userID string, fromMS, toMS int64) ([]ListenEvent, error) {
	rows, err := q.Query(ctx, `SELECT book_id, from_ms, to_ms, started_at, ended_at FROM progress_events
		WHERE user_id = ? AND ended_at >= ? AND ended_at < ? ORDER BY ended_at`, userID, fromMS, toMS)
	if err != nil {
		return nil, fmt.Errorf("listen events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ListenEvent
	for rows.Next() {
		var e ListenEvent
		if err := rows.Scan(&e.BookID, &e.FromMS, &e.ToMS, &e.StartedAt, &e.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListenSpan returns the first and last event time for a user (nil, nil if none).
func ListenSpan(ctx context.Context, q Querier, userID string) (*int64, *int64, error) {
	var lo, hi sql.NullInt64
	err := q.QueryRow(ctx, `SELECT MIN(ended_at), MAX(ended_at) FROM progress_events WHERE user_id = ?`, userID).Scan(&lo, &hi)
	if err != nil {
		return nil, nil, fmt.Errorf("listen span: %w", err)
	}
	return ptr(lo), ptr(hi), nil
}

// FinishedBook is a book a user finished.
type FinishedBook struct {
	BookID     string
	FinishedAt int64
}

// FinishedBetween lists books a user finished in [fromMS, toMS), newest first.
func FinishedBetween(ctx context.Context, q Querier, userID string, fromMS, toMS int64) ([]FinishedBook, error) {
	rows, err := q.Query(ctx, `SELECT book_id, finished_at FROM progress
		WHERE user_id = ? AND finished_at >= ? AND finished_at < ? ORDER BY finished_at DESC`, userID, fromMS, toMS)
	if err != nil {
		return nil, fmt.Errorf("finished books: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FinishedBook
	for rows.Next() {
		var f FinishedBook
		if err := rows.Scan(&f.BookID, &f.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
