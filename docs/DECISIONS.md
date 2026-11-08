# Decisions log

ADR-style: what was decided, the alternatives, and why. Newest at the bottom.
Open questions for Jacob are at the end.

---

## ADR-0001 — Monorepo, and a pure-JVM `core-api` Android module

**Decision.** One repository with `server/`, `web/`, `desktop/`, `android/`, `deploy/`,
`docs/`, `i18n/` as in the brief. On Android, the OpenAPI-generated Kotlin client lives in
its own **pure-JVM** module `android/core-api`, which `core-data` will depend on.

**Alternatives.** Put the generated client directly inside `core-data` (the brief's layout).

**Why.** The generated client has no Android dependencies. As a JVM module it compiles in
seconds on any machine with a JDK — including CI and this dev box, which has no Android SDK —
so "the generated clients compile" can be checked from Phase 0. `core-data` (Room, sync
engine) still owns everything Android-specific.

## ADR-0002 — The build brief is not committed

The brief contains personal deployment details (hostnames, home network). It lives at
`.local/BRIEF.md`, which is gitignored. `CLAUDE.md` points to it. Anything from the brief that
belongs in the public repo is restated in these docs in generic form.

## ADR-0003 — Pure-Go SQLite driver (`modernc.org/sqlite`)

**Decision.** Use `modernc.org/sqlite` (driver `sqlite`) and `pgx` (driver `pgx`) behind
`database/sql`.

**Alternatives.** `mattn/go-sqlite3` (cgo).

**Why.** No cgo means a truly static binary, trivial cross-compilation for the amd64+arm64
image, and no C toolchain for self-hosters building from source. modernc is slower than cgo
SQLite in micro-benchmarks, but our load (a few writes per second per active listener) is far
below where that matters.

## ADR-0004 — Schema conventions shared by both dialects

**Decision.**

| Concept | SQLite | PostgreSQL |
|---|---|---|
| IDs | `TEXT` holding a UUIDv7 | `UUID` |
| Timestamps | `INTEGER` unix **milliseconds** UTC | `BIGINT` unix ms |
| Booleans | `INTEGER` 0/1 + `CHECK` | `BOOLEAN` |
| JSON | `TEXT` | `JSONB` |
| Lists (authors, narrators…) | join tables | join tables |
| Type safety | `STRICT` tables | native |

Migrations are hand-written per dialect with **the same file names, tables and columns**.
`TestMigrationFilesMatch` always runs; `TestSchemaParity` compares every table's columns when
Postgres is available (CI runs it).

**Alternatives.** Native `timestamptz` in Postgres and `TEXT` ISO dates in SQLite; arrays for
authors; one generated migration set.

**Why.** Integer milliseconds compare, sort and subtract identically in both engines, which
keeps the query layer dialect-agnostic and matches the API's integer `position_ms` world (sync
conflicts are about time arithmetic). The API still speaks RFC 3339. Join tables give us
author/narrator pages and indexes for free. `STRICT` makes SQLite reject wrongly typed values
instead of silently storing them — important when the same Go code must behave the same on
Postgres.

**Small deviations from the brief's logical model** (all additive):
- `invites_or_requests` is named `signup_requests` and links to the pending user row.
- `listening_visibility` is a column `users.listening_visible` instead of its own table (it is
  one boolean per user).
