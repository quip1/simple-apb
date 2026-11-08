// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SignedTTL is how long a signed stream URL works (brief §6.9).
const SignedTTL = 6 * time.Hour

// Signer makes short-lived stream tokens bound to a user and a book, so
// casting devices (which can't send our auth headers) can fetch audio.
type Signer struct{ Key []byte }

// ErrBadSignature covers forged, expired and mismatched tokens.
var ErrBadSignature = errors.New("invalid or expired stream signature")

// Sign returns a token for user+book valid until exp.
func (s Signer) Sign(userID, bookID string, exp time.Time) string {
	payload := "v1." + userID + "." + bookID + "." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.mac(payload)
}

func (s Signer) mac(payload string) string {
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Verify checks a token for the given book and returns the user id.
func (s Signer) Verify(token, bookID string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || parts[0] != "v1" {
		return "", ErrBadSignature
	}
	payload := strings.Join(parts[:4], ".")
	if !hmac.Equal([]byte(parts[4]), []byte(s.mac(payload))) {
		return "", ErrBadSignature
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || now.Unix() >= exp || parts[2] != bookID {
		return "", ErrBadSignature
	}
	return parts[1], nil
}
