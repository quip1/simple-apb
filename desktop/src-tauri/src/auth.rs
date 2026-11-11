// SPDX-License-Identifier: AGPL-3.0-or-later
//! Signing in as a token client (platform "windows").
//!
//! The web app posts its usual login; this side swaps in the desktop's
//! device info, keeps the returned token pair in Windows Credential Manager
//! and hands the web app the rest of the answer without the tokens.

use serde_json::{json, Value};
use tauri::http::StatusCode;

use crate::config::{self, Tokens};
use crate::proxy::{error, json_response, read, send, Failure, Resp};
use crate::Shared;

pub enum Refresh {
    Ok(String),
    Denied,
    Offline,
}

fn device(s: &Shared) -> Value {
    let name = std::env::var("COMPUTERNAME")
        .map(|n| format!("Windows ({n})"))
        .unwrap_or_else(|_| "Windows".into());
    json!({
        "device_id": s.cfg.lock().unwrap().device_id,
        "device_name": name.chars().take(100).collect::<String>(),
        "platform": "windows",
        "app_version": env!("CARGO_PKG_VERSION"),
    })
}

pub async fn login(s: &Shared, path: &str, body: Vec<u8>) -> Resp {
    let mut v: Value = serde_json::from_slice(&body).unwrap_or(Value::Null);
    if path.ends_with("/login") {
        if let Some(o) = v.as_object_mut() {
            o.insert("device".into(), device(s));
        }
    }
    let headers = vec![("content-type".to_string(), "application/json".to_string())];
    let res = match send(
        s,
        "POST",
        path,
        &headers,
        serde_json::to_vec(&v).unwrap_or_default(),
    )
    .await
    {
        Ok(r) => read(r).await,
        Err(Failure::NoServer) => {
            return error(
                StatusCode::SERVICE_UNAVAILABLE,
                "no_server",
                "No server chosen",
            )
        }
        Err(Failure::Offline) => {
            return error(
                StatusCode::SERVICE_UNAVAILABLE,
                "offline",
                "The server can't be reached",
            )
        }
    };
    let mut out: Value = serde_json::from_slice(&res.body).unwrap_or(Value::Null);
    if res.status == StatusCode::OK {
        if let Some(t) = out.get("tokens") {
            let pair = Tokens {
                access: t["access_token"].as_str().unwrap_or("").to_string(),
                refresh: t["refresh_token"].as_str().unwrap_or("").to_string(),
            };
            if !pair.access.is_empty() {
                set_tokens(s, Some(pair)).await;
                // The sign-in answer carries the user: keep it as /me for offline starts.
                if let (Some(server), Some(u)) = (s.server(), out.get("user")) {
                    s.cache.put(
                        &server,
                        "/api/v1/me",
                        "application/json",
                        &serde_json::to_vec(u).unwrap_or_default(),
                    );
                }
                let user = out["user"]["id"].as_str().map(str::to_string);
                {
                    let mut cfg = s.cfg.lock().unwrap();
                    cfg.user_id = user;
                }
                s.save_config();
            }
        }
        if let Some(o) = out.as_object_mut() {
            o.remove("tokens");
        }
    }
    json_response(res.status, &out)
}

pub async fn set_tokens(s: &Shared, t: Option<Tokens>) {
    let Some(server) = s.server() else { return };
    match &t {
        Some(t) => config::save_tokens(&server, t),
        None => config::forget_tokens(&server),
    }
    *s.tokens.lock().await = t;
}

/// Rotates the pair once. `failed` is the access token that was refused:
/// if another request already refreshed meanwhile, its new token is used.
pub async fn refresh(s: &Shared, failed: Option<&str>) -> Refresh {
    let Some(server) = s.server() else {
        return Refresh::Denied;
    };
    let mut guard = s.tokens.lock().await;
    let Some(current) = guard.clone() else {
        return Refresh::Denied;
    };
    if failed.is_some_and(|f| f != current.access) {
        return Refresh::Ok(current.access);
    }
    let res = s
        .http
        .post(format!("{server}/api/v1/auth/refresh"))
        .json(&json!({ "refresh_token": current.refresh }))
        .send()
        .await;
    match res {
        Err(_) => Refresh::Offline,
        Ok(r) if r.status().is_success() => {
            let v: Value = r.json().await.unwrap_or(Value::Null);
            let pair = Tokens {
                access: v["access_token"].as_str().unwrap_or("").to_string(),
                refresh: v["refresh_token"].as_str().unwrap_or("").to_string(),
            };
            if pair.access.is_empty() {
                return Refresh::Offline;
            }
            config::save_tokens(&server, &pair);
            let access = pair.access.clone();
            *guard = Some(pair);
            Refresh::Ok(access)
        }
        Ok(r)
            if r.status() == reqwest::StatusCode::UNAUTHORIZED
                || r.status() == reqwest::StatusCode::FORBIDDEN =>
        {
            // Refused: this device is signed out (the web app shows sign-in).
            config::forget_tokens(&server);
            *guard = None;
            Refresh::Denied
        }
        Ok(_) => Refresh::Offline,
    }
}

/// The web app's own refresh call (after a 401): answered from here.
pub async fn refresh_for_web(s: &Shared) -> Resp {
    match refresh(s, None).await {
        Refresh::Ok(_) => json_response(StatusCode::OK, &json!({})),
        Refresh::Denied => error(StatusCode::UNAUTHORIZED, "invalid_token", "Signed out"),
        Refresh::Offline => error(
            StatusCode::SERVICE_UNAVAILABLE,
            "offline",
            "The server can't be reached",
        ),
    }
}

pub async fn logout(s: &Shared) -> Resp {
    // Best effort: the session ends on the server when it's reachable.
    let _ = send(s, "POST", "/api/v1/auth/logout", &[], Vec::new()).await;
    set_tokens(s, None).await;
    s.cache.clear();
    s.outbox.clear();
    {
        let mut cfg = s.cfg.lock().unwrap();
        cfg.user_id = None;
    }
    s.save_config();
    tauri::http::Response::builder()
        .status(StatusCode::NO_CONTENT)
        .body(Vec::new())
        .unwrap()
}
