// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"sort"
	"time"
	_ "time/tzdata" // user time zones work without OS zoneinfo (Windows, scratch images)

	"github.com/quip1/simple-apb/server/internal/store"
)

// MaxSpeed is the fastest playback speed; a jump further than this in the
// elapsed time is a seek, not listening.
const MaxSpeed = 3.5

// seekSlackMS tolerates clock jitter between position and wall time.
const seekSlackMS = 2000

// ListenedMS is the real time spent listening in one event: how far the
// position advanced, capped by the wall-clock time it took (so faster speeds
// count real time, and pauses inside the event don't count). Backward moves
// and seeks count as zero. At speeds below 1× this undercounts slightly
// (events don't record speed).
func ListenedMS(e store.ListenEvent) int64 {
	adv := e.ToMS - e.FromMS
	wall := e.EndedAt - e.StartedAt
	if adv <= 0 || wall <= 0 {
		return 0
	}
	if float64(adv) > float64(wall)*MaxSpeed+seekSlackMS {
		return 0
	}
	return min(adv, wall)
}

// Day is listening on one calendar day.
type Day struct {
	Date string // YYYY-MM-DD
	MS   int64
}

// Summary aggregates a period of listening.
type Summary struct {
	TotalMS       int64
	Days          []Day // days with listening, in order
	PerBook       map[string]int64
	LongestStreak int // consecutive days with listening
}

// Summarize groups events by calendar day in loc (by when each event ended).
func Summarize(events []store.ListenEvent, loc *time.Location) Summary {
	s := Summary{PerBook: map[string]int64{}}
	perDay := map[string]int64{}
	for _, e := range events {
		ms := ListenedMS(e)
		if ms == 0 {
			continue
		}
		day := time.UnixMilli(e.EndedAt).In(loc).Format(time.DateOnly)
		perDay[day] += ms
		s.PerBook[e.BookID] += ms
		s.TotalMS += ms
	}
	for d, ms := range perDay {
		s.Days = append(s.Days, Day{Date: d, MS: ms})
	}
	sort.Slice(s.Days, func(i, j int) bool { return s.Days[i].Date < s.Days[j].Date })
	s.LongestStreak = longestStreak(s.Days)
	return s
}

func longestStreak(days []Day) int {
	best, run := 0, 0
	var prev time.Time
	for i, d := range days {
		t, err := time.Parse(time.DateOnly, d.Date)
		if err != nil {
			continue
		}
		if i > 0 && t.Equal(prev.AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		best, prev = max(best, run), t
	}
	return best
}

// Ranked is an id with a total, for top lists.
type Ranked struct {
	ID string
	MS int64
}

// Top returns the n largest entries of m, largest first (ties by id).
func Top(m map[string]int64, n int) []Ranked {
	out := make([]Ranked, 0, len(m))
	for id, ms := range m {
		out = append(out, Ranked{id, ms})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MS != out[j].MS {
			return out[i].MS > out[j].MS
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
