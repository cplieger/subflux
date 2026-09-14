// events.ts — the live-update stream and its reconciliation body.
//
// The connection is @cplieger/sse's: one SharedWorker per browser profile
// owns the stream (sse-worker.ts) and this tab attaches to it, or runs
// createStream itself where the worker cannot be reached. Cursor resume, the
// backoff ladder, the visibility and online folds, the silence watchdog and
// hold-and-drain around a revalidation are the library's. What stays here is
// subflux's: the typed frame table, the digest subjects the legs refetch, and
// the ordered transaction that runs when the server cannot vouch for what
// this tab holds. The digest is per tab in both topologies: the worker fans a
// run to every tab and each tab asks about the subjects it holds.

import {
  attachToWorker,
  bindListener,
  createDigestClient,
  createStream,
  type ClientState,
  type DigestClient,
  type DigestResult,
  type Frame,
  type LifecycleEvent,
  type RevalidateContext,
  type Subject,
  type TabAttachment,
} from "@cplieger/sse";
import * as notify from "./notify.js";
import { healFromCoverageEvent, resetCoverageHeal, subsumeDirtyRoots } from "./coverage-heal.js";
import { noteHistoryMutation } from "./history.js";
import {
  abortPoll,
  applyActivityEvent,
  applyAlertEvent,
  applyProviderEvent,
  pollStatus,
  setStatusAttached,
  setStatusDegraded,
} from "./status.js";
import { applyCoveragePair, abortInFlightPairFetch } from "./coverage.js";
import {
  beginCoveredPairWrite,
  registeredCollections,
  releaseCoverageTombstones,
  setCollectionLegJoin,
} from "./coverage-store.js";
import { beginTransaction, settleTransaction } from "./transaction.js";
import { currentRouteKey, dispatchTransactionPageLeg, routeSubject } from "./page-leg.js";
import { clearSyncCorrelation, reattachSyncWatches, syncDoneFromEvent } from "./sync-jobs.js";
import { noteServerRestart } from "./search.js";
import { authFetch, handleSessionExpiry } from "./api-client.js";
import { registerCleanup } from "@cplieger/actions";
import {
  PATH_EVENTS,
  PATH_EVENTS_ALIVE,
  PATH_EVENTS_SYNC,
  coverageMoviesRaw,
  coverageSeriesRaw,
} from "./wire/client.gen.js";
import {
  decodeActivityEvent,
  decodeAlertEvent,
  decodeCoverageEvent,
  decodeNotifyEvent,
  decodeProviderEvent,
  decodeScanEvent,
  decodeSyncDoneEvent,
} from "./wire/decoders.gen.js";
import type {
  ActivityEvent,
  AlertEvent,
  CoverageEvent,
  NotifyEvent,
  ProviderEvent,
  ScanEvent,
  SyncDoneEvent,
} from "./wire/types.gen.js";
import type { Decoder } from "./validators.js";
import { forgetSubject, versionMap } from "./subjects.js";

// --- Typed SSE event payloads ---

type AppFrame =
  | { type: "coverage"; payload: CoverageEvent }
  | { type: "notify"; payload: NotifyEvent }
  | { type: "scan:start"; payload: ScanEvent }
  | { type: "scan:done"; payload: ScanEvent }
  | { type: "sync:done"; payload: SyncDoneEvent }
  | { type: "activity"; payload: ActivityEvent }
  | { type: "alert"; payload: AlertEvent }
  | { type: "provider"; payload: ProviderEvent };

// null on a malformed frame so a bad event can't throw out of the handler.
function decodeSSE<T>(data: string, decoder: Decoder<T>): T | null {
  try {
    const env = JSON.parse(data) as { data?: unknown };
    return decoder(env.data);
  } catch {
    return null;
  }
}

/** The frame table: the decoder is picked by the frame's event name, and a
 *  name this bundle does not know (the header-gated legacy `epoch` frame
 *  included) decodes to nothing. */
