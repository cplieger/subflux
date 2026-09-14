// events.integration.test.ts — the revalidate body against the REAL coverage
// modules (coverage.ts + coverage-heal.ts): the tombstone set over both
// full-pair writers, the per-collection JOIN, the failure-preserving
// collection leg, orphaned legs, and supersession of the plain loader. Only
// the network edge (wire client), the page leg, status, notify, and the
// actions hook are replaced; the stream is the real library over a fake
// server.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SUMMARY_COALESCE_MS } from "./constants.js";
import { EPOCH_A, fakeSSE, type FakeSSE } from "./events-fakes.js";

const server = vi.hoisted(() => ({ current: null as FakeSSE | null }));
// The real transport stays (the generated client the coverage modules use
// dispatches through it); only the stream's fetch and the 401 seam move.
vi.mock("./api-client.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  authFetch: (input: RequestInfo | URL, init?: RequestInit) => {
    if (!server.current) {
      throw new Error("fake server not installed");
    }
    return server.current.fetch(input, init);
  },
  handleSessionExpiry: vi.fn(),
}));

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
// Real coverage pulls detail-scan, which needs the full actions surface;
// only the unload hook is neutered.
vi.mock("@cplieger/actions", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  registerCleanup: vi.fn(),
}));

const status = vi.hoisted(() => ({
  polls: 0,
  reconcileTasks: [] as (() => void)[],
}));
vi.mock("./status.js", () => ({
  pollStatus: () => {
    status.polls += 1;
    return Promise.resolve();
  },
  abortPoll: vi.fn(),
  setStatusDegraded: vi.fn(),
  setStatusAttached: vi.fn(),
  applyActivityEvent: vi.fn(),
  applyAlertEvent: vi.fn(),
  applyProviderEvent: vi.fn(),
  registerReconcileTask: (fn: () => void) => {
    status.reconcileTasks.push(fn);
    return () => undefined;
  },
}));

// The page leg, deferrable so a transaction can be held open while heals and
// loaders race it. An Error entry rejects lazily at dispatch time.
const leg = vi.hoisted(() => ({
  results: [] as (string | Error | Promise<string>)[],
  calls: 0,
}));
vi.mock("./page-leg.js", () => ({
  currentRouteKey: () => {
    // Mirrors the real dispatcher's route identity off the real store; the
    // suite drives routes through store state exactly like the router.
    const page = storeRef.get("currentPage");
    if (page === "history") {
      return "history";
    }
    const ctx = storeRef.get("detailCtx") as { tvdbId?: number } | null;
    if (ctx && ctx.tvdbId !== undefined) {
      return `series:${String(ctx.tvdbId)}`;
    }
    return "library";
  },
  routeSubject: (key: string) => {
    if (key === "history") {
      return { kind: "history", ref: "" };
    }
    const m = /^series:(\d+)$/.exec(key);
    return m ? { kind: "detail", ref: `tvdb-${m[1] ?? ""}` } : null;
  },
  dispatchTransactionPageLeg: () => {
    leg.calls += 1;
    const next = leg.results.shift() ?? "applied";
    return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
  },
}));

// The network edge: every read scripted + deferrable per function.
interface RawResult {
  ok: boolean;
  status: number;
  data?: unknown;
  error?: string;
}
interface WireFn {
  calls: unknown[][];
  result: () => unknown;
  defer: boolean;
  pending: ((v: unknown) => void)[];
}
const wire = vi.hoisted(() => {
  const fn = (): WireFn => ({
    calls: [],
    result: () => null,
    defer: false,
    pending: [],
  });
  return {
    series: fn(), // null-collapsing loader read
    movies: fn(),
    seriesRaw: fn(), // the collection leg
    moviesRaw: fn(),
    seriesSummaryRaw: fn(), // the heal
    movieSummaryRaw: fn(),
  };
});
function wireCall(f: WireFn, args: unknown[]): Promise<unknown> {
  f.calls.push(args);
  if (f.defer) {
    return new Promise((resolve) => {
      f.pending.push(resolve);
    });
  }
  return Promise.resolve(f.result());
}
// Keep the real module (PATH_* constants and the functions the deeper
// coverage imports pull in) and override only the reads this suite scripts.
vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  coverageSeries: (q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.series, [q, opts?.signal]).then((v) => (opts?.signal?.aborted ? null : v)),
  coverageMovies: (q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.movies, [q, opts?.signal]).then((v) => (opts?.signal?.aborted ? null : v)),
  coverageSeriesRaw: (q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.seriesRaw, [q, opts?.signal]) as Promise<RawResult>,
  coverageMoviesRaw: (q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.moviesRaw, [q, opts?.signal]) as Promise<RawResult>,
  coverageSeriesSummaryRaw: (id: unknown, q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.seriesSummaryRaw, [id, q, opts?.signal]) as Promise<RawResult>,
  coverageMovieSummaryRaw: (id: unknown, q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.movieSummaryRaw, [id, q, opts?.signal]) as Promise<RawResult>,
}));
vi.mock("./history.js", () => ({ noteHistoryMutation: vi.fn() }));
vi.mock("./search.js", () => ({ noteServerRestart: vi.fn() }));

