// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A small resumable upload client for the server's tus endpoint (brief §6.11):
// create, then send 8 MB chunks; after a network error it asks the server how
// much arrived (HEAD) and continues from there.

import { api, readCookie } from "../api/client";

const BASE = "/api/v1/uploads/";
const CHUNK = 8 << 20;
const RETRIES = 6;

export type UploadErrorKind = "forbidden" | "unsupported" | "quota" | "failed" | "cancelled";

export class UploadError extends Error {
  constructor(public kind: UploadErrorKind) {
    super(kind);
  }
}

function b64(s: string): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(s)));
}

function headers(extra: Record<string, string>): Headers {
  const h = new Headers({ "Tus-Resumable": "1.0.0", ...extra });
  const csrf = readCookie("sapb_csrf");
  if (csrf) h.set("X-CSRF-Token", csrf);
  return h;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** fetch with one session refresh on 401 (long uploads outlive the access token). */
async function send(url: string, init: () => RequestInit): Promise<Response> {
  let res = await fetch(url, { credentials: "include", ...init() });
  if (res.status === 401 && (await api.refresh()) === "ok") res = await fetch(url, { credentials: "include", ...init() });
  return res;
}

function failure(status: number): UploadError {
  if (status === 403) return new UploadError("forbidden");
  if (status === 415) return new UploadError("unsupported");
  if (status === 413) return new UploadError("quota");
  return new UploadError("failed");
}

/** Uploads a file; resolves with the upload id once every byte has arrived. */
export async function tusUpload(file: File, onProgress: (sent: number) => void, signal?: AbortSignal): Promise<string> {
  const created = await send(BASE, () => ({
    method: "POST",
    signal,
    headers: headers({ "Upload-Length": String(file.size), "Upload-Metadata": `filename ${b64(file.name)}` }),
  }));
  if (created.status !== 201) throw failure(created.status);
  const location = created.headers.get("Location") ?? "";
  // Only the path: the server may name its own host, which can differ behind a proxy.
  const url = location.startsWith("http") ? new URL(location).pathname : location;
  const id = url.split("/").filter(Boolean).pop() ?? "";

  let offset = 0;
  let failures = 0;
  while (offset < file.size) {
    if (signal?.aborted) throw new UploadError("cancelled");
    const chunk = file.slice(offset, offset + CHUNK);
    try {
      const res = await send(url, () => ({
        method: "PATCH",
        signal,
        headers: headers({ "Upload-Offset": String(offset), "Content-Type": "application/offset+octet-stream" }),
        body: chunk,
      }));
      if (res.status === 204) {
        offset = Number(res.headers.get("Upload-Offset") ?? offset + chunk.size);
        failures = 0;
        onProgress(offset);
        continue;
      }
      if (res.status !== 409 && res.status < 500) throw failure(res.status);
    } catch (e) {
      if (e instanceof UploadError) throw e;
      if (signal?.aborted) throw new UploadError("cancelled");
    }
    // Network trouble or an offset mismatch: ask where we are, then go on.
    if (++failures > RETRIES) throw new UploadError("failed");
    await sleep(Math.min(1000 * 2 ** failures, 15000));
    try {
      const head = await send(url, () => ({ method: "HEAD", signal, headers: headers({}) }));
      if (head.ok) offset = Number(head.headers.get("Upload-Offset") ?? offset);
    } catch {
      /* still offline; the loop tries again */
    }
  }
  return id;
}
