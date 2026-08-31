// page-leg.ts — B2: refreshCurrentPage is the page-leg dispatcher.
//
// ONE place enumerates what refreshing the current route means (E3 step 3):
// library reloads the pair, series detail runs the triple (episodes, episode
// coverage, history ids), movie detail runs its own triple (summary + /subs
// + movie stateIDs — the plain dispatcher delegates the latter two to
// openMovieDetail, the transaction awaits all three), history reloads, and
// files is an empty leg (the files page owns its refresh). Task 9's
// transactions dispatch their page leg through refreshCurrentPage, so this
// per-route enumeration is the shared seam.
//
// Every dispatch runs under an AbortController + generation token PER ROUTE:
// a newer dispatch supersedes the older one (abort + stale-generation
// discard), and the router's leave path aborts the departing route's
// controller. A superseded run settles "superseded" in silence — its own
// abort is never an error, because the superseding caller owns the fresh
// render.

import * as store from "./store.js";
import { on, BusEvent } from "./bus.js";
import {
  coverageMovieSubsRaw,
  coverageMovieSummary,
  coverageMovieSummaryRaw,
  coverageSeriesDetail,
  coverageSeriesDetailRaw,
  mediaEpisodes,
  mediaEpisodesRaw,
  stateIDs,
  stateIDsRaw,
} from "./wire/client.gen.js";
import type { QueryValue } from "./wire/client.gen.js";
import type { ApiResult } from "./api-client.js";
import type { MovieDetail } from "./api-types.js";
import { loadCoverage } from "./coverage.js";
import {
  disposeDetailBindings,
  openMovieDetail,
  renderMovieDetailFromLeg,
  renderSeriesDetail,
} from "./detail.js";
import { reloadHistory, reloadHistoryForTransaction } from "./history.js";
import { setDetailRefresher } from "./coverage-heal.js";
import { coverageRow } from "./coverage-store.js";

/** How a page-leg run settled: it applied its results, or a newer dispatch /
 *  a route leave superseded it and the results were discarded. */
export type PageLegResult = "applied" | "superseded";

// Route key → newest generation. A landing run applies only while its
// generation is still the newest for its route.
const generations = new Map<string, number>();
// Route key → in-flight controller (at most one live run per route).
const controllers = new Map<string, AbortController>();

// The dispatcher's route identity, in the order refreshCurrentPage always
// branched: history page, files view, series detail, movie detail, library.
// Exported for task 9's transaction (the collection leg's routeRequired
// check reads it).
export function currentRouteKey(): string {
  if (store.get("currentPage") === "history") {
    return "history";
  }
  const ctx = store.get("detailCtx");
  if (ctx && "files" in ctx && ctx.files) {
    return "files";
  }
  if (ctx && "tvdbId" in ctx && ctx.tvdbId) {
    return `series:${ctx.tvdbId}`;
  }
  if (ctx && "movie" in ctx && ctx.movie) {
    return `movie:${ctx.tmdbId}`;
  }
  // No landed detail context: the URL classifies, not the transient
  // currentPage value. A detail path IS a detail route at boot (R2.3/A7) —
  // the router sets currentPage="library" synchronously while detailCtx
  // lands only with the summary, so store state alone misreads a deep-link
  // boot as the library and the collection leg fetches the pair.
  return routeKeyFromPath(location.pathname);
}

// URL → route identity for the window before any detail context lands,
// mirroring the router's own table (same key space as the ctx-derived arms,
// so a landing context is a no-op key change). Unknown paths are the
// library — applyRoute's default arm.
function routeKeyFromPath(path: string): string {
  if (path === "/history") {
    return "history";
  }
  if (/^\/(?:series|movie)\/\d+\/files$/.test(path)) {
    return "files";
  }
  const m = /^\/(series|movie)\/(\d+)(?:\/sync|\/search\/[a-z]{2,3})?$/.exec(path);
  return m ? `${m[1] ?? ""}:${m[2] ?? ""}` : "library";
}

