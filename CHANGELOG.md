# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses semver.

## [Unreleased]

**Current phase: 0 — Foundations.**

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
