// SPDX-License-Identifier: AGPL-3.0-or-later
import fs from "node:fs";
import { readState, STATE_FILE } from "./helpers";

export default async function globalTeardown(): Promise<void> {
  if (!fs.existsSync(STATE_FILE)) return;
  const s = readState();
  try {
    process.kill(s.pid);
  } catch {
    /* already gone */
  }
  await new Promise((r) => setTimeout(r, 500));
  if (process.env.E2E_KEEP) {
    console.log(`kept ${s.dir}`);
    return;
  }
  fs.rmSync(s.dir, { recursive: true, force: true });
  fs.rmSync(STATE_FILE, { force: true });
}
