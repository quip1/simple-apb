// SPDX-License-Identifier: AGPL-3.0-or-later
//
// End-to-end tests (brief §15): a real server binary with the embedded web app,
// started fresh by e2e/global-setup.ts. Build first:
//   npm run build:embed && (cd ../server && go build -o bin/simpleapb ./cmd/simpleapb)
//   npm run e2e
import { defineConfig, devices } from "@playwright/test";

export const PORT = Number(process.env.E2E_PORT ?? 18190);

export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: 0, // one serial journey: a retry would meet the accounts the first try made
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: { args: ["--autoplay-policy=no-user-gesture-required"] },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
