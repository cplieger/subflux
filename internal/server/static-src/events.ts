// The resumable SSE connection (E3). See subflux-ui.md "Live UI updates" for
// the full protocol (boot epoch, watermark, ordered transaction).

import * as notify from "./notify.js";
import { healFromCoverageEvent, resetCoverageHeal, subsumeDirtyRoots } from "./coverage-heal.js";
import { noteHistoryMutation } from "./history.js";
import {
  abortPoll,
  applyActivityEvent,
  applyAlertEvent,
  applyProviderEvent,
  pollStatus,
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
import { currentRouteKey, dispatchTransactionPageLeg } from "./page-leg.js";
import { clearSyncCorrelation, syncDoneFromEvent } from "./sync-jobs.js";
import { noteServerRestart } from "./search.js";
import { registerCleanup } from "@cplieger/actions";
import {
  EPOCH_TIMEOUT_MS,
  REPLAY_BUDGET,
  SSE_RECONNECT_MS,
  SSE_MAX_RECONNECT_MS,
  VERDICT_BUFFER_CAP,
  VISIBILITY_DEBOUNCE_MS,
} from "./constants.js";
import { PATH_EVENTS, coverageMoviesRaw, coverageSeriesRaw } from "./wire/client.gen.js";
import type { QueryValue } from "./wire/client.gen.js";
import {
  decodeActivityEvent,
  decodeAlertEvent,
  decodeCoverageEvent,
  decodeEpochEvent,
  decodeNotifyEvent,
  decodeProviderEvent,
  decodeScanEvent,
  decodeSyncDoneEvent,
} from "./wire/decoders.gen.js";
import type {
  ActivityEvent,
  AlertEvent,
  CoverageEvent,
  EpochEvent,
  EventData,
  NotifyEvent,
  ProviderEvent,
  ScanEvent,
  SyncDoneEvent,
} from "./wire/types.gen.js";
import type { Decoder } from "./validators.js";

// --- Typed SSE event payloads ---

// null on a malformed frame so a bad event can't throw out of a listener.
function decodeSSE<T extends EventData>(e: MessageEvent, decoder: Decoder<T>): T | null {
  try {
    const env = JSON.parse(e.data as string) as { data?: unknown };
    return decoder(env.data);
  } catch {
    return null;
  }
}

// --- Module state ---

// `bootID` is the frame's SOURCE boot (stamped at classification or at hold
// time), so a deferred application can tell an old-boot frame from a current one.
interface BufferedFrame {
  type:
    | "coverage"
    | "notify"
    | "scan:start"
    | "scan:done"
    | "sync:done"
    | "activity"
    | "alert"
    | "provider";
  payload: EventData;
  id: number | null;
  bootID: string | null;
}

// `revoked` flips on abort — results already applied stay applied, but an
// unlanded leg's landing becomes a no-op.
interface Transaction {
  head: number;
  revoked: boolean;
}

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempt = 0;

// The stored boot identity (null = boot: no epoch seen this page load). The
// watermark is COMMIT-ONLY; real frames never advance it. appliedHigh
// advances by max over applied frame ids and resets on boot change.
let bootID: string | null = null;
let watermark: number | null = null;
let appliedHigh: number | null = null;

// forceLatch is set by an abort (teardown + recreate with no cursor);
// downPeriodLatch is set by an epoch-less down period and buys ONE
// precautionary refetch. Both coalesce into one transaction and clear on commit.
let forceLatch = false;
let downPeriodLatch = false;

// Per-connection state.
let epochSeen = false;
let presentedCursor = false;
let latchedConnect = false;
let epochTimer: ReturnType<typeof setTimeout> | null = null;
let verdictBuffer: BufferedFrame[] = [];

// Post-epoch frames > head during a transaction; applied ascending on
// commit. On abort, KEPT for the next valid epoch — a same-boot frame
// applies in full, an old-boot frame applies through the boot-change arm.
let holdQueue: BufferedFrame[] = [];

let txn: Transaction | null = null;

// Toast dedupe, keyed (boot_id, frame_id); resets with the namespace on a
// boot change, AFTER the old namespace's held payloads applied.
const appliedToastKeys = new Set<string>();

// Resolved by the first valid epoch or a degrade. app.ts gates the boot
// route apply on it; a degraded boot's ungated load is superseded later
// under B2's generation guard.
let bootGateResolve: () => void = () => {
  /* replaced below */
};
let bootGatePromise = new Promise<void>((res) => {
  bootGateResolve = res;
});
let bootGateSettled = false;
let degradedBoot = false;

/** Resolves when the first epoch arrives or the gate degrades. */
export function bootGate(): Promise<void> {
  return bootGatePromise;
}

function settleBootGate(): void {
  if (!bootGateSettled) {
    bootGateSettled = true;
    bootGateResolve();
  }
}

function degradeBoot(): void {
  if (bootID === null && !bootGateSettled) {
    degradedBoot = true;
  }
  settleBootGate();
}

// --- Connection lifecycle ---

/** Synthetic cursor: presents ?last_id=watermark, withheld under the force
 *  latch or when appliedHigh - watermark exceeds REPLAY_BUDGET. */
function connectURL(): string {
  // A zero watermark is a commit at an empty ring, not a cursor (server ids
  // start at 1).
  if (forceLatch || watermark === null || watermark === 0) {
    return PATH_EVENTS;
  }
  if (appliedHigh !== null && appliedHigh - watermark > REPLAY_BUDGET) {
    return PATH_EVENTS;
  }
  return `${PATH_EVENTS}?last_id=${String(watermark)}`;
}

export function connect(): void {
  if (eventSource) {
    return;
  }

  // registerCleanup guarantees teardown fires before an unload/soft-nav can
  // leave timers firing into a torn-down DOM.
  registerCleanup(disconnect);

  const url = connectURL();
  presentedCursor = url !== PATH_EVENTS;
  latchedConnect = forceLatch;
  epochSeen = false;
  verdictBuffer = [];

  eventSource = new EventSource(url);

  eventSource.addEventListener("open", () => {
    setStatusDegraded(false);
    // Attempt counter resets only on a non-latched open and on commit, so a
    // persistent outage costs at most one transaction per SSE_MAX_RECONNECT_MS.
    if (!latchedConnect) {
      reconnectAttempt = 0;
    }
    // EPOCH_TIMEOUT_MS prices a SILENT open stream only; refusals fail fast
    // through the error path.
    clearEpochTimer();
    epochTimer = setTimeout(() => {
      epochTimer = null;
      undecodableEpoch();
    }, EPOCH_TIMEOUT_MS);
  });

  eventSource.addEventListener("epoch", (e: MessageEvent) => {
    // Must not read lastEventId: the epoch carries no id.
    clearEpochTimer();
    if (epochSeen) {
      return; // server writes exactly one epoch per connection
    }
    const payload = decodeSSE(e, decodeEpochEvent);
    if (!payload) {
      undecodableEpoch();
      return;
    }
    epochSeen = true;
    void handleEpoch(payload);
  });

  eventSource.addEventListener("coverage", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeCoverageEvent);
    if (payload) {
      routeFrame("coverage", payload, e);
    }
  });
  eventSource.addEventListener("notify", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeNotifyEvent);
    if (payload) {
      routeFrame("notify", payload, e);
    }
  });
  eventSource.addEventListener("scan:start", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeScanEvent);
    if (payload) {
      routeFrame("scan:start", payload, e);
    }
  });
  eventSource.addEventListener("scan:done", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeScanEvent);
    if (payload) {
      routeFrame("scan:done", payload, e);
    }
  });
  eventSource.addEventListener("sync:done", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeSyncDoneEvent);
    if (payload) {
      routeFrame("sync:done", payload, e);
    }
  });
  eventSource.addEventListener("activity", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeActivityEvent);
    if (payload) {
      routeFrame("activity", payload, e);
    }
  });
  eventSource.addEventListener("alert", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeAlertEvent);
    if (payload) {
      routeFrame("alert", payload, e);
    }
  });
  eventSource.addEventListener("provider", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeProviderEvent);
    if (payload) {
      routeFrame("provider", payload, e);
    }
  });

  eventSource.addEventListener("error", () => {
    const es = eventSource;
    if (!es) {
      return;
    }
    if (txn) {
      abortTransaction(txn);
      return;
    }
    if (es.readyState === EventSource.CLOSED) {
      // Refusals land here immediately: fail fast, without waiting the
      // epoch deadline.
      if (!epochSeen) {
        degradeBoot();
        downPeriodLatch = true;
      }
      verdictBuffer = [];
      clearEpochTimer();
      eventSource = null;
      scheduleReconnect();
    } else {
      // Browser retries this connection itself, carrying the real
      // Last-Event-ID header. Reset per-connection state so pre-epoch
      // frames buffer again.
      if (!epochSeen) {
        degradeBoot();
        downPeriodLatch = true;
      }
      verdictBuffer = [];
      epochSeen = false;
      clearEpochTimer();
    }
  });
}

