// page-leg.test.ts — B2: the page-leg dispatcher. Driven against the REAL
// store and bus; the wire client, the route renderers (detail, coverage,
// history) and the coverage row store are replaced so the assertions are about
// dispatch, supersession, and abort — never about rendering, which each
// renderer's own suite pins. The heal double captures the detail refresher this
// module registers with it, which is how the coupling is driven here.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// Plain factories over hoisted mutable records (mockReset strips vi.fn
// implementations between tests; see coverage.test.ts's header note).
const wire = vi.hoisted(() => ({
  episodes: null as unknown,
  subFiles: null as unknown,
  historyIDs: null as unknown,
  movieSummary: null as unknown,
  movieSubs: null as unknown,
  calls: [] as { fn: string; args: unknown[]; signal: AbortSignal | undefined }[],
  // When true, wire reads hang until resolved by hand. The deferred promise
  // deliberately IGNORES its abort signal: resolving it late with real data
  // is exactly the stale-response race the generation guard must discard.
  defer: false,
  pending: [] as { fn: string; resolve: (v: unknown) => void }[],
  // The status a Raw read reports when its record is null (a failed read).
  failStatus: 502,
}));

function wireCall(
  fn: string,
  value: () => unknown,
  args: unknown[],
  signal: AbortSignal | undefined,
): Promise<unknown> {
  wire.calls.push({ fn, args, signal });
  if (wire.defer) {
    return new Promise((resolve) => {
      wire.pending.push({ fn, resolve });
    });
  }
  return Promise.resolve(value());
}

/** The Raw flavor over the same records: null = a non-2xx envelope (status
 *  wire.failStatus), anything else a 2xx with data. An aborted signal
 *  reports status 0, matching the transport. */
async function wireCallRaw(
  fn: string,
  value: () => unknown,
  args: unknown[],
  signal: AbortSignal | undefined,
): Promise<unknown> {
  const v = await wireCall(fn, value, args, signal);
  if (signal?.aborted) {
    return { ok: false, status: 0, error: "aborted" };
  }
  return v === null
    ? { ok: false, status: wire.failStatus, error: `${fn} failed` }
    : { ok: true, status: 200, data: v };
}

vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: (id: unknown, _q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("mediaEpisodes", () => wire.episodes, [id], opts?.signal),
  coverageSeriesDetail: (id: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("coverageSeriesDetail", () => wire.subFiles, [id], opts?.signal),
  stateIDs: (q: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("stateIDs", () => wire.historyIDs, [q], opts?.signal),
  coverageMovieSummary: (id: unknown, _q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("coverageMovieSummary", () => wire.movieSummary, [id], opts?.signal),
  // The transaction arms run on the RAW client (zero automatic retries; a
  // non-2xx surfaces on first receipt). The query arg is recorded so the
  // ?recovery=1 pins can read it.
  mediaEpisodesRaw: (id: unknown, q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCallRaw("mediaEpisodesRaw", () => wire.episodes, [id, q], opts?.signal),
  coverageSeriesDetailRaw: (id: unknown, opts?: { signal?: AbortSignal }) =>
    wireCallRaw("coverageSeriesDetailRaw", () => wire.subFiles, [id], opts?.signal),
  stateIDsRaw: (q: unknown, opts?: { signal?: AbortSignal }) =>
    wireCallRaw("stateIDsRaw", () => wire.historyIDs, [q], opts?.signal),
  coverageMovieSummaryRaw: (id: unknown, q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCallRaw("coverageMovieSummaryRaw", () => wire.movieSummary, [id, q], opts?.signal),
  coverageMovieSubsRaw: (id: unknown, opts?: { signal?: AbortSignal }) =>
    wireCallRaw("coverageMovieSubsRaw", () => wire.movieSubs, [id], opts?.signal),
  // The dispatcher must never read the collection (R1.2): present so a
  // regression that re-adds the call is recorded and fails the pin below.
  coverageMovies: (_q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("coverageMovies", () => null, [], opts?.signal),
}));

const rendered = vi.hoisted(() => ({
  series: [] as { series: unknown; seasons: unknown; subFiles: unknown; historySet: unknown }[],
  movies: [] as { m: unknown; skipPush: boolean | undefined; signal: AbortSignal | undefined }[],
  movieLegs: [] as { m: unknown; subs: unknown; historyIDs: unknown }[],
  loadCoverage: [] as (boolean | undefined)[],
  reloadHistory: 0,
  disposeCalls: 0,
  // The transaction history leg's scripted outcomes, consumed in order; an
  // Error entry rejects; a Promise entry defers until the test resolves it.
  historyLeg: [] as (string | Error | Promise<string>)[],
  historyLegCalls: [] as (AbortSignal | undefined)[],
}));
vi.mock("./detail.js", () => ({
  renderSeriesDetail: (
    series: unknown,
    seasons: unknown,
    subFiles: unknown,
    historySet: unknown,
  ) => {
    rendered.series.push({ series, seasons, subFiles, historySet });
  },
  openMovieDetail: (m: unknown, skipPush?: boolean, signal?: AbortSignal) => {
    rendered.movies.push({ m, skipPush, signal });
  },
  renderMovieDetailFromLeg: (m: unknown, subs: unknown, historyIDs: unknown) => {
    rendered.movieLegs.push({ m, subs, historyIDs });
  },
  disposeDetailBindings: () => {
    rendered.disposeCalls++;
  },
}));
vi.mock("./coverage.js", () => ({
  loadCoverage: (silent?: boolean) => {
    rendered.loadCoverage.push(silent);
    return Promise.resolve();
  },
}));
// page-leg registers its detail refresher with the heal at module load; the
// capture is a PLAIN function so mockReset cannot strip it before the tests
// that drive it (see the header note).
const heal = vi.hoisted(() => ({ refresh: null as null | ((root: unknown) => void) }));
vi.mock("./coverage-heal.js", () => ({
  setDetailRefresher: (fn: (root: unknown) => void) => {
    heal.refresh = fn;
  },
}));
// The row store the movie arm reads its freshly healed row from.
const rows = vi.hoisted(() => ({ byKey: new Map<string, unknown>() }));
vi.mock("./coverage-store.js", () => ({
  coverageRow: (key: string) => rows.byKey.get(key),
}));
vi.mock("./history.js", () => ({
  reloadHistory: () => {
    rendered.reloadHistory++;
  },
  reloadHistoryForTransaction: (signal?: AbortSignal) => {
    rendered.historyLegCalls.push(signal);
    const next = rendered.historyLeg.shift() ?? "applied";
    return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
  },
}));

import * as store from "./store.js";
import { emit, BusEvent } from "./bus.js";
import {
  abortPageLeg,
  currentRouteKey,
  dispatchTransactionPageLeg,
  refreshCurrentPage,
} from "./page-leg.js";
import type { SeriesItem, SeasonGroup, MovieItem } from "./api-types.js";

const SERIES = { id: 1042, tvdb_id: 42, title: "Show" } as unknown as SeriesItem;
const CACHED_SEASONS = [{ season: 1, episodes: [] }] as unknown as SeasonGroup[];
const MOVIE_ROW = {
  title: "Film",
  tmdb_id: 7,
  id: 2007,
  year: 2021,
  has_file: true,
  audio_lang: "en",
  rule: "en",
  targets: [],
} as unknown as MovieItem;

function onSeriesDetail(): void {
  store.set("currentPage", "library");
  store.set("detailCtx", { series: SERIES, seasons: CACHED_SEASONS, tvdbId: 42 });
}

function onMovieDetail(): void {
  store.set("currentPage", "library");
  store.set("detailCtx", { movie: true, tmdbId: 7 });
}

function onLibrary(): void {
  store.set("currentPage", "library");
  store.set("detailCtx", null);
}

/** Hand a flushed root to the refresher page-leg registered with the heal. */
function coupleDetail(kind: "series" | "movie", numericID: number): void {
  const prefix = kind === "series" ? "tvdb" : "tmdb";
  if (!heal.refresh) {
    throw new Error("page-leg registered no detail refresher");
  }
  heal.refresh({ kind, numericID, rootKey: `${prefix}-${String(numericID)}` });
}

/** Drain the dispatch chain (mock fetches settle on the microtask queue). */
async function settle(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
  await new Promise((r) => setTimeout(r, 0));
}

function calls(fn: string): { fn: string; args: unknown[]; signal: AbortSignal | undefined }[] {
  return wire.calls.filter((c) => c.fn === fn);
}

beforeEach(() => {
  abortPageLeg(); // kill anything a prior test left in flight
  wire.episodes = null;
  wire.subFiles = null;
  wire.historyIDs = null;
  wire.movieSummary = null;
  wire.movieSubs = null;
  wire.calls = [];
  wire.defer = false;
  wire.pending = [];
  wire.failStatus = 502;
  rendered.series = [];
  rendered.movies = [];
  rendered.movieLegs = [];
  rendered.loadCoverage = [];
  rendered.reloadHistory = 0;
  rendered.disposeCalls = 0;
  rendered.historyLeg = [];
  rendered.historyLegCalls = [];
  rows.byKey.clear();
  onLibrary();
});

describe("page-leg: per-route enumeration", () => {
  it("library: reloads the pair silently", async () => {
    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(rendered.loadCoverage).toStrictEqual([true]);
    expect(wire.calls).toStrictEqual([]);
  });

  it("series detail: dispatches the triple and renders the fresh results", async () => {
    onSeriesDetail();
    const seasons = [{ season: 2, episodes: [] }];
    const subFiles = [{ media_id: "tvdb-42-s02e01" }];
    wire.episodes = seasons;
    wire.subFiles = subFiles;
    wire.historyIDs = ["tvdb-42-s02e01"];

    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(calls("mediaEpisodes").map((c) => c.args)).toStrictEqual([[1042]]);
    expect(calls("coverageSeriesDetail").map((c) => c.args)).toStrictEqual([[42]]);
    expect(calls("stateIDs").map((c) => c.args)).toStrictEqual([
      [{ type: "episode", prefix: "tvdb-42-" }],
    ]);
    expect(rendered.series).toStrictEqual([
      { series: SERIES, seasons, subFiles, historySet: new Set(["tvdb-42-s02e01"]) },
    ]);
  });

  it("series detail: a failed episodes read keeps the cached seasons", async () => {
    onSeriesDetail();
    wire.episodes = null; // the generated client null-collapses failures
    wire.subFiles = [];
    wire.historyIDs = [];

    await refreshCurrentPage();

    expect(rendered.series).toHaveLength(1);
    expect(rendered.series[0]?.seasons).toBe(CACHED_SEASONS);
  });

  it("movie detail: reads the summary, never the collection, and threads the leg signal", async () => {
    onMovieDetail();
    wire.movieSummary = MOVIE_ROW;

    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(calls("coverageMovies")).toHaveLength(0);
    const summary = calls("coverageMovieSummary");
    expect(summary.map((c) => c.args)).toStrictEqual([[7]]);
    // openMovieDetail runs the /subs + movie stateIDs pair under the SAME leg
    // signal, so a route leave aborts the whole movie leg.
    expect(rendered.movies).toHaveLength(1);
    expect(rendered.movies[0]?.m).toBe(MOVIE_ROW);
    expect(rendered.movies[0]?.skipPush).toBe(true);
    expect(rendered.movies[0]?.signal).toBe(summary[0]?.signal);
  });

  it("movie detail: a failed summary read renders nothing and is not an error", async () => {
    onMovieDetail();
    wire.movieSummary = null;

    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(rendered.movies).toHaveLength(0);
    expect(rendered.loadCoverage).toHaveLength(0); // the old collection fallback is gone
  });

  it("history: runs the reload, no coverage reads", async () => {
    store.set("currentPage", "history");

    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(rendered.reloadHistory).toBe(1);
    expect(wire.calls).toStrictEqual([]);
    expect(rendered.loadCoverage).toHaveLength(0);
  });

  it("files: an empty leg applies immediately", async () => {
    store.set("currentPage", "files");
    store.set("detailCtx", { files: true });

    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(wire.calls).toStrictEqual([]);
    expect(rendered.loadCoverage).toHaveLength(0);
    expect(rendered.reloadHistory).toBe(0);
  });
});

describe("page-leg: DataInvalidate is a direct dispatch", () => {
  it("config-save still refreshes once: one emit, one reload", async () => {
    emit(BusEvent.DataInvalidate);
    await settle();

    expect(rendered.loadCoverage).toStrictEqual([true]);
  });

  it("no coalesce window: two emits dispatch twice", async () => {
    emit(BusEvent.DataInvalidate);
    emit(BusEvent.DataInvalidate);
    await settle();

    expect(rendered.loadCoverage).toStrictEqual([true, true]);
  });
});

describe("page-leg: the series detail-coupling refresh pair (R1.2)", () => {
  it("fetches the PAIR — never the arr-backed episodes read — and renders with cached seasons", async () => {
    onSeriesDetail();
    const subFiles = [{ media_id: "tvdb-42-s01e01" }];
    wire.subFiles = subFiles;
    wire.historyIDs = ["tvdb-42-s01e01"];

    coupleDetail("series", 42);
    await settle();

    expect(calls("mediaEpisodes")).toHaveLength(0);
    expect(calls("mediaEpisodesRaw")).toHaveLength(0);
    expect(calls("coverageSeriesDetail").map((c) => c.args)).toStrictEqual([[42]]);
    expect(calls("stateIDs").map((c) => c.args)).toStrictEqual([
      [{ type: "episode", prefix: "tvdb-42-" }],
    ]);
    expect(rendered.series).toStrictEqual([
      {
        series: SERIES,
        seasons: CACHED_SEASONS,
        subFiles,
        historySet: new Set(["tvdb-42-s01e01"]),
      },
    ]);
  });

  it("no-op off a series detail", async () => {
    onLibrary();

    coupleDetail("series", 42);
    await settle();

    expect(wire.calls).toStrictEqual([]);
    expect(rendered.series).toHaveLength(0);
    expect(rendered.loadCoverage).toHaveLength(0);
  });

  it("a route leave aborts the in-flight pair (same controller as any dispatch)", async () => {
    onSeriesDetail();
    wire.defer = true;

    coupleDetail("series", 42);
    await settle();
    const pending = wire.pending.splice(0);
    const signals = wire.calls.map((c) => c.signal);
    expect(pending).toHaveLength(2);

    abortPageLeg();
    expect(signals.every((s) => s?.aborted)).toBe(true);

    for (const p of pending) {
      p.resolve([]);
    }
    await settle();
    expect(rendered.series).toHaveLength(0);
  });
});

describe("page-leg: the movie detail-coupling (A6's other arm)", () => {
  it("re-opens the detail from the healed row, with no history push and no fetch", async () => {
    onMovieDetail();
    rows.byKey.set("tmdb-7", MOVIE_ROW);

    coupleDetail("movie", 7);
    await settle();

    // openMovieDetail runs the detail's own on-demand reads under its own
    // controller, so this arm issues nothing itself and pushes no history.
    expect(rendered.movies).toStrictEqual([{ m: MOVIE_ROW, skipPush: true, signal: undefined }]);
    expect(wire.calls).toStrictEqual([]);
  });

  it("a root with no row opens nothing", async () => {
    onMovieDetail();

    coupleDetail("movie", 7);
    await settle();

    expect(rendered.movies).toStrictEqual([]);
  });
});

describe("page-leg: abort + generation guards", () => {
  it("discards a stale response from an older generation (the fetch-races pin)", async () => {
    onSeriesDetail();
    wire.defer = true;

    const d1 = refreshCurrentPage();
    const p1 = wire.pending.splice(0);
    const d1signals = wire.calls.map((c) => c.signal);
    expect(p1).toHaveLength(3);

    const d2 = refreshCurrentPage();
    const p2 = wire.pending.splice(0);
    // The newer dispatch aborts the older run's per-route controller.
    expect(d1signals.every((s) => s?.aborted)).toBe(true);

    // The newer generation lands first and renders.
    const freshSeasons = [{ season: 3, episodes: [] }];
    for (const p of p2) {
      p.resolve(p.fn === "mediaEpisodes" ? freshSeasons : []);
    }
    expect(await d2).toBe("applied");
    expect(rendered.series).toHaveLength(1);
    expect(rendered.series[0]?.seasons).toBe(freshSeasons);

    // The OLDER generation's response arrives late with real (stale) data:
    // discarded in silence, never rendered, never an error.
    const staleSeasons = [{ season: 1, episodes: [] }];
    for (const p of p1) {
      p.resolve(p.fn === "mediaEpisodes" ? staleSeasons : []);
    }
    expect(await d1).toBe("superseded");
    expect(rendered.series).toHaveLength(1);
  });

  it("a degraded boot's ungated fetch is superseded by a transaction leg under one generation", async () => {
    onSeriesDetail();
    wire.defer = true;

    // The degraded boot path refetches via the bus (events.ts's emitter).
    emit(BusEvent.DataInvalidate);
    const p1 = wire.pending.splice(0);
    const bootSignals = wire.calls.map((c) => c.signal);
    expect(p1).toHaveLength(3);

    // The follow-up transaction's page leg dispatches through the same
    // dispatcher, so both runs share ONE per-route generation counter.
    const leg = refreshCurrentPage();
    const p2 = wire.pending.splice(0);
    expect(bootSignals.every((s) => s?.aborted)).toBe(true);

    const freshSeasons = [{ season: 9, episodes: [] }];
    for (const p of p2) {
      p.resolve(p.fn === "mediaEpisodes" ? freshSeasons : []);
    }
    expect(await leg).toBe("applied");

    // The boot fetch lands late: superseded, not a failure — a rejection
    // here would surface as an unhandled rejection and fail this test.
    for (const p of p1) {
      p.resolve(p.fn === "mediaEpisodes" ? CACHED_SEASONS : []);
    }
    await settle();
    expect(rendered.series).toHaveLength(1);
    expect(rendered.series[0]?.seasons).toBe(freshSeasons);
  });

  it("the leave path aborts the departing detail refresh pair", async () => {
    onSeriesDetail();
    wire.defer = true;

    const d1 = refreshCurrentPage();
    const p1 = wire.pending.splice(0);
    const signals = wire.calls.map((c) => c.signal);
    expect(p1).toHaveLength(3);
    expect(signals.every((s) => s !== undefined && !s.aborted)).toBe(true);

    abortPageLeg(); // what the router's leave path calls

    expect(signals.every((s) => s?.aborted)).toBe(true);
    // Even a transport that ignores the abort cannot paint: the landing is
    // discarded.
    for (const p of p1) {
      p.resolve([]);
    }
    expect(await d1).toBe("superseded");
    expect(rendered.series).toHaveLength(0);
  });

  it("the leave path releases the detail bindings beside the abort (C2)", () => {
    // One owner: the router's applyRoute funnels every route leave through
    // abortPageLeg, which drops the departing view's row effects. The
    // dispose behavior itself is pinned in detail.test.ts; this pins the
    // wiring.
    expect(rendered.disposeCalls).toBe(0);

    abortPageLeg();

    expect(rendered.disposeCalls).toBe(1);
  });

  it("a landing for a route no longer on screen is discarded", async () => {
    // A row click swaps views without passing the router (no leave abort):
    // the route-current check is what keeps the stale landing out.
    onSeriesDetail();
    wire.defer = true;
    const d1 = refreshCurrentPage();
    const p1 = wire.pending.splice(0);

    store.set("detailCtx", { files: true }); // files view opened mid-flight
    for (const p of p1) {
      p.resolve([]);
    }

    expect(await d1).toBe("superseded");
    expect(rendered.series).toHaveLength(0);
  });
});

// --- Task 9: the transaction dispatch ---

describe("page-leg: transaction dispatch", () => {
  it("library: the transaction page leg is EMPTY and applies immediately", async () => {
    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("applied");
    // Nothing extra: the collection leg owns the pair; no loader dispatch.
    expect(rendered.loadCoverage).toStrictEqual([]);
    expect(wire.calls).toStrictEqual([]);
  });

  it("series detail: runs the triple on the RAW client and renders on landing", async () => {
    onSeriesDetail();
    const seasons = [{ season: 2, episodes: [] }];
    wire.episodes = seasons;
    wire.subFiles = [];
    wire.historyIDs = ["tvdb-42-s02e01"];

    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("applied");
    expect(calls("mediaEpisodesRaw").map((c) => c.args)).toStrictEqual([[1042, undefined]]);
    expect(calls("coverageSeriesDetailRaw")).toHaveLength(1);
    expect(calls("stateIDsRaw")).toHaveLength(1);
    expect(rendered.series).toHaveLength(1);
  });

  it("a RECOVERY transaction sends ?recovery=1 on the honoring endpoints only", async () => {
    onSeriesDetail();
    wire.episodes = [];
    wire.subFiles = [];
    wire.historyIDs = [];

    await dispatchTransactionPageLeg(true);

    // mediaEpisodes honors ?recovery=1; the detail rows and state ids do not.
    expect(calls("mediaEpisodesRaw").map((c) => c.args)).toStrictEqual([[1042, { recovery: 1 }]]);
    expect(calls("coverageSeriesDetailRaw").map((c) => c.args)).toStrictEqual([[42]]);
    expect(calls("stateIDsRaw").map((c) => c.args)).toStrictEqual([
      [{ type: "episode", prefix: "tvdb-42-" }],
    ]);

    onMovieDetail();
    wire.movieSummary = MOVIE_ROW;
    wire.movieSubs = [];
    await dispatchTransactionPageLeg(true);
    expect(calls("coverageMovieSummaryRaw").map((c) => c.args)).toStrictEqual([
      [7, { recovery: 1 }],
    ]);
    // The movie's /subs read never carries a query: it does not honor
    // ?recovery=1 (a store-only read).
    expect(calls("coverageMovieSubsRaw").map((c) => c.args)).toStrictEqual([[7]]);
  });

  it("a BOOT transaction reads plain (no recovery param)", async () => {
    onMovieDetail();
    wire.movieSummary = MOVIE_ROW;
    wire.movieSubs = [];
    wire.historyIDs = [];

    await dispatchTransactionPageLeg(false);

    expect(calls("coverageMovieSummaryRaw").map((c) => c.args)).toStrictEqual([[7, undefined]]);
  });

  it("movie detail: the transaction awaits the TRIPLE and renders from the leg's own reads", async () => {
    onMovieDetail();
    wire.movieSummary = MOVIE_ROW;
    const subs = [{ media_id: "tmdb-7" }];
    wire.movieSubs = subs;
    wire.historyIDs = ["tmdb-7"];

    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("applied");
    expect(calls("coverageMovieSubsRaw").map((c) => c.args)).toStrictEqual([[7]]);
    expect(calls("stateIDsRaw").map((c) => c.args)).toStrictEqual([
      [{ type: "movie", prefix: "tmdb-7" }],
    ]);
    // The render is the leg's own (pre-fetched reads), never the plain
    // openMovieDetail path with its fire-and-forget fetches.
    expect(rendered.movieLegs).toStrictEqual([{ m: MOVIE_ROW, subs, historyIDs: ["tmdb-7"] }]);
    expect(rendered.movies).toHaveLength(0);
  });

  it("commit WAITS for the movie triple: the leg settles only once /subs lands", async () => {
    onMovieDetail();
    wire.defer = true;

    let settled = false;
    const leg = dispatchTransactionPageLeg(false).then((r) => {
      settled = true;
      return r;
    });
    await new Promise((r) => setTimeout(r, 0));
    const pending = wire.pending.splice(0);
    expect(pending.map((p) => p.fn).sort()).toStrictEqual([
      "coverageMovieSubsRaw",
      "coverageMovieSummaryRaw",
      "stateIDsRaw",
    ]);

    // Summary + state ids land; /subs is still in flight — the leg (and so
    // the transaction's commit) must keep waiting.
    for (const p of pending) {
      if (p.fn !== "coverageMovieSubsRaw") {
        p.resolve(p.fn === "coverageMovieSummaryRaw" ? MOVIE_ROW : []);
      }
    }
    await new Promise((r) => setTimeout(r, 0));
    expect(settled).toBe(false);

    pending.find((p) => p.fn === "coverageMovieSubsRaw")?.resolve([]);
    expect(await leg).toBe("applied");
    expect(rendered.movieLegs).toHaveLength(1);
  });

  it("a /subs failure during the movie transaction REJECTS (the transaction aborts)", async () => {
    onMovieDetail();
    wire.movieSummary = MOVIE_ROW;
    wire.movieSubs = null; // 502 on the raw client
    wire.historyIDs = [];

    await expect(dispatchTransactionPageLeg(false)).rejects.toThrow("coverageMovieSubsRaw failed");
    // The failure is preserved, never painted as an empty subs table.
    expect(rendered.movieLegs).toHaveLength(0);
    expect(rendered.movies).toHaveLength(0);
  });

  it("a movie summary 404 is definitive: no render, the leg applies", async () => {
    onMovieDetail();
    wire.failStatus = 404;
    wire.movieSummary = null; // vanished movie
    wire.movieSubs = null;
    wire.historyIDs = [];

    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("applied");
    expect(rendered.movieLegs).toHaveLength(0);
  });

  it("a genuine leg failure REJECTS (the transaction aborts)", async () => {
    onSeriesDetail();
    wire.episodes = null; // 502 on the raw client
    wire.subFiles = [];
    wire.historyIDs = [];

    await expect(dispatchTransactionPageLeg(false)).rejects.toThrow("mediaEpisodesRaw failed");
    expect(rendered.series).toHaveLength(0);
  });

  it("a typed 429 refusal is a genuine leg failure after exactly ONE request", async () => {
    onSeriesDetail();
    wire.failStatus = 429;
    wire.episodes = null;
    wire.subFiles = [];
    wire.historyIDs = [];

    await expect(dispatchTransactionPageLeg(true)).rejects.toThrow();
    // Zero automatic retries: the refused endpoint saw exactly one request.
    expect(calls("mediaEpisodesRaw")).toHaveLength(1);
  });

  it("a 404 is a definitive answer, not a failure: the arm applies its fallback", async () => {
    onSeriesDetail();
    wire.failStatus = 404;
    wire.episodes = null; // vanished series: keep the cached seasons
    wire.subFiles = [];
    wire.historyIDs = [];

    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("applied");
    expect(rendered.series).toHaveLength(1);
    expect(rendered.series[0]?.seasons).toBe(CACHED_SEASONS);
  });

  it("history: dispatches the settlement-aware extraction and lands on its chain", async () => {
    store.set("currentPage", "history");
    store.set("detailCtx", null);
    rendered.historyLeg = ["superseded"];

    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("superseded"); // chained-to-applied counts as landed
    expect(rendered.historyLegCalls).toHaveLength(1);
    expect(rendered.reloadHistory).toBe(0); // never the void UI adapter
  });

  it("history: a chain ending failed rejects the leg", async () => {
    store.set("currentPage", "history");
    rendered.historyLeg = [new Error("history load failed (502)")];

    await expect(dispatchTransactionPageLeg(false)).rejects.toThrow("history load failed (502)");
  });

  it("the RE-ROUTE arm: a re-routed history run re-dispatches for the NEW route", async () => {
    // Transaction on /history; the route leaves to the library mid-leg. The
    // history run settles superseded(next = the new route's page leg); the
    // re-dispatch finds the library, whose EMPTY leg applies immediately —
    // no latch, no rejection.
    store.set("currentPage", "history");
    let reroute!: (v: string) => void;
    rendered.historyLeg = [
      new Promise<string>((res) => {
        reroute = res;
      }),
    ];
    const leg = dispatchTransactionPageLeg(false);
    await new Promise((r) => setTimeout(r, 0));
    expect(rendered.historyLegCalls).toHaveLength(1);

    // The route leave (what the router's applyRoute does mid-transaction),
    // then the aborted run settles as re-routed.
    store.set("currentPage", "library");
    store.set("detailCtx", null);
    reroute("rerouted");

    expect(await leg).toBe("applied");
    expect(rendered.historyLegCalls).toHaveLength(1); // not re-run for history
  });

  it("an outrun transaction leg lands as superseded (the newer dispatch owns the route)", async () => {
    onSeriesDetail();
    wire.defer = true;
    const leg = dispatchTransactionPageLeg(false);
    await Promise.resolve();
    const first = wire.pending.splice(0);

    // A newer plain dispatch (a DataInvalidate refresh) outruns the leg.
    wire.defer = false;
    wire.episodes = [];
    wire.subFiles = [];
    wire.historyIDs = [];
    const newer = refreshCurrentPage();

    for (const p of first) {
      p.resolve([]);
    }
    expect(await leg).toBe("superseded");
    expect(await newer).toBe("applied");
  });
});

// --- Deep-link boots: the URL classifies a pending detail (R2.3/A7) ---

describe("page-leg: pending deep-link classification", () => {
  // The runner's own URL, restored after each fixture (the suite drives the
  // real History API — Chromium in browser mode).
  const HOME = location.pathname + location.search;

  afterEach(() => {
    history.replaceState(null, "", HOME);
  });

  it("classifies from the URL when no detail context has landed, never from currentPage", () => {
    // The router's prepareDetailView window: currentPage is already
    // "library" while detailCtx lands only with the summary.
    onLibrary();

    history.replaceState(null, "", "/series/42");
    expect(currentRouteKey()).toBe("series:42");
    history.replaceState(null, "", "/series/42/search/en");
    expect(currentRouteKey()).toBe("series:42");
    history.replaceState(null, "", "/series/42/sync");
    expect(currentRouteKey()).toBe("series:42");
    history.replaceState(null, "", "/movie/7");
    expect(currentRouteKey()).toBe("movie:7");
    history.replaceState(null, "", "/series/42/files");
    expect(currentRouteKey()).toBe("files");
    history.replaceState(null, "", "/history");
    expect(currentRouteKey()).toBe("history");
    history.replaceState(null, "", "/");
    expect(currentRouteKey()).toBe("library");
    history.replaceState(null, "", "/settings");
    expect(currentRouteKey()).toBe("library");
  });

  it("a landed context outranks the URL (a row click swaps views without the router)", () => {
    history.replaceState(null, "", "/");
    onSeriesDetail();

    expect(currentRouteKey()).toBe("series:42");
  });

  it("a pending deep link's PLAIN dispatch is an empty leg — never the library pair", async () => {
    onLibrary();
    history.replaceState(null, "", "/series/42");

    const r = await refreshCurrentPage();

    expect(r).toBe("applied");
    expect(rendered.loadCoverage).toStrictEqual([]);
    expect(wire.calls).toStrictEqual([]);
  });

  it("a pending deep link's TRANSACTION leg is empty and settles applied (terminates)", async () => {
    onLibrary();
    history.replaceState(null, "", "/movie/7");

    const r = await dispatchTransactionPageLeg(false);

    expect(r).toBe("applied");
    expect(wire.calls).toStrictEqual([]);
    expect(rendered.loadCoverage).toStrictEqual([]);
  });
});
