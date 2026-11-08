// SPDX-License-Identifier: AGPL-3.0-or-later

// Package testdb gives tests a fresh, migrated database on every dialect:
// SQLite always, Postgres when SIMPLEAPB_TEST_POSTGRES_DSN is set (each test
// in its own throwaway schema, so parallel test packages don't collide).
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/quip1/simple-apb/server/internal/store"
)

// ForEachDialect runs fn once per available dialect.
func ForEachDialect(t *testing.T, fn func(t *testing.T, db *store.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, SQLite(t)) })
	t.Run("postgres", func(t *testing.T) {
		db := Postgres(t)
		if db == nil {
			t.Skip("SIMPLEAPB_TEST_POSTGRES_DSN not set")
		}
		fn(t, db)
	})
}

// SQLite returns a migrated SQLite DB in a temp dir.
func SQLite(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.OpenDB(context.Background(), store.SQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// PostgresDSN returns a DSN for a fresh schema, or "" when Postgres isn't configured.
func PostgresDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SIMPLEAPB_TEST_POSTGRES_DSN")
	if base == "" {
		return ""
	}
	ctx := context.Background()
	admin, err := store.Open(ctx, store.Postgres, base)
	if err != nil {
		t.Fatal(err)
	}
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "search_path=" + schema
}

// Postgres returns a migrated DB in a fresh schema, or nil when not configured.
func Postgres(t *testing.T) *store.DB {
	t.Helper()
	dsn := PostgresDSN(t)
	if dsn == "" {
		return nil
	}
	db, err := store.OpenDB(context.Background(), store.Postgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
