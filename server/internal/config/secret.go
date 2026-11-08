// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/quip1/simple-apb/server/internal/fsutil"
)

// Secrets holds the instance master key from /config/secret.key and derives
// purpose-specific keys from it with HKDF, so one leaked derived key (e.g. the
// JWT key) never reveals the others.
type Secrets struct {
	master []byte
	aead   cipher.AEAD
}

const secretFile = "secret.key"

// LoadOrCreateSecrets reads secret.key, generating it on first run.
func LoadOrCreateSecrets(configDir string) (*Secrets, error) {
	p := filepath.Join(configDir, secretFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		enc := []byte(base64.StdEncoding.EncodeToString(key) + "\n")
		if err := fsutil.WriteFileAtomic(p, enc, 0o600); err != nil {
			return nil, fmt.Errorf("write secret.key: %w", err)
		}
		return NewSecrets(key)
	}
	if err != nil {
		return nil, fmt.Errorf("read secret.key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(string(trimSpace(b)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("secret.key is corrupt (want 32 base64-encoded bytes)")
	}
	return NewSecrets(key)
}

// NewSecrets builds Secrets from a 32-byte master key.
func NewSecrets(master []byte) (*Secrets, error) {
	encKey, err := hkdf.Key(sha256.New, master, nil, "simpleapb settings encryption v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Secrets{master: master, aead: aead}, nil
}

// Derive returns a 32-byte key for a named purpose (e.g. "jwt", "stream-url").
func (s *Secrets) Derive(purpose string) []byte {
	k, err := hkdf.Key(sha256.New, s.master, nil, "simpleapb "+purpose+" v1", 32)
	if err != nil {
		panic(err) // only fails for absurd lengths
	}
	return k
}

// Encrypt seals plaintext with AES-256-GCM; output is base64(nonce|ciphertext).
func (s *Secrets) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt.
func (s *Secrets) Decrypt(enc string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	n := s.aead.NonceSize()
	if len(b) < n {
		return "", errors.New("ciphertext too short")
	}
	pt, err := s.aead.Open(nil, b[:n], b[n:], nil)
	if err != nil {
		return "", errors.New("decrypt secret: wrong key or corrupted value")
	}
	return string(pt), nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
