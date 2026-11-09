// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/store"
)

// tusUpload performs a real tus 1.0 upload (create + one PATCH) and returns
// the creation status and upload URL.
func (h *harness) tusUpload(token, filename string, data []byte, declaredLen int) (int, string) {
	h.t.Helper()
	meta := "filename " + base64.StdEncoding.EncodeToString([]byte(filename))
	req, _ := http.NewRequest("POST", h.ts.URL+"/api/v1/uploads/", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", fmt.Sprint(declaredLen))
	req.Header.Set("Upload-Metadata", meta)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return res.StatusCode, ""
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "http") {
		loc = h.ts.URL + loc
	}
	patch, _ := http.NewRequest("PATCH", loc, bytes.NewReader(data))
	patch.Header.Set("Tus-Resumable", "1.0.0")
	patch.Header.Set("Upload-Offset", "0")
	patch.Header.Set("Content-Type", "application/offset+octet-stream")
	patch.Header.Set("Authorization", "Bearer "+token)
	pres, err := http.DefaultClient.Do(patch)
	if err != nil {
		h.t.Fatal(err)
	}
	body, _ := io.ReadAll(pres.Body)
	_ = pres.Body.Close()
	if pres.StatusCode != http.StatusNoContent {
		h.t.Fatalf("PATCH status %d: %s", pres.StatusCode, body)
	}
	return res.StatusCode, loc
}

