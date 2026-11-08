// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mail sends optional SMTP email: translated plain-text + HTML
// templates (templates.go), delivered as multipart/alternative.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/settings"
)

// Send delivers a plain-text message.
func Send(ctx context.Context, cfg settings.SMTP, to, subject, body string) error {
	return SendMessage(ctx, cfg, Message{To: to, Subject: subject, Text: body})
}

// SendMessage delivers a message (with an HTML part when it has one).
func SendMessage(ctx context.Context, cfg settings.SMTP, m Message) error {
	if !cfg.Enabled() {
		return errors.New("smtp is not configured")
	}
	if strings.ContainsAny(m.To+m.Subject+cfg.From, "\r\n") {
		return errors.New("invalid header value")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := &net.Dialer{Deadline: deadline}
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}

	var (
		conn net.Conn
		err  error
	)
	if cfg.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = c.Close() }()
	if cfg.TLS == "starttls" {
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(Compose(cfg.From, m, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Compose builds the RFC 5322 message: the subject encoded as needed and
// base64 bodies (plain text, plus HTML as multipart/alternative).
func Compose(from string, m Message, now time.Time) []byte {
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", now.UTC().Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	part := func(ctype, body string) {
		b.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		enc := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
		for len(enc) > 76 {
			b.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		b.WriteString(enc + "\r\n")
	}
	if m.HTML == "" {
		part("text/plain", m.Text)
		return []byte(b.String())
	}
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	boundary := "sapb-" + hex.EncodeToString(rnd[:])
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\n")
	part("text/plain", m.Text)
	b.WriteString("--" + boundary + "\r\n")
	part("text/html", m.HTML)
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}
