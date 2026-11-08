// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mail sends optional SMTP email (plain text; HTML templates arrive
// with the translated templates in Phase 2).
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/settings"
)

// Send delivers a plain-text message.
func Send(ctx context.Context, cfg settings.SMTP, to, subject, body string) error {
	if !cfg.Enabled() {
		return errors.New("smtp is not configured")
	}
	if strings.ContainsAny(to+subject, "\r\n") {
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
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + cfg.From + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nDate: " +
		time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
