#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// One version for every part of Simple APB (docs/UPDATING.md, ADR-0049).
//
//   node scripts/release.mjs 0.5.0           sets it everywhere and dates the changelog
//   node scripts/release.mjs 0.5.0 --check   fails unless every part already says 0.5.0
//
// The server takes its version from the release tag at build time (-ldflags).

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const [version, flag] = process.argv.slice(2);
const check = flag === "--check";

const SEMVER = /^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/;
const m = SEMVER.exec(version ?? "");
if (!m) {
  console.error("usage: node scripts/release.mjs X.Y.Z[-pre] [--check]");
  process.exit(2);
}
const [major, minor, patch] = [Number(m[1]), Number(m[2]), Number(m[3])];
if (minor > 99 || patch > 99) {
  console.error("minor and patch must be below 100 (they form the Android versionCode)");
  process.exit(2);
}
// Android needs a growing integer: 1.2.3 → 10203. A pre-release shares its release's code
// minus one, so 1.2.3-rc.1 (10202) installs below 1.2.3.
const versionCode = major * 10000 + minor * 100 + patch - (m[4] ? 1 : 0);

const problems = [];
function update(rel, pattern, replacement, what) {
  const path = join(root, rel);
  const before = readFileSync(path, "utf8");
  const match = pattern.exec(before);
  if (!match) {
    problems.push(`${rel}: ${what} not found`);
    return;
  }
  const after = before.replace(pattern, replacement);
  if (check) {
    if (after !== before) problems.push(`${rel}: ${what} is "${match[1]}", not ${version}`);
  } else if (after !== before) {
    writeFileSync(path, after);
    console.log(`${rel}: ${what} → ${version}`);
  }
}

// JSON "version" fields (the first one: the package itself).
for (const rel of ["web/package.json", "desktop/package.json", "desktop/src-tauri/tauri.conf.json"]) {
  update(rel, /"version": "([^"]+)"/, `"version": "${version}"`, "version");
}
// package-lock.json: the root package's version (top and in "packages" → "").
for (const rel of ["web/package-lock.json", "desktop/package-lock.json"]) {
  update(rel, /^(?:\{\s*"name": "[^"]+",\s*)"version": "([^"]+)"/m, (s) => s.replace(/"version": "[^"]+"/, `"version": "${version}"`), "version");
  update(rel, /"": \{\s*"name": "[^"]+",\s*"version": "([^"]+)"/, (s) => s.replace(/"version": "[^"]+"/, `"version": "${version}"`), "root package version");
}
update("desktop/src-tauri/Cargo.toml", /^version = "([^"]+)"/m, `version = "${version}"`, "version");
update(
  "desktop/src-tauri/Cargo.lock",
  /name = "simple-apb-desktop"\nversion = "([^"]+)"/,
  `name = "simple-apb-desktop"\nversion = "${version}"`,
  "simple-apb-desktop version",
);
update("android/app/build.gradle.kts", /versionName = "([^"]+)"/, `versionName = "${version}"`, "versionName");
update("android/app/build.gradle.kts", /versionCode = (\d+)/, `versionCode = ${versionCode}`, "versionCode");

// CHANGELOG: the release gets its own dated section under a fresh [Unreleased].
const changelog = join(root, "CHANGELOG.md");
const text = readFileSync(changelog, "utf8");
const heading = `## [${version}]`;
if (check) {
  if (!text.includes(heading)) problems.push(`CHANGELOG.md: no "${heading}" section`);
} else if (!text.includes(heading)) {
  const date = new Date().toISOString().slice(0, 10);
  writeFileSync(changelog, text.replace("## [Unreleased]\n", `## [Unreleased]\n\n${heading} - ${date}\n`));
  console.log(`CHANGELOG.md: ${heading} - ${date}`);
}

if (problems.length) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log(check ? `Everything says ${version}.` : `Now commit, then tag v${version} and push the tag.`);