- `users.conflict_threshold_ms` (1–30 min, default 3) holds the per-user sync conflict setting.
- Added tables the brief's features need: `recovery_codes` (TOTP), `api_tokens` (§6.19),
  `uploads` (tus tracking), `progress_conflicts` (409s and the user's final choice are logged),
  `duplicate_candidates` (duplicate finder review list), `sync_changes` (delta sync, ADR-0005),
  `book_authors`/`book_narrators` (join tables).
- `books.locked_fields` records hand-edited metadata so providers never overwrite it;
  `books.metadata_review` is the "needs review" flag.
- The `books` table is kept free of audio-only concepts (those live in `book_files`) so it can
  later become a shared "media item" table for the podcasts app.

## ADR-0005 — Delta sync uses a change log with a sequence cursor

**Decision.** Every change relevant to sync appends a row to `sync_changes(seq, user_id,
entity, entity_id, op, at)`. `GET /me/sync?since=<cursor>` reads rows with `seq > cursor` for
that user (plus `user_id IS NULL` rows for objects they can see), returns current state for
upserts and tombstones for deletes, and the new cursor is the highest `seq` returned. The
cursor is opaque to clients. If old rows are pruned the server answers `410` and the client
does a full sync.

**Alternatives.** `updated_at > since` on every table.

**Why.** Timestamps from different writers can tie or arrive out of order, so `updated_at`
cursors silently skip changes. A single monotonic sequence never does, gives tombstones a
natural home, and makes "what changed for user X" one indexed range scan.

## ADR-0006 — SQLite durability: WAL + `synchronous=NORMAL`

As the brief specifies. In WAL mode, `NORMAL` never corrupts the database on power loss; at
worst the last few committed transactions roll back. `FULL` would fsync on every commit and
cost a lot of write latency for a guarantee the progress-sync design already tolerates (clients
keep an outbox and resend). Foreign keys are switched on per connection and `_txlock=immediate`
avoids deadlock-prone lock upgrades.

## ADR-0007 — Code generation and what gets committed

| Target | Tool | Committed? |
|---|---|---|
| Go server | `oapi-codegen` v2, chi + **strict server**, run with `go tool` (pinned in `go.mod`) | yes, `api.gen.go`; CI fails if stale |
| TypeScript | `openapi-typescript` (types) + `openapi-fetch` (≈6 kB runtime) | yes, `schema.gen.ts`; CI fails if stale |
| Kotlin | `openapi-generator` `kotlin` / `jvm-retrofit2` + kotlinx.serialization, coroutines | no, generated at build time |
| Docs | `web/scripts/gen-api-docs.mjs` → `docs/API.md` | yes; CI fails if stale |

**Why.** Committed Go/TS output makes code review show API changes and lets the server build
without Node. The Kotlin generator is a JVM tool that Gradle already runs, so committing its
large output would only add noise. `openapi-typescript` produces types only — no generated
runtime code to audit — which suits a small, fast web player. Dates are plain strings in the
Kotlin client to avoid a date-library dependency in the generated layer; `core-data` converts.

Kotlin build details: `core-api` emits Java 17 bytecode with whatever JDK runs Gradle (no separate
JDK 17 toolchain needed); free-form JSON maps to `JsonElement` because kotlinx.serialization cannot
handle `Any`; and a post-generation step drops duplicate `import` lines, working around a generator
template bug triggered by our two `apiKey` security schemes.

## ADR-0008 — "Same offline window" for progress conflicts

*(Implemented in M5 — see ADR-0020 for the final details.)*

The brief returns 409 when two devices disagree by more than the threshold "within the same
offline window". Proposed concrete rule (implemented in Phase 1): each `PUT /me/progress`
carries `last_synced_at`, the time that device last synced this book. If the stored record
came from a *different* device and was written **after** the incoming device's
`last_synced_at`, the incoming device didn't know about it — they were offline at the same
time. Then, if positions differ by more than the threshold, answer 409. Otherwise latest
`client_ts` wins silently. The client's re-send with `resolution: <conflict_id>` always wins
and is recorded in `progress_conflicts`.

## ADR-0009 — Repository layer (proposed, finalized in Phase 1)

Lean towards a **thin hand-written repository** over `database/sql`, with queries written once
using `?` placeholders and rebound to `$n` for Postgres, instead of `sqlc` with two query sets.
Because ADR-0004 keeps both schemas column-identical with portable types, almost every query is
the same text in both dialects; the few that aren't (upserts, `RETURNING`) are small branches.
Two `sqlc` query sets would double every query edit.

## ADR-0010 — No blanket trust of forwarded client IPs

**Decision.** Do not use chi `middleware.RealIP`. In Phase 1, client IPs (rate limits, audit log,
sessions) come from `X-Forwarded-For` only when the request arrives from a configured trusted proxy
(e.g. the Nginx Proxy Manager container); otherwise the TCP peer address is used.

**Why.** `RealIP` believes whatever `X-Forwarded-For`/`X-Real-IP` header a client sends, so anyone
could dodge per-IP rate limits or forge audit-log IPs. It is deprecated upstream for this reason.

## ADR-0011 — Default user capabilities

Until an admin changes them (setting `permission_defaults`), approved users get:
upload **yes**, edit metadata **yes** (only ever on their own uploads), delete own **yes**,
review **yes**, share **no**, post to Public **no**. Sharing and Public posting are the two
capabilities that put content in front of other people, so they start off and are granted per
user or per user tag (e.g. `trusted`). Restricted/banned/suspended users are limited by authz rule
order regardless of these defaults.

## ADR-0012 — Setup wizard: one transaction, config.json first, no restart

**Decision.** `/config/config.json` holds only where the database is (dialect, and the Postgres DSN
encrypted with `secret.key`). Everything else is in the DB `settings` table. The "instance is set
up" truth is the `setup_completed_at` setting, written in the **same transaction** as the admin
account, the Public group and all wizard settings. `config.json` is written *before* that
transaction. On start: no config → setup mode; config but no marker → setup mode reusing that DB;
marker → normal mode. After `POST /setup/complete` the server swaps from setup mode to normal mode
in-process (an atomic pointer), so no restart is needed.

**Why.** Any power cut leaves exactly one of two states: "not set up" (wizard runs again, nothing
half-created is visible) or "set up". Writing config.json last (the first draft) had a bad window:
commit, then power loss before the file is written → the wizard would come back but the DB would
refuse a second setup.

The setup token is random per process start and only printed to the log; it is compared in
constant time. The first admin's TOTP enrollment is mandatory (the wizard verifies a code before
creating anything) and the admin gets 10 recovery codes once.

## ADR-0013 — Authentication design

- **Access tokens:** HS256 JWTs, 15 min, key derived from `secret.key` via HKDF (purpose `jwt`).
  Every request still checks the session row, so revoking a session takes effect immediately rather
  than after 15 minutes. (Cost: one indexed lookup per request; fine for our scale.)
- **Refresh tokens:** 32 random bytes, stored as SHA-256 (high-entropy tokens don't need a slow
  hash), one session per device, 90-day sliding expiry. Rotation keeps the previous hash; presenting
  it again **revokes the session** (theft detection) unless it's within 30 s of the rotation, which
  is treated as two requests refreshing at once.
