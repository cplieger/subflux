// events.ts — the resumable SSE connection (E3): boot epoch, commit-only
// watermark, and the ordered recovery transaction.
//
// Every connection is answered by exactly one `epoch` handshake (after any
// Last-Event-ID replay, before live delivery) carrying {boot_id, gap, head}.
// Frames arriving BEFORE the epoch buffer in the verdictBuffer (they can only
// be replay); a clean verdict classifies them — ≤ head applies, > head is a
// defensive hold — while a condemned connection's buffer is discarded. A
// trigger (gap, boot change, missed-head evidence, or a latch) runs THE
// ORDERED TRANSACTION: hold post-epoch frames > head, run the legs
// (collection pair, page leg, status fetch) after this connection's
// subscribe, commit by advancing the watermark to the epoch head, drain the
// holdQueue ascending, and clear every pending latch. An abort (anchor died,
// genuine leg failure, typed 429 refusal) rolls nothing back: applied legs
// stay applied, unlanded legs' landings become no-ops, the holdQueue is kept
// for the next epoch (where the boot comparison picks the application arm),
// the forceTransaction latch is set, and the connection is recreated with no
// cursor — the first valid epoch transacts unconditionally.
//
// Module state throughout: a mid-latch reload boots fresh, which is safe —
// the boot transaction refetches everything.

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
import {
  applyCoveragePair,
  abortInFlightPairFetch,
  beginCoverageTransaction,
  beginCoveredPairWrite,
  registeredCollections,
  setCollectionLegJoin,
  settleCoverageTransaction,
} from "./coverage.js";
import { currentRouteKey, dispatchTransactionPageLeg } from "./page-leg.js";
import { clearSyncCorrelation, syncDoneFromEvent } from "./sync-jobs.js";
import { armDownloadRestartSweep } from "./search.js";
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

// SSE frames arrive as { type, data: <event> } where data is one variant of
// the generated EventData union (the sealed events.EventData interface).
// Each named-SSE listener already knows its variant, so it decodes the inner
// payload directly through that variant's generated decoder; the T extends
// EventData bound keeps a non-union decoder out of this path. Returns null on
// a malformed frame so a bad event can't throw out of a listener.
function decodeSSE<T extends EventData>(e: MessageEvent, decoder: Decoder<T>): T | null {
  try {
    const env = JSON.parse(e.data as string) as { data?: unknown };
    return decoder(env.data);
  } catch {
    return null;
  }
}

// --- Module state ---

// One buffered frame. `id` is the SSE frame id (null when absent); `bootID`
// is the frame's SOURCE boot — stamped at classification (verdictBuffer) or
// at hold time (holdQueue) — so a deferred application can tell an old-boot
// frame from a current one.
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

// A running transaction. `head` is its epoch's head (the hold threshold);
// `revoked` flips on abort — results already applied stay applied, but an
// unlanded leg's eventual landing becomes a no-op.
interface Transaction {
  head: number;
  revoked: boolean;
}

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempt = 0;

// The stored boot identity (null = boot: no epoch seen this page load) and
// the two counters. The watermark is COMMIT-ONLY and scoped to one boot_id;
// real frames never advance it. appliedHigh advances by max over applied
// frame ids, resets on boot change, and is never a cursor.
let bootID: string | null = null;
let watermark: number | null = null;
let appliedHigh: number | null = null;

// Latches. forceTransaction is set by an abort (teardown + recreate with no
// cursor; the first valid epoch transacts unconditionally); the down-period
// latch is set by an epoch-less down period and buys ONE precautionary
// refetch. Both coalesce into one transaction and clear on commit.
let forceLatch = false;
let downPeriodLatch = false;

// Per-connection state.
let epochSeen = false;
let presentedCursor = false;
let latchedConnect = false;
let epochTimer: ReturnType<typeof setTimeout> | null = null;
let verdictBuffer: BufferedFrame[] = [];

// The holdQueue: post-epoch frames > head during a transaction. Applied on
// commit (after the snapshot, ascending) — and on abort it is KEPT for the
// next valid epoch, where the per-frame boot comparison picks the arm: a
// same-boot frame applies in full, an old-boot frame applies through the
// boot-change arm (only non-reconstructible payloads, no counter
// advancement, old dedupe namespace) BEFORE any new-boot frame classifies.
let holdQueue: BufferedFrame[] = [];

let txn: Transaction | null = null;

// Toast dedupe, keyed (boot_id, frame_id); the set resets with the namespace
// on a boot change — AFTER the old namespace's held payloads applied.
const appliedToastKeys = new Set<string>();

