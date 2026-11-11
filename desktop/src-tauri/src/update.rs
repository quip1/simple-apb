// SPDX-License-Identifier: AGPL-3.0-or-later
//! The update check (brief §10): compares this version with the newest
//! GitHub release and offers the download page. Nothing is installed
//! automatically (ADR-0047).

use serde_json::{json, Value};

use crate::Shared;

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
