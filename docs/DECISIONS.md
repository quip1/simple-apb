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

## Open questions for Jacob

| # | Question | Current choice (until answered) |
|---|---|---|
| Q1 | ~~GitHub owner~~ **Answered:** `quip1` — `github.com/quip1/simple-apb`, image `ghcr.io/quip1/simple-apb-server` | — |
| Q2 | License identifier: `AGPL-3.0-or-later` or `AGPL-3.0-only`? | `AGPL-3.0-or-later` (GNU's recommendation) in SPDX headers |
| Q3 | Commit the Gradle wrapper now, or when the Android app starts (Phase 3)? | CI installs Gradle 9.1 directly; wrapper added in Phase 3 |
| Q4 | ~~Review the schema and API outline~~ **Approved** (Phase 0 accepted) | — |
| Q5 | TOTP replay protection (reject a code already used in its 30 s window)? Adds a column per user. | Not yet; rate limits + lockout bound guessing |
| Q7 | `low` HLS is AAC-LC 32 kbps instead of HE-AAC (HE-AAC needs non-free libfdk_aac; see ADR-0018). OK? | AAC-LC |
| Q6 | Brief says the wizard is "served at `/setup`". Phase 1 ships the wizard **API**; the wizard **page** comes with the web SPA in Phase 2 (until then: curl, see `docs/SETUP-API.md`). OK? | As described |
