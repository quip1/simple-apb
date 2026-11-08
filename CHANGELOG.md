# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses semver.

## [Unreleased]

**Current phase: 1 — Server core** (milestones M1–M2 done: foundations, setup wizard, accounts, auth, authz).

### Added (Phase 1)
- Crash-safe file writes (`fsutil`: temp → fsync → rename → fsync dir), bootstrap `config.json`, `secret.key` with HKDF-derived keys and AES-GCM encryption for secret settings.
- DB access layer with `?`→`$n` rebinding, transactions, typed settings, append-only audit log; repositories for users, sessions, groups, sign-up requests, recovery codes.
- `authz` package: status → role → user overrides → user tags → instance defaults, with table-driven tests.
- Auth: argon2id passwords + common-password check, 15-min JWT access tokens, rotating refresh tokens with reuse detection, TOTP + recovery codes, per-IP/per-account rate limits, exponential login lockout, web cookies with double-submit CSRF.
- Setup wizard API (`/setup/*`): DB/library/SMTP checks, mandatory admin TOTP, single-transaction completion, in-process switch to normal mode.
- Admin API: sign-up approval/denial, users (status/role/quota/notice with required reason), groups and members, session revocation.
- Client IP from `X-Forwarded-For` only via `SIMPLEAPB_TRUSTED_PROXIES`.

## Phase 0

### Fixed
- CI: Kotlin client build (JDK toolchain, `Any` serialization, duplicate generated imports) and golangci-lint findings.

### Added
- Monorepo layout (`server/`, `web/`, `desktop/`, `android/`, `deploy/`, `docs/`, `i18n/`).
- AGPL-3.0 license with the Google Play Services / Cast SDK linking exception (`LICENSE-EXCEPTION.md`).
- `server/openapi.yaml` v0: instance, auth (sign-up, login, TOTP, refresh, logout), me/sessions,
  library, progress with conflict (409) semantics, delta sync, media (range files, HLS, signed
  stream URLs, covers).
- Database schema for the full v1 data model in SQLite and PostgreSQL, with goose migrations,
  up/down tests and a cross-dialect parity test.
- Code generation: Go (oapi-codegen strict server), TypeScript (openapi-typescript +
  openapi-fetch), Kotlin (openapi-generator, build time); generated `docs/API.md`.
- Go server skeleton serving `/api/v1/health` and `/api/v1/instance`.
- CI: spec lint, generated-code freshness, Go tests on SQLite + Postgres, golangci-lint,
  Kotlin client build, Docker image build.
- Docs: `ARCHITECTURE.md`, `DECISIONS.md` (ADRs 0001–0009), `API.md`, stubs for self-hosting and privacy.
