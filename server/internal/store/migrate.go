// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is the database layer: connection setup for SQLite and
// PostgreSQL, and goose migrations kept in parallel for both dialects.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationsFS embed.FS

// Migrations returns the migration files for a dialect.
func Migrations(d Dialect) (fs.FS, error) {
	sub, err := fs.Sub(migrationsFS, "migrations/"+string(d))
	if err != nil {
		return nil, fmt.Errorf("migrations for %s: %w", d, err)
	}
	return sub, nil
}

func newProvider(db *sql.DB, d Dialect) (*goose.Provider, error) {
	fsys, err := Migrations(d)
	if err != nil {
		return nil, err
	}
	var gd goose.Dialect
	switch d {
	case SQLite:
		gd = goose.DialectSQLite3
	case Postgres:
		gd = goose.DialectPostgres
	default:
		return nil, fmt.Errorf("unknown dialect %q", d)
	}
	p, err := goose.NewProvider(gd, db, fsys)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return p, nil
}

// MigrateUp applies all pending migrations.
func MigrateUp(ctx context.Context, db *sql.DB, d Dialect) error {
	p, err := newProvider(db, d)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateDownAll rolls back every migration. Used by tests.
func MigrateDownAll(ctx context.Context, db *sql.DB, d Dialect) error {
	p, err := newProvider(db, d)
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}