// A run may apply only while un-aborted, newest for its route, and its route
// is still on screen (a row click swaps views without passing the router).
function isCurrent(key: string, gen: number, ctrl: AbortController): boolean {
  return !ctrl.signal.aborted && generations.get(key) === gen && currentRouteKey() === key;
}

/** The ROUTER's leave path: abort the departing route's in-flight page leg,
 *  so the detail refresh pair dies on leave, and release the detail bindings
 *  beside it (C2) — the departing view's row effects must not outlive the
 *  view. One owner: every route leave funnels through here. */
export function abortPageLeg(): void {
  for (const [key, ctrl] of controllers) {
    generations.set(key, (generations.get(key) ?? 0) + 1);
    ctrl.abort();
  }
  controllers.clear();
  disposeDetailBindings();
}

/** THE page-leg dispatcher. Runs the current route's page leg under the
 *  route's controller and a fresh generation, superseding any prior run for
 *  the route, and hands the results to the route's renderer. */
export async function refreshCurrentPage(): Promise<PageLegResult> {
  const key = currentRouteKey();
  const gen = (generations.get(key) ?? 0) + 1;
  generations.set(key, gen);
  controllers.get(key)?.abort();
  const ctrl = new AbortController();
  controllers.set(key, ctrl);
  try {
    return await dispatchLeg(key, gen, ctrl);
  } finally {
    if (controllers.get(key) === ctrl) {
      controllers.delete(key);
    }
  }
}

async function dispatchLeg(
  key: string,
  gen: number,
  ctrl: AbortController,
): Promise<PageLegResult> {
  if (key === "history") {
    // History is its own surface: a completed download invalidates it just
    // like the library, but the coverage arms below would never reload it.
    reloadHistory();
    return "applied";
  }
  if (key === "files") {
    // The files page owns its refresh after delete; an empty leg applies
    // immediately.
    return "applied";
  }
  const { signal } = ctrl;
  const ctx = store.get("detailCtx");
  if (ctx && "tvdbId" in ctx && ctx.tvdbId) {
    const [seasons, subFiles, historyIDs] = await Promise.all([
      mediaEpisodes(ctx.series.id, undefined, { signal }),
      coverageSeriesDetail(ctx.tvdbId, { signal }),
      stateIDs({ type: "episode", prefix: `tvdb-${ctx.tvdbId}-` }, { signal }),
    ]);
    if (!isCurrent(key, gen, ctrl)) {
      return "superseded";
    }
    // A failed episodes read keeps the cached seasons rather than blanking
    // the table; the coverage/history reads keep their empty fallbacks.
    renderSeriesDetail(
      ctx.series,
      seasons ?? ctx.seasons,
      subFiles ?? [],
      new Set(historyIDs ?? []),
    );
    return "applied";
  }
  if (ctx && "movie" in ctx && ctx.movie) {
    // The leg is summary + /subs + movie stateIDs — never a collection read.
    // openMovieDetail runs the latter two under this same leg signal.
    const row = await coverageMovieSummary(ctx.tmdbId, undefined, { signal });
    if (!isCurrent(key, gen, ctrl)) {
      return "superseded";
    }
    if (row) {
      openMovieDetail(row, true, signal);
    }
    return "applied";
  }
  if (key === "library") {
    await loadCoverage(true);
    return "applied";
  }
  // A pending detail (the URL names a detail route whose context has not
  // landed): the route loader owns the fetch — an empty leg.
  return "applied";
}

// Any module can trigger a refresh by emitting BusEvent.DataInvalidate — a
// DIRECT dispatch, no coalesce window (config-save is the surviving
// steady-state emitter and it is not bursty).
on(BusEvent.DataInvalidate, () => {
  void refreshCurrentPage();
});

/** A6's series detail-coupling (R1.2): a healed series root whose own detail
 *  is open refreshes the REFRESH PAIR — episode coverage + history ids —
 *  rendering with the cached seasons. Never mediaEpisodes: the event path is
 *  specified to cost no arr-backed read; the transaction/page-leg triple
 *  keeps it. Runs under the route's controller and a fresh generation like
 *  any dispatch, superseding an older run. */
