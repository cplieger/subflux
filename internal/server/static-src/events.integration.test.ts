// events.integration.test.ts — the transaction against the REAL coverage
// modules (coverage.ts + coverage-heal.ts): the tombstone set over both
// full-pair writers, the per-collection JOIN, the failure-preserving
// collection leg, revocation, and the degraded-boot supersession. Only the
// network edge (wire client), the page leg, status, notify, and the actions
// hook are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SSE_MAX_RECONNECT_MS, SUMMARY_COALESCE_MS } from "./constants.js";
import { FakeEventSource, lastFakeES } from "./events-fakes.js";

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
  pollStatus: async () => {
    status.polls += 1;
  },
  abortPoll: vi.fn(),
  setStatusDegraded: vi.fn(),
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
  coverageSeriesRaw: (q?: unknown) => wireCall(wire.seriesRaw, [q]) as Promise<RawResult>,
  coverageMoviesRaw: (q?: unknown) => wireCall(wire.moviesRaw, [q]) as Promise<RawResult>,
  coverageSeriesSummaryRaw: (id: unknown, q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.seriesSummaryRaw, [id, q, opts?.signal]) as Promise<RawResult>,
  coverageMovieSummaryRaw: (id: unknown, q?: unknown, opts?: { signal?: AbortSignal }) =>
    wireCall(wire.movieSummaryRaw, [id, q, opts?.signal]) as Promise<RawResult>,
}));
vi.mock("./history.js", () => ({ noteHistoryMutation: vi.fn() }));

import type { SeriesItem } from "./wire/types.gen.js";
import * as store from "./store.js";
const storeRef = store;
import {
  coverageItems,
  fetchAndMergeCoverage,
  libraryLoaded,
  registeredCollections,
  _resetCoverageForTest,
} from "./coverage.js";
import { _resetHealForTest } from "./coverage-heal.js";
import { noteHistoryMutation } from "./history.js";

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
  await vi.advanceTimersByTimeAsync(0);
}

function onLibrary(): void {
  store.set("currentPage", "library");
  store.set("detailCtx", null);
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0);
  status.polls = 0;
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
  vi.runOnlyPendingTimers();
  events._resetEventsForTest();
  _resetHealForTest();
  _resetCoverageForTest();
  FakeEventSource.instances = [];
  vi.useRealTimers();
  vi.clearAllMocks();
});

/** A committed boot on the library route: the collection leg fetches the
 *  pair, applies it, opens the gate, registers the collections. */
async function bootWithPair(rows: Record<string, unknown>[], head = 5): Promise<void> {
  wire.seriesRaw.result = () => ({ ok: true, status: 200, data: rows });
  wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
  events.connect();
  lastFakeES().open();
  lastFakeES().epoch("boot-a", false, head);
  await settle();
  expect(events._stateForTest().watermark).toBe(head);
  expect(libraryLoaded()).toBe(true);
}

/** Emit a coverage frame and flush the heal coalescer's window. */
async function healFrame(es: FakeEventSource, tvdb: number, id: number): Promise<void> {
  es.frame(
    "coverage",
    {
      media_type: "episode",
      media_id: `tvdb-${String(tvdb)}-s01e01`,
      language: "en",
      variant: "standard",
      source: "auto",
    },
    id,
  );
  await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
}

