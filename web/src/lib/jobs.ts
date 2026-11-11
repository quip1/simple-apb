// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Background jobs (book tools, scans): wait for one to finish.

import { api, type Schemas } from "../api/client";

export type Job = Schemas["JobStatus"];

/** Polls a job until it succeeds or fails (or the signal aborts). */
export async function waitForJob(id: string, signal?: AbortSignal, everyMs = 1000): Promise<Job | null> {
  for (;;) {
    if (signal?.aborted) return null;
    const { data } = await api.GET("/jobs/{jobId}", { params: { path: { jobId: id } } });
    if (data && (data.status === "succeeded" || data.status === "failed" || data.status === "cancelled")) return data;
    await new Promise((r) => setTimeout(r, everyMs));
  }
}
