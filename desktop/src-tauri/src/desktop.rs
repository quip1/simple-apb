// SPDX-License-Identifier: AGPL-3.0-or-later
//! `/desktop/…`: the endpoints the web app uses when it runs in this app.

use std::sync::atomic::Ordering;
use std::sync::Arc;

use serde_json::{json, Value};
use tauri::http::{header, Method, Request, StatusCode};
use tauri::AppHandle;
use tauri_plugin_dialog::DialogExt;

use crate::proxy::{error, json_response, Resp};
use crate::{config, downloads, player, Shared};

pub async fn route(app: &AppHandle, s: &Arc<Shared>, rest: &str, req: Request<Vec<u8>>) -> Resp {
    let method = req.method().clone();
    let parts: Vec<&str> = rest.split('/').filter(|p| !p.is_empty()).collect();
    match (method, parts.as_slice()) {
        (Method::GET, ["info"]) => json_response(StatusCode::OK, &info(s)),
        (Method::POST, ["server"]) => set_server(s, req.body()).await,
        (Method::GET, ["downloads"]) => json_response(StatusCode::OK, &json!(downloads::list(s))),
        (Method::POST, ["downloads", id]) => {
            if downloads::enqueue(s, id) {
                json_response(StatusCode::ACCEPTED, &json!({}))
            } else {
                error(StatusCode::BAD_REQUEST, "invalid", "Can't download that")
            }
        }
        (Method::DELETE, ["downloads", id]) => {
            downloads::remove(s, id);
            json_response(StatusCode::OK, &json!({}))
        }
        (Method::POST, ["download-dir"]) => pick_folder(app, s).await,
        (Method::GET, ["files", id, file]) => {
            let range = req
                .headers()
                .get(header::RANGE)
                .and_then(|v| v.to_str().ok());
            downloads::serve(s, id, Some(file), range)
        }
        (Method::GET, ["cover", id]) => downloads::serve(s, id, None, None),
        (Method::POST, ["open"]) => open(req.body()),
        (Method::POST, ["player"]) => {
            player::update(app, s, req.body());
            json_response(StatusCode::OK, &json!({}))
        }
        (Method::POST, ["command"]) => {
            player::command(app, req.body());
            json_response(StatusCode::OK, &json!({}))
        }
        (Method::GET, ["player"]) => json_response(StatusCode::OK, &player::current(s)),
        (Method::POST, ["mini"]) => {
            player::toggle_mini(app);
            json_response(StatusCode::OK, &json!({}))
        }
        (Method::POST, ["update", "skip"]) => {
            let v: Value = serde_json::from_slice(req.body()).unwrap_or(Value::Null);
            s.cfg.lock().unwrap().skipped_update = v["version"].as_str().map(str::to_string);
            s.save_config();
            json_response(StatusCode::OK, &json!({}))
        }
        (Method::GET, ["update"]) => json_response(StatusCode::OK, &crate::update::check(s).await),
        _ => error(StatusCode::NOT_FOUND, "not_found", "Not found"),
    }
}

fn info(s: &Shared) -> Value {
    let cfg = s.cfg.lock().unwrap();
    let dir = cfg
        .download_dir
        .clone()
        .unwrap_or_else(|| s.default_download_dir.clone());
    json!({
        "server_url": cfg.server_url,
        "version": env!("CARGO_PKG_VERSION"),
        "download_dir": dir.to_string_lossy(),
        "online": s.online.load(Ordering::Relaxed),
        "waiting_writes": s.outbox.snapshot().len(),
    })
}

/// Chooses (or changes) the server: checks it answers as a Simple APB server first.
async fn set_server(s: &Arc<Shared>, body: &[u8]) -> Resp {
    let v: Value = serde_json::from_slice(body).unwrap_or(Value::Null);
    let Some(url) = v["url"].as_str().and_then(config::normalize_server) else {
        return error(
            StatusCode::BAD_REQUEST,
            "invalid_url",
            "Not a server address",
        );
    };
    let probe = s
        .http
        .get(format!("{url}/api/v1/instance"))
        .timeout(std::time::Duration::from_secs(10))
        .send()
        .await;
    let inst: Value = match probe {
        Ok(r) if r.status().is_success() => r.json().await.unwrap_or(Value::Null),
        Ok(_) => {
            return error(
                StatusCode::BAD_GATEWAY,
                "not_simpleapb",
                "No Simple APB server there",
            )
        }
        Err(_) => {
            return error(
                StatusCode::BAD_GATEWAY,
                "unreachable",
                "The server can't be reached",
            )
        }
    };
    if inst.get("api_version").is_none() {
        return error(
            StatusCode::BAD_GATEWAY,
            "not_simpleapb",
            "No Simple APB server there",
        );
    }
    let changed = s.server().as_deref() != Some(url.as_str());
    if changed {
        // A different server: sign out of the old one here, start clean.
        crate::auth::set_tokens(s, None).await;
        s.cache.clear();
        s.outbox.clear();
        {
            let mut cfg = s.cfg.lock().unwrap();
            cfg.server_url = Some(url.clone());
            cfg.user_id = None;
        }
        s.save_config();
        *s.tokens.lock().await = config::tokens(&url);
        downloads::rescan(s);
    }
    json_response(
        StatusCode::OK,
        &json!({ "server_url": url, "instance": inst }),
    )
}

async fn pick_folder(app: &AppHandle, s: &Arc<Shared>) -> Resp {
    let (tx, rx) = tokio::sync::oneshot::channel();
    app.dialog().file().pick_folder(move |p| {
        let _ = tx.send(p);
    });
    let Some(Some(picked)) = rx.await.ok() else {
        return json_response(StatusCode::OK, &json!({ "changed": false }));
    };
    let Ok(path) = picked.into_path() else {
        return error(StatusCode::BAD_REQUEST, "invalid", "Not a folder");
    };
    s.cfg.lock().unwrap().download_dir = Some(path.join("Simple APB"));
    s.save_config();
    downloads::rescan(s);
    json_response(
        StatusCode::OK,
        &json!({ "changed": true, "download_dir": path.join("Simple APB").to_string_lossy() }),
    )
}

/// Links out of the app open in the browser (web pages only).
fn open(body: &[u8]) -> Resp {
    let v: Value = serde_json::from_slice(body).unwrap_or(Value::Null);
    let Some(u) = v["url"].as_str().and_then(|u| url::Url::parse(u).ok()) else {
        return error(StatusCode::BAD_REQUEST, "invalid_url", "Not a link");
    };
    if !matches!(u.scheme(), "http" | "https") {
        return error(StatusCode::BAD_REQUEST, "invalid_url", "Not a web link");
    }
    let _ = tauri_plugin_opener::open_url(u.as_str(), None::<&str>);
    json_response(StatusCode::OK, &json!({}))
}
