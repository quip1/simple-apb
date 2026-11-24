// SPDX-License-Identifier: AGPL-3.0-or-later
// Places this device saved but the server hasn't confirmed yet (ADR-0056).
// Every position write is kept here first and only forgotten once the server
// has answered it, so a dropped connection, a server error or a closed tab
// never loses a place: it's resent on the next load, when the network comes
// back, and every minute until it arrives.
import { api, putProgress, type Schemas } from "../api/client";
import { readLocal, writeLocal } from "./prefs.svelte";

type Update = Schemas["ProgressUpdate"];

const KEY = "sapb.player.pending";
const RETRY_MS = 60_000;

/** The waiting writes, one per book (the newest). Reactive copy for the sync page. */
export const pending = $state<{ writes: Record<string, Update> }>({ writes: readLocal(KEY, {}) });

function save(all: Record<string, Update>): void {
  pending.writes = all;
  writeLocal(KEY, all);
}

/** Remembers a write before it's sent; an older one for the same book is replaced. */
export function keep(bookId: string, body: Update): void {
  const all = { ...readLocal<Record<string, Update>>(KEY, {}) };
  const old = all[bookId];
  if (old && Date.parse(old.client_ts) > Date.parse(body.client_ts)) return;
  all[bookId] = { ...body, resolution: undefined };
  save(all);
}

/** The server answered the write stamped [clientTs] (or a newer one): forget it. */
export function settled(bookId: string, clientTs: string): void {
  const all = { ...readLocal<Record<string, Update>>(KEY, {}) };
  const w = all[bookId];
  if (!w || Date.parse(w.client_ts) > Date.parse(clientTs)) return;
  delete all[bookId];
  save(all);
}

/** This device's unsent place for a book, when newer than [serverTs]. */
export function unsentPlace(bookId: string, serverTs?: string): Update | undefined {
  const w = readLocal<Record<string, Update>>(KEY, {})[bookId];
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
    for (const [bookId, body] of Object.entries(readLocal<Record<string, Update>>(KEY, {}))) {
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
  }
}

if (typeof window !== "undefined") {
  window.addEventListener("online", () => void flushPending());
  setInterval(() => {
    if (Object.keys(pending.writes).length > 0) void flushPending();
  }, RETRY_MS);
}
