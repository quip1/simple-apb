// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"

	"github.com/quip1/simple-apb/server/internal/store"
)

func TestSocialAndModeration(t *testing.T) { forEachDialect(t, testSocialAndModeration) }

func testSocialAndModeration(t *testing.T, h *harness) {
	ctx := context.Background()
	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	samID, sam := h.approvedUser(admin, "sam")
	kimID, kim := h.approvedUser(admin, "kim")
	leeID, lee := h.approvedUser(admin, "lee")
	S, K, L := bearer(sam), bearer(kim), bearer(lee)
	db := h.srv.App().DB

	// Sam uploaded a book; nobody else sees it yet.
	bookID, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Shared Tale"}, FolderPath: h.bookFolder("s/Shared Tale"),
		Origin: "upload", Status: "ready", UploaderID: samID, DurationMS: 60_000, Checksum: "c-shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(ctx, db, samID, bookID, "own_upload"); err != nil {
		t.Fatal(err)
	}
	if r := h.do(nil, "GET", "/books/"+bookID, nil, S); r.body["uploaded_by_me"] != true {
		t.Fatalf("uploaded_by_me: %s", r.raw)
	}
	expect(t, h.do(nil, "GET", "/books/"+bookID, nil, K), 404, "not shared yet")

	// ── sharing (brief §6.5 rules 5–6) ──
	share := func(tok map[string]string, typ, id string) resp {
		return h.do(nil, "POST", "/books/"+bookID+"/shares", map[string]any{"target_type": typ, "target_id": id}, tok)
	}
	expect(t, share(S, "user", kimID), 403, "share without can_share")
	expect(t, h.do(nil, "PUT", "/admin/users/"+samID+"/permissions", map[string]any{"can_share": true}, A), 204, "grant share")
	expect(t, share(S, "user", samID), 400, "share with self")
	r := share(S, "user", kimID)
	if r.status != 201 || r.body["status"] != "shared" {
		t.Fatalf("share to kim: %d %s", r.status, r.raw)
	}
	expect(t, h.do(nil, "GET", "/books/"+bookID, nil, K), 200, "kim sees it")
	expect(t, share(K, "user", leeID), 403, "kim is not the uploader")

	var publicID string
	for _, g := range h.do(nil, "GET", "/me/groups", nil, S).list {
		if gm := g.(map[string]any); gm["public"] == true {
			publicID = gm["id"].(string)
		}
	}
	if publicID == "" {
		t.Fatal("sam should be in Public")
	}
	expect(t, share(S, "group", publicID), 403, "Public needs can_post_public")
	r = h.do(nil, "POST", "/admin/groups", map[string]any{"name": "Secret"}, A)
	secretGroup := str(r.body, "id")
	expect(t, share(S, "group", secretGroup), 403, "group sam isn't in")

	expect(t, h.do(nil, "PUT", "/admin/users/"+samID+"/permissions", map[string]any{"can_share": true, "can_post_public": true}, A), 204, "grant public")
	r = share(S, "group", publicID)
	if r.status != 201 || r.body["status"] != "pending_review" {
		t.Fatalf("share to Public: %d %s", r.status, r.raw)
	}
	reqID := str(r.body, "id")
	if again := share(S, "group", publicID); str(again.body, "id") != reqID {
		t.Fatalf("re-request should be idempotent: %s", again.raw)
	}
	expect(t, h.do(nil, "GET", "/books/"+bookID, nil, L), 404, "pending review is not visible")
	r = h.do(nil, "GET", "/books/"+bookID+"/shares", nil, S)
	if len(r.body["shares"].([]any)) != 1 || len(r.body["requests"].([]any)) != 1 {
		t.Fatalf("shares: %s", r.raw)
	}
	expect(t, h.do(nil, "GET", "/books/"+bookID+"/shares", nil, K), 403, "shares of someone else's book")

	expect(t, h.do(nil, "GET", "/admin/review-queue", nil, S), 403, "queue as user")
	r = h.do(nil, "GET", "/admin/review-queue", nil, A)
	if items := r.body["items"].([]any); len(items) != 1 || str(items[0].(map[string]any), "uploader", "username") != "sam" ||
		str(items[0].(map[string]any), "target", "name") != "Public" {
		t.Fatalf("queue: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/admin/dashboard", nil, A); r.body["inbox"].(map[string]any)["review_queue"] != float64(1) {
		t.Fatalf("inbox count: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/admin/review-queue/"+reqID+"/reject", map[string]any{}, A), 400, "reject without reason")
	expect(t, h.do(nil, "POST", "/admin/review-queue/"+reqID+"/approve", nil, A), 204, "approve")
	expect(t, h.do(nil, "POST", "/admin/review-queue/"+reqID+"/approve", nil, A), 409, "approve twice")
	expect(t, h.do(nil, "POST", "/admin/review-queue/"+reqID+"/reject", map[string]any{"reason": "late"}, A), 409, "reject decided")
	expect(t, h.do(nil, "GET", "/books/"+bookID, nil, L), 200, "public after approval")
	expect(t, share(S, "group", publicID), 409, "already public")
	r = h.do(nil, "GET", "/books/"+bookID+"/shares", nil, S)
	if len(r.body["shares"].([]any)) != 2 || str(r.body["requests"].([]any)[0].(map[string]any), "status") != "approved" {
		t.Fatalf("shares after approval: %s", r.raw)
	}

	// A rejected request carries the reason back to the uploader.
	book2, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Second"}, FolderPath: h.bookFolder("s/Second"),
		Origin: "upload", Status: "ready", UploaderID: samID, DurationMS: 60_000, Checksum: "c-second"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AddEntry(ctx, db, samID, book2, "own_upload")
	r = h.do(nil, "POST", "/books/"+book2+"/shares", map[string]any{"target_type": "group", "target_id": publicID}, S)
	req2 := str(r.body, "id")
	expect(t, h.do(nil, "POST", "/admin/review-queue/"+req2+"/reject", map[string]any{"reason": "low audio quality"}, A), 204, "reject")
	if r := h.do(nil, "GET", "/books/"+book2+"/shares", nil, S); str(r.body["requests"].([]any)[0].(map[string]any), "reason") != "low audio quality" {
		t.Fatalf("reject reason: %s", r.raw)
	}
	// Withdrawing a pending request, and revoking a direct share.
	r = h.do(nil, "POST", "/books/"+book2+"/shares", map[string]any{"target_type": "group", "target_id": publicID}, S)
	expect(t, h.do(nil, "DELETE", "/books/"+book2+"/shares/"+str(r.body, "id"), nil, S), 204, "withdraw")
	r = h.do(nil, "POST", "/books/"+book2+"/shares", map[string]any{"target_type": "user", "target_id": leeID}, S)
	expect(t, h.do(nil, "GET", "/books/"+book2, nil, L), 200, "lee sees second")
	expect(t, h.do(nil, "DELETE", "/books/"+book2+"/shares/"+str(r.body, "id"), nil, K), 404, "someone else's share")
	expect(t, h.do(nil, "DELETE", "/books/"+book2+"/shares/"+str(r.body, "id"), nil, S), 204, "revoke")
	expect(t, h.do(nil, "GET", "/books/"+book2, nil, L), 404, "revoked")

	// ── ratings and reviews ──
	review := func(tok map[string]string, stars int, text string) resp {
		return h.do(nil, "PUT", "/books/"+bookID+"/reviews/mine", map[string]any{"stars": stars, "text": text}, tok)
	}
	expect(t, review(K, 6, ""), 400, "six stars")
	expect(t, review(K, 4, strings.Repeat("é", 501)), 400, "too long")
	r = review(K, 4, "Great listen")
	if r.status != 200 || r.body["mine"] != true || r.body["stars"] != float64(4) {
		t.Fatalf("review: %d %s", r.status, r.raw)
	}
	kimReview := str(r.body, "id")
	expect(t, review(L, 2, ""), 200, "lee rates")
	if r := review(K, 5, "Even better the second time"); str(r.body, "id") != kimReview {
		t.Fatalf("editing replaces the review: %s", r.raw)
	}
	r = h.do(nil, "GET", "/books/"+bookID+"/reviews", nil, S)
	sum := r.body["summary"].(map[string]any)
	if len(r.body["items"].([]any)) != 2 || sum["count"] != float64(2) || sum["average"] != 3.5 || r.body["mine"] != nil {
		t.Fatalf("reviews: %s", r.raw)
	}
	if hist := sum["histogram"].([]any); hist[1] != float64(1) || hist[4] != float64(1) {
		t.Fatalf("histogram: %v", hist)
	}

	// ── reports ──
	report := func(tok map[string]string, typ, id, reason string) resp {
		return h.do(nil, "POST", "/reports", map[string]any{"object_type": typ, "object_id": id, "reason": reason, "details": "please look"}, tok)
	}
	expect(t, report(L, "review", kimReview, "nonsense"), 400, "bad reason")
	expect(t, report(K, "review", kimReview, "spam"), 400, "own review")
	expect(t, report(L, "user", leeID, "spam"), 400, "self report")
	r = report(L, "review", kimReview, "spam")
	expect(t, r, 201, "report review")
	repID := str(r.body, "id")
	if again := report(L, "review", kimReview, "harassment"); str(again.body, "id") != repID {
		t.Fatalf("re-report should update: %s", again.raw)
	}
	expect(t, report(S, "review", kimReview, "inappropriate"), 201, "second reporter")
	r = h.do(nil, "GET", "/admin/reports", nil, A)
	items := r.body["items"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 2 || first["object_label"] != "Shared Tale" || str(first, "review", "text") != "Even better the second time" ||
		first["reason"] != "harassment" || str(first, "reporter", "username") != "lee" {
		t.Fatalf("reports: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/admin/reports/"+repID+"/resolve", map[string]any{"action": "hide_review", "note": " "}, A), 400, "no note")
	expect(t, h.do(nil, "POST", "/admin/reports/"+repID+"/resolve", map[string]any{"action": "hide_review", "note": "spoilers"}, A), 204, "hide")
	expect(t, h.do(nil, "POST", "/admin/reports/"+repID+"/resolve", map[string]any{"action": "dismiss", "note": "x"}, A), 409, "closed")
	if r := h.do(nil, "GET", "/admin/reports", nil, A); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("both reports on the review should close: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/admin/reports?status=resolved", nil, A); len(r.body["items"].([]any)) != 2 ||
		r.body["items"].([]any)[0].(map[string]any)["resolution"] != "hide_review" {
		t.Fatalf("resolved: %s", r.raw)
	}
	r = h.do(nil, "GET", "/books/"+bookID+"/reviews", nil, S)
	if len(r.body["items"].([]any)) != 1 || r.body["summary"].(map[string]any)["average"] != float64(2) {
		t.Fatalf("hidden review must drop out: %s", r.raw)
	}
	r = h.do(nil, "GET", "/books/"+bookID+"/reviews", nil, K)
	if mine := r.body["mine"].(map[string]any); mine["hidden"] != true || mine["hidden_reason"] != nil || len(r.body["items"].([]any)) != 2 {
		t.Fatalf("author still sees it, without the reason: %s", r.raw)
	}
	r = h.do(nil, "GET", "/books/"+bookID+"/reviews", nil, A)
	if len(r.body["items"].([]any)) != 2 {
		t.Fatalf("admin sees hidden: %s", r.raw)
	}
	expect(t, h.do(nil, "DELETE", "/admin/reviews/"+kimReview+"/hide", nil, A), 204, "unhide")
	expect(t, h.do(nil, "POST", "/admin/reviews/"+kimReview+"/hide", map[string]any{}, A), 400, "hide without reason")

	// Reporting a person; a takedown is only for books.
	r = report(L, "user", samID, "harassment")
	userRep := str(r.body, "id")
	expect(t, h.do(nil, "POST", "/admin/reports/"+userRep+"/resolve", map[string]any{"action": "takedown", "note": "x"}, A), 400, "takedown a person")
	expect(t, h.do(nil, "POST", "/admin/reports/"+userRep+"/resolve", map[string]any{"action": "resolved", "note": "warned"}, A), 204, "resolved")

	// Reporting a book and taking it down from the report.
	r = report(K, "book", bookID, "copyright")
	expect(t, h.do(nil, "POST", "/admin/reports/"+str(r.body, "id")+"/resolve", map[string]any{"action": "takedown", "note": "DMCA notice"}, A), 204, "takedown")
	expect(t, h.do(nil, "GET", "/books/"+bookID, nil, K), 404, "taken down")
	if r := h.do(nil, "GET", "/admin/books/"+bookID, nil, A); str(r.body, "takedown", "reason") != "DMCA notice" {
		t.Fatalf("takedown from report: %s", r.raw)
	}
	expect(t, h.do(nil, "DELETE", "/admin/books/"+bookID+"/takedown", nil, A), 204, "lift")

	// Restricted users are read-only: no reports, reviews or recommendations.
	expect(t, h.do(nil, "PATCH", "/admin/users/"+leeID, map[string]any{"status": "restricted", "reason": "cool off"}, A), 200, "restrict")
	expect(t, report(L, "book", bookID, "spam"), 403, "restricted report")
	expect(t, review(L, 1, ""), 403, "restricted review")
	expect(t, h.do(nil, "PATCH", "/admin/users/"+leeID, map[string]any{"status": "active", "reason": "ok"}, A), 200, "reactivate")
	r = h.do(nil, "POST", "/auth/login", map[string]any{"username": "lee", "password": "marble-canyon-whistle", "device": device("android")}, nil)
	L = bearer(str(r.body, "tokens", "access_token"))

	// ── recommendations ──
	private, err := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Kim Only"}, FolderPath: h.bookFolder("k/Kim Only"),
		Origin: "scan", Status: "ready", DurationMS: 60_000, Checksum: "c-kim"})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, h.do(nil, "POST", "/admin/distributions", map[string]any{"book_id": private, "target_type": "user", "target_id": kimID}, A), 201, "kim only")
	r = h.do(nil, "POST", "/books/"+private+"/recommend", map[string]any{"user_ids": []string{leeID}}, K)
	if r.status != 200 || r.body["sent"] != float64(0) || r.body["skipped"] != float64(1) {
		t.Fatalf("recommend invisible book: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/books/"+bookID+"/recommend", map[string]any{"user_ids": []string{kimID}}, K), 400, "only yourself")
	r = h.do(nil, "POST", "/books/"+bookID+"/recommend", map[string]any{"user_ids": []string{leeID, kimID, leeID}, "note": "you'll love it"}, K)
	if r.body["sent"] != float64(1) || r.body["skipped"] != float64(0) {
		t.Fatalf("recommend: %s", r.raw)
	}
	r = h.do(nil, "GET", "/me/recommendations", nil, L)
	if len(r.list) != 1 || str(r.list[0].(map[string]any), "from", "username") != "kim" ||
		str(r.list[0].(map[string]any), "book", "title") != "Shared Tale" || r.list[0].(map[string]any)["note"] != "you'll love it" {
		t.Fatalf("my recommendations: %s", r.raw)
	}
	recID := str(r.list[0].(map[string]any), "id")
	expect(t, h.do(nil, "DELETE", "/me/recommendations/"+recID, nil, K), 404, "someone else's recommendation")
	expect(t, h.do(nil, "DELETE", "/me/recommendations/"+recID, nil, L), 204, "dismiss")
	if r := h.do(nil, "GET", "/me/recommendations", nil, L); len(r.list) != 0 {
		t.Fatalf("dismissed: %s", r.raw)
	}

	// ── people search ──
	expect(t, h.do(nil, "GET", "/people?q=k", nil, S), 400, "too short")
	if r := h.do(nil, "GET", "/people?q=KI", nil, S); len(r.list) != 1 || r.list[0].(map[string]any)["username"] != "kim" {
		t.Fatalf("people: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/people?q=sa", nil, S); len(r.list) != 0 {
		t.Fatalf("you don't find yourself: %s", r.raw)
	}

	// ── who's listening ──
	if err := store.UpsertProgress(ctx, db, store.ProgressWrite{UserID: kimID, BookID: bookID, DeviceID: "d1", PositionMS: 30_000,
		ClientTS: store.NowMS(), Speed: 1}); err != nil {
		t.Fatal(err)
	}
	r = h.do(nil, "GET", "/books/"+bookID+"/listeners", nil, S)
	if len(r.list) != 1 || str(r.list[0].(map[string]any), "user", "username") != "kim" || r.list[0].(map[string]any)["state"] != "listening" {
		t.Fatalf("listeners: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/books/"+bookID+"/listeners", nil, K); len(r.list) != 0 {
		t.Fatalf("you don't see yourself: %s", r.raw)
	}
	expect(t, h.do(nil, "PATCH", "/me", map[string]any{"listening_visible": false}, K), 200, "opt out")
	if r := h.do(nil, "GET", "/books/"+bookID+"/listeners", nil, S); len(r.list) != 0 {
		t.Fatalf("opted-out listener shown: %s", r.raw)
	}

	// ── sharing collections ──
	r = h.do(nil, "POST", "/me/collections", map[string]any{"name": "Kim's picks"}, K)
	colID := str(r.body, "id")
	for _, b := range []string{bookID, private} {
		expect(t, h.do(nil, "PUT", "/me/collections/"+colID+"/books/"+b, nil, K), 204, "add to collection")
	}
	expect(t, h.do(nil, "POST", "/me/collections/"+colID+"/shares", map[string]any{"target_type": "group", "target_id": secretGroup}, K), 403, "other group")
	expect(t, h.do(nil, "POST", "/me/collections/"+colID+"/shares", map[string]any{"target_type": "user", "target_id": leeID}, S), 404, "not your collection")
	r = h.do(nil, "POST", "/me/collections/"+colID+"/shares", map[string]any{"target_type": "user", "target_id": leeID}, K)
	expect(t, r, 201, "share collection")
	colShare := str(r.body, "id")
	if r.body["target_name"] != "lee" {
		t.Fatalf("collection share: %s", r.raw)
	}
	r = h.do(nil, "GET", "/shared-collections", nil, L)
	if len(r.list) != 1 {
		t.Fatalf("shared collections: %s", r.raw)
	}
	sc := r.list[0].(map[string]any)
	if ids := sc["book_ids"].([]any); len(ids) != 1 || ids[0] != bookID || str(sc, "owner", "username") != "kim" {
		t.Fatalf("lee must only see books lee can see: %s", r.raw)
	}
	if r := h.do(nil, "GET", "/me/collections/"+colID+"/shares", nil, K); len(r.list) != 1 {
		t.Fatalf("collection shares: %s", r.raw)
	}
	expect(t, h.do(nil, "DELETE", "/me/collections/"+colID+"/shares/"+colShare, nil, K), 204, "unshare collection")
	if r := h.do(nil, "GET", "/shared-collections", nil, L); len(r.list) != 0 {
		t.Fatalf("unshared: %s", r.raw)
	}

	// Deleting your review.
	expect(t, h.do(nil, "DELETE", "/books/"+bookID+"/reviews/mine", nil, K), 204, "delete review")
	expect(t, h.do(nil, "DELETE", "/books/"+bookID+"/reviews/mine", nil, K), 404, "delete again")

	// Everything above is in the audit log.
	r = h.do(nil, "GET", "/admin/audit?limit=200", nil, A)
	seen := map[string]bool{}
	for _, e := range r.body["items"].([]any) {
		seen[e.(map[string]any)["action"].(string)] = true
	}
	for _, a := range []string{"share.create", "share.request", "share.withdraw", "share.revoke", "review_queue.approve", "review_queue.reject",
		"review.create", "review.update", "review.hide", "review.unhide", "review.delete", "report.create", "report.resolve",
		"book.recommend", "collection.share", "collection.unshare"} {
		if !seen[a] {
			t.Errorf("audit log is missing %s", a)
		}
	}
}
