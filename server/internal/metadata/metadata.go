// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quip1/simple-apb/server/internal/version"
)

// Record is book metadata as a provider describes it.
type Record struct {
	Title, Subtitle, Description, Publisher, Language string
	ISBN, ASIN                                        string
	PublishedYear                                     int
	Authors, Narrators, Genres                        []string
	Series, SeriesIndex                               string
	CoverURL                                          string
}

// Candidate is a provider's record with its match score (0–1).
type Candidate struct {
	Provider string
	Record
	Score float64
}

// Query is what we know about a book when looking it up.
type Query struct {
	Title, Author, ISBN, ASIN string
}

// Provider looks books up in one online source.
type Provider interface {
	Name() string
	Lookup(ctx context.Context, q Query) ([]Record, error)
}

// errNotApplicable means the provider can't look this query up (no ASIN
// for Audnexus, nothing to search for); it doesn't count as asked.
var errNotApplicable = errors.New("not applicable")

// ErrRateLimited means the provider asked us to slow down; the job retries later.
var ErrRateLimited = errors.New("metadata provider rate limit")

// Endpoints are the provider base URLs (tests point them at fakes).
type Endpoints struct {
	OpenLibrary, OpenLibraryCovers, GoogleBooks, Audnexus string
	Client                                                *http.Client
}

// DefaultEndpoints are the public services.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		OpenLibrary:       "https://openlibrary.org",
		OpenLibraryCovers: "https://covers.openlibrary.org",
		GoogleBooks:       "https://www.googleapis.com/books/v1",
		Audnexus:          "https://api.audnex.us",
	}
}

func (e Endpoints) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Config chooses providers (from the instance settings).
type Config struct {
	OpenLibrary, GoogleBooks, Audnexus bool
	GoogleAPIKey                       string
	Threshold                          float64
}

// Providers returns the enabled providers. Audnexus answers ASIN lookups
// only, so it goes first: an ASIN match is exact.
func (e Endpoints) Providers(c Config) []Provider {
	var out []Provider
	hc := e.client()
	if c.Audnexus {
		out = append(out, &audnexus{base: e.Audnexus, hc: hc, limit: limiterFor("audnexus", 1100*time.Millisecond)})
	}
	if c.GoogleBooks {
		out = append(out, &googleBooks{base: e.GoogleBooks, key: c.GoogleAPIKey, hc: hc, limit: limiterFor("google", 250*time.Millisecond)})
	}
	if c.OpenLibrary {
		out = append(out, &openLibrary{base: e.OpenLibrary, covers: e.OpenLibraryCovers, hc: hc, limit: limiterFor("openlibrary", 500*time.Millisecond)})
	}
	return out
}

// Search asks every provider and returns their records scored against q,
// best first. It fails only if every provider failed (so a job retries).
func Search(ctx context.Context, providers []Provider, q Query) ([]Candidate, error) {
	var (
		out   []Candidate
		errs  []error
		asked int
	)
	for _, p := range providers {
		recs, err := p.Lookup(ctx, q)
		if errors.Is(err, errNotApplicable) {
			continue
		}
		asked++
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		for _, r := range recs {
			out = append(out, Candidate{Provider: p.Name(), Record: r, Score: Score(q, r)})
		}
	}
	if asked > 0 && len(errs) == asked {
		return nil, errors.Join(errs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// ── HTTP helpers ──

var userAgent = "SimpleAPB/" + version.Version + " (+https://github.com/quip1/simple-apb)"

// limiter spaces requests to one provider across the whole process.
type limiter struct {
	mu   sync.Mutex
	next time.Time
	gap  time.Duration
}

var (
	limitersMu sync.Mutex
	limiters   = map[string]*limiter{}
)

func limiterFor(name string, gap time.Duration) *limiter {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	if l, ok := limiters[name]; ok {
		return l
	}
	l := &limiter{gap: gap}
	limiters[name] = l
	return l
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.gap)
	l.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// getJSON fetches a URL; 404 returns (false, nil), 429/503 ErrRateLimited.
func getJSON(ctx context.Context, hc *http.Client, l *limiter, url string, dest any) (bool, error) {
	if err := l.wait(ctx); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return false, nil
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
		return false, ErrRateLimited
	case res.StatusCode != http.StatusOK:
		return false, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if err := decodeLimited(res, dest); err != nil {
		return false, fmt.Errorf("decode: %w", err)
	}
	return true, nil
}

func trimAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	seen := map[string]bool{}
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
}
