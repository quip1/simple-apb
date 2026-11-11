// SPDX-License-Identifier: AGPL-3.0-or-later
//! Offline downloads (brief §10): whole books in a folder the user picks,
//! played from disk with no network.
//!
//! Layout: `<folder>/<server>-<user>/<book id>/` holds the audio files (named
//! by file id), `cover.jpg`, and `book.json` (the server's book details),
//! written last: a book is complete exactly when `book.json` is there.
//! Downloads resume where they stopped, and every file is checked against
//! the server's SHA-256 before it counts.

use std::collections::BTreeMap;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use futures_util::StreamExt;
use serde::Serialize;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use tauri::http::{header, Response, StatusCode};

use crate::config::write_atomic;
use crate::proxy::{error, parse_range, read, send, Failure, Resp};
use crate::Shared;

#[derive(Clone, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Status {
    Queued,
    Downloading,
    Done,
    Failed,
}

#[derive(Clone, Serialize)]
pub struct Job {
    pub book_id: String,
    pub title: String,
    pub status: Status,
    pub done_bytes: u64,
    pub total_bytes: u64,
    pub error: Option<String>,
}

#[derive(Default)]
pub struct Downloads {
    pub jobs: Mutex<BTreeMap<String, Job>>,
    pub wake: tokio::sync::Notify,
    cancel: Mutex<BTreeMap<String, Arc<AtomicBool>>>,
}

/// Book ids are UUIDs; anything else never touches the disk.
fn valid_id(id: &str) -> bool {
    id.len() == 36 && id.chars().all(|c| c.is_ascii_hexdigit() || c == '-')
}

/// The current account's folder, or None when signed out.
pub fn account_dir(s: &Shared) -> Option<PathBuf> {
    let cfg = s.cfg.lock().unwrap();
    let server = cfg.server_url.clone()?;
    let user = cfg.user_id.clone()?;
    let base = cfg
        .download_dir
        .clone()
        .unwrap_or_else(|| s.default_download_dir.clone());
    let tag = &hex::encode(Sha256::digest(server.as_bytes()))[..8];
    Some(base.join(format!("{tag}-{user}")))
}

fn book_dir(s: &Shared, id: &str) -> Option<PathBuf> {
    valid_id(id)
        .then(|| account_dir(s).map(|d| d.join(id)))
        .flatten()
}

fn detail(s: &Shared, id: &str) -> Option<Value> {
    let p = book_dir(s, id)?.join("book.json");
    serde_json::from_slice(&std::fs::read(p).ok()?).ok()
}

/// Finds the books already on disk (start-up, another account, a new folder).
pub fn rescan(s: &Shared) {
    let mut jobs = s.downloads.jobs.lock().unwrap();
    jobs.retain(|_, j| matches!(j.status, Status::Queued | Status::Downloading));
    let Some(dir) = account_dir(s) else { return };
    let Ok(rd) = std::fs::read_dir(dir) else {
        return;
    };
    for e in rd.flatten() {
        let id = e.file_name().to_string_lossy().to_string();
        if !valid_id(&id) || jobs.contains_key(&id) {
            continue;
        }
        if let Ok(b) = std::fs::read(e.path().join("book.json")) {
            let v: Value = serde_json::from_slice(&b).unwrap_or(Value::Null);
            let total = v["files"]
                .as_array()
                .map(|f| f.iter().filter_map(|x| x["size_bytes"].as_u64()).sum())
                .unwrap_or(0);
            jobs.insert(
                id.clone(),
                Job {
                    book_id: id,
                    title: v["title"].as_str().unwrap_or("").to_string(),
                    status: Status::Done,
                    done_bytes: total,
                    total_bytes: total,
                    error: None,
                },
            );
        }
    }
}

pub fn list(s: &Shared) -> Vec<Job> {
    s.downloads.jobs.lock().unwrap().values().cloned().collect()
}

pub fn enqueue(s: &Shared, id: &str) -> bool {
    if !valid_id(id) || account_dir(s).is_none() {
        return false;
    }
    let mut jobs = s.downloads.jobs.lock().unwrap();
    let keep = jobs
        .get(id)
        .is_some_and(|j| !matches!(j.status, Status::Failed));
    if !keep {
        jobs.insert(
            id.to_string(),
            Job {
                book_id: id.to_string(),
                title: String::new(),
                status: Status::Queued,
                done_bytes: 0,
                total_bytes: 0,
                error: None,
            },
        );
    }
    drop(jobs);
    s.downloads.wake.notify_one();
    true
}

pub fn remove(s: &Shared, id: &str) {
    if let Some(flag) = s.downloads.cancel.lock().unwrap().get(id) {
        flag.store(true, Ordering::Relaxed);
    }
    s.downloads.jobs.lock().unwrap().remove(id);
    if let Some(d) = book_dir(s, id) {
        let _ = std::fs::remove_dir_all(d);
    }
}

