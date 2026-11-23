// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"slices"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	for _, off := range []int{0, 1, 50, 1 << 20} {
		got, err := decodeCursor(encodeCursor(off))
		if err != nil || got != off {
			t.Fatalf("offset %d: got %d, %v", off, got, err)
		}
	}
	if got, err := decodeCursor(nil); err != nil || got != 0 {
		t.Fatalf("nil cursor: got %d, %v", got, err)
	}
	empty := ""
	if got, err := decodeCursor(&empty); err != nil || got != 0 {
		t.Fatalf("empty cursor: got %d, %v", got, err)
	}
}

func TestCursorRejectsGarbage(t *testing.T) {
	// "not base64!", base64 of "not json", and base64 of {"o":-1}.
	for _, c := range []string{"not base64!", "bm90IGpzb24", "eyJvIjotMX0"} {
		_, err := decodeCursor(&c)
		var he *httpError
		if !errors.As(err, &he) || he.code != "invalid_cursor" {
			t.Fatalf("cursor %q: want invalid_cursor, got %v", c, err)
		}
	}
}

func TestSyncCursor(t *testing.T) {
	for _, seq := range []int64{0, 7, 1 << 40} {
		got, err := decodeSyncCursor(encodeSyncCursor(seq))
		if err != nil || got != seq {
			t.Fatalf("seq %d: got %d, %v", seq, got, err)
		}
	}
	// An offset cursor is not a sync cursor.
	if _, err := decodeSyncCursor(*encodeCursor(3)); err == nil {
		t.Fatal("offset cursor accepted as a sync cursor")
	}
}

func TestLimitOf(t *testing.T) {
	ptr := func(n int) *int { return &n }
	cases := []struct {
		in   *int
		want int
	}{{nil, 50}, {ptr(0), 50}, {ptr(-3), 50}, {ptr(10), 10}, {ptr(200), 200}, {ptr(999), 200}}
	for _, c := range cases {
		if got := limitOf(c.in, 50); got != c.want {
			t.Fatalf("limitOf(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTrimPage(t *testing.T) {
	items, next := trimPage([]int{1, 2, 3}, 0, 3)
	if next != nil || len(items) != 3 {
		t.Fatalf("exact page: %v %v", items, next)
	}
	items, next = trimPage([]int{1, 2, 3, 4}, 6, 3)
	if !slices.Equal(items, []int{1, 2, 3}) || next == nil {
		t.Fatalf("overfull page: %v %v", items, next)
	}
	if off, err := decodeCursor(next); err != nil || off != 9 {
		t.Fatalf("next cursor: %d, %v", off, err)
	}
}
