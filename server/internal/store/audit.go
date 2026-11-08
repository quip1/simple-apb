// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// AuditEntry is one append-only audit_log row.
type AuditEntry struct {
	ActorID    string // "" for system actions
	Action     string // e.g. "user.approve", "setup.complete"
	ObjectType string
	ObjectID   string
	Details    map[string]any
	IP         string
}

// Audit appends to the audit log. Call it inside the same transaction as the
// change it records, so the log and the data never disagree.
func Audit(ctx context.Context, q Querier, e AuditEntry) error {
	details := "{}"
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		details = string(b)
	}
	now := NowMS()
	_, err := q.Exec(ctx, `
		INSERT INTO audit_log (id, actor_id, action, object_type, object_id, details, ip, at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), nullStr(e.ActorID), e.Action, nullStr(e.ObjectType), nullStr(e.ObjectID),
		details, nullStr(e.IP), now, now, now)
	if err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}
