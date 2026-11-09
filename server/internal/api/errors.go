// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/setup"
	"github.com/quip1/simple-apb/server/internal/store"
)

// httpError is a handler failure with a status and a stable code.
type httpError struct {
	status int
	code   string
	msg    string
}

func (e *httpError) Error() string { return e.code + ": " + e.msg }

func apiErr(status int, code, msg string) *httpError { return &httpError{status, code, msg} }

func badRequest(code, msg string) *httpError { return apiErr(http.StatusBadRequest, code, msg) }

var (
	errConflict       = apiErr(http.StatusConflict, "conflict", "conflicts with current state")
	errSetupDone      = apiErr(http.StatusConflict, "setup_complete", "this instance is already set up")
	errBadSetupToken  = apiErr(http.StatusUnauthorized, "invalid_setup_token", "missing or wrong setup token (see the server log)")
	errSetupInProcess = apiErr(http.StatusConflict, "setup_in_progress", "setup is already being completed")
)

// responseError maps handler errors to JSON responses. Unknown errors become
// a generic 500 and are logged; internal details never reach clients.
func (s *Server) responseError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		he *httpError
		ae *auth.Error
		se *setup.Error
	)
	switch {
	case errors.As(err, &he):
		writeError(w, he.status, he.code, he.msg)
	case errors.As(err, &ae):
		if ae.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(ae.RetryAfter.Seconds()))))
		}
		writeError(w, ae.Status, ae.Code, ae.Message)
	case errors.As(err, &se):
		writeError(w, http.StatusBadRequest, se.Code, se.Message)
	case errors.Is(err, setup.ErrAlreadyComplete):
		writeError(w, http.StatusConflict, errSetupDone.code, errSetupDone.msg)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrInvalidFilter):
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "conflicts with current state")
	case errors.Is(err, ErrNotImplemented):
		writeError(w, http.StatusNotImplemented, "not_implemented", "this endpoint is not implemented yet")
	default:
		s.log().ErrorContext(r.Context(), "handler error", "err", err, "path", r.URL.Path,
			"request_id", middleware.GetReqID(r.Context()))
		writeError(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Code: code, Message: msg})
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
