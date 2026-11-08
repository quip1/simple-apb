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

## Open questions for Jacob

| # | Question | Current choice (until answered) |
|---|---|---|
| Q1 | ~~GitHub owner~~ **Answered:** `quip1` — `github.com/quip1/simple-apb`, image `ghcr.io/quip1/simple-apb-server` | — |
| Q2 | License identifier: `AGPL-3.0-or-later` or `AGPL-3.0-only`? | `AGPL-3.0-or-later` (GNU's recommendation) in SPDX headers |
| Q3 | Commit the Gradle wrapper now, or when the Android app starts (Phase 3)? | CI installs Gradle 9.1 directly; wrapper added in Phase 3 |
| Q4 | ~~Review the schema and API outline~~ **Approved** (Phase 0 accepted) | — |
| Q5 | TOTP replay protection (reject a code already used in its 30 s window)? Adds a column per user. | Not yet; rate limits + lockout bound guessing |
| Q6 | Brief says the wizard is "served at `/setup`". Phase 1 ships the wizard **API**; the wizard **page** comes with the web SPA in Phase 2 (until then: curl, see `docs/SETUP-API.md`). OK? | As described |
