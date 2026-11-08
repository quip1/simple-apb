// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The embedded catalog must match the shared translations (run `go generate`).
func TestCatalogIsCurrent(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "i18n", "*.json"))
	if len(files) == 0 {
		t.Skip("shared catalogs not found")
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		var all map[string]string
		if err := json.Unmarshal(raw, &all); err != nil {
			t.Fatal(err)
		}
		emb, err := catalogFS.ReadFile("catalog/" + filepath.Base(f))
		if err != nil {
			t.Fatalf("%s is not embedded; run go generate ./...", filepath.Base(f))
		}
		var got map[string]string
		_ = json.Unmarshal(emb, &got)
		for k, v := range all {
			if strings.HasPrefix(k, "mail.") && got[k] != v {
				t.Errorf("%s: embedded %q differs from %q; run go generate ./...", k, got[k], v)
			}
		}
	}
}

func TestRenderAll(t *testing.T) {
	vars := map[string]string{"instance": "Demo <Shelf>", "name": "Ana", "reason": "nope", "event_type": "backup_failed"}
	for _, name := range []string{SignupApproved, SignupDenied, PasswordReset, AdminAlert} {
		m, err := Render(name, Data{To: "a@example.org", Locale: "en-GB", Vars: vars, Link: "https://x.example/p?a=1&b=2"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(m.Subject, "mail.") || strings.Contains(m.Text, "mail.") || strings.Contains(m.Text, "{") {
			t.Errorf("%s: untranslated or unfilled text: %q / %q", name, m.Subject, m.Text)
		}
		if strings.Contains(m.HTML, "<Shelf>") || !strings.Contains(m.HTML, "Demo &lt;Shelf&gt;") {
			t.Errorf("%s: HTML must escape values", name)
		}
	}
	if m, _ := Render(AdminAlert, Data{Vars: vars}); !strings.Contains(m.Subject, "The nightly backup failed") {
		t.Errorf("alert subject: %q", m.Subject)
	}
	if _, err := Render(PasswordReset, Data{Vars: vars}); err == nil {
		t.Error("a reset email needs a link")
	}
	if _, err := Render("nope", Data{}); err == nil {
		t.Error("unknown template")
	}
}

func TestCompose(t *testing.T) {
	raw := string(Compose("Books <b@example.org>", Message{To: "a@example.org", Subject: "Grüße", Text: "hi\nthere", HTML: "<p>hi</p>"}, time.Unix(0, 0)))
	for _, want := range []string{"Subject: =?utf-8?q?Gr=C3=BC=C3=9Fe?=", "multipart/alternative; boundary=", "Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: base64", "Auto-Submitted: auto-generated"} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "\n") && strings.Count(raw, "\n") != strings.Count(raw, "\r\n") {
		t.Error("every line must end in CRLF")
	}
}
