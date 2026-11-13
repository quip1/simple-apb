// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The brief's end-to-end journey (§15): sign-up → approval → upload → share →
// play → progress synced to a second browser profile (another device).
import { expect, test, type Browser, type Page } from "@playwright/test";
import { readState, signIn } from "./helpers";

const TITLE = "Lighthouse Diaries";

test.describe.configure({ mode: "serial" });

let admin: Page;
const state = readState();

let profiles = 0;

/** Seconds into the (single-chapter) book, from the player's position slider. */
async function position(page: Page): Promise<number> {
  return Number(await page.locator("#scrub").inputValue()) / 1000;
}

async function newPage(browser: Browser): Promise<Page> {
  // A new context is a separate browser profile: its own cookies and device id,
  // and its own client address (documentation range, RFC 5737) so each one
  // gets the real per-IP sign-in limits.
  profiles++;
  const ctx = await browser.newContext({ extraHTTPHeaders: { "X-Forwarded-For": `192.0.2.${profiles}` } });
  return ctx.newPage();
}

test("someone asks for an account", async ({ browser }) => {
  const page = await newPage(browser);
  await page.goto("/signup");
  await page.getByLabel("Username").fill("riley");
  await page.getByLabel(/^Password/).fill(state.password);
  await page.getByLabel("Repeat password").fill(state.password);
  await page.getByLabel("I confirm I am old enough").check();
  await page.getByLabel("I accept the terms").check();
  await page.getByRole("button", { name: "Send request" }).click();
  await expect(page.getByText("Request sent")).toBeVisible();

  // Not approved yet: signing in is refused.
  await page.goto("/login");
  await page.getByLabel("Username").fill("riley");
  await page.getByLabel(/^Password/).fill(state.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
});

test("an admin approves it from the inbox", async ({ browser }) => {
  admin = await newPage(browser);
  await signIn(admin, state.admin.username, state.admin.password, state.admin.totpSecret);
  await admin.goto("/admin/inbox");
  const item = admin.getByRole("listitem").filter({ hasText: "riley" });
  const approved = admin.waitForResponse((r) => r.url().endsWith("/approve"));
  await item.getByRole("button", { name: "Approve" }).click();
  expect((await approved).status()).toBe(204);
  await expect(admin.getByRole("listitem").filter({ hasText: "riley" })).toHaveCount(0);
});

test("the new member uploads a book", async ({ browser }) => {
  const page = await newPage(browser);
  await signIn(page, "riley", state.password);
  await page.goto("/library");
  await page.getByRole("link", { name: "Upload books" }).click();
  await page.getByLabel("Choose files").setInputFiles(state.fixtureMp3);
  const row = page.getByRole("listitem").filter({ hasText: `${TITLE}.mp3` });
  await expect(row.getByText("Added")).toBeVisible({ timeout: 30_000 });
  await row.getByRole("link").click();
  await expect(page.getByRole("heading", { name: TITLE, level: 1 })).toBeVisible();
});

test("the admin shares it with everyone", async () => {
  await admin.goto("/admin/library");
  await admin.getByLabel(`Select ${TITLE}`).check();
  await admin.getByRole("button", { name: "Share…" }).click();
  const dialog = admin.getByRole("dialog");
  await dialog.getByRole("combobox").selectOption({ label: "Public" });
  await dialog.getByRole("button", { name: "Share", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(admin.getByRole("row").filter({ hasText: TITLE })).toContainText("Public");
});

test("sam listens on one device and continues on another", async ({ browser }) => {
  const phone = await newPage(browser);
  await signIn(phone, "sam", state.password);
  await phone.goto("/library");
  await phone
    .getByRole("link", { name: new RegExp(TITLE) })
    .first()
    .click();
  await phone.getByRole("button", { name: "Play", exact: true }).click();
  await expect(phone).toHaveURL(/\/player$/);
  // Real playback: the position moves.
  await expect.poll(() => position(phone), { timeout: 20_000 }).toBeGreaterThan(4);
  await phone.getByRole("button", { name: "Pause" }).first().click(); // pausing saves progress
  const saved = await position(phone);

  const laptop = await newPage(browser);
  await signIn(laptop, "sam", state.password);
  // Home leads with the book you're on (ADR-0051).
  const current = laptop.getByRole("region", { name: new RegExp(TITLE) });
  await expect(current.getByText("Pick up where you left off")).toBeVisible();
  // The position came across: resuming starts where the phone stopped.
  await current.getByRole("button", { name: "Resume" }).click();
  await expect(laptop).toHaveURL(/\/player$/);
  await expect.poll(() => position(laptop), { timeout: 20_000 }).toBeGreaterThanOrEqual(saved - 1);
});
