// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Live sync hints. The server's /ws socket sends {"type":"changed"} when
// something of ours changed on another device; screens watch `live.tick` and
// reload. Reconnects with backoff; a failed connection refreshes the session
// first in case the access cookie expired.
//
// The server also hints this tab about its own writes (it can't tell tabs
// apart). Hints arriving shortly after one of our writes are echoes and are
// dropped (the server polls every 500 ms); bursts are coalesced.

import { DESKTOP } from "./desktop.svelte";
import { api } from "../api/client";

export const live = $state({ tick: 0, connected: false });

const ECHO_MS = 1500;
const COALESCE_MS = 300;

let socket: WebSocket | null = null;
let retry = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
let bump: ReturnType<typeof setTimeout> | undefined;
let wanted = false;
let lastOwnWrite = 0;

window.addEventListener("sapb:wrote", () => (lastOwnWrite = Date.now()));

function onHint(): void {
  if (Date.now() - lastOwnWrite < ECHO_MS) return;
  clearTimeout(bump);
  bump = setTimeout(() => live.tick++, COALESCE_MS);
}

function connect(): void {
  if (!wanted || socket) return;
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${proto}//${location.host}/ws`);
  socket = ws;
  ws.onopen = () => {
    retry = 0;
    live.connected = true;
  };
  ws.onmessage = (e) => {
    try {
      const msg = JSON.parse(String(e.data)) as { type?: string };
      if (msg.type === "changed") onHint();
    } catch {
      /* ignore malformed hints */
    }
  };
  ws.onclose = () => {
    socket = null;
    live.connected = false;
    if (!wanted) return;
    const delay = Math.min(30_000, 1000 * 2 ** retry++);
    // A cheap authed call refreshes an expired access cookie before retrying.
    timer = setTimeout(
      () =>
        void api
          .GET("/me")
          .catch(() => undefined)
          .finally(connect),
      delay,
    );
  };
}

// The Windows app has no socket to its server (its requests go through the
// app, ADR-0047): it checks for changes when the window comes back and every
// two minutes.
let poll: ReturnType<typeof setInterval> | undefined;
const bumpNow = () => live.tick++;

export function startLive(): void {
  wanted = true;
  if (DESKTOP) {
    window.addEventListener("focus", bumpNow);
    poll ??= setInterval(bumpNow, 120_000);
    return;
  }
  connect();
}

export function stopLive(): void {
  wanted = false;
  window.removeEventListener("focus", bumpNow);
  clearInterval(poll);
  poll = undefined;
  clearTimeout(timer);
  socket?.close();
  socket = null;
}
