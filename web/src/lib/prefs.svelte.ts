// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Per-browser preferences. They are conveniences for this device only, so
// they live in localStorage (the brief's synced prefs come with the account
// settings work).

export type Quality = "auto" | "original" | "medium" | "low";
export type LibraryView = "grid" | "list";

export interface Prefs {
  /** "auto" = original, but medium on a cellular connection (brief §6.9). */
  quality: Quality;
  skipBackS: number;
  skipForwardS: number;
  /** E-ink: how often the time display refreshes, 10–30 s (brief §11). */
  einkTickS: number;
  /** Mirror the player controls for left-handed use (brief §11). */
  leftHanded: boolean;
  libraryView: LibraryView;
  speed: number;
}

const KEY = "sapb.prefs";
const DEFAULTS: Prefs = {
  quality: "auto",
  skipBackS: 15,
  skipForwardS: 30,
  einkTickS: 15,
  leftHanded: false,
  libraryView: "grid",
  speed: 1,
};

function load(): Prefs {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) return { ...DEFAULTS, ...(JSON.parse(raw) as Partial<Prefs>) };
  } catch {
    /* unreadable or private mode: defaults */
  }
  return { ...DEFAULTS };
}

export const prefs = $state<Prefs>(load());

export function savePrefs(): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    /* not remembered; still applies for this visit */
  }
}

/** Small JSON helpers for other per-browser state. */
export function readLocal<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

export function writeLocal(key: string, value: unknown): void {
  try {
    if (value === undefined) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* ignore */
  }
}
