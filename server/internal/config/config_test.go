// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"testing"
)

func TestBootstrapRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadBootstrap(dir); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	if err := SaveBootstrap(dir, &Bootstrap{DBDialect: "sqlite"}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBootstrap(dir)
	if err != nil || b.DBDialect != "sqlite" || b.Version != 1 {
		t.Fatalf("got %+v, %v", b, err)
	}
}

func TestSecretsPersistAndEncrypt(t *testing.T) {
	dir := t.TempDir()
	s1, err := LoadOrCreateSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := LoadOrCreateSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s1.Derive("jwt"), s2.Derive("jwt")) {
		t.Fatal("key changed between loads")
	}
	if bytes.Equal(s1.Derive("jwt"), s1.Derive("stream-url")) {
		t.Fatal("derived keys must differ per purpose")
	}
	enc, err := s1.Encrypt("hunter22-smtp")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := s2.Decrypt(enc)
	if err != nil || pt != "hunter22-smtp" {
		t.Fatalf("decrypt: %q %v", pt, err)
	}
	other, _ := NewSecrets(bytes.Repeat([]byte{1}, 32))
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}