/// Runs queued downloads one at a time, for as long as the app runs.
pub async fn worker(s: Arc<Shared>) {
    loop {
        let next = {
            let jobs = s.downloads.jobs.lock().unwrap();
            jobs.values()
                .find(|j| matches!(j.status, Status::Queued))
                .map(|j| j.book_id.clone())
        };
        let Some(id) = next else {
            s.downloads.wake.notified().await;
            continue;
        };
        let flag = Arc::new(AtomicBool::new(false));
        s.downloads
            .cancel
            .lock()
            .unwrap()
            .insert(id.clone(), flag.clone());
        set(&s, &id, |j| j.status = Status::Downloading);
        let result = download(&s, &id, &flag).await;
        s.downloads.cancel.lock().unwrap().remove(&id);
        if flag.load(Ordering::Relaxed) {
            continue;
        }
        match result {
            Ok(()) => set(&s, &id, |j| {
                j.status = Status::Done;
                j.done_bytes = j.total_bytes;
            }),
            Err(e) => {
                log::warn!("download of {id} failed: {e}");
                set(&s, &id, |j| {
                    j.status = Status::Failed;
                    j.error = Some(e);
                });
                // Offline: wait before trying the next one.
                if !s.online.load(Ordering::Relaxed) {
                    tokio::time::sleep(std::time::Duration::from_secs(5)).await;
                }
            }
        }
    }
}

fn set(s: &Shared, id: &str, f: impl FnOnce(&mut Job)) {
    if let Some(j) = s.downloads.jobs.lock().unwrap().get_mut(id) {
        f(j);
    }
}

async fn get(
    s: &Shared,
    path: &str,
    headers: &[(String, String)],
) -> Result<reqwest::Response, String> {
    match send(s, "GET", path, headers, Vec::new()).await {
        Ok(r) => Ok(r),
        Err(Failure::Offline) => Err("offline".into()),
        Err(Failure::NoServer) => Err("no_server".into()),
    }
}

async fn download(s: &Shared, id: &str, cancel: &AtomicBool) -> Result<(), String> {
    let dir = book_dir(s, id).ok_or("signed_out")?;
    let res = get(s, &format!("/api/v1/books/{id}"), &[]).await?;
    if !res.status().is_success() {
        return Err(format!("book: HTTP {}", res.status().as_u16()));
    }
    let detail_bytes = res.bytes().await.map_err(|e| e.to_string())?;
    let d: Value = serde_json::from_slice(&detail_bytes).map_err(|e| e.to_string())?;
    let files = d["files"].as_array().cloned().unwrap_or_default();
    let total: u64 = files.iter().filter_map(|f| f["size_bytes"].as_u64()).sum();
    set(s, id, |j| {
        j.title = d["title"].as_str().unwrap_or("").to_string();
        j.total_bytes = total;
    });
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;

    let mut before: u64 = 0;
    for f in &files {
        let fid = f["id"]
            .as_str()
            .filter(|x| valid_id(x))
            .ok_or("bad file id")?;
        let size = f["size_bytes"].as_u64().unwrap_or(0);
        let want = f["sha256"].as_str().unwrap_or("").to_lowercase();
        let done = dir.join(fid);
        if !(done.is_file() && std::fs::metadata(&done).map(|m| m.len()).unwrap_or(0) == size) {
            fetch_file(s, id, fid, &dir, before, cancel).await?;
        }
        if !want.is_empty() && sha256_file(&done).map_err(|e| e.to_string())? != want {
            let _ = std::fs::remove_file(&done);
            return Err("checksum".into());
        }
        before += size;
        set(s, id, |j| j.done_bytes = before);
    }

    if d["has_cover"].as_bool().unwrap_or(false) {
        if let Ok(r) = get(s, &format!("/api/v1/books/{id}/cover?size=1024"), &[]).await {
            if r.status().is_success() {
                let u = read(r).await;
                let _ = write_atomic(&dir.join("cover.jpg"), &u.body);
            }
        }
    }
    // Last: the book counts as downloaded from here on.
    write_atomic(&dir.join("book.json"), &detail_bytes).map_err(|e| e.to_string())
}

