// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/media"
)

// farFuture: "now" for scans, so freshly written files count as settled.
var farFuture = time.Now().Add(time.Hour)

func TestBookTools(t *testing.T) { forEachDialect(t, testBookTools) }

// The book tools through the API (brief §6.13): who may use them, chapters,
// detection and merge jobs, the cover picker and the duplicate review.
func testBookTools(t *testing.T, h *harness) {
	tools := media.DefaultTools()
	if !tools.Available() {
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	_, sam := h.approvedUser(admin, "sam")
	S := bearer(sam)
	_, eve := h.approvedUser(admin, "eve")
	E := bearer(eve)

	src := filepath.Join(t.TempDir(), "book.m4b")
	if err := tools.MakeM4B(ctx, src, media.FixtureBook{Title: "Tool Book", Author: "Tess Tools", Seconds: 6, Frequency: 640, Cover: true,
		Chapters: []media.FixtureChapter{{Title: "One", StartMS: 0, EndMS: 6000}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(src)
	if st, _ := h.tusUpload(sam, "book.m4b", data, len(data)); st != http.StatusCreated {
		t.Fatalf("upload status %d", st)
	}
	book := h.waitUpload(sam)["book_id"].(string)

	chapters := map[string]any{"chapters": []map[string]any{{"title": "Intro", "start_ms": 0}, {"title": "Main", "start_ms": 2500}}}
	// Not the uploader: can't see it (404); the uploader and admins may.
	expect(t, h.do(nil, "PUT", "/books/"+book+"/chapters", chapters, E), 404, "stranger edits chapters")
	expect(t, h.do(nil, "PUT", "/books/"+book+"/chapters", map[string]any{"chapters": []map[string]any{{"title": "A", "start_ms": 999_999}}}, S),
		400, "chapter past the end")
	r := h.do(nil, "PUT", "/books/"+book+"/chapters", chapters, S)
	expect(t, r, 200, "uploader edits chapters")
	if len(r.body["chapters"].([]any)) != 2 || r.body["job_id"] == nil {
		t.Fatalf("saved: %s", r.raw)
	}
	if j := h.waitJob(sam, r.body["job_id"].(string)); j["status"] != "succeeded" {
		t.Fatalf("write chapters job: %v", j)
	}
	if r := h.do(nil, "GET", "/books/"+book, nil, S); str(r.body["chapters"].([]any)[1].(map[string]any), "title") != "Main" {
		t.Fatalf("book chapters: %s", r.raw)
	}

	// Silence detection: a proposal in the job result.
	r = h.do(nil, "POST", "/books/"+book+"/chapters/detect", map[string]any{"noise_db": -40, "min_silence_ms": 500}, S)
	expect(t, r, 202, "detect")
	j := h.waitJob(sam, r.body["job_id"].(string))
	if j["status"] != "succeeded" || len(j["result"].(map[string]any)["chapters"].([]any)) == 0 {
		t.Fatalf("detect job: %v", j)
	}
	expect(t, h.do(nil, "POST", "/books/"+book+"/chapters/detect", map[string]any{"noise_db": 5}, S), 400, "bad noise level")

	// Already one m4b: nothing to merge.
	expect(t, h.do(nil, "POST", "/books/"+book+"/merge", map[string]any{}, S), 409, "merge an m4b")

	// Cover picker: an uploaded PNG, then the embedded cover.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := range 64 {
		for y := range 64 {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(x * 4), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	req, _ := http.NewRequest("POST", h.ts.URL+"/api/v1/books/"+book+"/cover/upload", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Authorization", "Bearer "+sam)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 204 {
		t.Fatalf("cover upload: %v %v", res, err)
	}
	bad, _ := http.NewRequest("POST", h.ts.URL+"/api/v1/books/"+book+"/cover/upload", bytes.NewReader([]byte("not an image")))
	bad.Header.Set("Content-Type", "image/png")
	bad.Header.Set("Authorization", "Bearer "+sam)
	if res, _ := http.DefaultClient.Do(bad); res.StatusCode != 400 {
		t.Fatalf("bad image status %d", res.StatusCode)
	}
	expect(t, h.do(nil, "POST", "/books/"+book+"/cover/embedded", nil, S), 204, "embedded cover")

	// Duplicates: the same audio uploaded again by the admin's scan.
	lib := filepath.Join(h.dir, "library")
	dup := filepath.Join(lib, "Tess Tools", "Tool Book Again", "again.m4b")
	_ = os.MkdirAll(filepath.Dir(dup), 0o750)
	if err := tools.MakeM4B(ctx, dup, media.FixtureBook{Title: "Tool Book", Author: "Tess Tools", Seconds: 6, Frequency: 700}); err != nil {
		t.Fatal(err)
	}
	expect(t, h.do(nil, "POST", "/books/"+book+"/chapters/detect", map[string]any{}, A), 202, "admins may use the tools on any book")
	expect(t, h.do(nil, "GET", "/admin/duplicates", nil, S), 403, "duplicates are admin-only")
	if _, err := h.srv.App().Lib.Scan(ctx, farFuture); err != nil {
		t.Fatal(err)
	}
	r = h.do(nil, "POST", "/admin/duplicates/scan", nil, A)
	expect(t, r, 202, "scan duplicates")
	if j := h.waitJob(admin, r.body["job_id"].(string)); j["status"] != "succeeded" {
		t.Fatalf("duplicate scan: %v", j)
	}
	r = h.do(nil, "GET", "/admin/duplicates", nil, A)
	items := r.body["items"].([]any)
	if len(items) != 1 || str(items[0].(map[string]any), "kind") != "probable" {
		t.Fatalf("duplicates: %s", r.raw)
	}
	pair := items[0].(map[string]any)
	expect(t, h.do(nil, "POST", "/admin/duplicates/"+pair["id"].(string), map[string]any{"action": "merge"}, A), 400, "merge needs a book")
	expect(t, h.do(nil, "POST", "/admin/duplicates/"+pair["id"].(string), map[string]any{"action": "merge", "keep_book_id": book}, A), 204, "merge")
	if r := h.do(nil, "GET", "/admin/duplicates", nil, A); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("pair still open: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/library", nil, S); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("sam keeps the book: %s", r.raw)
	}
}
