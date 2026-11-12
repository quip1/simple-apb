# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses semver.

## [Unreleased]

## [0.5.1] - 2026-11-12

### Fixed
- Releases: the last step couldn't collect the built files, so 0.5.0 published its server image but no GitHub release.

## [0.5.0] - 2026-11-11

**Phases 4 (Windows app) and 5 (book tools) done; next is Phase 6.** Phase 3 — Android phone app (milestones 1–7 done: foundation, onboarding and sign-in; playback; screens and account settings; offline sync and downloads; audio effects, car mode, Android Auto and the widget; local folders, push, e-ink and accessibility; release signing and F-Droid checks. Next: Jacob's week of daily use and the Android Auto checklist. Plan in ADR-0039). Phase 2 is feature-complete and awaits Jacob's sign-off; Phase 1 accepted 2026-11-08.

### Added (releases and updates, ADR-0049)
- One version for every part, set by `scripts/release.mjs X.Y.Z`. A `vX.Y.Z` tag publishes the server image to GHCR, the signed Android APK and Play bundle, and the Windows installer as a GitHub release with the changelog notes (`docs/UPDATING.md`).
- Server: Admin → Updates shows the running version and the newest release with its notes (checked daily). **Update now** backs up the database, then the optional updater container (`docker compose --profile updater up -d`) switches versions and rolls back by itself if the new one doesn't come up healthy. Without the updater, the page shows the two commands to run. CI tests an update and a rollback end to end.
- Android (GitHub build): the app offers new releases on Home and in Settings → App updates. The APK installs over the current app.
- `deploy/docker-compose.yml` now runs the release image (`SIMPLEAPB_VERSION`). Building from source moved to `deploy/docker-compose.build.yml`.

### Added (Phase 5)
- Book tools (ADR-0048), for admins and for uploaders on their own books, on a new "Book tools" page:
  - a chapter editor (rename, add, remove and move chapters; listen at a chapter) with chapters found from silences. Saved chapters survive re-scans, and m4b files get them written in (no re-encode).
  - merging a book's files into one chaptered m4b, copying the audio when possible. It replaces the files, or keeps the original as a separate book.
  - a cover picker: provider search, an uploaded image, or the cover embedded in the audio.
- Admin: a duplicate review (Admin → Duplicates). Exact (same audio) and probable (same title and author, length within 2 %) pairs are found weekly or on demand. "Keep both", or keep one: the other's listeners, progress, bookmarks and reviews move to it and it goes to the Trash.

### Fixed (Phase 5)
- Unlocking a metadata field dropped any lock that wasn't a metadata field.

### Added (Phase 4)
- Simple APB for Windows (ADR-0047): a Tauri app showing the web app, with no default server (the first screen asks for it). It signs in as a Windows device, with the tokens in Windows Credential Manager.
- Offline: whole books download to a folder you choose (resumable, checksum-verified) and play with no network. The library and book pages open from a cache, and progress made offline is sent when the server is back.
- Windows media controls (cover, title and author in the volume flyout; the keyboard's media keys), a tray icon with play/pause, skip, show, mini player and quit, close-to-tray, and an always-on-top mini player.
- Drag and drop: files or folders dropped on the window upload. A folder of mp3s goes as one zip. The web app's Uploads page takes folders too.
- An update check against GitHub releases, a per-user NSIS installer, and a CI job that lints, tests and builds the installer on every push.

### Changed (scope)
- Casting (Chromecast, DLNA/UPnP, Sonos) and the Wear OS app are dropped (ADR-0046). Phase 5 is now the book tools only.

### Added (Phase 3)
- Android app foundation (milestone 1, ADR-0039): Gradle wrapper and current AndroidX/Compose/Media3/Room/Hilt versions, modules `core-ui`, `core-data` and `app` with `play`, `github` and `fdroid` flavors; the Fern dark and e-ink themes; strings generated from the shared `i18n` catalog; the Open book adaptive launcher icon.
- Onboarding: connect to a server (address checks, version guard, warning before plain http), sign in with 2FA or a recovery code, request an account, "Forgot your password?" when the server offers email reset, and a 3-screen tour.
- Several servers and accounts with one active; tokens sealed with an Android Keystore key, refreshed once on expiry, and a sign-out notice when the server refuses the refresh; no backups of account data.
- CI builds every flavor, runs ktlint, Android lint (warnings fail) and the unit tests, and checks a minified release build.
- CI keeps the debug APKs of every flavor from each push as a downloadable artifact for 14 days.
- Release signing (milestone 7, ADR-0045): the github APK and the play bundle are signed with keys from the environment or `~/.gradle/gradle.properties`, never the repository. A `v*` tag builds them from keys in Actions secrets. The fdroid build stays unsigned, for F-Droid to sign. Setup in `docs/ANDROID-RELEASE.md`.
- F-Droid checks: every push fails if the fdroid build depends on or contains a proprietary Google library. Each night the fdroid APK is built in two checkouts and must come out byte-identical. Release builds no longer embed the git commit.
- `docs/ANDROID-AUTO-CHECKLIST.md`: the Desktop Head Unit checks for Android Auto, with setup steps.
- Playback (milestone 2, ADR-0040): new `core-player` module with one Media3 service for the app, notification, lock screen, headset and Bluetooth. Multi-file books play on one timeline, streamed with the account's sign-in. Notification and lock-screen buttons are configurable (up to 5; default back 30, play/pause, forward 30). Also: speed per book (0.5–3.5×), configurable skips, auto-rewind on resume, sleep timer (presets, custom, end of chapter) with fade-out and shake to extend, pause or lower the volume for navigation prompts, single/double/triple/long headset presses, and resuming the last book from a headset button.
- Player screen (cover, chapter and book progress, skip, chapter skip, speed, sleep timer, chapters, bookmarks with notes), mini player, the "Continue where you left off?" conflict prompt, a Playback settings screen, and covers loaded signed in. Home rows play the book.
- Progress reports every 15 s, on pause, after seeks and at the end; the position is also kept on the device so an unreachable server never loses it.
- Screens (milestone 3, ADR-0041): bottom tabs for Home, Library, Stats and Settings. Home has continue listening, recommended (with dismiss), next in series and recently added. Library has the All, Series, Authors, Narrators, Genres and Collections views, shelves, sort, instant search and grid or list, plus collections. Also author, narrator and series pages.
- Book page: details, chapters (play from any), bookmarks, ratings and reviews, who's listening, mark finished, add to collection, archive, remove or restore, recommend, report, and edit details for editors.
- Stats page: year in review, daily and weekly charts with table views, time per book, top authors, books finished.
- Settings: profile, theme, e-ink time refresh, left-handed player, streaming quality (low-data HLS), sync conflict threshold, privacy, admin email alerts. Security: password, turning 2FA on and off in the app, recovery codes, signed-in devices. Accounts: the servers and accounts on the device.
- Skip lengths, the speed for new books, the e-ink refresh and the left-handed layout now sync with the account, as on the web. Accounts that must use 2FA set it up in the app instead of being sent to the web.
- Offline (milestone 4, ADR-0042): a Room mirror of each account's library, progress, bookmarks and collections. Home, the Library and book pages work offline from it. Progress and bookmarks written offline wait in an outbox and are sent in order. A sync engine (WorkManager every 15 minutes and on reconnect, plus `/ws` live hints while the app is open) pulls `/me/sync`. A position conflict met while offline is asked about when the book is open.
- Downloads: a Downloads tab with the queue, progress and storage used. Downloads resume after interruptions and every file is checked against the server's SHA-256. Settings: storage limit, this device or the memory card, Wi-Fi only, and auto-download of the next book in a series at 90 %. Downloaded books play offline and are marked on covers, with a "Downloaded" view in the Library. A book that disappears from your library takes its download along.
- Audio effects (milestone 5, ADR-0043): volume boost up to +12 dB with a limiter, voice clarity, mono and balance, in the player's own audio pipeline, from an "Audio" panel on the player.
- Car mode: five large buttons, the chapter and the time left in it, the screen kept on. It opens from the player, or by itself when a car's Bluetooth connects (optional).
- Android Auto: browse Continue listening, Recently added, Downloaded, Collections, Series and Authors (with covers and progress); search and "play <title>" by voice; skip and chapter-skip buttons in the car.
- Folders on this device (milestone 6, ADR-0044): pick folders with the system picker. Each subfolder (or loose audio file) is a book, with its tags and cover. Books on this device play from their files and sit in the library with an "On this device" badge, view and Home shelf. "Upload to <server>" sends one with the resumable tus protocol (a folder of mp3s as one zip) and follows its import.
- "Just play files on this device" in onboarding: the app without a server, for books on the device. A server can be connected later.
- Push notifications through UnifiedPush (ntfy or another distributor), one endpoint per account: new books, uploads, and admin events. Without a distributor the app syncs live while open and every 15 minutes, and tells about new books itself. Settings → Notifications explains which is in use.
- Scan a QR code for the server address (the web app's Settings shows it under "Connect the Android app"); Home shows the server and switches between accounts.
- E-ink and accessibility pass: dialogs don't dim the page grey on e-ink, a "Refresh the screen" button clears ghosting, outlined car-mode buttons, no buffering spinner. Every screen's controls are labelled for TalkBack. At 200 % text the tabs show icons only, shelf cards widen and car-mode labels wrap between words.
- A Now playing home-screen widget (2×2, stretching to 4×2): cover, title, chapter progress, back, play/pause and forward; play resumes the last book; black on white in the e-ink theme.

### Fixed (Phase 3)
- The server answers unknown library shelf, sort and facet values with 400 `invalid_filter` (unknown shelves were a 500, unknown sorts were ignored).
- The generated Kotlin client sends enum query values as on the wire (`all`, not `ALL`).
- Android strings: ICU `{x, number}` arguments are converted (the speed label showed raw ICU text).
- Android: an e-ink screen no longer flashes the dark theme while the app starts.
- Android strings without arguments showed a literal "%%" where the text has a percent sign.
- Server: a zip of loose mp3s imported its files in name order ("Part 1, Part 10, Part 2"); they now play in natural order.
- Android: signing out left that account's book in the mini player.
- Postgres backups of an instance kept in its own schema (`search_path` in the DSN) dump that schema only. This also fixes a flaky backup test, which dumped other tests' throwaway schemas while they were being dropped.

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
- Email (milestone 7, ADR-0036): translated plain-text + HTML templates for sign-up approved/declined, password reset and admin alerts, sent in the background; "Forgot your password?" on the sign-in page when email is set up; admins can opt in to email alerts in Settings.
- Upload books from the web app (resumable, with import status), linked from the library (ADR-0037).
- Playwright end-to-end test of the brief's journey (sign-up → approval → upload → share → play → sync to a second device), run in CI (ADR-0037).
- API: `POST /auth/password-reset/request`, `Instance.email_password_reset`; `openapi.yaml` 0.12.0.
- The Fern accent colour and the Open book icon (decision #9, ADR-0038): web theme and favicon now, plus an e-ink version and Android adaptive icon layers in `branding/`.

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
