// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeISBN(t *testing.T) {
	cases := map[string]string{
		"978-0-306-40615-7": "9780306406157",
		"0-306-40615-2":     "9780306406157", // ISBN-10 → 13
		"080442957X":        "9780804429573",
		"978-0-306-40615-8": "", // bad check digit
		"12345":             "",
		"X123456789":        "",
		"":                  "",
	}
	for in, want := range cases {
		if got := NormalizeISBN(in); got != want {
			t.Errorf("NormalizeISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScore(t *testing.T) {
	q := Query{Title: "The Quiet Orbit", Author: "Mara Lindqvist"}
	cases := []struct {
		name     string
		q        Query
		r        Record
		min, max float64
	}{
		{"exact", q, Record{Title: "The Quiet Orbit", Authors: []string{"Mara Lindqvist"}}, 1, 1},
		{"article, case, punctuation", q, Record{Title: "quiet orbit!", Authors: []string{"MARA LINDQVIST"}}, 1, 1},
		{"author reversed", q, Record{Title: "The Quiet Orbit", Authors: []string{"Lindqvist, Mara"}}, 0.95, 1},
		{"unabridged noise", Query{Title: "The Quiet Orbit (Unabridged)", Author: "Mara Lindqvist"},
			Record{Title: "The Quiet Orbit", Authors: []string{"Mara Lindqvist"}}, 1, 1},
		{"subtitle on our side", Query{Title: "The Quiet Orbit: Lantern Cycle 1", Author: "Mara Lindqvist"},
			Record{Title: "The Quiet Orbit", Authors: []string{"Mara Lindqvist"}}, 0.9, 1},
		{"wrong author", q, Record{Title: "The Quiet Orbit", Authors: []string{"Someone Else"}}, 0.7, 0.8},
		{"different book", q, Record{Title: "Salt and Cedar", Authors: []string{"Ines Moreau"}}, 0, 0.4},
		{"no author known caps the score", Query{Title: "The Quiet Orbit"}, Record{Title: "The Quiet Orbit"}, 0.75, 0.75},
		{"isbn match is certain", Query{Title: "x", ISBN: "0-306-40615-2"}, Record{Title: "y", ISBN: "9780306406157"}, 1, 1},
		{"asin match is certain", Query{Title: "x", ASIN: "b0testasin"}, Record{Title: "y", ASIN: "B0TESTASIN"}, 1, 1},
		{"accents", Query{Title: "Café Stories", Author: "Zoë Brontë"}, Record{Title: "Cafe Stories", Authors: []string{"Zoe Bronte"}}, 1, 1},
	}
	for _, c := range cases {
		if got := Score(c.q, c.r); got < c.min || got > c.max {
			t.Errorf("%s: score %.2f, want %.2f–%.2f", c.name, got, c.min, c.max)
		}
	}
}

func fake(t *testing.T, h http.HandlerFunc) Endpoints {
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return Endpoints{OpenLibrary: ts.URL + "/ol", OpenLibraryCovers: ts.URL + "/olc", GoogleBooks: ts.URL + "/gb", Audnexus: ts.URL + "/ax", Client: ts.Client()}
}

func TestProvidersParse(t *testing.T) {
	var gotKey, gotQuery, gotUA string
	e := fake(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		switch {
		case strings.HasPrefix(r.URL.Path, "/gb/volumes"):
			gotKey, gotQuery = r.URL.Query().Get("key"), r.URL.Query().Get("q")
			_, _ = w.Write([]byte(`{"items":[{"volumeInfo":{"title":"The Quiet Orbit","subtitle":"A Novel","authors":["Mara Lindqvist"],
				"publisher":"Lantern","publishedDate":"2019-05-01","description":"<p>Stars &amp; silence.</p>","categories":["Fiction"],
				"language":"en","industryIdentifiers":[{"type":"ISBN_10","identifier":"0306406152"},{"type":"ISBN_13","identifier":"9780306406157"}],
				"imageLinks":{"thumbnail":"http://books.google.com/books/content?id=1&zoom=1&edge=curl"}}}]}`))
		case r.URL.Path == "/ol/search.json":
			_, _ = w.Write([]byte(`{"docs":[{"title":"The Quiet Orbit","author_name":["Mara Lindqvist"],"first_publish_year":2019,"cover_i":42,"language":["ger"]}]}`))
		case r.URL.Path == "/ax/books/B0TESTASIN":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	})
	ps := e.Providers(Config{OpenLibrary: true, GoogleBooks: true, GoogleAPIKey: "key-1", Audnexus: true})
	if len(ps) != 3 || ps[0].Name() != "audnexus" {
		t.Fatalf("providers: %v", ps)
	}
	ctx := context.Background()
	q := Query{Title: "The Quiet Orbit", Author: "Mara Lindqvist"}

	gb, err := ps[1].Lookup(ctx, q)
	if err != nil || len(gb) != 1 {
		t.Fatalf("google: %v %v", gb, err)
	}
	r := gb[0]
	if r.ISBN != "9780306406157" || r.PublishedYear != 2019 || r.Description != "Stars & silence." ||
		r.CoverURL != "https://books.google.com/books/content?id=1&zoom=1" || gotKey != "key-1" ||
		gotQuery != "intitle:The Quiet Orbit inauthor:Mara Lindqvist" {
		t.Fatalf("google record: %+v key=%q q=%q", r, gotKey, gotQuery)
	}
	if !strings.HasPrefix(gotUA, "SimpleAPB/") {
		t.Fatalf("user agent %q", gotUA)
	}
	ol, err := ps[2].Lookup(ctx, q)
	if err != nil || len(ol) != 1 || ol[0].Language != "de" || !strings.HasSuffix(ol[0].CoverURL, "/olc/b/id/42-L.jpg") {
		t.Fatalf("open library: %+v %v", ol, err)
	}
	// Audnexus only answers ASINs; a 429 is reported as rate-limited.
	if _, err := ps[0].Lookup(ctx, q); !errors.Is(err, errNotApplicable) {
		t.Fatalf("audnexus without ASIN: %v", err)
	}
	if _, err := ps[0].Lookup(ctx, Query{ASIN: "B0TESTASIN"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("audnexus 429: %v", err)
	}
	// Search sorts by score and tolerates one provider failing.
	cands, err := Search(ctx, ps, Query{Title: "The Quiet Orbit", Author: "Mara Lindqvist", ASIN: "B0TESTASIN"})
	if err != nil || len(cands) != 2 || cands[0].Score < cands[1].Score {
		t.Fatalf("search: %+v %v", cands, err)
	}
}

func TestSearchFailsOnlyWhenEveryProviderFails(t *testing.T) {
	e := fake(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusBadGateway) })
	ps := e.Providers(Config{OpenLibrary: true, GoogleBooks: true, Audnexus: true})
	if _, err := Search(context.Background(), ps, Query{Title: "x"}); err == nil {
		t.Fatal("want an error when every asked provider fails")
	}
	if c, err := Search(context.Background(), ps, Query{}); err != nil || len(c) != 0 {
		t.Fatalf("nothing to ask is not a failure: %v %v", c, err)
	}
}

func TestCoverHosts(t *testing.T) {
	allowed := []string{"https://covers.openlibrary.org/b/id/1-L.jpg", "https://books.google.com/books/content?id=x",
		"https://m.media-amazon.com/images/I/x.jpg"}
	denied := []string{"http://covers.openlibrary.org/b/id/1-L.jpg", "https://evil.example/x.jpg", "https://covers.openlibrary.org.evil.example/x",
		"https://user@covers.openlibrary.org/x", "file:///etc/passwd", "http://169.254.169.254/", "not a url"}
	for _, u := range allowed {
		if !AllowedCoverURL(u) {
			t.Errorf("should allow %s", u)
		}
	}
	for _, u := range denied {
		if AllowedCoverURL(u) {
			t.Errorf("should deny %s", u)
		}
	}
	if !AllowedCoverURL("http://127.0.0.1:9/olc/x", "http://127.0.0.1:9/olc") || AllowedCoverURL("http://127.0.0.1:10/x", "http://127.0.0.1:9") {
		t.Error("extra hosts must match scheme and host:port exactly")
	}
}

func TestToJPEG(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 100, 150)))
	out, err := ToJPEG(buf.Bytes())
	if err != nil || !bytes.HasPrefix(out, []byte{0xFF, 0xD8}) {
		t.Fatalf("png → jpeg: %v", err)
	}
	if _, err := ToJPEG([]byte("<html>")); err == nil {
		t.Fatal("HTML is not an image")
	}
	buf.Reset()
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if _, err := ToJPEG(buf.Bytes()); err == nil {
		t.Fatal("tiny images are rejected")
	}
}
