// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/testdb"
)

// b64Enc stands in for config.Secrets: reversible, but the plaintext doesn't
// show in the stored value.
type b64Enc struct{}

func (b64Enc) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (b64Enc) Decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

func rawSetting(t *testing.T, db *store.DB, key string) (string, bool) {
	t.Helper()
	var (
		val    string
		secret bool
	)
	if err := db.QueryRow(context.Background(), `SELECT value, secret FROM settings WHERE key = ?`, key).Scan(&val, &secret); err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return val, secret
}

func TestDefaultsWhenUnset(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := Store{Q: db, Enc: b64Enc{}}
		if v, err := s.Instance(ctx); err != nil || v != DefaultInstance() {
			t.Fatalf("instance: %+v, %v", v, err)
		}
		if v, err := s.Library(ctx); err != nil || v != DefaultLibrary() {
			t.Fatalf("library: %+v, %v", v, err)
		}
		if v, err := s.Bandwidth(ctx); err != nil || v != DefaultBandwidth() {
			t.Fatalf("bandwidth: %+v, %v", v, err)
		}
		if v, err := s.Metadata(ctx); err != nil || v != DefaultMetadata() {
			t.Fatalf("metadata: %+v, %v", v, err)
		}
		if v, err := s.SMTP(ctx); err != nil || v.Enabled() {
			t.Fatalf("smtp: %+v, %v", v, err)
		}
		if done, err := s.SetupCompleted(ctx); err != nil || done {
			t.Fatalf("setup completed: %v, %v", done, err)
		}
		d, err := s.PermissionDefaults(ctx)
		if err != nil || len(d) != len(authz.DefaultCapabilities()) {
			t.Fatalf("permission defaults: %v, %v", d, err)
		}
	})
}

func TestStoredValuesOverlayDefaults(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := Store{Q: db, Enc: b64Enc{}}
		// A stored group missing newer fields keeps their defaults.
		if err := store.SetSetting(ctx, db, nil, KeyBandwidth, map[string]int{"egress_mbps": 20}, false); err != nil {
			t.Fatal(err)
		}
		bw, err := s.Bandwidth(ctx)
		if err != nil || bw.EgressMbps != 20 || bw.HLSCacheGB != DefaultBandwidth().HLSCacheGB {
			t.Fatalf("bandwidth: %+v, %v", bw, err)
		}
		// Permission overrides change only the actions they name.
		if err := s.Set(ctx, KeyPermissions, map[string]bool{string(authz.Upload): false}); err != nil {
			t.Fatal(err)
		}
		d, err := s.PermissionDefaults(ctx)
		if err != nil || d[authz.Upload] || len(d) != len(authz.DefaultCapabilities()) {
			t.Fatalf("permission defaults: %v, %v", d, err)
		}
		if err := s.Set(ctx, KeySetupDone, int64(1)); err != nil {
			t.Fatal(err)
		}
		if done, err := s.SetupCompleted(ctx); err != nil || !done {
			t.Fatalf("setup completed: %v, %v", done, err)
		}
	})
}

func TestSecretGroupsAreEncrypted(t *testing.T) {
	testdb.ForEachDialect(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := Store{Q: db, Enc: b64Enc{}}
		const pw = "smtp-password-plaintext"
		in := SMTP{Host: "mail.example.org", Port: 587, TLS: "starttls", Password: pw, From: "a@example.org"}
		if err := s.Set(ctx, KeySMTP, in); err != nil {
			t.Fatal(err)
		}
		if raw, secret := rawSetting(t, db, KeySMTP); !secret || strings.Contains(raw, pw) {
			t.Fatalf("smtp stored in the clear: secret=%v value=%s", secret, raw)
		}
		got, err := s.SMTP(ctx)
		if err != nil || got != in {
			t.Fatalf("smtp round trip: %+v, %v", got, err)
		}
		for _, key := range []string{KeyNtfy, KeyMetadata} {
			if err := s.Set(ctx, key, map[string]string{"token": pw}); err != nil {
				t.Fatal(err)
			}
			if raw, secret := rawSetting(t, db, key); !secret || strings.Contains(raw, pw) {
				t.Fatalf("%s stored in the clear: %s", key, raw)
			}
		}
		if err := s.Set(ctx, KeyInstance, DefaultInstance()); err != nil {
			t.Fatal(err)
		}
		if _, secret := rawSetting(t, db, KeyInstance); secret {
			t.Fatal("instance settings marked secret")
		}
	})
}
