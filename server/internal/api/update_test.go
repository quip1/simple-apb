// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/quip1/simple-apb/server/internal/version"
)

func TestServerUpdate(t *testing.T) { forEachDialect(t, testServerUpdate) }

// The admin Updates page (brief §6.21): the release check, and handing an
// update to the updater container with a backup first.
func testServerUpdate(t *testing.T, h *harness) {
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.0","html_url":"https://example.com/r","body":"Better","published_at":"2026-11-12T10:00:00Z"}`))
	}))
	defer release.Close()
	old := version.Version
	version.Version = "0.5.0"
	defer func() { version.Version = old }()

	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	_, sam := h.approvedUser(admin, "sam")
	app := h.srv.App()
	app.Updates.URL = release.URL

	expect(t, h.do(nil, "GET", "/admin/update", nil, bearer(sam)), 403, "not for users")
	r := h.do(nil, "POST", "/admin/update/check", nil, A)
	expect(t, r, 200, "check")
	if r.body["update_available"] != true || str(r.body, "latest", "version") != "0.6.0" || r.body["updater_present"] != false {
		t.Fatalf("status: %s", r.raw)
	}
	// No updater container: "Update now" can't work.
	expect(t, h.do(nil, "POST", "/admin/update", map[string]any{"version": "0.6.0"}, A), 409, "no updater")

	// With the updater's heartbeat, the request is written after a backup.
	if err := os.MkdirAll(app.UpdaterDir, 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(app.UpdaterDir, "heartbeat"), nil, 0o600)
	expect(t, h.do(nil, "POST", "/admin/update", map[string]any{"version": "0.4.0"}, A), 400, "older version")
	r = h.do(nil, "POST", "/admin/update", map[string]any{"version": "0.6.0"}, A)
	expect(t, r, 202, "update")
	if str(r.body, "last_update", "status") != "requested" || str(r.body, "last_update", "to_version") != "0.6.0" {
		t.Fatalf("after request: %s", r.raw)
	}
	if _, err := os.Stat(filepath.Join(app.UpdaterDir, "request.json")); err != nil {
		t.Fatal("no request for the updater")
	}
	if files, _ := app.Backup.List(); len(files) == 0 {
		t.Fatal("no backup before the update")
	}
	expect(t, h.do(nil, "POST", "/admin/update", map[string]any{"version": "0.6.0"}, A), 409, "already under way")
}
