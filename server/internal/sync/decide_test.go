// SPDX-License-Identifier: AGPL-3.0-or-later

package sync

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	min := func(m int) int64 { return int64(m) * 60_000 }
	threshold := 3 * time.Minute
	// Stored: phone at 1:00:00, written by the phone at t0, received at t0.
	phone := &Stored{PositionMS: min(60), DeviceID: "phone", ClientTS: t0, ReceivedAt: t0}

	cases := []struct {
		name   string
		stored *Stored
		in     Incoming
		want   Outcome
	}{
		{"first write", nil, Incoming{PositionMS: 5, DeviceID: "phone", ClientTS: t0}, Apply},
		{"same device moves on", phone, Incoming{PositionMS: min(90), DeviceID: "phone", ClientTS: t0.Add(time.Minute)}, Apply},
		{"same device, stale write", phone, Incoming{PositionMS: min(10), DeviceID: "phone", ClientTS: t0.Add(-time.Minute)}, Ignore},
		{"same device, huge jump is never a conflict", phone, Incoming{PositionMS: min(500), DeviceID: "phone", ClientTS: t0.Add(time.Minute)}, Apply},
		{"other device knew the stored write, newer", phone,
			Incoming{PositionMS: min(120), DeviceID: "web", ClientTS: t0.Add(time.Hour), LastSyncedAt: t0.Add(time.Second)}, Apply},
		{"other device knew the stored write, older", phone,
			Incoming{PositionMS: min(120), DeviceID: "web", ClientTS: t0.Add(-time.Hour), LastSyncedAt: t0.Add(time.Second)}, Ignore},
		{"offline on both, small difference resolves silently", phone,
			Incoming{PositionMS: min(62), DeviceID: "web", ClientTS: t0.Add(time.Minute), LastSyncedAt: t0.Add(-time.Hour)}, Apply},
		{"offline on both, exactly the threshold is not a conflict", phone,
			Incoming{PositionMS: min(63), DeviceID: "web", ClientTS: t0.Add(time.Minute), LastSyncedAt: t0.Add(-time.Hour)}, Apply},
		{"offline on both, newer and far apart", phone,
			Incoming{PositionMS: min(240), DeviceID: "web", ClientTS: t0.Add(time.Minute), LastSyncedAt: t0.Add(-time.Hour)}, Conflict},
		{"offline on both, OLDER write far apart still asks (not silently dropped)", phone,
			Incoming{PositionMS: min(240), DeviceID: "web", ClientTS: t0.Add(-30 * time.Minute), LastSyncedAt: t0.Add(-time.Hour)}, Conflict},
		{"never synced this book counts as not knowing", phone,
			Incoming{PositionMS: 0, DeviceID: "watch", ClientTS: t0.Add(time.Minute)}, Conflict},
		{"backwards jump far apart also asks", phone,
			Incoming{PositionMS: min(10), DeviceID: "web", ClientTS: t0.Add(time.Minute), LastSyncedAt: t0.Add(-time.Hour)}, Conflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.stored, tc.in, threshold); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestClampClientTS(t *testing.T) {
	now := time.Now()
	if got := ClampClientTS(now.Add(time.Hour), now); !got.Equal(now) {
		t.Fatal("far-future timestamp not clamped")
	}
	past := now.Add(-time.Hour)
	if got := ClampClientTS(past, now); !got.Equal(past) {
		t.Fatal("past timestamp changed")
	}
	if got := ClampClientTS(time.Time{}, now); !got.Equal(now) {
		t.Fatal("zero timestamp not replaced")
	}
}
