// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// NewTOTPSecret creates a TOTP key for an account.
func NewTOTPSecret(issuerName, account string) (secret, uri string, err error) {
	k, err := totp.Generate(totp.GenerateOpts{Issuer: issuerName, AccountName: account, Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		return "", "", err
	}
	return k.Secret(), k.URL(), nil
}

// ValidateTOTP checks a 6-digit code, allowing ±1 step of clock skew.
func ValidateTOTP(secret, code string, now time.Time) bool {
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, now, totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// NewRecoveryCodes returns n codes like "k7wq-3mzp-x2rd" and their hashes.
func NewRecoveryCodes(n int) (codes, hashes []string, err error) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for range n {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		s := strings.ToLower(enc.EncodeToString(b))[:12]
		code := s[0:4] + "-" + s[4:8] + "-" + s[8:12]
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}

// HashRecoveryCode normalizes (case, dashes, spaces) then hashes a code.
func HashRecoveryCode(code string) string {
	c := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
	return HashOpaque("recovery:" + c)
}