function clearEpochTimer(): void {
  if (epochTimer) {
    clearTimeout(epochTimer);
    epochTimer = null;
  }
}

/** Undecodable epoch or deadline expiry on a silent open stream: the
 *  connection cannot be trusted. */
function undecodableEpoch(): void {
  degradeBoot();
  downPeriodLatch = true;
  verdictBuffer = [];
  teardownConnection();
  scheduleReconnect();
}

function teardownConnection(): void {
  clearEpochTimer();
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
}

function scheduleReconnect(): void {
  setStatusDegraded(true);
  if (reconnectTimer) {
    return;
  }
  const base = SSE_RECONNECT_MS * Math.pow(2, reconnectAttempt);
  const jitter = Math.random() * SSE_RECONNECT_MS;
  const delay = Math.min(base + jitter, SSE_MAX_RECONNECT_MS);
  reconnectAttempt++;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connect();
  }, delay);
}

function disconnect(): void {
  if (txn) {
    // Kill deliberately (hidden tab, page unload): abort first so the
    // reconnect it schedules is cancelled below, and the latch makes the
    // next connect's epoch transact unconditionally.
    abortTransaction(txn);
  }
  teardownConnection();
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  // reconnectAttempt deliberately NOT reset: it resets only on a
  // non-latched open and on commit. A deliberate teardown is not DOWN.
  setStatusDegraded(false);
  abortPoll();
}

