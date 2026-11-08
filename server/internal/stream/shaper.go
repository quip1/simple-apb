// SPDX-License-Identifier: AGPL-3.0-or-later

// Package stream serves audio bytes: range responses, HLS, and the global
// egress shaper (brief §6.10).
//
// The shaper enforces one global byte rate for every streamed or downloaded
// response. Each response is a Flow; every tick the shaper hands out that
// tick's byte budget:
//  1. live playback flows first get a guaranteed minimum (their bitrate × 1.5),
//  2. the rest is shared by weighted fair queuing: priority classes have
//     weights (admin 4, high-priority group 2, everyone else 1); a class's
//     share is split equally between its *users*, and a user's share between
//     their connections, so opening more connections doesn't get you more.
//
// Unused share is re-offered to flows that still want more (water-filling),
// so the link is never left idle while someone is waiting.
package stream

import (
	"context"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Class is a priority class.
type Class int

const (
	ClassNormal   Class = iota // everyone else
	ClassPriority              // users in a high-priority group (e.g. Friends)
	ClassAdmin                 // admins
)

var classWeight = map[Class]float64{ClassNormal: 1, ClassPriority: 2, ClassAdmin: 4}

// tick is how often budget is handed out.
const tick = 20 * time.Millisecond

// Demand is one flow's request in a tick (input to Allocate).
type Demand struct {
	User   string
	Class  Class
	Live   bool
	Want   int64 // bytes wanted now
	MinPer int64 // guaranteed bytes per tick (live flows)
}

// Allocate splits budget among demands. It is pure and deterministic.
func Allocate(budget int64, ds []Demand) []int64 {
	out := make([]int64, len(ds))
	if budget <= 0 || len(ds) == 0 {
		return out
	}
	remaining := budget
	// 1. Live minimums (scaled down proportionally if they exceed the budget).
	var minTotal int64
	for _, d := range ds {
		if d.Live {
			minTotal += min(d.Want, d.MinPer)
		}
	}
	scale := 1.0
	if minTotal > budget {
		scale = float64(budget) / float64(minTotal)
	}
	for i, d := range ds {
		if d.Live {
			g := int64(float64(min(d.Want, d.MinPer)) * scale)
			out[i] = g
			remaining -= g
		}
	}
	// 2. Weighted fair share of what's left, by water-filling.
	for remaining > 0 {
		weights := shareWeights(ds, out)
		var total float64
		for _, w := range weights {
			total += w
		}
		if total == 0 {
			break
		}
		given := int64(0)
		pool := remaining
		for i, w := range weights {
			if w == 0 {
				continue
			}
			share := int64(float64(pool) * w / total)
			if share == 0 {
				share = 1
			}
			need := ds[i].Want - out[i]
			g := min(share, need, remaining-given)
			out[i] += g
			given += g
		}
		remaining -= given
		if given == 0 {
			break
		}
	}
	return out
}

// shareWeights gives each still-hungry flow its weight: class weight split
// equally across that class's hungry users, then across the user's hungry flows.
func shareWeights(ds []Demand, got []int64) []float64 {
	type key struct {
		c Class
		u string
	}
	usersPerClass := map[Class]map[string]bool{}
	flowsPerUser := map[key]int{}
	for i, d := range ds {
		if d.Want-got[i] <= 0 {
			continue
		}
		if usersPerClass[d.Class] == nil {
			usersPerClass[d.Class] = map[string]bool{}
		}
		usersPerClass[d.Class][d.User] = true
		flowsPerUser[key{d.Class, d.User}]++
	}
	w := make([]float64, len(ds))
	for i, d := range ds {
		if d.Want-got[i] <= 0 {
			continue
		}
		w[i] = classWeight[d.Class] / float64(len(usersPerClass[d.Class])) / float64(flowsPerUser[key{d.Class, d.User}])
	}
	return w
}

// Shaper enforces the global egress rate.
type Shaper struct {
	mu      sync.Mutex
	rateBps int64 // bytes per second; 0 = unlimited
	flows   map[*Flow]struct{}
	kick    chan struct{}
	stop    chan struct{}
	started bool
}

// NewShaper creates a shaper for mbps megabits per second (0 = unlimited).
func NewShaper(mbps int) *Shaper {
	s := &Shaper{flows: map[*Flow]struct{}{}, kick: make(chan struct{}, 1), stop: make(chan struct{})}
	s.SetRate(mbps)
	return s
}

// SetRate changes the cap at runtime.
func (s *Shaper) SetRate(mbps int) {
	s.mu.Lock()
	s.rateBps = int64(mbps) * 1_000_000 / 8
	s.mu.Unlock()
}

// Start runs the allocation loop until ctx is done.
func (s *Shaper) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		last := time.Now()
		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				for f := range s.flows {
					f.grant(-1)
				}
				s.mu.Unlock()
				return
			case now := <-t.C:
				dt := min(now.Sub(last), 5*tick)
				last = now
				s.distribute(dt)
			}
		}
	}()
}

