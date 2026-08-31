// Behaviour of the client-side sync job settlement registry (D3): watchers
// settle by job_id, replay is idempotent per job_id, the boot-change clear
// resolves pending watchers null, and re-attach prefers a live job.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const syncJobsRead = vi.hoisted(() => vi.fn());

vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  syncJobs: syncJobsRead,
}));

import {
  attachSyncJob,
  clearSyncCorrelation,
  syncDoneFromEvent,
  watchSyncJob,
  _resetSyncJobsForTest,
} from "./sync-jobs.js";
import type { Job, SyncDoneEvent } from "./wire/types.gen.js";

function doneEvent(jobId: number, over: Partial<SyncDoneEvent> = {}): SyncDoneEvent {
  return {
    job_id: jobId,
    file_ref: {
      media_type: "movie",
      media_id: "tmdb-1",
      language: "en",
      variant: "standard",
      source: "external",
    },
    offset_ms: 250,
    confidence: 0.9,
    method: "audio",
    applied: true,
    dry_run: true,
    ...over,
  };
}

function job(jobId: number, state: Job["state"], over: Partial<Job> = {}): Job {
  return {
    job_id: jobId,
    activity_id: `act-${String(jobId)}`,
    file_ref: {
      media_type: "movie",
      media_id: "tmdb-1",
      language: "en",
      variant: "standard",
      source: "external",
      ordinal: 0,
    },
    state,
    accepted_at: new Date(2026, 0, 1, 0, 0, jobId).toISOString(),
    ...over,
  };
}

beforeEach(() => {
  _resetSyncJobsForTest();
  syncJobsRead.mockReset();
});

afterEach(() => {
  _resetSyncJobsForTest();
});

describe("watchSyncJob + syncDoneFromEvent", () => {
  it("settles the watcher for the event's job_id", () => {
    const seen: (SyncDoneEvent | null)[] = [];
    watchSyncJob(7, (ev) => seen.push(ev));
    syncDoneFromEvent(doneEvent(7));
    expect(seen).toHaveLength(1);
    expect(seen[0]?.job_id).toBe(7);
  });

  it("a late watcher of an already-settled job answers immediately", () => {
    syncDoneFromEvent(doneEvent(7));
    const seen: (SyncDoneEvent | null)[] = [];
    watchSyncJob(7, (ev) => seen.push(ev));
    expect(seen).toHaveLength(1);
  });

  it("job A's replayed sync:done settles only A — never job B on the SAME file", () => {
    // A completed; B is a NEW job for the same file (a later dispatch).
    // Correlation is job_id, so A's replay cannot touch B's watcher.
    const seenA: (SyncDoneEvent | null)[] = [];
    const seenB: (SyncDoneEvent | null)[] = [];
    watchSyncJob(7, (ev) => seenA.push(ev));
    syncDoneFromEvent(doneEvent(7));
    watchSyncJob(8, (ev) => seenB.push(ev));

    syncDoneFromEvent(doneEvent(7)); // the replay

    expect(seenA).toHaveLength(1); // idempotent per job_id: no second settle
    expect(seenB).toHaveLength(0); // B untouched by A's replay
    syncDoneFromEvent(doneEvent(8));
    expect(seenB).toHaveLength(1);
  });

  it("unwatch stops the callback", () => {
    const seen: (SyncDoneEvent | null)[] = [];
    const unwatch = watchSyncJob(7, (ev) => seen.push(ev));
    unwatch();
    syncDoneFromEvent(doneEvent(7));
    expect(seen).toHaveLength(0);
  });
});

describe("clearSyncCorrelation (boot change)", () => {
  it("resolves pending watchers null and resets the settled namespace", () => {
    const seen: (SyncDoneEvent | null)[] = [];
    watchSyncJob(7, (ev) => seen.push(ev));
    syncDoneFromEvent(doneEvent(3)); // an unrelated settled job

    clearSyncCorrelation();

    expect(seen).toEqual([null]);
    // The namespace reset: the SAME numeric job_id from the new boot is a
    // first delivery, not a replayed duplicate.
    const reborn: (SyncDoneEvent | null)[] = [];
    watchSyncJob(3, (ev) => reborn.push(ev));
    expect(reborn).toHaveLength(0); // not answered from the stale settled map
    syncDoneFromEvent(doneEvent(3));
    expect(reborn).toHaveLength(1);
  });
});

describe("attachSyncJob (the reload re-attach)", () => {
  const ref = {
    media_type: "movie" as const,
    media_id: "tmdb-1",
    language: "en",
    variant: "standard",
    source: "external",
    ordinal: 0,
  };

  it("prefers the queued/running job for the file", async () => {
    syncJobsRead.mockResolvedValue([job(9, "done"), job(10, "queued")]);
    const state = await attachSyncJob(ref);
    expect(state).toEqual({ kind: "live", job: expect.objectContaining({ job_id: 10 }) });
  });

  it("falls back to the newest terminal by the registry's total order", async () => {
    // The read is served already sorted (accepted_at DESC, job_id DESC).
    syncJobsRead.mockResolvedValue([job(12, "done"), job(9, "done")]);
    const state = await attachSyncJob(ref);
    expect(state).toEqual({ kind: "done", job: expect.objectContaining({ job_id: 12 }) });
  });

  it("ignores jobs for OTHER files and answers none when nothing matches", async () => {
    const other = job(4, "running");
    other.file_ref = { ...other.file_ref, media_id: "tmdb-2" };
    syncJobsRead.mockResolvedValue([other]);
    expect(await attachSyncJob(ref)).toEqual({ kind: "none" });
  });

  it("answers none when the read fails", async () => {
    syncJobsRead.mockResolvedValue(null);
    expect(await attachSyncJob(ref)).toEqual({ kind: "none" });
  });
});
