// SPDX-License-Identifier: AGPL-3.0-or-later
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";
import pkg from "./package.json" with { type: "json" };

// In development, run the Go server on :8080 and `npm run dev` here: API and
// WebSocket calls are proxied so cookies stay same-origin.
const server = process.env.SIMPLEAPB_DEV_SERVER ?? "http://127.0.0.1:8080";

// `--mode desktop` builds the copy the Windows app embeds (desktop/, ADR-0047):
// the same app, with the desktop features switched on.
export default defineConfig(({ mode }) => ({
  plugins: [svelte()],
  define: { __APP_VERSION__: JSON.stringify(pkg.version), __DESKTOP__: JSON.stringify(mode === "desktop") },
  server: {
    port: 5173,
    // Shared files outside web/: /i18n strings and the privacy template.
    fs: { allow: [".."] },
    proxy: {
      "/api": { target: server, changeOrigin: false },
      "/ws": { target: server.replace(/^http/, "ws"), ws: true },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
    sourcemap: false,
    // The SPA is embedded in the Go binary; keep chunks few and cacheable.
    assetsDir: "assets",
  },
}));
