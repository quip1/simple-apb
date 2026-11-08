// SPDX-License-Identifier: AGPL-3.0-or-later

// Command simpleapb is the Simple APB server.
//
// Phase 0: opens the database, applies migrations and serves the API skeleton.
// The first-run wizard, lock file / startup check and jobs arrive in Phase 1.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/quip1/simple-apb/server/internal/api"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/version"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	configDir := env("SIMPLEAPB_CONFIG_DIR", "/config")
	listen := env("SIMPLEAPB_LISTEN", ":8080")
	dialect, err := store.ParseDialect(env("SIMPLEAPB_DB_DIALECT", "sqlite"))
	if err != nil {
		return err
	}
	dsn := os.Getenv("SIMPLEAPB_DB_DSN")
	if dialect == store.SQLite && dsn == "" {
		if err := os.MkdirAll(configDir, 0o750); err != nil {
			return err
		}
		dsn = filepath.Join(configDir, "simpleapb.db")
	}

	db, err := store.Open(ctx, dialect, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.MigrateUp(ctx, db, dialect); err != nil {
		return err
	}

	srv := &api.Server{
		Instance: api.InstanceInfo{Name: "Simple APB", SetupRequired: true},
		Log:      log,
	}
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", listen, "version", version.Version, "db", string(dialect))
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}
