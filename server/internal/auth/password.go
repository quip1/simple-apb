// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth handles credentials and sessions: argon2id passwords, JWT
// access tokens, rotating refresh tokens, TOTP, rate limits and login lockout.
package auth

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: OWASP's minimum recommendation (19 MiB, 2 passes, 1 lane).
// Kept modest so the server stays under its RAM target with concurrent logins;
// the rate limiter bounds how many hashes run at once.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// MinPasswordLength is the minimum password length (brief §6.6).
const MinPasswordLength = 10

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a PHC argon2id hash in constant time.
func VerifyPassword(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash format")
	}
	var (
		version    int
		mem, iters uint32
		threads    uint8
	)
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iters, &threads); err != nil {
		return false, errors.New("bad argon2 parameters")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, iters, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when a username doesn't exist, so login
// timing doesn't reveal which usernames are registered.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("not-a-real-password-just-timing")
	return h
})

//go:embed common-passwords.txt
var commonPasswordsRaw []byte

var commonPasswords = sync.OnceValue(func() map[string]struct{} {
	m := make(map[string]struct{}, 10000)
	sc := bufio.NewScanner(bytes.NewReader(commonPasswordsRaw))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		m[line] = struct{}{}
	}
	return m
})

// PasswordProblem is a stable reason code a client can translate.
type PasswordProblem string

const (
	PasswordTooShort    PasswordProblem = "password_too_short"
	PasswordTooLong     PasswordProblem = "password_too_long"
	PasswordCommon      PasswordProblem = "password_too_common"
	PasswordIsUsername  PasswordProblem = "password_matches_username"
	PasswordAcceptable  PasswordProblem = ""
	maxPasswordLenBytes                 = 256
)

// CheckPassword applies the password policy. Returns "" when acceptable.
func CheckPassword(pw, username string) PasswordProblem {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return PasswordTooShort
	}
	if len(pw) > maxPasswordLenBytes {
		return PasswordTooLong
	}
	lower := strings.ToLower(pw)
	if username != "" && strings.Contains(lower, strings.ToLower(username)) && len(username) >= 4 {
		return PasswordIsUsername
	}
	if _, bad := commonPasswords()[lower]; bad {
		return PasswordCommon
	}
	return PasswordAcceptable
}