import type { SeriesItem } from "./wire/types.gen.js";
import * as store from "./store.js";
const storeRef = store;
import { fetchAndMergeCoverage, _resetCoverageForTest } from "./coverage.js";
import { coverageItems, libraryLoaded, registeredCollections } from "./coverage-store.js";
import { _resetHealForTest } from "./coverage-heal.js";
import { noteHistoryMutation } from "./history.js";
import { _resetSubjectsForTest, versionMap } from "./subjects.js";

const events = await import("./events.js");

// One series row, enough of the wire shape for the signature + media id.
function seriesRow(tvdb: number, title = "Show"): Record<string, unknown> {
  return {
    id: 1000 + tvdb,
    tvdb_id: tvdb,
    title,
    year: 2020,
    rule: "en",
    audio_lang: "en",
    excluded: false,
    episodes: 5,
    targets: [],
  };
}

function itemKeys(): string[] {
  return coverageItems()
    .map((i) => (i._type === "series" ? `tvdb-${String(i.tvdb_id)}` : `tmdb-${String(i.tmdb_id)}`))
    .sort();
}

async function settle(): Promise<void> {
  for (let i = 0; i < 6; i++) {
    await vi.advanceTimersByTimeAsync(0);
  }
}

function onLibrary(): void {
  store.set("currentPage", "library");
  store.set("detailCtx", null);
}

const SERIES_CHANGED = { changed: [{ kind: "series", ref: "", version: "2" }] };

/** The pair as the transport would have recorded it after the loader landed. */
function holdPair(): void {
  versionMap().observe({ kind: "series", ref: "" }, "1", EPOCH_A);
  versionMap().observe({ kind: "movies", ref: "" }, "1", EPOCH_A);
}

/** Connect and answer with a hello, then let the revalidate it schedules run. */
async function connectHello(): Promise<void> {
  events.connect();
  await settle();
  server.current?.last().hello(EPOCH_A, { head: 5 });
  await settle();
}

/** Drop the live stream and let the ladder reconnect, answering with a fresh
 *  hello whose revalidate runs the digest again. */
async function reconnect(): Promise<void> {
  server.current?.last().end();
  await vi.advanceTimersByTimeAsync(2_000);
  server.current?.last().hello(EPOCH_A, { head: 6 });
  await settle();
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0);
  server.current = fakeSSE();
  status.polls = 0;
  // The browser project runs in real Chromium, where SharedWorker exists;
  // these suites pin the per-tab stream, so the worker branch is closed.
  vi.stubGlobal("SharedWorker", undefined);
  status.reconcileTasks = [];
  leg.results = [];
  leg.calls = 0;
  for (const f of Object.values(wire)) {
    f.calls = [];
    f.result = () => null;
    f.defer = false;
    f.pending = [];
  }
  onLibrary();
});

afterEach(() => {
  events._resetEventsForTest();
  _resetSubjectsForTest();
  _resetHealForTest();
  _resetCoverageForTest();
  server.current = null;
  vi.useRealTimers();
  vi.clearAllMocks();
});

/** A booted library tab: the route loader landed the pair (registered, gate
 *  open, both stamps held) and the stream is up on an empty digest. */
async function bootWithPair(rows: Record<string, unknown>[]): Promise<void> {
  wire.series.result = () => rows;
  wire.movies.result = () => [];
  await fetchAndMergeCoverage();
  holdPair();
  await connectHello();
  expect(libraryLoaded()).toBe(true);
  expect(server.current!.digestCalls).toHaveLength(1);
}

/** Emit a coverage frame and flush the heal coalescer's window. */
async function healFrame(tvdb: number, offset: number): Promise<void> {
  server.current!.last().frame(
    "coverage",
    {
      media_type: "episode",
      media_id: `tvdb-${String(tvdb)}-s01e01`,
      language: "en",
      variant: "standard",
      source: "auto",
    },
    `${EPOCH_A}:${String(offset)}`,
  );
  await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
  await settle();
}

