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
or `Referer` headers. Email-based reset arrived with ADR-0036.

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

## ADR-0036 — Email templates and reset by email

**Templates (brief §6.15).** Four emails: sign-up approved, sign-up declined (with the admin's
note), password reset, and admin alerts. Each is sent as `multipart/alternative`: plain text plus
a simple inline-styled HTML version, because mail clients ignore stylesheets. The words live in
the shared catalogs (`i18n/*.json`, keys `mail.*`), so they are translated with the rest of the
app. `go generate` copies them into `server/internal/mail/catalog/` for embedding, and a test fails
if the copy is stale. Mail strings use simple `{placeholders}` only, with no plurals, and are
rendered in the recipient's locale, falling back to English. HTML values are escaped.

**Delivery.** Emails are jobs (`mail_send`). A slow or unreachable mail server never delays a
request, and failures retry with backoff. Without SMTP nothing is queued. The job result never
records the address or the link.

**Forgot password.** `POST /auth/password-reset/request` takes a username or email address and
always answers 202. It emails the existing reset link (ADR-0033) when the account can sign in and
has an address. Only the configured instance address is used for the link, never the request's
`Host` header, so a forged header can't plant another site's address in someone's reset email.
Email reset is offered (`Instance.email_password_reset`) only when SMTP and the instance address
are both set. Requests are limited to 3 per hour per IP and, silently, per account, so nobody can
use the form to flood an inbox.

**Admin alerts.** Admins who turn on "Email me admin alerts" in Settings (the account pref
`email_alerts`, which needs an email address) get an email for each admin push event: sign-up
request, report, review request, failed backup, integrity problem, unclean restart. The email is
sent on the push job's first attempt only, so push retries don't repeat it. Like push, it carries
no personal details: it names the event and links to the admin panel.

## ADR-0037 — Web uploads and the end-to-end journey

**Uploads in the web app.** "Upload books" (from the library, for accounts with `can_upload`)
sends files to the tus endpoint with a small client (`web/src/lib/upload.ts`). It sends 8 MB
chunks; after a network error it asks the server how much arrived (HEAD) and continues from
there, and it refreshes the session once if a long upload outlives the access token. The page
lists recent uploads and their import status. This completes "run the instance from the browser"
(Phase 2 acceptance) and the brief's end-to-end journey.

**Playwright (brief §15).** `web/e2e` starts the real server binary with its own temporary
directories, completes setup through the API, then drives the journey in Chromium: a sign-up
through the form, approval in the admin inbox (signing in with a TOTP code), an upload, sharing it
with Public from the admin library, playback on one browser profile, and the position carried over
to a second profile (a second device). The fixture is an mp3, because Playwright's Chromium has no
AAC decoder. Each profile sends its own `X-Forwarded-For` (a documentation address,
RFC 5737), with the server trusting only 127.0.0.1 as a proxy. That way the real per-IP sign-in
limits stay on and apply per profile. Online metadata lookups are switched off for the run. The
suite runs serially with no retries (a retry would meet the accounts the first try created). CI
runs it as the `e2e` job and keeps the report and traces when it fails.

## ADR-0038 — Accent colour and icon

