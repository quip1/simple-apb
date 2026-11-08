// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Generates docs/API.md from server/openapi.yaml.
// Usage: node scripts/gen-api-docs.mjs <openapi.yaml> <out.md>
import fs from "node:fs";
import { parse } from "yaml";

const [specPath, outPath] = process.argv.slice(2);
const spec = parse(fs.readFileSync(specPath, "utf8"));
const METHODS = ["get", "post", "put", "patch", "delete"];
const ref = (r) => r?.$ref?.split("/").pop();

const lines = [];
lines.push("# Simple APB API");
lines.push("");
lines.push("> **Generated** from `server/openapi.yaml` by `web/scripts/gen-api-docs.mjs`. Do not edit by hand.");
lines.push("");
lines.push(`Version **${spec.info.version}** · base path \`${spec.servers?.[0]?.url ?? "/"}\``);
lines.push("");
lines.push(spec.info.description?.trim() ?? "");
lines.push("");

const byTag = new Map();
for (const [path, item] of Object.entries(spec.paths)) {
  for (const m of METHODS) {
    const op = item[m];
    if (!op) continue;
    const tag = op.tags?.[0] ?? "other";
    if (!byTag.has(tag)) byTag.set(tag, []);
    byTag.get(tag).push({ path, method: m.toUpperCase(), op });
  }
}

const tagOrder = (spec.tags ?? []).map((t) => t.name);
const tags = [...byTag.keys()].sort((a, b) => tagOrder.indexOf(a) - tagOrder.indexOf(b));

lines.push("## Endpoints");
lines.push("");
for (const tag of tags) {
  lines.push(`### ${tag}`);
  lines.push("");
  lines.push("| Method | Path | Summary | Auth | Responses |");
  lines.push("|---|---|---|---|---|");
  for (const { path, method, op } of byTag.get(tag)) {
    const secured = op.security ? op.security.length > 0 : (spec.security ?? []).length > 0;
    const responses = Object.keys(op.responses ?? {}).join(", ");
    lines.push(`| ${method} | \`${path}\` | ${op.summary ?? ""} | ${secured ? "yes" : "public"} | ${responses} |`);
  }
  lines.push("");
}

lines.push("## Schemas");
lines.push("");
for (const [name, schema] of Object.entries(spec.components?.schemas ?? {})) {
  lines.push(`### ${name}`);
  lines.push("");
  if (schema.description) lines.push(schema.description.trim(), "");
  if (schema.enum) {
    lines.push(`Enum: ${schema.enum.map((e) => `\`${e}\``).join(", ")}`, "");
    continue;
  }
  const parts = schema.allOf ?? [schema];
  const rows = [];
  for (const part of parts) {
    if (part.$ref) {
      rows.push(`| _all fields of_ \`${ref(part)}\` | | | |`);
      continue;
    }
    const required = new Set(part.required ?? []);
    for (const [prop, p] of Object.entries(part.properties ?? {})) {
      let type = p.$ref ? `\`${ref(p)}\`` : p.type ?? "";
      if (p.type === "array") type = `array of ${p.items?.$ref ? `\`${ref(p.items)}\`` : p.items?.type}`;
      if (p.format) type += ` (${p.format})`;
      if (p.enum) type += `: ${p.enum.join(" \\| ")}`;
      const desc = (p.description ?? "").replace(/\n/g, " ").replace(/\|/g, "\\|").trim();
      rows.push(`| \`${prop}\` | ${type} | ${required.has(prop) ? "yes" : ""} | ${desc} |`);
    }
  }
  if (rows.length) {
    lines.push("| Field | Type | Required | Notes |", "|---|---|---|---|", ...rows, "");
  }
}

fs.writeFileSync(outPath, lines.join("\n").replace(/\n{3,}/g, "\n\n") + "\n");
console.log(`wrote ${outPath}`);