describe("cold boot on /", () => {
  it("the boot transaction fetches the indivisible pair ONCE; the joined loader fetches nothing", async () => {
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(42)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    events.connect();

    // The gated boot load: applyRoute's loader arrives while the leg is in
    // flight and JOINS it (per collection).
    const loader = events.bootGate().then(() => fetchAndMergeCoverage());
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    const items = await loader;
    expect(items.map((i) => i.title)).toStrictEqual(["Show"]);
    expect(wire.seriesRaw.calls).toHaveLength(1);
    expect(wire.series.calls).toHaveLength(0); // the loader issued no read of its own
    expect(libraryLoaded()).toBe(true);
    expect([...registeredCollections()].sort()).toStrictEqual(["movies", "series"]);
  });

  it("a JOINED loader whose leg 502s: one request pair, empty state, registration, and the retry paints", async () => {
    wire.seriesRaw.result = () => ({ ok: false, status: 502, error: "upstream down" });
    wire.moviesRaw.result = () => ({ ok: false, status: 502, error: "upstream down" });
    events.connect();
    const loader = events.bootGate().then(() => fetchAndMergeCoverage());
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    // The loader rendered the route's normal empty state (no rows, no error
    // throw), still registered the pair, and the failed pair load set
    // neither the gate nor the watermark.
    const items = await loader;
    expect(items).toStrictEqual([]);
    expect(libraryLoaded()).toBe(false);
    expect(events._stateForTest().watermark).toBeNull();
    expect(events._stateForTest().forceLatch).toBe(true);
    expect(wire.seriesRaw.calls).toHaveLength(1);
    expect(wire.series.calls).toHaveLength(0);
    expect([...registeredCollections()].sort()).toStrictEqual(["movies", "series"]);

    // The latch's forced transaction commits and paints with zero user
    // action: the registered pair makes its collection leg non-empty.
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(42)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    expect(events._stateForTest().watermark).toBe(5);
    expect(coverageItems().map((i) => i.title)).toStrictEqual(["Show"]);
    expect(libraryLoaded()).toBe(true);
  });
});

describe("the JOIN per collection", () => {
  it("a loader whose pair the leg does NOT cover runs its normal load: the library paints and the gate opens", async () => {
    // A /history-session transaction: empty collection leg.
    store.set("currentPage", "history");
    events.connect();
    lastFakeES().open();
    leg.results = [
      new Promise<string>(() => {
        /* held open: the transaction outlives the navigation */
      }),
    ];
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
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
    const es0 = lastFakeES();
    expect(itemKeys()).toStrictEqual(["tvdb-42", "tvdb-43"]);

    // A recovery transaction whose pair was read BEFORE an arr delete: hold
    // the leg's responses, let a heal 404-delete land mid-transaction. The
    // reconnect's replay fell behind the ring, so the epoch verdict is a gap
    // — the trigger that opens the transaction.
    es0.fail();
    await settle();
    wire.seriesRaw.defer = true;
    wire.moviesRaw.defer = true;
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    const es1 = lastFakeES();
    es1.open();
    es1.epoch("boot-a", true, 5);
    await settle();
    expect(wire.seriesRaw.calls).toHaveLength(2); // boot's + this transaction's

    // The heal (frame ≤ head applies immediately): arr deleted tvdb-42, the
    // summary 404s, the row is removed and TOMBSTONED.
    wire.seriesSummaryRaw.result = () => ({ ok: false, status: 404, error: "gone" });
    await healFrame(es1, 42, 4);
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

    expect(events._stateForTest().watermark).toBe(5); // committed
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
    let releaseLeg!: (v: string) => void;
    leg.results = [
      new Promise<string>((res) => {
        releaseLeg = res;
      }),
    ];
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
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
    await healFrame(lastFakeES(), 42, 4);

    // The transaction settles FIRST (commit)…
    releaseLeg("applied");
    await settle();
    expect(events._stateForTest().watermark).toBe(5);

    // …and the loader's stale pair lands after settle: still dropped.
    for (const r of wire.series.pending.splice(0)) {
      r([seriesRow(42), seriesRow(43)]);
    }
    const items = await loader;
    expect(items.map((i) => (i as { tvdb_id?: number }).tvdb_id).sort()).toStrictEqual([43]);
    expect(itemKeys()).toStrictEqual(["tvdb-43"]);
  });
});

