// SPDX-License-Identifier: AGPL-3.0-or-later
//! Working without the server (brief §10, Phase 4 acceptance).
//!
//! - **Cache:** the last good answer to each GET (JSON and covers) is kept on
//!   disk, so Home, the library and book pages open offline.
//! - **Outbox:** a progress write that can't reach the server waits here, one
//!   per book (the newest), and is sent when the server is back. A write the
//!   server refuses as a conflict is dropped: the player meets the same
//!   conflict on its next write and asks the user (ADR-0047).

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::config::write_atomic;

/// JSON and images only, and nothing big: audio never goes in the cache.
const MAX_ENTRY: usize = 4 * 1024 * 1024;
const MAX_TOTAL: u64 = 200 * 1024 * 1024;

pub struct Cache {
    dir: PathBuf,
}

#[derive(Serialize, Deserialize)]
struct Meta {
    content_type: String,
}

impl Cache {
    pub fn new(dir: PathBuf) -> Self {
        Self { dir }
    }

    fn key(server: &str, path_and_query: &str) -> String {
        hex::encode(Sha256::digest(
            format!("{server}{path_and_query}").as_bytes(),
        ))
    }

    pub fn cacheable(path: &str, content_type: &str, len: usize) -> bool {
        let ct = content_type.split(';').next().unwrap_or("").trim();
        let kind = ct == "application/json" || ct.starts_with("image/");
        kind && len <= MAX_ENTRY
            && !path.starts_with("/api/v1/auth/")
            && !path.contains("/stream-url")
            && !path.contains("/files/")
    }

    pub fn put(&self, server: &str, path_and_query: &str, content_type: &str, body: &[u8]) {
        let k = Self::key(server, path_and_query);
        let meta = serde_json::to_vec(&Meta {
            content_type: content_type.to_string(),
        })
        .unwrap_or_default();
        if write_atomic(&self.dir.join(format!("{k}.body")), body).is_ok() {
            let _ = write_atomic(&self.dir.join(format!("{k}.meta")), &meta);
        }
    }

    pub fn get(&self, server: &str, path_and_query: &str) -> Option<(String, Vec<u8>)> {
        let k = Self::key(server, path_and_query);
        let meta: Meta =
            serde_json::from_slice(&std::fs::read(self.dir.join(format!("{k}.meta"))).ok()?)
                .ok()?;
        let body = std::fs::read(self.dir.join(format!("{k}.body"))).ok()?;
        Some((meta.content_type, body))
    }

    /// Drops everything (sign-out, another server).
    pub fn clear(&self) {
        let _ = std::fs::remove_dir_all(&self.dir);
    }

    /// Keeps the cache under its size cap, oldest entries out first.
    pub fn trim(&self) {
        let Ok(rd) = std::fs::read_dir(&self.dir) else {
            return;
        };
        let mut files: Vec<(std::time::SystemTime, u64, PathBuf)> = rd
            .flatten()
            .filter_map(|e| {
                let m = e.metadata().ok()?;
                Some((m.modified().ok()?, m.len(), e.path()))
            })
            .collect();
        let mut total: u64 = files.iter().map(|f| f.1).sum();
        if total <= MAX_TOTAL {
            return;
        }
        files.sort_by_key(|f| f.0);
        for (_, len, p) in files {
            if total <= MAX_TOTAL * 8 / 10 {
                break;
            }
            let _ = std::fs::remove_file(&p);
            total = total.saturating_sub(len);
        }
    }
}

/// One waiting progress write.
#[derive(Clone, Serialize, Deserialize)]
pub struct Pending {
    pub path: String,
    pub body: serde_json::Value,
}

pub struct Outbox {
    file: PathBuf,
    items: Mutex<BTreeMap<String, Pending>>,
}

impl Outbox {
    pub fn load(file: PathBuf) -> Self {
        let items = std::fs::read(&file)
            .ok()
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default();
        Self {
            file,
            items: Mutex::new(items),
        }
    }