// waitUpload polls /me/uploads until the newest upload finishes.
func (h *harness) waitUpload(token string) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		r := h.do(nil, "GET", "/me/uploads", nil, bearer(token))
		if len(r.list) > 0 {
			u := r.list[0].(map[string]any)
			if st := u["status"]; st == "done" || st == "failed" {
				return u
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatal("upload did not finish")
	return nil
}

func (h *harness) waitJob(token, id string) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		r := h.do(nil, "GET", "/jobs/"+id, nil, bearer(token))
		if st := r.body["status"]; st == "succeeded" || st == "failed" {
			return r.body
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatal("job did not finish")
	return nil
}

// approvedUser signs up, approves and logs in a user; returns id and token.
func (h *harness) approvedUser(adminTok, name string) (string, string) {
	h.t.Helper()
	expect(h.t, h.do(nil, "POST", "/auth/signup", map[string]any{"username": name, "password": "marble-canyon-whistle",
		"age_confirmed": true, "terms_accepted": true}, nil), 202, "signup "+name)
	reqs := h.do(nil, "GET", "/admin/signup-requests", nil, bearer(adminTok))
	for _, r := range reqs.list {
		m := r.(map[string]any)
		if m["username"] == name {
			expect(h.t, h.do(nil, "POST", "/admin/signup-requests/"+m["id"].(string)+"/approve", nil, bearer(adminTok)), 204, "approve")
		}
	}
	r := h.do(nil, "POST", "/auth/login", map[string]any{"username": name, "password": "marble-canyon-whistle", "device": device("android")}, nil)
	expect(h.t, r, 200, "login "+name)
	return str(r.body, "user", "id"), str(r.body, "tokens", "access_token")
}

func TestLibraryFlow(t *testing.T) { forEachDialect(t, testLibraryFlow) }

func testLibraryFlow(t *testing.T, h *harness) {
	tools := media.DefaultTools()
	if !tools.Available() {
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	lib := filepath.Join(h.dir, "library")
	if err := tools.MakeM4B(ctx, filepath.Join(lib, "Ann Author", "Scanned Book", "scanned.m4b"), media.FixtureBook{
		Title: "Scanned Book", Author: "Ann Author", Seconds: 3, Frequency: 350,
		Chapters: []media.FixtureChapter{{Title: "A", StartMS: 0, EndMS: 1500}, {Title: "B", StartMS: 1500, EndMS: 3000}}}); err != nil {
		t.Fatal(err)
	}
	// Backdate so the scan treats the folder as settled.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(lib, "Ann Author", "Scanned Book", "scanned.m4b"), old, old)

	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)
	aliceID, alice := h.approvedUser(adminTok, "alice")
	_, bob := h.approvedUser(adminTok, "bob")

	// Scan → book in the master store, invisible to users.
	r := h.do(nil, "POST", "/admin/library/scan", nil, bearer(adminTok))
	expect(t, r, 202, "scan")
	if job := h.waitJob(adminTok, str(r.body, "job_id")); job["status"] != "succeeded" {
		t.Fatalf("scan job: %v", job)
	}
	r = h.do(nil, "GET", "/admin/books?q=scanned", nil, bearer(adminTok))
	items, _ := r.body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("master store: %s", r.raw)
	}
	bookID := items[0].(map[string]any)["id"].(string)
	if r := h.do(nil, "GET", "/library", nil, bearer(alice)); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("undistributed book visible: %s", r.raw)
	}
	expect(t, h.do(nil, "GET", "/books/"+bookID, nil, bearer(alice)), 404, "book not visible yet")

	// Distribute to a group containing alice only.
	g := h.do(nil, "POST", "/admin/groups", map[string]any{"name": "Friends"}, bearer(adminTok))
	expect(t, h.do(nil, "PUT", "/admin/groups/"+str(g.body, "id")+"/members/"+aliceID, nil, bearer(adminTok)), 204, "add alice")
	d := h.do(nil, "POST", "/admin/distributions", map[string]any{"book_id": bookID, "target_type": "group", "target_id": str(g.body, "id")}, bearer(adminTok))
	expect(t, d, 201, "distribute")
	if d.body["target_name"] != "Friends" {
		t.Fatalf("distribution: %s", d.raw)
	}
	r = h.do(nil, "GET", "/library", nil, bearer(alice))
	if got := r.body["items"].([]any); len(got) != 1 || got[0].(map[string]any)["source"] != "distribution" {
		t.Fatalf("alice library: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/library", nil, bearer(bob)); len(r.body["items"].([]any)) != 0 {
		t.Fatal("bob must not see a Friends-only book")
	}
	r = h.do(nil, "GET", "/books/"+bookID, nil, bearer(alice))
	expect(t, r, 200, "book detail")
	if _, ok := r.body["has_cover"].(bool); !ok {
		t.Fatalf("has_cover missing: %s", r.raw)
	}
	if chs := r.body["chapters"].([]any); len(chs) != 2 {
		t.Fatalf("chapters: %s", r.raw)
	}
	if files := r.body["files"].([]any); len(files) != 1 || files[0].(map[string]any)["mime_type"] != "audio/mp4" {
		t.Fatalf("files: %s", r.raw)
	}
	authorID := r.body["authors"].([]any)[0].(map[string]any)["id"].(string)
	if ap := h.do(nil, "GET", "/authors/"+authorID, nil, bearer(alice)); len(ap.body["books"].([]any)) != 1 || ap.body["name"] != "Ann Author" {
		t.Fatalf("author page: %s", ap.raw)
	}

	for kind, want := range map[string]string{"authors": "Ann Author", "series": "", "narrators": "", "tags": ""} {
		r := h.do(nil, "GET", "/library/facets?kind="+kind, nil, bearer(alice))
		expect(t, r, 200, "facets "+kind)
		if want != "" && (len(r.list) != 1 || r.list[0].(map[string]any)["name"] != want || r.list[0].(map[string]any)["count"] != float64(1)) {
			t.Fatalf("facets %s: %s", kind, r.raw)
		}
	}
	if r := h.do(nil, "GET", "/library/facets?kind=authors", nil, bearer(bob)); strings.TrimSpace(r.raw) != "[]" {
		t.Fatalf("bob must not see facets of books he can't see: %q", r.raw)
	}
	expect(t, h.do(nil, "GET", "/library/facets?kind=bogus", nil, bearer(alice)), 400, "bad facet kind")

	// Remove → recently removed → restore; hide → archived.
	expect(t, h.do(nil, "DELETE", "/me/library/"+bookID, nil, bearer(alice)), 204, "remove")
	if r := h.do(nil, "GET", "/library?shelf=recently_removed", nil, bearer(alice)); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("recently removed: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/me/library/"+bookID+"/restore", nil, bearer(alice)), 204, "restore")
	expect(t, h.do(nil, "POST", "/me/library/"+bookID+"/hide", nil, bearer(alice)), 204, "hide")
	if r := h.do(nil, "GET", "/library?shelf=archived", nil, bearer(alice)); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("archived: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/me/library/"+bookID+"/restore", nil, bearer(alice)), 204, "unarchive")

	// Upload (tus) as bob.
	src := filepath.Join(t.TempDir(), "up.m4b")
	if err := tools.MakeM4B(ctx, src, media.FixtureBook{Title: "Bob's Upload", Author: "Bo Author", Seconds: 2, Frequency: 800}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(src)
	if st, _ := h.tusUpload(bob, "notes.txt", data, len(data)); st != http.StatusUnsupportedMediaType {
		t.Fatalf("txt upload status %d", st)
	}
	st, loc := h.tusUpload(bob, "up.m4b", data, len(data))
	if st != http.StatusCreated {
		t.Fatalf("create status %d", st)
	}
	u := h.waitUpload(bob)
	if u["status"] != "done" || u["book_id"] == nil {
		t.Fatalf("upload: %v", u)
	}
	// Someone else can't touch bob's upload.
	head, _ := http.NewRequest("HEAD", loc, nil)
	head.Header.Set("Tus-Resumable", "1.0.0")
	head.Header.Set("Authorization", "Bearer "+alice)
	if res, _ := http.DefaultClient.Do(head); res.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign HEAD status %d", res.StatusCode)
	}
	if r := h.do(nil, "GET", "/library", nil, bearer(bob)); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("bob's library after upload: %s", r.raw)
	}
	if me := h.do(nil, "GET", "/me", nil, bearer(bob)); me.body["used_bytes"].(float64) != float64(len(data)) {
		t.Fatalf("used_bytes: %v want %d", me.body["used_bytes"], len(data))
	}

	// Quota: 1 byte left → refused before any data is sent.
	expect(t, h.do(nil, "PATCH", "/admin/users/"+aliceID, map[string]any{"reason": "test", "quota_bytes": 10}, bearer(adminTok)), 200, "set quota")
	if st, _ := h.tusUpload(alice, "big.m4b", data, len(data)); st != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota status %d", st)
	}

	// Trash hides the scanned book from alice; empty trash needs password + TOTP + count.
	expect(t, h.do(nil, "DELETE", "/admin/books/"+bookID, nil, bearer(adminTok)), 204, "trash")
	if r := h.do(nil, "GET", "/library", nil, bearer(alice)); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("trashed book still visible: %s", r.raw)
	}
	code := func() string { return totpStep(secret, 1) } // login used the current step
	expect(t, h.do(nil, "POST", "/admin/trash/empty", map[string]any{"confirm_count": 2, "password": adminPassword, "totp_code": code()}, bearer(adminTok)), 400, "wrong count")
	expect(t, h.do(nil, "POST", "/admin/trash/empty", map[string]any{"confirm_count": 1, "password": "wrong-password-x", "totp_code": code()}, bearer(adminTok)), 401, "wrong password")
	r = h.do(nil, "POST", "/admin/trash/empty", map[string]any{"confirm_count": 1, "password": adminPassword, "totp_code": code()}, bearer(adminTok))
	expect(t, r, 200, "empty trash")
	if r.body["purged"].(float64) != 1 {
		t.Fatalf("purged: %s", r.raw)
	}
	if _, err := os.Stat(filepath.Join(lib, "Ann Author", "Scanned Book")); !os.IsNotExist(err) {
		t.Fatal("purged book's files still on disk")
	}
	if _, err := store.GetBook(ctx, h.srv.App().DB, bookID); err == nil {
		t.Fatal("purged book still in DB")
	}
}

func TestLibraryFilterValidation(t *testing.T) { forEachDialect(t, testLibraryFilterValidation) }

// Unknown filter values are the client's mistake (400), not a server error.
func testLibraryFilterValidation(t *testing.T, h *harness) {
	A := bearer(h.adminLogin(h.setupInstance()))
	expect(t, h.do(nil, "GET", "/library?shelf=ALL", nil, A), 400, "unknown shelf")
	expect(t, h.do(nil, "GET", "/library?sort=bogus", nil, A), 400, "unknown sort")
	expect(t, h.do(nil, "GET", "/library/facets?kind=bogus", nil, A), 400, "unknown facet")
	expect(t, h.do(nil, "GET", "/library?shelf=all&sort=recent_added", nil, A), 200, "valid")
}
