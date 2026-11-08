// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Preferences. All are kept in localStorage; the ones that make sense on every
// device (skip lengths, e-ink refresh, left-handed layout, default speed) are
// also synced through the account (`prefs` on /me, last write wins). Quality,
// layout and theme stay per device (a phone on cellular, an e-ink reader…).

import { api } from "../api/client";

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

/** Synced keys and their names on the wire (shared with the other clients). */
const SYNCED: [keyof Prefs, string][] = [
  ["skipBackS", "skip_back_s"],
  ["skipForwardS", "skip_forward_s"],
  ["einkTickS", "eink_tick_s"],
  ["leftHanded", "left_handed"],
  ["speed", "default_speed"],
];

// The account's stored prefs (other clients' keys are kept when we write).
let serverPrefs: Record<string, unknown> | null = null;
let pushTimer: ReturnType<typeof setTimeout> | undefined;

function saveLocal(): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    /* not remembered; still applies for this visit */
  }
}

/** Adopt the account's synced prefs (after sign-in or a sync hint). */
export function applyServerPrefs(p: Record<string, unknown>): void {
  serverPrefs = { ...p };
  const target = prefs as unknown as Record<string, unknown>;
  for (const [key, wire] of SYNCED) {
    const v = p[wire];
    if (v !== undefined && typeof v === typeof DEFAULTS[key]) target[key] = v;
  }
  saveLocal();
}

/** Forget the account (sign-out): stop syncing. */
export function detachServerPrefs(): void {
  serverPrefs = null;
  clearTimeout(pushTimer);
}

export function savePrefs(): void {
  saveLocal();
  if (!serverPrefs) return;
  clearTimeout(pushTimer);
  pushTimer = setTimeout(() => {
    if (!serverPrefs) return;
    const next = { ...serverPrefs };
    for (const [key, wire] of SYNCED) next[wire] = prefs[key];
    serverPrefs = next;
    void api.PATCH("/me", { body: { prefs: next } });
  }, 800);
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
