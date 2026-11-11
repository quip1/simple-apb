# Simple APB

A simple, self-hostable, local-first audiobook platform: a Go server that holds
your library and syncs progress, plus players for Android (phone and Android
Auto), the web and Windows.

> **Status:** in development. The server, web app and Android app are built; the Windows app,
> the book tools and the first release are next. See `CHANGELOG.md`.

## Repository layout

| Path | What |
|---|---|
| `server/` | Go server: API, sync, streaming, scanner, admin. `server/openapi.yaml` is the API source of truth. |
| `web/` | TypeScript SPA (web player + admin panel), served by the server |
| `desktop/` | Windows app (Tauri wrapping `web/`) |
| `android/` | Android phone app and shared modules (Gradle) |
| `deploy/` | Docker Compose and deployment guides |
| `docs/` | Architecture, decisions log, API reference, self-hosting |
| `i18n/` | Shared translation strings |

## Development

```bash
# Server
cd server && go generate ./... && go test ./...

# TypeScript client
cd web && npm ci && npm run gen:api && npm run typecheck

# Kotlin client (needs JDK 21)
cd android && ./gradlew :core-api:build
```

See `docs/ARCHITECTURE.md` and `docs/DECISIONS.md`.

## License

AGPL-3.0 (`LICENSE`), with an additional permission under AGPL §7 that lets
the Play/GitHub Android builds link the Google Cast SDK — see
`LICENSE-EXCEPTION.md`. The F-Droid build contains no Google libraries.
