// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/quip1/simple-apb/server/internal/store"
)

func TestAccountSelfService(t *testing.T) { forEachDialect(t, testAccountSelfService) }

func testAccountSelfService(t *testing.T, h *harness) {
	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)
	_, sam := h.approvedUser(adminTok, "sam")
	_, kim := h.approvedUser(adminTok, "kim")

	// Defaults.
	r := h.do(nil, "GET", "/me", nil, bearer(sam))
	expect(t, r, 200, "me")
	if r.body["totp_required"] != false || r.body["conflict_threshold_minutes"] != float64(3) ||
		r.body["listening_visible"] != true || len(r.body["prefs"].(map[string]any)) != 0 {
		t.Fatalf("me defaults: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/me", nil, bearer(adminTok)); r.body["totp_required"] != true {
		t.Fatalf("admin must have totp_required: %s", r.raw)
	}

	// Profile and preferences.
	r = h.do(nil, "PATCH", "/me", map[string]any{"display_name": "  Sam R  ", "email": "sam@example.com",
		"conflict_threshold_minutes": 10, "listening_visible": false, "prefs": map[string]any{"skip_back_s": 10}}, bearer(sam))
	expect(t, r, 200, "patch me")
	if r.body["display_name"] != "Sam R" || r.body["email"] != "sam@example.com" || r.body["conflict_threshold_minutes"] != float64(10) ||
		r.body["listening_visible"] != false || r.body["prefs"].(map[string]any)["skip_back_s"] != float64(10) {
		t.Fatalf("patched me: %s", r.raw)
	}
	for _, bad := range []map[string]any{
		{"conflict_threshold_minutes": 0}, {"conflict_threshold_minutes": 31}, {"email": "nope"},
		{"display_name": " "}, {"locale": "english"}, {"prefs": map[string]any{"x": strings.Repeat("a", 9000)}},
	} {
		expect(t, h.do(nil, "PATCH", "/me", bad, bearer(sam)), 400, "bad patch")
	}
	if r := h.do(nil, "PATCH", "/me", map[string]any{"email": "sam@example.com"}, bearer(kim)); r.status != 409 || str(r.body, "code") != "email_taken" {
		t.Fatalf("duplicate email: %d %s", r.status, r.raw)
	}
	expect(t, h.do(nil, "PATCH", "/me", map[string]any{"email": ""}, bearer(sam)), 200, "clear email")

	// Admins can't turn 2FA off.
	if r := h.do(nil, "POST", "/me/totp/disable", map[string]any{"password": adminPassword, "code": totpStep(secret, 1)}, bearer(adminTok)); r.status != 403 ||
		str(r.body, "code") != "totp_required" {
		t.Fatalf("admin disable: %d %s", r.status, r.raw)
	}

	// Sign out other devices: a second session of sam stops working.
	other := h.do(nil, "POST", "/auth/login", map[string]any{"username": "sam", "password": "marble-canyon-whistle", "device": device("web")}, nil)
	otherTok := str(other.body, "tokens", "access_token")
	expect(t, h.do(nil, "GET", "/me", nil, bearer(otherTok)), 200, "other session works")
	expect(t, h.do(nil, "DELETE", "/me/sessions", nil, bearer(sam)), 204, "revoke others")
	expect(t, h.do(nil, "GET", "/me", nil, bearer(otherTok)), 401, "other session revoked")
	expect(t, h.do(nil, "GET", "/me", nil, bearer(sam)), 200, "own session kept")

	// Password change.
	if r := h.do(nil, "POST", "/me/password", map[string]any{"current_password": "wrong-password-here", "new_password": "copper-lantern-meadow"}, bearer(sam)); r.status != 401 ||
		str(r.body, "code") != "wrong_password" {
		t.Fatalf("wrong current password: %d %s", r.status, r.raw)
	}
	if r := h.do(nil, "POST", "/me/password", map[string]any{"current_password": "marble-canyon-whistle", "new_password": "short"}, bearer(sam)); str(r.body, "code") != "password_too_short" {
		t.Fatalf("weak new password: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/me/password", map[string]any{"current_password": "marble-canyon-whistle", "new_password": "copper-lantern-meadow"}, bearer(sam)), 204, "change password")
	expect(t, h.do(nil, "POST", "/auth/login", map[string]any{"username": "sam", "password": "marble-canyon-whistle", "device": device("web")}, nil), 401, "old password")
	expect(t, h.do(nil, "POST", "/auth/login", map[string]any{"username": "sam", "password": "copper-lantern-meadow", "device": device("web")}, nil), 200, "new password")

	// 2FA: enroll, new recovery codes, turn off.
	expect(t, h.do(nil, "POST", "/me/recovery-codes", map[string]any{"code": "123456"}, bearer(sam)), 409, "codes without 2FA")
	r = h.do(nil, "POST", "/auth/totp/enroll", nil, bearer(sam))
	expect(t, r, 200, "enroll")
	samSecret := str(r.body, "secret")
	expect(t, h.do(nil, "POST", "/auth/totp/confirm", map[string]any{"code": totpStep(samSecret, -1)}, bearer(sam)), 200, "confirm")
	r = h.do(nil, "POST", "/me/recovery-codes", map[string]any{"code": totpStep(samSecret, 0)}, bearer(sam))
	expect(t, r, 200, "new recovery codes")
	if codes, _ := r.body["codes"].([]any); len(codes) != 10 {
		t.Fatalf("codes: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/me/recovery-codes", map[string]any{"code": totpStep(samSecret, 0)}, bearer(sam)), 401, "reused code")
	expect(t, h.do(nil, "POST", "/me/totp/disable", map[string]any{"password": "nope-nope-nope", "code": totpStep(samSecret, 1)}, bearer(sam)), 401, "disable wrong password")
	expect(t, h.do(nil, "POST", "/me/totp/disable", map[string]any{"password": "copper-lantern-meadow", "code": totpStep(samSecret, 1)}, bearer(sam)), 204, "disable")
	if r := h.do(nil, "GET", "/me", nil, bearer(sam)); r.body["totp_enabled"] != false {
		t.Fatalf("2FA still on: %s", r.raw)
	}
}

