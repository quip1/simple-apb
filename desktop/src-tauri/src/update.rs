// SPDX-License-Identifier: AGPL-3.0-or-later
//! Updates (brief §10). The check compares this version with the newest
//! GitHub release (ADR-0047). "Update now" then downloads the release's
//! signed installer with Tauri's updater, checks its signature against the
//! public key in tauri.conf.json, and runs it: a progress bar, no questions,
//! and the new version opens with the same settings and downloads
//! (ADR-0052). Nothing installs without the user asking.

use std::sync::Mutex;

use serde_json::{json, Value};
use tauri::AppHandle;
use tauri_plugin_updater::UpdaterExt;

use crate::Shared;

#[derive(Clone, Copy, PartialEq)]
enum Phase {
    Idle,
    Downloading,
    Installing,
    Failed,
}

struct Install {
    phase: Phase,
    done: u64,
    total: u64,
}

static INSTALL: Mutex<Install> = Mutex::new(Install {
    phase: Phase::Idle,
    done: 0,
    total: 0,
});

/// Where "Update now" is: idle, downloading (with a percentage), installing or failed.
pub fn progress() -> Value {
    let i = INSTALL.lock().unwrap();
    let phase = match i.phase {
        Phase::Idle => "idle",
        Phase::Downloading => "downloading",
        Phase::Installing => "installing",
        Phase::Failed => "failed",
    };
    let percent = (i.done * 100).checked_div(i.total).unwrap_or(0).min(100);
    json!({ "phase": phase, "percent": percent })
}

/// Starts downloading and installing the newest release; false if one is under way.
pub fn install(app: &AppHandle) -> bool {
    {
        let mut i = INSTALL.lock().unwrap();
        if matches!(i.phase, Phase::Downloading | Phase::Installing) {
            return false;
        }
        *i = Install {
            phase: Phase::Downloading,
            done: 0,
            total: 0,
        };
    }
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        if let Err(e) = download_and_install(&app).await {
            log::warn!("update failed: {e}");
            INSTALL.lock().unwrap().phase = Phase::Failed;
        }
    });
    true
}

async fn download_and_install(app: &AppHandle) -> Result<(), String> {
    let update = app
        .updater()
        .map_err(|e| e.to_string())?
        .check()
        .await
        .map_err(|e| e.to_string())?
        .ok_or("no signed update is published")?;
    let bytes = update
        .download(
            |chunk, total| {
                let mut i = INSTALL.lock().unwrap();
                i.done += chunk as u64;
                i.total = total.unwrap_or(0);
            },
            || INSTALL.lock().unwrap().phase = Phase::Installing,
        )
        .await
        .map_err(|e| e.to_string())?;
    // On Windows this hands over to the installer, which closes this app,
    // installs over it and opens the new version.
    update.install(bytes).map_err(|e| e.to_string())?;
    app.restart()
}

const RELEASES: &str = "https://api.github.com/repos/quip1/simple-apb/releases/latest";

pub async fn check(s: &Shared) -> Value {
    let none = json!({ "available": false });
    let Ok(res) = s
        .http
        .get(RELEASES)
        .header("User-Agent", "simple-apb-desktop")
        .header("Accept", "application/vnd.github+json")
        .timeout(std::time::Duration::from_secs(10))
        .send()
        .await
    else {
        return none;
    };
    if !res.status().is_success() {
        return none;
    }
    let v: Value = res.json().await.unwrap_or(Value::Null);
    let tag = v["tag_name"]
        .as_str()
        .unwrap_or("")
        .trim_start_matches('v')
        .to_string();
    let url = v["html_url"].as_str().unwrap_or("").to_string();
    let skipped = s.cfg.lock().unwrap().skipped_update.clone();
    if newer(&tag, env!("CARGO_PKG_VERSION"))
        && skipped.as_deref() != Some(tag.as_str())
        && url.starts_with("https://github.com/")
    {
        json!({ "available": true, "version": tag, "url": url })
    } else {
        none
    }
}

/// Semver "a.b.c" (a pre-release suffix counts as older than the release).
pub fn newer(candidate: &str, current: &str) -> bool {
    fn parse(v: &str) -> Option<(u64, u64, u64, bool)> {
        let (core, pre) = match v.split_once('-') {
            Some((c, _)) => (c, true),
            None => (v, false),
        };
        let mut it = core.split('.').map(|p| p.parse::<u64>().ok());
        Some((it.next()??, it.next()??, it.next()??, !pre))
    }
    match (parse(candidate), parse(current)) {
        (Some(a), Some(b)) => a > b,
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::newer;

    #[test]
    fn versions() {
        assert!(newer("0.5.0", "0.4.0"));
        assert!(newer("1.0.0", "0.9.9"));
        assert!(newer("0.4.0", "0.4.0-rc.1"));
        assert!(!newer("0.4.0", "0.4.0"));
        assert!(!newer("0.4.0-rc.1", "0.4.0"));
        assert!(!newer("garbage", "0.4.0"));
    }
}
