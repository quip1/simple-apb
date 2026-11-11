// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The Windows app (desktop/, ADR-0047). The same web app runs there; this
// module is its link to the native side, through the app's own `/desktop/…`
// endpoints (no Tauri JS API). In the server's build `DESKTOP` is false and
// all of this is dead code that the bundler drops.

export const DESKTOP = __DESKTOP__;

export interface DesktopInfo {
  server_url: string | null;
  version: string;
  download_dir: string;
  online: boolean;
  waiting_writes: number;
}

export interface DownloadJob {
  book_id: string;
  title: string;
  status: "queued" | "downloading" | "done" | "failed";
  done_bytes: number;
  total_bytes: number;
  error?: string | null;
}

export interface UpdateInfo {
  available: boolean;
  version?: string;
  url?: string;
}

export const desktop = $state<{
  info: DesktopInfo | null;
  downloads: Record<string, DownloadJob>;
  update: UpdateInfo | null;
  /** Showing the server screen (first run, or "Use another server"). */
  choosingServer: boolean;
}>({ info: null, downloads: {}, update: null, choosingServer: false });

async function call<T>(method: string, path: string, body?: unknown): Promise<T | null> {
  try {
    const res = await fetch(`/desktop/${path}`, {
      method,
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return (await res.json().catch(() => null)) as T | null;
  } catch {
    return null;
  }
}

export async function loadInfo(): Promise<DesktopInfo | null> {
  desktop.info = await call<DesktopInfo>("GET", "info");
  return desktop.info;
}

export type ServerError = "invalid_url" | "unreachable" | "not_simpleapb";

/** Chooses the server; the native side checks it answers as Simple APB. */
export async function setServer(url: string): Promise<ServerError | null> {
  try {
    const res = await fetch("/desktop/server", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url }),
    });
    if (res.ok) {
      await loadInfo();
      return null;
    }
    const e = (await res.json().catch(() => ({}))) as { code?: ServerError };
    return e.code ?? "unreachable";
  } catch {
    return "unreachable";
  }
}

// ── downloads ──

let polling: ReturnType<typeof setInterval> | null = null;

export async function refreshDownloads(): Promise<void> {
  const list = (await call<DownloadJob[]>("GET", "downloads")) ?? [];
  desktop.downloads = Object.fromEntries(list.map((j) => [j.book_id, j]));
  const active = list.some((j) => j.status === "queued" || j.status === "downloading");
  if (active && !polling) polling = setInterval(() => void refreshDownloads(), 1000);
  if (!active && polling) {
    clearInterval(polling);
    polling = null;
  }
}

export function isDownloaded(bookId: string): boolean {
  return desktop.downloads[bookId]?.status === "done";
}

export async function download(bookId: string): Promise<void> {
  await call("POST", `downloads/${encodeURIComponent(bookId)}`);
  await refreshDownloads();
}

export async function removeDownload(bookId: string): Promise<void> {
  await call("DELETE", `downloads/${encodeURIComponent(bookId)}`);
  await refreshDownloads();
}

/** Opens the folder picker; downloads then go to <folder>\Simple APB. */
export async function pickDownloadFolder(): Promise<void> {
  await call("POST", "download-dir");
  await loadInfo();
  await refreshDownloads();
}

// ── links, updates ──

export function openExternal(url: string): void {
  void call("POST", "open", { url });
}

export async function checkUpdate(): Promise<void> {
  desktop.update = await call<UpdateInfo>("GET", "update");
}

export async function skipUpdate(version: string): Promise<void> {
  await call("POST", "update/skip", { version });
  desktop.update = null;
}

// ── the player outside the window (tray, media keys, mini player) ──

export interface PlayerSnapshot {
  book_id: string | null;
  title?: string;
  author?: string;
  series?: string;
  cover?: string;
  playing: boolean;
  position_ms: number;
  duration_ms: number;
}

let lastSent = "";
let lastAt = 0;

/** Tells the native side what's playing (throttled; immediate on changes that matter). */
export function postPlayer(s: PlayerSnapshot): void {
  const key = JSON.stringify({ ...s, position_ms: 0 });
  const now = Date.now();
  if (key === lastSent && now - lastAt < 5000) return;
  lastSent = key;
  lastAt = now;
  void call("POST", "player", s);
}

export function sendCommand(action: string): void {
  void call("POST", "command", { action });
}

export function toggleMini(): void {
  void call("POST", "mini");
}

export async function currentPlayer(): Promise<PlayerSnapshot | null> {
  return call<PlayerSnapshot>("GET", "player");
}
