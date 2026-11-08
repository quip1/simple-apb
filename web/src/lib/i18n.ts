// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Every user-facing string goes through t() (CLAUDE.md). Messages are ICU
// MessageFormat in /i18n/<locale>.json, shared with the other clients.

import { IntlMessageFormat } from "intl-messageformat";
import en from "../../../i18n/en.json";

type Catalog = Record<string, string>;

const catalogs: Record<string, Catalog> = { en: en as Catalog };
let locale = "en";
const cache = new Map<string, IntlMessageFormat>();

export function setLocale(l: string): void {
  if (catalogs[l]) {
    locale = l;
    cache.clear();
    document.documentElement.lang = l;
  }
}

/** Translate a key. Missing keys render as the key itself (visible in review). */
export function t(key: string, values?: Record<string, string | number | Date>): string {
  const msg = catalogs[locale]?.[key] ?? catalogs.en?.[key];
  if (msg === undefined) {
    if (import.meta.env.DEV) console.warn(`i18n: missing key ${key}`);
    return key;
  }
  if (!values) return msg;
  let f = cache.get(key);
  if (!f) {
    f = new IntlMessageFormat(msg, locale);
    cache.set(key, f);
  }
  return String(f.format(values));
}

/** Translate a server error code (falls back to the server's English message). */
export function tError(code: string | undefined, fallback?: string): string {
  if (code) {
    const k = `error.${code}`;
    if (catalogs[locale]?.[k] ?? catalogs.en?.[k]) return t(k);
  }
  return fallback ?? t("error.generic");
}
