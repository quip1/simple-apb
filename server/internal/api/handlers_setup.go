// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"

	"github.com/quip1/simple-apb/server/internal/config"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/setup"
	"github.com/quip1/simple-apb/server/internal/version"
)

// ── Instance ──

func (s *Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: HealthStatusOk, Version: version.Version}, nil
}

func (s *Server) GetInstance(ctx context.Context, _ GetInstanceRequestObject) (GetInstanceResponseObject, error) {
	// Placeholder guard until per-platform minimums are configurable (Phase 6).
	v := VersionPair{Required: "0.0.0", Recommended: "0.0.0"}
	out := GetInstance200JSONResponse{
		Name:             settings.DefaultInstance().Name,
		Version:          version.Version,
		ApiVersion:       version.APIVersion,
		SetupRequired:    true,
		MinClientVersion: ClientVersionGuard{Android: &v, Wear: &v, Web: &v, Windows: &v},
		OidcProviders:    []OidcProvider{},
	}
	if app := s.App(); app != nil {
		inst, err := app.Settings().Instance(ctx)
		if err != nil {
			return nil, err
		}
		out.Name, out.SignupOpen, out.SetupRequired = inst.Name, inst.SignupOpen, false
		out.PrivacyText, out.TermsText = strPtr(inst.PrivacyText), strPtr(inst.TermsText)
		out.CopyrightContact = strPtr(inst.CopyrightContact)
	}
	return out, nil
}

// ── Setup wizard ──

func (s *Server) requireSetupToken(ctx context.Context) error {
	if s.App() != nil {
		return errSetupDone
	}
	r := requestFrom(ctx)
	if r == nil || s.Setup == nil || !s.Setup.CheckToken(r.Header.Get("X-Setup-Token")) {
		return errBadSetupToken
	}
	return nil
}

func (s *Server) GetSetupStatus(context.Context, GetSetupStatusRequestObject) (GetSetupStatusResponseObject, error) {
	configured := false
	if s.Setup != nil {
		_, err := config.LoadBootstrap(s.Setup.Paths.Config)
		configured = err == nil
	}
	return GetSetupStatus200JSONResponse{SetupRequired: s.App() == nil, DatabaseConfigured: &configured}, nil
}

func checkResult(err error) SetupCheckResult {
	if err != nil {
		msg := err.Error()
		return SetupCheckResult{Ok: false, Message: &msg}
	}
	return SetupCheckResult{Ok: true}
}

func (s *Server) TestSetupDatabase(ctx context.Context, req TestSetupDatabaseRequestObject) (TestSetupDatabaseResponseObject, error) {
	if err := s.requireSetupToken(ctx); err != nil {
		return nil, err
	}
	err := s.Setup.TestDatabase(ctx, setup.Database{Dialect: string(req.Body.Dialect), DSN: deref(req.Body.Dsn)})
	return TestSetupDatabase200JSONResponse(checkResult(err)), nil
}

func (s *Server) TestSetupLibrary(ctx context.Context, req TestSetupLibraryRequestObject) (TestSetupLibraryResponseObject, error) {
	if err := s.requireSetupToken(ctx); err != nil {
		return nil, err
	}
	c := setup.TestLibrary(req.Body.Path)
	return TestSetupLibrary200JSONResponse{Exists: c.Exists, Writable: c.Writable, Empty: c.Empty, Message: strPtr(c.Message)}, nil
}

func smtpFromAPI(in *SmtpSettings) *settings.SMTP {
	if in == nil {
		return nil
	}
	return &settings.SMTP{Host: in.Host, Port: in.Port, TLS: string(in.Tls), Username: deref(in.Username),
		Password: deref(in.Password), From: string(in.From)}
}

func (s *Server) TestSetupSmtp(ctx context.Context, req TestSetupSmtpRequestObject) (TestSetupSmtpResponseObject, error) {
	if err := s.requireSetupToken(ctx); err != nil {
		return nil, err
	}
	err := setup.TestSMTP(ctx, *smtpFromAPI(&req.Body.Smtp), string(req.Body.To))
	return TestSetupSmtp200JSONResponse(checkResult(err)), nil
}

func (s *Server) CreateSetupTotp(ctx context.Context, req CreateSetupTotpRequestObject) (CreateSetupTotpResponseObject, error) {
	if err := s.requireSetupToken(ctx); err != nil {
		return nil, err
	}
	secret, uri, err := setup.NewAdminTOTP("Simple APB", req.Body.Username)
	if err != nil {
		return nil, err
	}
	return CreateSetupTotp200JSONResponse{Secret: secret, OtpauthUri: uri}, nil
}

func (s *Server) CompleteSetup(ctx context.Context, req CompleteSetupRequestObject) (CompleteSetupResponseObject, error) {
	if err := s.requireSetupToken(ctx); err != nil {
		return nil, err
	}
	if !s.completing.CompareAndSwap(false, true) {
		return nil, errSetupInProcess
	}
	defer s.completing.Store(false)

	b := req.Body
	in := setup.Request{
		AdminUsername: b.Admin.Username, AdminPassword: b.Admin.Password,
		AdminDisplayName: deref(b.Admin.DisplayName), TOTPSecret: b.Admin.TotpSecret, TOTPCode: b.Admin.TotpCode,
		Database:    setup.Database{Dialect: string(b.Database.Dialect), DSN: deref(b.Database.Dsn)},
		LibraryPath: b.LibraryPath, BaseURL: b.BaseUrl, SMTP: smtpFromAPI(b.Smtp),
		Instance: settings.Instance{Name: b.InstanceName, SignupOpen: b.SignupOpen == nil || *b.SignupOpen,
			PrivacyText: deref(b.PrivacyText), TermsText: deref(b.TermsText)},
		IP: clientIP(ctx),
	}
	if b.Admin.Email != nil {
		in.AdminEmail = string(*b.Admin.Email)
	}
	if b.Ntfy != nil {
		in.Ntfy = settings.Ntfy{InternalURL: deref(b.Ntfy.InternalUrl), PublicURL: deref(b.Ntfy.PublicUrl), Token: deref(b.Ntfy.Token)}
	}
	if b.EgressMbps != nil {
		in.EgressMbps = *b.EgressMbps
	}
	res, err := s.Setup.Complete(ctx, in)
	if err != nil {
		return nil, err
	}
	app, err := NewApp(ctx, res.DB, s.Secrets, s.log(), s.AppOptions)
	if err != nil {
		return nil, err
	}
	base := s.BaseCtx
	if base == nil {
		base = context.Background()
	}
	app.Start(base)
	s.SetApp(app)
	s.log().InfoContext(ctx, "setup complete; server is now in normal mode", "db", string(res.DB.Dialect()))
	return CompleteSetup200JSONResponse{AdminId: mustUUID(res.AdminID), RecoveryCodes: res.RecoveryCodes}, nil
}
