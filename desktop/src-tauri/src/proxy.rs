// SPDX-License-Identifier: AGPL-3.0-or-later
//! The `sapb://` protocol: the whole app is served from here (ADR-0047).
//!
//! - `/api/…` goes to the user's server, signed in with this install's own
//!   token pair (the web app never sees a token or a cookie). Answers are
//!   cached for offline use; progress written offline waits in the outbox.
//! - `/desktop/…` are the app's own endpoints: the server address, downloads
//!   and local files, the player state for the tray and mini player.
//! - Anything else is the web app (`web/dist-desktop`, built into the exe),
//!   with `index.html` for every route.

use std::sync::atomic::Ordering;
use std::sync::Arc;

use serde_json::{json, Value};
use tauri::http::{header, Request, Response, StatusCode};
use tauri::AppHandle;

use crate::offline::{is_progress_write, Cache};
use crate::{desktop, downloads, Shared};

pub type Resp = Response<Vec<u8>>;

/// The origin the app runs on (WebView2 maps custom schemes to http://<scheme>.localhost).
pub const ORIGINS: [&str; 2] = ["http://sapb.localhost", "sapb://localhost"];

/// Live-playback chunks: an open-ended range is capped so a long file never
/// has to arrive in one piece before it starts.
const CHUNK: u64 = 4 * 1024 * 1024;

pub fn json_response(status: StatusCode, v: &Value) -> Resp {
    Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, "application/json")
        .header(header::CACHE_CONTROL, "no-store")
        .body(serde_json::to_vec(v).unwrap_or_default())
        .unwrap()
}

pub fn error(status: StatusCode, code: &str, message: &str) -> Resp {
    json_response(status, &json!({ "code": code, "message": message }))
}

pub async fn handle(app: &AppHandle, s: &Arc<Shared>, req: Request<Vec<u8>>) -> Resp {
    let path = req.uri().path().to_string();
    // Writes and the app's own endpoints only from the app itself.
    let write = !matches!(
        *req.method(),
        tauri::http::Method::GET | tauri::http::Method::HEAD
    );
    if (write || path.starts_with("/desktop/")) && !same_origin(&req) {
        return error(StatusCode::FORBIDDEN, "forbidden", "Cross-origin request");
    }
    if path.starts_with("/api/") {
        return api(s, req).await;
    }
    if let Some(rest) = path.strip_prefix("/desktop/") {
        return desktop::route(app, s, rest, req).await;
    }
    asset(app, &path)
}

fn same_origin(req: &Request<Vec<u8>>) -> bool {
    match req
        .headers()
        .get(header::ORIGIN)
        .and_then(|o| o.to_str().ok())
    {
        None => true,
        Some(o) => ORIGINS.contains(&o),
    }
}

fn asset(app: &AppHandle, path: &str) -> Resp {
    let resolver = app.asset_resolver();
    let found = resolver
        .get(path.to_string())
        .or_else(|| resolver.get("index.html".to_string()));
    let Some(a) = found else {
        return error(StatusCode::NOT_FOUND, "not_found", "Not found");
    };
    let html = a.mime_type.starts_with("text/html");
    let mut b = Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, &a.mime_type)
        .header("X-Content-Type-Options", "nosniff");
    if html {
        // Everything comes from this origin; the app has no inline scripts.
        b = b
            .header(
                "Content-Security-Policy",
                "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
            )
            .header(header::CACHE_CONTROL, "no-cache");
    }
    b.body(a.bytes).unwrap()
}

