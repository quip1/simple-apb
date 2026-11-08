# Architecture (as built)

> Updated as each phase lands. Current state: **Phase 1 accepted; Phase 2 milestones 1–6** (web foundation, player screens, account and stats, admin panel, moderation and social, metadata pipeline).

## Overview

```
 Android / Wear / Web / Windows clients
        │  HTTPS  (REST /api/v1, WebSocket /ws, signed stream URLs)
        ▼
 reverse proxy (TLS)
        ▼
 simpleapb-server (one Go process) ──► ntfy (push, UnifiedPush)
   API · sync · streaming · scanner · jobs · ffmpeg
        │                 │
   SQLite or Postgres   /library  /cache  /config  /backups
```

- **One server process** owns all state. Background work runs as jobs in a DB-persisted queue
  (`jobs` table) inside the same process.
- **`server/openapi.yaml` is the contract.** Go handler interfaces, the TypeScript client and the
  Kotlin client are all generated from it (`docs/DECISIONS.md` ADR-0007).

## What exists today

| Piece | Location | State |
|---|---|---|
| API contract v0 | `server/openapi.yaml` | instance, auth, me/sessions, library, progress + delta sync, media |
| API reference | `docs/API.md` | generated from the spec |
| DB schema | `server/internal/store/migrations/{sqlite,postgres}/00001_init.sql` | full v1 logical model, both dialects |
| Migrations runner | `server/internal/store` | goose, embedded; tests for both dialects + parity |
| Server modes | `server/cmd/simpleapb`, `internal/setup` | **setup mode** (only health, instance, `/setup/*`) until the wizard finishes, then **normal mode** in-process (ADR-0012) |
| Accounts & auth | `internal/auth`, `internal/api/handlers_auth.go` | sign-up → admin approval, login (+TOTP), rotating refresh, sessions, rate limits, lockout, web cookies + CSRF (ADR-0013) |
| Authorization | `internal/authz` | single decision point, table-tested |
| Admin API | `internal/api/handlers_admin.go` | sign-up inbox, users, groups/members, session revocation |
| Library | `internal/library`, `internal/api/handlers_library.go`, `handlers_master.go` | detection + import, scanner, tus uploads, dedup, distribution, Trash (ADR-0015) |
| Jobs | `internal/jobs` | persisted queue, retries, periodic jobs (ADR-0016) |
| Streaming | `internal/stream`, `internal/api/handlers_media.go` | range files, HLS, signed URLs, covers, bandwidth shaper (ADR-0017..0019) |
| Sync | `internal/sync`, `internal/api/handlers_sync.go`, `ws.go` | progress conflicts, bookmarks, collections, delta sync, `/ws` hints (ADR-0020..0021) |
| Operations | `internal/backup`, `internal/push`, `internal/library/scrub.go`, `internal/api/ops.go` | startup check, backups, scrubs, push (ADR-0022..0024) |
| Crash harness | `cmd/simpleapb/crash_test.go` | SIGKILL durability test (ADR-0025) |
| Deployment | `server/Dockerfile`, `deploy/docker-compose.yml` | image with ffmpeg + pg_dump 17; compose with ntfy, optional Postgres |
| Web app | `web/src`, embedded via `server/internal/web` | Svelte 5 SPA: setup wizard, sign-in, sign-up, 2FA; Home, Library, book/author/series pages, Now playing, Stats, Settings, Security; dark + e-ink themes; i18n (ADR-0026) |
| Admin panel | `web/src/pages/admin` (lazy-loaded), `server/internal/api/handlers_panel.go`, `handlers_settings.go`, `internal/sysinfo` | dashboard, users/groups/tags, master store, Trash, takedowns, audit log + CSV, backups, settings (ADR-0032) |
| Social & moderation | `internal/authz/social.go`, `internal/store/social.go`, `internal/api/handlers_social.go`, `web/src/pages/admin/inbox` | reviews and ratings, recommendations, who's listening, reports, sharing your uploads (Public via the review queue), collection sharing, inbox tabs (ADR-0034) |
| Metadata | `internal/metadata`, `internal/store/metadata.go`, `internal/api/handlers_metadata.go`, `web/src/components/MetadataDialog.svelte` | Open Library, Google Books and Audnexus providers, confidence scoring, fill-only matching job after import, locked hand edits, needs-review inbox tab, provider covers (ADR-0035) |
| Web player | `web/src/lib/player.svelte.ts`, `live.svelte.ts` | one `<audio>` on the book timeline (multi-file or HLS via hls.js), signed stream links, progress sync + conflict prompt, Media Session, `/ws` hints (ADR-0029) |
| TS client | `web/src/api` | generated types + cookie/CSRF/refresh-aware client |
| Kotlin client | `android/core-api` | generated at build time |
| CI | `.github/workflows/ci.yml` | spec lint + codegen freshness, Go tests on SQLite and Postgres, golangci-lint, Kotlin client build, Docker image build |

