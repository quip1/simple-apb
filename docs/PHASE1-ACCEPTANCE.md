# Phase 1 acceptance

Brief §16, Phase 1: *"Accept when: with curl / the generated client you can set up an instance,
approve a user, upload a book, distribute it to a group, stream it at both qualities, sync progress
from two 'devices' (including a > 3 min conflict returning 409), and survive the SIGKILL harness."*

Every criterion is covered by an end-to-end test that drives the real HTTP API, on **SQLite and
Postgres**, in CI on every push.

| Criterion | Proven by | What it does |
|---|---|---|
| Set up an instance | `internal/api/server_test.go` → `TestSetupMode` | setup mode gating, wizard with a real TOTP code, `config.json` written, switch to normal mode |
| Approve a user | `TestAccountFlow` | sign-up → pending login refused → admin approves → login, refresh rotation, replay revokes, ban |
| Upload a book | `internal/api/library_test.go` → `TestLibraryFlow` | real tus upload → background import → book in the uploader's library, quota counted |
| Distribute to a group | `TestLibraryFlow` | scan → master store → distribute to "Friends" → visible to members only |
| Stream at both qualities | `internal/api/media_test.go` → `TestMediaFlow` | original file with Range (checksum-verified), HLS `low` and `medium`, signed cast URLs |
| Sync from two devices, 409 | `internal/api/sync_test.go` → `TestSyncFlow` | two device logins; small difference resolves silently; > 3 min offline difference → 409 with both positions; user's resolution final; stale write ignored |
| Survive SIGKILL | `cmd/simpleapb/crash_test.go` → `TestCrashHarness` | real binary killed mid-write N times; DB integrity, file checksums, backups, acknowledged writes, upload resume (nightly in CI, 40 kills) |

## Run it yourself

```bash
cd server && go test ./...
```

```bash
cd server && SIMPLEAPB_CRASH_TEST=1 SIMPLEAPB_CRASH_ITERATIONS=20 go test -v -run TestCrashHarness ./cmd/simpleapb
```

To include Postgres, point `SIMPLEAPB_TEST_POSTGRES_DSN` at an empty database (each test uses its
own schema) and, for the backup test, put `pg_dump` (same major version) on `PATH` or in
`SIMPLEAPB_PG_DUMP`.

## By hand with Docker

```bash
cp deploy/.env.example deploy/.env
```

Edit `deploy/.env` (at least `SIMPLEAPB_LIBRARY` and `NTFY_PUBLIC_URL`), then:

```bash
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
```

```bash
docker compose -f deploy/docker-compose.yml logs simpleapb | grep setup_token
```

Then follow `docs/SETUP-API.md` for the wizard and admin login. The API reference is
`docs/API.md`; uploads use any tus client (e.g. `tusc`, or tus-js-client) against `/api/v1/uploads/`.
