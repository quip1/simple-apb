// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/quip1/simple-apb/server/internal/auth"
	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
	"github.com/quip1/simple-apb/server/internal/version"
)

func mustUUID(s string) uuid.UUID { return uuid.MustParse(s) }

func (s *Server) Signup(ctx context.Context, req SignupRequestObject) (SignupResponseObject, error) {
	b := req.Body
	in := auth.SignupInput{Username: b.Username, Password: b.Password, Message: deref(b.Message),
		AgeConfirmed: b.AgeConfirmed, TermsAccepted: b.TermsAccepted, IP: clientIP(ctx)}
	if b.Email != nil {
		in.Email = string(*b.Email)
	}
	if err := s.App().Auth.Signup(ctx, in); err != nil {
		return nil, err
	}
	return Signup202JSONResponse{Status: SignupAcceptedStatusPending}, nil
}

func deviceFrom(d DeviceInfo) auth.Device {
	return auth.Device{ID: d.DeviceId.String(), Name: d.DeviceName, Platform: string(d.Platform), AppVersion: d.AppVersion}
}

func (s *Server) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	res, err := s.App().Auth.Login(ctx, req.Body.Username, req.Body.Password, deviceFrom(req.Body.Device), clientIP(ctx))
	if err != nil {
		return nil, err
	}
	return s.loginResponse(ctx, res, req.Body.Device.Platform == PlatformWeb)
}

func (s *Server) VerifyTotp(ctx context.Context, req VerifyTotpRequestObject) (VerifyTotpResponseObject, error) {
	res, err := s.App().Auth.VerifyTOTP(ctx, req.Body.TotpChallenge, deref(req.Body.Code), deref(req.Body.RecoveryCode), clientIP(ctx))
	if err != nil {
		return nil, err
	}
	web := res.Tokens != nil && isWebSession(ctx, s.App(), res.Tokens.SessionID)
	return s.loginResponse(ctx, res, web)
}

func isWebSession(ctx context.Context, app *App, sessionID string) bool {
	sess, err := store.GetSession(ctx, app.DB, sessionID)
	return err == nil && sess.Platform == "web"
}

// loginResponse builds the body and, for web clients, the auth cookies.
func (s *Server) loginResponse(ctx context.Context, res *auth.LoginResult, web bool) (*jsonWithCookies, error) {
	if res.Tokens == nil {
		return &jsonWithCookies{status: http.StatusOK, body: LoginResponse{Status: LoginResponseStatusTotpRequired, TotpChallenge: &res.Challenge}}, nil
	}
	app := s.App()
	p, err := app.Auth.Authenticate(ctx, res.Tokens.Access)
	if err != nil {
		return nil, err
	}
	me, err := s.meFor(ctx, p)
	if err != nil {
		return nil, err
	}
	if res.User.Notice != "" {
		_ = store.ClearNotice(ctx, app.DB, res.User.ID) // shown once
	}
	out := &jsonWithCookies{status: http.StatusOK}
	pair := tokenPair(res.Tokens)
	if web {
		out.cookies = authCookies(ctx, res.Tokens)
		pair.RefreshToken = ""
	}
	out.body = LoginResponse{Status: LoginResponseStatusOk, Tokens: &pair, User: me}
	return out, nil
}

func tokenPair(t *auth.TokenSet) TokenPair {
	return TokenPair{AccessToken: t.Access, AccessExpiresAt: t.AccessExp.UTC(), RefreshToken: t.Refresh, RefreshExpiresAt: t.RefreshExp.UTC()}
}

func secureCookies(ctx context.Context) bool {
	r := requestFrom(ctx)
	return r != nil && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https")
}

func authCookies(ctx context.Context, t *auth.TokenSet) []*http.Cookie {
	secure := secureCookies(ctx)
	// Keep the CSRF value stable across refreshes so other open tabs keep working.
	csrf := ""
	if r := requestFrom(ctx); r != nil {
		if c, err := r.Cookie(cookieCSRF); err == nil && len(c.Value) >= 32 {
			csrf = c.Value
		}
	}
	if csrf == "" {
		csrf, _, _ = auth.NewOpaqueToken()
	}
	base := "/api/" + version.APIVersion
	return []*http.Cookie{
		{Name: cookieSession, Value: t.Access, Path: "/", Expires: t.AccessExp, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode},
		{Name: cookieRefresh, Value: t.Refresh, Path: base + "/auth", Expires: t.RefreshExp, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode},
		{Name: cookieCSRF, Value: csrf, Path: "/", Expires: t.RefreshExp, Secure: secure, SameSite: http.SameSiteLaxMode},
	}
}

func clearAuthCookies(ctx context.Context) []*http.Cookie {
	secure := secureCookies(ctx)
	base := "/api/" + version.APIVersion
	gone := time.Unix(0, 0)
	return []*http.Cookie{
		{Name: cookieSession, Value: "", Path: "/", Expires: gone, MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode},
		{Name: cookieRefresh, Value: "", Path: base + "/auth", Expires: gone, MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode},
		{Name: cookieCSRF, Value: "", Path: "/", Expires: gone, MaxAge: -1, Secure: secure, SameSite: http.SameSiteLaxMode},
	}
}

// jsonWithCookies is a response that also sets cookies. It satisfies the
// generated response interfaces of the auth operations.
type jsonWithCookies struct {
	status  int
	body    any
	cookies []*http.Cookie
}

