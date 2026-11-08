// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/mail"
	"github.com/quip1/simple-apb/server/internal/push"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Email (brief §6.15): optional, sent by a background job so a slow or
// unreachable mail server never delays a request, and failures retry.

// JobMail delivers one templated email.
const JobMail = "mail_send"

type mailPayload struct {
	Template string            `json:"template"`
	UserID   string            `json:"user_id"`
	Vars     map[string]string `json:"vars,omitempty"`
	Link     string            `json:"link,omitempty"`
}

// mailEnabled reports whether SMTP is configured.
func (a *App) mailEnabled(ctx context.Context) bool {
	cfg, err := a.Settings().SMTP(ctx)
	return err == nil && cfg.Enabled()
}

// baseURL is the configured public address ("" if not set). Links in emails
// only ever use it, never the request's Host header, so a forged Host can't
// put an attacker's address in a reset email.
func (a *App) baseURL(ctx context.Context) string {
	inst, err := a.Settings().Instance(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimRight(inst.BaseURL, "/")
}

// queueMail queues an email to a user (no-op without SMTP).
func (a *App) queueMail(ctx context.Context, q store.Querier, p mailPayload) {
	if !a.mailEnabled(ctx) {
		return
	}
	if _, err := jobs.Enqueue(context.WithoutCancel(ctx), q, JobMail, p, time.Time{}, ""); err != nil {
		a.log.Warn("queue email", "template", p.Template, "err", err)
		return
	}
	a.Jobs.Wake()
}

func (a *App) sendMailJob(ctx context.Context, j *jobs.Job) (any, error) {
	var p mailPayload
	if err := j.Decode(&p); err != nil {
		return nil, jobs.Permanent(err)
	}
	cfg, err := a.Settings().SMTP(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled() {
		return nil, jobs.Permanent(errSMTPOff)
	}
	u, err := store.GetUser(ctx, a.DB, p.UserID)
	if err != nil {
		return nil, jobs.Permanent(err)
	}
	if u.Email == "" {
		return map[string]string{"skipped": "no email address"}, nil
	}
	inst, err := a.Settings().Instance(ctx)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{"instance": inst.Name, "name": u.DisplayName}
	for k, v := range p.Vars {
		vars[k] = v
	}
	msg, err := mail.Render(p.Template, mail.Data{To: u.Email, Locale: u.Locale, Vars: vars, Link: p.Link})
	if err != nil {
		return nil, jobs.Permanent(err)
	}
	if err := mail.SendMessage(ctx, cfg, msg); err != nil {
		return nil, err // retried with backoff
	}
	// Never log or return the address or the link (a reset link is a secret).
	return map[string]string{"template": p.Template}, nil
}

var errSMTPOff = apiErr(503, "smtp_off", "email is not set up")

// adminAlertsFrom queues email alerts for admins who opted in (prefs
// "email_alerts"), for the admin push events (brief §6.15). It runs on the
// push job's first attempt only, so push retries don't repeat emails.
func (a *App) adminAlertsFrom(ctx context.Context, j *jobs.Job) {
	if j.Attempts > 1 {
		return
	}
	var p struct {
		To    push.Recipients `json:"to"`
		Event push.Event      `json:"event"`
	}
	if err := j.Decode(&p); err != nil || !p.To.Admins || !a.mailEnabled(ctx) {
		return
	}
	rows, err := a.DB.Query(ctx, `SELECT id, prefs FROM users WHERE role = 'admin' AND status = 'active' AND email IS NOT NULL`)
	if err != nil {
		a.log.Warn("admin alert recipients", "err", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id, prefs string
		if rows.Scan(&id, &prefs) != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(prefs), &m) == nil && m["email_alerts"] == true {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	link := ""
	if base := a.baseURL(ctx); base != "" {
		link = base + "/admin"
	}
	for _, id := range ids {
		a.queueMail(ctx, a.DB, mailPayload{Template: mail.AdminAlert, UserID: id, Vars: map[string]string{"event_type": p.Event.Type}, Link: link})
	}
}

// RequestPasswordReset emails a reset link (always 202; see the spec).
func (s *Server) RequestPasswordReset(ctx context.Context, req RequestPasswordResetRequestObject) (RequestPasswordResetResponseObject, error) {
	login := strings.TrimSpace(req.Body.Login)
	if login == "" || utf8.RuneCountInString(login) > 254 {
		return nil, badRequest("invalid_login", "enter your username or email address")
	}
	app := s.App()
	base := app.baseURL(ctx)
	if base == "" || !app.mailEnabled(ctx) {
		// Same answer as always; the sign-in page hides the option anyway.
		return RequestPasswordReset202Response{}, nil
	}
	u, token, err := app.Auth.RequestReset(ctx, login, clientIP(ctx))
	if err != nil {
		return nil, err
	}
	if u != nil {
		app.queueMail(ctx, app.DB, mailPayload{Template: mail.PasswordReset, UserID: u.ID, Link: base + "/reset-password#" + token})
		_ = store.Audit(ctx, app.DB, store.AuditEntry{ActorID: u.ID, Action: "user.reset_email", ObjectType: "user", ObjectID: u.ID, IP: clientIP(ctx)})
	}
	return RequestPasswordReset202Response{}, nil
}