/// An answer from the server, read whole.
pub struct Upstream {
    pub status: StatusCode,
    pub content_type: String,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

pub enum Failure {
    /// The server couldn't be reached (offline, DNS, timeout).
    Offline,
    /// No server chosen yet.
    NoServer,
}

/// Headers passed through, besides the body: content negotiation, byte
/// ranges, caching, and the tus upload protocol's own headers.
const PASS_REQUEST: &[&str] = &[
    "accept",
    "accept-language",
    "content-type",
    "range",
    "if-none-match",
    "if-modified-since",
    "tus-resumable",
    "upload-length",
    "upload-metadata",
    "upload-offset",
];
const PASS_RESPONSE: &[&str] = &[
    "content-type",
    "content-range",
    "accept-ranges",
    "etag",
    "last-modified",
    "cache-control",
    "content-disposition",
    "retry-after",
    "location",
    "tus-resumable",
    "tus-version",
    "tus-extension",
    "tus-max-size",
    "upload-offset",
    "upload-length",
];

/// Sends one request to the server as the signed-in user. A 401 for an
/// expired access token refreshes the pair once and retries.
pub async fn send(
    s: &Shared,
    method: &str,
    path_and_query: &str,
    headers: &[(String, String)],
    body: Vec<u8>,
) -> Result<reqwest::Response, Failure> {
    let server = s.server().ok_or(Failure::NoServer)?;
    let method = reqwest::Method::from_bytes(method.as_bytes()).unwrap_or(reqwest::Method::GET);
    let attempt = |access: Option<String>| {
        let mut rb = s
            .http
            .request(method.clone(), format!("{server}{path_and_query}"));
        for (k, v) in headers {
            rb = rb.header(k, v);
        }
        if let Some(a) = access {
            rb = rb.bearer_auth(a);
        }
        rb.body(body.clone()).send()
    };
    let access = s.tokens.lock().await.as_ref().map(|t| t.access.clone());
    let res = attempt(access.clone())
        .await
        .map_err(|_| Failure::Offline)?;
    s.online.store(true, Ordering::Relaxed);
    if res.status() != reqwest::StatusCode::UNAUTHORIZED
        || access.is_none()
        || path_and_query.starts_with("/api/v1/auth/")
    {
        return Ok(res);
    }
    match crate::auth::refresh(s, access.as_deref()).await {
        crate::auth::Refresh::Ok(new) => attempt(Some(new)).await.map_err(|_| Failure::Offline),
        _ => Ok(res),
    }
}

pub async fn read(res: reqwest::Response) -> Upstream {
    let status = StatusCode::from_u16(res.status().as_u16()).unwrap_or(StatusCode::BAD_GATEWAY);
    let headers: Vec<(String, String)> = res
        .headers()
        .iter()
        .filter(|(k, _)| PASS_RESPONSE.contains(&k.as_str()))
        .filter_map(|(k, v)| Some((k.to_string(), v.to_str().ok()?.to_string())))
        .collect();
    let content_type = res
        .headers()
        .get(reqwest::header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_string();
    let body = res.bytes().await.map(|b| b.to_vec()).unwrap_or_default();
    Upstream {
        status,
        content_type,
        headers,
        body,
    }
}

fn respond(u: Upstream) -> Resp {
    let mut b = Response::builder().status(u.status);
    for (k, v) in &u.headers {
        b = b.header(k, v);
    }
    b.body(u.body).unwrap()
}

async fn api(s: &Arc<Shared>, req: Request<Vec<u8>>) -> Resp {
    let method = req.method().as_str().to_string();
    let path = req.uri().path().to_string();
    let pq = match req.uri().query() {
        Some(q) => format!("{path}?{q}"),
        None => path.clone(),
    };
    let Some(server) = s.server() else {
        return error(
            StatusCode::SERVICE_UNAVAILABLE,
            "no_server",
            "No server chosen",
        );
    };

    // Sign-in and sign-out are this app's business (tokens stay in Rust).
    match (method.as_str(), path.as_str()) {
        ("POST", "/api/v1/auth/login") | ("POST", "/api/v1/auth/totp/verify") => {
            return crate::auth::login(s, &path, req.into_body()).await
        }
        ("POST", "/api/v1/auth/refresh") => return crate::auth::refresh_for_web(s).await,
        ("POST", "/api/v1/auth/logout") => return crate::auth::logout(s).await,
        _ => {}
    }

    // Downloaded books play from disk, online or not.
    if method == "POST" && path.starts_with("/api/v1/books/") && path.ends_with("/stream-url") {
        let id = path
            .trim_start_matches("/api/v1/books/")
            .trim_end_matches("/stream-url");
        if let Some(v) = downloads::local_stream_urls(s, id) {
            return json_response(StatusCode::OK, &v);
        }
    }

    let mut headers: Vec<(String, String)> = req
        .headers()
        .iter()
        .filter(|(k, _)| PASS_REQUEST.contains(&k.as_str()))
        .filter_map(|(k, v)| Some((k.to_string(), v.to_str().ok()?.to_string())))
        .collect();
    // Live audio from the server: bounded chunks (see CHUNK).
    if method == "GET" && is_media(&path) {
        let range = headers.iter().position(|(k, _)| k == "range");
        let start = range
            .and_then(|i| parse_open_range(&headers[i].1))
            .or(if range.is_none() { Some(0) } else { None });
        if let Some(start) = start {
            headers.retain(|(k, _)| k != "range");
            headers.push((
                "range".into(),
                format!("bytes={}-{}", start, start + CHUNK - 1),
            ));
        }
    }
    let body = req.into_body();
    match send(s, &method, &pq, &headers, body.clone()).await {
        Ok(res) => {
            let u = read(res).await;
            if method == "GET"
                && u.status == StatusCode::OK
                && Cache::cacheable(&path, &u.content_type, u.body.len())
            {
                s.cache.put(&server, &pq, &u.content_type, &u.body);
            }
            if !s.outbox.is_empty() {
                crate::flush_outbox_soon(s.clone());
            }
            respond(u)
        }
        Err(Failure::NoServer) => error(
            StatusCode::SERVICE_UNAVAILABLE,
            "no_server",
            "No server chosen",
        ),
        Err(Failure::Offline) => {
            s.online.store(false, Ordering::Relaxed);
            if method == "GET" {
                if let Some((ct, body)) = s
                    .cache
                    .get(&server, &pq)
                    .or_else(|| downloads::offline_detail(s, &path))
                {
                    return Response::builder()
                        .status(StatusCode::OK)
                        .header(header::CONTENT_TYPE, ct)
                        .header("X-Simpleapb-Offline", "1")
                        .body(body)
                        .unwrap();
                }
            }
            if is_progress_write(&method, &path) {
                s.outbox.push(&pq, &body);
            }
            error(
                StatusCode::SERVICE_UNAVAILABLE,
                "offline",
                "The server can't be reached",
            )
        }
    }
}

/// Audio from the server: book files and signed stream links.
fn is_media(path: &str) -> bool {
    (path.starts_with("/api/v1/books/") && path.contains("/files/"))
        || path.starts_with("/api/v1/stream/")
}

/// "bytes=123-" → Some(123); a closed range ("bytes=0-99") is left alone.
pub fn parse_open_range(v: &str) -> Option<u64> {
    let r = v.strip_prefix("bytes=")?;
    let (a, b) = r.split_once('-')?;
    if !b.trim().is_empty() {
        return None;
    }
    a.trim().parse().ok()
}

/// "bytes=a-b" / "bytes=a-" / "bytes=-n" against a file of `len` bytes.
pub fn parse_range(v: &str, len: u64) -> Option<(u64, u64)> {
    let r = v.strip_prefix("bytes=")?.split(',').next()?;
    let (a, b) = r.split_once('-')?;
    let (start, end) = match (a.trim(), b.trim()) {
        ("", n) => {
            let n: u64 = n.parse().ok()?;
            (len.saturating_sub(n), len.checked_sub(1)?)
        }
        (a, "") => (a.parse().ok()?, len.checked_sub(1)?),
        (a, b) => (
            a.parse().ok()?,
            b.parse::<u64>().ok()?.min(len.checked_sub(1)?),
        ),
    };
    (start <= end && start < len).then_some((start, end))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ranges() {
        assert_eq!(parse_open_range("bytes=100-"), Some(100));
        assert_eq!(parse_open_range("bytes=0-99"), None);
        assert_eq!(parse_range("bytes=0-", 10), Some((0, 9)));
        assert_eq!(parse_range("bytes=2-4", 10), Some((2, 4)));
        assert_eq!(parse_range("bytes=5-50", 10), Some((5, 9)));
        assert_eq!(parse_range("bytes=-3", 10), Some((7, 9)));
        assert_eq!(parse_range("bytes=10-", 10), None);
        assert_eq!(parse_range("bytes=0-", 0), None);
    }
}
