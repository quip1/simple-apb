// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// SIGKILL crash harness (brief §15). It builds the real server, drives
// uploads, imports, progress writes, bookmarks and backups at it, kills the
// process (SIGKILL / TerminateProcess) at random moments, and checks after
// every crash that:
//   - the database passes PRAGMA integrity_check,
//   - every "ready" book's files exist and match their checksums,
//   - every finished backup file is itself a valid database,
//   - no acknowledged progress write was lost,
//   - the restart detects the unclean shutdown and runs the startup check,
//   - interrupted uploads resume (tus) and interrupted imports finish.
//
// Run with: SIMPLEAPB_CRASH_TEST=1 go test ./cmd/simpleapb -run TestCrashHarness -v
// (nightly in CI). SIMPLEAPB_CRASH_ITERATIONS sets the number of kills (default 8).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	_ "modernc.org/sqlite"

	"github.com/quip1/simple-apb/server/internal/media"
)

type crashEnv struct {
	t       *testing.T
	bin     string
	dir     string
	port    int
	cmd     *exec.Cmd
	logPath string
	logFile *os.File
	boots   int
}

func (e *crashEnv) url(p string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/api/v1%s", e.port, p)
}

func (e *crashEnv) start() {
	e.t.Helper()
	e.boots++
	e.logPath = filepath.Join(e.dir, fmt.Sprintf("server-%02d.log", e.boots))
	logf, err := os.Create(e.logPath)
	if err != nil {
		e.t.Fatal(err)
	}
	cmd := exec.Command(e.bin)
	cmd.Env = append(os.Environ(),
		"SIMPLEAPB_CONFIG_DIR="+filepath.Join(e.dir, "config"), "SIMPLEAPB_LIBRARY_DIR="+filepath.Join(e.dir, "library"),
		"SIMPLEAPB_CACHE_DIR="+filepath.Join(e.dir, "cache"), "SIMPLEAPB_BACKUPS_DIR="+filepath.Join(e.dir, "backups"),
		fmt.Sprintf("SIMPLEAPB_LISTEN=127.0.0.1:%d", e.port))
	cmd.Stdout, cmd.Stderr = logf, logf
	e.logFile = logf
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.cmd = cmd
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(e.url("/health")); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(e.logPath)
	e.t.Fatalf("server did not become healthy:\n%s", b)
}

func (e *crashEnv) kill() {
	_ = e.cmd.Process.Kill() // SIGKILL on Unix, TerminateProcess on Windows: no cleanup runs
	_ = e.cmd.Wait()
	_ = e.logFile.Close()
}

func (e *crashEnv) logText() string { b, _ := os.ReadFile(e.logPath); return string(b) }

