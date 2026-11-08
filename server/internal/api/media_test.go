// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/store"
)

// get fetches an absolute or API-relative URL and returns status, headers, body.
func (h *harness) get(rawURL string, headers map[string]string) (int, http.Header, []byte) {
	h.t.Helper()
	if strings.HasPrefix(rawURL, "/") {
		rawURL = h.ts.URL + rawURL
	}
	// Signed URLs carry the configured public base URL; point them at the test server.
	rawURL = strings.Replace(rawURL, "https://books.example.com", h.ts.URL, 1)
	req, _ := http.NewRequest("GET", rawURL, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func TestMediaFlow(t *testing.T) { forEachDialect(t, testMediaFlow) }

func testMediaFlow(t *testing.T, h *harness) {
	tools := media.DefaultTools()
	if !tools.Available() {
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	lib := filepath.Join(h.dir, "library")
	bookDir := filepath.Join(lib, "Ann Author", "Two Part")
	for i, secs := range []int{12, 11} {
		p := filepath.Join(bookDir, []string{"01.mp3", "02.mp3"}[i])
		if err := tools.MakeMP3(ctx, p, media.FixtureBook{Title: "Two Part", Author: "Ann Author", Seconds: secs, Frequency: 500 + 50*i}, "Part", i+1); err != nil {
			t.Fatal(err)
		}
	}
	// A folder cover.jpg (big, so resizing has work to do).
	cover := filepath.Join(bookDir, "cover.jpg")
	if err := exec(tools.FFmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=900x600", "-frames:v", "1", cover); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = filepath.Walk(bookDir, func(p string, _ os.FileInfo, _ error) error { return os.Chtimes(p, old, old) })

	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)
	aliceID, alice := h.approvedUser(adminTok, "alice")
	_, bob := h.approvedUser(adminTok, "bob")
	r := h.do(nil, "POST", "/admin/library/scan", nil, bearer(adminTok))
	h.waitJob(adminTok, str(r.body, "job_id"))
	items := h.do(nil, "GET", "/admin/books?q=two", nil, bearer(adminTok)).body["items"].([]any)
	bookID := items[0].(map[string]any)["id"].(string)
	expect(t, h.do(nil, "POST", "/admin/distributions", map[string]any{"book_id": bookID, "target_type": "user", "target_id": aliceID}, bearer(adminTok)), 201, "distribute")

	detail := h.do(nil, "GET", "/books/"+bookID, nil, bearer(alice)).body
	files := detail["files"].([]any)
	f0 := files[0].(map[string]any)
	fileURL := "/api/v1/books/" + bookID + "/files/" + f0["id"].(string)

	// Whole file: bytes match the stored checksum.
	st, hdr, body := h.get(fileURL, bearer(alice))
	if st != 200 || hdr.Get("Accept-Ranges") != "bytes" || hdr.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("file: %d %v", st, hdr)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != f0["sha256"] {
		t.Fatal("served bytes don't match the stored checksum")
	}
	// Range and bad range.
	st, hdr, part := h.get(fileURL, map[string]string{"Authorization": "Bearer " + alice, "Range": "bytes=100-199"})
	if st != 206 || len(part) != 100 || !bytes.Equal(part, body[100:200]) || !strings.HasPrefix(hdr.Get("Content-Range"), "bytes 100-199/") {
		t.Fatalf("range: %d %d %v", st, len(part), hdr)
	}
	if st, _, _ := h.get(fileURL, map[string]string{"Authorization": "Bearer " + alice, "Range": "bytes=99999999-"}); st != 416 {
		t.Fatalf("bad range status %d", st)
	}
	// Access control.
	if st, _, _ := h.get(fileURL, nil); st != 401 {
		t.Fatalf("anonymous status %d", st)
	}
	if st, _, _ := h.get(fileURL, bearer(bob)); st != 404 {
		t.Fatalf("bob (not distributed) status %d", st)
	}

	// Signed URLs for casting: work without auth, fail when tampered.
	su := h.do(nil, "POST", "/books/"+bookID+"/stream-url", map[string]any{"profile": "original"}, bearer(alice))
	expect(t, su, 200, "stream-url")
	signed := su.body["files"].([]any)[0].(map[string]any)["url"].(string)
	if !strings.HasPrefix(signed, "https://books.example.com/api/v1/") {
		t.Fatalf("signed URL must use the public base URL: %s", signed)
	}
	if st, _, b := h.get(signed, nil); st != 200 || !bytes.Equal(b, body) {
		t.Fatalf("signed file: %d", st)
	}
	if st, _, _ := h.get(strings.Replace(signed, "sig=v1.", "sig=v1.x", 1), nil); st != 401 {
		t.Fatalf("tampered sig status %d", st)
	}
	if st, _, _ := h.get(strings.Replace(signed, bookID+"/files", bookID+"x/files", 1), nil); st != 400 && st != 404 {
		t.Fatalf("sig for wrong book status %d", st)
	}

	// HLS over a signed URL: playlist carries the sig into segment URIs.
	hu := h.do(nil, "POST", "/books/"+bookID+"/stream-url", map[string]any{"profile": "low"}, bearer(alice))
	hlsURL := str(hu.body, "hls_url")
	st, hdr, pl := h.get(hlsURL, nil)
	if st != 200 || hdr.Get("Content-Type") != "application/vnd.apple.mpegurl" || !strings.Contains(string(pl), "seg_000001.ts?sig=") {
		t.Fatalf("playlist: %d %s\n%s", st, hdr.Get("Content-Type"), pl)
	}
	segURL := strings.Replace(hlsURL, "index.m3u8", "seg_000001.ts", 1)
	if st, hdr, seg := h.get(segURL, nil); st != 200 || hdr.Get("Content-Type") != "video/mp2t" || len(seg) < 1000 {
		t.Fatalf("segment: %d %s %d bytes", st, hdr.Get("Content-Type"), len(seg))
	}
	if st, _, _ := h.get("/api/v1/stream/"+bookID+"/hls/medium/index.m3u8", bearer(alice)); st != 200 {
		t.Fatalf("medium playlist with bearer: %d", st)
	}
	if st, _, _ := h.get("/api/v1/stream/"+bookID+"/hls/low/seg_000099.ts", bearer(alice)); st != 404 {
		t.Fatalf("out-of-range segment: %d", st)
	}

	// Covers: resized, cached, never upscaled.
	st, hdr, img := h.get("/api/v1/books/"+bookID+"/cover?size=128", bearer(alice))
	if st != 200 || hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("cover: %d %v", st, hdr)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	if err != nil || cfg.Width != 128 || cfg.Height > 128 {
		t.Fatalf("cover size %dx%d %v", cfg.Width, cfg.Height, err)
	}
	if st, _, _ := h.get("/api/v1/books/"+bookID+"/cover?size=128", map[string]string{"Authorization": "Bearer " + alice, "If-None-Match": hdr.Get("ETag")}); st != 304 {
		t.Fatalf("cover revalidation status %d", st)
	}

	// A takedown cuts off even an already-issued signed URL.
	now := store.NowMS()
	if _, err := h.srv.App().DB.Exec(ctx, `INSERT INTO takedowns (id, book_id, reason, taken_at, created_at, updated_at) VALUES (?, ?, 'test', ?, ?, ?)`,
		store.NewID(), bookID, now, now, now); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := h.get(signed, nil); st != 404 {
		t.Fatalf("signed URL after takedown: %d", st)
	}
	if snap := h.srv.App().Shaper.Snapshot(); snap.RateBps != 80*1_000_000/8 {
		t.Fatalf("shaper rate %d", snap.RateBps)
	}
}

func exec(bin string, args ...string) error {
	return runCmd(bin, args...)
}

func runCmd(bin string, args ...string) error {
	out, err := osexec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}
