// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
)

func ev(book string, from, to int64, start time.Time, wall time.Duration) store.ListenEvent {
	return store.ListenEvent{BookID: book, FromMS: from, ToMS: to, StartedAt: start.UnixMilli(), EndedAt: start.Add(wall).UnixMilli()}
}

func TestListenedMS(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		e    store.ListenEvent
		want int64
	}{
		{"1x for 15 s", ev("b", 0, 15_000, t0, 15*time.Second), 15_000},
		{"2x counts real time", ev("b", 0, 30_000, t0, 15*time.Second), 15_000},
		{"paused in between", ev("b", 0, 15_000, t0, 2*time.Hour), 15_000},
		{"seek forward", ev("b", 0, 600_000, t0, 15*time.Second), 0},
		{"seek back", ev("b", 600_000, 0, t0, 15*time.Second), 0},
		{"first write of a device", ev("b", 5000, 5000, t0, 0), 0},
	} {
		if got := ListenedMS(c.e); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSummarizeDaysAndStreak(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	day := func(d, h int) time.Time { return time.Date(2026, 3, d, h, 0, 0, 0, time.UTC) }
	events := []store.ListenEvent{
		ev("a", 0, 60_000, day(1, 10), time.Minute),
		ev("a", 60_000, 120_000, day(2, 10), time.Minute),
		ev("b", 0, 30_000, day(3, 10), 30*time.Second),
		// 23:30 UTC on the 4th is the 5th in Berlin.
		ev("b", 30_000, 60_000, day(4, 23).Add(30*time.Minute), 30*time.Second),
		ev("b", 0, 900_000, day(9, 10), 15*time.Second), // a seek: ignored
	}
	s := Summarize(events, berlin)
	if s.TotalMS != 180_000 {
		t.Fatalf("total %d", s.TotalMS)
	}
	want := []string{"2026-03-01", "2026-03-02", "2026-03-03", "2026-03-05"}
	if len(s.Days) != len(want) {
		t.Fatalf("days %+v", s.Days)
	}
	for i, d := range want {
		if s.Days[i].Date != d {
			t.Fatalf("day %d = %s, want %s", i, s.Days[i].Date, d)
		}
	}
	if s.LongestStreak != 3 {
		t.Fatalf("streak %d", s.LongestStreak)
	}
	if top := Top(s.PerBook, 1); len(top) != 1 || top[0].ID != "a" || top[0].MS != 120_000 {
		t.Fatalf("top %+v", top)
	}
}