- **Web:** access token in `sapb_session` (HttpOnly, SameSite=Lax), refresh token in `sapb_refresh`
  (HttpOnly, path-scoped to `/api/v1/auth`, never in the JSON body), and a readable `sapb_csrf`
  cookie that must be echoed in `X-CSRF-Token` on state-changing cookie-authenticated requests
  (double-submit). Bearer-token requests are exempt (not sent automatically by browsers). The CSRF
  value stays stable across refreshes so several tabs keep working.
- **Passwords:** argon2id at OWASP's minimum (19 MiB, t=2, p=1) to respect the 150 MB RAM target;
  ≥ 10 characters, not containing the username, not in a bundled list of ~9,200 common passwords of
  that length (SecLists NCSC 100k, MIT).
- **Login lockout:** after 10 consecutive failures the account locks for 1, 2, 4 … minutes (cap
  24 h). Unknown usernames are verified against a dummy hash so timing doesn't reveal them.
- **Status at login:** pending → `account_pending`; denied sign-ups keep their user row (status
  `deleted`, reason `signup_denied`) so the next login can say so; suspended/banned users *can* sign
  in, because authz still lets them export their data, delete their account and log out (brief §6.7).
- **TOTP required but not enrolled** (admins, a tag with `require_totp`, or the instance-wide
  setting): the session works only for `/me`, TOTP enrollment and logout until enrolled.
- **TOTP enrollment** keeps the pending secret in memory for 10 minutes; a restart just means
  starting enrollment again.

---

## ADR-0014 — Opaque cursors, offset inside (for now)

`/library` and `/admin/books` return an opaque `next_cursor` that currently wraps an offset. Keyset
pagination is more robust when rows change between pages, but our sorts include "recently
played" (a nullable join) and author name (a join), where keyset cursors get complicated. At the
library sizes we target (10,000 books, pages of 50) offset is fast enough. Because the cursor is
opaque, switching to keyset later is not an API change.

## ADR-0015 — Book detection, covers, and the upload pipeline

