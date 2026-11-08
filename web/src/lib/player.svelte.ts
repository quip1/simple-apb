// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The web player engine (one per tab). Plays a book on its global timeline:
//
//   original — each audio file in turn on one <audio> element, mapped onto the
//              book timeline with the files' `offset_ms` (brief §6.9);
//   hls      — the server's low-bitrate HLS playlist (whole book) through
//              hls.js, or natively where the browser supports HLS (Safari).
//
// Media is fetched with signed stream URLs (6 h, bound to user + book) rather
// than the 15-minute access cookie, so a long chapter never fails mid-way when
// the cookie expires. Progress is sent every 15 s while playing, on
// pause/seek/end and when the tab is hidden (brief §6.8); a 409 conflict
// is surfaced as `player.conflict` for the prompt.

import type HlsType from "hls.js";
import { api, deviceId, putProgress, readCookie, type Book, type ProgressConflict } from "../api/client";
import { clock } from "./format";
import { t } from "./i18n";
import { prefs, readLocal, writeLocal, type Quality } from "./prefs.svelte";
import { getTheme } from "./theme.svelte";

type Profile = "original" | "medium" | "low";
type Chapter = Book["chapters"][number];
export type SleepTimer = { kind: "time"; endsAt: number } | { kind: "chapter"; endMs: number } | null;

export const SPEEDS = [0.75, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3];
export const SLEEP_MINUTES = [5, 10, 15, 30, 45, 60];

const LAST_KEY = "sapb.player.last";
const SYNCED_KEY = "sapb.player.synced";
const TICK_MS = 15_000;
const FADE_MS = 10_000;

export const player = $state({
  book: null as Book | null,
  profile: "original" as Profile,
  playing: false,
  buffering: false,
  /** Live position on the book timeline. */
  positionMs: 0,
  /** What the UI shows; in e-ink it refreshes only every few seconds. */
  displayMs: 0,
  speed: 1,
  error: "",
  sleep: null as SleepTimer,
  conflict: null as ProgressConflict | null,
  /** Polite live-region text for screen readers. */
  announce: "",
});

// ── engine state (not reactive) ──
let audio: HTMLAudioElement | null = null;
let hls: HlsType | null = null;
let fileIdx = 0;
let urls: { files: Map<string, string>; hls?: string; cover?: string; expires: number } | null = null;
let pendingLocalS: number | null = null;
let wantPlay = false;
let lastSentMs = -1;
let lastDisplayAt = 0;
let lastChapter = -1;
let tickTimer: ReturnType<typeof setInterval> | undefined;
let seekTimer: ReturnType<typeof setTimeout> | undefined;
let retriedAt = 0;

// ── timeline helpers ──

export function chapterIndexAt(book: Book | null, ms: number): number {
  if (!book) return -1;
  const chs = book.chapters;
  for (let i = chs.length - 1; i >= 0; i--) if (ms >= chs[i]!.start_ms) return i;
  return chs.length ? 0 : -1;
}

export function currentChapter(): Chapter | null {
  const i = chapterIndexAt(player.book, player.positionMs);
  return i >= 0 ? (player.book?.chapters[i] ?? null) : null;
}

function fileIndexAt(book: Book, ms: number): number {
  const fs = book.files;
  for (let i = fs.length - 1; i >= 0; i--) if (ms >= fs[i]!.offset_ms) return i;
  return 0;
}

function durationMs(): number {
  return player.book?.duration_ms ?? 0;
}

/** Keep URLs on this origin (the SPA is served by the same server). */
function local(u: string): string {
  try {
    const parsed = new URL(u, location.href);
    return parsed.pathname + parsed.search;
  } catch {
    return u;
  }
}

function chooseProfile(book: Book, q: Quality): Profile {
  const probe = document.createElement("audio");
  const playable = book.files.length > 0 && book.files.every((f) => probe.canPlayType(f.mime_type) !== "");
  if (q === "low" || q === "medium") return q;
  // Original can't play here (e.g. a codec this browser lacks): fall back to HLS.
  if (!playable) return "medium";
  if (q === "original") return "original";
  const conn = (navigator as Navigator & { connection?: { type?: string } }).connection;
  return conn?.type === "cellular" ? "medium" : "original";
}

