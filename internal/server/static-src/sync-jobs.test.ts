// Behaviour of the client-side sync job settlement registry (D3): watchers
// settle by job_id, replay is idempotent per job_id, the restart clear
// resolves pending watchers null, re-attach prefers a live job, the stream's
// jobs leg releases the watchers whose job is no longer live, and the `jobs`
// digest subject is held exactly while a watcher exists.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const syncJobsRead = vi.hoisted(() => vi.fn());

vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  syncJobs: syncJobsRead,
}));

import {
  attachSyncJob,
  clearSyncCorrelation,
  reattachSyncWatches,
  syncDoneFromEvent,
  watchSyncJob,
  _resetSyncJobsForTest,
} from "./sync-jobs.js";
import { SUBJECT_JOBS, _resetSubjectsForTest, versionMap } from "./subjects.js";
import type { Job, SyncDoneEvent } from "./wire/types.gen.js";

const EPOCH = "aaaaaaaaaaaaaaaa";

/** The jobs GET landed: the transport recorded its stamp. */
function holdJobs(): void {
  versionMap().observe(SUBJECT_JOBS, "1", EPOCH);
}

function doneEvent(jobId: number, over: Partial<SyncDoneEvent> = {}): SyncDoneEvent {
  return {
    job_id: jobId,
    outcome: "result",
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
  _resetSubjectsForTest();
  syncJobsRead.mockReset();
});

afterEach(() => {
  _resetSyncJobsForTest();
  _resetSubjectsForTest();
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

    // The replay carries a DIFFERENT verdict: settlement is idempotent per
    // job_id, so the first delivery's outcome is the one the dialog keeps.
    syncDoneFromEvent(doneEvent(7, { outcome: "cancelled", error: "context canceled" }));

    expect(seenA).toHaveLength(1); // idempotent per job_id: no second settle
    expect(seenA[0]?.outcome).toBe("result");
    // A late watcher of the settled job reads the FIRST verdict too, so a
    // replay can never re-render a settled job as something else.
    const lateA: (SyncDoneEvent | null)[] = [];
    watchSyncJob(7, (ev) => lateA.push(ev));
    expect(lateA[0]?.outcome).toBe("result");
    expect(seenB).toHaveLength(0); // B untouched by A's replay
    syncDoneFromEvent(doneEvent(8, { outcome: "crash" }));
    expect(seenB).toHaveLength(1);
    expect(seenB[0]?.outcome).toBe("crash");
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

describe("reattachSyncWatches (the stream's jobs leg)", () => {
  it("releases a watcher whose job the registry shows terminal, so its owner re-attaches", async () => {
    holdJobs();
    const seen: (SyncDoneEvent | null)[] = [];
    watchSyncJob(9, (ev) => seen.push(ev));
    syncJobsRead.mockResolvedValue([job(9, "done", { outcome: "result" })]);

    await reattachSyncWatches();

    expect(seen).toStrictEqual([null]);
    expect(syncJobsRead).toHaveBeenCalledTimes(1);
  });

  it("releases a watcher whose job is GONE from the registry", async () => {
    holdJobs();
    const seen: (SyncDoneEvent | null)[] = [];
    watchSyncJob(9, (ev) => seen.push(ev));
    syncJobsRead.mockResolvedValue([job(12, "queued")]);

    await reattachSyncWatches();

    expect(seen).toStrictEqual([null]);
  });

  it("keeps a watcher whose job is still live; its sync:done settles it later", async () => {
    holdJobs();
    const seen: (SyncDoneEvent | null)[] = [];
    watchSyncJob(9, (ev) => seen.push(ev));
    syncJobsRead.mockResolvedValue([job(9, "running")]);

    await reattachSyncWatches();
    expect(seen).toStrictEqual([]);
    expect(versionMap().has(SUBJECT_JOBS)).toBe(true); // still watched, still held

    syncDoneFromEvent(doneEvent(9));
    expect(seen.map((e) => e?.job_id)).toStrictEqual([9]);
  });

  it("with no watcher it reads nothing and drops the jobs subject", async () => {
    holdJobs();

    await reattachSyncWatches();

    expect(syncJobsRead).not.toHaveBeenCalled();
    expect(versionMap().has(SUBJECT_JOBS)).toBe(false);
  });

  it("passes the run's signal to the read and fails the leg when the read fails", async () => {
    holdJobs();
    watchSyncJob(9, () => undefined);
    syncJobsRead.mockResolvedValue(null);
    const ctrl = new AbortController();

    await expect(reattachSyncWatches(ctrl.signal)).rejects.toThrow("jobs leg failed");
    expect(syncJobsRead).toHaveBeenCalledWith(undefined, { signal: ctrl.signal });
  });
});

describe("the jobs subject is held exactly while a watcher exists", () => {
  it("the last watcher's settlement drops it", () => {
    holdJobs();
    watchSyncJob(7, () => undefined);
    watchSyncJob(8, () => undefined);

    syncDoneFromEvent(doneEvent(7));
    expect(versionMap().has(SUBJECT_JOBS)).toBe(true);
    syncDoneFromEvent(doneEvent(8));
    expect(versionMap().has(SUBJECT_JOBS)).toBe(false);
  });

  it("unwatching the last watcher drops it", () => {
    holdJobs();
    const unwatch = watchSyncJob(7, () => undefined);

    unwatch();

    expect(versionMap().has(SUBJECT_JOBS)).toBe(false);
  });

  it("a restart drops it with the correlation", () => {
    holdJobs();
    watchSyncJob(7, () => undefined);

    clearSyncCorrelation();

    expect(versionMap().has(SUBJECT_JOBS)).toBe(false);
  });
});
