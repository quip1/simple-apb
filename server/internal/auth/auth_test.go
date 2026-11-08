// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestPasswordHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format %s", h)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery"); !ok {
		t.Fatal("correct password rejected")
	}
	if ok, _ := VerifyPassword(h, "wrong horse battery"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("$bcrypt$x", "x"); err == nil {
		t.Fatal("expected unsupported format error")
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := map[string]PasswordProblem{
		"short":                   PasswordTooShort,
		"password1234":            PasswordCommon,
		"PASSWORD1234":            PasswordCommon,
		"alicealice-is-me":        PasswordIsUsername,
		"tangerine-orbit-quietly": PasswordAcceptable,
		strings.Repeat("x", 300):  PasswordTooLong,
		"ünïcödé-pässwörd":        PasswordAcceptable,
	}
	for pw, want := range cases {
		if got := CheckPassword(pw, "alice"); got != want {
			t.Errorf("CheckPassword(%q) = %q, want %q", pw, got, want)
		}
	}
	if len(commonPasswords()) < 5000 {
		t.Fatalf("common password list too small: %d", len(commonPasswords()))
	}
}

func TestTokens(t *testing.T) {
	tk := NewTokens([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Now()
	tk.now = func() time.Time { return now }
	s, _, err := tk.IssueAccess("u1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := tk.ParseAccess(s)
	if err != nil || c.Subject != "u1" || c.SessionID != "s1" {
		t.Fatalf("parse: %+v %v", c, err)
	}
	// A challenge token is not an access token.
	ch, _ := tk.IssueChallenge("u1", Device{ID: "d"})
	if _, err := tk.ParseAccess(ch); err == nil {
		t.Fatal("challenge accepted as access token")
	}
	// Expiry.
	tk.now = func() time.Time { return now.Add(AccessTTL + time.Minute) }
	if _, err := tk.ParseAccess(s); err == nil {
		t.Fatal("expired token accepted")
	}
	// Wrong key.
	other := NewTokens([]byte("ffffffffffffffffffffffffffffffff"))
	if _, err := other.ParseAccess(s); err == nil {
		t.Fatal("token from another key accepted")
	}
}

func TestTOTPAndRecovery(t *testing.T) {
	secret, uri, err := NewTOTPSecret("Simple APB", "alice")
	if err != nil || !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatal(err, uri)
	}
	now := time.Now()
	code, _ := totp.GenerateCode(secret, now)
	if !ValidateTOTP(secret, code, now) {
		t.Fatal("valid code rejected")
	}
	if ValidateTOTP(secret, code, now.Add(5*time.Minute)) {
		t.Fatal("stale code accepted")
	}
	codes, hashes, _ := NewRecoveryCodes(10)
	if len(codes) != 10 || HashRecoveryCode(strings.ToUpper(codes[3])) != hashes[3] {
		t.Fatal("recovery code normalization broken")
	}
}

func TestLimiterAndLockout(t *testing.T) {
	l := NewLimiter(5, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := range 5 {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("request %d denied", i)
		}
	}
	ok, wait := l.Allow("ip")
	if ok || wait <= 0 {
		t.Fatal("6th request should be limited")
	}
	if ok, _ := l.Allow("other-ip"); !ok {
		t.Fatal("keys must be independent")
	}
	now = now.Add(13 * time.Second)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("token should refill after 12s")
	}

	if LockoutDuration(9) != 0 || LockoutDuration(10) != time.Minute || LockoutDuration(12) != 4*time.Minute {
		t.Fatal("lockout schedule wrong")
	}
	if LockoutDuration(100) != 24*time.Hour {
		t.Fatal("lockout must cap at 24h")
	}
}
