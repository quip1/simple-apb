// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config holds the bootstrap configuration: the few settings needed
// before the database is open (where the DB is, and the paths of the volumes).
// Everything else lives in the DB `settings` table and is edited in the admin panel.
//
// /config/config.json is written by the setup wizard. Its absence means the
// instance has never been set up.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/quip1/simple-apb/server/internal/fsutil"
)

// Paths are the container volumes. Defaults match the Docker image;
// SIMPLEAPB_*_DIR env vars override them for local development.
type Paths struct {
	Config  string
	Library string
	Cache   string
	Backups string
}

// PathsFromEnv returns volume paths with env overrides.
func PathsFromEnv() Paths {
	get := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	return Paths{
		Config:  get("SIMPLEAPB_CONFIG_DIR", "/config"),
		Library: get("SIMPLEAPB_LIBRARY_DIR", "/library"),
		Cache:   get("SIMPLEAPB_CACHE_DIR", "/cache"),
		Backups: get("SIMPLEAPB_BACKUPS_DIR", "/backups"),
	}
}

// Bootstrap is /config/config.json.
type Bootstrap struct {
	Version   int    `json:"version"`
	DBDialect string `json:"db_dialect"`
	// DBDSN is empty for SQLite (the DB lives at /config/simpleapb.db).
	// For Postgres it is the connection string, encrypted with the secret key.
	DBDSNEncrypted string `json:"db_dsn_encrypted,omitempty"`
}

const bootstrapFile = "config.json"

// ErrNotConfigured means the setup wizard has not run yet.
var ErrNotConfigured = errors.New("instance not configured")

// LoadBootstrap reads config.json, or returns ErrNotConfigured.
func LoadBootstrap(configDir string) (*Bootstrap, error) {
	b, err := os.ReadFile(filepath.Join(configDir, bootstrapFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("read config.json: %w", err)
	}
	var c Bootstrap
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}
	return &c, nil
}

// SaveBootstrap writes config.json crash-safely.
func SaveBootstrap(configDir string, c *Bootstrap) error {
	c.Version = 1
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(configDir, bootstrapFile), append(b, '\n'), 0o600)
}

// SQLitePath is where the SQLite database lives.
func SQLitePath(configDir string) string { return filepath.Join(configDir, "simpleapb.db") }
