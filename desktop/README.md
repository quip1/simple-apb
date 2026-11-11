# desktop — Simple APB for Windows

Tauri 2 showing the `web/` app, with offline downloads, Windows media controls, a tray icon, a
mini player and drag-and-drop upload. The design is in ADR-0047 (`docs/DECISIONS.md`).

## Build

Needs Node, Rust (stable, MSVC) and the Visual Studio C++ build tools. WebView2 ships with
Windows 10 and 11.

```bash
cd desktop
npm install
npm run build        # builds web/ in desktop mode, then the app and the NSIS installer
```

The installer lands in `src-tauri/target/release/bundle/nsis/`. For a quick debug build:
`npm --prefix ../web run build:desktop`, then `cargo build` in `src-tauri`.

## Layout (`src-tauri/src`)

| File | What |
|---|---|
| `proxy.rs` | The `sapb://` handler: the web app's files, `/api` to the server, `/desktop` routes |
| `auth.rs` | Sign-in as a token client; refresh; sign-out |
| `offline.rs` | The response cache and the progress outbox |
| `downloads.rs` | Book downloads (resume, SHA-256) and serving local files |
| `player.rs` | Windows media controls, the tray menu, the mini player |
| `desktop.rs` | The `/desktop/…` endpoints the web app calls |
| `config.rs` | `config.json` and the tokens in Credential Manager |
| `update.rs` | The GitHub release check |
| `i18n.rs` | Native strings from `i18n/en.json` |

## Testing against a server

WebView2 honours `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9333`, so a debug
build can be driven with Playwright's `connectOverCDP`.
