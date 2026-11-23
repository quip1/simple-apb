// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/base64"
	"encoding/json"
)

// Cursors are opaque to clients; today they wrap an offset (ADR-0014).
type pageCursor struct {
	Offset int `json:"o"`
}

func encodeCursor(offset int) *string {
	b, _ := json.Marshal(pageCursor{Offset: offset})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(c *string) (int, error) {
	if c == nil || *c == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(*c)
	var pc pageCursor
	if err != nil || json.Unmarshal(b, &pc) != nil || pc.Offset < 0 {
		return 0, badRequest("invalid_cursor", "the cursor is not valid")
	}
	return pc.Offset, nil
}

func limitOf(l *int, def int) int {
	if l == nil || *l <= 0 {
		return def
	}
	return min(*l, 200)
}

// trimPage cuts a "limit+1" fetch back to limit items and returns the cursor
// of the next page, or nil when this is the last one.
func trimPage[T any](items []T, offset, limit int) ([]T, *string) {
	if len(items) <= limit {
		return items, nil
	}
	return items[:limit], encodeCursor(offset + limit)
}
