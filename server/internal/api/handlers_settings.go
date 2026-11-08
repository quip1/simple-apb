// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/setup"
	"github.com/quip1/simple-apb/server/internal/store"
)

// ── audit log (brief §6.16: filterable, CSV export) ──

const auditExportMax = 100_000

func auditFilter(actor *AuditActor, action *AuditAction, otype *AuditObjectType, oid *AuditObjectId, from *AuditFrom, to *AuditTo) store.AuditFilter {
	f := store.AuditFilter{}
	if actor != nil {
		f.ActorID = actor.String()
	}
	if action != nil {
		f.Action = strings.TrimSpace(*action)
	}
	if otype != nil {
		f.ObjectType = *otype
	}
	if oid != nil {
		f.ObjectID = *oid
	}
	if from != nil {
		f.FromMS = from.UnixMilli()
	}
	if to != nil {
		f.ToMS = to.UnixMilli()
	}
	return f
}

func auditOut(r *store.AuditRow) AuditEntry {
	e := AuditEntry{Id: mustUUID(r.ID), At: msTime(r.At), Action: r.Action, ObjectType: strPtr(r.ObjectType),
		ObjectId: strPtr(r.ObjectID), Ip: strPtr(r.IP), ActorName: strPtr(r.ActorName), Details: map[string]any{}}
	if id, err := uuid.Parse(r.ActorID); err == nil {
		e.ActorId = &id
	}
	if r.Details != "" {
		_ = json.Unmarshal([]byte(r.Details), &e.Details)
	}
	return e
}

func (s *Server) ListAudit(ctx context.Context, req ListAuditRequestObject) (ListAuditResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	p := req.Params
	offset, err := decodeCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	limit := limitOf(p.Limit, 100)
	f := auditFilter(p.Actor, p.Action, p.ObjectType, p.ObjectId, p.From, p.To)
	f.Limit, f.Offset = limit+1, offset
	rows, err := store.ListAudit(ctx, s.App().DB, f)
	if err != nil {
		return nil, err
	}
	page := AuditPage{Items: []AuditEntry{}}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = encodeCursor(offset + limit)
	}
	for _, r := range rows {
		page.Items = append(page.Items, auditOut(r))
	}
	return ListAudit200JSONResponse(page), nil
}

// csvSafe stops spreadsheet apps from running cell contents as formulas.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (s *Server) ExportAudit(ctx context.Context, req ExportAuditRequestObject) (ExportAuditResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := auditFilter(p.Actor, p.Action, p.ObjectType, p.ObjectId, p.From, p.To)
	f.Limit = auditExportMax
	db := s.App().DB
	rows, err := store.ListAudit(ctx, db, f)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"at", "actor_id", "actor", "action", "object_type", "object_id", "ip", "details"})
	for _, r := range rows {
		_ = w.Write([]string{msTime(r.At).Format(time.RFC3339), r.ActorID, csvSafe(r.ActorName), csvSafe(r.Action),
			csvSafe(r.ObjectType), csvSafe(r.ObjectID), csvSafe(r.IP), csvSafe(r.Details)})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	_ = store.Audit(ctx, db, store.AuditEntry{ActorID: admin, Action: "audit.export", IP: clientIP(ctx),
		Details: map[string]any{"rows": len(rows)}})
	return ExportAudit200TextcsvResponse{Body: &buf, ContentLength: int64(buf.Len())}, nil
}

// ── settings ──

func (s *Server) adminSettings(ctx context.Context) (AdminSettings, error) {
	st := s.App().Settings()
	var out AdminSettings
	inst, err := st.Instance(ctx)
	if err != nil {
		return out, err
	}
	a, err := st.Auth(ctx)
	if err != nil {
		return out, err
	}
	bw, err := st.Bandwidth(ctx)
	if err != nil {
		return out, err
	}
	q, err := st.Quotas(ctx)
	if err != nil {
		return out, err
	}
	def, err := st.PermissionDefaults(ctx)
	if err != nil {
		return out, err
	}
	smtp, err := st.SMTP(ctx)
	if err != nil {
		return out, err
	}
	ntfy, err := st.Ntfy(ctx)
	if err != nil {
		return out, err
	}
	meta, err := st.Metadata(ctx)
	if err != nil {
		return out, err
	}
	tls := smtp.TLS
	if tls == "" {
		tls = "starttls"
	}
	out = AdminSettings{
		Instance: InstanceSettings{Name: inst.Name, BaseUrl: inst.BaseURL, SignupOpen: inst.SignupOpen, PrivacyText: inst.PrivacyText,
			TermsText: inst.TermsText, CopyrightContact: inst.CopyrightContact},
		Auth:      AuthSettings{RequireTotpAll: a.RequireTOTPAll},
		Bandwidth: BandwidthSettings{EgressMbps: bw.EgressMbps, HlsCacheGb: bw.HLSCacheGB},
		Quotas:    QuotaSettings{DefaultQuotaBytes: q.DefaultQuotaBytes},
		PermissionDefaults: EffectivePermissions{CanUpload: def[authz.Upload], CanEditMetadata: def[authz.EditMetadata],
			CanDeleteOwn: def[authz.DeleteOwn], CanShare: def[authz.Share], CanReview: def[authz.Review], CanPostPublic: def[authz.PostPublic]},
		Smtp: SmtpSettingsView{Host: smtp.Host, Port: smtp.Port, Tls: SmtpSettingsViewTls(tls), Username: smtp.Username, From: smtp.From,
			PasswordSet: smtp.Password != ""},
		Ntfy: NtfySettingsView{InternalUrl: ntfy.InternalURL, PublicUrl: ntfy.PublicURL, TokenSet: ntfy.Token != ""},
		Metadata: MetadataSettingsView{OpenLibrary: meta.OpenLibrary, GoogleBooks: meta.GoogleBooks, GoogleBooksKeySet: meta.GoogleAPIKey != "",
			Audnexus: meta.Audnexus, Threshold: float32(meta.Threshold)},
	}
	return out, nil
}

