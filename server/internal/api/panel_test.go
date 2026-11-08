// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"

	"github.com/quip1/simple-apb/server/internal/store"
)

func TestAdminPanel(t *testing.T) { forEachDialect(t, testAdminPanel) }

func testAdminPanel(t *testing.T, h *harness) {
	ctx := context.Background()
	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	samID, sam := h.approvedUser(admin, "sam")

	// Dashboard and growth are admin-only.
	expect(t, h.do(nil, "GET", "/admin/dashboard", nil, bearer(sam)), 403, "dashboard as user")
	r := h.do(nil, "GET", "/admin/dashboard", nil, A)
	expect(t, r, 200, "dashboard")
	health := r.body["health"].(map[string]any)
	if r.body["version"] == "" || health["db_bytes"].(float64) <= 0 || len(health["disks"].([]any)) == 0 || health["zfs"] != "unknown" {
		t.Fatalf("dashboard: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/admin/growth?months=6&tz=UTC", nil, A); r.status != 200 || len(r.list) != 6 {
		t.Fatalf("growth: %d %s", r.status, r.raw)
	}

	// User tags: a tag that requires 2FA and allows sharing.
	r = h.do(nil, "POST", "/admin/user-tags", map[string]any{"name": "Trusted", "color": "#3366ff", "require_totp": true,
		"capability_overrides": map[string]any{"can_share": true}}, A)
	expect(t, r, 201, "create tag")
	tagID := str(r.body, "id")
	expect(t, h.do(nil, "POST", "/admin/user-tags", map[string]any{"name": "trusted"}, A), 409, "duplicate tag")
	expect(t, h.do(nil, "POST", "/admin/user-tags", map[string]any{"name": "X", "color": "blue"}, A), 400, "bad color")
	expect(t, h.do(nil, "PUT", "/admin/users/"+samID+"/tags/"+tagID, nil, A), 204, "assign tag")
	r = h.do(nil, "GET", "/admin/users/"+samID, nil, A)
	expect(t, r, 200, "user detail")
	if tags := r.body["tags"].([]any); len(tags) != 1 || r.body["effective"].(map[string]any)["can_share"] != true {
		t.Fatalf("detail after tag: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/me", nil, bearer(sam)); r.body["totp_required"] != true {
		t.Fatalf("tag must require 2FA: %s", r.raw)
	}
	expect(t, h.do(nil, "PATCH", "/admin/user-tags/"+tagID, map[string]any{"require_totp": false}, A), 200, "edit tag")

	// Explicit permission override beats the tag.
	expect(t, h.do(nil, "PUT", "/admin/users/"+samID+"/permissions", map[string]any{"can_share": false, "can_upload": false}, A), 204, "permissions")
	r = h.do(nil, "GET", "/admin/users/"+samID, nil, A)
	if eff := r.body["effective"].(map[string]any); eff["can_share"] != false || eff["can_upload"] != false || r.body["permissions"].(map[string]any)["can_review"] != nil {
		t.Fatalf("overrides: %s", r.raw)
	}
	expect(t, h.do(nil, "DELETE", "/admin/users/"+samID+"/tags/"+tagID, nil, A), 204, "unassign")
	if r := h.do(nil, "GET", "/admin/user-tags", nil, A); r.list[0].(map[string]any)["members"] != float64(0) {
		t.Fatalf("members after unassign: %s", r.raw)
	}

	// Groups.
	r = h.do(nil, "POST", "/admin/groups", map[string]any{"name": "Friends"}, A)
	groupID := str(r.body, "id")
	r = h.do(nil, "PATCH", "/admin/groups/"+groupID, map[string]any{"name": "Close friends", "high_priority": true}, A)
	if r.status != 200 || r.body["name"] != "Close friends" || r.body["high_priority"] != true {
		t.Fatalf("group patch: %s", r.raw)
	}
	expect(t, h.do(nil, "PUT", "/admin/groups/"+groupID+"/members/"+samID, nil, A), 204, "add member")
	if r := h.do(nil, "GET", "/admin/groups/"+groupID+"/members", nil, A); len(r.list) != 1 || r.list[0].(map[string]any)["username"] != "sam" {
		t.Fatalf("members: %s", r.raw)
	}
	for _, g := range h.do(nil, "GET", "/admin/groups", nil, A).list {
		if gm := g.(map[string]any); gm["builtin"] == true {
			expect(t, h.do(nil, "PATCH", "/admin/groups/"+gm["id"].(string), map[string]any{"name": "Everyone"}, A), 403, "rename Public")
		}
	}

	// Takedown hides a distributed book; lifting shows it again.
	db := h.srv.App().DB
	bookID, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Hot Potato"}, FolderPath: h.bookFolder("x/Hot Potato"),
		Origin: "scan", Status: "ready", DurationMS: 60_000, Checksum: "c-hot"})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, h.do(nil, "POST", "/admin/distributions", map[string]any{"book_id": bookID, "target_type": "user", "target_id": samID}, A), 201, "distribute")
	visible := func() int { return len(h.do(nil, "GET", "/library", nil, bearer(sam)).body["items"].([]any)) }
	if visible() != 1 {
		t.Fatal("book should be visible")
	}
	// The master-store list shows who each book is shared with.
	if r := h.do(nil, "GET", "/admin/books?q=Hot", nil, A); len(r.body["items"].([]any)) != 1 ||
		len(r.body["items"].([]any)[0].(map[string]any)["distributions"].([]any)) != 1 {
		t.Fatalf("master list distributions: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/admin/books/"+bookID+"/takedown", map[string]any{"reason": " "}, A), 400, "takedown without reason")
	expect(t, h.do(nil, "POST", "/admin/books/"+bookID+"/takedown", map[string]any{"reason": "copyright claim"}, A), 204, "takedown")
	expect(t, h.do(nil, "POST", "/admin/books/"+bookID+"/takedown", map[string]any{"reason": "again"}, A), 409, "double takedown")
	if visible() != 0 {
		t.Fatal("taken-down book still visible")
	}
	if r := h.do(nil, "GET", "/admin/books/"+bookID, nil, A); str(r.body, "takedown", "reason") != "copyright claim" {
		t.Fatalf("master view takedown: %s", r.raw)
	}
	expect(t, h.do(nil, "DELETE", "/admin/books/"+bookID+"/takedown", nil, A), 204, "lift")
	if visible() != 1 {
		t.Fatal("book should be visible after lift")
	}

	// Settings: secrets never come back; bad base URL rejected; default quota applies to new accounts.
	r = h.do(nil, "PATCH", "/admin/settings", map[string]any{
		"instance":  map[string]any{"name": "Renamed Shelf", "copyright_contact": "dmca@example.com"},
		"bandwidth": map[string]any{"egress_mbps": 40},
		"quotas":    map[string]any{"default_quota_bytes": 1 << 30},
		"smtp":      map[string]any{"host": "mail.example.com", "port": 587, "tls": "starttls", "from": "books@example.com", "password": "hunter2-secret"},
	}, A)
	expect(t, r, 200, "patch settings")
	if strings.Contains(r.raw, "hunter2-secret") || str(r.body, "smtp", "host") != "mail.example.com" || r.body["smtp"].(map[string]any)["password_set"] != true {
		t.Fatalf("settings: %s", r.raw)
	}
	if h.srv.App().Shaper.Snapshot().RateBps != 40*1000*1000/8 {
		t.Fatalf("shaper rate not updated: %d", h.srv.App().Shaper.Snapshot().RateBps)
	}
	expect(t, h.do(nil, "PATCH", "/admin/settings", map[string]any{"instance": map[string]any{"base_url": "ftp://nope"}}, A), 400, "bad base url")
	if r := h.do(nil, "GET", "/instance", nil, nil); r.body["name"] != "Renamed Shelf" || r.body["copyright_contact"] != "dmca@example.com" {
		t.Fatalf("instance: %s", r.raw)
	}
	kimID, _ := h.approvedUser(admin, "kim")
	if r := h.do(nil, "GET", "/admin/users/"+kimID, nil, A); r.body["quota_bytes"] != float64(1<<30) {
		t.Fatalf("default quota: %s", r.raw)
	}

	// Password reset link: works once.
	r = h.do(nil, "POST", "/admin/users/"+samID+"/reset-link", nil, A)
	expect(t, r, 200, "reset link")
	link := str(r.body, "url")
	token := link[strings.Index(link, "#")+1:]
	if !strings.HasPrefix(link, "https://books.example.com/reset-password#") {
		t.Fatalf("link: %s", link)
	}
	expect(t, h.do(nil, "POST", "/auth/password-reset", map[string]any{"token": token, "new_password": "fresh-meadow-lantern"}, nil), 204, "reset")
	expect(t, h.do(nil, "POST", "/auth/password-reset", map[string]any{"token": token, "new_password": "other-meadow-lantern"}, nil), 400, "reuse")
	expect(t, h.do(nil, "GET", "/me", nil, bearer(sam)), 401, "sessions revoked by reset")
	expect(t, h.do(nil, "POST", "/auth/login", map[string]any{"username": "sam", "password": "fresh-meadow-lantern", "device": device("web")}, nil), 200, "login after reset")

	// Audit log: filter by prefix, CSV export.
	r = h.do(nil, "GET", "/admin/audit?action=user_tag.&limit=1", nil, A)
	expect(t, r, 200, "audit")
	items := r.body["items"].([]any)
	if len(items) != 1 || !strings.HasPrefix(items[0].(map[string]any)["action"].(string), "user_tag.") || r.body["next_cursor"] == nil {
		t.Fatalf("audit page: %s", r.raw)
	}
	if items[0].(map[string]any)["actor_name"] != "admin" {
		t.Fatalf("actor name: %s", r.raw)
	}
	r = h.do(nil, "GET", "/admin/audit/export?object_type=book", nil, A)
	expect(t, r, 200, "csv")
	if !strings.HasPrefix(r.raw, "at,actor_id,actor,action") || !strings.Contains(r.raw, "book.takedown") || !strings.HasPrefix(r.header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %s", r.raw)
	}
}