**What counts as one book on disk** (`library.Detect`):
- a folder containing mp3 files is one book (all its audio, natural sort: "2" before "10");
- a folder with exactly one m4b/m4a is one book; several m4b files in one folder are one book each
  (`books.folder_path` is then the file's path);
- a folder with no audio whose audio subfolders are all named like `CD 1`, `Disc 2`, `Part 3` is one
  multi-disc book;
- dot-folders (including `.incoming`) are ignored.

**Metadata on import** comes from tags first (album/title, album_artist/artist, composer/narrator,
series/mvnm, series-part/mvin, genre, date, ASIN/ISBN), then the folder layout
`/<Author>/<Series?>/<Title>/`. Online providers and "needs review" arrive with the metadata
pipeline in Phase 2. A folder must be unchanged for 30 s before it is imported (half-copied books
are skipped and rescanned).

**Covers:** a `cover.jpg`/`folder.jpg` in the book folder wins; otherwise the embedded cover is
extracted to `/cache/covers/<book-id>/cover.jpg`. The server does not write into folders it didn't
create, so external managers (Chaptarr, LazyLibrarian) keep full ownership of their layout.
`books.cover_path` carries a `library:`/`cache:` prefix so volumes can move.

**Scanned books** belong to the admin (`uploader_id` NULL, `origin = scan`) and are visible to nobody
until distributed. A book whose folder disappears is quarantined ("files missing") rather than
deleted, and returns to `ready` when the files come back — the master store never loses a record
because a disk was unmounted.

**Uploads** use tus (resumable) into `/library/.incoming/` — inside the library volume so the final
move is an atomic same-filesystem rename. Processing is a job with this crash-safe order:
1. stage the file (or safely unzip: no paths outside the folder, only known file types, bounded size
   and entry count against zip bombs) into `<id>.d`, verifying the optional client `sha256`;
2. probe every file with ffprobe — any invalid file fails the upload with a clear message and
   deletes the partial data;
3. **dedup:** an identical whole-book checksum links the uploader to the existing book (no second
   copy; it counts against the original uploader's quota only);
4. insert the book (`processing`) and the uploader's library entry in one transaction, with a small
   "move note" in the staging folder;
5. rename into `/<Author>/<Series?>/<Title>/` (suffix ` (2)` if taken), then mark `ready`.
A crash between 4 and 5 is finished by the retried job, which redoes the rename from the note.

**Quota** = sum of sizes of books the user uploaded whose entry isn't removed, checked (with
in-flight uploads) before an upload starts.

**Deferred to Phase 2 (moderation):** users with `can_share` sharing their own uploads, and the
Public review queue. Admin distribution (including directly to Public) is in.

## ADR-0016 — Background jobs

One process, a `jobs` table, N workers (default 2). Claiming is a single
`UPDATE … WHERE status='queued' AND id=(SELECT … LIMIT 1) RETURNING …`, which is atomic on both
SQLite and Postgres. Failures retry with backoff 15 s, 1 m, 4 m, 16 m (max 5 attempts); handlers
mark validation failures `Permanent`. Jobs left `running` by a crash are requeued at startup.
Periodic jobs (hourly library scan, daily Trash purge, daily removal of 30-day-old "Recently
removed" entries) are deduplicated so they never pile up. fsnotify triggers a debounced scan; the
hourly scan is the fallback for mounts where inotify is unreliable.

---

## ADR-0017 — Bandwidth shaper

**Decision.** One in-process shaper enforces the global egress cap (default 80 Mbps) on every audio
byte (original files, HLS segments, downloads). Each response is a *flow*; every 20 ms the shaper
hands out that tick's byte budget:
1. **live playback first:** each live flow gets a guaranteed minimum of its bitrate × 1.5 (scaled down
   proportionally only if the minimums alone exceed the cap);
2. **weighted fair share of the rest:** class weights admin 4, high-priority group 2, everyone else 1;
   a class's share splits equally between its **users**, then between that user's connections —
   opening five connections gets you nothing extra;
3. **water-filling:** share a flow doesn't need is re-offered to flows that still want more, so the
   link never idles while anyone waits.

The allocation is a pure function (`stream.Allocate`) with deterministic unit tests; the ticker loop
around it is tiny. Clients mark offline downloads with `?download=1` (bulk); everything else
(playback range requests, HLS) is live. Covers are not shaped (small, cached by clients).
`Shaper.Snapshot()` exposes live flows for the admin dashboard (Phase 2).

**Alternatives.** Per-connection token buckets (unfair per user, can't prioritize); kernel traffic
shaping (`tc`) on the host (not portable to self-hosters, can't see users or classes).

## ADR-0018 — HLS: on-demand, frame-aligned, restart on seek

- **Codec.** The brief asks for HE-AAC 32 kbps for `low`. HE-AAC needs `libfdk_aac`, which is
  non-free and can't ship in our image. `low` is AAC-LC 32 kbps mono at 22.05 kHz (fine for
  speech), and `medium` is AAC-LC 64 kbps stereo at 44.1 kHz. Segments are MPEG-TS, which hls.js,
  ExoPlayer and the Chromecast Default Media Receiver all play.
- **Whole-book timeline.** One playlist covers the book across all its files (ffmpeg concat demuxer,
  `inpoint` into the first file). The playlist is generated up front from the known duration, so
  players can seek anywhere immediately.
- **Frame-aligned segments.** Segments are ~10 s rounded to a whole number of AAC frames (215 frames
  = 9.985 s at 22.05 kHz; 431 = 10.008 s at 44.1 kHz). A transcoder restarted at segment N therefore
  cuts at exactly the sample positions a from-the-start run would, and `-output_ts_offset` keeps
  timestamps continuous across runs.
- **Seek = restart.** A request for a segment that's not on disk and not about to be produced
  restarts ffmpeg at that segment. So a listener jumping to hour 30 of a 40-hour book waits for one
  segment, not for 30 hours of encoding.
- **No half-written segments.** ffmpeg's `hls_flags temp_file` writes each segment under a temp name
  and renames it when complete, so "file exists" means "segment complete".
- **Bounded work.** A transcoder more than 60 segments (~10 min) ahead of the last request is
  stopped and restarted on demand. The cache (`/cache/hls/<book>/<checksum>/<profile>/`) is keyed by
  the book's checksum, so re-imported files never serve stale audio. An hourly job evicts
  least-recently-used books above the cap (default 10 GB, setting `bandwidth.hls_cache_gb`).

## ADR-0019 — Signed stream URLs

Casting targets (Chromecast, DLNA, Sonos) can't send our auth headers, so `POST
/books/{id}/stream-url` returns URLs with `?sig=v1.<user>.<book>.<expiry>.<HMAC-SHA256>` (key derived
from `secret.key`, purpose `stream-url`), valid 6 hours and only for that book. Every request
re-checks that the user is still active and can still see the book, so a takedown, ban or revoked
distribution cuts off even an issued URL. HLS playlists fetched with a signature carry it into
every segment URI. URLs are built from the instance's public base URL (setup wizard), because a
speaker on the LAN must reach the server by its public name, not the address the phone used.

**Deferred:** a single "download whole book" endpoint (`GET /books/{id}/download`). Clients download
per file with Range (resumable) and `?download=1`, which already covers offline copies on Android
and Windows. A zip/m4b bundle can come later if a client needs it.

---

## ADR-0020 — Delta sync and last-write-wins, as built

- **Conflict rule** (ADR-0008, implemented in `internal/sync.Decide`, table-tested). The conflict
  check runs **before** "latest timestamp wins". Otherwise a device that listened offline for hours
  but has an older clock reading would be silently overwritten — exactly the data loss the rule is
  meant to prevent. A device moving within its own position never conflicts. The user's resolution
  (`resolution: <conflict_id>`) always applies; its timestamp is bumped past the stored one so later
  comparisons respect the choice, and the conflict row records the chosen position.
- **Clocks.** Client timestamps more than 5 minutes in the future are clamped to server time, so one
  device with a wrong clock can't win every comparison forever. Bookmarks and collections use the
  same clamped client time as their last-write-wins clock (`updated_at`). Clients may supply their
  own UUIDs, so creates made offline are idempotent when replayed.
- **What the change log records.** Per-user changes (progress, bookmarks, collections, the user's
  own library entry) carry the user id. Changes that can affect many users at once (distribution
  added/revoked, Trash, purge, quarantine, re-import) are recorded once with no user. Joining or
  leaving a group records one row per book distributed to that group, for that user. The delta
  re-checks visibility for every mentioned book: visible → a summary, not visible → a
  `library_entry` tombstone. This can occasionally send a tombstone for a book the client never had,
  which clients ignore; it never reveals titles or metadata.
- **Full snapshot** (no `since`) reads the cursor *first*, then gathers state, so anything changing
  meanwhile is simply sent again next time. It isn't paged in v1 (10,000 summaries is a few MB,
  once per device).
- **Retention.** The change log is pruned after 90 days; a cursor older than the low-water mark gets
  `410` and the client does a full snapshot.
- **Deferred to Phase 2:** sharing collections with users and groups (`collection_shares`), alongside
  the other social features.

## ADR-0021 — `/ws` change hints

`/ws` (Bearer header or session cookie) sends `{"type":"hello","seq":N}` on connect and
`{"type":"changed","seq":N}` whenever the change log gains rows for that user (or for everyone). It
carries no data — the client calls `GET /me/sync`. One notifier polls the change log every 500 ms
instead of hooking every write path, so changes made by background jobs (imports, scans, purges) are
covered automatically, and a hint can never be "lost" because a write forgot to publish one. The
WebSocket library's Origin check blocks cross-site pages from opening the socket with a user's
cookie. Pings every 30 s keep proxies from closing idle connections.

---

## ADR-0022 — Unclean-shutdown detection and the startup check

`/config/simpleapb.running` is written (atomically) when the server starts and removed only after a
clean shutdown — HTTP drained, jobs at a safe point, DB closed. If it exists at startup, the previous
run was killed or lost power, and the server:
1. runs `PRAGMA integrity_check` (SQLite) or pings Postgres,
2. requeues jobs left `running`,
3. deletes staging files with no upload record and re-queues imports for uploads stuck in
   `processing` (uploads still `receiving` are left for tus clients to resume),
4. queues a checksum check of files modified in the last 24 hours,
5. records the outcome (shown in `GET /admin/backups`), writes the audit log and pushes
   `unclean_shutdown` to admins.

It keeps serving even if the integrity check fails, and says so loudly: the admin decides whether to
restore a backup — the server never discards data on its own. Temp files left by interrupted atomic
writes in `/config` are swept at start.

## ADR-0023 — Backups and integrity scrubs

- **Nightly DB backup at 03:00 UTC**, keeping the newest 14: `VACUUM INTO` for SQLite (consistent
  while the server keeps writing), `pg_dump --format=custom` for Postgres. Snapshots are written
  under a temp name, fsynced, renamed. The Postgres password travels in `PGPASSWORD`, never on the
  command line; pgx-only DSN parameters (e.g. `search_path`) are translated into libpq `options`.
  `pg_dump` must match the server's major version, so the image ships the PostgreSQL 17 client. A
  missing `pg_dump` fails once (no retry storm) and pushes `backup_failed` to admins.
- **Weekly scrub, Sunday 04:00 UTC:** every file of every ready book is re-hashed against its stored
  checksum. A mismatch, a missing file or a book with no audio on record quarantines the book (hidden
  from users, reason recorded, audit logged) and pushes `integrity_failed`. Restore from the library
  mirror arrives with the mirror in Phase 6.
- Scheduled jobs run at their time slot, not "on every start", so a crash loop never triggers repeated
  full scrubs. Admins can run either on demand (`POST /admin/backups`, `POST /admin/integrity-check`).

## ADR-0024 — Push delivery and SSRF protection

Devices register either a **UnifiedPush endpoint** (the distributor app, usually ntfy, gives them a
URL; the server POSTs there) or get a **random 128-bit topic** on the instance's own ntfy. Payloads
are `{type, id}` only. Delivery is a background job and best-effort: apps also sync on start and via
`/ws`, so a lost push loses nothing. Endpoints answering 404/410 are dropped.

Because the server POSTs to URLs clients give it, endpoints must be `https` and must not resolve to
loopback, private, link-local or metadata addresses. This is checked at registration **and again at
connection time** inside the dialer, which defeats DNS rebinding. The only exception is the
instance's configured ntfy host. Redirects aren't followed, and the ntfy access token is sent only to
the instance's ntfy, never to third-party endpoints.

## ADR-0025 — The SIGKILL crash harness

`cmd/simpleapb/crash_test.go` builds the real binary and, for N rounds, drives concurrent work at it:
- a resumable upload continuing from wherever the previous crash left it,
- a stream of progress writes,
- database backups,
- library scans of books copied in non-atomically.

It then kills the process at a random moment (SIGKILL / TerminateProcess — no cleanup runs).

After every kill, with the server down, it asserts:
- the DB passes `integrity_check`;
- every ready book's files exist and match their checksums;
- every finished backup is itself a valid database.

After every restart, it asserts that the unclean shutdown was detected and that no acknowledged
progress write was lost. At the end, the interrupted upload must complete and the recovery must be
recorded as OK. CI runs 40 kills nightly. A process kill (unlike a power cut) keeps the OS page cache,
so true power-loss behavior additionally relies on fsync discipline, WAL mode and ZFS — the harness
proves the application's own ordering is right.

---

## ADR-0026 — Web app stack and embedding

**Decision (brief decision #8, answered: Svelte).** Svelte 5 (runes) + Vite + TypeScript strict.
Kept deliberately small:
- a ~60-line History-API router instead of a routing library;
- `openapi-fetch` with types generated from `openapi.yaml`;
- `intl-messageformat` for ICU strings from the shared `/i18n` catalogs (every string goes through
  `t()`);
- `qrcode` renders 2FA QR codes in the browser, so the secret never goes to a third-party service.

The first build is ~49 KB gzipped of JavaScript.

**Auth in the browser** uses the server's cookies (ADR-0013). The client reads the `sapb_csrf`
cookie and echoes it on writes. On `401 invalid_token` it refreshes once (concurrent requests share
one refresh) and retries; if that fails, the app shows the sign-in page.

**Embedding.** `vite build` output is copied into `server/internal/web/dist` (`npm run build:embed`
locally, a Node stage in the Dockerfile) and compiled into the binary with `go:embed`. Hashed
assets are cached for a year; `index.html` is `no-cache`, so a new deploy takes effect on the next
load. Every non-API path falls back to `index.html` (deep links); a missing asset is a real 404; API
404s stay JSON. Setup mode now gates only `/api/*` and `/ws`, so the wizard page itself loads.

**Security headers on the app:** a CSP with `script-src 'self'` (no inline scripts — the
pre-paint theme snippet is an external file), `frame-ancestors 'none'`, `nosniff`,
`Referrer-Policy: same-origin` and a restrictive `Permissions-Policy`.

**Themes.** Colours exist only as CSS tokens on `[data-theme]`. The dark theme's accent is a
placeholder until decision #9 is made on real screens. The e-ink theme is pure black on white with
hairline borders and **no** animation, shadows or gradients (enforced globally with
`!important`, so a component can't accidentally animate). `prefers-reduced-motion` zeroes motion in
the dark theme too.

## ADR-0027 — Single-use TOTP codes (Q5 answered: yes)

Each account stores the last accepted 30-second TOTP step (`users.totp_last_step`, migration
00002). A code is accepted only if an atomic `UPDATE … WHERE totp_last_step < step` claims its step,
so a code seen over someone's shoulder — or two simultaneous requests with the same code — can't be
used twice. This applies to login, enrollment, the setup wizard (so the wizard's code can't be
replayed for the first login) and re-authentication for dangerous actions (emptying the Trash, which
now checks the typed count *before* spending the code).

## ADR-0028 — Library facets and `has_cover`

**Decision.** `GET /library/facets?kind=series|authors|narrators|tags` lists the names in my
library with book counts (same visibility rules as `/library`, "all" shelf only). `BookSummary`
gains a required `has_cover`.

**Why.** The Library's Series/Authors/Narrators/Genres views (brief §7.4) need the list of names;
paging through the whole library on the client to collect them doesn't scale. `has_cover` lets
clients skip the cover request for books without one: before it, every coverless book cost a 404
(and a console error) per screen.

## ADR-0029 — The web player engine

**Decision.**
- **One `<audio>` element, book timeline.** Original quality plays each file in turn and maps
  positions with the files' `offset_ms`. Data saver plays the server's HLS playlist through
  hls.js (light build, loaded only when needed), or natively in Safari.
- **Signed stream links** (`POST /books/{id}/stream-url`, 6 h) for all media, not the access
  cookie. The browser fetches audio in many range requests over a chapter; with the 15-minute
  cookie those would start failing mid-chapter. Links are renewed 10 minutes before they expire
  and once on a playback error.
- **Quality "Automatic"** = original, except data saver when the browser reports a cellular
  connection (brief §6.9). Files this browser can't decode fall back to data saver.
- **Progress** every 15 s while playing, on pause, seek (debounced), end and tab hide; a
  `keepalive` request on page unload. A 409 shows the "Jump to … from <device>?" prompt; the
  user's answer is sent with `resolution`. While paused, a newer position from another device
  is followed silently.
- **Live hints.** `/ws` also hints a tab about its own writes. Hints within 1.5 s of our own
  successful write are treated as echoes and dropped (the server polls every 500 ms), and bursts
  are coalesced, so open screens don't reload every 15 s during playback.
- **Offline is not signed out.** A refresh that fails because the server is unreachable keeps
  the session; only a refusal from the server signs the user out.
- **Per-browser preferences** (skip lengths, quality, e-ink refresh interval, left-handed layout,
  grid/list) live in `localStorage` for now; syncing them belongs with the account settings work.

**Not in the web player (v1).** Boost/EQ/mono/balance, casting, car mode and downloads. Audio
settings need Web Audio, which stops background playback in mobile browsers; casting and car mode
are Android features; the web player is streaming-only (brief §9).

## ADR-0030 — Account self-service

**Decision.** `PATCH /me` edits display name, email, locale, the sync conflict threshold
(1–30 minutes, brief §6.8), the "who's listening" opt-out and a free-form `prefs` object (≤ 8 KB,
last write wins). `POST /me/password` checks the current password and signs out every *other*
session. `POST /me/totp/disable` needs password + code and is refused while 2FA is required
(admin, a tag, or instance-wide); `POST /me/recovery-codes` needs a code. `DELETE /me/sessions`
signs out all other devices. Password and code re-checks share the per-account login rate limit.
`Me` now carries `totp_required`, so clients don't guess it from the role.

**Synced vs per-device preferences.** Skip lengths, the e-ink refresh interval, the left-handed
layout and the default speed follow the account (`prefs`, snake_case keys shared with the other
clients). Theme and stream quality stay per device: an e-ink reader and a phone on cellular want
different answers.

**Not here.** Data export and self-service account deletion are Phase 6 work (brief §16).

## ADR-0031 — Listening stats

**Decision.** `GET /me/stats?year=&tz=` is computed on request from `progress_events` (each
progress write records from/to position and the device's start/end times). An event counts
`min(position advance, wall-clock time)`: real time at any speed, pauses inside an event don't
count, and a jump faster than 3.5× the elapsed time is a seek and counts zero. Days are calendar
days in the client's IANA time zone (tzdata is embedded in the binary). An author gets the full
time of each of their books. The longest streak is reported as a fun fact only (brief §6.17).

**`finished_at` is the device's time.** It used to be the server's receive time, which puts a
book finished offline (and synced later) on the wrong day or even year. It's now the write's
`client_ts`, never later than now.

**Known limit.** Below 1× speed the formula undercounts a little (events don't store speed).

## ADR-0032 — The admin panel

**Decision.** One admin section in the web app (brief §6.23), loaded on demand so listeners never
download it: dashboard, inbox, users, groups, user tags, library (master store), Trash, audit log,
backups and settings. New API: `GET /admin/dashboard`, `GET /admin/growth`, user detail with
tags/groups/permission overrides/devices, user-tag CRUD, group rename/priority/members, book
takedowns, `GET /admin/audit` (+ CSV export), `GET/PATCH /admin/settings` and an SMTP test.

**Dashboard health is what the host can tell us, never guessed.** Disk space comes from
`statfs`/`GetDiskFreeSpaceEx` per volume; memory and load from `/proc` on Linux (shown as unknown
elsewhere); the ZFS pool is always "unknown" — a container can't query `zpool` without extra
privileges, and the brief allows "unknown". "Jobs waiting" counts running and *due* jobs only, not
the nightly backup scheduled for later. Live streams are the shaper's open responses with their
average rate since they started.

**Settings.** Secrets (SMTP password, ntfy token) are write-only: the API reports only whether they
are set, and the audit log records which groups changed, never values. Changing the bandwidth cap
applies immediately. A new `quotas.default_quota_bytes` setting gives newly approved accounts their
quota (brief §6.11). The copyright contact (Q10) is shown in the app footer when set.

**Audit CSV** cells that start with `= + - @` are prefixed with `'` so spreadsheet apps don't run
them as formulas.

**Not yet:** duplicates (with the book tools), book tools
(Phase 5), updates and crash reports (Phase 6), OIDC settings (Phase 6). Metadata-provider
settings arrived with ADR-0035.

## ADR-0033 — Password reset links without a table

**Decision.** An admin can make a one-time reset link (brief §6.6, for instances without SMTP).
The token is `userID.expiry.HMAC(key, userID, expiry, current password hash)`, with a key derived
from `secret.key`. It needs no table and is single-use by construction: setting the password
changes the hash, so the same token (and any other outstanding link) stops matching. Links work
for 24 hours, are rate-limited per IP, and using one signs the account out everywhere and clears a
lockout. The token sits in the URL fragment (`/reset-password#…`), so it never reaches server logs
or `Referer` headers. Email-based reset (when SMTP is configured) comes with the mail templates.

## ADR-0034 — Social features and moderation

**Sharing your uploads (brief §6.5 rules 5–6).** `authz.BookShare` makes the whole decision from
three facts the handler gathers: is the user the book's uploader, what kind of target is it
(a person, a group they're in, another group, Public), and their permissions. People and your own
groups need `can_share`; Public needs `can_post_public` and always goes to the review queue; other
groups are refused; admins share anything directly. A user's share is a normal `distributions`
row with `distributed_by` = the user, so visibility needs no new rule, the user can take it back,
and admins can revoke it like any other. Approving a Public request also records the uploader as
`distributed_by`, so they can later stop sharing it publicly themselves. "People" means active
users who share a group with you (`GET /people` searches only them, at least 2 letters).

**Recommendations never grant access.** The brief lists `recommendation` as a library-entry
source, but an entry with that source would make a book visible to someone the admin never
shared it with. So a recommendation is only a row in `recommendations`: it reaches people who can
already see the book (the rest are counted as `skipped`, never named), and "Recommended to you"
re-checks visibility when it is read. The `recommendation` entry source stays unused.

**Reviews.** One live review per user and book (`PUT /books/{id}/reviews/mine`), up to 500
characters, needs `can_review`; deleting your own is always allowed. A moderator hides a review
with a reason: it drops out of the list and the average for everyone but its author (who sees it
marked hidden, without the reason) and admins. Editing a hidden review keeps it hidden. Hiding
doesn't change the review's edit time.

**Reports.** Anyone active can report a book or review they can see, or a person (not themselves
or their own review), choosing a reason from a fixed list. Reporting the same thing again while
your report is open updates it, and one account can have at most 50 open reports. Resolving
needs a note and closes every open report about the same object at once: dismiss, hide the review,
take the book down (the reviewed book, for a review), or "resolved" after acting on the person
from their user page. New reports and review-queue items notify admins by push.

**Who's listening.** People with progress on the book in the last 30 days, or who finished it, minus anyone who
turned off "show my listening", inactive accounts and yourself; at most 50.

**Collection sharing.** Any active user may share a collection with people or their own groups,
Public included, because recipients only ever see the books in it they can already see. Shared
collections are read through `GET /shared-collections`; they are not part of delta sync yet (the
apps can add them when they need them).

**Warnings.** The sign-in response already carries an admin's notice once; the web app now shows
it in a dialog after signing in.

## ADR-0035 — The metadata pipeline

**Order (brief §6.12).** Embedded tags are read at import (ADR-0015). Every newly imported book,
scanned or uploaded, then gets a `metadata_match` job. The job asks the enabled providers in order:
Audnexus (ASIN only), Google Books, Open Library. It never touches a field that already has a value
or is locked. A hand edit (`PATCH /books/{id}`) saves the fields and adds them to
`books.locked_fields`, so no later match can change them; `unlock` releases them. Admins can edit any
book, uploaders with `can_edit_metadata` only their own (`authz.EditBookMetadata`).

**Matching and confidence.** A shared ASIN or ISBN (ISBN-10 is converted to ISBN-13 and the check
digit is verified) scores 1. Otherwise the title counts 70 % and the best author 30 %. Both are
normalized first (case, accents, punctuation, a leading article, bracketed notes and words like
"unabridged") and compared with an edit-distance ratio or word overlap, whichever is higher, so
"Lindqvist, Mara" matches "Mara Lindqvist". With no author known, the title alone scores at most 0.75, so
a bare title ("Test", "Untitled") always goes to review instead of borrowing a random book's details. Candidates at or above the threshold (default 0.8, adjustable from 0.5 to 1) are applied,
best first, filling only the fields that are still empty. Below the threshold nothing is applied:
the book gets "needs review" and appears in the Metadata tab of the moderation inbox. Any hand
edit, or "Keep as is", clears the flag. A no-match also counts as needs review, because an admin
should look at a book nobody could identify.

**What each provider contributes.** An Open Library search result describes a whole work, so its
ISBN and publisher lists mix every edition. They are used only when the search was by ISBN; a
title search gives the title, authors, first publication year, language, subjects (as genres) and
cover. Google Books gives publisher, date, description, ISBN and categories. Audnexus gives
narrators, series and position, genres and the summary (HTML is converted to plain text).

**Network behaviour.** Requests carry a `SimpleAPB/<version>` User-Agent. Each provider has its own
process-wide spacing (Audnexus about one request a second, as its maintainers ask). HTTP 429/503
counts as rate limited. A run fails, and the job retries with backoff, only when every provider it
actually asked failed; a provider with nothing to look up (Audnexus without an ASIN) doesn't count.
So an offline instance or Google's keyless quota (which runs out quickly on shared networks)
doesn't break matching. Admins can add a Google Books API key; it is stored encrypted and never
shown again.

**Covers.** A provider cover is used only for a book with no cover. It is downloaded only from
known provider image hosts over HTTPS, including Open Library's redirect to archive.org storage,
so a cover URL can't make the server fetch arbitrary or internal addresses. It must be at most
10 MB and a real JPEG or PNG between 32 and 6000 px; PNG is converted to JPEG. It is written
crash-safely as `cover.jpg` in the book folder (an existing `cover.jpg` the book doesn't use is
never overwritten), or to the cache if the library is read-only. `cover_url` now carries
`?v=<change time>`, so browsers fetch a new cover at once despite the one-day cache. Clients add
`size` with `&`.

**Not done.** No `metadata.json` sidecar is written yet (brief §6.4 calls it optional); hand edits
live in the database and survive re-scans, because a re-scan only replaces files and chapters.
Moving a book folder after its title or author changes is left to the book tools (Phase 5).

---

## Open questions for Jacob

| # | Question | Current choice (until answered) |
|---|---|---|
| Q1 | ~~GitHub owner~~ **Answered:** `quip1` — `github.com/quip1/simple-apb`, image `ghcr.io/quip1/simple-apb-server` | — |
| Q2 | License identifier: `AGPL-3.0-or-later` or `AGPL-3.0-only`? | `AGPL-3.0-or-later` (GNU's recommendation) in SPDX headers |
| Q3 | Commit the Gradle wrapper now, or when the Android app starts (Phase 3)? | CI installs Gradle 9.1 directly; wrapper added in Phase 3 |
| Q4 | ~~Review the schema and API outline~~ **Approved** (Phase 0 accepted) | — |
| Q8 | ~~Web framework (brief #8)~~ **Answered: Svelte** (ADR-0026) | — |
| Q9 | Accent colour and icon (brief #9): **to be picked on real screens** once Home/Library/Player exist | Placeholder accent |
| Q10 | Copyright/takedown contact in the footer (brief #3) | Yes, admin-editable (proposed default) |
| Q5 | ~~TOTP replay protection~~ **Answered: yes** (ADR-0027) | — |
| Q7 | ~~AAC-LC instead of HE-AAC~~ **Answered: AAC-LC is fine** (ADR-0018) | — |
| Q6 | ~~Setup wizard page in Phase 2~~ **Answered: yes** — shipped in Phase 2 milestone 1 | — |
