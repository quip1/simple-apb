// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AccessTTL        = 15 * time.Minute
	RefreshTTL       = 90 * 24 * time.Hour // sliding: each refresh extends it
	TOTPChallengeTTL = 5 * time.Minute
	issuer           = "simpleapb"
)

// AccessClaims are carried in the 15-minute access JWT.
type AccessClaims struct {
	SessionID string `json:"sid"`
	Type      string `json:"typ"`
	jwt.RegisteredClaims
}

// ChallengeClaims carry a half-finished login waiting for a TOTP code.
type ChallengeClaims struct {
	Type       string `json:"typ"`
	DeviceID   string `json:"did"`
	DeviceName string `json:"dnm"`
	Platform   string `json:"plt"`
	AppVersion string `json:"ver"`
	jwt.RegisteredClaims
}

// Tokens signs and verifies JWTs with an HMAC key derived from secret.key.
type Tokens struct {
	key []byte
	now func() time.Time
}

// NewTokens builds a signer; key must be ≥32 bytes.
func NewTokens(key []byte) *Tokens { return &Tokens{key: key, now: time.Now} }

func (t *Tokens) sign(c jwt.Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(t.key)
}

func (t *Tokens) parse(tok string, c jwt.Claims) error {
	_, err := jwt.ParseWithClaims(tok, c, func(*jwt.Token) (any, error) { return t.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(issuer), jwt.WithTimeFunc(t.now),
		jwt.WithExpirationRequired())
	return err
}

// IssueAccess creates an access token for a session.
func (t *Tokens) IssueAccess(userID, sessionID string) (string, time.Time, error) {
	now := t.now()
	exp := now.Add(AccessTTL)
	s, err := t.sign(AccessClaims{SessionID: sessionID, Type: "access", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: issuer, Subject: userID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(exp),
	}})
	return s, exp, err
}

// ErrInvalidToken covers expired, forged and malformed tokens.
var ErrInvalidToken = errors.New("invalid token")

// ParseAccess validates an access token.
func (t *Tokens) ParseAccess(tok string) (*AccessClaims, error) {
	var c AccessClaims
	if err := t.parse(tok, &c); err != nil || c.Type != "access" || c.SessionID == "" {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

// IssueChallenge creates a short-lived TOTP challenge after a correct password.
func (t *Tokens) IssueChallenge(userID string, d Device) (string, error) {
	now := t.now()
	return t.sign(ChallengeClaims{Type: "totp_challenge", DeviceID: d.ID, DeviceName: d.Name, Platform: d.Platform,
		AppVersion: d.AppVersion, RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Subject: userID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(TOTPChallengeTTL)),
		}})
}

// ParseChallenge validates a TOTP challenge.
func (t *Tokens) ParseChallenge(tok string) (*ChallengeClaims, error) {
	var c ChallengeClaims
	if err := t.parse(tok, &c); err != nil || c.Type != "totp_challenge" {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

// NewOpaqueToken returns a random URL-safe token and its SHA-256 hex hash
// (only the hash is stored).
func NewOpaqueToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("random token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashOpaque(token), nil
}

// HashOpaque hashes an opaque token for storage/lookup. High-entropy tokens
// don't need a slow hash; SHA-256 is enough.
func HashOpaque(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