describe("cold boot on /", () => {
  it("the route loader fetches the pair ONCE and the boot hello's digest fetches nothing", async () => {
    wire.series.result = () => [seriesRow(42)];
    wire.movies.result = () => [];
    const items = await fetchAndMergeCoverage();
    expect(items.map((i) => i.title)).toStrictEqual(["Show"]);

    await connectHello();

    // An empty map on a fresh page: the digest named nothing, the leg is empty.
    expect(server.current!.digestCalls[0]?.subjects).toStrictEqual([]);
    expect(wire.seriesRaw.calls).toHaveLength(0);
    expect(wire.series.calls).toHaveLength(1);
    expect(libraryLoaded()).toBe(true);
    expect([...registeredCollections()].sort()).toStrictEqual(["movies", "series"]);
    expect(versionMap().epoch()).toBe(EPOCH_A);
  });

  it("a JOINED loader whose leg 502s: one request pair, registration, and the retry paints", async () => {
    await bootWithPair([seriesRow(42)]);
    _resetCoverageForTest(); // the tab reloads its view: rows gone, gate closed
    holdPair();

    // A digest names the pair; the leg is in flight when the loader arrives
    // and JOINS it instead of issuing a second read.
    server.current!.digestScript.push(SERIES_CHANGED);
    wire.seriesRaw.result = () => ({ ok: false, status: 502, error: "upstream down" });
    wire.moviesRaw.result = () => ({ ok: false, status: 502, error: "upstream down" });
    wire.seriesRaw.defer = true;
    wire.moviesRaw.defer = true;
    await reconnect();
    expect(wire.seriesRaw.calls).toHaveLength(1);
    wire.series.calls = []; // the boot loader's own read is not the question
    const loader = fetchAndMergeCoverage();
    await settle();
    expect(wire.series.calls).toHaveLength(0); // joined, no read of its own

    for (const r of wire.seriesRaw.pending.splice(0)) {
      r({ ok: false, status: 502, error: "upstream down" });
    }
    for (const r of wire.moviesRaw.pending.splice(0)) {
      r({ ok: false, status: 502, error: "upstream down" });
    }
    await settle();

    // The loader rendered the route's normal empty state, registered the
    // pair, and the gate stayed closed; the map kept the OLD version.
    const items = await loader;
    expect(items).toStrictEqual([]);
    expect(libraryLoaded()).toBe(false);
    expect([...registeredCollections()].sort()).toStrictEqual(["movies", "series"]);
    expect(versionMap().has({ kind: "series", ref: "" })).toBe(true);

    // The failed run ended the stream; the reconnect's digest names the pair
    // again and the retry paints with zero user action.
    wire.seriesRaw.defer = false;
    wire.moviesRaw.defer = false;
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(42)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    server.current!.digestScript.push(SERIES_CHANGED);
    await vi.advanceTimersByTimeAsync(2_000);
    server.current!.last().hello(EPOCH_A, { head: 6, resumed: true });
    await settle();

    expect(wire.seriesRaw.calls).toHaveLength(2);
    expect(coverageItems().map((i) => i.title)).toStrictEqual(["Show"]);
    expect(libraryLoaded()).toBe(true);
  });
});

describe("the JOIN per collection", () => {
  it("a loader whose pair the leg does NOT cover runs its normal load: the library paints and the gate opens", async () => {
    // A /history-session revalidate: empty collection leg, page leg held.
    store.set("currentPage", "history");
    versionMap().observe({ kind: "history", ref: "" }, "1", EPOCH_A);
    server.current!.digestScript.push({ changed: [{ kind: "history", ref: "", version: "2" }] });
    leg.results = [
      new Promise<string>(() => {
        /* held open: the transaction outlives the navigation */
      }),
    ];
    await connectHello();
    expect(leg.calls).toBe(1);
    expect(wire.seriesRaw.calls).toHaveLength(0); // the leg is empty

    // Mid-transaction navigation to the library: the loader's NORMAL load.
    onLibrary();
    wire.series.result = () => [seriesRow(42)];
    wire.movies.result = () => [];
    const items = await fetchAndMergeCoverage();

    expect(items.map((i) => i.title)).toStrictEqual(["Show"]);
    expect(wire.series.calls).toHaveLength(1);
    expect(libraryLoaded()).toBe(true);
    expect([...registeredCollections()].sort()).toStrictEqual(["movies", "series"]);
  });
});

