// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a tiny plain-text SMTP server that records messages.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []sentMail
}

type sentMail struct {
	To, Subject, Text, HTML string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	say("220 fake ESMTP")
	var to string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			to = strings.Trim(strings.TrimSpace(line)[8:], "<> \r\n")
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, "."))
			}
			f.record(to, data.String())
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) record(to, raw string) {
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return
	}
	dec := new(mime.WordDecoder)
	subj, _ := dec.DecodeHeader(m.Header.Get("Subject"))
	out := sentMail{To: to, Subject: subj}
	_, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if b := params["boundary"]; b != "" {
		mr := multipart.NewReader(m.Body, b)
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			body, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
			if strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") {
				out.HTML = string(body)
			} else {
				out.Text = string(body)
			}
		}
	}
	f.mu.Lock()
	f.msgs = append(f.msgs, out)
	f.mu.Unlock()
}

// wait returns the first message to `to` whose subject contains `subj`.
func (f *fakeSMTP) wait(t *testing.T, to, subj string) sentMail {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, m := range f.msgs {
			if m.To == to && strings.Contains(m.Subject, subj) {
				f.mu.Unlock()
				return m
			}
		}
		f.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no email to %s about %q", to, subj)
	return sentMail{}
}

func (f *fakeSMTP) count(to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.msgs {
		if m.To == to {
			n++
		}
	}
	return n
}

var linkRe = regexp.MustCompile(`https?://\S+`)

func TestEmail(t *testing.T) { forEachDialect(t, testEmail) }

func testEmail(t *testing.T, h *harness) {
	secret := h.setupInstance()
	admin := h.adminLogin(secret)
	A := bearer(admin)
	smtpSrv := newFakeSMTP(t)

	// Without SMTP and an instance address, email reset is off (and the
	// endpoint still answers 202 without revealing anything).
	if r := h.do(nil, "GET", "/instance", nil, nil); r.body["email_password_reset"] != false {
		t.Fatalf("instance: %s", r.raw)
	}
	expect(t, h.do(nil, "POST", "/auth/password-reset/request", map[string]any{"login": "nobody"}, nil), 202, "reset without smtp")

	expect(t, h.do(nil, "PATCH", "/admin/settings", map[string]any{
		"instance": map[string]any{"base_url": "https://books.example.org"},
		"smtp":     map[string]any{"host": "127.0.0.1", "port": smtpSrv.port(), "tls": "none", "from": "Books <books@example.org>"},
	}, A), 200, "settings")
	if r := h.do(nil, "GET", "/instance", nil, nil); r.body["email_password_reset"] != true {
		t.Fatalf("instance after smtp: %s", r.raw)
	}

	// Sign-up approved and declined emails.
	signup := func(name, email string) {
		expect(t, h.do(nil, "POST", "/auth/signup", map[string]any{"username": name, "password": "marble-canyon-whistle", "email": email,
			"age_confirmed": true, "terms_accepted": true}, nil), 202, "signup "+name)
	}
	signup("ana", "ana@example.org")
	signup("bo", "bo@example.org")
	for _, r := range h.do(nil, "GET", "/admin/signup-requests", nil, A).list {
		m := r.(map[string]any)
		switch m["username"] {
		case "ana":
			expect(t, h.do(nil, "POST", "/admin/signup-requests/"+m["id"].(string)+"/approve", nil, A), 204, "approve")
		case "bo":
			expect(t, h.do(nil, "POST", "/admin/signup-requests/"+m["id"].(string)+"/deny", map[string]any{"reason": "Friends only <b>sorry</b>"}, A), 204, "deny")
		}
	}
	m := smtpSrv.wait(t, "ana@example.org", "account is ready")
	if !strings.Contains(m.Text, "https://books.example.org/login") || !strings.Contains(m.HTML, `href="https://books.example.org/login"`) {
		t.Fatalf("approval mail: %+v", m)
	}
	m = smtpSrv.wait(t, "bo@example.org", "account request")
	if !strings.Contains(m.Text, "Friends only <b>sorry</b>") || strings.Contains(m.HTML, "<b>sorry") || !strings.Contains(m.HTML, "&lt;b&gt;sorry") {
		t.Fatalf("denial mail must carry the reason, escaped in HTML: %+v", m)
	}

	// Forgot password: by username or email; unknown logins look the same.
	expect(t, h.do(nil, "POST", "/auth/password-reset/request", map[string]any{"login": "nobody@example.org"}, nil), 202, "unknown")
	expect(t, h.do(nil, "POST", "/auth/password-reset/request", map[string]any{"login": "ANA@example.org"}, nil), 202, "by email")
	m = smtpSrv.wait(t, "ana@example.org", "Reset your")
	link := linkRe.FindString(m.Text)
	if !strings.HasPrefix(link, "https://books.example.org/reset-password#") {
		t.Fatalf("reset link must use the configured address: %q", link)
	}
	token := strings.TrimPrefix(link, "https://books.example.org/reset-password#")
	expect(t, h.do(nil, "POST", "/auth/password-reset", map[string]any{"token": token, "new_password": "quiet-harbor-lantern-7"}, nil), 204, "reset")
	expect(t, h.do(nil, "POST", "/auth/login", map[string]any{"username": "ana", "password": "quiet-harbor-lantern-7", "device": device("web")}, nil), 200, "new password")

	// A forged Host header can't change the link (only the configured address is used).
	r := h.do(nil, "POST", "/auth/password-reset/request", map[string]any{"login": "ana"}, map[string]string{"Host": "evil.example"})
	expect(t, r, 202, "forged host")

	// Per-account limit: at most 3 emails an hour, then silence (still 202).
	for range 3 {
		h.do(nil, "POST", "/auth/password-reset/request", map[string]any{"login": "ana"}, nil)
	}
	time.Sleep(500 * time.Millisecond)
	if n := smtpSrv.count("ana@example.org"); n > 1+3 {
		t.Fatalf("too many reset emails: %d", n)
	}

	// Admin alerts go to admins who opted in.
	expect(t, h.do(nil, "PATCH", "/me", map[string]any{"email": "admin@example.org", "prefs": map[string]any{"email_alerts": true}}, A), 200, "opt in")
	signup("cy", "cy@example.org")
	m = smtpSrv.wait(t, "admin@example.org", "New account request")
	if !strings.Contains(m.Text, "https://books.example.org/admin") {
		t.Fatalf("alert: %+v", m)
	}
	for _, x := range []sentMail{m} {
		if strings.Contains(x.Text, "cy@example.org") {
			t.Fatal("alerts carry no personal details")
		}
	}
}