// --- Frame routing, the replay table, and counter advancement ---

function routeFrame(type: BufferedFrame["type"], payload: EventData, e: MessageEvent): void {
  const id = e.lastEventId ? Number(e.lastEventId) : null;
  if (!epochSeen) {
    // Pre-epoch frames can only be replay; buffer until the verdict. The
    // cap is a heuristic belt against unbounded buffering.
    verdictBuffer.push({ type, payload, id, bootID: null });
    if (verdictBuffer.length > VERDICT_BUFFER_CAP) {
      verdictBuffer = [];
      forceLatch = true;
      teardownConnection();
      scheduleReconnect();
    }
    return;
  }
  if (txn && id !== null && id > txn.head) {
    // Post-epoch frame beyond the snapshot the legs are reading.
    holdQueue.push({ type, payload, id, bootID });
    return;
  }
  applyFrame({ type, payload, id, bootID }, true);
}

/** Toast dedupe by (boot_id, frame_id); a frame without an id applies. */
function dedupeToast(f: BufferedFrame): boolean {
  if (f.id === null) {
    return true;
  }
  const key = `${f.bootID ?? ""}:${String(f.id)}`;
  if (appliedToastKeys.has(key)) {
    return false;
  }
  appliedToastKeys.add(key);
  return true;
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

/** THE REPLAY TABLE, exhaustive over the union. Idempotency per type:
 *  coverage/activity/alert/provider re-apply through idempotent appliers;
 *  notify/scan:start dedupe by (boot_id, frame_id); scan:done is a no-op
 *  (state rides the terminal activity upsert + per-root coverage events);
 *  sync:done is idempotent per job_id; epoch is never replayed (no id). */
function applyFrame(f: BufferedFrame, advanceCounters: boolean): void {
  switch (f.type) {
    case "coverage":
      // History trigger observes OUTSIDE the heal gate: a poller import on
      // a fresh /history tab must reload even when no collection is loaded.
      noteHistoryMutation();
      healFromCoverageEvent(f.payload as CoverageEvent);
      break;
    case "notify":
      if (dedupeToast(f)) {
        showNotifyToast(f.payload as NotifyEvent);
      }
      break;
    case "scan:start": {
      if (dedupeToast(f)) {
        const p = f.payload as ScanEvent;
        notify.info(`Scan started: ${p.detail || p.action || "Scan"}`);
      }
      break;
    }
    case "scan:done":
      break;
    case "activity": {
      const p = f.payload as ActivityEvent;
      applyActivityEvent(p);
      if (p.op !== "remove" && p.entry?.done) {
        noteHistoryMutation();
      }
      break;
    }
    case "alert":
      applyAlertEvent(f.payload as AlertEvent);
      break;
    case "provider":
      applyProviderEvent(f.payload as ProviderEvent);
      break;
    case "sync:done":
      syncDoneFromEvent(f.payload as SyncDoneEvent);
      break;
  }
  if (advanceCounters && f.id !== null) {
    appliedHigh = appliedHigh === null ? f.id : Math.max(appliedHigh, f.id);
  }
}

/** Boot-change application arm: from an old boot, ONLY the
 *  non-reconstructible payloads apply (notify toasts, sync:done — a restart
 *  drops the server's job registry, so a held settlement is the only
 *  delivery). State-bearing frames are skipped: the new transaction's legs
 *  are strictly newer authority. No counter advancement. */
function applyOldBootFrame(f: BufferedFrame): void {
  if (f.type === "notify" && dedupeToast(f)) {
    showNotifyToast(f.payload as NotifyEvent);
  }
  if (f.type === "sync:done") {
    syncDoneFromEvent(f.payload as SyncDoneEvent);
  }
}

// --- The epoch: triggers and the ordered transaction ---

function maxKnown(a: number | null, b: number | null): number | null {
  if (a === null) {
    return b;
  }
  if (b === null) {
    return a;
  }
  return Math.max(a, b);
}

async function handleEpoch(epoch: EpochEvent): Promise<void> {
  const isBoot = bootID === null;
  const bootChanged = !isBoot && bootID !== epoch.boot_id;
  const latched = forceLatch || downPeriodLatch;

  // Order matters: reset counters -> apply the abort-deferred holdQueue ->
  // classify verdictBuffer -> legs -> commit -> drain. Classification runs
  // BEFORE trigger evaluation, so a header-carried native retry advances
  // appliedHigh to head without this module ever reading lastEventId.
  if (bootChanged) {
    watermark = null;
    appliedHigh = null;
  }

  if (holdQueue.length > 0 && !txn) {
    const held = holdQueue;
    holdQueue = [];
    held.sort((a, b) => (a.id ?? 0) - (b.id ?? 0));
    for (const f of held) {
      if (f.bootID === epoch.boot_id) {
        applyFrame(f, true);
      } else {
        applyOldBootFrame(f);
      }
    }
  }
  if (bootChanged) {
    // Sync correlation and download tracking are per-boot too: reported
    // here so this transaction's status read resolves the dead boot's entries.
    appliedToastKeys.clear();
    clearSyncCorrelation();
    noteServerRestart();
  }
  bootID = epoch.boot_id;

  // Classify verdictBuffer: <= head applies and advances appliedHigh; > head
  // is defensive (unreachable by webhttp's ordering, kept as a belt).
  const buffered = verdictBuffer;
  verdictBuffer = [];
  const defensiveHold: BufferedFrame[] = [];
  for (const f of buffered) {
    f.bootID = epoch.boot_id;
    if (f.id !== null && f.id > epoch.head) {
      defensiveHold.push(f);
    } else {
      applyFrame(f, true);
    }
  }

  // Triggers (coalesce with the latches into ONE transaction): epoch gap;
  // boot_id change; a cursor-less non-boot connect whose head exceeds
  // max(watermark, appliedHigh) or finds both unknown; plus boot itself.
  const highWater = maxKnown(watermark, appliedHigh);
  const trigger3 = !presentedCursor && !isBoot && (highWater === null || epoch.head > highWater);
  const transact = isBoot || epoch.gap || bootChanged || latched || trigger3;

  if (!transact) {
    for (const f of defensiveHold) {
      applyFrame(f, true);
    }
    settleBootGate();
    return;
  }

  // Created synchronously before resolving the boot gate, so the route
  // loader finds the collection leg's join registered and joins it instead
  // of double-fetching.
  const t: Transaction = { head: epoch.head, revoked: false };
  txn = t;
  holdQueue.push(...defensiveHold);
  beginTransaction();
  const join = createCollectionLegJoin();
  settleBootGate();
  // One microtask so the gate-released applyRoute sets route state before
  // the legs read it.
  await Promise.resolve();

  const recovery = !isBoot || degradedBoot;
  try {
    // Legs apply on landing; commit is the point all legs have landed.
    await Promise.all([
      collectionLeg(recovery, t, join),
      dispatchTransactionPageLeg(recovery),
      pollStatus(),
    ]);
    if (t.revoked) {
      return; // aborted while legs were landing
    }
    watermark = epoch.head;
    txn = null;
    const held = holdQueue;
    holdQueue = [];
    held.sort((a, b) => (a.id ?? 0) - (b.id ?? 0));
    for (const f of held) {
      applyFrame(f, true);
    }
    forceLatch = false;
    downPeriodLatch = false;
    reconnectAttempt = 0;
    if (join.covered) {
      subsumeDirtyRoots();
    }
  } catch {
    abortTransaction(t);
  } finally {
    if (txn === t) {
      txn = null;
    }
    settleTransaction();
    releaseCoverageTombstones();
  }
}

/** Revoke unlanded legs (their landings become no-ops), keep the holdQueue
 *  for the next epoch, latch, and rejoin the ladder cursor-less. */
function abortTransaction(t: Transaction): void {
  if (t.revoked) {
    return;
  }
  t.revoked = true;
  if (txn === t) {
    txn = null;
  }
  forceLatch = true;
  teardownConnection();
  scheduleReconnect();
  settleTransaction();
  releaseCoverageTombstones();
}

// --- The collection leg ---

interface CollectionLegJoinHandle {
  resolve: (r: "landed" | "failed" | "uncovered") => void;
  covered: boolean;
}

function createCollectionLegJoin(): CollectionLegJoinHandle {
  const handle: CollectionLegJoinHandle = {
    covered: false,
    resolve: () => {
      /* replaced below */
    },
  };
  const p = new Promise<"landed" | "failed" | "uncovered">((res) => {
    handle.resolve = res;
  });
  setCollectionLegJoin(p);
  return handle;
}

/** Fetches the pair on the raw generated client (zero automatic retries)
 *  and REJECTS on genuine transport failure, never the loader's
 *  null-collapsing read. A /history or deep-link session's leg is empty. */
async function collectionLeg(
  recovery: boolean,
  t: Transaction,
  join: CollectionLegJoinHandle,
): Promise<void> {
  const needPair = registeredCollections().size > 0 || currentRouteKey() === "library";
  if (!needPair) {
    join.resolve("uncovered");
    setCollectionLegJoin(null);
    return;
  }
  join.covered = true;
  const endWrite = beginCoveredPairWrite();
  try {
    const q: Record<string, QueryValue> | undefined = recovery ? { recovery: 1 } : undefined;
    const [series, movies] = await Promise.all([coverageSeriesRaw(q), coverageMoviesRaw(q)]);
    if (t.revoked) {
      // An orphaned stale pair landing after the successor's fresher one
      // must not revert healed rows.
      join.resolve("failed");
      return;
    }
    if (!series.ok || !movies.ok) {
      join.resolve("failed");
      const status = !series.ok ? series.status : movies.status;
      throw new Error(series.error ?? movies.error ?? `collection leg failed (${String(status)})`);
    }
    abortInFlightPairFetch();
    applyCoveragePair(series.data ?? [], movies.data ?? []);
    join.resolve("landed");
  } finally {
    endWrite();
    setCollectionLegJoin(null);
  }
}

// --- Visibility pause ---

let visibilityTimer: ReturnType<typeof setTimeout> | null = null;

document.addEventListener("visibilitychange", () => {
  if (visibilityTimer) {
    clearTimeout(visibilityTimer);
    visibilityTimer = null;
  }
  if (document.hidden) {
    disconnect();
  } else {
    visibilityTimer = setTimeout(() => {
      visibilityTimer = null;
      connect();
    }, VISIBILITY_DEBOUNCE_MS);
  }
});

// --- Test seam ---

/** Tear the connection down and reset EVERY piece of module state. */
export function _resetEventsForTest(): void {
  if (txn) {
    txn.revoked = true;
    txn = null;
  }
  teardownConnection();
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (visibilityTimer) {
    clearTimeout(visibilityTimer);
    visibilityTimer = null;
  }
  reconnectAttempt = 0;
  bootID = null;
  watermark = null;
  appliedHigh = null;
  forceLatch = false;
  downPeriodLatch = false;
  epochSeen = false;
  presentedCursor = false;
  latchedConnect = false;
  verdictBuffer = [];
  holdQueue = [];
  appliedToastKeys.clear();
  bootGateSettled = false;
  degradedBoot = false;
  bootGatePromise = new Promise<void>((res) => {
    bootGateResolve = res;
  });
  setCollectionLegJoin(null);
  resetCoverageHeal();
}

/** Test-only accessor for the counters and latches. */
export function _stateForTest(): {
  bootID: string | null;
  watermark: number | null;
  appliedHigh: number | null;
  forceLatch: boolean;
  downPeriodLatch: boolean;
  holdQueueLength: number;
  reconnectAttempt: number;
} {
  return {
    bootID,
    watermark,
    appliedHigh,
    forceLatch,
    downPeriodLatch,
    holdQueueLength: holdQueue.length,
    reconnectAttempt,
  };
}
