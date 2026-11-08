// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"crypto/subtle"
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
// It does not prevent reuse; use MatchTOTPStep + store.ClaimTOTPStep for logins.
func ValidateTOTP(secret, code string, now time.Time) bool {
	_, ok := MatchTOTPStep(secret, code, now)
	return ok
}

// MatchTOTPStep returns the 30-second time step a valid code belongs to
// (checking the current step ±1 for clock skew). Callers store the step and
// reject codes from a step that was already used (DECISIONS Q5).
func MatchTOTPStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	base := now.Unix() / 30
	for _, d := range []int64{0, -1, 1} {
		step := base + d
		want, err := totp.GenerateCodeCustom(secret, time.Unix(step*30, 0), totp.ValidateOpts{
			Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
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
