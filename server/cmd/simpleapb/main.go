// SPDX-License-Identifier: AGPL-3.0-or-later

// Command simpleapb is the Simple APB server.
//
// On first start (no /config/config.json, or the wizard never finished) it
// runs in setup mode and prints a one-time setup token to the log. Once the
// wizard completes it switches to normal mode without a restart.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quip1/simple-apb/server/internal/api"
	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/media"
	"github.com/quip1/simple-apb/server/internal/setup"
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

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	paths := config.PathsFromEnv()
	listen := os.Getenv("SIMPLEAPB_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	if err := os.MkdirAll(paths.Config, 0o750); err != nil {
		return err
	}
	secrets, err := config.LoadOrCreateSecrets(paths.Config)
	if err != nil {
		return err
	}
	proxies, err := api.ParseTrustedProxies(os.Getenv("SIMPLEAPB_TRUSTED_PROXIES"))
	if err != nil {
		return err
	}

	tools := media.DefaultTools()
	if !tools.Available() {
		log.Warn("ffprobe/ffmpeg not found; imports will fail until they are installed")
	}
	srv := &api.Server{
		Log:            log,
		Secrets:        secrets,
		TrustedProxies: proxies,
		Setup:          &setup.Service{Paths: paths, Secrets: secrets, Token: setup.NewToken()},
		AppOptions:     api.AppOptions{Paths: paths, Tools: tools, TrustedProxies: len(proxies) > 0},
		BaseCtx:        ctx,
	}

	db, done, err := setup.LoadExisting(ctx, paths, secrets)
	if err != nil {
		return err
	}
	switch {
	case done:
		app, err := api.NewApp(ctx, db, secrets, log, srv.AppOptions)
		if err != nil {
			_ = db.Close()
			return err
		}
		app.Start(ctx)
		srv.SetApp(app)
		log.Info("instance ready", "db", string(db.Dialect()))
	default:
		if db != nil {
			_ = db.Close() // the wizard reopens it
		}
		log.Warn("SETUP REQUIRED: open the setup wizard and enter this one-time token",
			"setup_token", srv.Setup.Token, "url_path", "/setup")
	}
	defer func() {
		if app := srv.App(); app != nil {
			app.Jobs.Wait() // let running jobs reach a safe point (ctx is cancelled)
			if err := app.DB.Close(); err != nil {
				log.Error("closing database", "err", err)
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", listen, "version", version.Version)
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