    fn save(&self, items: &BTreeMap<String, Pending>) {
        if let Ok(bytes) = serde_json::to_vec(items) {
            let _ = write_atomic(&self.file, &bytes);
        }
    }

    /// Queues a progress write; a newer one for the same book replaces it.
    pub fn push(&self, path: &str, body: &[u8]) {
        let Ok(value) = serde_json::from_slice::<serde_json::Value>(body) else {
            return;
        };
        let mut items = self.items.lock().unwrap();
        let newer = match items.get(path) {
            Some(old) => ts(&value) >= ts(&old.body),
            None => true,
        };
        if newer {
            items.insert(
                path.to_string(),
                Pending {
                    path: path.to_string(),
                    body: value,
                },
            );
            self.save(&items);
        }
    }

    pub fn is_empty(&self) -> bool {
        self.items.lock().unwrap().is_empty()
    }

    pub fn snapshot(&self) -> Vec<Pending> {
        self.items.lock().unwrap().values().cloned().collect()
    }

    /// Removes a sent write, unless a newer one replaced it meanwhile.
    pub fn done(&self, sent: &Pending) {
        let mut items = self.items.lock().unwrap();
        if items
            .get(&sent.path)
            .is_some_and(|p| ts(&p.body) == ts(&sent.body))
        {
            items.remove(&sent.path);
            self.save(&items);
        }
    }

    pub fn clear(&self) {
        let mut items = self.items.lock().unwrap();
        items.clear();
        self.save(&items);
    }
}

impl Outbox {
    /// Lays the places still waiting to be sent over a JSON answer (a book,
    /// a library page, a progress record…), where they are newer than what
    /// the answer holds: reopening a book before its place reached the
    /// server must not start from the older one (ADR-0056).
    pub fn overlay(&self, body: &[u8]) -> Option<Vec<u8>> {
        let pending: BTreeMap<String, serde_json::Value> = self
            .items
            .lock()
            .unwrap()
            .values()
            .filter_map(|p| Some((book_of(&p.path)?.to_string(), p.body.clone())))
            .collect();
        if pending.is_empty() {
            return None;
        }
        let mut v: serde_json::Value = serde_json::from_slice(body).ok()?;
        if !patch(&mut v, &pending) {
            return None;
        }
        serde_json::to_vec(&v).ok()
    }
}

/// The book id of a queued `PUT /api/v1/me/progress/{id}`.
fn book_of(path: &str) -> Option<&str> {
    path.strip_prefix("/api/v1/me/progress/")
        .map(|r| r.split(['?', '/']).next().unwrap_or(r))
        .filter(|id| !id.is_empty())
}

/// Walks a JSON answer and replaces stale progress; true when it changed something.
fn patch(v: &mut serde_json::Value, pending: &BTreeMap<String, serde_json::Value>) -> bool {
    let mut changed = false;
    match v {
        serde_json::Value::Array(items) => {
            for i in items {
                changed |= patch(i, pending);
            }
        }
        serde_json::Value::Object(map) => {
            // A bare progress record (a bookmark has a position too, but an id
            // and no client time).
            if map.contains_key("position_ms")
                && map.contains_key("client_ts")
                && !map.contains_key("id")
            {
                let book = map
                    .get("book_id")
                    .and_then(|i| i.as_str())
                    .map(str::to_string);
                if let Some((book, w)) = book.and_then(|b| pending.get(&b).map(|w| (b, w))) {
                    let cur = serde_json::Value::Object(map.clone());
                    if let Some(serde_json::Value::Object(p)) = merged(&book, &cur, w) {
                        *map = p;
                        return true;
                    }
                }
                return false;
            }
            // A book (summary or detail) with its progress.
            let id = map.get("id").and_then(|i| i.as_str()).map(str::to_string);
            if let Some((id, w)) = id.and_then(|i| pending.get(&i).map(|w| (i, w))) {
                if map.contains_key("progress") && map.contains_key("title") {
                    let cur = map
                        .get("progress")
                        .cloned()
                        .unwrap_or(serde_json::Value::Null);
                    if let Some(p) = merged(&id, &cur, w) {
                        map.insert("progress".into(), p);
                        changed = true;
                    }
                }
            }
            for (k, child) in map.iter_mut() {
                if k != "progress" {
                    changed |= patch(child, pending);
                }
            }
        }
        _ => {}
    }
    changed
}