function decodeFrame(frame: Frame): AppFrame | null {
  const d = frame.data;
  switch (frame.type) {
    case "coverage": {
      const payload = decodeSSE(d, decodeCoverageEvent);
      return payload ? { type: "coverage", payload } : null;
    }
    case "notify": {
      const payload = decodeSSE(d, decodeNotifyEvent);
      return payload ? { type: "notify", payload } : null;
    }
    case "scan:start": {
      const payload = decodeSSE(d, decodeScanEvent);
      return payload ? { type: "scan:start", payload } : null;
    }
    case "scan:done": {
      const payload = decodeSSE(d, decodeScanEvent);
      return payload ? { type: "scan:done", payload } : null;
    }
    case "sync:done": {
      const payload = decodeSSE(d, decodeSyncDoneEvent);
      return payload ? { type: "sync:done", payload } : null;
    }
    case "activity": {
      const payload = decodeSSE(d, decodeActivityEvent);
      return payload ? { type: "activity", payload } : null;
    }
    case "alert": {
      const payload = decodeSSE(d, decodeAlertEvent);
      return payload ? { type: "alert", payload } : null;
    }
    case "provider": {
      const payload = decodeSSE(d, decodeProviderEvent);
      return payload ? { type: "provider", payload } : null;
    }
    default:
      return null;
  }
}

// --- Module state ---

let attachment: TabAttachment | null = null;
let digest: DigestClient | null = null;
let unbind: (() => void) | null = null;
// `restartedFor` makes the restart bookkeeping run once per epoch however many
// binds announce it (the per-tab stream's on a hello, this module's on a
// worker hello or a digest's must_refetch).
let restartedFor: string | null = null;

const CLIENT_TAG_KEY = "subflux.sse_client";

function storedClientTag(): string | null {
  try {
    return localStorage.getItem(CLIENT_TAG_KEY);
  } catch {
    return null;
  }
}

/** The per-profile presence tag (SSE-Client): 16 hex minted once and kept
 *  in localStorage, so every tab of one profile presents the same one. */
function clientTag(): string {
  const stored = storedClientTag();
  if (stored !== null && /^[0-9a-f]{16}$/.test(stored)) {
    return stored;
  }
  const bytes = crypto.getRandomValues(new Uint8Array(8));
  const tag = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  try {
    localStorage.setItem(CLIENT_TAG_KEY, tag);
  } catch {
    /* a profile that cannot persist presents a per-page tag */
  }
  return tag;
}

// --- Connection lifecycle ---

/** The profile's worker, constructed by its content-hashed bundle URL: a
 *  redeploy's tabs cannot attach to a worker still running the old script. */
function spawnWorker(): SharedWorker {
  const name = __SSE_WORKER_URL__.slice(__SSE_WORKER_URL__.lastIndexOf("/") + 1);
  return new SharedWorker(__SSE_WORKER_URL__, {
    name,
    type: "classic",
    credentials: "same-origin",
  });
}

export function connect(): void {
  if (attachment) {
    return;
  }
  const tag = clientTag();
  const headers = { "SSE-Client": tag };
  digest = createDigestClient({ url: PATH_EVENTS_SYNC, headers, fetch: authFetch });
  unbind = bindListener(versionMap(), onBind);
  attachment = attachToWorker({
    spawn: spawnWorker,
    // The fallback shares the digest's headers record, so the tag the tab
    // writes into it is the one the digest already carries.
    fallback: () => {
      const versions = versionMap();
      const stream = createStream({
        url: PATH_EVENTS,
        headers,
        fetch: authFetch,
        alive: { url: PATH_EVENTS_ALIVE },
        versions,
        onFrame,
        onLifecycle,
        revalidate,
      });
      return { stream, versions, headers };
    },
    onFrame,
    onLifecycle,
    revalidate,
    tag,
  });
  // registerCleanup guarantees teardown fires before an unload/soft-nav can
  // leave timers firing into a torn-down DOM.
  registerCleanup(disconnect);
}

function teardown(cause: "unload" | "logout"): void {
  attachment?.detach(cause);
  attachment = null;
  digest = null;
  unbind?.();
  unbind = null;
  abortPoll();
}

function disconnect(): void {
  teardown("unload");
}

/** Called BEFORE the logout request: a `logout` detach ends the profile's
 *  stream in the worker (every tab's) or this tab's own, so no frame minted
 *  for the dying session is delivered once the server has dropped it. */
export function disconnectForLogout(): void {
  teardown("logout");
}

/** A bind that drops held versions is a restart: the map held versions
 *  another server process minted, and the job registry and the download
 *  tracking died with it, so their correlations are cleared once per epoch.
 *  The per-tab stream binds on its hello; bindTo covers the worker path. */
function onBind(epoch: string, dropped: number): void {
  if (dropped === 0 || restartedFor === epoch) {
    return;
  }
  restartedFor = epoch;
  clearSyncCorrelation();
  noteServerRestart();
}

/** Binds the map to the epoch a hello, a full run or a digest names, so the
 *  legs' stamps land under it. In worker mode the host's stream binds only
 *  its own map, so this is where the tab's follows; a per-tab stream has
 *  already bound it by the time its hello is reported. */
