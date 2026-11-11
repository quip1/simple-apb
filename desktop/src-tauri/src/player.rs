// SPDX-License-Identifier: AGPL-3.0-or-later
//! The player outside the window (brief §10): Windows media controls (SMTC,
//! with the cover in the volume flyout, and the keyboard's media keys), the
//! tray menu and the mini player. The web app's player stays the one source
//! of truth: it posts its state here, and these send commands back to it.

use serde_json::{json, Value};
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};

use crate::i18n::t;
use crate::Shared;

#[derive(Default)]
pub struct PlayerState {
    pub state: Value,
    cover_key: String,
    #[cfg(windows)]
    smtc: Option<souvlaki::MediaControls>,
}

pub struct Tray {
    pub toggle: MenuItem<tauri::Wry>,
}

/// Sends a command to the web app's player in the main window.
pub fn send_command(app: &AppHandle, action: &str) {
    if action == "show" {
        show_main(app);
        return;
    }
    let allowed = [
        "toggle",
        "play",
        "pause",
        "back",
        "forward",
        "previous_chapter",
        "next_chapter",
    ];
    if !allowed.contains(&action) {
        return;
    }
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.eval(format!(
            "window.dispatchEvent(new CustomEvent('sapb:command', {{ detail: {} }}))",
            json!(action)
        ));
    }
}

pub fn command(app: &AppHandle, body: &[u8]) {
    let v: Value = serde_json::from_slice(body).unwrap_or(Value::Null);
    if let Some(a) = v["action"].as_str() {
        send_command(app, a);
    }
}

pub fn current(s: &Shared) -> Value {
    s.player.lock().unwrap().state.clone()
}

pub fn show_main(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
    }
}

/// The web app's player changed: title, author, playing, position, cover.
pub fn update(app: &AppHandle, s: &Shared, body: &[u8]) {
    let v: Value = serde_json::from_slice(body).unwrap_or(Value::Null);
    let playing = v["playing"].as_bool().unwrap_or(false);
    if let Some(tray) = app.try_state::<Tray>() {
        let _ = tray.toggle.set_text(if playing {
            t("desktop.tray.pause")
        } else {
            t("desktop.tray.play")
        });
    }
    if let Some(w) = app.get_webview_window("mini") {
        let _ = w.eval(format!(
            "window.dispatchEvent(new CustomEvent('sapb:player', {{ detail: {v} }}))"
        ));
    }
    let new_cover = {
        let mut p = s.player.lock().unwrap();
        let key = format!(
            "{}|{}",
            v["book_id"].as_str().unwrap_or(""),
            v["cover"].as_str().unwrap_or("")
        );
        let changed = key != p.cover_key;
        p.cover_key = key;
        p.state = v.clone();
        #[cfg(windows)]
        smtc_update(&mut p, &v, None, changed);
        changed
    };
    // The flyout needs the cover as a file: fetch it once per book.
    #[cfg(windows)]
    if new_cover {
        if let (Some(path), Some(cover)) =
            (cover_file(app), v["cover"].as_str().map(str::to_string))
        {
            let s = app.state::<std::sync::Arc<Shared>>().inner().clone();
            tauri::async_runtime::spawn(async move {
                if fetch_cover(&s, &cover, &path).await {
                    let mut p = s.player.lock().unwrap();
                    let v = p.state.clone();
                    smtc_update(&mut p, &v, Some(&path), true);
                }
            });
        }
    }
    let _ = new_cover;
}

fn cover_file(app: &AppHandle) -> Option<std::path::PathBuf> {
    Some(app.path().app_cache_dir().ok()?.join("now-playing.jpg"))
}

async fn fetch_cover(s: &Shared, cover: &str, path: &std::path::Path) -> bool {
    let bytes = if let Some(id) = cover.strip_prefix("/desktop/cover/") {
        crate::downloads::account_dir(s)
            .and_then(|d| std::fs::read(d.join(id).join("cover.jpg")).ok())
    } else if cover.starts_with("/api/") {
        match crate::proxy::send(s, "GET", cover, &[], Vec::new()).await {
            Ok(r) if r.status().is_success() => r.bytes().await.ok().map(|b| b.to_vec()),
            _ => None,
        }
    } else {
        None
    };
    bytes.is_some_and(|b| crate::config::write_atomic(path, &b).is_ok())
}

