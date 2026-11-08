// SPDX-License-Identifier: AGPL-3.0-or-later

// Package backup makes consistent database snapshots (brief §6.18):
// `VACUUM INTO` for SQLite, `pg_dump --format=custom` for Postgres. Each
// snapshot is written under a temp name, fsynced, then renamed, so a power
// cut never leaves a half-written file under a real backup name.
package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/fsutil"
	"github.com/quip1/simple-apb/server/internal/store"
)

// ErrNoPGDump means Postgres backups can't run until the client tools are installed.
var ErrNoPGDump = errors.New("pg_dump not found: install the PostgreSQL client tools (same major version as the server)")

// Keep is how many nightly snapshots are retained.
const Keep = 14

// Service makes DB backups into Dir.
type Service struct {
	DB     *store.DB
	Dir    string // usually /backups/db
	PGDump string // pg_dump binary (Postgres only)
	Now    func() time.Time
}

// File is one backup on disk.
type File struct {
	Name      string
	SizeBytes int64
	CreatedAt time.Time
}

const prefix = "simpleapb-"

// Run makes a snapshot and prunes old ones; returns the new file's name.
func (s *Service) Run(ctx context.Context) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	stamp := now().UTC().Format("20060102-150405")
	ext := ".db"
	if s.DB.Dialect() == store.Postgres {
		ext = ".pgdump"
	}
	name := prefix + stamp + ext
	final := filepath.Join(s.Dir, name)
	tmp := filepath.Join(s.Dir, ".tmp-"+name)
	_ = os.Remove(tmp)
	defer func() { _ = os.Remove(tmp) }()

	var err error
	if s.DB.Dialect() == store.SQLite {
		err = s.sqlite(ctx, tmp)
	} else {
		err = s.postgres(ctx, tmp)
	}
	if err != nil {
		return "", err
	}
	if err := fsyncFile(tmp); err != nil {
		return "", err
	}
	if err := fsutil.RenameDurable(tmp, final); err != nil {
		return "", err
	}
	if err := s.prune(); err != nil {
		return name, fmt.Errorf("prune old backups: %w", err)
	}
	return name, nil
}

func (s *Service) sqlite(ctx context.Context, tmp string) error {
	// VACUUM INTO writes a consistent, compacted copy while the DB stays in use.
	if _, err := s.DB.SQL.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return fmt.Errorf("sqlite snapshot: %w", err)
	}
	return nil
}

func (s *Service) postgres(ctx context.Context, tmp string) error {
	bin := s.PGDump
	if bin == "" {
		bin = "pg_dump"
	}
	dsn, env := splitPassword(s.DB.DSN())
	cmd := exec.CommandContext(ctx, bin, "--format=custom", "--no-owner", "--file="+tmp, "--dbname="+dsn)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrNoPGDump
		}
		return fmt.Errorf("pg_dump: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// libpqParams are URI query parameters pg_dump (libpq) understands. Anything
// else in the DSN is a pgx runtime parameter (e.g. search_path), which libpq
// rejects, so it is passed as `options=-c name=value` instead.
var libpqParams = map[string]bool{
	"host": true, "hostaddr": true, "port": true, "dbname": true, "user": true, "password": true, "passfile": true,
	"connect_timeout": true, "client_encoding": true, "options": true, "application_name": true,
	"fallback_application_name": true, "keepalives": true, "sslmode": true, "sslcert": true, "sslkey": true,
	"sslrootcert": true, "sslcrl": true, "sslpassword": true, "target_session_attrs": true, "gssencmode": true,
}

// splitPassword adapts a pgx DSN for pg_dump: the URL password moves into
// PGPASSWORD (so it never shows in the process list) and runtime parameters
// become libpq `options`.
func splitPassword(dsn string) (string, []string) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return dsn, nil
	}
	var env []string
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
		}
	}
	q := u.Query()
	var opts []string
	if o := q.Get("options"); o != "" {
		opts = append(opts, o)
	}
	for k, vs := range q {
		if libpqParams[k] {
			continue
		}
		for _, v := range vs {
			opts = append(opts, "-c "+k+"="+v)
		}
		q.Del(k)
	}
	if len(opts) > 0 {
		sort.Strings(opts)
		q.Set("options", strings.Join(opts, " "))
	}
	// libpq decodes %XX but treats "+" literally, while url.Values encodes
	// spaces as "+": re-encode spaces as %20 (a literal "+" is already %2B).
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String(), env
}

func fsyncFile(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// List returns backups, newest first.
func (s *Service) List() ([]File, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), SizeBytes: info.Size(), CreatedAt: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func (s *Service) prune() error {
	files, err := s.List()
	if err != nil {
		return err
	}
	for i := Keep; i < len(files); i++ {
		if err := os.Remove(filepath.Join(s.Dir, files[i].Name)); err != nil {
			return err
		}
	}
	// Leftover temp files from a crash mid-backup are useless.
	tmps, _ := filepath.Glob(filepath.Join(s.Dir, ".tmp-"+prefix+"*"))
	for _, t := range tmps {
		_ = os.Remove(t)
	}
	return nil
}