describe("revocation and supersession", () => {
  it("ABORT rolls nothing back: an applied collection leg stays applied", async () => {
    await bootWithPair([seriesRow(42)]);
    lastFakeES().fail();
    await settle();

    // The pair lands and APPLIES mid-transaction; the page leg then fails.
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(42), seriesRow(43)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    leg.results = [new Error("page leg failed (502)")];
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", true, 6);
    await settle();

    // Aborted (latched, no commit) — but the landed rows STAY: they are
    // newer than what they replaced.
    expect(events._stateForTest().forceLatch).toBe(true);
    expect(events._stateForTest().watermark).toBe(5); // the boot's commit, unchanged
    expect(itemKeys()).toStrictEqual(["tvdb-42", "tvdb-43"]);
  });

  it("an aborting transaction's still-in-flight pair landing does not revert the successor's fresher pair", async () => {
    await bootWithPair([seriesRow(42), seriesRow(43)]);
    lastFakeES().fail();
    await settle();

    // Transaction A (a gap verdict opens it): pair deferred, page leg fails
    // → abort with the pair unlanded.
    wire.seriesRaw.defer = true;
    wire.moviesRaw.defer = true;
    leg.results = [new Error("page leg failed (502)")];
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", true, 6);
    await settle();
    expect(events._stateForTest().forceLatch).toBe(true);
    const orphanSeries = wire.seriesRaw.pending.splice(0);
    const orphanMovies = wire.moviesRaw.pending.splice(0);

    // The successor (forced) transaction lands the FRESHER pair: tvdb-42 is
    // gone upstream.
    wire.seriesRaw.defer = false;
    wire.moviesRaw.defer = false;
    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(43)] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 7);
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
    expect(events._stateForTest().watermark).toBe(7);
  });

  it("a degraded boot's ungated fetch is superseded by the transaction's leg", async () => {
    events.connect();
    lastFakeES().fail(); // degrade the gate
    await settle();

    // The ungated boot load: a plain pair fetch, still in flight when the
    // recovery transaction's leg lands.
    wire.series.defer = true;
    wire.movies.result = () => [];
    const loader = events.bootGate().then(() => fetchAndMergeCoverage());
    await settle();
    expect(wire.series.calls).toHaveLength(1);

    wire.seriesRaw.result = () => ({ ok: true, status: 200, data: [seriesRow(43, "Fresh")] });
    wire.moviesRaw.result = () => ({ ok: true, status: 200, data: [] });
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    expect(coverageItems().map((i) => i.title)).toStrictEqual(["Fresh"]);
    // Recovery semantics: the leg read with ?recovery=1.
    expect(wire.seriesRaw.calls).toStrictEqual([[{ recovery: 1 }]]);

    // The stale ungated fetch lands late: aborted + discarded.
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
    await healFrame(lastFakeES(), 42, 4);
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // the single re-enqueue
    const healCallsBefore = wire.seriesSummaryRaw.calls.length;
    expect(healCallsBefore).toBeGreaterThanOrEqual(2);

    // A committing transaction (a gap verdict opens it) lands the pair fresh.
    lastFakeES().fail();
    await settle();
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", true, 6);
    await settle();
    expect(events._stateForTest().watermark).toBe(6);

    // The reconcile tick retries nothing: the dirty set was subsumed.
    for (const task of status.reconcileTasks) {
      task();
    }
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    expect(wire.seriesSummaryRaw.calls).toHaveLength(healCallsBefore);
  });
});

describe("E4's history trigger sits OUTSIDE the heal gate", () => {
  it("a poller-import event on a fresh /history tab notes the reload with zero coverage fetches", async () => {
    // A fresh tab straight to /history: no collection loaded, no library
    // route — the boot transaction's collection leg is EMPTY.
    store.set("currentPage", "history");
    store.set("detailCtx", null);
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    expect(libraryLoaded()).toBe(false);
    expect(wire.seriesRaw.calls).toHaveLength(0);

    // The server's poller imported a subtitle: a coverage event arrives.
    lastFakeES().frame(
      "coverage",
      {
        media_type: "episode",
        media_id: "tvdb-42-s01e01",
        language: "en",
        variant: "standard",
        source: "auto",
      },
      6,
    );
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS + 50);

    // The heal gate is CLOSED (nothing on screen renders the root): zero
    // summary fetches — but the history trigger observed the event anyway.
    expect(wire.seriesSummaryRaw.calls).toHaveLength(0);
    expect(noteHistoryMutation).toHaveBeenCalledTimes(1);
  });
});
