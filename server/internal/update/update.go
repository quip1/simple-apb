// SPDX-License-Identifier: AGPL-3.0-or-later

// Package update finds new releases and talks to the optional updater
// container (brief §6.21, ADR-0049).
//
// The updater (deploy/updater) shares a folder with the server, normally
// /config/updater. They talk through files there:
//   - heartbeat     touched by the updater every few seconds while it runs
//   - request.json  written by the server: switch to this version
//   - running.json  the request, once the updater has taken it
//   - result.json   the outcome: succeeded, rolled_back or failed
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quip1/simple-apb/server/internal/fsutil"
)

// DefaultReleasesURL is where releases are published (GitHub's API for the
// project's repository). SIMPLEAPB_RELEASES_URL overrides it; "off" disables
// the check.
const DefaultReleasesURL = "https://api.github.com/repos/quip1/simple-apb/releases/latest"

// Release is a published version.
type Release struct {
	Version     string
	URL         string
	Notes       string
	PublishedAt time.Time
}

// Checker looks up the newest release and remembers the answer.
type Checker struct {
	URL  string
	HTTP *http.Client

	mu        sync.Mutex
	latest    *Release
	checkedAt time.Time
	err       string
}

// NewChecker uses the URL from the environment or the default.
func NewChecker() *Checker {
	u := os.Getenv("SIMPLEAPB_RELEASES_URL")
	if u == "" {
		u = DefaultReleasesURL
	}
	if u == "off" {
		u = ""
	}
	return &Checker{URL: u, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Check asks for the newest release now.
func (c *Checker) Check(ctx context.Context) error {
	if c.URL == "" {
		return nil
	}
	rel, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkedAt = time.Now()
	if err != nil {
		c.err = err.Error()
		return err
	}
	c.latest, c.err = rel, ""
	return nil
}

func (c *Checker) fetch(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-apb-server")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release check: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil // nothing released yet
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release check: HTTP %d", res.StatusCode)
	}
	var v struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&v); err != nil {
		return nil, fmt.Errorf("release check: %w", err)
	}
	ver := strings.TrimPrefix(v.TagName, "v")
	if v.Draft || v.Prerelease || !Valid(ver) {
		return nil, nil
	}
	notes := v.Body
	if len(notes) > 20_000 {
		notes = notes[:20_000]
	}
	return &Release{Version: ver, URL: v.HTMLURL, Notes: notes, PublishedAt: v.PublishedAt}, nil
}

// State is the last answer: the newest release (nil if none), when it was
// checked, and why the last check failed.
func (c *Checker) State() (*Release, time.Time, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest, c.checkedAt, c.err
}

// ── versions ──

var semver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// Valid reports whether v is a semver version (no "v" prefix).
func Valid(v string) bool { return semver.MatchString(v) }

// Newer reports whether candidate is a later version than current. A build
// from source ("dev") is never offered an update.
func Newer(candidate, current string) bool {
	a, b := semver.FindStringSubmatch(candidate), semver.FindStringSubmatch(current)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	// Same numbers: a release is newer than its pre-releases.
	return a[4] == "" && b[4] != ""
}

// ── the updater's folder ──

// Attempt is one update, as the updater reports it.
type Attempt struct {
	From    string    `json:"from,omitempty"`
	To      string    `json:"to"`
	Status  string    `json:"status"` // requested, running, succeeded, rolled_back, failed
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// The updater counts as present while its heartbeat is this fresh.
const heartbeatMaxAge = 90 * time.Second

// UpdaterPresent reports whether the updater container is running.
func UpdaterPresent(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "heartbeat"))
	return err == nil && time.Since(st.ModTime()) < heartbeatMaxAge
}

// ErrBusy means an update is already requested or running.
var ErrBusy = errors.New("an update is already under way")

func readAttempt(path string) (*Attempt, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Attempt
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Last returns the latest update: a pending or running one first, else the
// last result. Nil if there has never been one.
func Last(dir string) *Attempt {
	for _, f := range []struct{ name, status string }{{"running.json", "running"}, {"request.json", "requested"}} {
		if a, err := readAttempt(filepath.Join(dir, f.name)); err == nil {
			a.Status = f.status
			return a
		}
	}
	if a, err := readAttempt(filepath.Join(dir, "result.json")); err == nil {
		return a
	}
	return nil
}

// Request asks the updater to switch from one version to another.
func Request(dir, from, to string) error {
	if a := Last(dir); a != nil && (a.Status == "requested" || a.Status == "running") {
		// A request the updater never picked up (it was stopped) doesn't block forever.
		if time.Since(a.At) < 15*time.Minute {
			return ErrBusy
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.Marshal(Attempt{From: from, To: to, Status: "requested", At: time.Now().UTC()})
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, "request.json"), b, 0o640)
}
