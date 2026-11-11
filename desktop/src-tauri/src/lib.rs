// SPDX-License-Identifier: AGPL-3.0-or-later
//! Simple APB for Windows (brief §10, ADR-0047): the web app in a Tauri
//! window, with offline downloads, Windows media controls, a tray icon, a
//! mini player and drag-and-drop upload.

mod auth;
mod config;
mod desktop;
mod downloads;
mod i18n;
mod offline;
mod player;
mod proxy;
mod update;

use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder, WindowEvent};

use config::{Config, Tokens};
use offline::{Cache, Outbox};

/// Tauri's default WebView2 flags, plus no built-in media-key handling:
/// this app drives Windows' media controls itself (player.rs).
pub const BROWSER_ARGS: &str =
    "--disable-features=msWebOOUI,msPdfOOUI,msSmartScreenProtection,HardwareMediaKeyHandling";

pub struct Shared {
    pub cfg: Mutex<Config>,
    pub config_dir: PathBuf,
    pub default_download_dir: PathBuf,
    pub http: reqwest::Client,
    pub tokens: tokio::sync::Mutex<Option<Tokens>>,
    pub online: AtomicBool,
    pub cache: Cache,
    pub outbox: Outbox,
    pub downloads: downloads::Downloads,
    pub player: Mutex<player::PlayerState>,
    flushing: AtomicBool,
}

impl Shared {
    pub fn server(&self) -> Option<String> {
        self.cfg.lock().unwrap().server_url.clone()
    }

    pub fn save_config(&self) {
        let cfg = self.cfg.lock().unwrap().clone();
        if let Err(e) = config::save(&self.config_dir, &cfg) {
            log::warn!("could not save settings: {e}");
        }
    }
}

/// Sends the progress written offline, oldest book first. A write the server
/// refuses (a conflict, a removed book) is dropped; offline stops the round.
pub fn flush_outbox_soon(s: Arc<Shared>) {
    if s.flushing.swap(true, Ordering::AcqRel) {
        return;
    }
    tauri::async_runtime::spawn(async move {
        for p in s.outbox.snapshot() {
            let body = serde_json::to_vec(&p.body).unwrap_or_default();
            let headers = vec![("content-type".to_string(), "application/json".to_string())];
            match proxy::send(&s, "PUT", &p.path, &headers, body).await {
                Ok(r) => {
                    if r.status() == reqwest::StatusCode::CONFLICT {
                        log::info!(
                            "an offline position for {} conflicts; the player will ask",
                            p.path
                        );
                    }
                    if r.status().is_server_error() {
                        break;
                    }
                    s.outbox.done(&p);
                }
                Err(_) => break,
            }
        }
        s.flushing.store(false, Ordering::Release);
    });
}

/// Pages stay inside the app; web links open in the browser.
pub fn allow_navigation(u: &url::Url) -> bool {
    let inside =
        u.scheme() == "sapb" || (u.scheme() == "http" && u.host_str() == Some("sapb.localhost"));
    if !inside && matches!(u.scheme(), "http" | "https") {
        let _ = tauri_plugin_opener::open_url(u.as_str(), None::<&str>);
    }
    inside
}

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            player::show_main(app)
        }))
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .register_asynchronous_uri_scheme_protocol("sapb", |ctx, request, responder| {
            let app = ctx.app_handle().clone();
            tauri::async_runtime::spawn(async move {
                let s = app.state::<Arc<Shared>>().inner().clone();
                responder.respond(proxy::handle(&app, &s, request).await);
            });
        })
        .setup(|app| {
            let handle = app.handle().clone();
            let config_dir = app.path().app_config_dir()?;
            let cache_dir = app.path().app_cache_dir()?;
            offline::ensure_dir(&config_dir);
            let cfg = config::load(&config_dir);
            let default_download_dir = app
                .path()
                .audio_dir()
                .or_else(|_| app.path().document_dir())
                .unwrap_or_else(|_| config_dir.clone())
                .join("Simple APB");
            let tokens = cfg.server_url.as_deref().and_then(config::tokens);
            let http = reqwest::Client::builder()
                .connect_timeout(Duration::from_secs(8))
                .read_timeout(Duration::from_secs(30))
                .user_agent(concat!("SimpleAPB-Windows/", env!("CARGO_PKG_VERSION")))
                .build()?;
            let shared = Arc::new(Shared {
                cfg: Mutex::new(cfg),
                default_download_dir,
                http,
                tokens: tokio::sync::Mutex::new(tokens),
                online: AtomicBool::new(true),
                cache: Cache::new(cache_dir.join("api")),
                outbox: Outbox::load(config_dir.join("outbox.json")),
                downloads: downloads::Downloads::default(),
                player: Mutex::new(player::PlayerState::default()),
                flushing: AtomicBool::new(false),
                config_dir,
            });
            app.manage(shared.clone());
            app.manage(player::Quitting::default());
            downloads::rescan(&shared);
            shared.cache.trim();

            let url = WebviewUrl::CustomProtocol("sapb://localhost/".parse()?);
            let main = WebviewWindowBuilder::new(app, "main", url)
                .title("Simple APB")
                .inner_size(1200.0, 820.0)
                .min_inner_size(420.0, 520.0)
                .additional_browser_args(BROWSER_ARGS)
                // Files dropped on the window reach the web app's drop zone.
                .disable_drag_drop_handler()
                .on_navigation(allow_navigation)
                .build()?;
            // Closing the window keeps playing in the tray; Quit (tray) exits.
            let h = handle.clone();
            main.on_window_event(move |e| {
                if let WindowEvent::CloseRequested { api, .. } = e {
                    if !h.state::<player::Quitting>().0.load(Ordering::Relaxed) {
                        api.prevent_close();
                        if let Some(w) = h.get_webview_window("main") {
                            let _ = w.hide();
                        }
                    }
                }
            });
            player::build_tray(&handle)?;
            #[cfg(windows)]
            player::attach_smtc(&handle, &shared);

            tauri::async_runtime::spawn(downloads::worker(shared.clone()));
            // Offline writes go out when the server is back.
            let s = shared.clone();
            tauri::async_runtime::spawn(async move {
                loop {
                    tokio::time::sleep(Duration::from_secs(30)).await;
                    if !s.outbox.is_empty() {
                        flush_outbox_soon(s.clone());
                    }
                }
            });
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("Simple APB failed to start");
}
