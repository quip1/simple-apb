# Setting up an instance with curl

Until the web wizard page ships (Phase 2), the first-run wizard is driven through its API.
Replace `https://books.example.com` with your server and use your own values.

## 1. Get the setup token

On first start the server logs a line like:

```
{"level":"WARN","msg":"SETUP REQUIRED: open the setup wizard and enter this one-time token","setup_token":"…"}
```

```bash
export SAPB=https://books.example.com/api/v1
export TOKEN=paste-the-setup-token-here
```

## 2. Optional checks

```bash
curl -s -X POST $SAPB/setup/library/test -H "X-Setup-Token: $TOKEN" -H 'Content-Type: application/json' -d '{"path":"/library"}'
curl -s -X POST $SAPB/setup/database/test -H "X-Setup-Token: $TOKEN" -H 'Content-Type: application/json' -d '{"dialect":"sqlite"}'
```

## 3. Enroll the admin's authenticator (mandatory)

```bash
curl -s -X POST $SAPB/setup/totp -H "X-Setup-Token: $TOKEN" -H 'Content-Type: application/json' -d '{"username":"admin"}'
```

Add the returned `otpauth_uri` (or `secret`) to your authenticator app.

## 4. Complete setup

```bash
curl -s -X POST $SAPB/setup/complete -H "X-Setup-Token: $TOKEN" -H 'Content-Type: application/json' -d '{
  "admin": {"username": "admin", "password": "a long unique password", "totp_secret": "SECRET-FROM-STEP-3", "totp_code": "123456"},
  "database": {"dialect": "sqlite"},
  "library_path": "/library",
  "base_url": "https://books.example.com",
  "instance_name": "My Audiobooks",
  "signup_open": true,
  "egress_mbps": 80
}'
```

Save the 10 `recovery_codes` somewhere safe — they are shown only once. The server switches to
normal mode immediately.

## 5. Sign in as the admin

```bash
curl -s -X POST $SAPB/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"…",
  "device":{"device_id":"6f1c3a52-0000-4000-8000-000000000001","device_name":"curl","platform":"other","app_version":"0"}}'
# → {"status":"totp_required","totp_challenge":"…"}
curl -s -X POST $SAPB/auth/totp/verify -H 'Content-Type: application/json' -d '{"totp_challenge":"…","code":"123456"}'
# → {"status":"ok","tokens":{"access_token":"…","refresh_token":"…"},"user":{…}}
```

Use `-H "Authorization: Bearer <access_token>"` for admin calls, e.g. approving sign-ups:

```bash
curl -s $SAPB/admin/signup-requests -H "Authorization: Bearer $ACCESS"
curl -s -X POST $SAPB/admin/signup-requests/<id>/approve -H "Authorization: Bearer $ACCESS"
```
