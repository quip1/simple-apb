// SPDX-License-Identifier: AGPL-3.0-or-later
//! Native strings (tray menu, dialogs) from the shared catalog, i18n/en.json.

use std::collections::HashMap;
use std::sync::OnceLock;

static EN: &str = include_str!("../../../i18n/en.json");

fn catalog() -> &'static HashMap<String, String> {
    static C: OnceLock<HashMap<String, String>> = OnceLock::new();
    C.get_or_init(|| serde_json::from_str(EN).unwrap_or_default())
}

/// The English text for `key` (the key itself if it's missing).
pub fn t(key: &str) -> String {
    catalog()
        .get(key)
        .cloned()
        .unwrap_or_else(|| key.to_string())
}

#[cfg(test)]
mod tests {
    #[test]
    fn tray_strings_exist() {
        for k in [
            "desktop.tray.play",
            "desktop.tray.pause",
            "desktop.tray.back",
            "desktop.tray.forward",
            "desktop.tray.show",
            "desktop.tray.mini",
            "desktop.tray.quit",
        ] {
            assert_ne!(super::t(k), k, "{k} missing from i18n/en.json");
        }
    }
}
