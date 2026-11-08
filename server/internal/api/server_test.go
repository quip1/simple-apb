// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/setup"
	"github.com/quip1/simple-apb/server/internal/store"
)

// ── test harness ──

type harness struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
	dir string
	// db is the wizard's database choice: {"dialect":"sqlite"} or a Postgres DSN.
	db map[string]any
}

// forEachDialect runs fn on SQLite, and on Postgres when
// SIMPLEAPB_TEST_POSTGRES_DSN is set. Each Postgres run gets its own schema so
// it can't collide with other packages' tests on the same database.
func forEachDialect(t *testing.T, fn func(t *testing.T, h *harness)) {
	t.Run("sqlite", func(t *testing.T) { fn(t, newHarness(t)) })
	t.Run("postgres", func(t *testing.T) {
		base := os.Getenv("SIMPLEAPB_TEST_POSTGRES_DSN")
		if base == "" {
			t.Skip("SIMPLEAPB_TEST_POSTGRES_DSN not set")
		}
		ctx := context.Background()
		admin, err := store.Open(ctx, store.Postgres, base)
		if err != nil {
			t.Fatal(err)
		}
		schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
			_ = admin.Close()
		})
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		h := newHarness(t)
		h.db = map[string]any{"dialect": "postgres", "dsn": base + sep + "search_path=" + schema}
		fn(t, h)
	})
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{Config: filepath.Join(dir, "config"), Library: filepath.Join(dir, "library"),
		Cache: filepath.Join(dir, "cache"), Backups: filepath.Join(dir, "backups")}
	for _, p := range []string{paths.Config, paths.Library} {
		if err := os.MkdirAll(p, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	secrets, err := config.LoadOrCreateSecrets(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	bg, cancel := context.WithCancel(context.Background())
	srv := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Secrets: secrets,
		Setup:      &setup.Service{Paths: paths, Secrets: secrets, Token: "test-setup-token"},
		AppOptions: AppOptions{Paths: paths, Tools: media.DefaultTools()},
		BaseCtx:    bg,
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() {
		ts.Close()
		cancel()
		if a := srv.App(); a != nil {
			a.Jobs.Wait()
			_ = a.DB.Close()
		}
	})
	return &harness{t: t, srv: srv, ts: ts, dir: dir, db: map[string]any{"dialect": "sqlite"}}
}

type resp struct {
	status int
	body   map[string]any
	list   []any
	header http.Header
	raw    string
}

func (h *harness) do(c *http.Client, method, path string, body any, headers map[string]string) resp {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.ts.URL+"/api/v1"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &out.list)
	} else {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func expect(t *testing.T, r resp, status int, what string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: status %d, want %d; body %s", what, r.status, status, r.raw)
	}
}

func device(platform string) map[string]any {
	return map[string]any{"device_id": uuid.NewString(), "device_name": "test " + platform, "platform": platform, "app_version": "0.1.0"}
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, _ := cur.(map[string]any)
		cur = mm[p]
	}
	s, _ := cur.(string)
	return s
}

const adminPassword = "tangerine-orbit-quietly"

// setupInstance runs the wizard and returns the admin's TOTP secret.
func (h *harness) setupInstance() string {
	h.t.Helper()
	tok := map[string]string{"X-Setup-Token": "test-setup-token"}
	r := h.do(nil, "POST", "/setup/totp", map[string]any{"username": "admin"}, tok)
	expect(h.t, r, 200, "setup totp")
	secret := str(r.body, "secret")
	// Codes are single-use per 30 s step: the wizard uses the previous step,
	// logins the current one, re-authentication the next (all within ±1 skew).
	code := totpStep(secret, -1)
	r = h.do(nil, "POST", "/setup/complete", map[string]any{
		"admin":         map[string]any{"username": "admin", "password": adminPassword, "totp_secret": secret, "totp_code": code},
		"database":      h.db,
		"library_path":  filepath.Join(h.dir, "library"),
		"base_url":      "https://books.example.com/",
		"instance_name": "Test Shelf",
	}, tok)
	expect(h.t, r, 200, "setup complete")
	if codes, _ := r.body["recovery_codes"].([]any); len(codes) != 10 {
		h.t.Fatalf("want 10 recovery codes, got %v", r.body["recovery_codes"])
	}
	// Generous limits for flow tests; TestRateLimits checks the real ones.
	h.srv.App().Auth.Limits = &auth.Limits{
		LoginIP: auth.NewLimiter(1000, time.Minute), LoginAccount: auth.NewLimiter(1000, time.Minute),
		SignupIP: auth.NewLimiter(1000, time.Minute), ResetIP: auth.NewLimiter(1000, time.Minute),
	}
	return secret
}

