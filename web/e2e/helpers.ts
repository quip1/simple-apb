// SPDX-License-Identifier: AGPL-3.0-or-later
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { expect, type Page } from "@playwright/test";

export const STATE_FILE = path.join(os.tmpdir(), "simpleapb-e2e", "state.json");

export interface E2EState {
  dir: string;
  pid: number;
  base: string;
  admin: { username: string; password: string; totpSecret: string };
  password: string; // every test user's password
  fixtureMp3: string;
}

export function readState(): E2EState {
  return JSON.parse(fs.readFileSync(STATE_FILE, "utf8")) as E2EState;
}

/** RFC 6238 TOTP (SHA-1, 6 digits, 30 s) for a base32 secret. */
export function totp(secret: string, stepOffset = 0): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of secret.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
  const key = Buffer.from((bits.match(/.{8}/g) ?? []).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000) + stepOffset));
  const h = crypto.createHmac("sha1", key).update(counter).digest();
  const o = (h[19] ?? 0) & 15;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1e6).padStart(6, "0");
}

/** Sign in through the sign-in page (with a TOTP code when asked). */
export async function signIn(page: Page, username: string, password: string, totpSecret?: string): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel(/^Password/).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  if (totpSecret) {
    // Each code works once (replay protection), so use the next step's code.
    await page.getByLabel("Authenticator code").fill(totp(totpSecret, 1));
    await page.getByRole("button", { name: "Verify" }).click();
  }
  await expect(page.getByRole("navigation", { name: "Main" })).toBeVisible();
}
