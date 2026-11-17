// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/quip1/simple-apb/server/internal/media"
)

func TestAutoShare(t *testing.T) { forEachDialect(t, testAutoShare) }

// ADR-0053: books found in the library folder are shared with the group the
// admin chose; with none chosen they stay the admin's until shared.
func testAutoShare(t *testing.T, h *harness) {
	tools := media.DefaultTools()
	if !tools.Available() {
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	samID, sam := h.approvedUser(admin, "sam")
	_, eve := h.approvedUser(admin, "eve")
	lib := filepath.Join(h.dir, "library")
	addBook := func(title string, hz int) {
		p := filepath.Join(lib, "Ann Author", title, "book.m4b")
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		if err := tools.MakeM4B(ctx, p, media.FixtureBook{Title: title, Author: "Ann Author", Seconds: 3, Frequency: hz}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.srv.App().Lib.Scan(ctx, farFuture); err != nil {
			t.Fatal(err)
		}
	}
	count := func(tok string) int { return len(h.do(nil, "GET", "/library", nil, bearer(tok)).body["items"].([]any)) }

	// Off by default: nobody sees a new book.
	r := h.do(nil, "GET", "/admin/settings", nil, A)
	expect(t, r, 200, "settings")
	if l := r.body["library"].(map[string]any); l["auto_share_group_id"] != nil {
		t.Fatalf("auto-share on by default: %s", r.raw)
	}
	addBook("First", 500)
	if count(sam) != 0 {
		t.Fatal("a scanned book was shared without the setting")
	}

	g := h.do(nil, "POST", "/admin/groups", map[string]any{"name": "Family"}, A)
	groupID := str(g.body, "id")
	expect(t, h.do(nil, "PUT", "/admin/groups/"+groupID+"/members/"+samID, nil, A), 204, "add sam")

	patch := func(id string, tok string) resp {
		return h.do(nil, "PATCH", "/admin/settings", map[string]any{"library": map[string]any{"auto_share_group_id": id}}, bearer(tok))
	}
	expect(t, patch(groupID, sam), 403, "only admins")
	expect(t, patch("not-a-group", admin), 400, "bad id")
	expect(t, patch("00000000-0000-4000-8000-000000000000", admin), 400, "unknown group")
	r = patch(groupID, admin)
	expect(t, r, 200, "turn on")
	if l := r.body["library"].(map[string]any); str(l, "auto_share_group_id") != groupID || str(l, "auto_share_group_name") != "Family" {
		t.Fatalf("settings: %s", r.raw)
	}

	// Only new books are shared, only with the group.
	addBook("Second", 600)
	if count(sam) != 1 || count(eve) != 0 {
		t.Fatalf("after import: sam %d, eve %d", count(sam), count(eve))
	}

	// Off again.
	r = patch("", admin)
	expect(t, r, 200, "turn off")
	if l := r.body["library"].(map[string]any); l["auto_share_group_id"] != nil {
		t.Fatalf("still on: %s", r.raw)
	}
	addBook("Third", 700)
	if count(sam) != 1 {
		t.Fatalf("shared after turning off: %d", count(sam))
	}
}
