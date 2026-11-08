// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/quip1/simple-apb/server/internal/store"
)

// deviceLogin logs a user in from a specific device and returns the token.
func (h *harness) deviceLogin(user, deviceID, platform string) string {
	h.t.Helper()
	r := h.do(nil, "POST", "/auth/login", map[string]any{"username": user, "password": "marble-canyon-whistle",
		"device": map[string]any{"device_id": deviceID, "device_name": user + "'s " + platform, "platform": platform, "app_version": "1"}}, nil)
	expect(h.t, r, 200, "device login")
	return str(r.body, "tokens", "access_token")
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func TestSyncFlow(t *testing.T) { forEachDialect(t, testSyncFlow) }

func testSyncFlow(t *testing.T, h *harness) {
	ctx := context.Background()
	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)
	aliceID, _ := h.approvedUser(adminTok, "alice")
	db := h.srv.App().DB

	// A 10-hour book distributed to alice (no audio needed for sync).
	bookID, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Long Book", Authors: []string{"A"}},
		FolderPath: "x/Long Book", Origin: "scan", Status: "ready", DurationMS: 10 * 3600_000, Checksum: "c-long"})
	if err != nil {
		t.Fatal(err)
	}
	distID, _ := store.CreateDistribution(ctx, db, bookID, "user", aliceID, "", "")
	_ = store.RecordBookChange(ctx, db, bookID)

	phoneID, webID := uuid.NewString(), uuid.NewString()
	phone := h.deviceLogin("alice", phoneID, "android")
	web := h.deviceLogin("alice", webID, "web")

	// Full snapshot.
	r := h.do(nil, "GET", "/me/sync", nil, bearer(phone))
	expect(t, r, 200, "full sync")
	if r.body["full"] != true || len(r.body["library"].([]any)) != 1 {
		t.Fatalf("full snapshot: %s", r.raw)
	}
	cursor0 := str(r.body, "cursor")

	put := func(tok, device string, posMin int, clientTS time.Time, lastSynced *time.Time, extra map[string]any) resp {
		body := map[string]any{"position_ms": posMin * 60_000, "device_id": device, "client_ts": ts(clientTS), "speed": 1.25}
		if lastSynced != nil {
			body["last_synced_at"] = ts(*lastSynced)
		}
		for k, v := range extra {
			body[k] = v
		}
		return h.do(nil, "PUT", "/me/progress/"+bookID, body, bearer(tok))
	}
	t0 := time.Now().Add(-time.Hour)
	expect(t, h.do(nil, "GET", "/me/progress/"+bookID, nil, bearer(phone)), 404, "no progress yet")

	// 1. Phone listens to 1:00:00.
	if r := put(phone, phoneID, 60, t0, nil, nil); r.status != 200 || r.body["applied"] != true {
		t.Fatalf("first write: %s", r.raw)
	}
	// 2. Web was offline the whole time and reports 1:02:00 → small difference, silently latest.
	before := t0.Add(-2 * time.Hour)
	if r := put(web, webID, 62, t0.Add(time.Minute), &before, nil); r.status != 200 || r.body["applied"] != true {
		t.Fatalf("small diff should apply silently: %s", r.raw)
	}
	// 3. Phone, also offline since before, reports 4:00:00 → 3 h apart from another device's write → 409.
	r = put(phone, phoneID, 240, t0.Add(2*time.Minute), &before, nil)
	expect(t, r, 409, "conflict")
	conflictID := str(r.body, "conflict_id")
	if str(r.body, "code") != "progress_conflict" || r.body["server"].(map[string]any)["position_ms"].(float64) != 62*60_000 ||
		r.body["incoming"].(map[string]any)["position_ms"].(float64) != 240*60_000 {
		t.Fatalf("conflict body: %s", r.raw)
	}
	if !strings.Contains(r.raw, "alice's web") {
		t.Fatalf("conflict should name the other device: %s", r.raw)
	}
	// Nothing changed while unresolved.
	if g := h.do(nil, "GET", "/me/progress/"+bookID, nil, bearer(phone)); g.body["position_ms"].(float64) != 62*60_000 {
		t.Fatalf("progress changed by a conflicting write: %s", g.raw)
	}
	// 4. The user picks "jump to 4:00:00": final, even with an older timestamp.
	r = put(phone, phoneID, 240, t0, &before, map[string]any{"resolution": conflictID})
	if r.status != 200 || r.body["applied"] != true {
		t.Fatalf("resolution: %s", r.raw)
	}
	if r := put(phone, phoneID, 240, t0, &before, map[string]any{"resolution": conflictID}); r.status != 400 {
		t.Fatalf("reusing a resolution should fail: %d %s", r.status, r.raw)
	}
	// 5. A stale write from a device that knew the latest state is ignored.
	now := time.Now()
	if r := put(web, webID, 30, t0.Add(-5*time.Minute), &now, nil); r.status != 200 || r.body["applied"] != false {
		t.Fatalf("stale write should be ignored: %s", r.raw)
	}
	g := h.do(nil, "GET", "/me/progress/"+bookID, nil, bearer(web))
	if g.body["position_ms"].(float64) != 240*60_000 || g.body["device_id"] != phoneID || g.body["speed"].(float64) != 1.25 {
		t.Fatalf("final progress: %s", g.raw)
	}
	if r := h.do(nil, "PUT", "/me/progress/"+bookID, map[string]any{"position_ms": 1, "device_id": phoneID, "client_ts": ts(now), "speed": 9}, bearer(phone)); r.status != 400 {
		t.Fatalf("bad speed accepted: %d", r.status)
	}
	var events int
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM progress_events WHERE book_id = ?`, bookID).Scan(&events)
	var resolved int
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM progress_conflicts WHERE resolved_at IS NOT NULL AND chosen_position = ?`, 240*60_000).Scan(&resolved)
	if events != 3 || resolved != 1 {
		t.Fatalf("events %d, resolved conflicts %d", events, resolved)
	}

	// Delta since the snapshot: progress shows up once, in its latest state.
	d := h.do(nil, "GET", "/me/sync?since="+cursor0, nil, bearer(web))
	expect(t, d, 200, "delta")
	if p := d.body["progress"].([]any); len(p) != 1 || p[0].(map[string]any)["position_ms"].(float64) != 240*60_000 {
		t.Fatalf("delta progress: %s", d.raw)
	}
	cursor1 := str(d.body, "cursor")
	if d := h.do(nil, "GET", "/me/sync?since="+cursor1, nil, bearer(web)); len(d.body["progress"].([]any)) != 0 || d.body["has_more"] != false {
		t.Fatalf("empty delta: %s", d.raw)
	}

	// Bookmarks: idempotent create, LWW edits, tombstones.
	bmID := uuid.NewString()
	bm := map[string]any{"id": bmID, "book_id": bookID, "position_ms": 3_600_000, "note": "great line", "client_ts": ts(now)}
	expect(t, h.do(nil, "POST", "/me/bookmarks", bm, bearer(phone)), 201, "bookmark")
	expect(t, h.do(nil, "POST", "/me/bookmarks", bm, bearer(phone)), 201, "bookmark again (idempotent)")
	if l := h.do(nil, "GET", "/me/bookmarks?book_id="+bookID, nil, bearer(web)); len(l.list) != 1 {
		t.Fatalf("bookmarks: %s", l.raw)
	}
	r = h.do(nil, "PATCH", "/me/bookmarks/"+bmID, map[string]any{"note": "older edit", "client_ts": ts(now.Add(-time.Hour))}, bearer(web))
	if str(r.body, "note") != "great line" {
		t.Fatalf("older edit won: %s", r.raw)
	}
	r = h.do(nil, "PATCH", "/me/bookmarks/"+bmID, map[string]any{"note": "newer edit", "client_ts": ts(now.Add(time.Second))}, bearer(web))
	if str(r.body, "note") != "newer edit" {
		t.Fatalf("newer edit lost: %s", r.raw)
	}

	// Collections.
	c := h.do(nil, "POST", "/me/collections", map[string]any{"name": "Road trip"}, bearer(phone))
	expect(t, c, 201, "collection")
	colID := str(c.body, "id")
	expect(t, h.do(nil, "PUT", "/me/collections/"+colID+"/books/"+bookID, nil, bearer(phone)), 204, "add to collection")
	if l := h.do(nil, "GET", "/me/collections", nil, bearer(web)); len(l.list) != 1 || len(l.list[0].(map[string]any)["book_ids"].([]any)) != 1 {
		t.Fatalf("collections: %s", l.raw)
	}

	// Live hint over /ws when something changes.
	wsURL := "ws" + strings.TrimPrefix(h.ts.URL, "http") + "/ws"
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(wctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + web}}})
	if err != nil {
		t.Fatal("ws dial:", err)
	}
	defer func() { _ = conn.CloseNow() }()
	readMsg := func() wsMessage {
		_, b, err := conn.Read(wctx)
		if err != nil {
			t.Fatal("ws read:", err)
		}
		var m wsMessage
		_ = json.Unmarshal(b, &m)
		return m
	}
	if m := readMsg(); m.Type != "hello" {
		t.Fatalf("first ws message %+v", m)
	}
	expect(t, h.do(nil, "DELETE", "/me/bookmarks/"+bmID, nil, bearer(phone)), 204, "delete bookmark")
	if m := readMsg(); m.Type != "changed" || m.Seq == 0 {
		t.Fatalf("change hint %+v", m)
	}
	if _, _, err := websocket.Dial(wctx, wsURL, nil); err == nil {
		t.Fatal("unauthenticated ws accepted")
	}

	// Deletions and revoked visibility arrive as tombstones.
	expect(t, h.do(nil, "DELETE", "/me/collections/"+colID, nil, bearer(phone)), 204, "delete collection")
	expect(t, h.do(nil, "DELETE", "/admin/distributions/"+distID, nil, bearer(adminTok)), 204, "revoke")
	d = h.do(nil, "GET", "/me/sync?since="+cursor1, nil, bearer(web))
	kinds := map[string]bool{}
	for _, tb := range d.body["tombstones"].([]any) {
		kinds[tb.(map[string]any)["entity"].(string)] = true
	}
	if !kinds["bookmark"] || !kinds["collection"] || !kinds["library_entry"] {
		t.Fatalf("tombstones: %s", d.raw)
	}

	// Expired cursors get 410; garbage cursors 400.
	if _, err := db.Exec(ctx, `UPDATE sync_changes SET at = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PruneChanges(ctx, db, h.srv.App().Auth.Enc, 1); err != nil {
		t.Fatal(err)
	}
	expect(t, h.do(nil, "GET", "/me/sync?since="+cursor0, nil, bearer(web)), 410, "expired cursor")
	expect(t, h.do(nil, "GET", "/me/sync?since=bogus!", nil, bearer(web)), 400, "bad cursor")
}