// The boot gate: resolved by the first valid epoch or by a degrade
// (refusal / failure / deadline — refusals fail fast). app.ts gates the boot
// route apply on it; a degraded boot's ungated load is superseded later
// under B2's generation guard, and the transaction FOLLOWING a degraded boot
// uses recovery semantics.
let bootGateResolve: () => void = () => {
  /* replaced below */
};
let bootGatePromise = new Promise<void>((res) => {
  bootGateResolve = res;
});
let bootGateSettled = false;
let degradedBoot = false;

/** The boot gate: resolves when the first epoch arrives or the gate
 *  degrades. Boot page loads await it on every route. */
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

/** The synthetic cursor: recreate paths present ?last_id = watermark only
 *  with a committed watermark for the CURRENT boot_id (trigger 2 resets it),
 *  the latch clear, and the client pre-filter passing (appliedHigh −
 *  watermark ≤ REPLAY_BUDGET; an unevaluable pre-filter PASSES — the cursor
 *  is presented and the server's budget disjunct is authoritative). */
function connectURL(): string {
  // A zero watermark is a commit at an empty ring: id 0 is not a cursor
  // (server ids start at 1, and the server ignores a zero anyway).
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

  // Drain SSE state on page unload so timers can't fire into a torn-down
  // DOM during the unload window. registerCleanup guarantees deterministic
  // teardown for tests + soft-navigation cases.
  registerCleanup(disconnect);

  const url = connectURL();
  presentedCursor = url !== PATH_EVENTS;
  latchedConnect = forceLatch;
  epochSeen = false;
  verdictBuffer = [];

  eventSource = new EventSource(url);

  eventSource.addEventListener("open", () => {
    // The stream is live again: events own status from here (the epoch's
    // transaction runs the one status fetch), so the degraded 5s poll stops.
    setStatusDegraded(false);
    // The attempt counter resets ONLY on a non-latched open and on COMMIT;
    // a latched ladder keeps climbing so a persistent outage costs at most
    // one transaction per SSE_MAX_RECONNECT_MS.
    if (!latchedConnect) {
      reconnectAttempt = 0;
    }
    // EPOCH_TIMEOUT_MS prices a SILENT open stream only (refusals fail fast
    // through the error path); expiry = undecodable handling.
    clearEpochTimer();
    epochTimer = setTimeout(() => {
      epochTimer = null;
      undecodableEpoch();
    }, EPOCH_TIMEOUT_MS);
  });

  eventSource.addEventListener("epoch", (e: MessageEvent) => {
    // This handler must not read lastEventId: the epoch carries no id, and
    // the header-carried native-retry case is answered by classification
    // advancing appliedHigh instead.
    clearEpochTimer();
    if (epochSeen) {
      return; // defensive: the server writes exactly one epoch per connection
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
      // The ANCHOR died mid-transaction.
      abortTransaction(txn);
      return;
    }
    if (es.readyState === EventSource.CLOSED) {
      // The browser gave up — refusals land here immediately (fail fast:
      // the 401 redirect and the degraded boot run at once, never waiting
      // the epoch deadline).
      if (!epochSeen) {
        degradeBoot();
        downPeriodLatch = true; // an epoch-less down period latches ONE precautionary refetch
      }
      verdictBuffer = []; // a condemned connection's frames never apply
      clearEpochTimer();
      eventSource = null;
      scheduleReconnect();
    } else {
      // The browser retries this connection itself, carrying the real
      // Last-Event-ID header; its epoch classifies the replay. Reset
      // per-connection state so pre-epoch frames buffer again.
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

/** An undecodable epoch — or the deadline expiring on a silent open stream,
 *  which is handled identically: the connection cannot be trusted. Condemn
 *  it (buffered frames never apply), degrade the boot gate, note the
 *  epoch-less down period, and rejoin the ladder. */
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
  // Entering (or already in) the reconnect ladder: the stream is DOWN, so
  // status rides the 5s poll until an open. Every down period funnels
  // through here — refused connects, post-CLOSED backoff, latched teardowns.
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
    // The anchor is being deliberately killed (hidden tab, page unload):
    // abort first — the reconnect it schedules is cancelled just below, and
    // the latch makes the next connect's epoch transact unconditionally.
    abortTransaction(txn);
  }
  teardownConnection();
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  // reconnectAttempt deliberately NOT reset here: it resets only on a
  // non-latched open and on COMMIT.
  //
  // A deliberate teardown is not DOWN: no ladder is left running (cancelled
  // just above), the tab is hidden or unloading, and a hidden tab issues
  // zero status polls. Reconnect-on-visible re-decides the state.
  setStatusDegraded(false);
  abortPoll();
}

// --- Frame routing, the replay table, and counter advancement ---

function routeFrame(type: BufferedFrame["type"], payload: EventData, e: MessageEvent): void {
  const id = e.lastEventId ? Number(e.lastEventId) : null;
  if (!epochSeen) {
    // Pre-epoch frames can only be replay (webhttp writes replay before the
    // handshake); they buffer until the verdict. The cap is a heuristic
    // belt: overflow degrades safely into a latched recovery.
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
    // HOLD: a post-epoch frame beyond the snapshot the legs are reading.
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

/** THE REPLAY TABLE, exhaustive over the union:
 *  - coverage re-applies through the A6 coalescer (idempotent — the heal
 *    fetches current truth) and notes E4's history trigger;
 *  - notify dedupes by (boot_id, frame_id);
 *  - scan:start re-applies; its toast half dedupes like notify's;
 *  - scan:done re-applies as a no-op: state rides the terminal activity
 *    upsert (status store + E4's history trigger) and the per-root coverage
 *    events; its toast half lives in the status poll's transition detector,
 *    deduped there by activity id;
 *  - activity / alert / provider re-apply through the status store's keyed
 *    idempotent appliers (a re-applied delta mutates nothing); a TERMINAL
 *    activity upsert notes E4's history trigger too;
 *  - sync:done re-applies, idempotent per job_id (the settlement registry
 *    keeps each job's terminal, so a replayed frame settles nothing twice);
 *  - epoch is never replayed (no id, handled before this table).
 *  Counter advancement: appliedHigh advances by max over applied ids. */
function applyFrame(f: BufferedFrame, advanceCounters: boolean): void {
  switch (f.type) {
    case "coverage":
      // E4's history trigger observes here, OUTSIDE the A6 gate: a poller
      // import on a fresh /history tab reloads history even though no
      // collection is loaded and the heal enqueues nothing.
      noteHistoryMutation();
      healFromCoverageEvent(f.payload as CoverageEvent);
      break;
    case "notify":
      if (dedupeToast(f)) {
        showNotifyToast(f.payload as NotifyEvent);
      }
      break;
    case "scan:start": {
      // An ephemeral "scan started" nudge — useful for scheduled scans the
      // user did not initiate; the status button flips from the poll.
      if (dedupeToast(f)) {
        const p = f.payload as ScanEvent;
        notify.info(`Scan started: ${p.detail || p.action || "Scan"}`);
      }
      break;
    }
    case "scan:done":
      // Nothing left to apply: the terminal activity upsert owns the status
      // flip and E4's history trigger, per-root coverage events own the row
      // heals, and the toast half lives in the status transition detector.
      break;
    case "activity": {
      const p = f.payload as ActivityEvent;
      applyActivityEvent(p);
      if (p.op !== "remove" && p.entry?.done) {
        // A terminal activity (download, scan) may have written history rows.
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

/** The BOOT-CHANGE APPLICATION arm: from an old boot, ONLY the
 *  non-reconstructible payloads apply — notify toasts and sync:done dialog
 *  settlement (a restart drops the server's job registry, so a held
 *  settlement is this job's only delivery), deduped in the OLD namespace.
 *  State-bearing frames (coverage, the scan:* state halves and scan:done's
 *  toast, and the activity/alert/provider status deltas) are SKIPPED: the
 *  new transaction's legs and status fetch are strictly newer authority. No
 *  counter advancement — old-boot ids never touch the new boot's counters. */
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
  // max over the counters treats an unknown as absent — the known one wins.
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

  // DETERMINISTIC TRIGGER ORDER: reset counters (trigger 2) → apply the
  // abort-deferred holdQueue (the abort slots where its trigger fires; with
  // the boot arm advancing nothing, reset-then-abort and abort-then-reset
  // agree) → classify the verdictBuffer → legs → commit → drain. On a clean
  // verdict, classification runs BEFORE trigger evaluation, so a
  // header-carried native retry advances appliedHigh to head and trigger 3
  // stays quiet without this module ever reading lastEventId.
  if (bootChanged) {
    watermark = null;
    appliedHigh = null;
  }

  if (holdQueue.length > 0 && !txn) {
    // Frames held by an aborted transaction: same-boot frames apply in full
    // (the forced reconnect carries no cursor, so these are the only copy);
    // old-boot frames apply through the boot-change arm BEFORE any new-boot
    // frame is classified.
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
    // The dedupe namespace resets with the boot — after the old namespace's
    // payloads applied. Sync correlation is per boot too: the dialog's
    // job_id match clears WITH the namespace (the held settlement landed
    // first, above), and pending watchers re-attach via the jobs read. The
    // download tracking is the same rule: its activity ids are per process,
    // and the sweep is armed HERE so this transaction's own status read is
    // the snapshot that resolves them.
    appliedToastKeys.clear();
    clearSyncCorrelation();
    armDownloadRestartSweep();
  }
  bootID = epoch.boot_id; // stores on epoch arrival

  // Classify the verdictBuffer (replayed frames of THIS connection, stamped
  // with its boot): ≤ head applies and advances appliedHigh; > head is
  // DEFENSIVE — unreachable by webhttp's ordering, since replay precedes the
  // epoch and b.Head covers every replayed id.
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

  // TRIGGERS (they and the latches coalesce into ONE transaction):
  //   (1) epoch gap; (2) boot_id change (counters reset above); (3) a
  //   cursor-less non-boot connect whose head exceeds max(watermark,
  //   appliedHigh) or finds both unknown; plus the latches, and boot itself
  //   (the epoch-gated page load IS the boot transaction). head == watermark
  //   triggers nothing; post-classification head == appliedHigh triggers
  //   nothing either (the native-retry-with-replay case).
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

  // Create the transaction SYNCHRONOUSLY before resolving the boot gate, so
  // the boot route loader finds the collection leg's join registered and
  // joins it instead of double-fetching.
  const t: Transaction = { head: epoch.head, revoked: false };
  txn = t;
  holdQueue.push(...defensiveHold);
  beginCoverageTransaction();
  const join = createCollectionLegJoin();
  settleBootGate();
  // One microtask: the gate-released applyRoute sets its route state
  // synchronously (currentPage for the library/history routes) before the
  // legs read it.
  await Promise.resolve();

  const recovery = !isBoot || degradedBoot;
  try {
    // (3) Run the legs after this connection's subscribe. LEGS APPLY ON
    // LANDING; commit is the point all legs have landed, never a visibility
    // gate. E2's status fetch is the third leg (it never rejects — offline
    // handling is its own).
    await Promise.all([
      collectionLeg(recovery, t, join),
      dispatchTransactionPageLeg(recovery),
      pollStatus(),
    ]);
    if (t.revoked) {
      return; // aborted while the legs were landing (anchor died)
    }
    // (4) COMMIT: watermark := epoch.head; drain the holdQueue ascending;
    // clear every pending latch (force + down-period coalesce into this
    // one); the attempt counter resets on COMMIT.
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
      // The collection leg landed every registered row fresh: nothing is
      // left for the dirty set to retry.
      subsumeDirtyRoots();
    }
  } catch {
    // (5) ABORT: a leg genuinely failed or was refused (the wave layer's
    // typed 429 counts). Results already applied stay applied.
    abortTransaction(t);
  } finally {
    if (txn === t) {
      txn = null;
    }
    settleCoverageTransaction();
  }
}

/** ABORT (E3 step 5): revoke unlanded legs (their landings become no-ops),
 *  keep the holdQueue for the next epoch's deferred application, set the
 *  forceTransaction latch, tear down via close() (no native retry can exist
 *  on a latched client) and rejoin the ladder — the recreate presents no
 *  cursor and the first valid epoch transacts unconditionally. */
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
  settleCoverageTransaction();
}

// --- The collection leg ---

interface CollectionLegJoinHandle {
  resolve: (r: "landed" | "failed" | "uncovered") => void;
  /** Whether the leg turned out non-empty (it covers the pair). */
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

/** The TRANSACTION-OWNED, FAILURE-PRESERVING collection leg:
 *  registeredCollections ∪ the current route's needs (cold `/` = the
 *  indivisible pair; a /history or deep-link session's leg is EMPTY and
 *  settles applied immediately). Fetches the pair on the RAW generated
 *  client (zero automatic retries), applies setAll on landing through the
 *  shared applyCoveragePair application site, settles applied | superseded,
 *  and REJECTS on genuine transport failure — never the loader's
 *  null-collapsing read. Navigation never aborts it. */
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
      // The license is revoked: an orphaned stale pair landing after the
      // successor's fresher one must not revert healed rows.
      join.resolve("failed");
      return;
    }
    if (!series.ok || !movies.ok) {
      join.resolve("failed");
      const status = !series.ok ? series.status : movies.status;
      throw new Error(series.error ?? movies.error ?? `collection leg failed (${String(status)})`);
    }
    // Supersede the in-flight plain pair fetch (a degraded boot's ungated
    // load) before the fresher snapshot lands, then apply through the one
    // shared application site (reset rule + tombstone drop + gate/register).
    abortInFlightPairFetch();
    applyCoveragePair(series.data ?? [], movies.data ?? []);
    join.resolve("landed");
  } finally {
    endWrite();
    setCollectionLegJoin(null);
  }
}

// --- Visibility pause ---

// Pause SSE when the tab is hidden to save server resources and battery.
// Debounce reconnect on visible to avoid flapping on quick tab switches.
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

/** Test-only: tear the connection down and reset EVERY piece of module
 *  state, the boot gate included. */
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

/** Test-only: the counters and latches, for trigger truth-table pins. */
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