/// The progress record with the queued write `w` applied, or None when the
/// record is already as new (another device may have listened on since).
fn merged(book: &str, cur: &serde_json::Value, w: &serde_json::Value) -> Option<serde_json::Value> {
    let wts = w.get("client_ts").and_then(|t| t.as_str())?;
    if let Some(cts) = cur.get("client_ts").and_then(|t| t.as_str()) {
        if !newer(wts, cts) {
            return None;
        }
    }
    let mut p = match cur {
        serde_json::Value::Object(m) => m.clone(),
        _ => serde_json::Map::new(),
    };
    p.insert("book_id".into(), book.into());
    for k in ["position_ms", "device_id", "client_ts", "speed"] {
        if let Some(x) = w.get(k) {
            p.insert(k.into(), x.clone());
        }
    }
    p.insert("updated_at".into(), wts.into());
    p.entry("speed").or_insert(1.0.into());
    match w.get("finished").and_then(|f| f.as_bool()) {
        Some(true) => {
            p.insert("finished_at".into(), wts.into());
        }
        Some(false) => {
            p.remove("finished_at");
        }
        None => {}
    }
    Some(serde_json::Value::Object(p))
}

/// a > b for RFC 3339 UTC times ("…:05Z", "…:05.120Z"); unreadable times count as older.
fn newer(a: &str, b: &str) -> bool {
    fn key(t: &str) -> Option<(String, String)> {
        let t = t.strip_suffix('Z').or_else(|| t.strip_suffix("+00:00"))?;
        let (whole, frac) = t.split_once('.').unwrap_or((t, ""));
        if whole.len() != 19 || !frac.bytes().all(|c| c.is_ascii_digit()) {
            return None;
        }
        Some((whole.to_string(), format!("{frac:0<9}")))
    }
    match (key(a), key(b)) {
        (Some(a), Some(b)) => a > b,
        (Some(_), None) => true,
        _ => false,
    }
}

fn ts(v: &serde_json::Value) -> String {
    v.get("client_ts")
        .and_then(|t| t.as_str())
        .unwrap_or("")
        .to_string()
}

/// `PUT /api/v1/me/progress/{id}`: the writes the outbox keeps.
pub fn is_progress_write(method: &str, path: &str) -> bool {
    method == "PUT"
        && path.starts_with("/api/v1/me/progress/")
        && path.len() > "/api/v1/me/progress/".len()
}

