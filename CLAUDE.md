# CLAUDE.md

Simple APB — self-hostable audiobook platform. Owner: Jacob.

## The brief

The full build brief is **not committed** (it contains personal deployment
details). The local copy is `.local/BRIEF.md` (gitignored). Read it before
starting a phase. Scope is locked: anything not in its §2.1 is out of scope —
log questions in `docs/DECISIONS.md` instead of building them.

## Rules

- Build phase by phase (brief §16). Current phase is in `CHANGELOG.md`.
- **API first:** edit `server/openapi.yaml`, run codegen, then implement.
  - Go: `cd server && go generate ./...`
  - TS: `cd web && npm run gen:api`
  - Kotlin: generated at build time by `android/core-api`
  - Docs: `cd web && npm run gen:docs` (writes `docs/API.md`)
- **Web app:** `cd web && npm run dev` (proxies `/api` and `/ws` to a server on :8080);
  `npm run check` (svelte-check, no warnings), `npm run format`; `npm run build:embed` copies the
  build into the server so `go build` embeds it.
- **Schema:** migrations live in `server/internal/store/migrations/{sqlite,postgres}`
  and must stay in lockstep (same file names, same tables/columns). The parity
  test enforces it when Postgres is available.
- Keep `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`, `docs/API.md`, `CHANGELOG.md` current.
  Explain non-obvious choices in `DECISIONS.md` in a short paragraph.
- Public repo (AGPL-3.0): never commit secrets, tokens, personal hostnames,
  or Jacob's LAN details. No instance hostname is ever a default.
- Authorization logic lives only in `server/internal/authz`. Never in handlers.
- Every user-facing string goes through i18n (`i18n/`).
- Every file write in the server: temp file → fsync → rename → fsync dir.

## Conventions

- Go: gofmt, golangci-lint, wrapped errors, no panics in handlers, `slog` without PII.
- TS: strict, eslint + prettier. Kotlin: ktlint/detekt, ViewModel + StateFlow.
- Conventional Commits. New source files start with the SPDX header
  `SPDX-License-Identifier: AGPL-3.0-or-later`.