async function signedUrls(book: Book, profile: Profile): Promise<boolean> {
  const { data } = await api.POST("/books/{bookId}/stream-url", {
    params: { path: { bookId: book.id } },
    body: { profile },
  });
  if (!data) return false;
  const files = new Map<string, string>();
  for (const f of data.files ?? []) files.set(f.file_id, local(f.url));
  urls = {
    files,
    hls: data.hls_url ? local(data.hls_url) : undefined,
    cover: data.cover_url ? local(data.cover_url) : undefined,
    // Renew 10 minutes early.
    expires: Date.parse(data.expires_at) - 10 * 60_000,
  };
  return true;
}

async function freshUrls(): Promise<boolean> {
  if (!player.book) return false;
  if (urls && Date.now() < urls.expires) return true;
  return signedUrls(player.book, player.profile);
}

// ── audio element ──

function el(): HTMLAudioElement {
  if (audio) return audio;
  const a = new Audio();
  a.preload = "auto";
  a.addEventListener("loadedmetadata", () => {
    if (pendingLocalS !== null) {
      a.currentTime = pendingLocalS;
      pendingLocalS = null;
    }
    a.playbackRate = player.speed;
    if (wantPlay) void a.play().catch(onPlayRejected);
  });
  a.addEventListener("timeupdate", onTime);
  a.addEventListener("playing", () => {
    player.buffering = false;
    setPlaying(true);
  });
  a.addEventListener("pause", () => {
    // A pause between files of one book is not a user pause.
    if (a.ended) return;
    setPlaying(false);
  });
  a.addEventListener("waiting", () => (player.buffering = true));
  a.addEventListener("ended", onEnded);
  a.addEventListener("error", () => void onError());
  audio = a;
  return a;
}

function onPlayRejected(err: unknown): void {
  // Autoplay without a user gesture is blocked by browsers: stay paused.
  if (err instanceof DOMException && err.name === "NotAllowedError") {
    wantPlay = false;
    setPlaying(false);
    return;
  }
  void onError();
}

function setPlaying(on: boolean): void {
  if (player.playing === on) return;
  player.playing = on;
  player.announce = on ? t("player.announce_playing", { title: player.book?.title ?? "" }) : t("player.announce_paused");
  if (on) {
    clearInterval(tickTimer);
    tickTimer = setInterval(() => void sendProgress(), TICK_MS);
  } else {
    clearInterval(tickTimer);
    void sendProgress();
  }
  updateDisplay(true);
  mediaSessionState();
}

function onTime(): void {
  const a = audio;
  const book = player.book;
  if (!a || !book || pendingLocalS !== null) return;
  const base = player.profile === "original" ? (book.files[fileIdx]?.offset_ms ?? 0) : 0;
  player.positionMs = Math.min(durationMs(), Math.round(base + a.currentTime * 1000));
  updateDisplay(false);
  checkSleep();
  const ch = chapterIndexAt(book, player.positionMs);
  if (ch !== lastChapter) {
    if (lastChapter !== -1 && ch >= 0) player.announce = book.chapters[ch]!.title;
    lastChapter = ch;
  }
}

function updateDisplay(force: boolean): void {
  const now = Date.now();
  const eink = getTheme() === "eink";
  const every = eink ? Math.min(30, Math.max(10, prefs.einkTickS)) * 1000 : 250;
  if (force || now - lastDisplayAt >= every) {
    player.displayMs = player.positionMs;
    lastDisplayAt = now;
    if (!eink || force) mediaSessionPosition();
  }
}

async function onEnded(): Promise<void> {
  const book = player.book;
  if (!book) return;
  if (player.profile === "original" && fileIdx < book.files.length - 1) {
    await loadFile(fileIdx + 1, 0, true);
    return;
  }
  player.positionMs = durationMs();
  wantPlay = false;
  setPlaying(false);
  await sendProgress({ finished: true, force: true });
  player.announce = t("player.announce_finished", { title: book.title });
}

async function onError(): Promise<void> {
  const a = audio;
  if (!a || !player.book) return;
  const code = a.error?.code;
  // Original codec unsupported here: switch to HLS once.
  if (code === MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED && player.profile === "original") {
    await switchProfile("medium");
    return;
  }
  // Expired or revoked link, or a network blip: renew and retry once a minute.
  if (Date.now() - retriedAt > 60_000) {
    retriedAt = Date.now();
    urls = null;
    const resume = wantPlay;
    await seekTo(player.positionMs, { play: resume, send: false });
    return;
  }
  player.error = t("player.error_playback");
  wantPlay = false;
  setPlaying(false);
}

