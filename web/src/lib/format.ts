// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Time and size formatting. Numbers go through Intl so they follow the locale.

import { t } from "./i18n";

/** 1:02:03 or 2:03 — a clock-style position. */
export function clock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  return `${h > 0 ? `${h}:` : ""}${mm}:${String(s).padStart(2, "0")}`;
}

/** "5 h 12 min" — a human duration. */
export function duration(ms: number): string {
  const mins = Math.round(ms / 60000);
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  if (h === 0) return t("time.minutes", { m });
  if (m === 0) return t("time.hours", { h });
  return t("time.hours_minutes", { h, m });
}

/** Spoken form for screen readers ("1 hour 2 minutes 3 seconds"). */
export function spokenClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  return t("time.spoken", {
    h: Math.floor(total / 3600),
    m: Math.floor((total % 3600) / 60),
    s: total % 60,
  });
}

export function percent(part: number, whole: number): number {
  if (whole <= 0) return 0;
  return Math.min(100, Math.max(0, Math.round((part / whole) * 100)));
}

/** "Dune Chronicles, book 2", or just the series name without a position. */
export function seriesLabel(s: { name: string; index?: string }): string {
  return s.index ? t("book.series_position", { series: s.name, index: s.index }) : s.name;
}