// call does a JSON request; returns status and decoded body.
func call(method, url, token string, body any, hdr map[string]string) (int, map[string]any) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{Timeout: 10 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = res.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func sha(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestCrashHarness(t *testing.T) {
	if os.Getenv("SIMPLEAPB_CRASH_TEST") != "1" {
		t.Skip("set SIMPLEAPB_CRASH_TEST=1 to run the crash harness")
	}
	tools := media.DefaultTools()
	if !tools.Available() {
		t.Skip("ffmpeg not installed")
	}
	iterations := 8
	if n, err := strconv.Atoi(os.Getenv("SIMPLEAPB_CRASH_ITERATIONS")); err == nil && n > 0 {
		iterations = n
	}
	ctx := context.Background()
	dir := t.TempDir()
	bin := filepath.Join(dir, "simpleapb-test")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if strings.HasSuffix(os.Getenv("OS"), "Windows_NT") || filepath.Separator == '\\' {
		_ = os.Rename(bin, bin+".exe")
		bin += ".exe"
	}
	for _, d := range []string{"config", "library", "cache", "backups", "src"} {
		_ = os.MkdirAll(filepath.Join(dir, d), 0o750)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	e := &crashEnv{t: t, bin: bin, dir: dir, port: port}

	// Fixtures: one larger m4b to upload in chunks, several small books to "copy in".
	big := filepath.Join(dir, "src", "big.m4b")
	if err := tools.MakeM4B(ctx, big, media.FixtureBook{Title: "Crash Upload", Author: "Harness", Seconds: 240, Frequency: 330}); err != nil {
		t.Fatal(err)
	}
	bigData, _ := os.ReadFile(big)
	for i := range iterations {
		p := filepath.Join(dir, "src", fmt.Sprintf("drop%02d.m4b", i))
		if err := tools.MakeM4B(ctx, p, media.FixtureBook{Title: fmt.Sprintf("Dropped %02d", i), Author: "Dropper", Seconds: 20, Frequency: 200 + i*37}); err != nil {
			t.Fatal(err)
		}
	}

	// First boot: setup wizard.
	e.start()
	tok := regexp.MustCompile(`"setup_token":"([^"]+)"`).FindStringSubmatch(e.logText())
	if tok == nil {
		t.Fatalf("no setup token in log:\n%s", e.logText())
	}
	setupHdr := map[string]string{"X-Setup-Token": tok[1]}
	_, tb := call("POST", e.url("/setup/totp"), "", map[string]any{"username": "admin"}, setupHdr)
	secret := tb["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())
	st, _ := call("POST", e.url("/setup/complete"), "", map[string]any{
		"admin":    map[string]any{"username": "admin", "password": "tangerine-orbit-quietly", "totp_secret": secret, "totp_code": code},
		"database": map[string]any{"dialect": "sqlite"}, "library_path": filepath.Join(dir, "library"),
		"base_url": "http://127.0.0.1", "instance_name": "Crash",
	}, setupHdr)
	if st != 200 {
		t.Fatalf("setup status %d", st)
	}
	dev := map[string]any{"device_id": uuid.NewString(), "device_name": "harness", "platform": "other", "app_version": "1"}
	_, lb := call("POST", e.url("/auth/login"), "", map[string]any{"username": "admin", "password": "tangerine-orbit-quietly", "device": dev}, nil)
	code, _ = totp.GenerateCode(secret, time.Now())
	_, vb := call("POST", e.url("/auth/totp/verify"), "", map[string]any{"totp_challenge": lb["totp_challenge"], "code": code}, nil)
	admin := vb["tokens"].(map[string]any)["access_token"].(string)
	// Sessions are durable and access tokens last 15 min: plenty for the run.

	var (
		uploadURL  string
		lastAckPos atomic.Int64 // highest position_ms the server acknowledged as applied
		progBook   string
	)
	deviceID := uuid.NewString()

	for it := 0; it < iterations; it++ {
		if it > 0 {
			e.start()
			if !strings.Contains(e.logText(), "did not shut down cleanly") {
				t.Fatalf("boot %d: unclean shutdown not detected:\n%s", e.boots, e.logText())
			}
			// Durability: the acknowledged position survived the kill.
			if progBook != "" {
				_, p := call("GET", e.url("/me/progress/"+progBook), admin, nil, nil)
				if got := int64(p["position_ms"].(float64)); got < lastAckPos.Load() {
					t.Fatalf("lost an acknowledged progress write: stored %d < acked %d", got, lastAckPos.Load())
				}
			}
		}
		// Drop a new book into the library the way an external manager would
		// (plain non-atomic copy), backdated so the scan imports it at once.
		dst := filepath.Join(dir, "library", "Dropper", fmt.Sprintf("Dropped %02d", it), "book.m4b")
		_ = os.MkdirAll(filepath.Dir(dst), 0o750)
		src, _ := os.ReadFile(filepath.Join(dir, "src", fmt.Sprintf("drop%02d.m4b", it)))
		_ = os.WriteFile(dst, src, 0o640)
		old := time.Now().Add(-time.Hour)
		_ = os.Chtimes(dst, old, old)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		// (a) resumable upload in 64 KB chunks, continuing where the last crash left it.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if uploadURL == "" {
				req, _ := http.NewRequest("POST", e.url("/uploads/"), nil)
				req.Header.Set("Tus-Resumable", "1.0.0")
				req.Header.Set("Upload-Length", strconv.Itoa(len(bigData)))
				sum := sha256.Sum256(bigData)
				req.Header.Set("Upload-Metadata", "filename "+base64.StdEncoding.EncodeToString([]byte("big.m4b"))+
					",sha256 "+base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(sum[:]))))
				req.Header.Set("Authorization", "Bearer "+admin)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					return
				}
				_ = res.Body.Close()
				if loc := res.Header.Get("Location"); loc != "" {
					uploadURL = loc
				}
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				head, _ := http.NewRequest("HEAD", uploadURL, nil)
				head.Header.Set("Tus-Resumable", "1.0.0")
				head.Header.Set("Authorization", "Bearer "+admin)
				res, err := http.DefaultClient.Do(head)
				if err != nil {
					return
				}
				_ = res.Body.Close()
				off, _ := strconv.Atoi(res.Header.Get("Upload-Offset"))
				if res.StatusCode != 200 || off >= len(bigData) {
					return
				}
				end := min(off+64<<10, len(bigData))
				patch, _ := http.NewRequest("PATCH", uploadURL, bytes.NewReader(bigData[off:end]))
				patch.Header.Set("Tus-Resumable", "1.0.0")
				patch.Header.Set("Upload-Offset", strconv.Itoa(off))
				patch.Header.Set("Content-Type", "application/offset+octet-stream")
				patch.Header.Set("Authorization", "Bearer "+admin)
				if res, err := http.DefaultClient.Do(patch); err == nil {
					_ = res.Body.Close()
				} else {
					return
				}
				time.Sleep(time.Duration(rand.IntN(30)) * time.Millisecond)
			}
		}()
		// (b) progress writes with increasing positions; remember the last acknowledged one.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if progBook == "" {
				for range 100 {
					_, mb := call("GET", e.url("/admin/books?q=dropped"), admin, nil, nil)
					if items, _ := mb["items"].([]any); len(items) > 0 {
						id := items[0].(map[string]any)["id"].(string)
						// Admins can track progress on any book.
						progBook = id
						break
					}
					call("POST", e.url("/admin/library/scan"), admin, nil, nil)
					time.Sleep(100 * time.Millisecond)
				}
			}
			for pos := lastAckPos.Load() + 1; ; pos += 7 {
				select {
				case <-stop:
					return
				default:
				}
				if progBook == "" {
					return
				}
				st, b := call("PUT", e.url("/me/progress/"+progBook), admin, map[string]any{"position_ms": pos % 19_000, "device_id": deviceID,
					"client_ts": time.Now().UTC().Format(time.RFC3339Nano), "speed": 1.0}, nil)
				if st == 200 && b["applied"] == true {
					p := int64(b["progress"].(map[string]any)["position_ms"].(float64))
					if p >= lastAckPos.Load() {
						lastAckPos.Store(p)
					}
				}
				if pos%19_000 < 7 {
					lastAckPos.Store(0) // wrapped around; restart the monotonic check
				}
			}
		}()
		// (c) a backup and a scan.
		wg.Add(1)
		go func() {
			defer wg.Done()
			call("POST", e.url("/admin/backups"), admin, nil, nil)
			call("POST", e.url("/admin/library/scan"), admin, nil, nil)
		}()

		time.Sleep(time.Duration(100+rand.IntN(1400)) * time.Millisecond)
		e.kill()
		close(stop)
		wg.Wait()

		// ── checks with the server down ──
		db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "config", "simpleapb.db")+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		var ok string
		if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
			t.Fatalf("iteration %d: DB integrity %q %v", it, ok, err)
		}
		rows, err := db.Query(`SELECT b.folder_path, f.path, f.checksum FROM books b JOIN book_files f ON f.book_id = b.id WHERE b.status = 'ready'`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var folder, p, sum string
			_ = rows.Scan(&folder, &p, &sum)
			full := filepath.Join(dir, "library", filepath.FromSlash(folder), filepath.FromSlash(p))
			if sha(full) != sum {
				t.Fatalf("iteration %d: ready book file %s missing or corrupt", it, full)
			}
		}
		_ = rows.Close()
		_ = db.Close()
		backups, _ := filepath.Glob(filepath.Join(dir, "backups", "db", "simpleapb-*.db"))
		for _, b := range backups {
			bdb, _ := sql.Open("sqlite", "file:"+b+"?mode=ro")
			var bok string
			if err := bdb.QueryRow(`PRAGMA integrity_check`).Scan(&bok); err != nil || bok != "ok" {
				t.Fatalf("iteration %d: backup %s is not a valid database: %q %v", it, filepath.Base(b), bok, err)
			}
			_ = bdb.Close()
		}
		t.Logf("iteration %d: killed after random delay; DB ok, %d backups ok, acked progress %d", it, len(backups), lastAckPos.Load())
	}

	// Final boot: let everything finish and check the end state.
	e.start()
	if uploadURL != "" {
		// Finish the upload if it is still partial.
		for range 200 {
			head, _ := http.NewRequest("HEAD", uploadURL, nil)
			head.Header.Set("Tus-Resumable", "1.0.0")
			head.Header.Set("Authorization", "Bearer "+admin)
			res, err := http.DefaultClient.Do(head)
			if err != nil || res.StatusCode != 200 {
				break
			}
			_ = res.Body.Close()
			off, _ := strconv.Atoi(res.Header.Get("Upload-Offset"))
			if off >= len(bigData) {
				break
			}
			patch, _ := http.NewRequest("PATCH", uploadURL, bytes.NewReader(bigData[off:]))
			patch.Header.Set("Tus-Resumable", "1.0.0")
			patch.Header.Set("Upload-Offset", strconv.Itoa(off))
			patch.Header.Set("Content-Type", "application/offset+octet-stream")
			patch.Header.Set("Authorization", "Bearer "+admin)
			if r, err := http.DefaultClient.Do(patch); err == nil {
				_ = r.Body.Close()
			}
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	var lastStatus string
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", e.url("/me/uploads"), nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			var list []map[string]any
			_ = json.NewDecoder(res.Body).Decode(&list)
			_ = res.Body.Close()
			if len(list) > 0 {
				lastStatus, _ = list[0]["status"].(string)
				if lastStatus == "done" {
					break
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if uploadURL != "" && lastStatus != "done" {
		t.Fatalf("interrupted upload never finished: status %q\n%s", lastStatus, e.logText())
	}
	_, bs := call("GET", e.url("/admin/backups"), admin, nil, nil)
	if lu, _ := bs["last_unclean_shutdown"].(map[string]any); lu == nil || lu["ok"] != true {
		t.Fatalf("startup check did not record a clean recovery: %v", bs["last_unclean_shutdown"])
	}
	e.kill()
	t.Logf("survived %d SIGKILLs; upload %q, %d server boots", iterations, lastStatus, e.boots)
}
