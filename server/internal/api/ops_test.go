// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"os"
	osexec "os/exec"
	"testing"

	"github.com/google/uuid"

	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

func TestOpsFlow(t *testing.T) { forEachDialect(t, testOpsFlow) }

func testOpsFlow(t *testing.T, h *harness) {
	ctx := context.Background()
	secret := h.setupInstance()
	adminTok := h.adminLogin(secret)
	db := h.srv.App().DB
	pushJobs := func() int {
		var n int
		_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM jobs WHERE type = ?`, push.JobPush).Scan(&n)
		return n
	}

	// Push registration: unsafe endpoints refused, a server topic otherwise.
	dev := uuid.NewString()
	expect(t, h.do(nil, "POST", "/devices/push-subscription", map[string]any{"device_id": dev, "endpoint": "http://169.254.169.254/latest"}, bearer(adminTok)), 400, "unsafe endpoint")
	r := h.do(nil, "POST", "/devices/push-subscription", map[string]any{"device_id": dev}, bearer(adminTok))
	expect(t, r, 200, "register push")
	if topic := str(r.body, "topic"); len(topic) != 36 {
		t.Fatalf("topic %q", topic)
	}

	// A sign-up queues an admin push; a distribution queues a library push.
	before := pushJobs()
	aliceID, _ := h.approvedUser(adminTok, "alice")
	if pushJobs() != before+1 {
		t.Fatalf("sign-up should queue one admin push (%d → %d)", before, pushJobs())
	}
	bookID, _ := store.InsertBook(ctx, db, &store.NewBook{Meta: store.BookMeta{Title: "Pushy"}, FolderPath: "p/Pushy",
		Origin: "scan", Status: "ready", Checksum: "c-pushy"})
	expect(t, h.do(nil, "POST", "/admin/distributions", map[string]any{"book_id": bookID, "target_type": "user", "target_id": aliceID}, bearer(adminTok)), 201, "distribute")
	if pushJobs() != before+2 {
		t.Fatalf("distribution should queue a push")
	}

	// Backups on demand (Postgres needs pg_dump on PATH or SIMPLEAPB_PG_DUMP).
	if h.db["dialect"] == "postgres" && !havePGDump() {
		t.Log("pg_dump not installed: skipping the Postgres backup step")
		return
	}
	r = h.do(nil, "POST", "/admin/backups", nil, bearer(adminTok))
	expect(t, r, 202, "backup")
	if job := h.waitJob(adminTok, str(r.body, "job_id")); job["status"] != "succeeded" {
		t.Fatalf("backup job: %v", job)
	}
	st := h.do(nil, "GET", "/admin/backups", nil, bearer(adminTok))
	if len(st.body["backups"].([]any)) != 1 || st.body["last_backup"].(map[string]any)["ok"] != true {
		t.Fatalf("backup status: %s", st.raw)
	}

	// Integrity check on demand (no files: the book above is a bare record → reported missing and quarantined).
	r = h.do(nil, "POST", "/admin/integrity-check", nil, bearer(adminTok))
	expect(t, r, 202, "integrity check")
	h.waitJob(adminTok, str(r.body, "job_id"))
	st = h.do(nil, "GET", "/admin/backups", nil, bearer(adminTok))
	if ls := st.body["last_scrub"].(map[string]any); ls["ok"] != false {
		t.Fatalf("scrub should flag the book without files: %s", st.raw)
	}
	if b, _ := store.GetBook(ctx, db, bookID); b.Status != "quarantined" {
		t.Fatalf("book status %s", b.Status)
	}

	// Crash recovery path: records its outcome.
	h.srv.App().RecoverAfterCrash(ctx)
	st = h.do(nil, "GET", "/admin/backups", nil, bearer(adminTok))
	if lu := st.body["last_unclean_shutdown"].(map[string]any); lu["ok"] != true {
		t.Fatalf("recovery status: %s", st.raw)
	}
	expect(t, h.do(nil, "GET", "/admin/backups", nil, bearer(h.deviceLoginToken("alice"))), 403, "non-admin backups")
}

func (h *harness) deviceLoginToken(user string) string {
	return h.deviceLogin(user, uuid.NewString(), "android")
}

func havePGDump() bool {
	bin := os.Getenv("SIMPLEAPB_PG_DUMP")
	if bin == "" {
		bin = "pg_dump"
	}
	_, err := osexec.LookPath(bin)
	return err == nil
}