describe("tombstones over both writers", () => {
  it("collection leg lands LAST: the healed-away row is absent after commit", async () => {
    await bootWithPair([seriesRow(42), seriesRow(43)]);
    expect(itemKeys()).toStrictEqual(["tvdb-42", "tvdb-43"]);

    // A revalidate whose digest names the pair, its responses held; a heal
    // 404-delete lands mid-transaction.
    wire.seriesRaw.defer = true;
    wire.moviesRaw.defer = true;
    server.current!.digestScript.push(SERIES_CHANGED);
    await reconnect();
    expect(wire.seriesRaw.calls).toHaveLength(1);

    // The heal is HELD by the library while the revalidate runs, so drive
    // the 404-delete through the coalescer directly: arr deleted tvdb-42.
    wire.seriesSummaryRaw.result = () => ({ ok: false, status: 404, error: "gone" });
    const { healFromCoverageEvent } = await import("./coverage-heal.js");
    healFromCoverageEvent({
      media_type: "episode",
      media_id: "tvdb-42-s01e01",
      language: "en",
      variant: "standard",
      source: "auto",
    });
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await settle();
    expect(itemKeys()).toStrictEqual(["tvdb-43"]);

    // The stale pair (still carrying tvdb-42) lands LAST: the shared
    // application site drops the tombstoned row.
    for (const r of wire.seriesRaw.pending.splice(0)) {
      r({ ok: true, status: 200, data: [seriesRow(42), seriesRow(43)] });
    }
    for (const r of wire.moviesRaw.pending.splice(0)) {
      r({ ok: true, status: 200, data: [] });
    }
    await settle();

    expect(itemKeys()).toStrictEqual(["tvdb-43"]); // the row stayed absent
  });

  it("the empty-leg arm's LOADER lands last — even AFTER settle, a covered writer still drops", async () => {
    // A deep-link detail session: gate closed, empty collection leg; the
    // heal reaches the open detail's root through the detail arm.
    store.set("currentPage", "library");
    store.set("detailCtx", {
      series: seriesRow(42) as unknown as SeriesItem,
      seasons: [],
      tvdbId: 42,
    });
    versionMap().observe({ kind: "detail", ref: "tvdb-42" }, "1", EPOCH_A);
    server.current!.digestScript.push({
      changed: [{ kind: "detail", ref: "tvdb-42", version: "2" }],
    });
    let releaseLeg!: (v: string) => void;
    leg.results = [
      new Promise<string>((res) => {
        releaseLeg = res;
      }),
    ];
    await connectHello();
    expect(leg.calls).toBe(1);
    expect(wire.seriesRaw.calls).toHaveLength(0); // empty leg

    // Mid-transaction navigation to the library: the loader's plain fetch
    // BEGINS during the transaction (a covered writer) and defers.
    onLibrary();
    wire.series.defer = true;
    wire.movies.result = () => [];
    const loader = fetchAndMergeCoverage();
    await Promise.resolve();

    // The heal deletes the detail root mid-transaction (tombstone recorded).
    store.set("detailCtx", {
      series: seriesRow(42) as unknown as SeriesItem,
      seasons: [],
      tvdbId: 42,
    });
    wire.seriesSummaryRaw.result = () => ({ ok: false, status: 404, error: "gone" });
    const { healFromCoverageEvent } = await import("./coverage-heal.js");
    healFromCoverageEvent({
      media_type: "episode",
      media_id: "tvdb-42-s01e01",
      language: "en",
      variant: "standard",
      source: "auto",
    });
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await settle();

    // The transaction settles FIRST (commit)…
    releaseLeg("applied");
    await settle();

    // …and the loader's stale pair lands after settle: still dropped.
    for (const r of wire.series.pending.splice(0)) {
      r([seriesRow(42), seriesRow(43)]);
    }
    const items = await loader;
    expect(items.map((i) => (i as { tvdb_id?: number }).tvdb_id).sort()).toStrictEqual([43]);
    expect(itemKeys()).toStrictEqual(["tvdb-43"]);
  });
});

