// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Typed API client. Types come from schema.gen.ts, which is generated from
// server/openapi.yaml (`npm run gen:api`). Do not hand-edit the generated file.

import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema.gen";

export type Schemas = components["schemas"];
export type Book = Schemas["BookDetail"];
export type BookSummary = Schemas["BookSummary"];
export type Progress = Schemas["Progress"];
export type ProgressConflict = Schemas["ProgressConflict"];
export type ApiError = Schemas["Error"];

export interface ClientOptions {
  /** Server origin, e.g. "https://books.example.com". Empty = same origin (web player). */
  baseUrl?: string;
  /** Returns the current CSRF token for cookie-authenticated, state-changing requests. */
  csrfToken?: () => string | undefined;
  /** Returns a bearer access token (desktop/other non-cookie clients). */
  accessToken?: () => string | undefined;
}

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export function createApiClient(opts: ClientOptions = {}) {
  const client = createClient<paths>({
    baseUrl: `${opts.baseUrl ?? ""}/api/v1`,
    credentials: "include",
  });

  const auth: Middleware = {
    onRequest({ request }) {
      const token = opts.accessToken?.();
      if (token) request.headers.set("Authorization", `Bearer ${token}`);
      const csrf = opts.csrfToken?.();
      if (csrf && UNSAFE_METHODS.has(request.method)) request.headers.set("X-CSRF-Token", csrf);
      return request;
    },
  };
  client.use(auth);
  return client;
}

export type ApiClient = ReturnType<typeof createApiClient>;

/** Example of a typed call (also keeps the compile check honest). */
export async function putProgress(
  api: ApiClient,
  bookId: string,
  body: Schemas["ProgressUpdate"],
): Promise<
  | { kind: "ok"; result: Schemas["ProgressWriteResult"] }
  | { kind: "conflict"; conflict: ProgressConflict }
  | { kind: "error"; error: ApiError | undefined; status: number }
> {
  const { data, error, response } = await api.PUT("/me/progress/{bookId}", {
    params: { path: { bookId } },
    body,
  });
  if (data) return { kind: "ok", result: data };
  if (response.status === 409) return { kind: "conflict", conflict: error as ProgressConflict };
  return { kind: "error", error: error as ApiError | undefined, status: response.status };
}