async function loadFile(idx: number, localS: number, play: boolean): Promise<void> {
  const book = player.book;
  if (!book || !(await freshUrls()) || !urls) return;
  const f = book.files[idx];
  if (!f) return;
  const src = urls.files.get(f.id);
  if (!src) return;
  const a = el();
  fileIdx = idx;
  pendingLocalS = localS;
  wantPlay = play;
  a.src = src;
  a.load();
}

async function loadHls(startS: number, play: boolean): Promise<void> {
  if (!(await freshUrls()) || !urls?.hls) return;
  const a = el();
  wantPlay = play;
  hls?.destroy();
  hls = null;
  const { default: Hls } = await import("hls.js/light");
  if (Hls.isSupported()) {
    const h = new Hls({ startPosition: startS, maxBufferLength: 60 });
    h.on(Hls.Events.ERROR, (_e, data) => {
      if (data.fatal) void onError();
    });
    h.loadSource(urls.hls);
    h.attachMedia(a);
    hls = h;
    pendingLocalS = startS;
  } else if (a.canPlayType("application/vnd.apple.mpegurl")) {
    pendingLocalS = startS;
    a.src = urls.hls;
    a.load();
  } else {
    player.error = t("player.error_unsupported");
  }
}

// ── public API ──

/** Open a book. Starts at `startMs`, else at the stored progress. */
export async function openBook(book: Book, opts: { startMs?: number; play?: boolean } = {}): Promise<void> {
  if (player.book && player.book.id !== book.id) await closeBook();
  const same = player.book?.id === book.id;
  player.book = book;
  player.error = "";
  if (!same) {
    player.conflict = null;
    player.sleep = null;
    player.speed = book.progress?.speed ?? prefs.speed;
    player.profile = chooseProfile(book, prefs.quality);
    urls = null;
    lastSentMs = -1;
    lastChapter = -1;
    retriedAt = 0;
  }
  const finished = !!book.progress?.finished_at;
  const stored = finished ? 0 : (book.progress?.position_ms ?? 0);
  const start = Math.min(opts.startMs ?? (same ? player.positionMs : stored), Math.max(0, book.duration_ms - 1000));
  writeLocal(LAST_KEY, { bookId: book.id });
  mediaSessionMetadata();
  await seekTo(start, { play: opts.play ?? false, send: false });
}

export async function closeBook(): Promise<void> {
  if (!player.book) return;
  audio?.pause();
  await sendProgress({ force: true });
  hls?.destroy();
  hls = null;
  if (audio) {
    audio.removeAttribute("src");
    audio.load();
  }
  clearInterval(tickTimer);
  player.book = null;
  player.playing = false;
  writeLocal(LAST_KEY, undefined);
  if ("mediaSession" in navigator) navigator.mediaSession.metadata = null;
}

/** Restore the last book (paused) after a reload. */
export async function restoreLastBook(): Promise<void> {
  const last = readLocal<{ bookId?: string } | null>(LAST_KEY, null);
  if (!last?.bookId || player.book) return;
  const { data } = await api.GET("/books/{bookId}", { params: { path: { bookId: last.bookId } } });
  if (data) await openBook(data, { play: false });
  else writeLocal(LAST_KEY, undefined);
}

export function play(): void {
  const a = audio;
  if (!player.book) return;
  player.error = "";
  wantPlay = true;
  if (!a || (!a.src && !hls)) {
    void seekTo(player.positionMs, { play: true, send: false });
    return;
  }
  void a.play().catch(onPlayRejected);
}

export function pause(): void {
  wantPlay = false;
  audio?.pause();
}

export function toggle(): void {
  if (player.playing) pause();
  else play();
}

