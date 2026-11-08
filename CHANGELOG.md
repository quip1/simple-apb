# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses semver.

## [Unreleased]

**Current phase: 2 — Admin panel + web player** (milestones 1–6 done: web foundation, player screens, account and stats, admin panel, moderation and social, metadata pipeline). Phase 1 accepted 2026-11-08.

### Added (Phase 2)
- Web app foundation: Svelte 5 + Vite + TypeScript, embedded in the server binary with SPA fallback, strict CSP and caching; cookie auth with CSRF and transparent refresh; ICU i18n from the shared catalogs; dark and e-ink themes with accessible components (48 px targets, visible focus, labelled errors).
- Pages: setup wizard (all brief steps, 2FA QR enrollment, DB/library/SMTP tests, recovery codes), sign-in with 2FA or recovery code, account request, forced 2FA enrollment.
- Single-use TOTP codes (ADR-0027); emptying the Trash checks the count before spending the code.
- Docker image builds and embeds the web app; CI type-checks, format-checks and builds it.
- Player screens (milestone 2): Home (continue listening, next in series, recently added), Library (all/series/authors/narrators/genres/collections views, shelves, sort, instant search, grid/list, paged in e-ink), author/narrator/series pages, book detail (play, mark finished, collections, archive, remove/restore, chapters, bookmarks), Now playing (chapter and book progress, skip, chapter skip, speed, sleep timer, chapters, bookmarks with notes, quality), mini-player bar, Settings (theme, playback, e-ink refresh interval, left-handed layout, sign out).
- Web player engine (ADR-0029): multi-file books on one timeline, HLS data saver via hls.js, signed stream links, progress sync with the conflict prompt, Media Session (media keys, lock screen), live sync hints, screen-reader announcements, e-ink screen refresh button.
- `GET /library/facets` and `BookSummary.has_cover` (ADR-0028); `openapi.yaml` 0.7.0.
- ESLint for the web app, run in CI.

- Account pages (milestone 3): Settings (profile, appearance, playback, sync conflict threshold, privacy) and Security (change password, turn 2FA on/off, new recovery codes, signed-in devices with sign-out). Playback preferences sync through the account; theme and quality stay per device (ADR-0030).
- Stats page (milestone 3): time listened, books finished, active days, longest run of days, most listened book and author, last-30-days and weekly charts with table views, time per book, top authors, books finished (ADR-0031).
- API: `PATCH /me`, `POST /me/password`, `POST /me/totp/disable`, `POST /me/recovery-codes`, `DELETE /me/sessions`, `GET /me/stats`; `Me` gains `totp_required`, `conflict_threshold_minutes`, `listening_visible`, `prefs`; `openapi.yaml` 0.8.0.

- Admin panel (milestone 4, ADR-0032): dashboard (live streams vs the bandwidth cap, disk/memory/load, DB size, jobs, last backup/scrub, inbox counts, growth), inbox (sign-up requests), users (warn/restrict/suspend/ban/reactivate with reasons, role, quota, tags, groups, permission overrides, devices, reset link), groups, user tags, master-store library (search, filters, bulk share and Trash, library scan), book page (sharing, takedown, Trash), Trash (restore, purge, empty with confirmation), audit log with filters and CSV export, backups and integrity checks, instance settings (instance, sign-up, quotas, permission defaults, bandwidth, SMTP with test, ntfy, privacy/terms, copyright contact). Loaded on demand.
- Admin password reset links and the public reset page (ADR-0033); copyright contact in the app footer.
- API: `GET /admin/dashboard`, `GET /admin/growth`, `GET /admin/users/{id}`, `PUT /admin/users/{id}/permissions`, user tags, `GET /admin/users/{id}/sessions`, `POST /admin/users/{id}/reset-link`, `POST /auth/password-reset`, `PATCH /admin/groups/{id}`, `GET /admin/groups/{id}/members`, takedowns, `GET /admin/audit` (+ `/export`), `GET/PATCH /admin/settings`, `POST /admin/settings/smtp/test`; `openapi.yaml` 0.9.0.

