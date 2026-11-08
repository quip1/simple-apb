// SPDX-License-Identifier: AGPL-3.0-or-later

export type Theme = "dark" | "eink";

export function getTheme(): Theme {
  return document.documentElement.dataset.theme === "eink" ? "eink" : "dark";
}

export function setTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem("sapb.theme", theme);
  } catch {
    /* private mode: theme just isn't remembered */
  }
}
