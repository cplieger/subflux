// coverage-heal.ts — A6: coverage rows heal from SSE events through one
// per-root trailing coalescer backed by the per-item summary endpoints.
//
// Full-pair collection reads stay legal in exactly two places — transactions
// (task 9) and the library route loader — both routed through coverage.ts's
// applyCoveragePair, which calls resetCoverageHeal before overwriting rows.
// Rows are written through coverage-store.ts, the leaf shared with
// coverage.ts; this module never imports the other orchestrator.

import * as store from "./store.js";
import { coverageMovieSummaryRaw, coverageSeriesSummaryRaw } from "./wire/client.gen.js";
import { applyHealedRow, libraryLoaded, removeCoverageRow } from "./coverage-store.js";
import { registerReconcileTask } from "./status.js";
import { DIRTY_ROOT_CAP, SUMMARY_COALESCE_MS } from "./constants.js";
import type { CoverageEvent } from "./wire/types.gen.js";
import type { CoverageItem } from "./api-types.js";

/** One heal target: the series/movie ROOT an event's media id maps onto. */
export interface CoverageRoot {
  kind: "series" | "movie";
  numericID: number;
  /** The collection key (`tvdb-{n}` / `tmdb-{n}`) — coverageMediaId's format. */
  rootKey: string;
}

// Positive by construction: a numeric id must start [1-9], so zero and
// leading-zero ids never match, and imdb-fallback / malformed ids match
// neither pattern. The optional -s##e## arm folds an episode id onto its
// series root.
const SERIES_MEDIA_ID = /^tvdb-([1-9]\d*)(?:-s\d+e\d+)?$/;
const MOVIE_MEDIA_ID = /^tmdb-([1-9]\d*)$/;

/** THE parser (A6): map an event's media identity onto a heal root, or null
 *  for anything that must produce no request — malformed, zero, leading-zero,
 *  imdb-fallback, and type-mismatched ids (every publisher pairs a tvdb id
 *  with media_type "episode" and a tmdb id with "movie"). */
export function parseCoverageMediaId(mediaId: string, mediaType: string): CoverageRoot | null {
  const s = SERIES_MEDIA_ID.exec(mediaId);
  if (s?.[1] !== undefined) {
    return mediaType === "episode"
      ? { kind: "series", numericID: Number(s[1]), rootKey: `tvdb-${s[1]}` }
      : null;
  }
  const m = MOVIE_MEDIA_ID.exec(mediaId);
  if (m?.[1] !== undefined) {
    return mediaType === "movie"
      ? { kind: "movie", numericID: Number(m[1]), rootKey: `tmdb-${m[1]}` }
      : null;
  }
  return null;
}

/** Whether `root`'s own detail view is on screen (detail views live on the
 *  library page; the file manager is not a detail). */
function detailOpen(root: CoverageRoot): boolean {
  if (store.get("currentPage") !== "library") {
    return false;
  }
  const ctx = store.get("detailCtx");
  if (!ctx || ("files" in ctx && ctx.files)) {
    return false;
  }
  if (root.kind === "series") {
    return "tvdbId" in ctx && ctx.tvdbId === root.numericID;
  }
  return "movie" in ctx && ctx.movie && ctx.tmdbId === root.numericID;
}

interface PendingHeal {
  root: CoverageRoot;
  /** 0 = event-fresh; 1 = the single automatic re-enqueue after a failure. */
  attempt: 0 | 1;
}

// rootKey → queued heal for the OPEN window. Trailing coalescer: the first
// enqueue arms the timer, everything arriving inside the window joins that
// flush, so a burst costs one GET per root per window.
const pending = new Map<string, PendingHeal>();
let windowTimer: ReturnType<typeof setTimeout> | null = null;

// rootKey → in-flight summary GET. At most one per root: dispatching a
// newer one aborts the older, so the latest always wins.
const inflight = new Map<string, AbortController>();

// Roots whose heal failed twice, retried at each reconcile tick. Insertion
// order is the drop-oldest order for the cap. A library entry persists until
// convergence or a committing transaction subsumes it; a detail entry lives
// in the mounted detail view's scope.
interface DirtyRoot {
  root: CoverageRoot;
  /** True while the ONLY renderer is the root's own detail view, so the entry
   *  dies when that view is left (`dropDetailScopedDirtyRoots`). A later
   *  library-scoped re-add clears the flag, which is what keeps a persisting
   *  library entry out of that release. */
  detailScoped: boolean;
}
const dirty = new Map<string, DirtyRoot>();
let tickRegistered = false;

/** Coverage SSE entry point (events.ts): parse, gate, enqueue. A root whose
 *  id fails the parser, or that nothing on screen renders, costs no request. */
export function healFromCoverageEvent(payload: CoverageEvent): void {
  const root = parseCoverageMediaId(payload.media_id, payload.media_type);
  if (!root) {
    return;
  }
  if (!libraryLoaded() && !detailOpen(root)) {
    return; // gate closed: a deep-link insert or a foreign page renders nothing
  }
  enqueue(root, 0);
}

function enqueue(root: CoverageRoot, attempt: 0 | 1): void {
  const cur = pending.get(root.rootKey);
  if (cur === undefined) {
    pending.set(root.rootKey, { root, attempt });
  } else if (attempt < cur.attempt) {
    cur.attempt = attempt; // a fresh event grants a fresh retry budget
  }
  windowTimer ??= setTimeout(flushWindow, SUMMARY_COALESCE_MS);
}

function flushWindow(): void {
  windowTimer = null;
  void flush();
}