export async function seekTo(ms: number, opts: { play?: boolean; send?: boolean } = {}): Promise<void> {
  const book = player.book;
  if (!book) return;
  const target = Math.max(0, Math.min(ms, Math.max(0, book.duration_ms - 250)));
  const play = opts.play ?? wantPlay;
  player.positionMs = target;
  updateDisplay(true);
  const a = el();
  if (player.profile === "original") {
    const idx = fileIndexAt(book, target);
    const localS = (target - (book.files[idx]?.offset_ms ?? 0)) / 1000;
    if (idx === fileIdx && a.src && a.readyState >= 1 && urls && Date.now() < urls.expires) {
      a.currentTime = localS;
      if (play) void a.play().catch(onPlayRejected);
    } else {
      await loadFile(idx, localS, play);
    }
  } else if (hls || (a.src && a.readyState >= 1 && urls && Date.now() < urls.expires)) {
    a.currentTime = target / 1000;
    if (play) void a.play().catch(onPlayRejected);
  } else {
    await loadHls(target / 1000, play);
  }
  if (opts.send !== false) {
    // "On seek": debounce so dragging the slider sends one write.
    clearTimeout(seekTimer);
    seekTimer = setTimeout(() => void sendProgress(), 800);
  }
}

export function skip(deltaS: number): void {
  void seekTo(player.positionMs + deltaS * 1000);
}

export function nextChapter(): void {
  const book = player.book;
  const i = chapterIndexAt(book, player.positionMs);
  const next = book?.chapters[i + 1];
  if (next) void seekTo(next.start_ms);
}

/** Back to the start of this chapter, or the previous one within 3 s. */
export function previousChapter(): void {
  const book = player.book;
  const i = chapterIndexAt(book, player.positionMs);
  const ch = book?.chapters[i];
  if (!ch) return void seekTo(0);
  const prev = book.chapters[i - 1];
  void seekTo(player.positionMs - ch.start_ms < 3000 && prev ? prev.start_ms : ch.start_ms);
}

export function setSpeed(speed: number): void {
  player.speed = Math.min(3.5, Math.max(0.5, speed));
  if (audio) {
    audio.playbackRate = player.speed;
    audio.defaultPlaybackRate = player.speed;
  }
  prefs.speed = player.speed;
  mediaSessionPosition();
}

export async function switchProfile(profile: Profile): Promise<void> {
  if (!player.book || profile === player.profile) return;
  const at = player.positionMs;
  const resume = wantPlay;
  audio?.pause();
  hls?.destroy();
  hls = null;
  player.profile = profile;
  urls = null;
  if (audio) {
    audio.removeAttribute("src");
    audio.load();
  }
  await seekTo(at, { play: resume, send: false });
}

// ── sleep timer ──

export function setSleep(choice: number | "chapter" | null): void {
  if (audio) audio.volume = 1;
  if (choice === null) player.sleep = null;
  else if (choice === "chapter") {
    const ch = currentChapter();
    player.sleep = { kind: "chapter", endMs: ch?.end_ms ?? durationMs() };
  } else player.sleep = { kind: "time", endsAt: Date.now() + choice * 60_000 };
}

function checkSleep(): void {
  const s = player.sleep;
  const a = audio;
  if (!s || !a) return;
  if (s.kind === "chapter") {
    if (player.positionMs >= s.endMs - 300) {
      pause();
      player.sleep = null;
    }
    return;
  }
  const left = s.endsAt - Date.now();
  if (left <= 0) {
    pause();
    a.volume = 1;
    player.sleep = null;
  } else if (left < FADE_MS) a.volume = Math.max(0.05, left / FADE_MS);
}

// ── progress sync (brief §6.8) ──

function syncedAt(bookId: string): string | undefined {
  return readLocal<Record<string, string>>(SYNCED_KEY, {})[bookId];
}

function markSynced(bookId: string): void {
  const all = readLocal<Record<string, string>>(SYNCED_KEY, {});
  all[bookId] = new Date().toISOString();
  // Keep the map small.
  const keys = Object.keys(all);
  if (keys.length > 200) for (const k of keys.slice(0, keys.length - 200)) delete all[k];
  writeLocal(SYNCED_KEY, all);
}

