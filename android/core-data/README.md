# core-data (Phase 3)

Accounts and tokens, the authenticated API client, and the offline layer (ADR-0042):

- `db`: the Room mirror (`mirror.db`; schema in `schemas/`).
- `sync`: `LocalLibrary` (mirror and outbox), `SyncEngine` + `SyncWorker`, `LiveHints` (`/ws`).
- `download`: `Downloads` (settings, queue, storage) and `DownloadWorker` (range requests, SHA-256).

Depends on `:core-api` for the generated OpenAPI client.