function bindTo(epoch: string): void {
  const versions = versionMap();
  if (versions.epoch() !== epoch) {
    versions.bind(epoch);
  }
}

function onLifecycle(ev: LifecycleEvent): void {
  switch (ev.kind) {
    case "hello":
      bindTo(ev.epoch);
      return;
    case "state":
      setStatusDegraded(ev);
      return;
    case "tab_attached":
      setStatusAttached(ev.state);
      return;
    case "connect_failed":
      if (ev.reason.kind === "status" && ev.reason.status === 401) {
        handleSessionExpiry(401);
      }
      return;
    case "auth_lost":
      handleSessionExpiry(ev.status);
      return;
    case "worker_unavailable":
      console.warn("sse: worker unavailable, this tab streams on its own", ev.cause);
      return;
    case "frame_rejected":
      console.warn("sse: frame rejected", ev.type, ev.error);
      return;
    case "revalidate_failed":
      console.warn("sse: revalidate failed", ev.cause);
      return;
    case "revalidate_timeout":
      console.warn("sse: revalidate timed out");
      return;
    default:
      return;
  }
}

// --- The frame table ---

function onFrame(frame: Frame): void {
  const decoded = decodeFrame(frame);
  if (decoded) {
    applyFrame(decoded);
  }
}

function showNotifyToast(payload: NotifyEvent): void {
  if (payload.level === "error") {
    notify.error(payload.text || "");
  } else if (payload.level === "success") {
    notify.success(payload.text || "");
  } else {
    notify.info(payload.text || "");
  }
}

/** THE REPLAY TABLE, exhaustive over the union. A frame reaches here exactly
 *  once (the library's cursor never redelivers one), so idempotency is per
 *  type only where a replay from the server's ring can carry it again:
 *  coverage/activity/alert/provider re-apply through idempotent appliers,
 *  sync:done is idempotent per job_id, scan:done is a no-op (state rides the
 *  terminal activity upsert + per-root coverage events). */
function applyFrame(f: AppFrame): void {
  switch (f.type) {
    case "coverage":
      // History trigger observes OUTSIDE the heal gate: a poller import on
      // a fresh /history tab must reload even when no collection is loaded.
      noteHistoryMutation();
      healFromCoverageEvent(f.payload);
      break;
    case "notify":
      showNotifyToast(f.payload);
      break;
    case "scan:start":
      notify.info(`Scan started: ${f.payload.detail || f.payload.action || "Scan"}`);
      break;
    case "scan:done":
      break;
    case "activity":
      applyActivityEvent(f.payload);
      if (f.payload.op !== "remove" && f.payload.entry?.done) {
        noteHistoryMutation();
      }
      break;
    case "alert":
      applyAlertEvent(f.payload);
      break;
    case "provider":
      applyProviderEvent(f.payload);
      break;
    case "sync:done":
      syncDoneFromEvent(f.payload);
      break;
  }
}

// --- The revalidate body: the digest and the ordered transaction ---

type Leg = "pair" | "page" | "status" | "jobs";

const ALL_LEGS: ReadonlySet<Leg> = new Set<Leg>(["pair", "page", "status", "jobs"]);

/** Which legs a digest's `changed` subjects name. A subject this tab no
 *  longer renders (a detail root that is not open, history off /history) is
 *  forgotten instead: the map mirrors the open route. */
function legsFor(changed: readonly Subject[]): Set<Leg> {
  const legs = new Set<Leg>();
  const open = routeSubject(currentRouteKey());
  for (const s of changed) {
    switch (s.kind) {
      case "series":
      case "movies":
        legs.add("pair");
        break;
      case "detail":
      case "history":
        if (open !== null && open.kind === s.kind && open.ref === s.ref) {
          legs.add("page");
        } else {
          forgetSubject(s);
        }
        break;
      case "activity":
      case "alerts":
      case "providers":
        legs.add("status");
        break;
      case "jobs":
        legs.add("jobs");
        break;
      default:
        break;
    }
  }
  return legs;
}

async function revalidate(ctx: RevalidateContext): Promise<void> {
  if (ctx.full) {
    // Nothing this tab holds can be vouched for (a restart cleared the map,
    // or the worker re-admitted this tab after it missed frames): the whole
    // body runs without asking.
    if (ctx.epoch !== null) {
      bindTo(ctx.epoch);
    }
    await runLegs(ALL_LEGS, ctx.signal);
    return;
  }
  if (!digest) {
    return;
  }
  const versions = versionMap();
  const r: DigestResult = await digest.check(versions.snapshot(), ctx.signal);
  if (r.kind === "must_refetch") {
    // Bind first, so the legs' stamps land in a map already at the new epoch
    // and the hello that follows has nothing to clear.
    bindTo(r.epoch);
    await runLegs(ALL_LEGS, ctx.signal);
    return;
  }
  for (const s of r.removed) {
    versions.forget(s);
  }
  await runLegs(legsFor(r.changed), ctx.signal);
}

