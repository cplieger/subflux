// sync-jobs.ts — client-side sync job settlement (D3).
//
// The server owns async sync jobs; this module owns the client's view of
// them: events.ts routes every sync:done frame here (live and replayed),
// watchers await one job's terminal by the 202's job_id, and the reload path
// re-attaches through the jobs read.
//
// Settlement is idempotent per job_id: a replayed frame settles nothing
// twice, and a later job on the SAME file is a different job_id, so a replay
// for job A can never settle job B. Correlation is per server epoch — a
// restart clears the watchers and the settled namespace together, and
// pending watchers resolve null so their owners re-attach via the jobs read.
// The `jobs` digest subject is held exactly while a watcher exists.

import { syncJobs } from "./wire/client.gen.js";
import type { Job, SyncDoneEvent } from "./wire/types.gen.js";
import { refKey, type FileRefArgs } from "./file-ref.js";
import { SUBJECT_JOBS, forgetSubject } from "./subjects.js";

/** A watcher's answer: the job's terminal event, or null when the
 *  correlation was lost (a restart, or a registry change the stream missed)
 *  and the owner must re-attach via the jobs read. */
export type SyncJobWatcher = (ev: SyncDoneEvent | null) => void;

// Terminal events kept for late watchers (a dialog re-watching a job whose
// event already arrived) and for replay dedupe. Bounded drop-oldest: a
// dropped entry converges via the jobs read.
const SETTLED_CAP = 64;
const settled = new Map<number, SyncDoneEvent>();
const watchers = new Map<number, SyncJobWatcher>();

/** Await one job's terminal event. An already-settled job answers
 *  immediately; otherwise the callback fires on the job's sync:done (or with
 *  null when the correlation is lost). Returns the unwatch disposer. */
export function watchSyncJob(jobId: number, cb: SyncJobWatcher): () => void {
  const done = settled.get(jobId);
  if (done) {
    cb(done);
    return () => {
      /* already settled */
    };
  }
  watchers.set(jobId, cb);
  return () => {
    if (watchers.get(jobId) === cb) {
      watchers.delete(jobId);
      forgetJobsWhenUnwatched();
    }
  };
}

/** The `jobs` subject is held only while something awaits a job: with no
 *  watcher left, a registry change has no consumer, so the digest must not
 *  name it. Every jobs GET re-observes it through the transport. */
function forgetJobsWhenUnwatched(): void {
  if (watchers.size === 0) {
    forgetSubject(SUBJECT_JOBS);
  }
}

/** The stream's `jobs` leg: the digest reported the registry moved while
 *  this tab missed its frames, so re-read it and release every watcher whose
 *  job is no longer live (terminal, pruned, or gone with a restart) — the
 *  owner re-attaches via attachSyncJob and finds the terminal record, or
 *  nothing. A watcher on a still-live job keeps waiting for its sync:done. */
export async function reattachSyncWatches(signal?: AbortSignal): Promise<void> {
  if (watchers.size === 0) {
    forgetSubject(SUBJECT_JOBS);
    return;
  }
  const jobs = await syncJobs(undefined, signal === undefined ? undefined : { signal });
  if (jobs === null) {
    throw new Error("jobs leg failed");
  }
  const live = new Set<number>();
  for (const job of jobs) {
    if (job.state === "queued" || job.state === "running") {
      live.add(job.job_id);
    }
  }
  const lost: SyncJobWatcher[] = [];
  for (const [jobId, w] of watchers) {
    if (!live.has(jobId)) {
      watchers.delete(jobId);
      lost.push(w);
    }
  }
  forgetJobsWhenUnwatched();
  for (const w of lost) {
    w(null);
  }
}

/** THE REPLAY-TABLE ROW: apply one sync:done frame, idempotently per job_id
 *  (a replayed frame for a settled job is a no-op). Called by events.ts for
 *  live frames and replays. */
export function syncDoneFromEvent(ev: SyncDoneEvent): void {
  if (settled.has(ev.job_id)) {
    return;
  }
  settled.set(ev.job_id, ev);
  if (settled.size > SETTLED_CAP) {
    const oldest = settled.keys().next();
    if (!oldest.done) {
      settled.delete(oldest.value);
    }
  }
  const w = watchers.get(ev.job_id);
  if (w) {
    watchers.delete(ev.job_id);
    forgetJobsWhenUnwatched();
    w(ev);
  }
}

/** RESTART: job_id correlation holds within one server epoch only. Clears
 *  the settled namespace and resolves every pending watcher null — the
 *  owners re-attach via the jobs read (which, after a restart, is empty: the
 *  server registry does not survive it). events.ts calls this when the
 *  stream's hello or its digest names a new epoch. */
export function clearSyncCorrelation(): void {
  const pending = [...watchers.values()];
  watchers.clear();
  settled.clear();
  forgetSubject(SUBJECT_JOBS);
  for (const w of pending) {
    w(null);
  }
}

/** What a re-attach found for one subtitle file. */
export type SyncAttachState =
  { kind: "live"; job: Job } | { kind: "done"; job: Job } | { kind: "none" };

/** RE-ATTACH via the jobs read: prefer a queued/running job for the FileRef,
 *  else the newest terminal one by the registry's total order (the read is
 *  already sorted accepted_at DESC, job_id DESC). Answers "none" when the
 *  read fails or nothing matches. */
export async function attachSyncJob(ref: FileRefArgs): Promise<SyncAttachState> {
  const jobs = await syncJobs();
  if (!jobs) {
    return { kind: "none" };
  }
  const key = refKey(ref);
  let newestDone: Job | null = null;
  for (const job of jobs) {
    if (refKey(job.file_ref) !== key) {
      continue;
    }
    if (job.state === "queued" || job.state === "running") {
      return { kind: "live", job };
    }
    newestDone ??= job;
  }
  return newestDone ? { kind: "done", job: newestDone } : { kind: "none" };
}

/** Test-only: reset every piece of module state. */
export function _resetSyncJobsForTest(): void {
  watchers.clear();
  settled.clear();
}
