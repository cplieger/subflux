// page-leg.test.ts — B2: the page-leg dispatcher. Driven against the REAL
// store and bus; the wire client and the route renderers (detail, coverage,
// history) are replaced so the assertions are about dispatch, supersession,
// and abort — never about rendering, which each renderer's own suite pins.
import { describe, it, vi, beforeEach, expect } from "vitest";

// Plain factories over hoisted mutable records (mockReset strips vi.fn
// implementations between tests; see coverage.test.ts's header note).
const wire = vi.hoisted(() => ({
  episodes: null as unknown,
  subFiles: null as unknown,
  historyIDs: null as unknown,
  movieSummary: null as unknown,
  calls: [] as { fn: string; args: unknown[]; signal: AbortSignal | undefined }[],
  // When true, wire reads hang until resolved by hand. The deferred promise
  // deliberately IGNORES its abort signal: resolving it late with real data
  // is exactly the stale-response race the generation guard must discard.
  defer: false,
  pending: [] as { fn: string; resolve: (v: unknown) => void }[],
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

vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: (id: unknown, _q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("mediaEpisodes", () => wire.episodes, [id], opts?.signal),
  coverageSeriesDetail: (id: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("coverageSeriesDetail", () => wire.subFiles, [id], opts?.signal),
  stateIDs: (q: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("stateIDs", () => wire.historyIDs, [q], opts?.signal),
  coverageMovieSummary: (id: unknown, _q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("coverageMovieSummary", () => wire.movieSummary, [id], opts?.signal),
  // The dispatcher must never read the collection (R1.2): present so a
  // regression that re-adds the call is recorded and fails the pin below.
  coverageMovies: (_q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall("coverageMovies", () => null, [], opts?.signal),
}));

const rendered = vi.hoisted(() => ({
  series: [] as { series: unknown; seasons: unknown; subFiles: unknown; historySet: unknown }[],
  movies: [] as { m: unknown; skipPush: boolean | undefined; signal: AbortSignal | undefined }[],
  loadCoverage: [] as (boolean | undefined)[],
  reloadHistory: 0,
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
}));
vi.mock("./coverage.js", () => ({
  loadCoverage: (silent?: boolean) => {
    rendered.loadCoverage.push(silent);
    return Promise.resolve();
  },
}));
vi.mock("./history.js", () => ({
  reloadHistory: () => {
    rendered.reloadHistory++;
  },
}));

import * as store from "./store.js";
import { emit, BusEvent } from "./bus.js";
import { abortPageLeg, refreshCurrentPage } from "./page-leg.js";
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
  wire.calls = [];
  wire.defer = false;
  wire.pending = [];
  rendered.series = [];
  rendered.movies = [];
  rendered.loadCoverage = [];
  rendered.reloadHistory = 0;
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
