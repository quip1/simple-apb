// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
)

type ctxKey int

const (
	keyRequest ctxKey = iota
	keyClientIP
	keyAuth
)

// Cookie names (web/admin clients).
const (
	cookieSession = "sapb_session" // access JWT, HttpOnly
	cookieRefresh = "sapb_refresh" // refresh token, HttpOnly, scoped to /api/v1/auth
	cookieCSRF    = "sapb_csrf"    // readable by JS, echoed in X-CSRF-Token
	headerCSRF    = "X-CSRF-Token"
)

// authState is what the authenticate middleware found.
type authState struct {
	principal *auth.Principal
	viaCookie bool
	// presentedInvalid: a token was sent but is expired/revoked, so protected
	// endpoints answer invalid_token (client should refresh) not unauthenticated.
	presentedInvalid bool
}

func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, keyClientIP, ip)
}

func clientIP(ctx context.Context) string {
	ip, _ := ctx.Value(keyClientIP).(string)
	return ip
}

// withRequest makes the *http.Request available to strict handlers (cookies, TLS).
func withRequest(f StrictHandlerFunc, _ string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return f(context.WithValue(ctx, keyRequest, r), w, r, req)
	}
}

func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(keyRequest).(*http.Request)
	return r
}

func authFrom(ctx context.Context) authState {
	a, _ := ctx.Value(keyAuth).(authState)
	return a
}

// authenticate resolves a Bearer token or session cookie into a principal.
// It never rejects by itself (public endpoints must still work with a stale
// cookie); handlers decide via requireUser/requireAdmin.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app := s.App()
		if app == nil {
			next.ServeHTTP(w, r)
			return
		}
		var (
			st    authState
			token string
		)
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		} else if c, err := r.Cookie(cookieSession); err == nil {
			token, st.viaCookie = c.Value, true
		}
		if token != "" {
			p, err := app.Auth.Authenticate(r.Context(), token)
			if err != nil {
				st.presentedInvalid = true
			} else {
				st.principal = p
			}
		}
		if st.principal != nil && st.viaCookie && isUnsafe(r.Method) && !csrfOK(r) {
			writeError(w, http.StatusForbidden, "csrf_failed", "missing or wrong CSRF token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyAuth, st)))
	})
}

func isUnsafe(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// csrfOK implements the double-submit check: header must equal the cookie.
func csrfOK(r *http.Request) bool {
	c, err := r.Cookie(cookieCSRF)
	h := r.Header.Get(headerCSRF)
	return err == nil && c.Value != "" && subtle.ConstantTimeCompare([]byte(c.Value), []byte(h)) == 1
}

var (
	errForbidden      = apiErr(http.StatusForbidden, "forbidden", "you are not allowed to do that")
	errTOTPEnrollment = apiErr(http.StatusForbidden, "totp_enrollment_required", "set up two-factor authentication to continue")
)

// requireUser returns the signed-in principal. allowEnroll lets accounts that
// still must enroll TOTP through (for /me, TOTP enrollment and logout).
func requireUser(ctx context.Context, allowEnroll bool) (*auth.Principal, error) {
	st := authFrom(ctx)
	if st.principal == nil {
		if st.presentedInvalid {
			return nil, auth.ErrSessionInvalid
		}
		return nil, auth.ErrUnauthenticated
	}
	if st.principal.MustEnrollTOTP && !allowEnroll {
		return nil, errTOTPEnrollment
	}
	return st.principal, nil
}

// requireCan returns the principal if it may perform the action.
func requireCan(ctx context.Context, a authz.Action) (*auth.Principal, error) {
	p, err := requireUser(ctx, false)
	if err != nil {
		return nil, err
	}
	if !p.Can(a) {
		return nil, errForbidden
	}
	return p, nil
}