interface HealOutcome {
  heal: PendingHeal;
  /** Aborted by a newer dispatch or the reset rule — a fresher writer owns
   *  this root, so the outcome must not apply or escalate. */
  superseded: boolean;
  status: number;
  /** The healed row (summary payload + client discriminant) on a 2xx. */
  row: CoverageItem | null;
}

async function flush(): Promise<void> {
  const heals = [...pending.values()];
  pending.clear();
  if (heals.length === 0) {
    return;
  }
  const outcomes = await Promise.all(heals.map(fetchRoot));
  // One batch() per flush: every row write of this window lands in a single
  // notification pass, so the filtered/sorted view derives once.
  store.batch(() => {
    for (const o of outcomes) {
      applyOutcome(o);
    }
  });
  runDetailCouplings(outcomes);
}

async function fetchRoot(heal: PendingHeal): Promise<HealOutcome> {
  const { rootKey, kind, numericID } = heal.root;
  inflight.get(rootKey)?.abort();
  const ctrl = new AbortController();
  inflight.set(rootKey, ctrl);
  let status: number;
  let row: CoverageItem | null = null;
  if (kind === "series") {
    const res = await coverageSeriesSummaryRaw(numericID, undefined, { signal: ctrl.signal });
    status = res.status;
    if (res.ok && res.data !== undefined) {
      row = { ...res.data, _type: "series" };
    }
  } else {
    const res = await coverageMovieSummaryRaw(numericID, undefined, { signal: ctrl.signal });
    status = res.status;
    if (res.ok && res.data !== undefined) {
      row = { ...res.data, _type: "movie" };
    }
  }
  if (inflight.get(rootKey) === ctrl) {
    inflight.delete(rootKey);
  }
  return { heal, superseded: ctrl.signal.aborted, status, row };
}

function applyOutcome(o: HealOutcome): void {
  if (o.superseded) {
    return;
  }
  const { root } = o.heal;
  if (o.row) {
    applyHealedRow(o.row);
    dirty.delete(root.rootKey);
    return;
  }
  if (o.status === 404) {
    // The summary 404s exactly where the collection omits: the row is gone.
    removeCoverageRow(root.rootKey);
    dirty.delete(root.rootKey);
    return;
  }
  if (o.heal.attempt === 0) {
    enqueue(root, 1);
    return;
  }
  markDirty(root);
}

type DetailRefresher = (root: CoverageRoot) => void;

let refreshDetail: DetailRefresher = () => {
  /* no view layer loaded (the login bundle) */
};

/** Composition-time wiring: page-leg.ts, which sits above this module,
 *  declares how a root's own open detail is refreshed — registration keeps
 *  the dependency one-way. Not cleared by _resetHealForTest (wiring, not
 *  per-test state). */
export function setDetailRefresher(fn: DetailRefresher): void {
  refreshDetail = fn;
}

/** Detail coupling, once per window: a flushed root whose own detail is open
 *  refreshes that detail's reads (independent of the summary outcome — the
 *  detail's data rides different endpoints). */
function runDetailCouplings(outcomes: HealOutcome[]): void {
  for (const { heal } of outcomes) {
    if (detailOpen(heal.root)) {
      refreshDetail(heal.root);
    }
  }
}

function markDirty(root: CoverageRoot): void {
  dirty.delete(root.rootKey); // re-adding moves it to the newest slot
  dirty.set(root.rootKey, {
    root,
    detailScoped: !libraryLoaded(),
  });
  if (dirty.size > DIRTY_ROOT_CAP) {
    // Drop-oldest: a dropped root converges via the next event, replay, or
    // transaction.
    const oldest = dirty.keys().next().value;
    if (oldest !== undefined) {
      dirty.delete(oldest);
    }
  }
  if (!tickRegistered) {
    tickRegistered = true;
    registerReconcileTask(retryDirtyRoots);
  }
}

// Each reconcile tick re-enqueues every dirty root at attempt 1: one GET per
// root per tick, success converges (applyOutcome removes it), failure returns
// it to the dirty set. The tick itself pauses when the tab is hidden.
function retryDirtyRoots(): void {
  for (const d of dirty.values()) {
    enqueue(d.root, 1);
  }
}

/** Release dirty entries whose only renderer was a detail view, called by the
 *  router's leave path — once that view is gone a retry would repaint
 *  nothing. Library-scoped entries are untouched. */
export function dropDetailScopedDirtyRoots(): void {
  for (const [rootKey, d] of dirty) {
    if (d.detailScoped) {
      dirty.delete(rootKey);
    }
  }
}

/** Task 9 seam: a committing transaction subsumes the dirty set — its
 *  collection leg just landed every row fresh. The route loader's pair
 *  landing deliberately does not: dirty library roots persist until
 *  convergence or a commit. */
export function subsumeDirtyRoots(): void {
  dirty.clear();
}

const healResetCallbacks = new Set<() => void>();

/** Task 12 seam: the history pending-reload latch re-arms when a reset aborts
 *  in-flight heals. */
export function onHealReset(fn: () => void): () => void {
  healResetCallbacks.add(fn);
  return (): void => {
    healResetCallbacks.delete(fn);
  };
}

/** The reset rule (A6): abort in-flight per-root GETs and clear the pending
 *  window for rows about to be overwritten. Called by coverage.ts's
 *  applyCoveragePair. */
export function resetCoverageHeal(): void {
  for (const ctrl of inflight.values()) {
    ctrl.abort();
  }
  inflight.clear();
  pending.clear();
  if (windowTimer !== null) {
    clearTimeout(windowTimer);
    windowTimer = null;
  }
  for (const fn of healResetCallbacks) {
    fn();
  }
}

/** Test-only: abort/clear all coalescer state, callbacks included. */
export function _resetHealForTest(): void {
  healResetCallbacks.clear();
  resetCoverageHeal();
  dirty.clear();
}
