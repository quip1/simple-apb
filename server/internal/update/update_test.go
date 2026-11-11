// SPDX-License-Identifier: AGPL-3.0-or-later

package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.5.0", "0.4.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.10.0", "0.9.0", true},
		{"0.5.0", "0.5.0", false},
		{"0.5.0", "0.5.0-rc.1", true},
		{"0.5.0-rc.1", "0.5.0", false},
		{"0.4.0", "0.5.0", false},
		{"0.5.0", "dev", false},
		{"garbage", "0.1.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestCheck(t *testing.T) {
	body := `{"tag_name":"v0.6.0","html_url":"https://github.com/o/r/releases/tag/v0.6.0","body":"Notes","published_at":"2026-11-12T10:00:00Z"}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer ts.Close()
	c := &Checker{URL: ts.URL, HTTP: ts.Client()}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	rel, at, msg := c.State()
	if rel == nil || rel.Version != "0.6.0" || rel.Notes != "Notes" || at.IsZero() || msg != "" {
		t.Fatalf("state: %+v %v %q", rel, at, msg)
	}
	// Pre-releases aren't offered.
	body = `{"tag_name":"v0.7.0-rc.1","prerelease":true}`
	_ = c.Check(context.Background())
	if rel, _, _ := c.State(); rel != nil {
		t.Fatalf("pre-release offered: %+v", rel)
	}
	// Unreachable: the error is kept.
	ts.Close()
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("no error for an unreachable server")
	}
	if _, _, msg := c.State(); msg == "" {
		t.Fatal("check error not kept")
	}
}

func TestUpdaterFiles(t *testing.T) {
	dir := t.TempDir()
	if UpdaterPresent(dir) || Last(dir) != nil {
		t.Fatal("empty folder")
	}
	_ = os.WriteFile(filepath.Join(dir, "heartbeat"), nil, 0o600)
	if !UpdaterPresent(dir) {
		t.Fatal("fresh heartbeat not seen")
	}
	old := time.Now().Add(-5 * time.Minute)
	_ = os.Chtimes(filepath.Join(dir, "heartbeat"), old, old)
	if UpdaterPresent(dir) {
		t.Fatal("stale heartbeat counted")
	}
	if err := Request(dir, "0.5.0", "0.6.0"); err != nil {
		t.Fatal(err)
	}
	if a := Last(dir); a == nil || a.Status != "requested" || a.To != "0.6.0" {
		t.Fatalf("after request: %+v", a)
	}
	if err := Request(dir, "0.5.0", "0.6.0"); err != ErrBusy {
		t.Fatalf("second request: %v", err)
	}
	// The updater takes it and finishes.
	_ = os.Rename(filepath.Join(dir, "request.json"), filepath.Join(dir, "running.json"))
	if a := Last(dir); a.Status != "running" {
		t.Fatalf("running: %+v", a)
	}
	_ = os.Remove(filepath.Join(dir, "running.json"))
	_ = os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"from":"0.5.0","to":"0.6.0","status":"rolled_back","message":"unhealthy","at":"2026-11-12T10:00:00Z"}`), 0o600)
	if a := Last(dir); a.Status != "rolled_back" || a.Message != "unhealthy" {
		t.Fatalf("result: %+v", a)
	}
	if err := Request(dir, "0.5.0", "0.6.1"); err != nil {
		t.Fatalf("request after a result: %v", err)
	}
}
