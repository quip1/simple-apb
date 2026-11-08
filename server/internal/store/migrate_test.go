// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

// Postgres tests run when SIMPLEAPB_TEST_POSTGRES_DSN is set (CI sets it).
func postgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SIMPLEAPB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SIMPLEAPB_TEST_POSTGRES_DSN not set")
	}
	return dsn
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), SQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrationFilesMatch(t *testing.T) {
	names := func(d Dialect) []string {
		fsys, err := Migrations(d)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := fs.Glob(fsys, "*.sql")
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	a, b := names(SQLite), names(Postgres)
	if !slices.Equal(a, b) {
		t.Fatalf("migration files differ:\n sqlite:   %v\n postgres: %v", a, b)
	}
	if len(a) == 0 {
		t.Fatal("no migrations found")
	}
}

func TestSQLiteUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := openSQLite(t)
	if err := MigrateUp(ctx, db, SQLite); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDownAll(ctx, db, SQLite); err != nil {
		t.Fatal(err)
	}
	if err := MigrateUp(ctx, db, SQLite); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("foreign_keys not enabled")
	}
}

func TestSQLiteConstraints(t *testing.T) {
	ctx := context.Background()
	db := openSQLite(t)
	if err := MigrateUp(ctx, db, SQLite); err != nil {
		t.Fatal(err)
	}
	const ins = `INSERT INTO users (id, username, display_name, status, role, created_at, updated_at)
	             VALUES (?, ?, ?, ?, ?, 0, 0)`
	if _, err := db.ExecContext(ctx, ins, "u1", "Alice", "Alice", "active", "user"); err != nil {
		t.Fatal(err)
	}
	// Usernames are unique case-insensitively.
	if _, err := db.ExecContext(ctx, ins, "u2", "alice", "x", "active", "user"); err == nil {
		t.Fatal("expected case-insensitive username uniqueness violation")
	}
	// Status is constrained.
	if _, err := db.ExecContext(ctx, ins, "u3", "bob", "Bob", "nonsense", "user"); err == nil {
		t.Fatal("expected status CHECK violation")
	}
	// STRICT tables reject wrongly typed values.
	if _, err := db.ExecContext(ctx,
		`UPDATE users SET quota_bytes = 'lots' WHERE id = 'u1'`); err == nil {
		t.Fatal("expected STRICT type violation")
	}
	// Foreign keys are enforced.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, device_id, device_name, platform, app_version,
		  refresh_token_hash, expires_at, last_seen_at, created_at, updated_at)
		 VALUES ('s1', 'missing', 'd', 'n', 'web', '0', 'h', 0, 0, 0, 0)`); err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestPostgresUpDownUp(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Postgres, postgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := MigrateDownAll(ctx, db, Postgres); err != nil {
		t.Fatal(err)
	}
	if err := MigrateUp(ctx, db, Postgres); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDownAll(ctx, db, Postgres); err != nil {
		t.Fatal(err)
	}
	if err := MigrateUp(ctx, db, Postgres); err != nil {
		t.Fatal(err)
	}
}

// TestSchemaParity asserts both dialects define exactly the same tables and
// columns, so one repository layer works on both.
func TestSchemaParity(t *testing.T) {
	ctx := context.Background()
	pg, err := Open(ctx, Postgres, postgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pg.Close() }()
	if err := MigrateUp(ctx, pg, Postgres); err != nil {
		t.Fatal(err)
	}
	lite := openSQLite(t)
	if err := MigrateUp(ctx, lite, SQLite); err != nil {
		t.Fatal(err)
	}

	liteCols := map[string][]string{}
	rows, err := lite.QueryContext(ctx, `
		SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name <> 'goose_db_version'`)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, rows, liteCols)

	pgCols := map[string][]string{}
	rows, err = pg.QueryContext(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name <> 'goose_db_version'`)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, rows, pgCols)

	for table, cols := range liteCols {
		if !slices.Equal(cols, pgCols[table]) {
			t.Errorf("table %s columns differ:\n sqlite:   %v\n postgres: %v", table, cols, pgCols[table])
		}
	}
	for table := range pgCols {
		if _, ok := liteCols[table]; !ok {
			t.Errorf("table %s exists only in postgres", table)
		}
	}
}

func collect(t *testing.T, rows *sql.Rows, into map[string][]string) {
	t.Helper()
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		into[table] = append(into[table], col)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for k := range into {
		sort.Strings(into[k])
	}
}
