// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthAndVersionHeader(t *testing.T) {
	h := (&Server{}).Router()
	rec := do(t, h, http.MethodGet, "/api/v1/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("X-Server-Version") == "" {
		t.Fatal("missing X-Server-Version header")
	}
	var got Health
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != HealthStatusOk {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestInstance(t *testing.T) {
	h := (&Server{Instance: InstanceInfo{Name: "Test", SetupRequired: true}}).Router()
	rec := do(t, h, http.MethodGet, "/api/v1/instance", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got Instance
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test" || !got.SetupRequired || got.ApiVersion != "v1" {
		t.Fatalf("unexpected instance: %+v", got)
	}
}

func TestUnimplementedReturns501(t *testing.T) {
	h := (&Server{}).Router()
	body := `{"username":"a","password":"b","device":{"device_id":"0190c3b4-0000-7000-8000-000000000000","device_name":"x","platform":"web","app_version":"0"}}`
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login", body)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Code != "not_implemented" {
		t.Fatalf("unexpected error body %s", rec.Body.String())
	}
}

func TestUnknownPathIs404JSON(t *testing.T) {
	rec := do(t, (&Server{}).Router(), http.MethodGet, "/api/v1/nope", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