- Moderation and social (milestone 5, ADR-0034): ratings and reviews on book pages (average and breakdown, your review, report, admins hide/show), recommendations (people in your groups who can already see the book; "Recommended to you" on Home), who's listening, reporting books, reviews and people, sharing your uploads with people and your groups (Public goes through the review queue), sharing collections ("Shared with you" in the library), and the inbox tabs for the review queue and reports. The admin's warning notice now shows after signing in.
- API: `GET /books/{id}/reviews`, `PUT/DELETE /books/{id}/reviews/mine`, `POST /books/{id}/recommend`, `GET/DELETE /me/recommendations`, `GET /books/{id}/listeners`, `POST /reports`, `GET /people`, `GET /me/groups`, `GET/POST/DELETE /books/{id}/shares`, `GET/POST/DELETE /me/collections/{id}/shares`, `GET /shared-collections`, `GET /admin/review-queue` (+ approve/reject), `GET /admin/reports` (+ resolve), `POST/DELETE /admin/reviews/{id}/hide`; `BookDetail.uploaded_by_me`; push events `report` and `review_request`; migration 00003; `openapi.yaml` 0.10.0.
- Metadata pipeline (milestone 6, ADR-0035): new books are looked up on Open Library, Google Books and Audnexus. Confident matches (ISBN/ASIN, or title and author above the threshold) fill only empty fields and add a missing cover; uncertain ones are flagged "needs review" in a new Metadata inbox tab. Hand edits are locked against later matches. The book page has "Edit details" for admins and for uploaders with the edit permission, with "Find a match online". Instance settings gain a metadata providers section (enable each provider, Google Books API key, match threshold).
- API: `PATCH /books/{id}`, `GET /books/{id}/metadata/candidates`, `POST /books/{id}/metadata/refresh`, `PUT /books/{id}/cover` (from a provider), `GET /admin/metadata-review`, `POST /admin/metadata-review/{id}/dismiss`; `BookDetail` gains `can_edit`, `locked_fields`, `needs_review`; `InboxCounts.metadata_review`; `AdminSettings.metadata`; `cover_url` now carries a version (`?v=`); `openapi.yaml` 0.11.0.

### Fixed (Phase 2)
- The book page now reports `has_cover` (it was always false there).
- The web client no longer signs you out when the server is briefly unreachable during a token refresh.
- The web client no longer looks signed out after 15 idle minutes: when the access cookie has expired it now refreshes the session instead of showing the sign-in page.
- A book's finished date is when it was finished on the device, not when the server heard about it (offline listening).
- API tests: database-only test books get a real folder, so the background scan can't quarantine them mid-test (an intermittent Postgres failure).

## Phase 1

### Added (Phase 1)
- Crash-safe file writes (`fsutil`: temp → fsync → rename → fsync dir), bootstrap `config.json`, `secret.key` with HKDF-derived keys and AES-GCM encryption for secret settings.
- DB access layer with `?`→`$n` rebinding, transactions, typed settings, append-only audit log; repositories for users, sessions, groups, sign-up requests, recovery codes.
- `authz` package: status → role → user overrides → user tags → instance defaults, with table-driven tests.
- Auth: argon2id passwords + common-password check, 15-min JWT access tokens, rotating refresh tokens with reuse detection, TOTP + recovery codes, per-IP/per-account rate limits, exponential login lockout, web cookies with double-submit CSRF.
- Setup wizard API (`/setup/*`): DB/library/SMTP checks, mandatory admin TOTP, single-transaction completion, in-process switch to normal mode.
- Admin API: sign-up approval/denial, users (status/role/quota/notice with required reason), groups and members, session revocation.
- Client IP from `X-Forwarded-For` only via `SIMPLEAPB_TRUSTED_PROXIES`.
- M3 library: persisted job queue (retries, crash requeue, periodic jobs); ffprobe wrapper; book detection (m4b, mp3 folders, multi-disc, multiple m4b per folder); scanner with settle time, fsnotify + hourly rescan, quarantine of missing books; resumable tus uploads with quota check, safe unzip, sha256 verify, dedup and crash-safe import into the canonical layout; visibility rules (entries ∪ distributions, minus removed/hidden/filtered/taken down); admin master store browser, distributions, Trash (restore, purge one, empty with password + TOTP + count, 30-day auto-purge); library, book detail, author/narrator/series endpoints; `openapi.yaml` 0.3.0.
- M4 streaming: range serving of original files (resumable, `?download=1` for bulk), on-demand HLS `low`/`medium` with frame-aligned segments and restart-on-seek, LRU transcode cache (hourly prune), signed 6-hour stream URLs for casting (visibility re-checked per request), resized cached covers with ETags, global egress shaper (80 Mbps default) with live minimums, class weights and per-user fair share; `openapi.yaml` 0.4.0; ADR-0017..0019.
- M5 sync: progress with the offline-window conflict rule (409 with both positions, user resolution recorded, stale writes ignored, clock clamping), listening history events, bookmarks and collections with client ids, last-write-wins and tombstones, delta sync via the change log (full snapshot, cursors, 410 after the 90-day retention), change recording across library/upload/distribution/Trash/scan/group writes, `/ws` change hints; every contract operation now implemented; `openapi.yaml` 0.5.0; ADR-0020..0021.
- M6 operations: lock-file unclean-shutdown detection with a startup check (DB integrity, upload recovery, 24 h checksum pass, admin push); nightly DB backups (VACUUM INTO / pg_dump, keep 14); weekly integrity scrub with quarantine; push via ntfy topics and UnifiedPush endpoints with SSRF protection; v1 events (upload done/failed, library added, sign-up request, backup/integrity failed, unclean shutdown); admin backup/integrity endpoints; Docker image with ffmpeg + pg_dump 17; compose with ntfy, optional Postgres, healthchecks; SIGKILL crash harness (nightly CI, 40 kills); `openapi.yaml` 0.6.0; ADR-0022..0025.

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