func TestMyStats(t *testing.T) { forEachDialect(t, testMyStats) }

func testMyStats(t *testing.T, h *harness) {
	ctx := context.Background()
	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)
	alexID, alex := h.approvedUser(adminTok, "alex")
	db := h.srv.App().DB
	bookID, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Stats Book", Authors: []string{"Ann Author"}},
		FolderPath: h.bookFolder("x/Stats Book"), Origin: "scan", Status: "ready", DurationMS: 3600_000, Checksum: "c-stats"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDistribution(ctx, db, bookID, "user", alexID, "", ""); err != nil {
		t.Fatal(err)
	}

	// Empty stats first.
	r := h.do(nil, "GET", "/me/stats?tz=UTC", nil, bearer(alex))
	expect(t, r, 200, "empty stats")
	if r.body["total_ms"] != float64(0) || len(r.body["years"].([]any)) != 0 {
		t.Fatalf("empty: %s", r.raw)
	}

	// 60 s of listening, a 10-minute seek (not listening), 30 s more, then finished.
	dev := uuid.NewString()
	t0 := time.Now().UTC().Add(-20 * time.Minute)
	put := func(sec int, posS int, finished bool) {
		body := map[string]any{"position_ms": posS * 1000, "device_id": dev, "client_ts": t0.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano), "speed": 1}
		if finished {
			body["finished"] = true
		}
		expect(t, h.do(nil, "PUT", "/me/progress/"+bookID, body, bearer(alex)), 200, fmt.Sprintf("progress at %ds", sec))
	}
	put(0, 0, false)
	put(30, 30, false)
	put(60, 60, false)
	put(75, 660, false) // seek
	put(105, 690, true)

	r = h.do(nil, "GET", "/me/stats?tz=UTC&year="+t0.Format("2006"), nil, bearer(alex))
	expect(t, r, 200, "stats")
	if r.body["total_ms"] != float64(90_000) || r.body["books_finished"] != float64(1) || r.body["active_days"] == float64(0) {
		t.Fatalf("stats: %s", r.raw)
	}
	books := r.body["books"].([]any)
	if len(books) != 1 || books[0].(map[string]any)["title"] != "Stats Book" || books[0].(map[string]any)["listened_ms"] != float64(90_000) {
		t.Fatalf("books: %s", r.raw)
	}
	authors := r.body["authors"].([]any)
	if len(authors) != 1 || authors[0].(map[string]any)["name"] != "Ann Author" {
		t.Fatalf("authors: %s", r.raw)
	}
	if fin := r.body["finished"].([]any); len(fin) != 1 || fin[0].(map[string]any)["finished_at"] == nil {
		t.Fatalf("finished: %s", r.raw)
	}
	expect(t, h.do(nil, "GET", "/me/stats?tz=Mars/Olympus", nil, bearer(alex)), 400, "bad tz")
	// Other users see none of it.
	_, kim := h.approvedUser(adminTok, "kim")
	if r := h.do(nil, "GET", "/me/stats", nil, bearer(kim)); r.body["total_ms"] != float64(0) {
		t.Fatalf("stats leaked: %s", r.raw)
	}
}

// bookFolder creates a database-only test book's folder in the library, so the
// background scan doesn't quarantine it as "files missing" mid-test.
func (h *harness) bookFolder(rel string) string {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Join(h.dir, "library", filepath.FromSlash(rel)), 0o750); err != nil {
		h.t.Fatal(err)
	}
	return rel
}