/** Run the named legs as ONE transaction: every leg lands before commit, a
 *  failed or aborted leg rejects (the library keeps the connection unverified
 *  and the map keeps the old versions, so the next digest names the subject
 *  again), and a covered pair landing subsumes the heal's dirty set. */
async function runLegs(legs: ReadonlySet<Leg>, runSignal: AbortSignal): Promise<void> {
  if (legs.size === 0) {
    return;
  }
  // One signal per transaction, chained from the run's: a leg that fails
  // cancels its siblings, so no orphaned leg can land a snapshot older than
  // the transaction that replaces this one.
  const txn = new AbortController();
  const { signal } = txn;
  const onRunAbort = (): void => {
    txn.abort();
  };
  if (runSignal.aborted) {
    txn.abort();
  }
  runSignal.addEventListener("abort", onRunAbort, { once: true });
  beginTransaction();
  try {
    const pair = legs.has("pair") ? collectionLeg(signal) : Promise.resolve(false);
    const work: Promise<unknown>[] = [pair];
    if (legs.has("page")) {
      work.push(dispatchTransactionPageLeg(true, signal));
    }
    if (legs.has("status")) {
      work.push(pollStatus(signal));
    }
    if (legs.has("jobs")) {
      work.push(reattachSyncWatches(signal));
    }
    try {
      await Promise.all(work);
    } catch (e: unknown) {
      txn.abort();
      throw e;
    }
    if (signal.aborted) {
      throw new Error("revalidate aborted");
    }
    if (await pair) {
      subsumeDirtyRoots();
    }
  } finally {
    runSignal.removeEventListener("abort", onRunAbort);
    settleTransaction();
    releaseCoverageTombstones();
  }
}

// --- The collection leg ---

type CollectionLegJoin = "landed" | "failed" | "uncovered";

/** Fetches the pair on the raw generated client (zero automatic retries,
 *  ?recovery=1, the run's signal) and REJECTS on genuine transport failure,
 *  never the loader's null-collapsing read. A /history or deep-link session's
 *  leg is empty. Resolves whether the leg covered the pair; a loader arriving
 *  meanwhile joins it through the coverage store instead of fetching. */
async function collectionLeg(signal: AbortSignal): Promise<boolean> {
  let resolveJoin: (r: CollectionLegJoin) => void = () => {
    /* replaced below */
  };
  setCollectionLegJoin(
    new Promise<CollectionLegJoin>((res) => {
      resolveJoin = res;
    }),
  );
  const needPair = registeredCollections().size > 0 || currentRouteKey() === "library";
  if (!needPair) {
    resolveJoin("uncovered");
    setCollectionLegJoin(null);
    return false;
  }
  const endWrite = beginCoveredPairWrite();
  try {
    const q = { recovery: 1 };
    const [series, movies] = await Promise.all([
      coverageSeriesRaw(q, { signal }),
      coverageMoviesRaw(q, { signal }),
    ]);
    if (signal.aborted) {
      // A sibling leg failed or the run was stopped: this snapshot may be
      // older than the transaction that replaces this one.
      resolveJoin("failed");
      throw new Error("collection leg aborted");
    }
    if (!series.ok || !movies.ok) {
      resolveJoin("failed");
      const status = !series.ok ? series.status : movies.status;
      throw new Error(series.error ?? movies.error ?? `collection leg failed (${String(status)})`);
    }
    abortInFlightPairFetch();
    applyCoveragePair(series.data ?? [], movies.data ?? []);
    resolveJoin("landed");
    return true;
  } finally {
    endWrite();
    setCollectionLegJoin(null);
  }
}

// --- Test seam ---

/** Tear the connection down and reset EVERY piece of module state. */
export function _resetEventsForTest(): void {
  disconnect();
  restartedFor = null;
  settleTransaction();
  setCollectionLegJoin(null);
  resetCoverageHeal();
}

/** Test-only accessor for the connection state and the epoch bookkeeping. */
export function _stateForTest(): {
  mode: "worker" | "fallback" | null;
  stream: ClientState | null;
  knownEpoch: string | null;
} {
  return {
    mode: attachment?.mode() ?? null,
    stream: attachment?.state() ?? null,
    knownEpoch: versionMap().epoch(),
  };
}