## Request path

`RequestID → Recoverer → X-Server-Version → client IP (trusted proxies only) → setup gate → authenticate (Bearer or cookie; CSRF on cookie writes) → generated strict handler → handler asks authz → store`.
Errors are typed (`httpError`, `auth.Error`, `setup.Error`, `store.ErrNotFound`…) and mapped to the `Error` schema in one place (`internal/api/errors.go`); unknown errors become a logged 500 with no detail leaked.

## Server packages

| Package | Responsibility |
|---|---|
| `api` | HTTP handlers implementing the generated strict interface |
| `auth` | sessions, argon2id, TOTP, OIDC, rate limits |
| `authz` | **the only place** permission decisions are made |
| `library` | master store, user libraries, distribution |
| `scanner` | folder watch + import |
| `media` | ffprobe/ffmpeg, chapters, transcoding, HLS |
| `metadata` | providers (Open Library, Google Books, Audnexus), scoring, the matching job, provider covers |
| `sync` | progress, bookmarks, conflicts, delta sync |
| `stream` | range serving, bandwidth shaping |
| `push`, `mail` | ntfy publisher, SMTP |
| `moderation` | reserved; reports, the review queue and takedowns currently live in `store` (`social.go`, `admin.go`) and `api` (`handlers_social.go`, `handlers_panel.go`) |
| `stats`, `backup`, `jobs` | stats, backups/scrubs, job queue |
| `sysinfo` | disk space, memory and load for the admin dashboard |
| `store` | DB connections, migrations, `?`→`$n` rebinding, transactions, repositories, settings, audit log |
| `fsutil` | crash-safe writes (temp → fsync → rename → fsync dir) |
| `config` | `config.json` bootstrap, `secret.key`, HKDF-derived keys, AES-GCM for secret settings |
| `settings` | typed instance settings with defaults |
| `setup` | first-run wizard |
| `version` | build version (ldflags) |
| `web` | embedded SPA build (Phase 2) |

## Data model

See the migration files — every table is commented. Key relationships:

- **Master store:** `books` → `book_files` (ordered, each with `offset_ms` on the global
  timeline) and `chapters` (global timeline). Authors/narrators/series are normalized.
- **Visibility:** a user sees `user_library_entries` (own uploads, recommendations) ∪
  `distributions` to them or their `groups`, minus entries with `removed_at`/`hidden_at`, minus
  books carrying a tag in their `content_filters`, minus active `takedowns`, minus non-`ready`
  books.
- **Listening:** one `progress` row per (user, book); `progress_events` is append-only history;
  `progress_conflicts` records 409s and the user's choice.
- **Sync:** `sync_changes.seq` is the delta-sync cursor (ADR-0005).
- **Operations:** `jobs`, `audit_log` (append-only), moderation tables, `settings` (secret values
  encrypted with `/config/secret.key`).

## Conventions

IDs UUIDv7; timestamps integer unix ms in the DB and RFC 3339 in the API; positions/durations in
ms. See ADR-0004.
