// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/quip1/simple-apb/server/internal/metadata"
	"github.com/quip1/simple-apb/server/internal/store"
)

// fakeMeta stands in for Open Library, Google Books and Audnexus.
type fakeMeta struct {
	mu   sync.Mutex
	ts   *httptest.Server
	ol   map[string]map[string]any // lower-case title → Open Library doc
	ax   map[string]map[string]any // ASIN → Audnexus book
	gb   map[string]map[string]any // lower-case title → Google Books volumeInfo
	fail bool
	hits int
}

func newFakeMeta(t *testing.T) *fakeMeta {
	f := &fakeMeta{ol: map[string]map[string]any{}, ax: map[string]map[string]any{}, gb: map[string]map[string]any{}}
	f.ts = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.ts.Close)
	return f
}

func (f *fakeMeta) endpoints() metadata.Endpoints {
	return metadata.Endpoints{OpenLibrary: f.ts.URL + "/ol", OpenLibraryCovers: f.ts.URL + "/olc", GoogleBooks: f.ts.URL + "/gb",
		Audnexus: f.ts.URL + "/ax", Client: f.ts.Client()}
}

func (f *fakeMeta) set(fn func(f *fakeMeta)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeMeta) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits++
	if f.fail && !strings.HasPrefix(r.URL.Path, "/olc/") {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.URL.Path == "/ol/search.json":
		docs := []any{}
		if d, ok := f.ol[strings.ToLower(r.URL.Query().Get("title"))]; ok {
			docs = append(docs, d)
		}
		if isbn := metadata.NormalizeISBN(r.URL.Query().Get("isbn")); isbn != "" {
			for _, d := range f.ol {
				for _, i := range d["isbn"].([]string) {
					if metadata.NormalizeISBN(i) == isbn {
						docs = append(docs, d)
					}
				}
			}
		}
		writeJSON(map[string]any{"docs": docs})
	case r.URL.Path == "/gb/volumes":
		items := []any{}
		q := r.URL.Query().Get("q")
		for title, v := range f.gb {
			isbn, _ := v["isbn"].(string)
			if strings.HasPrefix(strings.ToLower(q), "intitle:"+title) || (isbn != "" && q == "isbn:"+isbn) {
				info := map[string]any{"industryIdentifiers": []map[string]string{{"type": "ISBN_13", "identifier": isbn}}}
				for k, x := range v {
					if k != "isbn" {
						info[k] = x
					}
				}
				items = append(items, map[string]any{"volumeInfo": info})
			}
		}
		writeJSON(map[string]any{"items": items})
	case strings.HasPrefix(r.URL.Path, "/ax/books/"):
		if b, ok := f.ax[strings.TrimPrefix(r.URL.Path, "/ax/books/")]; ok {
			writeJSON(b)
			return
		}
		http.NotFound(w, r)
	case strings.HasPrefix(r.URL.Path, "/olc/"):
		img := image.NewRGBA(image.Rect(0, 0, 64, 96))
		for x := range 64 {
			for y := range 96 {
				img.Set(x, y, color.RGBA{R: 40, G: 80, B: uint8(x * 3), A: 255})
			}
		}
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(buf.Bytes())
	default:
		http.NotFound(w, r)
	}
}

func TestMetadataPipeline(t *testing.T) { forEachDialect(t, testMetadataPipeline) }

