// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
)

func TestRebind(t *testing.T) {
	got := rebind(Postgres, `SELECT ? , '?' FROM x WHERE a = ? AND b = '??'`)
	want := `SELECT $1 , '?' FROM x WHERE a = $2 AND b = '??'`
	if got != want {
		t.Fatalf("got %s", got)
	}
	if rebind(SQLite, "a = ?") != "a = ?" {
		t.Fatal("sqlite must not rebind")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, db *DB) {
		ctx := context.Background()
		type smtp struct {
			Host string `json:"host"`
			Pass string `json:"pass"`
		}
		if err := SetSetting(ctx, db, plainEnc{}, "smtp", smtp{"mail", "pw"}, true); err != nil {
			t.Fatal(err)
		}
		if err := SetSetting(ctx, db, plainEnc{}, "smtp", smtp{"mail2", "pw2"}, true); err != nil {
			t.Fatal(err)
		}
		var got smtp
		if err := GetSetting(ctx, db, plainEnc{}, "smtp", &got); err != nil || got.Host != "mail2" || got.Pass != "pw2" {
			t.Fatalf("got %+v %v", got, err)
		}
		var raw string
		_ = db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'smtp'`).Scan(&raw)
		if raw == "" || contains(raw, "pw2") {
			t.Fatalf("secret stored in clear: %s", raw)
		}
		if err := GetSetting(ctx, db, plainEnc{}, "missing", &got); !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

func TestUsersSessionsGroups(t *testing.T) {
	forEachDialect(t, func(t *testing.T, db *DB) {
		ctx := context.Background()
		id, err := CreateUser(ctx, db, NewUser{Username: "Alice", PasswordHash: "h", Status: "pending", Role: "user"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CreateUser(ctx, db, NewUser{Username: "alice", Status: "pending", Role: "user"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("want ErrConflict, got %v", err)
		}
		u, err := GetUserByUsername(ctx, db, "ALICE")
		if err != nil || u.ID != id || u.DisplayName != "Alice" || !u.ListeningVisible || u.ConflictThresholdMS != 180000 {
			t.Fatalf("got %+v %v", u, err)
		}
		if err := SetUserStatus(ctx, db, id, "active", "approved", nil); err != nil {
			t.Fatal(err)
		}

		pub, err := EnsurePublicGroup(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := EnsurePublicGroup(ctx, db)
		if again != pub {
			t.Fatal("EnsurePublicGroup not idempotent")
		}
		if err := AddGroupMember(ctx, db, pub, id); err != nil {
			t.Fatal(err)
		}
		if err := AddGroupMember(ctx, db, pub, id); err != nil {
			t.Fatal("AddGroupMember must be idempotent:", err)
		}
		if err := DeleteGroup(ctx, db, pub); !errors.Is(err, ErrNotFound) {
			t.Fatal("builtin group must not be deletable")
		}
		gs, _ := UserGroupIDs(ctx, db, id)
		if len(gs) != 1 || gs[0] != pub {
			t.Fatalf("groups %v", gs)
		}

		s := &Session{UserID: id, DeviceID: NewID(), DeviceName: "Pixel", Platform: "android", AppVersion: "1", RefreshTokenHash: "r1", ExpiresAt: NowMS() + 1e6}
		if err := CreateSession(ctx, db, s); err != nil {
			t.Fatal(err)
		}
		ok, err := RotateRefresh(ctx, db, s.ID, "r1", "r2", NowMS()+1e6, "")
		if err != nil || !ok {
			t.Fatalf("rotate: %v %v", ok, err)
		}
		if ok, _ := RotateRefresh(ctx, db, s.ID, "r1", "r3", NowMS()+1e6, ""); ok {
			t.Fatal("rotating with a stale hash must fail")
		}
		found, err := FindSessionByRefresh(ctx, db, "r1")
		if err != nil || found.PrevRefreshHash != "r1" || found.RefreshTokenHash != "r2" {
			t.Fatalf("find by previous hash: %+v %v", found, err)
		}
		list, _ := ListUserSessions(ctx, db, id)
		if len(list) != 1 {
			t.Fatalf("sessions %d", len(list))
		}
		if err := RevokeSession(ctx, db, s.ID, "someone-else"); !errors.Is(err, ErrNotFound) && err != nil {
			t.Fatal(err)
		}

		if err := Audit(ctx, db, AuditEntry{ActorID: id, Action: "test", Details: map[string]any{"k": 1}}); err != nil {
			t.Fatal(err)
		}
	})
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
