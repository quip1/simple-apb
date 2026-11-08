// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Starts a fresh server (its own temporary config, library, cache and backups),
// finishes the setup wizard through the API, adds a second account and makes
// a small mp3 fixture. Everything goes away in global-teardown.ts.
import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { PORT } from "../playwright.config";
import { STATE_FILE, totp, type E2EState } from "./helpers";

const here = path.dirname(fileURLToPath(import.meta.url));
const ADMIN = { username: "admin", password: "e2e-tangerine-orbit-quietly" };
const PASSWORD = "e2e-marble-canyon-whistle";

async function waitFor(fn: () => Promise<boolean>, what: string, ms = 20_000): Promise<void> {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`timed out waiting for ${what}`);
}

export default async function globalSetup(): Promise<void> {
  const base = `http://127.0.0.1:${PORT}`;
  const api = `${base}/api/v1`;
  fs.mkdirSync(path.dirname(STATE_FILE), { recursive: true });
  const dir = fs.mkdtempSync(path.join(path.dirname(STATE_FILE), "run-"));
  for (const d of ["config", "library", "cache", "backups"]) fs.mkdirSync(path.join(dir, d), { recursive: true });

  const exe = process.platform === "win32" ? "simpleapb.exe" : "simpleapb";
  const bin = process.env.SIMPLEAPB_BIN ?? path.resolve(here, "..", "..", "server", "bin", exe);
  if (!fs.existsSync(bin)) throw new Error(`server binary not found at ${bin}; build it first (see playwright.config.ts)`);
  const log = path.join(dir, "server.log");
  const out = fs.openSync(log, "a");
  const child = spawn(bin, [], {
    env: {
      ...process.env,
      SIMPLEAPB_CONFIG_DIR: path.join(dir, "config"),
      SIMPLEAPB_LIBRARY_DIR: path.join(dir, "library"),
      SIMPLEAPB_CACHE_DIR: path.join(dir, "cache"),
      SIMPLEAPB_BACKUPS_DIR: path.join(dir, "backups"),
      SIMPLEAPB_LISTEN: `127.0.0.1:${PORT}`,
      // Each browser profile sends its own X-Forwarded-For (see journey.spec.ts),
      // so the real per-IP sign-in limits apply per profile, not to the whole run.
      SIMPLEAPB_TRUSTED_PROXIES: "127.0.0.1",
    },
    stdio: ["ignore", out, out],
    detached: process.platform !== "win32",
  });
  child.unref();
  await waitFor(async () => (await fetch(`${api}/health`)).ok, "the server");
  let token = "";
  await waitFor(async () => {
    token = /"setup_token":"([^"]+)"/.exec(fs.readFileSync(log, "utf8"))?.[1] ?? "";
    return token !== "";
  }, "the setup token");

  const post = async (url: string, body: unknown, headers: Record<string, string> = {}) => {
    const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(body) });
    const text = await r.text();
    if (!r.ok) throw new Error(`${url}: ${r.status} ${text}`);
    return text ? (JSON.parse(text) as Record<string, unknown>) : {};
  };
  const setup = { "X-Setup-Token": token };
  const { secret } = (await post(`${api}/setup/totp`, { username: ADMIN.username }, setup)) as { secret: string };
  await post(
    `${api}/setup/complete`,
    {
      admin: { ...ADMIN, totp_secret: secret, totp_code: totp(secret, -1) },
      database: { dialect: "sqlite" },
      library_path: path.join(dir, "library"),
      base_url: base,
      instance_name: "E2E shelf",
    },
    setup,
  );
  await waitFor(async () => (await (await fetch(`${api}/instance`)).json()).setup_required === false, "normal mode");

  // A second listener, "sam", approved through the API (the UI flow is tested with "riley").
  const device = { device_id: "6f1c3a52-0000-4000-8000-0000000000e2", device_name: "E2E setup", platform: "other", app_version: "0" };
  const login = await post(`${api}/auth/login`, { ...ADMIN, device });
  const verified = await post(`${api}/auth/totp/verify`, { totp_challenge: login.totp_challenge, code: totp(secret, 0) });
  const auth = { Authorization: `Bearer ${(verified.tokens as { access_token: string }).access_token}` };
  await post(`${api}/auth/signup`, { username: "sam", password: PASSWORD, age_confirmed: true, terms_accepted: true });
  const reqs = (await (await fetch(`${api}/admin/signup-requests`, { headers: auth })).json()) as { id: string; username: string }[];
  const sam = reqs.find((r) => r.username === "sam");
  if (!sam) throw new Error("sam's sign-up request is missing");
  await post(`${api}/admin/signup-requests/${sam.id}/approve`, {}, auth);

  // Keep the run hermetic: no online metadata lookups.
  const patch = await fetch(`${api}/admin/settings`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json", ...auth },
    body: JSON.stringify({ metadata: { open_library: false, google_books: false, audnexus: false } }),
  });
  if (!patch.ok) throw new Error(`metadata settings: ${patch.status}`);

  // A 30-second mp3 (Chromium plays mp3; Playwright's build has no AAC).
  const fixtureMp3 = path.join(dir, "Lighthouse Diaries.mp3");
  execFileSync("ffmpeg", [
    "-v",
    "error",
    "-y",
    "-f",
    "lavfi",
    "-i",
    "sine=frequency=440:duration=30:sample_rate=22050",
    "-metadata",
    "title=Lighthouse Diaries",
    "-metadata",
    "artist=Ines Calder",
    "-c:a",
    "libmp3lame",
    "-b:a",
    "48k",
    fixtureMp3,
  ]);

  const state: E2EState = { dir, pid: child.pid ?? 0, base, admin: { ...ADMIN, totpSecret: secret }, password: PASSWORD, fixtureMp3 };
  fs.writeFileSync(STATE_FILE, JSON.stringify(state));
}
