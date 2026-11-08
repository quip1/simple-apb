// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	_ "modernc.org/sqlite"             // registers the "sqlite" driver (pure Go, no cgo)
)

// Dialect is the SQL dialect in use.
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// ParseDialect validates a dialect name from configuration.
func ParseDialect(s string) (Dialect, error) {
	switch Dialect(strings.ToLower(s)) {
	case SQLite:
		return SQLite, nil
	case Postgres, "postgresql", "pg":
		return Postgres, nil
	}
	return "", fmt.Errorf("unknown database dialect %q (want sqlite or postgres)", s)
}

// Open connects to the database and applies the crash-safety settings.
//
// For SQLite, dsn is a file path. WAL mode with synchronous=NORMAL is durable
// against application crashes and, with WAL, loses at most the last
// transactions (never corrupts) on power loss — see docs/DECISIONS.md.
func Open(ctx context.Context, d Dialect, dsn string) (*sql.DB, error) {
	var (
		db  *sql.DB
		err error
	)
	switch d {
	case SQLite:
		db, err = sql.Open("sqlite", sqliteDSN(dsn))
		if err == nil {
			// SQLite allows one writer; a small pool avoids SQLITE_BUSY storms.
			db.SetMaxOpenConns(4)
		}
	case Postgres:
		db, err = sql.Open("pgx", dsn)
		if err == nil {
			db.SetMaxOpenConns(20)
			db.SetConnMaxIdleTime(5 * time.Minute)
		}
	default:
		return nil, fmt.Errorf("unknown dialect %q", d)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", d, err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect %s: %w", d, err)
	}
	return db, nil
}

// sqliteDSN turns a file path into a modernc DSN with pragmas applied to every
// pooled connection.
func sqliteDSN(path string) string {
	pragmas := []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
		"busy_timeout(5000)",
	}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return "file:" + path + sep + q.Encode()
}
