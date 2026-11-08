// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/setup"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/version"
)

// ErrNotImplemented is returned by operations that exist in the contract but
// are not built yet. It maps to 501 with code "not_implemented".
var ErrNotImplemented = errors.New("not implemented")

// App is everything that exists once the instance is set up.
type App struct {
	DB   *store.DB
	Auth *auth.Service
}

// Settings returns a settings store on the app DB.
func (a *App) Settings() settings.Store { return settings.Store{Q: a.DB, Enc: a.Auth.Enc} }

// NewApp wires the services for an open, set-up database.
func NewApp(db *store.DB, secrets *config.Secrets, log *slog.Logger) *App {
	return &App{DB: db, Auth: &auth.Service{
		DB: db, Enc: secrets, Tokens: auth.NewTokens(secrets.Derive("jwt")), Limits: auth.DefaultLimits(), Log: log,
	}}
}

// Server implements StrictServerInterface. Before setup it runs in "setup
// mode": only health, instance and /setup endpoints answer.
type Server struct {
	Log            *slog.Logger
	Setup          *setup.Service
	Secrets        *config.Secrets
	TrustedProxies []netip.Prefix
	app            atomic.Pointer[App]
	completing     atomic.Bool
}

var _ StrictServerInterface = (*Server)(nil)

// SetApp switches the server into normal mode.
func (s *Server) SetApp(a *App) { s.app.Store(a) }

// App returns the running app, or nil in setup mode.
func (s *Server) App() *App { return s.app.Load() }

// Router returns the full HTTP handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer) // last line of defence; handlers must not panic
	r.Use(serverVersionHeader)
	r.Use(s.clientIPMiddleware)
	r.Use(s.setupGate)
	r.Use(s.authenticate)

	strict := NewStrictHandlerWithOptions(s, []StrictMiddlewareFunc{withRequest}, StrictHTTPServerOptions{
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
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	return r
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

// setupGate: in setup mode only health, instance and setup answer.
func (s *Server) setupGate(next http.Handler) http.Handler {
	base := "/api/" + version.APIVersion
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.App() == nil {
			p := r.URL.Path
			if p != base+"/health" && p != base+"/instance" && !strings.HasPrefix(p, base+"/setup/") {
				writeError(w, http.StatusServiceUnavailable, "setup_required", "this instance has not been set up yet")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientIPMiddleware resolves the client IP. X-Forwarded-For is only trusted
// when the TCP peer is a configured trusted proxy (ADR-0010); we then take the
// right-most address that is not itself a trusted proxy.
func (s *Server) clientIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(withClientIP(r.Context(), s.clientIP(r))))
	})
}

func (s *Server) clientIP(r *http.Request) string {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	ip := peer.Addr().Unmap()
	if !s.trusted(ip) {
		return ip.String()
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if !s.trusted(a) {
			return a.String()
		}
		ip = a
	}
	return ip.String()
}

func (s *Server) trusted(ip netip.Addr) bool {
	for _, p := range s.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies parses a comma-separated CIDR/IP list.
func ParseTrustedProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