describe("a failed transaction", () => {
  it("rolls nothing back: an applied collection leg stays applied", async () => {
    await bootWithPair([seriesRow(42)]);

    // must_refetch runs every leg: the pair lands and APPLIES, then the page
    // leg fails.
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(42), seriesRow(43)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    leg.results = [new Error("page leg failed (502)")];
    server.current!.digestScript.push({ must_refetch: true, epoch: EPOCH_A });
    await reconnect();

    // Failed (the stream ended unverified) — but the landed rows STAY: they
    // are newer than what they replaced.
    expect(events._stateForTest().stream?.kind).not.toBe("open");
    expect(itemKeys()).toStrictEqual(["tvdb-42", "tvdb-43"]);
  });

  it("an orphaned pair landing does not revert the successor's fresher pair", async () => {
    await bootWithPair([seriesRow(42), seriesRow(43)]);

    // Transaction A: pair deferred, page leg fails → the run fails with the
    // pair unlanded.
    wire.seriesRaw.defer = true;
    wire.moviesRaw.defer = true;
    leg.results = [new Error("page leg failed (502)")];
    server.current!.digestScript.push({ must_refetch: true, epoch: EPOCH_A });
    await reconnect();
    const orphanSeries = wire.seriesRaw.pending.splice(0);
    const orphanMovies = wire.moviesRaw.pending.splice(0);
    expect(orphanSeries).toHaveLength(1);

    // The successor lands the FRESHER pair: tvdb-42 is gone upstream.
    wire.seriesRaw.defer = false;
    wire.moviesRaw.defer = false;
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(43)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    server.current!.digestScript.push(SERIES_CHANGED);
    await vi.advanceTimersByTimeAsync(2_000);
    server.current!.last().hello(EPOCH_A, { head: 7, resumed: true });
    await settle();
    expect(itemKeys()).toStrictEqual(["tvdb-43"]);

    // The ORPHAN lands (stale pair, tvdb-42 still present): a no-op.
    for (const r of orphanSeries) {
      r({ ok: true, status: 200, data: [seriesRow(42), seriesRow(43)] });
    }
    for (const r of orphanMovies) {
      r({ ok: true, status: 200, data: [] });
    }
    await settle();

    expect(itemKeys()).toStrictEqual(["tvdb-43"]); // no revert
  });

  it("the plain loader's in-flight fetch is superseded by the transaction's leg", async () => {
    holdPair(); // a previous load held the pair; this load re-reads it
    wire.series.defer = true;
    wire.movies.result = () => [];
    const loader = fetchAndMergeCoverage();
    await settle();
    expect(wire.series.calls).toHaveLength(1);

    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(43, "Fresh")] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    server.current!.digestScript.push(SERIES_CHANGED);
    await connectHello();
    expect(coverageItems().map((i) => i.title)).toStrictEqual(["Fresh"]);
    // Recovery semantics: the leg read with ?recovery=1.
    expect(wire.seriesRaw.calls[0]?.[0]).toStrictEqual({ recovery: 1 });

    // The stale plain fetch lands late: aborted + discarded.
    for (const r of wire.series.pending.splice(0)) {
      r([seriesRow(42, "Stale")]);
    }
    await loader;
    expect(coverageItems().map((i) => i.title)).toStrictEqual(["Fresh"]);
  });
});

describe("the dirty set and the committing transaction", () => {
  it("a committing covered transaction subsumes the dirty set", async () => {
    await bootWithPair([seriesRow(42)]);

    // Fail a heal twice: the root joins the dirty set (retried at ticks).
    wire.seriesSummaryRaw.result = () => ({ ok: false, status: 502, error: "down" });
    await healFrame(42, 4);
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // the single re-enqueue
    await settle();
    const healCallsBefore = wire.seriesSummaryRaw.calls.length;
    expect(healCallsBefore).toBeGreaterThanOrEqual(2);

    // A committing transaction (the digest names the pair) lands it fresh.
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(42)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    server.current!.digestScript.push(SERIES_CHANGED);
    await reconnect();
    expect(wire.seriesRaw.calls).toHaveLength(1);

    // The reconcile tick retries nothing: the dirty set was subsumed.
    for (const task of status.reconcileTasks) {
      task();
    }
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await settle();
    expect(wire.seriesSummaryRaw.calls).toHaveLength(healCallsBefore);
  });
});

describe("E4's history trigger sits OUTSIDE the heal gate", () => {
  it("a poller-import event on a fresh /history tab notes the reload with zero coverage fetches", async () => {
    // A fresh tab straight to /history: no collection loaded, no library
    // route — nothing held, nothing fetched.
    store.set("currentPage", "history");
    store.set("detailCtx", null);
    await connectHello();
    expect(libraryLoaded()).toBe(false);
    expect(wire.seriesRaw.calls).toHaveLength(0);

    // The server's poller imported a subtitle: a coverage event arrives.
    await healFrame(42, 6);
    await vi.advanceTimersByTimeAsync(50);

    // The heal gate is CLOSED (nothing on screen renders the root): zero
    // summary fetches — but the history trigger observed the event anyway.
    expect(wire.seriesSummaryRaw.calls).toHaveLength(0);
    expect(noteHistoryMutation).toHaveBeenCalledTimes(1);
  });
});
