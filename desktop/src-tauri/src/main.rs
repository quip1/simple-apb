// SPDX-License-Identifier: AGPL-3.0-or-later
// No console window in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    simple_apb_desktop_lib::run();
}