// adminLogin does password + TOTP and returns an access token.
func (h *harness) adminLogin(secret string) string {
	h.t.Helper()
	r := h.do(nil, "POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword, "device": device("android")}, nil)
	expect(h.t, r, 200, "admin login")
	if str(r.body, "status") != "totp_required" {
		h.t.Fatalf("admin login must require TOTP: %s", r.raw)
	}
	challenge := str(r.body, "totp_challenge")
	code := totpStep(secret, 0)
	r = h.do(nil, "POST", "/auth/totp/verify", map[string]any{"totp_challenge": challenge, "code": code}, nil)
	expect(h.t, r, 200, "totp verify")
	// The same code can't be used twice (DECISIONS Q5).
	r2 := h.do(nil, "POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword, "device": device("android")}, nil)
	if rr := h.do(nil, "POST", "/auth/totp/verify", map[string]any{"totp_challenge": str(r2.body, "totp_challenge"), "code": code}, nil); rr.status != 401 {
		h.t.Fatalf("reused TOTP code accepted: %d", rr.status)
	}
	return str(r.body, "tokens", "access_token")
}

// ── tests ──

func TestSetupMode(t *testing.T) {
	h := newHarness(t)
	r := h.do(nil, "GET", "/health", nil, nil)
	expect(t, r, 200, "health")
	if r.header.Get("X-Server-Version") == "" {
		t.Fatal("missing X-Server-Version")
	}
	r = h.do(nil, "GET", "/instance", nil, nil)
	if r.body["setup_required"] != true {
		t.Fatalf("instance should report setup_required: %s", r.raw)
	}
	expect(t, h.do(nil, "GET", "/library", nil, nil), 503, "library before setup")
	expect(t, h.do(nil, "POST", "/setup/totp", map[string]any{"username": "a"}, map[string]string{"X-Setup-Token": "wrong"}), 401, "bad token")
	r = h.do(nil, "POST", "/setup/library/test", map[string]any{"path": filepath.Join(h.dir, "library")}, map[string]string{"X-Setup-Token": "test-setup-token"})
	if r.body["writable"] != true || r.body["empty"] != true {
		t.Fatalf("library check: %s", r.raw)
	}

	// Wrong TOTP code is rejected and nothing is created.
	tok := map[string]string{"X-Setup-Token": "test-setup-token"}
	bad := h.do(nil, "POST", "/setup/complete", map[string]any{
		"admin":    map[string]any{"username": "admin", "password": adminPassword, "totp_secret": "JBSWY3DPEHPK3PXP", "totp_code": "000000"},
		"database": map[string]any{"dialect": "sqlite"}, "library_path": filepath.Join(h.dir, "library"),
		"base_url": "https://x.example", "instance_name": "X",
	}, tok)
	expect(t, bad, 400, "wrong totp")

	h.setupInstance()
	r = h.do(nil, "GET", "/instance", nil, nil)
	if r.body["setup_required"] != false || r.body["name"] != "Test Shelf" {
		t.Fatalf("instance after setup: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/setup/totp", map[string]any{"username": "a"}, tok), 409, "setup after completion")
	if _, err := config.LoadBootstrap(filepath.Join(h.dir, "config")); err != nil {
		t.Fatal("config.json not written:", err)
	}
}

func TestAccountFlow(t *testing.T) { forEachDialect(t, testAccountFlow) }

func testAccountFlow(t *testing.T, h *harness) {
	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)

	// Admin sees themself, TOTP on.
	r := h.do(nil, "GET", "/me", nil, bearer(adminTok))
	expect(t, r, 200, "admin me")
	if r.body["role"] != "admin" || r.body["totp_enabled"] != true {
		t.Fatalf("admin me: %s", r.raw)
	}

	// Sign-up: weak password, then a good one, then a duplicate.
	signup := func(user, pw string) resp {
		return h.do(nil, "POST", "/auth/signup", map[string]any{"username": user, "password": pw, "age_confirmed": true, "terms_accepted": true, "message": "hi"}, nil)
	}
	r = signup("bob", "password1234")
	expect(t, r, 400, "common password")
	if str(r.body, "code") != "password_too_common" {
		t.Fatalf("code: %s", r.raw)
	}
	expect(t, signup("bob", "marble-canyon-whistle"), 202, "signup")
	expect(t, signup("BOB", "marble-canyon-whistle"), 409, "duplicate username")

	// Pending users cannot log in.
	login := func(user, pw, platform string) resp {
		return h.do(nil, "POST", "/auth/login", map[string]any{"username": user, "password": pw, "device": device(platform)}, nil)
	}
	r = login("bob", "marble-canyon-whistle", "android")
	expect(t, r, 403, "pending login")
	if str(r.body, "code") != "account_pending" {
		t.Fatalf("code: %s", r.raw)
	}

	// Non-admins can't see the inbox; admins can and approve.
	r = h.do(nil, "GET", "/admin/signup-requests", nil, bearer(adminTok))
	expect(t, r, 200, "list signups")
	if len(r.list) != 1 {
		t.Fatalf("want 1 request: %s", r.raw)
	}
	reqID := r.list[0].(map[string]any)["id"].(string)
	expect(t, h.do(nil, "POST", "/admin/signup-requests/"+reqID+"/approve", nil, bearer(adminTok)), 204, "approve")
	expect(t, h.do(nil, "POST", "/admin/signup-requests/"+reqID+"/approve", nil, bearer(adminTok)), 409, "approve twice")

	// Bob logs in (no TOTP) and gets tokens.
	r = login("bob", "wrong-password-here", "android")
	expect(t, r, 401, "wrong password")
	r = login("bob", "marble-canyon-whistle", "android")
	expect(t, r, 200, "bob login")
	bobAccess, bobRefresh := str(r.body, "tokens", "access_token"), str(r.body, "tokens", "refresh_token")
	if bobRefresh == "" || str(r.body, "user", "status") != "active" {
		t.Fatalf("bob login body: %s", r.raw)
	}
	if r.body["user"].(map[string]any)["permissions"].(map[string]any)["can_upload"] != true {
		t.Fatal("default can_upload should be true")
	}
	expect(t, h.do(nil, "GET", "/admin/users", nil, bearer(bobAccess)), 403, "non-admin admin endpoint")
	expect(t, h.do(nil, "GET", "/me", nil, nil), 401, "no token")
	r = h.do(nil, "GET", "/me", nil, bearer("garbage"))
	expect(t, r, 401, "bad token")
	if str(r.body, "code") != "invalid_token" {
		t.Fatalf("bad token code: %s", r.raw)
	}

	// Refresh rotates; replaying the old token (outside the race window) revokes the session.
	r = h.do(nil, "POST", "/auth/refresh", map[string]any{"refresh_token": bobRefresh}, nil)
	expect(t, r, 200, "refresh")
	newRefresh, newAccess := str(r.body, "refresh_token"), str(r.body, "access_token")
	if newRefresh == bobRefresh {
		t.Fatal("refresh token not rotated")
	}
	expect(t, h.do(nil, "GET", "/me", nil, bearer(newAccess)), 200, "me with refreshed token")
	expect(t, h.do(nil, "POST", "/auth/refresh", map[string]any{"refresh_token": bobRefresh}, nil), 401, "old refresh within grace")
	h.srv.App().Auth.Now = func() time.Time { return time.Now().Add(time.Minute) }
	expect(t, h.do(nil, "POST", "/auth/refresh", map[string]any{"refresh_token": bobRefresh}, nil), 401, "old refresh replay")
	h.srv.App().Auth.Now = nil
	expect(t, h.do(nil, "GET", "/me", nil, bearer(newAccess)), 401, "session revoked after replay")
	expect(t, h.do(nil, "POST", "/auth/refresh", map[string]any{"refresh_token": newRefresh}, nil), 401, "new refresh dead too")

	// Groups: create, add bob, Public is protected.
	r = h.do(nil, "POST", "/admin/groups", map[string]any{"name": "Friends", "high_priority": true}, bearer(adminTok))
	expect(t, r, 201, "create group")
	gid := str(r.body, "id")
	expect(t, h.do(nil, "POST", "/admin/groups", map[string]any{"name": "friends"}, bearer(adminTok)), 409, "duplicate group")
	users := h.do(nil, "GET", "/admin/users?status=active", nil, bearer(adminTok))
	var bobID string
	for _, u := range users.list {
		if m := u.(map[string]any); m["username"] == "bob" {
			bobID = m["id"].(string)
		}
	}
	expect(t, h.do(nil, "PUT", "/admin/groups/"+gid+"/members/"+bobID, nil, bearer(adminTok)), 204, "add member")
	groups := h.do(nil, "GET", "/admin/groups", nil, bearer(adminTok))
	for _, g := range groups.list {
		m := g.(map[string]any)
		if m["name"] == "Public" {
			if m["members"].(float64) != 2 || m["builtin"] != true {
				t.Fatalf("Public group: %v", m)
			}
			expect(t, h.do(nil, "DELETE", "/admin/groups/"+m["id"].(string), nil, bearer(adminTok)), 404, "delete Public")
		}
	}

	// Ban needs a reason; banned users keep a session but only export/delete/logout.
	expect(t, h.do(nil, "PATCH", "/admin/users/"+bobID, map[string]any{"reason": " ", "status": "banned"}, bearer(adminTok)), 400, "ban without reason")
	expect(t, h.do(nil, "PATCH", "/admin/users/"+bobID, map[string]any{"reason": "spam", "status": "banned", "notice": "Read the rules"}, bearer(adminTok)), 200, "ban")
	r = login("bob", "marble-canyon-whistle", "android")
	expect(t, r, 200, "banned login")
	if str(r.body, "user", "status") != "banned" || str(r.body, "user", "notice") != "Read the rules" {
		t.Fatalf("banned me: %s", r.raw)
	}
	if r.body["user"].(map[string]any)["permissions"].(map[string]any)["can_upload"] != false {
		t.Fatal("banned user must not upload")
	}
	r = h.do(nil, "GET", "/me", nil, bearer(str(r.body, "tokens", "access_token")))
	if r.body["notice"] != nil {
		t.Fatal("notice must be shown only once")
	}
}

func TestWebCookiesAndCSRF(t *testing.T) { forEachDialect(t, testWebCookiesAndCSRF) }

func testWebCookiesAndCSRF(t *testing.T, h *harness) {
	secret := h.setupInstance()
	jar, _ := cookiejar.New(nil)
	web := &http.Client{Jar: jar}

	r := h.do(web, "POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword, "device": device("web")}, nil)
	code := totpStep(secret, 0)
	r = h.do(web, "POST", "/auth/totp/verify", map[string]any{"totp_challenge": str(r.body, "totp_challenge"), "code": code}, nil)
	expect(t, r, 200, "web totp verify")
	if str(r.body, "tokens", "refresh_token") != "" {
		t.Fatal("web clients must not get the refresh token in the body")
	}
	var csrf string
	for _, c := range jar.Cookies(mustURL(h.ts.URL)) {
		if c.Name == cookieCSRF {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("no csrf cookie")
	}
	expect(t, h.do(web, "GET", "/me", nil, nil), 200, "cookie auth GET")
	expect(t, h.do(web, "POST", "/admin/groups", map[string]any{"name": "Web"}, nil), 403, "cookie POST without CSRF")
	expect(t, h.do(web, "POST", "/admin/groups", map[string]any{"name": "Web"}, map[string]string{headerCSRF: csrf}), 201, "cookie POST with CSRF")
	expect(t, h.do(web, "POST", "/auth/refresh", nil, map[string]string{headerCSRF: csrf}), 200, "cookie refresh")
	expect(t, h.do(web, "POST", "/auth/logout", nil, map[string]string{headerCSRF: csrf}), 204, "logout")
	expect(t, h.do(web, "GET", "/me", nil, nil), 401, "after logout")
}

func TestRateLimitsAndLockout(t *testing.T) {
	h := newHarness(t)
	h.setupInstance()
	h.srv.App().Auth.Limits = auth.DefaultLimits()
	var last resp
	for range 6 {
		last = h.do(nil, "POST", "/auth/login", map[string]any{"username": "nobody", "password": "whatever-else", "device": device("android")}, nil)
	}
	expect(t, last, 429, "6th login in a minute")
	if last.header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestClientIPTrustedProxy(t *testing.T) {
	proxies, err := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{TrustedProxies: proxies}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("untrusted peer must not be able to spoof: %s", got)
	}
	req.RemoteAddr = "10.1.2.3:5555"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.7, 192.168.1.5")
	if got := s.clientIP(req); got != "198.51.100.7" {
		t.Fatalf("want right-most untrusted hop, got %s", got)
	}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }

func TestUnknownPathIs404JSON(t *testing.T) {
	h := newHarness(t)
	h.setupInstance()
	r := h.do(nil, "GET", "/nope", nil, nil)
	if r.status != 404 || !strings.Contains(r.raw, "not_found") {
		t.Fatalf("status %d body %s", r.status, r.raw)
	}
}

// totpStep returns the code for the current 30 s step plus offset.
func totpStep(secret string, offset int) string {
	c, _ := totp.GenerateCode(secret, time.Now().Add(time.Duration(offset)*30*time.Second))
	return c
}