func (s *Shaper) distribute(dt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	budget := int64(float64(s.rateBps) * dt.Seconds())
	var (
		fs []*Flow
		ds []Demand
	)
	for f := range s.flows {
		if f.want > 0 {
			fs = append(fs, f)
			ds = append(ds, Demand{User: f.User, Class: f.Class, Live: f.Live, Want: f.want,
				MinPer: int64(float64(f.minBps) * dt.Seconds())})
		}
	}
	if len(fs) == 0 {
		return
	}
	for i, g := range Allocate(budget, ds) {
		if g > 0 {
			fs[i].want = 0
			fs[i].grant(g)
		}
	}
}

// Flow is one shaped response.
type Flow struct {
	s      *Shaper
	User   string
	BookID string
	Class  Class
	Live   bool
	// Bitrate is the stream's bitrate in kbps (0 = unknown).
	Bitrate int
	minBps  int64
	want    int64      // guarded by s.mu
	ready   chan int64 // granted bytes; -1 = shaper stopped
	sent    int64      // guarded by s.mu
	opened  time.Time
}

// Open registers a flow. bitrateKbps sets the live guaranteed minimum (×1.5).
func (s *Shaper) Open(user, bookID string, class Class, live bool, bitrateKbps int) *Flow {
	f := &Flow{s: s, User: user, BookID: bookID, Class: class, Live: live, Bitrate: bitrateKbps, ready: make(chan int64, 1), opened: time.Now()}
	if live && bitrateKbps > 0 {
		f.minBps = int64(bitrateKbps) * 1000 / 8 * 3 / 2
	}
	s.mu.Lock()
	s.flows[f] = struct{}{}
	s.mu.Unlock()
	return f
}

// Close unregisters the flow.
func (f *Flow) Close() {
	f.s.mu.Lock()
	delete(f.s.flows, f)
	f.s.mu.Unlock()
}

func (f *Flow) grant(n int64) {
	select {
	case f.ready <- n:
	default:
	}
}

// Wait blocks until up to n bytes may be sent and returns how many.
func (f *Flow) Wait(ctx context.Context, n int) (int, error) {
	f.s.mu.Lock()
	unlimited := f.s.rateBps == 0 || !f.s.started
	if unlimited {
		f.sent += int64(n)
		f.s.mu.Unlock()
		return n, nil
	}
	f.want = int64(n)
	f.s.mu.Unlock()
	select {
	case g := <-f.ready:
		if g < 0 {
			return 0, context.Canceled
		}
		f.s.mu.Lock()
		f.sent += g
		f.s.mu.Unlock()
		return int(g), nil
	case <-ctx.Done():
		f.s.mu.Lock()
		f.want = 0
		f.s.mu.Unlock()
		return 0, ctx.Err()
	}
}

// FlowStat describes a live flow (for the admin dashboard).
type FlowStat struct {
	User, BookID string
	Class        Class
	Live         bool
	BitrateKbps  int
	SentBytes    int64
	Since        time.Time
}

// Stats is a snapshot of the shaper.
type Stats struct {
	RateBps int64
	Flows   []FlowStat
}

// Snapshot returns current flows.
func (s *Shaper) Snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{RateBps: s.rateBps}
	for f := range s.flows {
		st.Flows = append(st.Flows, FlowStat{User: f.User, BookID: f.BookID, Class: f.Class, Live: f.Live, BitrateKbps: f.Bitrate, SentBytes: f.sent, Since: f.opened})
	}
	sort.Slice(st.Flows, func(i, j int) bool { return st.Flows[i].Since.Before(st.Flows[j].Since) })
	return st
}

// chunk is the largest write handed to the shaper at once.
const chunk = 32 << 10

// Writer is an http.ResponseWriter whose body writes are shaped.
type Writer struct {
	http.ResponseWriter
	Flow *Flow
	Ctx  context.Context
}

func (w *Writer) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n, err := w.Flow.Wait(w.Ctx, min(len(p), chunk))
		if err != nil {
			return written, err
		}
		m, err := w.ResponseWriter.Write(p[:n])
		written += m
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// ReadFrom keeps io.Copy going through Write (so shaping applies).
func (w *Writer) ReadFrom(r io.Reader) (int64, error) {
	return io.CopyBuffer(writerOnly{w}, r, make([]byte, chunk))
}

type writerOnly struct{ io.Writer }