async function refreshSeriesDetailPair(): Promise<void> {
  const ctx = store.get("detailCtx");
  if (!ctx || !("tvdbId" in ctx) || !ctx.tvdbId) {
    return;
  }
  const key = currentRouteKey();
  const gen = (generations.get(key) ?? 0) + 1;
  generations.set(key, gen);
  controllers.get(key)?.abort();
  const ctrl = new AbortController();
  controllers.set(key, ctrl);
  try {
    const { signal } = ctrl;
    const [subFiles, historyIDs] = await Promise.all([
      coverageSeriesDetail(ctx.tvdbId, { signal }),
      stateIDs({ type: "episode", prefix: `tvdb-${ctx.tvdbId}-` }, { signal }),
    ]);
    if (!isCurrent(key, gen, ctrl)) {
      return;
    }
    renderSeriesDetail(ctx.series, ctx.seasons, subFiles ?? [], new Set(historyIDs ?? []));
  } finally {
    if (controllers.get(key) === ctrl) {
      controllers.delete(key);
    }
  }
}

// The heal's detail coupling, wired in the direction the layers already run:
// coverage-heal.ts sits BELOW this module (it writes rows through
// coverage-store.ts while this module refreshes routes), so it cannot import
// the refresh and this module registers it instead. A6's two arms differ in
// kind, so both live here rather than in the heal: the series arm is a route
// refresh under this module's own generation guard, and the movie arm re-runs
// openMovieDetail's on-demand reads (/subs + one state/ids read) from the row
// the heal just landed, under detail.ts's own controller.
setDetailRefresher((root) => {
  if (root.kind === "series") {
    void refreshSeriesDetailPair();
    return;
  }
  const row = coverageRow(root.rootKey);
  if (row) {
    // A movie root's row IS the summary payload plus the client discriminant,
    // so the narrowing is the same one detail.ts's navigation entry makes.
    openMovieDetail(row as MovieDetail, true);
  }
});

// --- Task 9: the transaction's PAGE leg ---

/** How the transaction's page leg landed: this dispatch applied, or a newer
 *  same-route dispatch superseded it (whose own landing satisfies the leg). */
export type TransactionLegOutcome = "applied" | "superseded";

/** Dispatch the transaction's PAGE leg (E3 step 3). Differences from the
 *  plain dispatcher: the library arm is EMPTY (the transaction's collection
 *  leg owns the pair — it settles applied immediately on dispatch), history
 *  runs task 9's settlement-aware extraction, recovery transactions send
 *  ?recovery=1 on the honoring endpoints, every fetch runs on the RAW
 *  generated client with zero automatic retries, and a genuine transport
 *  failure or typed 429 refusal REJECTS (the transaction aborts). A
 *  route-leave mid-leg RE-ROUTES: the loop's next dispatch IS the new
 *  route's page leg (an empty leg applies immediately, so it terminates),
 *  and commit waits for the re-routed leg. */
export async function dispatchTransactionPageLeg(
  recovery: boolean,
): Promise<TransactionLegOutcome> {
  for (;;) {
    const r = await runTransactionLeg(recovery);
    if (r !== "rerouted") {
      return r;
    }
  }
}

async function runTransactionLeg(recovery: boolean): Promise<TransactionLegOutcome | "rerouted"> {
  const key = currentRouteKey();
  const gen = (generations.get(key) ?? 0) + 1;
  generations.set(key, gen);
  controllers.get(key)?.abort();
  const ctrl = new AbortController();
  controllers.set(key, ctrl);
  try {
    return await transactionArm(key, gen, ctrl, recovery);
  } finally {
    if (controllers.get(key) === ctrl) {
      controllers.delete(key);
    }
  }
}

/** A superseded transaction leg run: outrun by a newer same-route dispatch
 *  (its landing satisfies the leg) or re-routed by a route leave (the
 *  dispatcher's next dispatch owns the continuation). */