func (j *jsonWithCookies) visit(w http.ResponseWriter) error {
	for _, c := range j.cookies {
		http.SetCookie(w, c)
	}
	w.Header().Set("Cache-Control", "no-store")
	if j.body == nil {
		w.WriteHeader(j.status)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(j.status)
	return json.NewEncoder(w).Encode(j.body)
}

func (j *jsonWithCookies) VisitLoginResponse(w http.ResponseWriter) error         { return j.visit(w) }
func (j *jsonWithCookies) VisitVerifyTotpResponse(w http.ResponseWriter) error    { return j.visit(w) }
func (j *jsonWithCookies) VisitRefreshTokensResponse(w http.ResponseWriter) error { return j.visit(w) }
func (j *jsonWithCookies) VisitLogoutResponse(w http.ResponseWriter) error        { return j.visit(w) }

func (s *Server) RefreshTokens(ctx context.Context, req RefreshTokensRequestObject) (RefreshTokensResponseObject, error) {
	token, fromCookie := "", false
	if req.Body != nil && req.Body.RefreshToken != "" {
		token = req.Body.RefreshToken
	} else if r := requestFrom(ctx); r != nil {
		if c, err := r.Cookie(cookieRefresh); err == nil {
			token, fromCookie = c.Value, true
			if !csrfOK(r) {
				return nil, apiErr(http.StatusForbidden, "csrf_failed", "missing or wrong CSRF token")
			}
		}
	}
	t, err := s.App().Auth.Refresh(ctx, token, clientIP(ctx))
	if err != nil {
		return nil, err
	}
	out := &jsonWithCookies{status: http.StatusOK}
	pair := tokenPair(t)
	if fromCookie {
		out.cookies = authCookies(ctx, t)
		pair.RefreshToken = ""
	}
	out.body = pair
	return out, nil
}

func (s *Server) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := s.App().Auth.Logout(ctx, p); err != nil {
		return nil, err
	}
	return &jsonWithCookies{status: http.StatusNoContent, cookies: clearAuthCookies(ctx)}, nil
}

func (s *Server) EnrollTotp(ctx context.Context, _ EnrollTotpRequestObject) (EnrollTotpResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	inst, err := s.App().Settings().Instance(ctx)
	if err != nil {
		return nil, err
	}
	secret, uri, err := s.App().Auth.EnrollTOTP(ctx, p, inst.Name)
	if err != nil {
		return nil, err
	}
	return EnrollTotp200JSONResponse{Secret: secret, OtpauthUri: uri}, nil
}

func (s *Server) ConfirmTotp(ctx context.Context, req ConfirmTotpRequestObject) (ConfirmTotpResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	codes, err := s.App().Auth.ConfirmTOTP(ctx, p, req.Body.Code, clientIP(ctx))
	if err != nil {
		return nil, err
	}
	return ConfirmTotp200JSONResponse{Codes: codes}, nil
}

// ── Me ──

func (s *Server) meFor(ctx context.Context, p *auth.Principal) (*Me, error) {
	used, err := store.UsedBytes(ctx, s.App().DB, p.User.ID)
	if err != nil {
		return nil, err
	}
	u := p.User
	eff := authz.Effective(p.Subject)
	me := &Me{
		Id: mustUUID(u.ID), Username: u.Username, DisplayName: u.DisplayName,
		Role: UserRole(u.Role), Status: UserStatus(u.Status), TotpEnabled: u.TOTPSecretEnc != "", TotpRequired: p.TOTPRequired,
		ConflictThresholdMinutes: int(u.ConflictThresholdMS / 60_000), ListeningVisible: u.ListeningVisible, Prefs: prefsOf(u.Prefs),
		QuotaBytes: u.QuotaBytes, UsedBytes: used, Locale: u.Locale, Notice: strPtr(u.Notice),
		Permissions: EffectivePermissions{
			CanUpload: eff[authz.Upload], CanEditMetadata: eff[authz.EditMetadata], CanDeleteOwn: eff[authz.DeleteOwn],
			CanShare: eff[authz.Share], CanReview: eff[authz.Review], CanPostPublic: eff[authz.PostPublic],
		},
	}
	if u.Email != "" {
		e := openapi_types.Email(u.Email)
		me.Email = &e
	}
	return me, nil
}

func (s *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	me, err := s.meFor(ctx, p)
	if err != nil {
		return nil, err
	}
	return GetMe200JSONResponse(*me), nil
}

func sessionOut(sess *store.Session, current string) Session {
	return Session{
		Id: mustUUID(sess.ID), DeviceId: uuidOrNil(sess.DeviceID), DeviceName: sess.DeviceName,
		Platform: Platform(sess.Platform), AppVersion: sess.AppVersion,
		LastSeenAt: msTime(sess.LastSeenAt), CreatedAt: msTime(sess.CreatedAt), Current: sess.ID == current,
	}
}

func uuidOrNil(s string) uuid.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return u
}

func (s *Server) ListMySessions(ctx context.Context, _ ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	list, err := store.ListUserSessions(ctx, s.App().DB, p.User.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListMySessions200JSONResponse, 0, len(list))
	for _, sess := range list {
		out = append(out, sessionOut(sess, p.SessionID))
	}
	return out, nil
}

func (s *Server) RevokeMySession(ctx context.Context, req RevokeMySessionRequestObject) (RevokeMySessionResponseObject, error) {
	p, err := requireUser(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := store.RevokeSession(ctx, s.App().DB, req.SessionId.String(), p.User.ID); err != nil {
		return nil, err
	}
	_ = store.Audit(ctx, s.App().DB, store.AuditEntry{ActorID: p.User.ID, Action: "session.revoke", ObjectType: "session",
		ObjectID: req.SessionId.String(), IP: clientIP(ctx)})
	return RevokeMySession204Response{}, nil
}

// prefsOf decodes the stored preferences object (empty on anything odd).
func prefsOf(raw string) map[string]any {
	m := map[string]any{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}