#[cfg(windows)]
fn smtc_update(p: &mut PlayerState, v: &Value, cover: Option<&std::path::Path>, metadata: bool) {
    use souvlaki::{MediaMetadata, MediaPlayback, MediaPosition};
    let Some(c) = p.smtc.as_mut() else { return };
    if v["book_id"].is_null() {
        let _ = c.set_playback(MediaPlayback::Stopped);
        return;
    }
    if metadata {
        // souvlaki opens "file://" + a plain Windows path (not a file URL).
        let cover_url = cover.map(|c| format!("file://{}", c.display()));
        let _ = c.set_metadata(MediaMetadata {
            title: v["title"].as_str(),
            artist: v["author"].as_str(),
            album: v["series"].as_str(),
            cover_url: cover_url.as_deref(),
            duration: v["duration_ms"]
                .as_u64()
                .map(std::time::Duration::from_millis),
        });
    }
    let progress = v["position_ms"]
        .as_u64()
        .map(|ms| MediaPosition(std::time::Duration::from_millis(ms)));
    let _ = c.set_playback(if v["playing"].as_bool().unwrap_or(false) {
        MediaPlayback::Playing { progress }
    } else {
        MediaPlayback::Paused { progress }
    });
}

/// SMTC needs the main window's handle; media keys and the flyout buttons
/// come back as commands.
#[cfg(windows)]
pub fn attach_smtc(app: &AppHandle, s: &Shared) {
    use souvlaki::{MediaControlEvent, MediaControls, PlatformConfig, SeekDirection};
    let Some(w) = app.get_webview_window("main") else {
        return;
    };
    let Ok(hwnd) = w.hwnd() else { return };
    let config = PlatformConfig {
        display_name: "Simple APB",
        dbus_name: "simple_apb",
        hwnd: Some(hwnd.0 as _),
    };
    let Ok(mut controls) = MediaControls::new(config) else {
        return;
    };
    let handle = app.clone();
    let attached = controls.attach(move |e: MediaControlEvent| {
        let action = match e {
            MediaControlEvent::Play => "play",
            MediaControlEvent::Pause => "pause",
            MediaControlEvent::Toggle => "toggle",
            MediaControlEvent::Next => "next_chapter",
            MediaControlEvent::Previous => "previous_chapter",
            MediaControlEvent::Seek(SeekDirection::Forward) => "forward",
            MediaControlEvent::Seek(SeekDirection::Backward) => "back",
            MediaControlEvent::Raise => "show",
            _ => return,
        };
        send_command(&handle, action);
    });
    if attached.is_ok() {
        s.player.lock().unwrap().smtc = Some(controls);
    }
}

pub fn build_tray(app: &AppHandle) -> tauri::Result<()> {
    let toggle = MenuItem::with_id(app, "toggle", t("desktop.tray.play"), true, None::<&str>)?;
    let back = MenuItem::with_id(app, "back", t("desktop.tray.back"), true, None::<&str>)?;
    let forward = MenuItem::with_id(
        app,
        "forward",
        t("desktop.tray.forward"),
        true,
        None::<&str>,
    )?;
    let show = MenuItem::with_id(app, "show", t("desktop.tray.show"), true, None::<&str>)?;
    let mini = MenuItem::with_id(app, "mini", t("desktop.tray.mini"), true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", t("desktop.tray.quit"), true, None::<&str>)?;
    let sep = PredefinedMenuItem::separator(app)?;
    let sep2 = PredefinedMenuItem::separator(app)?;
    let menu = Menu::with_items(
        app,
        &[&toggle, &back, &forward, &sep, &show, &mini, &sep2, &quit],
    )?;
    TrayIconBuilder::with_id("main")
        .icon(
            app.default_window_icon()
                .cloned()
                .ok_or(tauri::Error::InvalidIcon(std::io::Error::other("no icon")))?,
        )
        .tooltip("Simple APB")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, e| match e.id().as_ref() {
            "quit" => {
                app.state::<Quitting>()
                    .0
                    .store(true, std::sync::atomic::Ordering::Relaxed);
                app.exit(0);
            }
            // A window built inside a menu handler can deadlock on Windows.
            "mini" => {
                let a = app.clone();
                tauri::async_runtime::spawn(async move { toggle_mini(&a) });
            }
            other => send_command(app, other),
        })
        .on_tray_icon_event(|tray, e| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = e
            {
                show_main(tray.app_handle());
            }
        })
        .build(app)?;
    app.manage(Tray { toggle });
    Ok(())
}

/// Set when the user chose Quit: closing the window then really exits.
#[derive(Default)]
pub struct Quitting(pub std::sync::atomic::AtomicBool);

/// The small always-on-top window with the cover and the controls.
pub fn toggle_mini(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("mini") {
        let _ = w.close();
        return;
    }
    let url = WebviewUrl::CustomProtocol("sapb://localhost/mini".parse().unwrap());
    let built = WebviewWindowBuilder::new(app, "mini", url)
        .title("Simple APB")
        .inner_size(360.0, 128.0)
        .min_inner_size(300.0, 128.0)
        .resizable(true)
        .maximizable(false)
        .minimizable(false)
        .always_on_top(true)
        .skip_taskbar(true)
        .additional_browser_args(crate::BROWSER_ARGS)
        .on_navigation(crate::allow_navigation)
        .build();
    if let Err(e) = built {
        log::warn!("mini player: {e}");
    }
}
