// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/quip1/simple-apb/server/internal/version"
)

// ErrNotImplemented is returned by operations that exist in the contract but
// are not built yet. It maps to 501 with code "not_implemented".
var ErrNotImplemented = errors.New("not implemented")

// InstanceInfo supplies the public instance settings. In Phase 1 this is
// backed by the settings table; for now it is static.
type InstanceInfo struct {
	Name          string
	SignupOpen    bool
	SetupRequired bool
}

// Server implements StrictServerInterface.
type Server struct {
	Instance InstanceInfo
	Log      *slog.Logger
}

var _ StrictServerInterface = (*Server)(nil)

// Router returns the full HTTP handler: API under /api/v1 plus shared middleware.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer) // last line of defence; handlers must not panic
	r.Use(serverVersionHeader)

	strict := NewStrictHandlerWithOptions(s, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		},
		ResponseErrorHandlerFunc: s.responseError,
	})
	HandlerWithOptions(strict, ChiServerOptions{
		BaseURL:    "/api/" + version.APIVersion,
		BaseRouter: r,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		},
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return r
}

func (s *Server) responseError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotImplemented) {
		writeError(w, http.StatusNotImplemented, "not_implemented", "this endpoint is not implemented yet")
		return
	}
	// Never leak internal error text to clients.
	s.log().ErrorContext(r.Context(), "handler error", "err", err, "path", r.URL.Path,
		"request_id", middleware.GetReqID(r.Context()))
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func serverVersionHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server-Version", version.Version)
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Code: code, Message: msg})
}

// ── Implemented operations ──────────────────────────────────────────────────

func (s *Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: HealthStatusOk, Version: version.Version}, nil
}

func (s *Server) GetInstance(context.Context, GetInstanceRequestObject) (GetInstanceResponseObject, error) {
	// Placeholder guard until per-platform minimums are configurable (Phase 6).
	v := VersionPair{Required: "0.0.0", Recommended: "0.0.0"}
	return GetInstance200JSONResponse{
		Name:          s.Instance.Name,
		Version:       version.Version,
		ApiVersion:    version.APIVersion,
		SignupOpen:    s.Instance.SignupOpen,
		SetupRequired: s.Instance.SetupRequired,
		MinClientVersion: ClientVersionGuard{
			Android: &v, Wear: &v, Web: &v, Windows: &v,
		},
		OidcProviders: []OidcProvider{},
	}, nil
}