pub fn ensure_dir(p: &Path) {
    let _ = std::fs::create_dir_all(p);
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("sapb-test-{name}-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn outbox_keeps_the_newest_write_per_book() {
        let d = tmp("outbox");
        let o = Outbox::load(d.join("outbox.json"));
        o.push(
            "/api/v1/me/progress/a",
            br#"{"position_ms":1,"client_ts":"2026-01-01T00:00:02Z"}"#,
        );
        o.push(
            "/api/v1/me/progress/a",
            br#"{"position_ms":0,"client_ts":"2026-01-01T00:00:01Z"}"#,
        );
        o.push(
            "/api/v1/me/progress/b",
            br#"{"position_ms":5,"client_ts":"2026-01-01T00:00:03Z"}"#,
        );
        let again = Outbox::load(d.join("outbox.json"));
        let items = again.snapshot();
        assert_eq!(items.len(), 2);
        assert_eq!(items[0].body["position_ms"], 1);
        // A write sent while a newer one arrived stays queued.
        let sent = items[0].clone();
        again.push(
            "/api/v1/me/progress/a",
            br#"{"position_ms":9,"client_ts":"2026-01-01T00:00:09Z"}"#,
        );
        again.done(&sent);
        assert_eq!(again.snapshot().len(), 2);
        again.done(&again.snapshot()[0]);
        assert_eq!(again.snapshot().len(), 1);
    }

    #[test]
    fn overlay_shows_the_unsent_place_unless_the_server_has_newer() {
        let d = tmp("overlay");
        let o = Outbox::load(d.join("outbox.json"));
        assert!(o.overlay(br#"{"id":"a"}"#).is_none());
        o.push(
            "/api/v1/me/progress/a",
            br#"{"position_ms":90000,"client_ts":"2026-01-01T10:00:00.500Z","speed":1.5,"device_id":"d"}"#,
        );
        // A page with an older place for a, and b (nothing queued) untouched.
        let page = br#"{"items":[
            {"id":"a","title":"A","progress":{"book_id":"a","position_ms":5,"client_ts":"2026-01-01T10:00:00Z","speed":1.0}},
            {"id":"b","title":"B","progress":{"book_id":"b","position_ms":7,"client_ts":"2026-01-02T00:00:00Z"}}
        ]}"#;
        let v: serde_json::Value = serde_json::from_slice(&o.overlay(page).unwrap()).unwrap();
        assert_eq!(v["items"][0]["progress"]["position_ms"], 90000);
        assert_eq!(v["items"][0]["progress"]["speed"], 1.5);
        assert_eq!(v["items"][1]["progress"]["position_ms"], 7);
        let none = br#"{"id":"a","title":"A","progress":null}"#;
        let v: serde_json::Value = serde_json::from_slice(&o.overlay(none).unwrap()).unwrap();
        assert_eq!(v["progress"]["position_ms"], 90000);
        // Another device listened on later: the server's place stands.
        // A bookmark in the same book is left alone.
        let marks =
            br#"[{"id":"m","book_id":"a","position_ms":3,"updated_at":"2026-01-01T09:00:00Z"}]"#;
        assert!(o.overlay(marks).is_none());
        let later = br#"{"book_id":"a","position_ms":120000,"client_ts":"2026-01-01T11:00:00Z"}"#;
        assert!(o.overlay(later).is_none());
        let older = br#"{"book_id":"a","position_ms":1,"client_ts":"2026-01-01T09:00:00Z"}"#;
        let v: serde_json::Value = serde_json::from_slice(&o.overlay(older).unwrap()).unwrap();
        assert_eq!(v["position_ms"], 90000);
    }

    #[test]
    fn rfc3339_times_compare_by_instant() {
        assert!(newer("2026-01-01T10:00:00.5Z", "2026-01-01T10:00:00Z"));
        assert!(!newer("2026-01-01T10:00:00Z", "2026-01-01T10:00:00.001Z"));
        assert!(newer("2026-01-01T10:00:01Z", "2026-01-01T10:00:00.999Z"));
    }

    #[test]
    fn cache_round_trip_and_rules() {
        let d = tmp("cache");
        let c = Cache::new(d);
        c.put("https://s", "/api/v1/me", "application/json", b"{}");
        assert_eq!(c.get("https://s", "/api/v1/me").unwrap().1, b"{}");
        assert!(c.get("https://other", "/api/v1/me").is_none());
        assert!(Cache::cacheable(
            "/api/v1/library?x=1",
            "application/json; charset=utf-8",
            10
        ));
        assert!(!Cache::cacheable(
            "/api/v1/books/x/files/y",
            "audio/mp4",
            10
        ));
        assert!(!Cache::cacheable(
            "/api/v1/auth/login",
            "application/json",
            10
        ));
        assert!(is_progress_write("PUT", "/api/v1/me/progress/abc"));
        assert!(!is_progress_write("GET", "/api/v1/me/progress/abc"));
    }
}
