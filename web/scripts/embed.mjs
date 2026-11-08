// SPDX-License-Identifier: AGPL-3.0-or-later
// Copies the built SPA into the Go server so `go build` embeds it.
import fs from "node:fs";
import path from "node:path";
const src = path.resolve("dist");
const dst = path.resolve("../server/internal/web/dist");
fs.rmSync(dst, { recursive: true, force: true });
fs.cpSync(src, dst, { recursive: true });
fs.writeFileSync(path.join(dst, ".gitkeep"), "");
console.log("embedded web build into", dst);
