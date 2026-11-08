// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/testdb"
)

func TestSQLiteBackupAndRetention(t *testing.T) {
	ctx := context.Background()
	db := testdb.SQLite(t)
	if _, err := store.CreateUser(ctx, db, store.NewUser{Username: "keepme", Status: "active", Role: "user"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "db")
	clock := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	s := &Service{DB: db, Dir: dir, Now: func() time.Time { return clock }}
	name, err := s.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot is a valid SQLite DB containing our data.
	snap, err := sql.Open("sqlite", "file:"+filepath.Join(dir, name)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var ok string
	var n int
	_ = snap.QueryRow(`PRAGMA integrity_check`).Scan(&ok)
	_ = snap.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'keepme'`).Scan(&n)
	_ = snap.Close()
	if ok != "ok" || n != 1 {
		t.Fatalf("snapshot integrity %q, users %d", ok, n)
	}
	// Retention: after 20 nightly runs only the newest 14 remain, and stray temp files are cleaned.
	_ = os.WriteFile(filepath.Join(dir, ".tmp-"+prefix+"crashed.db"), []byte("partial"), 0o600)
	for i := 1; i < 20; i++ {
		clock = clock.Add(24 * time.Hour)
		if _, err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := s.List()
	if len(files) != Keep || files[0].Name != fmt.Sprintf("%s%s.db", prefix, clock.Format("20060102-150405")) {
		t.Fatalf("retention: %d files, newest %v", len(files), files[0])
	}
	if tmps, _ := filepath.Glob(filepath.Join(dir, ".tmp-*")); len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

func TestPostgresBackup(t *testing.T) {
	db := testdb.Postgres(t)
	if db == nil {
		t.Skip("SIMPLEAPB_TEST_POSTGRES_DSN not set")
	}
	bin := os.Getenv("SIMPLEAPB_PG_DUMP")
	if bin == "" {
		bin = "pg_dump"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skip("pg_dump not installed")
	}
	s := &Service{DB: db, Dir: t.TempDir(), PGDump: bin}
	name, err := s.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(s.Dir, name)); err != nil || st.Size() < 1000 {
		t.Fatalf("dump: %v %v", st, err)
	}
}

func TestSplitPassword(t *testing.T) {
	dsn, env := splitPassword("postgres://u:s3cret@db:5432/x?sslmode=disable")
	if dsn != "postgres://u@db:5432/x?sslmode=disable" || len(env) != 1 || env[0] != "PGPASSWORD=s3cret" {
		t.Fatalf("%s %v", dsn, env)
	}
	// pgx runtime parameters become libpq options.
	dsn, _ = splitPassword("postgres://u@db/x?sslmode=disable&search_path=t_1")
	if dsn != "postgres://u@db/x?options=-c%20search_path%3Dt_1&sslmode=disable" {
		t.Fatalf("runtime params: %s", dsn)
	}
}
