// SPDX-License-Identifier: AGPL-3.0-or-later
// Apply the saved theme before first paint (no flash; e-ink screens hate
// extra repaints). External file so the Content-Security-Policy can forbid
// inline scripts.
try {
  var t = localStorage.getItem("sapb.theme");
  if (t === "eink" || t === "dark") document.documentElement.dataset.theme = t;
} catch {
  /* private mode */
}
