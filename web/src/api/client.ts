// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Typed API client. Types come from schema.gen.ts, which is generated from
// server/openapi.yaml (`npm run gen:api`). Do not hand-edit the generated file.
//
// The web app authenticates with cookies: the server sets an HttpOnly access
// cookie, an HttpOnly refresh cookie scoped to /api/v1/auth, and a readable
// CSRF cookie that must be echoed in X-CSRF-Token on writes. When the access
// cookie expires (401 invalid_token) the client refreshes once and retries.

import createClient from "openapi-fetch";
import type { components, paths } from "./schema.gen";

export type Schemas = components["schemas"];
export type Book = Schemas["BookDetail"];
export type BookSummary = Schemas["BookSummary"];
export type Progress = Schemas["Progress"];
export type ProgressConflict = Schemas["ProgressConflict"];
export type ApiError = Schemas["Error"];
export type Me = Schemas["Me"];
export type Instance = Schemas["Instance"];

export interface ClientOptions {
  /** Server origin. Empty = same origin (the embedded web app). */
  baseUrl?: string;
  /** Returns a bearer token for non-cookie clients (desktop). */
  accessToken?: () => string | undefined;
  /** Called when the session can't be refreshed (show the login page). */
  onSignedOut?: () => void;
}

const UNSAFE = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export function readCookie(name: string): string | undefined {
  for (const part of document.cookie.split(";")) {
    const [k, ...v] = part.trim().split("=");
    if (k === name) return decodeURIComponent(v.join("="));
  }
  return undefined;
}

export function createApiClient(opts: ClientOptions = {}) {
  const base = `${opts.baseUrl ?? ""}/api/v1`;
  let refreshing: Promise<boolean> | null = null;

  const refresh = (): Promise<boolean> => {
    refreshing ??= fetch(`${base}/auth/refresh`, {
      method: "POST",
      credentials: "include",
      headers: { "X-CSRF-Token": readCookie("sapb_csrf") ?? "" },
    })
      .then((r) => r.ok)
      .catch(() => false)
      .finally(() => {
        refreshing = null;
      });
    return refreshing;
  };

  const authedFetch = async (input: Request): Promise<Response> => {
    const send = (req: Request) => {
      const token = opts.accessToken?.();
      if (token) req.headers.set("Authorization", `Bearer ${token}`);
      const csrf = readCookie("sapb_csrf");
      if (csrf && UNSAFE.has(req.method)) req.headers.set("X-CSRF-Token", csrf);
      return fetch(req);
    };
    const retry = input.clone();
    const res = await send(input);
    if (res.status !== 401 || input.url.includes("/auth/")) return res;
    const body = (await res
      .clone()
      .json()
      .catch(() => ({}))) as Partial<ApiError>;
    if (body.code !== "invalid_token") return res;
    if (await refresh()) return send(retry);
    opts.onSignedOut?.();
    return res;
  };

  return createClient<paths>({ baseUrl: base, credentials: "include", fetch: authedFetch });
}

export type ApiClient = ReturnType<typeof createApiClient>;

/** Shared client for the web app. */
export const api = createApiClient({
  onSignedOut: () => window.dispatchEvent(new CustomEvent("sapb:signed-out")),
});

/** A stable per-browser device id (sessions and progress are per device). */
export function deviceId(): string {
  const key = "sapb.device";
  try {
    let id = localStorage.getItem(key);
    if (!id) {
      id = crypto.randomUUID();
      localStorage.setItem(key, id);
    }
    return id;
  } catch {
    return crypto.randomUUID();
  }
}

export function deviceInfo(): Schemas["DeviceInfo"] {
  const ua = navigator.userAgent;
  const browser = /Firefox\//.test(ua)
    ? "Firefox"
    : /Edg\//.test(ua)
      ? "Edge"
      : /Chrome\//.test(ua)
        ? "Chrome"
        : /Safari\//.test(ua)
          ? "Safari"
          : "Browser";
  return { device_id: deviceId(), device_name: `${browser} (web)`, platform: "web", app_version: __APP_VERSION__ };
}

/** Example of a typed call (also keeps the compile check honest). */
export async function putProgress(
  client: ApiClient,
  bookId: string,
  body: Schemas["ProgressUpdate"],
): Promise<
  | { kind: "ok"; result: Schemas["ProgressWriteResult"] }
  | { kind: "conflict"; conflict: ProgressConflict }
  | { kind: "error"; error: ApiError | undefined; status: number }
> {
  const { data, error, response } = await client.PUT("/me/progress/{bookId}", {
    params: { path: { bookId } },
    body,
  });
  if (data) return { kind: "ok", result: data };
  if (response.status === 409) return { kind: "conflict", conflict: error as ProgressConflict };
  return { kind: "error", error: error as ApiError | undefined, status: response.status };
}
