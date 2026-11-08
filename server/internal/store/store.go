// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is implemented by *DB and *Tx. Repository functions take a Querier
// so they work inside or outside a transaction. Queries use `?` placeholders;
// they are rebound to `$n` for Postgres (ADR-0009).
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
	Dialect() Dialect
}

// DB wraps *sql.DB with dialect-aware helpers.
type DB struct {
	SQL     *sql.DB
	dialect Dialect
	// dsn is kept for pg_dump backups. Never log it (it may hold a password).
	dsn string
}

// Wrap pairs a connection with its dialect.
func Wrap(db *sql.DB, d Dialect) *DB { return &DB{SQL: db, dialect: d} }

// OpenDB opens, configures and migrates a database.
func OpenDB(ctx context.Context, d Dialect, dsn string) (*DB, error) {
	sqlDB, err := Open(ctx, d, dsn)
	if err != nil {
		return nil, err
	}
	if err := MigrateUp(ctx, sqlDB, d); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	db := Wrap(sqlDB, d)
	db.dsn = dsn
	return db, nil
}

// DSN returns the connection string the DB was opened with (for backups).
func (db *DB) DSN() string { return db.dsn }

func (db *DB) Dialect() Dialect { return db.dialect }
func (db *DB) Close() error     { return db.SQL.Close() }

func (db *DB) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return db.SQL.ExecContext(ctx, rebind(db.dialect, q), args...)
}

func (db *DB) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return db.SQL.QueryContext(ctx, rebind(db.dialect, q), args...)
}

func (db *DB) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return db.SQL.QueryRowContext(ctx, rebind(db.dialect, q), args...)
}

// Tx is a transaction with the same helpers.
type Tx struct {
	tx      *sql.Tx
	dialect Dialect
}

func (t *Tx) Dialect() Dialect { return t.dialect }

func (t *Tx) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, rebind(t.dialect, q), args...)
}

func (t *Tx) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, rebind(t.dialect, q), args...)
}

func (t *Tx) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, rebind(t.dialect, q), args...)
}

// InTx runs fn in a transaction, committing on nil and rolling back otherwise.
func (db *DB) InTx(ctx context.Context, fn func(tx *Tx) error) error {
	sqlTx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	t := &Tx{tx: sqlTx, dialect: db.dialect}
	if err := fn(t); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// rebind turns `?` into `$1, $2…` for Postgres, skipping quoted strings.
func rebind(d Dialect, q string) string {
	if d != Postgres || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n, inStr := 0, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'':
			inStr = !inStr
			b.WriteByte(c)
		case c == '?' && !inStr:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// NewID returns a new UUIDv7 (time-ordered, so IDs index well).
func NewID() string { return uuid.Must(uuid.NewV7()).String() }

// NowMS is the current time in unix milliseconds (the DB time format, ADR-0004).
func NowMS() int64 { return time.Now().UnixMilli() }

// MS converts a time to unix ms.
func MS(t time.Time) int64 { return t.UnixMilli() }

// FromMS converts unix ms to time.
func FromMS(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on unique-constraint violations.
var ErrConflict = errors.New("conflict")

// IsUniqueViolation reports whether err is a unique-constraint failure on either dialect.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// notFound maps sql.ErrNoRows, and Postgres rejecting a malformed UUID
// (which SQLite simply fails to match), to ErrNotFound so both dialects agree.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) || isInvalidUUID(err) {
		return ErrNotFound
	}
	return err
}

// isInvalidUUID reports Postgres SQLSTATE 22P02 (invalid_text_representation).
func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// nullStr converts "" to NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullInt converts a nil pointer to NULL.
func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
