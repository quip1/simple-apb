// SPDX-License-Identifier: AGPL-3.0-or-later

// Package settings defines the typed instance settings stored in the DB
// `settings` table (one JSON value per group). Missing groups fall back to
// defaults, so adding a new setting never needs a migration.
package settings

import (
	"context"
	"errors"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Keys of the setting groups.
const (
	KeyInstance    = "instance"
	KeyLibrary     = "library"
	KeySMTP        = "smtp" // secret
	KeyNtfy        = "ntfy" // secret
	KeyBandwidth   = "bandwidth"
	KeyAuth        = "auth"
	KeyPermissions = "permission_defaults"
	KeyQuotas      = "quotas"
	KeySetupDone   = "setup_completed_at"
)

// Instance is the public-facing instance configuration.
type Instance struct {
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	SignupOpen       bool   `json:"signup_open"`
	PrivacyText      string `json:"privacy_text"`
	TermsText        string `json:"terms_text"`
	CopyrightContact string `json:"copyright_contact"`
}

// Library settings.
type Library struct {
	Path string `json:"path"`
}

// SMTP settings (stored encrypted).
type SMTP struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"` // starttls | tls | none
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

// Enabled reports whether SMTP is configured.
func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

// Ntfy settings (stored encrypted because of the token).
type Ntfy struct {
	InternalURL string `json:"internal_url"`
	PublicURL   string `json:"public_url"`
	Token       string `json:"token"`
}

// Bandwidth settings (brief §6.10).
type Bandwidth struct {
	EgressMbps int `json:"egress_mbps"`
	// HLSCacheGB caps the transcode cache (LRU), default 10.
	HLSCacheGB int `json:"hls_cache_gb"`
}

// Quotas: storage given to newly approved accounts (brief §6.11).
type Quotas struct {
	DefaultQuotaBytes int64 `json:"default_quota_bytes"` // 0 = unlimited
}

// Auth settings.
type Auth struct {
	RequireTOTPAll bool `json:"require_totp_all"`
}

// Defaults for each group.
func DefaultInstance() Instance   { return Instance{Name: "Simple APB", SignupOpen: true} }
func DefaultLibrary() Library     { return Library{Path: "/library"} }
func DefaultBandwidth() Bandwidth { return Bandwidth{EgressMbps: 80, HLSCacheGB: 10} }

// Store loads and saves settings.
type Store struct {
	Q   store.Querier
	Enc store.Encrypter
}

func (s Store) get(ctx context.Context, key string, dest any) error {
	err := store.GetSetting(ctx, s.Q, s.Enc, key, dest)
	if errors.Is(err, store.ErrNotFound) {
		return nil // keep the default already in dest
	}
	return err
}

// Instance returns instance settings (defaults if unset).
func (s Store) Instance(ctx context.Context) (Instance, error) {
	v := DefaultInstance()
	return v, s.get(ctx, KeyInstance, &v)
}

// Library returns library settings.
func (s Store) Library(ctx context.Context) (Library, error) {
	v := DefaultLibrary()
	return v, s.get(ctx, KeyLibrary, &v)
}

// SMTP returns SMTP settings (zero value = disabled).
func (s Store) SMTP(ctx context.Context) (SMTP, error) {
	var v SMTP
	return v, s.get(ctx, KeySMTP, &v)
}

// Ntfy returns ntfy settings.
func (s Store) Ntfy(ctx context.Context) (Ntfy, error) {
	var v Ntfy
	return v, s.get(ctx, KeyNtfy, &v)
}

// Bandwidth returns bandwidth settings.
func (s Store) Bandwidth(ctx context.Context) (Bandwidth, error) {
	v := DefaultBandwidth()
	return v, s.get(ctx, KeyBandwidth, &v)
}

// Auth returns auth settings.
func (s Store) Auth(ctx context.Context) (Auth, error) {
	var v Auth
	return v, s.get(ctx, KeyAuth, &v)
}

// Quotas returns quota settings.
func (s Store) Quotas(ctx context.Context) (Quotas, error) {
	var v Quotas
	return v, s.get(ctx, KeyQuotas, &v)
}

// PermissionDefaults returns the instance capability defaults.
func (s Store) PermissionDefaults(ctx context.Context) (authz.Defaults, error) {
	m := map[string]bool{}
	if err := s.get(ctx, KeyPermissions, &m); err != nil {
		return nil, err
	}
	d := authz.DefaultCapabilities()
	for k, v := range m {
		d[authz.Action(k)] = v
	}
	return d, nil
}

// SetupCompleted reports whether the wizard has finished.
func (s Store) SetupCompleted(ctx context.Context) (bool, error) {
	var at int64
	err := store.GetSetting(ctx, s.Q, s.Enc, KeySetupDone, &at)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil && at > 0, err
}

// Set stores a group. Secret groups are encrypted.
func (s Store) Set(ctx context.Context, key string, value any) error {
	secret := key == KeySMTP || key == KeyNtfy
	return store.SetSetting(ctx, s.Q, s.Enc, key, value, secret)
}