**Decision (Jacob, decision #9).** The accent is **Fern** (`#94d68f`, with `#0b230c` for text on it),
chosen from four options shown on real screens (Tide, Dusk, Fern, Ink). On the dark background it
has an 11:1 contrast, the highest of the four, and text on Fern buttons has about 9.7:1. In e-ink
the accent stays plain black. The icon is **Open book**: an open book with sound rising from it,
which says "audiobook" at a glance and stays readable at 24 px. It comes in colour, an e-ink
version (black on white) and Android adaptive layers (`branding/`), with the foreground scaled to
stay inside the 66 dp safe zone and a monochrome layer for themed icons. The web favicon uses it
now; the Android app adopts it in Phase 3.

## ADR-0039 — The Android app's foundation (Phase 3, milestone 1)

**Toolchain.** The build uses the Android Gradle Plugin 9.5 (with its built-in Kotlin support)
and Kotlin 2.4. compileSdk and targetSdk are 37, and minSdk is 26 (brief §7.1). Libraries are
current stable versions: Compose BOM 2026.09, Media3 1.11, Room 2.8, Hilt 2.60 with KSP. A Gradle
wrapper (9.9.0, checksum-pinned) is now committed, which answers Q3. CI builds with JDK 21.

**Modules.** The modules are `core-api` (the generated client, pure JVM), `core-ui` (theme and
strings), `core-data` (accounts, sessions, API access, and later the Room mirror and sync) and
`app`. `core-player`, `wear` and `cast` join when their milestones start, so the build never
carries empty modules. The flavors are `play`, `github` and `fdroid`. In milestone 1 they differ
only in the Cast and update-check build flags; the F-Droid flavor never adds Google libraries, and
the build omits the dependency-info blob that would make it non-reproducible.

**One string catalog.** Android strings are generated at build time from the shared
`i18n/*.json`, the same catalog the web app and server use:
- `auth.sign_in` becomes `R.string.auth_sign_in`.
- `{name}` placeholders become `%1$s`, numbered in the order they first appear in the English
  message, so every translation keeps the same argument order.
- A message with a single plural becomes `<plurals>`. Exact-value branches (`=0`) have no
  Android equivalent and are dropped.
- Messages Android can't express (two plurals, `select`) are skipped.

Android-only strings use the `app.` prefix in the same file.

**Accounts and tokens.** Several servers and accounts can be signed in at once, with one active
(brief §7.4). Account details live in DataStore. The token pair is sealed with AES-256-GCM under
an Android Keystore key before it is written, and backups and device transfer are switched off,
so tokens never leave the phone. Requests carry the access token. On a 401 the client rotates the
pair once with the refresh token and retries; parallel requests wait for that one refresh rather
than each refreshing. A refused refresh (401/403) signs that account out with a notice. A network
failure during the refresh keeps the account, matching the web client. Sign-in sends the
installation's device id, model name and app version, so the device shows up in Sessions on the
web.

**Connecting.** The server address is whatever the user types: there is no default. `https://` is
added when no scheme is given, a pasted `/api/v1` is ignored and sub-paths work. The app reads
`/instance` before signing in and enforces the version guard (brief §6.21). Plain-http servers
(home networks without TLS) are allowed, but only after a warning that the password would travel
unprotected. Accounts that must set up 2FA are told to do it in the web app for now; enrolling
2FA in the app comes with the account screens.

**Bugs found on the way.**
- The generated Kotlin client sent enum query parameters by name ("ALL"); a build step now gives
  those enums a `toString()` that returns the wire value ("all").
- The server answered an unknown shelf with a 500 and ignored unknown sorts. Both, and unknown
  facet kinds, are now a 400 `invalid_filter`.

**Testing.**
- Unit tests run on Robolectric pinned to SDK 35, because Robolectric doesn't support SDK 37 yet.
- The Android 37 emulator image crashes on the development PC: SurfaceFlinger aborts in
  `GoldfishMapper::readFromHost` with every graphics mode. The app was checked on an Android 16
  (API 36) emulator instead.

**Phase 3 plan.** Milestones, each started after Jacob's go-ahead:
1. **Foundation:** this milestone.
2. **Playback:** the Media3 `MediaLibraryService`; signed-in streaming over the book timeline (ADR-0040);
   the notification and lock screen with configurable buttons; speed, skips, auto-rewind, sleep timer
   with fade and shake, audio focus, headset gestures; the player screen and mini player.
3. **Screens:** Home, Library (views, search, grid/list), book detail with reviews and actions,
   author/series pages, Stats, Settings and account screens.
4. **Offline:** the Room mirror and outbox, the WorkManager sync engine with `/ws` hints and the
   conflict prompt; downloads (resumable range requests, checksum, cap, SD card, Wi-Fi only,
   auto-download of the next in series).
5. **Car and widget:** the audio effect chain (boost with limiter, voice EQ, mono, balance), car
   mode, the Android Auto browse tree and voice search, the Glance widget.
6. **More:** local folders and the merged library with upload, UnifiedPush with the periodic
   fallback, the multi-server switcher polish, e-ink and accessibility passes (TalkBack, 200 %
   font).
7. **Hardening:** instrumented and Compose UI tests including e-ink and large-font, a DHU checklist,
   F-Droid build checks, and release signing for the three flavors.

---

## ADR-0040 — Android playback (Phase 3, milestone 2)

**One service, one session.** `core-player` holds a single Media3 `MediaLibraryService`. The
notification, lock screen, headset and Bluetooth buttons, and the app itself all control playback
through its `MediaSession`; Android Auto and Wear will attach to the same session later. The app
talks to it through a `MediaController` (`PlayerConnection`). Book-level state the session can't
carry (the loaded book's timeline, the sleep timer, a pending sync conflict) lives in a singleton
the service and the screens share. Both run in the app's process.

**A book is a playlist on one timeline.** Each audio file is one playlist item, so ExoPlayer plays
multi-file books without gaps. `BookTimeline` maps between the book-global position (what the user
sees and what progress reports carry) and (file, offset), and answers chapter questions. The
notification's seek bar covers the current file. For single-file books, the usual case, that is
the whole book.

**Streaming is signed in.** Files and covers are fetched with the account's own OkHttp client: the
access token is added and refreshed exactly as for API calls. One call factory signs in each
request as the account whose server address the URL starts with (longest match, so sub-path
servers work and look-alike hosts don't match). A book keeps streaming from its own server after
the user switches the active account. Signed stream URLs stay reserved for casting.

**Audiobook controls, not music controls.**
- "Next" and "previous" from Bluetooth, the car or the watch skip forward and back by the
  configured amount, never to the next file.
- The notification shows play/pause plus the user's buttons, in their order (up to five in all):
  - The button just before play/pause in the list takes the "back" slot, and the one just after
    it takes the "forward" slot. The rest go to the overflow.
  - The system draws play/pause itself, so it can't be removed.
  - The notification's own controller loses the "previous/next track" commands, so only the
    chosen buttons appear.
- Auto-rewind happens on resume: the full amount after a pause of a minute or more, half after a
  shorter one.
- A finished book starts again from the beginning on play.

**Headset gestures.** Media button key events are counted as single, double and triple presses
(a 450 ms window, so a double press never also fires a single one) or a long press. Many phones
keep a long press of the headset button for the voice assistant and pass the app only the
key-up. That stray key-up is ignored, so long press works only where the device passes it on
(brief: "where the device supports it"). Media next/previous and fast-forward/rewind keys skip.

**Sleep timer.** The timer is either a duration or "end of chapter". The volume fades linearly
over the last 10 s, then playback pauses. While a timer runs, and only then, the accelerometer
watches for a shake: two jolts above 2.3 g within 0.8 s add the configured minutes (default 5) to
the time left.

**Audio focus.** Calls always pause. Other interruptions such as navigation prompts follow the
setting:
- "Pause" plays the book as speech content, and ExoPlayer pauses for duckable interruptions.
- "Lower the volume" plays it as music content, and ExoPlayer ducks instead.

Unplugging headphones pauses.

**Progress.** The service reports the same way the web player does:
- When: every 15 s while playing, on pause, 1.5 s after a seek settles, at the end of the book
  (as finished), and when the app is swiped away.
- With: `last_synced_at` (the time of the last successful report for that book).
- A 409 shows the "Continue where you left off?" prompt. The "this device" side uses the live
  position, since playback continues while the user decides. No new reports are sent until the
  user answers.
- The position is also saved on the device every 5 s. On load, the newer of the server's record
  and the local one wins, so an unreachable server never loses the user's place.
- Failed reports are not queued yet: the outbox comes with the offline milestone.

Speed is saved per book (local first, then the server's, then the default for new books).

**Resumption.** The last book is remembered. A headset "play" after the app was closed resumes
it at its saved position (Media3 playback resumption with the media-button receiver).

**Settings are per device for now.** The playback settings are:
- skip lengths, auto-rewind and the speed for new books
- notification buttons and headset mapping
- focus behaviour and shake to extend

They live in the app's DataStore. Syncing the shared ones with the account preferences, as the web
does, comes with the account screens in milestone 3.

**Bugs found on the way.** The Android string generator didn't convert ICU `{x, number}`
arguments, so the "1.5×" speed label showed the raw ICU text; it now turns them into plain
arguments. The Gradle daemon needed 4 GB once the media libraries joined the build.

**Checked on the emulator** (API 36) against the demo server:
- streaming a multi-chapter book and the cover
- the notification with default and custom buttons
- skip and chapter jumps across chapter boundaries
- speed, a 1-minute sleep timer stopping playback, and bookmarks with notes
- the conflict prompt with "Jump"
- auto-rewind
- headset single, double and triple presses
- resuming from a headset button with the app closed
- progress and "finished" reaching the server

Shake and the fade can't be checked on the emulator; they are unit-tested.

---

## ADR-0041 — Android screens and account settings (Phase 3, milestone 3)

**Layout.** Four bottom tabs: Home, Library, Stats and Settings. The mini player sits above them,
and the full player opens from it (brief §11: Home, Library and Now playing, plus Stats and
Settings). Downloads joins the tabs with the offline milestone. A page opened from a tab keeps that
tab lit. The selected tab is bold, and also underlined on e-ink, so selection never relies on
colour alone.

**Screens.** They follow the web app, page for page and with the same API calls:
- **Home:** continue listening (with a progress ring), recommended to you (with dismiss), next in
  series, recently added.
- **Library:** the All, Series, Authors, Narrators, Genres and Collections views; shelves and sort;
  instant search; grid or list (remembered per device); collections can be created, renamed and
  deleted, and collections shared with you are listed.
- **Author, narrator and series pages.**
- **Book page:**
  - details, chapters (tap to play from there), bookmarks and progress;
  - ratings and reviews (write, edit, delete, report) and who's listening;
  - actions: play, mark finished, add to collection, archive, remove or restore, recommend,
    report, and edit details for editors.
- **Stats:** the year in review, daily and weekly charts (each with a table view), time per book,
  top authors, books finished.
- **Settings and Security:** described below.
- **Accounts:** the servers and accounts on the device.

Lists refresh when a screen opens and from a refresh button. Live `/ws` hints come with the sync
engine (milestone 4).

**E-ink.** No animation and no greys:
- Shelves are a fixed grid of six instead of a sideways scroll.
- The library, facet, series and chapter lists move by page, with Previous and Next buttons.
- Covers and progress rings get black hairlines.
- The player's time display moves only every 10–30 s (a new synced preference). Seeks and pauses
  still show at once.
- Before the saved theme is read, the app draws nothing, so an e-ink screen never flashes the dark
  theme first.

**Preferences follow the account.** Skip lengths, the speed for new books, the e-ink refresh
interval and the left-handed layout sync through `prefs` on `/me`, with the same keys as the web
(ADR-0030). Last write wins, and keys from other clients are kept:
- The app adopts the account's values when it signs in, switches account, or opens Settings.
- A local change is sent 0.8 s after the last edit.

Everything else stays per device:
- the theme and streaming quality;
- auto-rewind, the notification buttons, the headset mapping, focus behaviour and shake.

**Left-handed layout.** It reverses the order of the player's control rows. An early version
flipped the layout direction instead, which also flipped text: the speed read "×1".

**Streaming quality.** The choices are Automatic, Original, Data saver and Low data. Automatic
streams the original files, and the 64 kbps data saver on a metered connection. Data saver and
low data stream the server's HLS version of the whole book as a single item. Positions are already
book-global, so chapters and progress don't change. The choice applies the next time a book
starts.

**Two-factor sign-in in the app.**
- A phone can't scan a QR code shown on its own screen. So enrolment offers an "Open in
  authenticator app" link (`otpauth://`), the key to copy, then a code, then the recovery codes,
  shown once.
- An account that must use 2FA now signs in and sets it up in the app. Before, the app sent it to
  the web; ADR-0039 promised this change.
- Security also covers changing the password, new recovery codes, turning 2FA off, and signed-in
  devices with sign-out.

**Marking finished** from the book page is an explicit choice. If another device's position
conflicts, the app settles it in favour of the choice. If the book is in the player, the player
moves to the end or to the start, so its next report agrees. Playing from a chapter or bookmark
passes the start position with the request, so the book never starts at the saved position and
then jumps.

**Left for later:**
- Downloads, "Downloaded" and "On this device" come with milestones 4 and 6; push with milestone
  6; crash reports, data export and account deletion in Phase 6.
- Sharing your uploads and collections, "Find a match online" when editing details, and hiding
  reviews (admins) stay in the web app.
- There is no in-app language choice: the app follows the phone's language, and English is the
  only translation in v1.
- "Open EPUB if attached" (brief §7.4) has no API yet: `BookDetail.has_epub` exists but there is
  no endpoint to fetch the file. Logged as Q11.

**Checked on the emulator** (API 36) against the demo server:
- Home, library search, the Series view, a series page and the book page;
- posting a review, creating a collection with the book in it, recommending to another user;
- playing from a chapter, and marking finished and not finished (checked on the server);
- the Stats page;
- preference sync both ways (a left-handed change reached the server; a skip length set elsewhere
  reached the player), and the left-handed player;
- the e-ink theme, the e-ink time refresh, and low-data HLS playback;
- turning 2FA on and off, and enrolment forced by an admin tag requiring 2FA.

---

## ADR-0042 — Android offline sync and downloads (Phase 3, milestone 4)

**The mirror.** A Room database (`mirror.db`, schema exported to `core-data/schemas`) keeps, per
signed-in account:
- the library entries and book details, stored as the API's own JSON, so new server fields need
  no migration;
- progress, bookmarks and collections;
- the outbox, conflicts found offline, the sync cursor, and downloads.

When the server can't be reached:
- Home and the Library show the mirror, with a notice. Shelves, sort and search run on the device;
  a genre filter can't, because library entries carry no tags.
- A book page shows the saved details. Downloads always have them.

Signing out of an account deletes its mirror, outbox and downloads; queued writes are sent first
when the server is reachable.

**The outbox.**
- Writes that fail with no connection are queued and sent in order: progress reports (from the
  player and "mark finished") and bookmark adds and deletes.
- Only the newest queued position per book is kept. The server keeps the latest by client time
  anyway, and each write carries the `last_synced_at` it had when written, so the "same offline
  window" check (§6.8) still works.
- Deleting a bookmark that was never sent just drops it.
- A server error leaves the item for the next attempt; after 8 failed attempts it is dropped, so
  one bad item can't block the queue.
- Collections, reviews and the other actions need a connection and say so.

**The sync engine** sends the outbox, then pulls `GET /me/sync` page by page into the mirror and
stores the cursor. A 410 (cursor too old) starts over with a snapshot, which replaces the mirror
but keeps downloads. A library entry's tombstone means the book is no longer visible (removed,
taken down or unshared), so its download is deleted too.

The engine runs:
- from WorkManager every 15 minutes, and once whenever a network is available after an offline
  write;
- in the app's process when the app opens, and on every live hint.

**Live hints.** While the app is in the foreground, a WebSocket to the active account's `/ws`
(signed in with the account's own HTTP client, so the token is refreshed as usual) triggers a sync
on each `changed` hint. Open screens (Home, Library, the book page) reload when the sync changes
something.
- As on the web, hints within 1.5 s of this device's own writes are echoes and are dropped, and
  bursts are coalesced.
- On reconnect the app syncs, so nothing is missed.
- When a network comes back, the socket reconnects at once instead of waiting out its backoff.

**Conflicts found offline** (§7.5) are stored. The prompt appears straight away if that book is in
the player, and otherwise the next time it is opened. Answering clears it.

**Downloads.**
- Each book downloads in a WorkManager job, shown as a data-sync foreground notification.
- Files come from `GET /books/{id}/files/{fileId}?download=true` with range requests: a partial
  `.part` file resumes where it stopped, and a plain 200 starts it over.
- Each file must match the server's SHA-256, then is synced to disk and renamed into place. A
  mismatch is retried once, then reported as damaged.
- The book's details and cover are saved alongside, so a download plays, shows chapters and has
  its cover with no connection.
- A download plays its own files even when online. The quality setting doesn't apply to it.
- Before starting, the job checks the storage limit (default 10 GB, decimal as Android displays
  sizes, or no limit) and the free space.
- Downloads wait for Wi-Fi by default; changing that setting reschedules waiting downloads.

**Storage location.** The brief says "SD card via SAF". The app instead uses its own folder on the
removable card (`getExternalFilesDirs`). That needs no permission and no folder picker, works with
plain file paths for playback and checksums, and is removed with the app. The trade-off: the files
aren't visible to other apps, which they shouldn't need to be. The location applies to new
downloads.

**Auto-download of the next in series** (decision #4, default 90 %, on by default): once a
downloaded book passes 90 %, the next unfinished book of its series is queued, marked as
automatic. It only applies to downloaded books, so people who stream don't fill their storage by
surprise.

**Notifications.** On Android 13 and later, the first "Download" asks for permission to show
notifications. Downloading works either way; only the progress notification needs it.

**Bugs found on the way.**
- The Android string generator escaped `%` even in strings without arguments, so they showed "%%".
  Such strings are now written as is.
- The Download button first sat below the secondary actions, out of sight; it now sits under Play.

**Checked on the emulator** (API 36) against the demo server:
- Downloading an M4B book and a three-file MP3 book (files checked on the device).
- Going offline (Wi-Fi, data and the server tunnel off): Home, the Library and the book page from
  the mirror; playing the downloaded book; a bookmark added offline.
- Back online: the queued position (with its offline time) and the bookmark reached the server.
- A live hint updating an open book page within about 2.5 s; after a reconnect, within 6 s.
- A conflict met while offline: the prompt appeared once the sync ran, and "Stay" was recorded.
- Auto-download of book 2 of a series at 90 % of book 1; removing a download.
- The notification permission prompt; a download with notifications declined.

Resuming a partial file, a damaged file and the storage limit are covered by unit tests (the demo
files are too small to interrupt mid-download).

---

## ADR-0043 — Android audio effects, car mode, Android Auto and the widget (Phase 3, milestone 5)

**Audio effects** (brief §7.2) run in ExoPlayer's own audio sink, as a custom `AudioProcessor`
before the speed change, so they work on every output (speaker, headphones, Bluetooth, the car).
The chain works on 16-bit PCM, in this order:
- **Voice clarity:** a 100 Hz high-pass and a +5 dB presence lift at 3 kHz (biquad filters).
- **Mono** mixdown.
- **Balance.**
- **Volume boost**, 0 to +12 dB.
- **A limiter** with a -1 dBFS ceiling, an instant attack and a 150 ms release. A boost raises
  quiet speech without ever clipping.

Other formats (float, more than two channels) pass through untouched. With every effect off the
audio is passed through bit for bit. Settings change live from the player's "Audio" panel and are
kept per device, since they suit the speaker or headphones in use.

**Car mode** is a full-screen page with five large buttons (back, play/pause, forward, add
bookmark, next chapter), the title, the chapter and the time left in it, and the screen kept on. It
opens from the player. It can also open by itself when a car's Bluetooth connects (optional, off by
default):
- While the app is open, an audio-device callback sees a Bluetooth audio device arrive.
- The device's class tells a car ("car audio" or "hands-free") from headphones. Reading the class
  needs the "nearby devices" permission on Android 12+, which is asked for only when the setting
  is turned on. Without it the feature stays off.
- Opening a screen from the background isn't allowed on recent Android, so this only works with
  the app open. With the app closed, Android Auto or the notification covers the car.

**Android Auto** (brief §7.3) uses the same Media3 session:
- **Browse tree:** Continue listening · Recently added · Downloaded · Collections · Series ·
  Authors, for the active account. Books show as a grid with their progress, using Android Auto's
  completion extras. Offline, the lists come from the mirror.
- **Search and voice:** "play <title>" searches the library and plays the best match.
- **Car buttons:** a car host gets back, forward and both chapter skips, whatever the user picked
  for the notification.
- **Covers:** the car host can't fetch signed-in URLs, so covers are served by a content provider.
  It is not exported: the service grants each cover only to the host it lists it for. Covers come
  from the download, a small cache, or the server.
- **Untested here:** the Desktop Head Unit isn't installed and the emulator image has no Android
  Auto app. The browse tree, search and cover URIs are unit-tested, and the DHU checklist stays in
  milestone 7 as planned.

**Widget** (brief §7.6): a Glance widget.
- At 2×2 it shows the cover, the title, chapter progress, and back, play/pause and forward. From
  three cells wide it uses a wider layout that adds the chapter name; it stretches to 4×2.
- Play with nothing loaded resumes the last book. Tapping the widget opens the player.
- In the e-ink theme it is black on white, with no animation.
- **Keeping it current:** the playback service publishes playing or paused and the position on
  changes and every 10 s. The widget collects that state while its session is alive, and an
  updater redraws placed widgets when the book, playback or theme changes. A first version read the
  state once before drawing and didn't follow playback; the emulator showed it.

**Checked on the emulator** (API 36): the Audio panel with every effect on during playback (no
audio errors), car mode (pause and bookmark from it), and the widget at 2×2, three cells and 4×2.
On the widget: play with nothing loaded resumed the last book, the pause icon and chapter progress
followed playback, and the e-ink look was right. The effect DSP (no clipping at +12 dB, mono,
balance, rumble cut) is unit-tested; listening tests need a real phone.

---

## ADR-0044 — Android local folders, push, the server switcher, e-ink and accessibility (Phase 3, milestone 6)

**Folders on this device** (brief §7.4 screen 9):
- **Picking:** the user picks folders with the system picker (Storage Access Framework), so no
  storage permission is needed, and read access is kept across restarts.
- **What counts as a book** (superseded by ADR-0050, which follows the server's rules): each subfolder of a picked folder is a book (one level of disc
  subfolders such as CD1 and CD2 included), and an audio file lying directly in a picked folder is a
  book of its own.
- **What's read from the files:** the files play in natural order ("Part 2" before "Part 10"). The
  title, author, durations and cover come from the files' tags (or `cover.jpg`/`folder.jpg`). The
  cover is copied into the app.
- **Stored:** the books live in a new `local_books` table (Room schema 2, migrated automatically
  from 1). Their ids are a hash of the folder's address, so progress survives rescans.
- **Rescans:** when the app comes to the front (at most once a minute), on request, and when
  Android reports a change. Not every storage provider reports changes.
- **Playback:** local books play straight from their `content://` addresses. Each file is a
  chapter; chapter marks embedded in a single m4b aren't read. Position, speed and bookmarks stay
  on the device.
- **Merged library:** local books lead the first page of the library's "All" view and have their
  own "On this device" view and Home shelf. The "on this device" badge is an icon, and screen
  readers say it.

**"Just play files on this device"** is the second onboarding path. It opens a smaller app: the
books on this device, the player, car mode, folders, playback settings and the theme. A server can
be connected from there at any time. Signing out of the last account returns to it.

**Upload to the server.** A local book's page offers "Upload to <server>" for accounts that may
upload.
- **Format:** a single m4b, m4a or mp3 goes as it is. A folder of mp3s goes as one zip, stored
  rather than compressed (mp3 doesn't shrink), with its cover. These are the formats the server
  accepts (§6.11).
- **Transport:** the server's tus endpoint, in 8 MB chunks with the file's SHA-256. After a dropped
  connection the upload resumes from what the server says it has.
- **Afterwards:** the app follows the server's import through `/me/uploads` and shows when the
  book is in the library or why it failed.
- **Runs as:** a WorkManager job with a progress notification.

**Push** (§6.14) uses UnifiedPush, through the connector library:
- **Registration:** each signed-in account registers its own endpoint (the account id is the
  UnifiedPush "instance"), so events from different servers never mix. The endpoint goes to that
  server with `POST /devices/push-subscription`, and is removed on sign-out.
- **On a message:** the app syncs that account and shows a notification: a new book (with its
  title, fetched from the API), an upload finished or failed, or, for admins, the admin events.
  Tapping a new-book notification opens that book on its account.
- **Without a distributor** (ntfy or another), as the brief describes: live `/ws` hints while the
  app is open, the 15-minute WorkManager sync otherwise. The sync engine then announces books new
  in the library itself, never for a first or full snapshot.
- **Settings → Notifications** shows which way it works, lets the user pick among several
  distributors, and explains the trade-off.
- **Libraries:** the connector brings Google's Tink library for encrypted Web Push. Tink is
  Apache-2.0 open source, so it is fine for the F-Droid build, which excludes only proprietary
  Google libraries.

**Servers.** Home shows which server it is ("On Demo shelf"); tapping it switches account, adds a
server, or opens the account list.

**QR code sign-in** (the brief's onboarding "URL or QR code", missing from milestone 1):
- "Scan a QR code" on the server screen reads the address with CameraX and the open-source ZXing
  decoder, so the F-Droid build has it too. The camera permission is asked only when scanning.
- The web app's Settings now shows the server's address as a QR code under "Connect the Android
  app".

**E-ink pass:**
- Dialogs no longer dim the page grey. On e-ink they have a black outline on a white page.
- The player's buffering spinner (an animation) is hidden.
- The big car-mode buttons get outlines, since their tonal fill is white on e-ink.
- An optional "Refresh the screen" button sits in every screen title on e-ink. It flashes the
  screen black, then white, to clear ghosting.

**Accessibility pass:**
- A script dumps each screen's accessibility tree and lists any tappable element with no text or
  label inside it. Home, Library, a book page, Downloads, Stats, Settings, the player, the Audio
  panel and car mode had none.
- At 200 % text:
  - The five tabs show icons only, still named for TalkBack (from 150 %; their labels broke
    mid-word). On e-ink a bar marks the selected tab.
  - Shelf cards widen with the text size (up to 1.6×).
  - Car-mode labels wrap between words.
- A full TalkBack walk-through and other phones are left to milestone 7's UI tests and Jacob's
  week of daily use.

**Bugs found on the way:**
- **Server, wrong file order:** a zip of loose mp3s imported its files in name order ("Part 1,
  Part 10, Part 2"). The loose files are now merged in natural order (test added).
- **Stale mini player:** signing out left that account's book in the mini player; it is now
  unloaded.
- **Bookmarks on local books:** local book ids aren't UUIDs, which would have broken bookmarks; they
  get a stable UUID derived from their id.

**Checked on the emulator** (API 36):
- Picking a folder, finding two books, the merged library, playing a local book (with chapters in
  natural order), and local-only mode after signing out.
- Uploading a three-file mp3 book: imported as one book.
- The fallback "New in your library" notification: arrived within 10 s of an upload from another
  device, and tapping it opened the book.
- The notifications screen, the account switcher, and the QR scanner opening the camera.
- The e-ink dialogs, the screen refresh, the 200 % text layouts, and the label check.

Not checked here: a real UnifiedPush distributor (none on the emulator; registration is the
connector's documented flow), and a QR code through a real camera (the decoder is unit-tested with
a generated code).

---

## ADR-0045 — Android release signing, F-Droid checks and the Android Auto checklist (Phase 3, milestone 7)

**Release signing.** Each of the three flavors is signed its own way (`docs/ANDROID-RELEASE.md`):
- **`github`:** signed with the release key. Sideloaded updates install only over the same
  key, so losing it would strand every sideloaded install.
- **`play`:** signed with a separate upload key when one is given, otherwise the release key.
  Google Play re-signs it with the key it keeps (Play App Signing), and a lost upload key can be
  reset; a lost release key can't.
- **`fdroid`:** never signed here. F-Droid builds it from source and signs it itself.

The keys come from environment variables or `~/.gradle/gradle.properties`
(`SIMPLEAPB_KEYSTORE`, `SIMPLEAPB_PLAY_KEYSTORE`, with their passwords and aliases), never from
the repository. Without them, release builds stay unsigned, as before. A new workflow builds the
signed APK and bundle, plus the unsigned fdroid APK, on a `v*` tag, from keystores held in Actions
secrets. It verifies the signatures and keeps the files as run artifacts. Publishing a GitHub
release with every platform's files belongs to Phase 6.

**F-Droid checks** (brief §7.1, §14):
- **No proprietary libraries:** `:app:checkFdroid` fails if the fdroid release build depends on,
  or has classes from, Google Play Services, Firebase, Play Core, ML Kit, the ads SDK or
  Crashlytics. It runs on every push. It was checked by banning a library the app does use: both
  the dependency check and the APK scan failed as they should.
- **Same APK wherever it's built:** release builds no longer embed the git commit
  (`vcsInfo.include = false`), so a build from a source tarball matches one from a checkout. Two
  clean builds here gave byte-identical APKs. A nightly job builds the fdroid APK in two separate
  checkouts and compares them, and builds the signed outputs with a throwaway key to keep the
  signing path working.
- The F-Droid metadata and the inclusion request stay in Phase 6, as planned.

**Android Auto.** `docs/ANDROID-AUTO-CHECKLIST.md` lists the Desktop Head Unit checks the phase
must pass: browsing, playing, voice, offline and errors, interruptions, and the phone while
driving. It also says how to set up the DHU with a phone. The checks need a phone with the
Android Auto app, so Jacob runs them; they weren't run here.

**On-device UI tests: skipped, on Jacob's decision.** The plan included instrumented tests for
the playback service and Compose UI tests for e-ink, 200 % text and TalkBack labels (brief §15).
They were started against a recorded fake server, but Jacob chose to skip them: the app works
now, and users can report UI problems. Logged as Q12. The milestone 6 accessibility pass and the
unit tests stay as the cover for now.

---

## ADR-0046 — Casting and Wear OS dropped from scope

Jacob dropped two parts of the brief on 2026-11-11: **casting** (Chromecast, DLNA/UPnP, Sonos,
§7.7) and the **Wear OS app** (remote, standalone playback and tile, §8). Phase 5 is now the book
tools only: the chapter editor with silence detection, the mp3 → m4b merge, the cover picker and
the duplicate finder. It is accepted when chapter edits survive a re-scan.

What changes:
- The empty `android/cast` and `android/wear` placeholders are removed, along with the unused
  `CAST` build flag.
- The `play` and `github` flavors now differ only in the in-app update check, and no flavor links
  a proprietary Google library. The F-Droid checks (ADR-0045) still guard the fdroid build.
- The player gets no cast button.

What stays, because it's already built, tested and harmless:
- The signed stream URLs (`POST /books/{id}/stream-url`, ADR-0016). They're unused by the apps
  for now; an external player could use them.
- The `wear` platform value and its version-guard entry in the API.
- Android Auto, which was never part of either.

The AGPL linking exception for the Google Cast SDK and Play Services (`LICENSE-EXCEPTION.md`) now
covers nothing the apps use. Whether to keep it for later or remove it is Q13.

---

## ADR-0047 — The Windows app (Phase 4)

**One app, served by a native layer.** The Windows app is Tauri 2 (brief §10) showing the same
web app, built with `npm run build:desktop`. That build turns on the desktop features, which are
dead code in the server's build. The window loads `sapb://localhost/` (WebView2 sees
`http://sapb.localhost`), and a Rust handler answers every request:
- **The web app's files**, built into the exe, with `index.html` for every route and a strict
  Content Security Policy.
- **`/api/…`**, passed to the user's server.
- **`/desktop/…`**, the app's own endpoints: the server address, downloads and local files, the
  player state, the update check.

The web app stays same-origin, so its relative URLs (covers, uploads, the audit export) work
unchanged. The server needs no CORS, and the page needs no Tauri JavaScript API or IPC
permissions at all. Writes and `/desktop/…` calls from any other origin are refused, and
navigation outside the app opens the browser.

**Signing in as a token client.** The web app posts its usual login. The Rust side replaces the
device info with the desktop's (platform `windows`, named after the computer), keeps the token
pair in Windows Credential Manager, and gives the web app the answer without the tokens. Every
API request carries the access token. A 401 for an expired token refreshes the pair once and
retries, and parallel requests share that one refresh. The web app's own refresh call is
answered from the stored pair, so its existing sign-out handling works as it is. There's no
default server: the first screen asks for the address and checks it answers as Simple APB.

**Offline** (the phase's acceptance test):
- **Cache:** the last good answer to each GET (JSON and covers, up to 200 MB in all) is kept on
  disk, so Home, the library and book pages open with the server down. The sign-in answer is
  kept as `/me`, so the app starts signed in offline.
- **Outbox:** a progress write that can't reach the server waits on disk, the newest per book.
  It's sent within 30 s of the server answering again, with its original time and
  `last_synced_at`. A write the server refuses as a conflict is dropped: the player's next write
  meets the same conflict and the usual prompt asks the user.
- **Downloads:** books are saved in `<folder>\<server>-<user>\<book>\`; the default folder is
  Music\Simple APB, and Settings or the Downloads page changes it. Downloads resume after an
  interruption, and every file must match the server's SHA-256. `book.json` is written last: a
  book counts as downloaded exactly when it's there.
- **Playing a downloaded book:** its stream-link request is answered with its own files, online
  or not, and the player always uses the original files for it.

**Windows integration.**
- **Media controls:** the app drives Windows' media controls itself through the souvlaki crate,
  and WebView2's own media-key handling is turned off so there's only one session. The volume
  flyout shows the cover, title and author, and the keyboard's media keys and the flyout buttons
  control the book.
- **Tray:** play/pause, back, forward, show, the mini player and quit. Closing the window hides
  it to the tray and the book keeps playing.
- **Mini player:** a small always-on-top window with the cover and the controls. The web app's
  player in the main window stays the only player: it posts its state to the native side, and
  the tray, media controls and mini player send commands back.
- **Live updates:** the app has no `/ws` socket to its server. It refreshes when the window comes
  back to the front and every two minutes instead.

**Uploads.** The existing tus client runs through the proxy, which passes the tus headers both
ways. Files and folders dropped anywhere on the window go to the Uploads page; the web app's
Uploads page now takes dropped folders too. A folder with .m4b/.m4a files sends those. A folder
of mp3 files, including CD1/CD2 subfolders, is packed in the page into one zip, stored rather
than compressed, with its cover. That's the format the server reads as one book.

**Updates and the installer.** The app compares its version with the newest GitHub release and
offers the download page, with "Skip this version". It installs nothing by itself. The Tauri
updater would need an update-signing key that only Jacob can create, so automatic updates come
with the release work in Phase 6. The installer is NSIS, installed per user with no admin
rights. It isn't code-signed, so Windows SmartScreen warns on first run until the app has a
reputation or a signing certificate (brief §10). CI builds the installer on every push, after
`cargo fmt`, `clippy -D warnings` and the unit tests.

**Checked here**, against the demo server (Windows 11, driven through WebView2's debugging port):
- Connecting, signing in (the session is listed as "Windows (…)"), Home with covers, and
  downloading a book (checksum verified).
- **Server stopped:** the app restarted, opened Home from the cache, and played the downloaded
  book. Its position was queued, then reached the server within 30 s of the server starting
  again. This is the phase's acceptance test.
- Windows' media session showing the title, author, cover and playing state; the Play/Pause
  media key pausing the book; closing the window keeping it playing; the mini player's buttons.
- An mp3 uploaded through the app imported. The zip writer's output read back with Python's
  zipfile, checksums intact.
- **Not checked here:** clicking the tray menu (it calls the same commands as the media keys),
  and dropping a real folder from Explorer onto the window. The drop code was tested only through
  the zip writer.

---

## ADR-0048 — The book tools (Phase 5)

Phase 5 is the book tools only, since casting and Wear OS were dropped (ADR-0046). They're in the
web app, so they work in the Windows app too. The new API is in `openapi.yaml` (`/books/{id}/chapters`,
`…/chapters/detect`, `…/merge`, `…/cover/upload`, `…/cover/embedded`, `/admin/duplicates…`).

**Who may use them.** `authz.UseBookTools`: admins on any book, uploaders on their own books if
they may edit metadata (the same rule as editing details, so the book page's `can_edit` covers
it). Merging while keeping the original adds a new book, so `authz.MergeAsNewBook` also needs
upload permission. The duplicate finder is admin-only. A "Book tools" page
(`/books/{id}/tools`) holds the chapter editor, the merge and the cover picker. The duplicate
review is a new section of the admin panel.

**One lock for files.** Scans and the tools' file changes take the same library lock, so a scan
never sees half a change. Every file change is a job that can run again after a crash and
finish the same change.

**Chapters** (the phase's acceptance test: edits survive a re-scan):
- **Saving:** the list (title and start; each chapter ends where the next starts) is saved at
  once, and the book is marked with a `chapters` lock in `locked_fields`. Until now that column
  held only metadata fields, and a helper silently dropped any other name. It now keeps them.
- **m4b and m4a books:** a job also writes the chapters into the file. It remuxes into a temp
  file next to the original (no re-encode), fsyncs it, renames it over the original, then
  records the new checksum, so the weekly scrub doesn't flag the change.
- **mp3 books:** the files can't hold chapters. A re-scan keeps the locked chapters, fitted to
  the book's new length.
- **Previews** play ten seconds from a chapter start through a signed stream link, without
  touching the user's progress.

**Silence detection** runs `ffmpeg silencedetect` over each file as a job, with the threshold
and minimum length the user sets. A chapter starts at every file and half a second before the
end of each silence inside a file. Breaks less than 10 s apart are dropped. The result is only a
proposal in the job's result: nothing changes until it's saved.

**Merging into one m4b.** The files are concatenated in order, with the book's chapters, tags and
cover, into a work folder in the cache.
- **Audio:** copied as it is when every file has the same codec (AAC, MP3 or ALAC), sample rate
  and channels, "keeping the codec when possible". Otherwise, or when the user asks, it's
  converted to AAC at 64 kbit/s per channel.
- **Replace (the default):** the m4b is copied crash-safely into the book's folder. A note
  listing the old files is written, the database switches to the new file, and the old files
  (and empty disc folders) are deleted. If the server stops in between, the job's re-run
  finishes the deletion, and scans skip a folder while its note is there. Progress stays
  valid: the timeline is the same concatenation.
- **Keep the original:** the m4b becomes a new book in its own folder, with the same uploader
  (and in their library). The duplicate finder will then list the pair.

**Covers.** The picker offers three sources:
- **The metadata providers**, through the existing candidates search.
- **An uploaded image:** JPEG or PNG up to 10 MB, through the same checks as provider covers.
- **The audio's embedded cover.**

**Duplicates.**
- **Finding them:** a weekly job (and a "look now" button). Exact pairs have the same whole-book
  checksum. Probable pairs have the same normalized title (lower case, no punctuation, leading
  article or "(Unabridged)"-style aside) and first author, and durations within 2 %. A pair
  that was decided is never raised again.
- **Keep both** closes the pair.
- **Keep this one:** the other book's data moves to the kept book where the kept book doesn't
  already have it: library entries, active distributions, collection places, reviews,
  recommendations, bookmarks, listening history and upload records. When both books have
  progress, the newer wins. The other book then goes to the Trash, where it can still be
  restored for 30 days. Takedown and review-queue history stay with it.

**Tests.** Go tests cover each piece:
- The acceptance test: an m4b's edited chapters are in the file and come back from a forced
  re-import even unlocked, and an mp3 book's edits survive a re-scan that adds a file.
- A merge in place, with checksum, scan and scrub checks, and a re-run finishing a merge
  interrupted after the database switch.
- A merge that keeps the original, re-encoded.
- Silence detection; the chapter, silence and duplicate rules.
- Moving user data in a duplicate merge.
- The whole API flow, including the permission rules and the cover upload.

Also checked in a browser against the demo server: chapters written into an m4b, detection,
merging as a new book, and the duplicate review.

---

## ADR-0049 — Releases and the update path (part of Phase 6, brought forward)

Jacob wants to run everything now and keep it updated as new versions come. So this part of
Phase 6 (brief §6.21, §14) comes first.

**One version.** The server, web app, Android app and Windows app share one semver.
`scripts/release.mjs X.Y.Z` writes it into every place it lives:
- `package.json` and the lock files;
- the Windows app's `Cargo.toml`, `Cargo.lock` and `tauri.conf.json`;
- Android's `versionName`, and its `versionCode` = major·10000 + minor·100 + patch (minus one for
  a pre-release, so it installs below its release).

It also dates the changelog. The server gets its version from the tag at build time. The
release workflow checks the tag against the code before publishing anything.

**The release workflow** (on `vX.Y.Z`; it replaces the Android-only one from ADR-0045):
- the server image, for amd64 and arm64, to GHCR as `X.Y.Z`, `X.Y` and `latest`, labelled with its
  version;
- the signed Android APK and Play bundle, skipped with a warning when the keys aren't set;
- the Windows installer;
- a GitHub release with those files and the changelog section. A tag with a suffix is a
  pre-release, which servers and apps never offer.

**Server updates.**
- **The check:** the server looks at the newest GitHub release daily (`update_check` job; "Check
  now" on Admin → Updates). `SIMPLEAPB_RELEASES_URL` points it elsewhere, or `off`. A build from
  source reports `dev` and is never offered an update.
- **"Update now"** runs only with the optional updater container (`--profile updater`), which runs
  `deploy/updater/updater.sh` on the official Docker CLI image.
- **The handshake:** the server and the updater share `config/updater`. The updater touches a
  heartbeat there, so the server knows it runs. The server takes a database backup, then writes
  `request.json`. The updater:
  - pins the version in `.env`, pulls, and recreates only the server;
  - waits for its healthcheck;
  - writes `result.json`: succeeded; rolled back to the version the running image's label names
    (a locally tagged copy of the image for builds without a label); or failed.
- **Why rolling back is safe:** a failed migration rolls itself back, and migrations only add
  things, so the previous version starts on the database as it is.
- **Paths:** the updater mounts the deploy folder at the same absolute path
  (`SIMPLEAPB_DEPLOY_DIR`), so compose resolves the relative volume paths to the same host
  folders as when the user runs it.
- **What it can reach:** it has the Docker socket (root-equivalent, documented) and no network.
- **Tested in CI:** a job runs the real compose file and script against a local registry with
  stand-in images. One update succeeds; one whose server exits on start is rolled back, with its
  last log lines in the result.

**Manual path.** The same thing by hand is two commands, shown on the Updates page when there's
no updater: set the version in `.env`, then `pull` and `up -d`. Release images are now the
default in `deploy/docker-compose.yml`, and building from source moved to
`docker-compose.build.yml`.

**Apps.**
- **Android (GitHub build only):** the app looks at the newest release at most every 12 hours.
  If its `simple-apb-android-X.Y.Z.apk` is newer, Home offers Download or Not now, and Settings →
  App updates can check by hand. The download opens in the browser and installs over the app,
  since both are signed with the release key. Only `https://github.com/` downloads are offered.
- **Windows:** the app's existing check (ADR-0047) uses the same releases.
- **Version guard:** `min_client_version` stays at 0.0.0 until a release needs it raised.

**Not done here:**
- the Tauri updater's silent installs (they need an update-signing key);
- a configurable version guard;
- Play and F-Droid listings.

These stay in Phase 6.

---

## ADR-0050 — A preset Android app for one server, and folders split like the server

**Preset build.** Jacob wants an app his listeners can install and sign in to with no setup.
The repository may never name an instance (CLAUDE.md), so the server is baked in at build time:
- **Where it comes from:** `SIMPLEAPB_PRESET_SERVER` (an environment variable or Gradle
  property). The build accepts only a plain http(s) address and fails otherwise. It becomes
  `BuildConfig.PRESET_SERVER`, which is empty in every ordinary build.
- **In releases:** the release workflow builds the github flavor a second time with the
  `SIMPLEAPB_PRESET_SERVER` secret and attaches `simple-apb-android-X.Y.Z-preset.apk`. Without
  the secret there's no preset APK.
- **Public:** Jacob chose the public GitHub release, so the APK, and the server name inside it,
  is public. The code stays generic.

**What the preset app does differently:**
- It opens straight to the server's sign-in (and "Request an account" when sign-ups are open),
  with no welcome or server screen. When the server can't be reached, it says so, with a retry.
  A plain-http preset is accepted without the warning, since the builder chose it.
- There's no "Add a server", in the account switcher or under Accounts, and no files-only mode.
  Signing out returns to that sign-in. Folders on this device still work while signed in.
- It has the same package and signing key as the regular GitHub APK, so either installs over the
  other. Its update check takes the release's `-preset.apk`, and the regular one never does.
- **Checked on the emulator** with a preset pointing at the demo server:
  - first start opened on the sign-in;
  - the account switcher offered no "Add a server";
  - signing out came back to the sign-in;
  - with the server unreachable, the retry message showed, and retrying worked once it was back.

**Folders on this device now split like the server** (correcting ADR-0044's "each subfolder is
a book"). A folder of several m4b files, such as a series, was shown as one long book. Android
now follows the server's rules (`detect.go`, ADR-0015), in `BookLayout`, at any depth below a
picked folder:
- a folder with mp3s, or with one audio file, is one book;
- a folder with several m4b/m4a files holds one book per file;
- a folder whose only content is disc folders (CD1, Disc 2, Part 3) is one book.

Unit tests cover the series folder, nested Author/Series/Book folders, disc sets, and
non-disc subfolders (books of their own).

## Open questions for Jacob

| # | Question | Current choice (until answered) |
|---|---|---|
| Q1 | ~~GitHub owner~~ **Answered:** `quip1` — `github.com/quip1/simple-apb`, image `ghcr.io/quip1/simple-apb-server` | — |
| Q2 | License identifier: `AGPL-3.0-or-later` or `AGPL-3.0-only`? | `AGPL-3.0-or-later` (GNU's recommendation) in SPDX headers |
| Q3 | ~~Gradle wrapper~~ **Done:** committed with the Android app (Gradle 9.9.0, checksum-pinned; ADR-0039) | — |
| Q4 | ~~Review the schema and API outline~~ **Approved** (Phase 0 accepted) | — |
| Q8 | ~~Web framework (brief #8)~~ **Answered: Svelte** (ADR-0026) | — |
| Q9 | ~~Accent colour and icon (brief #9)~~ **Answered: Fern accent, Open book icon** (ADR-0038) | — |
| Q10 | Copyright/takedown contact in the footer (brief #3) | Yes, admin-editable (proposed default) |
| Q13 | Casting is dropped (ADR-0046), so no app links the Google Cast SDK or Play Services. Keep the AGPL §7 linking exception (`LICENSE-EXCEPTION.md`) for later use, or remove it so the license is plain AGPL? | Keep for now (harmless) |
| Q12 | Brief §15's instrumented playback-service tests and Compose UI tests (e-ink, 200 % font, TalkBack labels) were skipped in Phase 3 milestone 7 (ADR-0045). Add them later, before v1.0? | Skipped for now (Jacob, 2026-11-11) |
| Q11 | Brief §7.4 "open EPUB if attached": the API has `has_epub` but no endpoint to fetch the EPUB. Add `GET /books/{id}/epub` (and a way to attach one) in Phase 5 with the book tools? | Yes, Phase 5 (proposed) |
| Q5 | ~~TOTP replay protection~~ **Answered: yes** (ADR-0027) | — |
| Q7 | ~~AAC-LC instead of HE-AAC~~ **Answered: AAC-LC is fine** (ADR-0018) | — |
| Q6 | ~~Setup wizard page in Phase 2~~ **Answered: yes** — shipped in Phase 2 milestone 1 | — |
