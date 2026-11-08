// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"context"
	"math"
	"testing"
	"time"
)

func sum(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

func near(t *testing.T, what string, got, want int64) {
	t.Helper()
	if math.Abs(float64(got-want)) > float64(want)*0.02+2 {
		t.Errorf("%s: got %d, want ≈%d", what, got, want)
	}
}

func TestAllocateFairPerUserNotPerConnection(t *testing.T) {
	// Alice opens 3 bulk connections, Bob 1; same class → each *user* gets half.
	ds := []Demand{
		{User: "alice", Want: 1e9}, {User: "alice", Want: 1e9}, {User: "alice", Want: 1e9},
		{User: "bob", Want: 1e9},
	}
	g := Allocate(1200, ds)
	near(t, "alice total", g[0]+g[1]+g[2], 600)
	near(t, "bob", g[3], 600)
	if sum(g) > 1200 {
		t.Fatalf("over budget: %v", g)
	}
}

func TestAllocateClassWeights(t *testing.T) {
	ds := []Demand{
		{User: "admin", Class: ClassAdmin, Want: 1e9},
		{User: "friend", Class: ClassPriority, Want: 1e9},
		{User: "stranger", Class: ClassNormal, Want: 1e9},
	}
	g := Allocate(7000, ds)
	near(t, "admin", g[0], 4000)
	near(t, "friend", g[1], 2000)
	near(t, "stranger", g[2], 1000)
}

func TestAllocateRedistributesUnusedShare(t *testing.T) {
	// The admin only wants a little; the rest flows to others instead of idling.
	ds := []Demand{
		{User: "admin", Class: ClassAdmin, Want: 100},
		{User: "a", Want: 1e9},
		{User: "b", Want: 1e9},
	}
	g := Allocate(1000, ds)
	if g[0] != 100 {
		t.Fatalf("admin got %d", g[0])
	}
	near(t, "a", g[1], 450)
	near(t, "b", g[2], 450)
	if sum(g) != 1000 {
		t.Fatalf("budget not fully used: %v", g)
	}
}

func TestAllocateLiveMinimumBeforeBulk(t *testing.T) {
	// A bulk download in the admin class can't starve a stranger's live playback.
	ds := []Demand{
		{User: "admin", Class: ClassAdmin, Want: 1e9},
		{User: "listener", Class: ClassNormal, Live: true, Want: 1e9, MinPer: 300},
	}
	g := Allocate(1000, ds)
	if g[1] < 300 {
		t.Fatalf("live flow below its minimum: %v", g)
	}
	if sum(g) != 1000 {
		t.Fatalf("budget not used: %v", g)
	}
	// Minimums larger than the budget are scaled, never exceeding it.
	many := []Demand{{User: "x", Live: true, Want: 1e9, MinPer: 800}, {User: "y", Live: true, Want: 1e9, MinPer: 800}}
	if g := Allocate(1000, many); sum(g) > 1000 || g[0] < 450 || g[1] < 450 {
		t.Fatalf("scaled minimums: %v", g)
	}
}

func TestShaperEnforcesRate(t *testing.T) {
	s := NewShaper(8) // 8 Mbps = 1 MB/s
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	f := s.Open("u", "b", ClassNormal, false, 0)
	defer f.Close()
	start := time.Now()
	var sent int
	for sent < 300_000 {
		n, err := f.Wait(ctx, chunk)
		if err != nil {
			t.Fatal(err)
		}
		sent += n
	}
	el := time.Since(start)
	if el < 200*time.Millisecond || el > 900*time.Millisecond {
		t.Fatalf("300 KB at 1 MB/s took %v", el)
	}
	if st := s.Snapshot(); len(st.Flows) != 1 || st.Flows[0].SentBytes < 300_000 {
		t.Fatalf("snapshot %+v", st)
	}
}

func TestUnlimitedShaperDoesNotBlock(t *testing.T) {
	s := NewShaper(0)
	f := s.Open("u", "b", ClassNormal, true, 64)
	defer f.Close()
	n, err := f.Wait(context.Background(), 1<<20)
	if err != nil || n != 1<<20 {
		t.Fatal(n, err)
	}
}