function outrunOrRerouted(key: string): "superseded" | "rerouted" {
  return currentRouteKey() === key ? "superseded" : "rerouted";
}

/** The first genuine leg failure among the results, or null. An abort is
 *  never a failure (the isCurrent check precedes this), and a 404 is a
 *  definitive answer (a vanished item), not a transport failure — the arm
 *  applies its fallback. A typed 429 refusal counts as a genuine failure. */
function legFailure(...results: ApiResult<unknown>[]): Error | null {
  for (const r of results) {
    if (!r.ok && r.status !== 404) {
      return new Error(r.error ?? `page leg failed (${String(r.status)})`);
    }
  }
  return null;
}

async function transactionArm(
  key: string,
  gen: number,
  ctrl: AbortController,
  recovery: boolean,
): Promise<TransactionLegOutcome | "rerouted"> {
  if (key === "library" || key === "files") {
    // Library: nothing extra — the collection leg owns the pair. Files owns
    // its own refresh. An EMPTY leg settles applied immediately on dispatch,
    // which is what makes the re-route chain terminate.
    return "applied";
  }
  const { signal } = ctrl;
  const rq: Record<string, QueryValue> | undefined = recovery ? { recovery: 1 } : undefined;
  if (key === "history") {
    const r = await reloadHistoryForTransaction(signal);
    return r === "rerouted" ? "rerouted" : r;
  }
  const ctx = store.get("detailCtx");
  if (ctx && "tvdbId" in ctx && ctx.tvdbId) {
    const [seasons, subFiles, historyIDs] = await Promise.all([
      mediaEpisodesRaw(ctx.series.id, rq, { signal }),
      coverageSeriesDetailRaw(ctx.tvdbId, { signal }),
      stateIDsRaw({ type: "episode", prefix: `tvdb-${ctx.tvdbId}-` }, { signal }),
    ]);
    if (!isCurrent(key, gen, ctrl)) {
      return outrunOrRerouted(key);
    }
    const failure = legFailure(seasons, subFiles, historyIDs);
    if (failure) {
      throw failure;
    }
    renderSeriesDetail(
      ctx.series,
      seasons.data ?? ctx.seasons,
      subFiles.data ?? [],
      new Set(historyIDs.data ?? []),
    );
    return "applied";
  }
  if (ctx && "movie" in ctx && ctx.movie) {
    // E3 step 3: the movie leg is the summary + /subs + movie stateIDs
    // TRIPLE, all awaited on the raw client — commit waits for all three,
    // and a genuinely failed read aborts the transaction instead of
    // painting an empty subs table. /subs and stateIDs are store-only reads
    // and never carry ?recovery=1; a 404 is a definitive answer (vanished
    // item), rendered as the shipped fallback.
    const [row, subs, historyIDs] = await Promise.all([
      coverageMovieSummaryRaw(ctx.tmdbId, rq, { signal }),
      coverageMovieSubsRaw(ctx.tmdbId, { signal }),
      stateIDsRaw({ type: "movie", prefix: `tmdb-${ctx.tmdbId}` }, { signal }),
    ]);
    if (!isCurrent(key, gen, ctrl)) {
      return outrunOrRerouted(key);
    }
    const failure = legFailure(row, subs, historyIDs);
    if (failure) {
      throw failure;
    }
    if (row.ok && row.data !== undefined) {
      renderMovieDetailFromLeg(row.data, subs.data ?? [], historyIDs.data ?? []);
    }
    return "applied";
  }
  if (currentRouteKey() === key) {
    // A PENDING detail: the URL still names this route but its context has
    // not landed (a deep-link boot resolving its item). The ROUTE LOADER
    // owns the item-grain resolution (R2.3), so the leg is EMPTY and
    // settles applied immediately — which keeps the re-route chain
    // terminating.
    return "applied";
  }
  // Route state moved between the key computation and the arm (a detail
  // closing mid-dispatch): re-dispatch reads the fresh state.
  return "rerouted";
}
