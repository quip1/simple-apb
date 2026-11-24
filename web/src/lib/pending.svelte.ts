// SPDX-License-Identifier: AGPL-3.0-or-later
// Places this device saved but the server hasn't confirmed yet (ADR-0056).
// Every position write is kept here first and only forgotten once the server
// has answered it, so a dropped connection, a server error or a closed tab
// never loses a place: it's resent on the next load, when the network comes
// back, and every minute until it arrives. They're kept per account, so a
// place is only ever sent by the account that saved it.
import { api, putProgress, type Schemas } from "../api/client";
import { readLocal, writeLocal } from "./prefs.svelte";
import { session } from "./session.svelte";

type Update = Schemas["ProgressUpdate"];

const RETRY_MS = 60_000;

/** Storage key of the signed-in account's waiting writes. */
const KEY = (): string | null => (session.me ? `sapb.player.pending.${session.me.id}` : null);

function load(): Record<string, Update> {
  const k = KEY();
  return k ? readLocal<Record<string, Update>>(k, {}) : {};
}

/** The waiting writes, one per book (the newest). Reactive copy for the places page. */
export const pending = $state<{ writes: Record<string, Update> }>({ writes: {} });

/** Re-reads the signed-in account's waiting writes (after sign-in, or on the places page). */
export function refreshPending(): void {
  pending.writes = load();
}

function save(all: Record<string, Update>): void {
  pending.writes = all;
  const k = KEY();
  if (k) writeLocal(k, all);
}

/** Remembers a write before it's sent; an older one for the same book is replaced. */
export function keep(bookId: string, body: Update): void {
  const all = { ...load() };
  const old = all[bookId];
  if (old && Date.parse(old.client_ts) > Date.parse(body.client_ts)) return;
  all[bookId] = { ...body, resolution: undefined };
  save(all);
}

/** The server answered the write stamped [clientTs] (or a newer one): forget it. */
export function settled(bookId: string, clientTs: string): void {
  const all = { ...load() };
  const w = all[bookId];
  if (!w || Date.parse(w.client_ts) > Date.parse(clientTs)) return;
  delete all[bookId];
  save(all);
}

/** This device's unsent place for a book, when newer than [serverTs]. */
export function unsentPlace(bookId: string, serverTs?: string): Update | undefined {
  const w = load()[bookId];
  if (!w) return undefined;
  if (serverTs && Date.parse(serverTs) >= Date.parse(w.client_ts)) return undefined;
  return w;
}

/** A status worth trying again later (server trouble, rate limit, sign-in lapsed). */
export function retryable(status: number): boolean {
  return status === 0 || status >= 500 || status === 429 || status === 401;
}

let flushing = false;

/** Sends every waiting write; stops at the first one that can't get through. */
export async function flushPending(): Promise<void> {
  if (flushing) return;
  flushing = true;
  try {
    for (const [bookId, body] of Object.entries(load())) {
      let status = 0;
      try {
        const res = await putProgress(api, bookId, body);
        status = res.kind === "ok" ? 200 : res.kind === "conflict" ? 409 : res.status;
      } catch {
        status = 0;
      }
      // A conflict is asked about when the book is next played; anything
      // else the server refused for good (book gone…) can't be saved.
      if (retryable(status)) break;
      settled(bookId, body.client_ts);
    }
  } finally {
    flushing = false;
    refreshPending();
  }
}

if (typeof window !== "undefined") {
  window.addEventListener("online", () => void flushPending());
  setInterval(() => {
    if (Object.keys(load()).length > 0) void flushPending();
  }, RETRY_MS);
}