func testMetadataPipeline(t *testing.T, h *harness) {
	ctx := context.Background()
	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	samID, sam := h.approvedUser(admin, "sam")
	S := bearer(sam)
	app := h.srv.App()
	db := app.DB

	h.meta.set(func(f *fakeMeta) {
		f.ol["the quiet orbit"] = map[string]any{"title": "The Quiet Orbit", "author_name": []string{"Mara Lindqvist"},
			"first_publish_year": 2019, "publisher": []string{"Lantern Press"}, "isbn": []string{"0-306-40615-2"},
			"cover_i": 7, "subject": []string{"Science fiction"}, "language": []string{"eng"}}
		f.gb["the quiet orbit"] = map[string]any{"title": "The Quiet Orbit", "authors": []string{"Mara Lindqvist"},
			"publisher": "Lantern Press", "publishedDate": "2019-02", "isbn": "9780306406157"}
		f.ax["B0TESTASIN"] = map[string]any{"asin": "B0TESTASIN", "title": "Salt and Cedar", "authors": []map[string]string{{"name": "Ines Moreau"}},
			"narrators": []map[string]string{{"name": "Ana Ruiz"}}, "summary": "<p>A <b>coastal</b> story.</p>",
			"releaseDate": "2021-03-02T00:00:00.000Z", "seriesPrimary": map[string]string{"name": "Coast", "position": "2"},
			"genres": []map[string]string{{"name": "Literature & Fiction", "type": "genre"}, {"name": "Family", "type": "tag"}}}
	})
	newBook := func(title, folder string, authors []string, asin, uploader string) string {
		t.Helper()
		id, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: title, Authors: authors, ASIN: asin},
			FolderPath: h.bookFolder(folder), Origin: "scan", Status: "ready", DurationMS: 60_000, Checksum: "c-" + folder, UploaderID: uploader})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	orbit := newBook("The Quiet Orbit", "m/The Quiet Orbit", []string{"Mara Lindqvist"}, "", "")
	salt := newBook("Salt & Cedar (Unabridged)", "i/Salt", nil, "B0TESTASIN", "")
	odd := newBook("Untitled Recording 4", "x/Untitled", nil, "", "")

	// A confident title + author match fills empty fields and fetches a cover.
	res, err := app.Meta.Match(ctx, orbit)
	if err != nil || res.NeedsReview || res.Score < 0.8 || !res.Cover {
		t.Fatalf("orbit match: %+v %v", res, err)
	}
	r := h.do(nil, "GET", "/books/"+orbit, nil, A)
	if r.body["publisher"] != "Lantern Press" || r.body["published_year"] != float64(2019) || r.body["isbn"] != "9780306406157" ||
		r.body["language"] != "en" || r.body["has_cover"] != true || r.body["needs_review"] != false || r.body["can_edit"] != true {
		t.Fatalf("after match: %s", r.raw)
	}
	if !strings.Contains(str(r.body, "cover_url"), "?v=") {
		t.Fatalf("cover_url must carry a version: %s", r.raw)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "library", "m", "The Quiet Orbit", "cover.jpg")); err != nil {
		t.Fatalf("cover.jpg not written to the book folder: %v", err)
	}
	if c := h.do(nil, "GET", strings.TrimPrefix(str(r.body, "cover_url"), "/api/v1")+"&size=128", nil, A); c.status != 200 {
		t.Fatalf("cover: %d", c.status)
	}

	// An ASIN match (Audnexus) is certain: narrators, series and genres too.
	if res, err := app.Meta.Match(ctx, salt); err != nil || res.Score != 1 || res.Provider != "audnexus" {
		t.Fatalf("salt match: %+v %v", res, err)
	}
	r = h.do(nil, "GET", "/books/"+salt, nil, A)
	if str(r.body, "series", "name") != "Coast" || r.body["description"] != "A coastal story." || r.body["published_year"] != float64(2021) ||
		len(r.body["narrators"].([]any)) != 1 || len(r.body["authors"].([]any)) != 1 {
		t.Fatalf("after ASIN match: %s", r.raw)
	}
	if tags := r.body["tags"].([]any); len(tags) != 1 || tags[0].(map[string]any)["name"] != "Literature & Fiction" {
		t.Fatalf("genres: %s", r.raw)
	}

	// No match: flagged "needs review", nothing applied.
	if res, err := app.Meta.Match(ctx, odd); err != nil || !res.NeedsReview {
		t.Fatalf("odd match: %+v %v", res, err)
	}
	r = h.do(nil, "GET", "/admin/metadata-review", nil, A)
	if items := r.body["items"].([]any); len(items) != 1 || str(items[0].(map[string]any), "book", "title") != "Untitled Recording 4" {
		t.Fatalf("review list: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/admin/dashboard", nil, A); r.body["inbox"].(map[string]any)["metadata_review"] != float64(1) {
		t.Fatalf("inbox count: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/admin/metadata-review/"+odd+"/dismiss", nil, A), 204, "dismiss")
	if r := h.do(nil, "GET", "/admin/metadata-review", nil, A); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("dismissed: %s", r.raw)
	}

	// Hand edits win and are locked against later matches.
	patch := func(tok map[string]string, id string, body map[string]any) resp {
		return h.do(nil, "PATCH", "/books/"+id, body, tok)
	}
	expect(t, patch(A, orbit, map[string]any{}), 400, "empty patch")
	expect(t, patch(A, orbit, map[string]any{"title": "  "}), 400, "blank title")
	expect(t, patch(A, orbit, map[string]any{"isbn": "123"}), 400, "bad isbn")
	expect(t, patch(A, orbit, map[string]any{"asin": "short"}), 400, "bad asin")
	expect(t, patch(A, orbit, map[string]any{"language": "English!"}), 400, "bad language")
	r = patch(A, orbit, map[string]any{"publisher": "Hand Press", "series": map[string]any{"name": "Lantern Cycle", "index": "1"},
		"narrators": []string{"Kai Berg", " kai berg ", ""}})
	expect(t, r, 200, "edit")
	if r.body["publisher"] != "Hand Press" || str(r.body, "series", "name") != "Lantern Cycle" || len(r.body["narrators"].([]any)) != 1 {
		t.Fatalf("edit result: %s", r.raw)
	}
	if lf := r.body["locked_fields"].([]any); len(lf) != 3 || lf[0] != "publisher" || lf[1] != "narrators" || lf[2] != "series" {
		t.Fatalf("locked: %v", lf)
	}
	h.meta.set(func(f *fakeMeta) { f.gb["the quiet orbit"]["publisher"] = "Other House" })
	_ = store.UpdateBookMeta(ctx, db, orbit, store.MetaPatch{Publisher: ptrTo(""), Subtitle: ptrTo("")}, false) // empty but locked
	if _, err := app.Meta.Match(ctx, orbit); err != nil {
		t.Fatal(err)
	}
	if r := h.do(nil, "GET", "/books/"+orbit, nil, A); r.body["publisher"] != nil {
		t.Fatalf("a locked field must not be filled: %s", r.raw)
	}
	r = patch(A, orbit, map[string]any{"unlock": []string{"publisher"}})
	if lf := r.body["locked_fields"].([]any); len(lf) != 2 {
		t.Fatalf("unlock: %s", r.raw)
	}
	if _, err := app.Meta.Match(ctx, orbit); err != nil {
		t.Fatal(err)
	}
	if r := h.do(nil, "GET", "/books/"+orbit, nil, A); r.body["publisher"] != "Other House" {
		t.Fatalf("unlocked field fills again: %s", r.raw)
	}

	// Candidates, and covers only from provider hosts.
	r = h.do(nil, "GET", "/books/"+orbit+"/metadata/candidates", nil, A)
	coverURL := ""
	for _, c := range r.list {
		if m := c.(map[string]any); m["provider"] == "openlibrary" && m["score"].(float64) >= 0.8 {
			coverURL = str(m, "cover_url")
		}
	}
	if len(r.list) != 2 || coverURL == "" {
		t.Fatalf("candidates: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/books/"+orbit+"/metadata/candidates?title=Nothing+Here", nil, A); len(r.list) != 0 {
		t.Fatalf("override search: %s", r.raw)
	}
	expect(t, h.do(nil, "PUT", "/books/"+odd+"/cover", map[string]any{"url": "https://attacker.example/x.jpg"}, A), 400, "foreign cover host")
	expect(t, h.do(nil, "PUT", "/books/"+odd+"/cover", map[string]any{"url": "http://169.254.169.254/latest"}, A), 400, "metadata address")
	expect(t, h.do(nil, "PUT", "/books/"+odd+"/cover", map[string]any{"url": coverURL}, A), 204, "cover from provider")
	if r := h.do(nil, "GET", "/books/"+odd, nil, A); r.body["has_cover"] != true {
		t.Fatalf("cover set: %s", r.raw)
	}

	// The job path: refresh queues the pipeline.
	r = h.do(nil, "POST", "/books/"+odd+"/metadata/refresh", nil, A)
	expect(t, r, 202, "refresh")
	if j := h.waitJob(admin, str(r.body, "job_id")); j["status"] != "succeeded" {
		t.Fatalf("job: %v", j)
	}

	// Providers down: lookups report 502, the pipeline returns an error (the job retries).
	h.meta.set(func(f *fakeMeta) { f.fail = true })
	expect(t, h.do(nil, "GET", "/books/"+orbit+"/metadata/candidates", nil, A), 502, "providers down")
	if _, err := app.Meta.Match(ctx, odd); err == nil {
		t.Fatal("match must fail when every provider fails")
	}
	h.meta.set(func(f *fakeMeta) { f.fail = false })

	// Who may edit: admins anywhere; users only their own uploads with can_edit_metadata.
	expect(t, patch(S, orbit, map[string]any{"subtitle": "x"}), 404, "can't see it")
	expect(t, h.do(nil, "POST", "/admin/distributions", map[string]any{"book_id": orbit, "target_type": "user", "target_id": samID}, A), 201, "share")
	expect(t, patch(S, orbit, map[string]any{"subtitle": "x"}), 403, "not the uploader")
	if r := h.do(nil, "GET", "/books/"+orbit, nil, S); r.body["can_edit"] != false || r.body["locked_fields"] != nil {
		t.Fatalf("non-editor view: %s", r.raw)
	}
	mine := newBook("Sam's Recording", "s/Sams", nil, "", samID)
	if err := store.AddEntry(ctx, db, samID, mine, "own_upload"); err != nil {
		t.Fatal(err)
	}
	if r := patch(S, mine, map[string]any{"authors": []string{"Sam Doe"}}); r.status != 200 || r.body["can_edit"] != true {
		t.Fatalf("own upload edit: %d %s", r.status, r.raw)
	}
	expect(t, h.do(nil, "GET", "/books/"+mine+"/metadata/candidates", nil, S), 200, "own candidates")
	expect(t, h.do(nil, "GET", "/admin/metadata-review", nil, S), 403, "review list is admin-only")
	expect(t, h.do(nil, "PUT", "/admin/users/"+samID+"/permissions", map[string]any{"can_edit_metadata": false}, A), 204, "revoke")
	expect(t, patch(S, mine, map[string]any{"subtitle": "x"}), 403, "permission revoked")

	// Settings: providers can be switched off; the threshold is bounded.
	expect(t, h.do(nil, "PATCH", "/admin/settings", map[string]any{"metadata": map[string]any{"threshold": 0.3}}, A), 400, "threshold")
	r = h.do(nil, "PATCH", "/admin/settings", map[string]any{"metadata": map[string]any{"open_library": false, "google_books": false,
		"audnexus": false, "google_books_key": "k-123"}}, A)
	expect(t, r, 200, "settings")
	if m := r.body["metadata"].(map[string]any); m["open_library"] != false || m["google_books_key_set"] != true || m["google_books_key"] != nil {
		t.Fatalf("metadata settings: %s", r.raw)
	}
	before := 0
	h.meta.set(func(f *fakeMeta) { before = f.hits })
	if res, err := app.Meta.Match(ctx, orbit); err != nil || res.Skipped == "" {
		t.Fatalf("disabled providers: %+v %v", res, err)
	}
	h.meta.set(func(f *fakeMeta) {
		if f.hits != before {
			t.Error("disabled providers must not be called")
		}
	})

	// Everything is audited.
	seen := map[string]bool{}
	for _, e := range h.do(nil, "GET", "/admin/audit?limit=200", nil, A).body["items"].([]any) {
		seen[e.(map[string]any)["action"].(string)] = true
	}
	for _, a := range []string{"book.metadata_match", "book.metadata_needs_review", "book.metadata_edit", "book.cover_set",
		"book.metadata_refresh", "book.metadata_review_dismiss"} {
		if !seen[a] {
			t.Errorf("audit log is missing %s", a)
		}
	}
}

func ptrTo[T any](v T) *T { return &v }
