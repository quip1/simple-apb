// SPDX-License-Identifier: AGPL-3.0-or-later
//! The app's settings file and its sealed tokens.
//!
//! `config.json` (in the app's config folder) holds the server address, this
//! install's device id and the download folder. The token pair never goes
//! there: it is kept in Windows Credential Manager, per server.

use std::io::Write;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct Config {
    /// The server, as `https://host[:port][/sub/path]` with no trailing slash.
    pub server_url: Option<String>,
    /// Generated once per install (sessions and progress are per device).
    pub device_id: String,
    /// The signed-in user (downloads are kept per account).
    #[serde(default)]
    pub user_id: Option<String>,
    /// Where downloaded books go; None means the default folder.
    pub download_dir: Option<PathBuf>,
    /// The last update the user dismissed, so it isn't offered again.
    #[serde(default)]
    pub skipped_update: Option<String>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Tokens {
    pub access: String,
    pub refresh: String,
}

pub fn load(dir: &Path) -> Config {
    let mut cfg: Config = std::fs::read(dir.join("config.json"))
        .ok()
        .and_then(|b| serde_json::from_slice(&b).ok())
        .unwrap_or_default();
    if cfg.device_id.is_empty() {
        cfg.device_id = uuid::Uuid::new_v4().to_string();
        let _ = save(dir, &cfg);
    }
    cfg
}

pub fn save(dir: &Path, cfg: &Config) -> std::io::Result<()> {
    write_atomic(&dir.join("config.json"), &serde_json::to_vec_pretty(cfg)?)
}

/// Temp file → fsync → rename → fsync the folder, so a power cut leaves the
/// old file or the new one, never half of one.
pub fn write_atomic(path: &Path, bytes: &[u8]) -> std::io::Result<()> {
    let dir = path.parent().unwrap_or(Path::new("."));
    std::fs::create_dir_all(dir)?;
    let tmp = dir.join(format!(
        ".{}.tmp",
        path.file_name().and_then(|n| n.to_str()).unwrap_or("file")
    ));
    {
        let mut f = std::fs::File::create(&tmp)?;
        f.write_all(bytes)?;
        f.sync_all()?;
    }
    std::fs::rename(&tmp, path)?;
    // Windows can't open a folder to fsync it; NTFS journals the rename.
    #[cfg(not(windows))]
    std::fs::File::open(dir)?.sync_all()?;
    Ok(())
}

fn entry(server: &str) -> keyring::Result<keyring::Entry> {
    keyring::Entry::new("Simple APB", server)
}

pub fn tokens(server: &str) -> Option<Tokens> {
    let s = entry(server).ok()?.get_password().ok()?;
    serde_json::from_str(&s).ok()
}

pub fn save_tokens(server: &str, t: &Tokens) {
    if let Ok(e) = entry(server) {
        if let Err(err) = e.set_password(&serde_json::to_string(t).unwrap_or_default()) {
            log::warn!("could not keep the sign-in: {err}");
        }
    }
}

pub fn forget_tokens(server: &str) {
    if let Ok(e) = entry(server) {
        let _ = e.delete_credential();
    }
}

/// `https://Books.Example.com:443/sub/` → `https://books.example.com/sub`.
/// Adds https:// when no scheme is given and drops a pasted `/api/v1`.
pub fn normalize_server(input: &str) -> Option<String> {
    let s = input.trim();
    if s.is_empty() {
        return None;
    }
    let with_scheme = if s.contains("://") {
        s.to_string()
    } else {
        format!("https://{s}")
    };
    let mut u = url::Url::parse(&with_scheme).ok()?;
    if !matches!(u.scheme(), "http" | "https") || u.host_str().is_none() {
        return None;
    }
    u.set_query(None);
    u.set_fragment(None);
    let mut path = u.path().trim_end_matches('/').to_string();
    if let Some(p) = path.strip_suffix("/api/v1") {
        path = p.to_string();
    }
    u.set_path(&path);
    Some(u.as_str().trim_end_matches('/').to_string())
}

#[cfg(test)]
mod tests {
    use super::normalize_server;

    #[test]
    fn server_addresses() {
        assert_eq!(
            normalize_server("books.example.com").as_deref(),
            Some("https://books.example.com")
        );
        assert_eq!(
            normalize_server("HTTP://Books.Example.com:8080/").as_deref(),
            Some("http://books.example.com:8080")
        );
        assert_eq!(
            normalize_server("https://x.org/apb/api/v1?a=1").as_deref(),
            Some("https://x.org/apb")
        );
        assert_eq!(normalize_server("ftp://x.org"), None);
        assert_eq!(normalize_server("  "), None);
    }
}
