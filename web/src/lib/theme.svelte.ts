// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Theme switch. public/theme-init.js applies the saved theme before first
// paint; this module keeps it reactive for components.

export type Theme = "dark" | "eink";

export function getTheme(): Theme {
  return document.documentElement.dataset.theme === "eink" ? "eink" : "dark";
}

export const themeState = $state<{ theme: Theme }>({ theme: getTheme() });

export function setTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  themeState.theme = theme;
  try {
    localStorage.setItem("sapb.theme", theme);
  } catch {
    /* private mode: theme just isn't remembered */
  }
}
