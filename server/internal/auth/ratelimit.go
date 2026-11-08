// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is a keyed token-bucket rate limiter (per IP, per account…).
// Buckets idle for longer than the refill window are dropped to bound memory.
type Limiter struct {
	mu      sync.Mutex
	every   time.Duration // time to refill one token
	burst   int
	buckets map[string]*bucket
	now     func() time.Time
	lastGC  time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewLimiter allows `count` events per `window`, with bursts up to `count`.
func NewLimiter(count int, window time.Duration) *Limiter {
	return &Limiter{every: window / time.Duration(count), burst: count, buckets: map[string]*bucket{}, now: time.Now}
}

// Allow consumes a token for key. When denied it returns the wait until the next token.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastGC) > time.Minute {
		l.gc(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	r := b.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, l.every
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

func (l *Limiter) gc(now time.Time) {
	l.lastGC = now
	idle := l.every * time.Duration(l.burst)
	for k, b := range l.buckets {
		if now.Sub(b.seen) > idle {
			delete(l.buckets, k)
		}
	}
}

// Limits groups the auth rate limiters (brief §6.6).
type Limits struct {
	LoginIP      *Limiter // 5 / min per IP
	LoginAccount *Limiter // 5 / min per account
	SignupIP     *Limiter // 3 / hour per IP
	ResetIP      *Limiter // 3 / hour per IP
}

// DefaultLimits returns the brief's limits.
func DefaultLimits() *Limits {
	return &Limits{
		LoginIP:      NewLimiter(5, time.Minute),
		LoginAccount: NewLimiter(5, time.Minute),
		SignupIP:     NewLimiter(3, time.Hour),
		ResetIP:      NewLimiter(3, time.Hour),
	}
}

// LockoutAfter consecutive failures, accounts lock with exponential backoff.
const LockoutAfter = 10

// LockoutDuration returns how long to lock after `failures` consecutive
// failures: 0 below the threshold, then 1, 2, 4… minutes, capped at 24 h.
func LockoutDuration(failures int64) time.Duration {
	if failures < LockoutAfter {
		return 0
	}
	exp := float64(failures - LockoutAfter)
	d := time.Duration(math.Pow(2, math.Min(exp, 11))) * time.Minute
	return min(d, 24*time.Hour)
}
