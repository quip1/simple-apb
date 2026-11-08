// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sync holds the progress-sync rules (brief §6.8, ADR-0008).
//
// Import it as appsync: the name clashes with the standard library.
package sync

import "time"

// Stored is the server's current progress record for a user+book.
type Stored struct {
	PositionMS int64
	DeviceID   string
	ClientTS   time.Time // the writer's clock; "latest" is decided on this
	ReceivedAt time.Time // when the server stored it
}

// Incoming is a progress write from a device.
type Incoming struct {
	PositionMS int64
	DeviceID   string
	ClientTS   time.Time
	// LastSyncedAt is when this device last synced progress for the book
	// (zero if it never did).
	LastSyncedAt time.Time
}

// Outcome of a write.
type Outcome int

const (
	Apply    Outcome = iota // store the incoming write
	Ignore                  // older than what's stored; keep the stored record
	Conflict                // ask the user which position to keep (409)
)

func (o Outcome) String() string {
	return [...]string{"apply", "ignore", "conflict"}[o]
}

// MaxClockSkew: a client timestamp further in the future than this is
// clamped to the server clock, so one device with a wrong clock can't win
// every comparison forever.
const MaxClockSkew = 5 * time.Minute

// ClampClientTS bounds a client timestamp to now + MaxClockSkew.
func ClampClientTS(ts, now time.Time) time.Time {
	if ts.IsZero() || ts.After(now.Add(MaxClockSkew)) {
		return now
	}
	return ts
}

// Decide applies the sync rule:
//
//  1. Conflict when the stored record came from a *different* device, was
//     received after the incoming device last synced (so that device didn't
//     know about it — they were offline at the same time), and the positions
//     differ by more than the user's threshold.
//  2. Otherwise the latest client timestamp wins; ties go to the incoming write.
//
// Rule 1 comes first on purpose: an older write from a device that listened
// offline for hours must not be silently dropped.
func Decide(stored *Stored, in Incoming, threshold time.Duration) Outcome {
	if stored == nil {
		return Apply
	}
	diff := in.PositionMS - stored.PositionMS
	if diff < 0 {
		diff = -diff
	}
	if stored.DeviceID != in.DeviceID &&
		stored.ReceivedAt.After(in.LastSyncedAt) &&
		time.Duration(diff)*time.Millisecond > threshold {
		return Conflict
	}
	if in.ClientTS.Before(stored.ClientTS) {
		return Ignore
	}
	return Apply
}
