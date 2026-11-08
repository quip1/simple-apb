// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

//go:generate go run gen.go

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"path"
	"strings"
	"sync"
)

// Templates (brief §6.15).
const (
	SignupApproved = "signup_approved"
	SignupDenied   = "signup_denied"
	PasswordReset  = "password_reset"
	AdminAlert     = "admin_alert"
)

//go:embed catalog/*.json
var catalogFS embed.FS

var (
	catalogsOnce sync.Once
	catalogs     map[string]map[string]string
)

func loadCatalogs() {
	catalogs = map[string]map[string]string{}
	entries, _ := catalogFS.ReadDir("catalog")
	for _, e := range entries {
		raw, err := catalogFS.ReadFile(path.Join("catalog", e.Name()))
		if err != nil {
			continue
		}
		m := map[string]string{}
		if json.Unmarshal(raw, &m) == nil {
			catalogs[strings.TrimSuffix(e.Name(), ".json")] = m
		}
	}
}

// T looks a string up for a locale (falling back to its base language,
// then English) and fills {placeholders}. Mail strings use plain
// placeholders only (no plurals).
func T(locale, key string, vars map[string]string) string {
	catalogsOnce.Do(loadCatalogs)
	s := ""
	for _, l := range []string{locale, strings.SplitN(locale, "-", 2)[0], "en"} {
		if v, ok := catalogs[l][key]; ok && l != "" {
			s = v
			break
		}
	}
	if s == "" {
		return key
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// Message is a rendered email.
type Message struct {
	To, Subject, Text, HTML string
}

// Data fills a template. Vars always include "instance" and "name".
type Data struct {
	To, Locale string
	Vars       map[string]string
	// Link is the button target (sign-in page, reset link, admin panel).
	Link string
}

type layout struct {
	Paragraphs   []string
	Button, Link string
	After        []string
	Footer       string
}

// Render builds the plain-text and HTML versions of a template.
func Render(name string, d Data) (Message, error) {
	vars := map[string]string{}
	for k, v := range d.Vars {
		vars[k] = v
	}
	tr := func(key string) string { return T(d.Locale, "mail."+key, vars) }
	l := layout{Footer: tr("footer"), Link: d.Link}
	hello := tr("hello")
	var subject string
	switch name {
	case SignupApproved:
		subject = tr("signup_approved.subject")
		l.Paragraphs = []string{hello, tr("signup_approved.body")}
		if d.Link != "" {
			l.Button = tr("signup_approved.button")
		}
	case SignupDenied:
		subject = tr("signup_denied.subject")
		l.Paragraphs = []string{hello, tr("signup_denied.body")}
		if vars["reason"] != "" {
			l.Paragraphs = append(l.Paragraphs, tr("signup_denied.reason"))
		}
	case PasswordReset:
		if d.Link == "" {
			return Message{}, fmt.Errorf("password reset needs a link")
		}
		subject = tr("password_reset.subject")
		l.Paragraphs = []string{hello, tr("password_reset.body")}
		l.Button = tr("password_reset.button")
		l.After = []string{tr("password_reset.ignore")}
	case AdminAlert:
		vars["event"] = T(d.Locale, "mail.event."+vars["event_type"], vars)
		subject = tr("admin_alert.subject")
		l.Paragraphs = []string{hello, tr("admin_alert.body")}
		if d.Link != "" {
			l.Button = tr("admin_alert.button")
		}
		l.After = []string{tr("admin_alert.why")}
	default:
		return Message{}, fmt.Errorf("unknown mail template %q", name)
	}
	var text strings.Builder
	for _, p := range l.Paragraphs {
		text.WriteString(p + "\n\n")
	}
	if l.Button != "" {
		text.WriteString(l.Button + ":\n" + l.Link + "\n\n")
	}
	for _, p := range l.After {
		text.WriteString(p + "\n\n")
	}
	text.WriteString("-- \n" + l.Footer + "\n")
	var html bytes.Buffer
	if err := htmlTemplate.Execute(&html, l); err != nil {
		return Message{}, err
	}
	return Message{To: d.To, Subject: subject, Text: text.String(), HTML: html.String()}, nil
}

// Simple, inline-styled HTML (mail clients ignore stylesheets). Every value is
// escaped by html/template; only https/http links reach the href.
var htmlTemplate = template.Must(template.New("mail").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;padding:24px;background:#f4f4f5;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#18181b">
<div style="max-width:520px;margin:0 auto;background:#ffffff;border-radius:12px;padding:28px">
{{range .Paragraphs}}<p style="margin:0 0 16px;line-height:1.5">{{.}}</p>
{{end}}{{if .Button}}<p style="margin:24px 0"><a href="{{.Link}}" style="display:inline-block;padding:12px 20px;border-radius:8px;background:#18181b;color:#ffffff;text-decoration:none;font-weight:600">{{.Button}}</a></p>
<p style="margin:0 0 16px;font-size:13px;color:#52525b;word-break:break-all">{{.Link}}</p>
{{end}}{{range .After}}<p style="margin:0 0 16px;line-height:1.5;color:#52525b">{{.}}</p>
{{end}}<hr style="border:0;border-top:1px solid #e4e4e7;margin:24px 0 12px">
<p style="margin:0;font-size:13px;color:#71717a">{{.Footer}}</p>
</div></body></html>
`))