async fn fetch_file(
    s: &Shared,
    id: &str,
    fid: &str,
    dir: &Path,
    before: u64,
    cancel: &AtomicBool,
) -> Result<(), String> {
    let part = dir.join(format!("{fid}.part"));
    let have = std::fs::metadata(&part).map(|m| m.len()).unwrap_or(0);
    let headers = if have > 0 {
        vec![("range".to_string(), format!("bytes={have}-"))]
    } else {
        vec![]
    };
    let res = get(
        s,
        &format!("/api/v1/books/{id}/files/{fid}?download=1"),
        &headers,
    )
    .await?;
    let resumed = res.status() == reqwest::StatusCode::PARTIAL_CONTENT;
    if !res.status().is_success() {
        return Err(format!("file: HTTP {}", res.status().as_u16()));
    }
    let mut out = std::fs::OpenOptions::new()
        .create(true)
        .write(true)
        .append(resumed)
        .truncate(!resumed)
        .open(&part)
        .map_err(|e| e.to_string())?;
    let mut written = if resumed { have } else { 0 };
    let mut stream = res.bytes_stream();
    while let Some(chunk) = stream.next().await {
        if cancel.load(Ordering::Relaxed) {
            return Err("cancelled".into());
        }
        let chunk = chunk.map_err(|_| "offline".to_string())?;
        out.write_all(&chunk).map_err(|e| e.to_string())?;
        written += chunk.len() as u64;
        set(s, id, |j| j.done_bytes = before + written);
    }
    out.sync_all().map_err(|e| e.to_string())?;
    drop(out);
    std::fs::rename(&part, dir.join(fid)).map_err(|e| e.to_string())
}

fn sha256_file(p: &Path) -> std::io::Result<String> {
    let mut f = std::fs::File::open(p)?;
    let mut h = Sha256::new();
    std::io::copy(&mut f, &mut h)?;
    Ok(hex::encode(h.finalize()))
}

/// A downloaded book's "stream URLs": its own files on disk.
pub fn local_stream_urls(s: &Shared, id: &str) -> Option<Value> {
    let d = detail(s, id)?;
    let files: Vec<Value> = d["files"]
        .as_array()?
        .iter()
        .filter_map(|f| f["id"].as_str())
        .map(|fid| json!({ "file_id": fid, "url": format!("/desktop/files/{id}/{fid}") }))
        .collect();
    let has_cover = book_dir(s, id).is_some_and(|b| b.join("cover.jpg").is_file());
    let mut v = json!({ "expires_at": "2999-01-01T00:00:00Z", "files": files });
    if has_cover {
        v["cover_url"] = json!(format!("/desktop/cover/{id}"));
    }
    Some(v)
}

/// Book details for a downloaded book when the server can't be reached.
pub fn offline_detail(s: &Shared, path: &str) -> Option<(String, Vec<u8>)> {
    let id = path.strip_prefix("/api/v1/books/")?;
    let b = std::fs::read(book_dir(s, id)?.join("book.json")).ok()?;
    Some(("application/json".to_string(), b))
}

/// Serves a downloaded file or cover, with byte ranges for seeking.
pub fn serve(s: &Shared, id: &str, file: Option<&str>, range: Option<&str>) -> Resp {
    let Some(dir) = book_dir(s, id) else {
        return error(StatusCode::NOT_FOUND, "not_found", "Not downloaded");
    };
    let (path, mime) = match file {
        Some(fid) if valid_id(fid) => {
            let mime = detail(s, id)
                .and_then(|d| {
                    d["files"].as_array()?.iter().find(|f| f["id"] == fid)?["mime_type"]
                        .as_str()
                        .map(str::to_string)
                })
                .unwrap_or_else(|| "application/octet-stream".into());
            (dir.join(fid), mime)
        }
        Some(_) => return error(StatusCode::NOT_FOUND, "not_found", "Not found"),
        None => (dir.join("cover.jpg"), "image/jpeg".to_string()),
    };
    let Ok(meta) = std::fs::metadata(&path) else {
        return error(StatusCode::NOT_FOUND, "not_found", "Not downloaded");
    };
    let len = meta.len();
    let (start, end, status) = match range.and_then(|r| parse_range(r, len)) {
        Some((a, b)) => (
            a,
            b.min(a + 8 * 1024 * 1024 - 1),
            StatusCode::PARTIAL_CONTENT,
        ),
        None if range.is_some() => {
            return Response::builder()
                .status(StatusCode::RANGE_NOT_SATISFIABLE)
                .header(header::CONTENT_RANGE, format!("bytes */{len}"))
                .body(Vec::new())
                .unwrap()
        }
        None if len > 8 * 1024 * 1024 => (0, 8 * 1024 * 1024 - 1, StatusCode::PARTIAL_CONTENT),
        None => (0, len.saturating_sub(1), StatusCode::OK),
    };
    let body = read_range(&path, start, end).unwrap_or_default();
    let mut b = Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, mime)
        .header(header::ACCEPT_RANGES, "bytes");
    if status == StatusCode::PARTIAL_CONTENT {
        b = b.header(header::CONTENT_RANGE, format!("bytes {start}-{end}/{len}"));
    }
    b.body(body).unwrap()
}

fn read_range(p: &Path, start: u64, end: u64) -> std::io::Result<Vec<u8>> {
    use std::io::{Read, Seek, SeekFrom};
    let mut f = std::fs::File::open(p)?;
    f.seek(SeekFrom::Start(start))?;
    let mut buf = vec![0u8; (end + 1 - start) as usize];
    f.read_exact(&mut buf)?;
    Ok(buf)
}