export async function sendProgress(
  opts: { finished?: boolean; force?: boolean; resolution?: string; positionMs?: number } = {},
): Promise<void> {
  const book = player.book;
  if (!book) return;
  // While the user is deciding a conflict, don't keep creating new ones.
  if (player.conflict && !opts.resolution) return;
  const pos = Math.round(opts.positionMs ?? player.positionMs);
  if (!opts.force && !opts.resolution && opts.finished === undefined && Math.abs(pos - lastSentMs) < 1000) return;
  lastSentMs = pos;
  const res = await putProgress(api, book.id, {
    position_ms: pos,
    device_id: deviceId(),
    client_ts: new Date().toISOString(),
    speed: player.speed,
    finished: opts.finished,
    last_synced_at: syncedAt(book.id),
    resolution: opts.resolution,
  });
  if (res.kind === "ok") {
    markSynced(book.id);
    if (player.book?.id === book.id) player.book.progress = res.result.progress;
  } else if (res.kind === "conflict") {
    player.conflict = res.conflict;
  }
}

/** The user's answer to "Jump to 4:12:30 from <device>?" — final and logged. */
export async function resolveConflict(jump: boolean): Promise<void> {
  const c = player.conflict;
  if (!c) return;
  const pos = jump ? c.server.position_ms : player.positionMs;
  if (jump) await seekTo(pos, { send: false });
  await sendProgress({ resolution: c.conflict_id, positionMs: pos, force: true });
  player.conflict = null;
}

/** Another device moved on while we're paused: follow it quietly. */
export async function followRemoteProgress(): Promise<void> {
  const book = player.book;
  if (!book || player.playing || player.conflict) return;
  const { data } = await api.GET("/me/progress/{bookId}", { params: { path: { bookId: book.id } } });
  if (!data || data.device_id === deviceId() || player.book?.id !== book.id) return;
  if (Math.abs(data.position_ms - player.positionMs) < 2000) return;
  book.progress = data;
  player.speed = data.speed;
  await seekTo(data.position_ms, { send: false, play: false });
  lastSentMs = data.position_ms;
}

/** Last-chance write when the tab goes away (keepalive survives unload). */
function beacon(): void {
  const book = player.book;
  if (!book || player.conflict) return;
  const body = JSON.stringify({
    position_ms: Math.round(player.positionMs),
    device_id: deviceId(),
    client_ts: new Date().toISOString(),
    speed: player.speed,
    last_synced_at: syncedAt(book.id),
  });
  void fetch(`/api/v1/me/progress/${encodeURIComponent(book.id)}`, {
    method: "PUT",
    body,
    keepalive: true,
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": readCookie("sapb_csrf") ?? "" },
  }).catch(() => undefined);
}

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") void sendProgress({ force: true });
});
window.addEventListener("pagehide", beacon);

// ── Media Session (OS media keys, lock screen) ──

function mediaSessionMetadata(): void {
  const book = player.book;
  if (!book || !("mediaSession" in navigator)) return;
  const cover = book.cover_url;
  navigator.mediaSession.metadata = new MediaMetadata({
    title: book.title,
    artist: book.authors.map((a) => a.name).join(", "),
    album: book.series?.name ?? "",
    artwork: !book.has_cover ? [] : [256, 512, 1024].map((s) => ({ src: `${cover}?size=${s}`, sizes: `${s}x${s}`, type: "image/jpeg" })),
  });
  const ms = navigator.mediaSession;
  const set = (action: MediaSessionAction, fn: MediaSessionActionHandler) => {
    try {
      ms.setActionHandler(action, fn);
    } catch {
      /* action not supported by this browser */
    }
  };
  set("play", play);
  set("pause", pause);
  set("stop", pause);
  set("seekbackward", (d) => skip(-(d.seekOffset ?? prefs.skipBackS)));
  set("seekforward", (d) => skip(d.seekOffset ?? prefs.skipForwardS));
  set("seekto", (d) => {
    if (d.seekTime !== undefined) void seekTo(d.seekTime * 1000);
  });
  set("previoustrack", previousChapter);
  set("nexttrack", nextChapter);
}

function mediaSessionState(): void {
  if ("mediaSession" in navigator) navigator.mediaSession.playbackState = player.playing ? "playing" : "paused";
}

function mediaSessionPosition(): void {
  const d = durationMs() / 1000;
  if (!("mediaSession" in navigator) || !d || !navigator.mediaSession.setPositionState) return;
  try {
    navigator.mediaSession.setPositionState({
      duration: d,
      playbackRate: player.speed,
      position: Math.min(d, player.positionMs / 1000),
    });
  } catch {
    /* invalid state during loading */
  }
}

/** "4:12:30" for the conflict prompt and labels. */
export function positionLabel(ms: number): string {
  return clock(ms);
}