func (s *Server) GetAdminSettings(ctx context.Context, _ GetAdminSettingsRequestObject) (GetAdminSettingsResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	out, err := s.adminSettings(ctx)
	if err != nil {
		return nil, err
	}
	return GetAdminSettings200JSONResponse(out), nil
}

func maxLen(s *string, n int) bool { return s == nil || utf8.RuneCountInString(*s) <= n }

func (s *Server) UpdateAdminSettings(ctx context.Context, req UpdateAdminSettingsRequestObject) (UpdateAdminSettingsResponseObject, error) {
	admin, err := requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	app := s.App()
	st := app.Settings()
	b := req.Body
	changed := []string{}
	var newRate, newCacheGB int

	err = app.DB.InTx(ctx, func(tx *store.Tx) error {
		ts := settings.Store{Q: tx, Enc: st.Enc}
		if p := b.Instance; p != nil {
			v, err := ts.Instance(ctx)
			if err != nil {
				return err
			}
			if p.Name != nil {
				n := strings.TrimSpace(*p.Name)
				if n == "" || utf8.RuneCountInString(n) > 100 {
					return badRequest("invalid_instance_name", "the instance name must be 1–100 characters")
				}
				v.Name = n
			}
			if p.BaseUrl != nil {
				base, ok := setup.NormalizeBaseURL(*p.BaseUrl)
				if !ok {
					return badRequest("invalid_base_url", "base URL must look like https://books.example.com")
				}
				v.BaseURL = base
			}
			if !maxLen(p.PrivacyText, 50000) || !maxLen(p.TermsText, 50000) || !maxLen(p.CopyrightContact, 2000) {
				return badRequest("text_too_long", "that text is too long")
			}
			if p.SignupOpen != nil {
				v.SignupOpen = *p.SignupOpen
			}
			if p.PrivacyText != nil {
				v.PrivacyText = *p.PrivacyText
			}
			if p.TermsText != nil {
				v.TermsText = *p.TermsText
			}
			if p.CopyrightContact != nil {
				v.CopyrightContact = strings.TrimSpace(*p.CopyrightContact)
			}
			if err := ts.Set(ctx, settings.KeyInstance, v); err != nil {
				return err
			}
			changed = append(changed, "instance")
		}
		if p := b.Auth; p != nil && p.RequireTotpAll != nil {
			if err := ts.Set(ctx, settings.KeyAuth, settings.Auth{RequireTOTPAll: *p.RequireTotpAll}); err != nil {
				return err
			}
			changed = append(changed, "auth")
		}
		if p := b.Bandwidth; p != nil {
			v, err := ts.Bandwidth(ctx)
			if err != nil {
				return err
			}
			if p.EgressMbps != nil {
				if *p.EgressMbps < 1 || *p.EgressMbps > 100000 {
					return badRequest("bad_bandwidth", "the cap must be 1–100000 Mbps")
				}
				v.EgressMbps = *p.EgressMbps
			}
			if p.HlsCacheGb != nil {
				if *p.HlsCacheGb < 1 || *p.HlsCacheGb > 10000 {
					return badRequest("bad_cache_size", "the cache must be 1–10000 GB")
				}
				v.HLSCacheGB = *p.HlsCacheGb
			}
			if err := ts.Set(ctx, settings.KeyBandwidth, v); err != nil {
				return err
			}
			newRate, newCacheGB = v.EgressMbps, v.HLSCacheGB
			changed = append(changed, "bandwidth")
		}
		if p := b.Quotas; p != nil && p.DefaultQuotaBytes != nil {
			if *p.DefaultQuotaBytes < 0 {
				return badRequest("bad_quota", "the quota can't be negative")
			}
			if err := ts.Set(ctx, settings.KeyQuotas, settings.Quotas{DefaultQuotaBytes: *p.DefaultQuotaBytes}); err != nil {
				return err
			}
			changed = append(changed, "quotas")
		}
		if p := b.PermissionDefaults; p != nil {
			cur, err := ts.PermissionDefaults(ctx)
			if err != nil {
				return err
			}
			m := map[string]bool{}
			for k, v := range cur {
				m[string(k)] = v
			}
			for k, v := range overridesMap(*p) {
				m[k] = v
			}
			if err := ts.Set(ctx, settings.KeyPermissions, m); err != nil {
				return err
			}
			changed = append(changed, "permission_defaults")
		}
		if p := b.Smtp; p != nil {
			v, err := ts.SMTP(ctx)
			if err != nil {
				return err
			}
			if !maxLen(p.Host, 255) || !maxLen(p.Username, 255) || !maxLen(p.From, 255) || !maxLen(p.Password, 1024) {
				return badRequest("text_too_long", "that text is too long")
			}
			if p.Host != nil {
				v.Host = strings.TrimSpace(*p.Host)
			}
			if p.Port != nil {
				if *p.Port < 1 || *p.Port > 65535 {
					return badRequest("bad_port", "the port must be 1–65535")
				}
				v.Port = *p.Port
			}
			if p.Tls != nil {
				v.TLS = string(*p.Tls)
			}
			if p.Username != nil {
				v.Username = *p.Username
			}
			if p.Password != nil {
				v.Password = *p.Password
			}
			if p.From != nil {
				v.From = strings.TrimSpace(*p.From)
			}
			if err := ts.Set(ctx, settings.KeySMTP, v); err != nil {
				return err
			}
			changed = append(changed, "smtp")
		}
		if p := b.Ntfy; p != nil {
			v, err := ts.Ntfy(ctx)
			if err != nil {
				return err
			}
			if !maxLen(p.InternalUrl, 300) || !maxLen(p.PublicUrl, 300) || !maxLen(p.Token, 1024) {
				return badRequest("text_too_long", "that text is too long")
			}
			if p.InternalUrl != nil {
				v.InternalURL = strings.TrimSpace(*p.InternalUrl)
			}
			if p.PublicUrl != nil {
				v.PublicURL = strings.TrimSpace(*p.PublicUrl)
			}
			if p.Token != nil {
				v.Token = *p.Token
			}
			if err := ts.Set(ctx, settings.KeyNtfy, v); err != nil {
				return err
			}
			changed = append(changed, "ntfy")
		}
		if p := b.Metadata; p != nil {
			v, err := ts.Metadata(ctx)
			if err != nil {
				return err
			}
			if !maxLen(p.GoogleBooksKey, 200) {
				return badRequest("text_too_long", "that text is too long")
			}
			if p.OpenLibrary != nil {
				v.OpenLibrary = *p.OpenLibrary
			}
			if p.GoogleBooks != nil {
				v.GoogleBooks = *p.GoogleBooks
			}
			if p.Audnexus != nil {
				v.Audnexus = *p.Audnexus
			}
			if p.GoogleBooksKey != nil {
				v.GoogleAPIKey = strings.TrimSpace(*p.GoogleBooksKey)
			}
			if p.Threshold != nil {
				t := float64(*p.Threshold)
				if t < 0.5 || t > 1 {
					return badRequest("invalid_threshold", "the match threshold must be between 0.5 and 1")
				}
				v.Threshold = float64(int(t*100+0.5)) / 100
			}
			if err := ts.Set(ctx, settings.KeyMetadata, v); err != nil {
				return err
			}
			changed = append(changed, "metadata")
		}
		// Secrets are never written to the audit log, only which groups changed.
		return store.Audit(ctx, tx, store.AuditEntry{ActorID: admin, Action: "settings.update", IP: clientIP(ctx),
			Details: map[string]any{"groups": changed}})
	})
	if err != nil {
		return nil, err
	}
	if newRate > 0 {
		app.Shaper.SetRate(newRate)
		app.hlsCap = int64(newCacheGB) << 30
	}
	out, err := s.adminSettings(ctx)
	if err != nil {
		return nil, err
	}
	return UpdateAdminSettings200JSONResponse(out), nil
}

func (s *Server) TestAdminSmtp(ctx context.Context, req TestAdminSmtpRequestObject) (TestAdminSmtpResponseObject, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	cfg, err := s.App().Settings().SMTP(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled() {
		return TestAdminSmtp200JSONResponse{Ok: false, Message: strPtr("SMTP isn't set up")}, nil
	}
	if err := setup.TestSMTP(ctx, cfg, string(req.Body.To)); err != nil {
		return TestAdminSmtp200JSONResponse{Ok: false, Message: strPtr(err.Error())}, nil
	}
	return TestAdminSmtp200JSONResponse{Ok: true}, nil
}
