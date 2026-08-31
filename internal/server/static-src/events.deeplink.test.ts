// events.deeplink.test.ts — the deep-link boot seam (R2.3/A7): the REAL
// router, page-leg dispatcher, and coverage modules composed under the real
// SSE transaction. The class of defect this file exists for: every per-task
// suite green while the composition fetches the collection pair on a boot
// that must load none — the router sets currentPage="library" synchronously
// while detailCtx lands only with the summary, so only the real
// prepareDetailView-before-detailCtx window can prove the collection leg
// stays EMPTY. Only the network edge (wire client), status, notify, the
// detail renderers, and the router's popup collaborators are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SUMMARY_COALESCE_MS } from "./constants.js";
import { FakeEventSource, lastFakeES } from "./events-fakes.js";

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
// Real coverage pulls the actions surface; only the unload hook is neutered.
vi.mock("@cplieger/actions", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  registerCleanup: vi.fn(),
}));

const status = vi.hoisted(() => ({ polls: 0 }));
vi.mock("./status.js", () => ({
  pollStatus: async () => {
    status.polls += 1;
  },
  abortPoll: vi.fn(),
  setStatusDegraded: vi.fn(),
  applyActivityEvent: vi.fn(),
  applyAlertEvent: vi.fn(),
  applyProviderEvent: vi.fn(),
  registerReconcileTask: () => () => undefined,
}));

// The real module registers apiActions at import time and pulls in the
// status.ts graph; coverage.ts only consumes registerScanButton.
vi.mock("./detail-scan.js", () => ({ registerScanButton: () => undefined }));

// The detail renderers: page-leg and the OpenSeries wiring below stand in
// for detail.ts (whose own rendering is pinned in detail.test.ts).
const rendered = vi.hoisted(() => ({ series: 0, movies: 0 }));
vi.mock("./detail.js", () => ({
  renderSeriesDetail: () => {
    rendered.series += 1;
  },
  openMovieDetail: () => {
    rendered.movies += 1;
  },
  renderMovieDetailFromLeg: () => {
    rendered.movies += 1;
  },
  disposeDetailBindings: vi.fn(),
}));

// The router's popup collaborators (their module graphs stay out).
vi.mock("./config.js", () => ({ openConfig: vi.fn() }));
vi.mock("./search.js", () => ({ openSearchPopup: vi.fn(), armDownloadRestartSweep: vi.fn() }));
vi.mock("./files.js", () => ({ openFileManager: vi.fn() }));

// The view transition runs its callback straight through (cosmetics).
vi.mock("@cplieger/ui-primitives/view-transition", () => ({
  viewTransition: (fn: () => void) => {
    fn();
    return Promise.resolve();
  },
}));

// The network edge: per-function call counters + scripted results.
interface RawResult {
  ok: boolean;
  status: number;
  data?: unknown;
  error?: string;
}
const wire = vi.hoisted(() => ({
  // The four collection reads a deep-link boot must never issue.
  seriesRaw: 0,
  moviesRaw: 0,
  series: 0,
  movies: 0,
  pairRows: [] as Record<string, unknown>[],
  // Per-root summaries keyed `${kind}:${id}` (the route loader + the heal).
  summaries: new Map<string, RawResult>(),
  summaryCalls: [] as { kind: "series" | "movie"; id: number }[],
  // The detail-coupling refresh pair.
  seriesDetailCalls: 0,
  stateIDsCalls: 0,
}));
vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  coverageSeriesRaw: () => {
    wire.seriesRaw += 1;
    return Promise.resolve({ ok: true, status: 200, data: wire.pairRows });
  },
  coverageMoviesRaw: () => {
    wire.moviesRaw += 1;
    return Promise.resolve({ ok: true, status: 200, data: [] });
  },
  coverageSeries: () => {
    wire.series += 1;
    return Promise.resolve(wire.pairRows);
  },
  coverageMovies: () => {
    wire.movies += 1;
    return Promise.resolve([]);
  },
  coverageSeriesSummaryRaw: (id: string | number) => {
    wire.summaryCalls.push({ kind: "series", id: Number(id) });
    return Promise.resolve(
      wire.summaries.get(`series:${String(id)}`) ?? { ok: false, status: 500, error: "boom" },
    );
  },
  coverageMovieSummaryRaw: (id: string | number) => {
    wire.summaryCalls.push({ kind: "movie", id: Number(id) });
    return Promise.resolve(
      wire.summaries.get(`movie:${String(id)}`) ?? { ok: false, status: 500, error: "boom" },
    );
  },
  coverageSeriesDetail: () => {
    wire.seriesDetailCalls += 1;
    return Promise.resolve([]);
  },
  stateIDs: () => {
    wire.stateIDsCalls += 1;
    return Promise.resolve([]);
  },
  listStateRaw: () => Promise.resolve({ ok: true, status: 200, data: [] }),
}));

/** The subset of index.html the router + coverage modules touch. Built
 *  before the imports because router.ts resolves the filter controls at
 *  module scope. */
document.body.innerHTML = `
  <button type="button" id="historyBtn">History</button>
  <div id="coveragePanel">
    <div class="card-head"><h2 id="lib-heading">Library</h2></div>
    <div class="controls">
      <select id="cov-type-filter"><option value="all"></option><option value="movies"></option></select>
      <input id="cov-filter" type="search" />
      <input id="cov-missing" type="checkbox" />
      <select id="cov-sort"><option value="title"></option><option value="missing"></option></select>
    </div>
    <div id="coverageContent"></div>
  </div>
  <div id="historyPanel" hidden>
    <div class="card-head"><h2 id="hist-heading">History</h2></div>
    <input id="h-filter" type="search" />
  </div>`;

import * as store from "./store.js";
import { on, BusEvent } from "./bus.js";
import { _resetCoverageForTest } from "./coverage.js";
import { coverageItems, libraryLoaded, registeredCollections } from "./coverage-store.js";
import { _resetHealForTest } from "./coverage-heal.js";
import type { SeriesItem, SeasonGroup } from "./api-types.js";

// router.ts resolves the four filter controls at IMPORT time, so it must
// load AFTER the fixture above — a static import would hoist past it.
const { applyRoute } = await import("./router.js");
const events = await import("./events.js");

// detail.ts is mocked, so its OpenSeries subscription is re-created here:
// the real handler resolves the routed item into detailCtx once the summary
// lands (detail.ts renderSeriesDetail), which is exactly the state the heal
// gate and the coupling read.
on(BusEvent.OpenSeries, ({ item }) => {
  store.set("detailCtx", {
    series: item as SeriesItem,
    seasons: [] as SeasonGroup[],
    tvdbId: (item as SeriesItem).tvdb_id,
  });
});

const HOME = location.pathname + location.search;

function seriesSummary(tvdb: number): Record<string, unknown> {
  return {
    id: 1000 + tvdb,
    tvdb_id: tvdb,
    title: `Show ${String(tvdb)}`,
    year: 2020,
    rule: "en",
    audio_lang: "en",
    excluded: false,
    episodes: 5,
    targets: [],
  };
}

async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
}

/** Emit a coverage frame and flush the heal coalescer's window. */
async function healFrame(tvdb: number, id: number): Promise<void> {
  lastFakeES().frame(
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

/** A COLD deep-link boot on /series/42, wired exactly like app.ts: the boot
 *  gate releases applyRoute, the epoch's transaction runs beside it. */
async function bootDeepLink(): Promise<void> {
  history.replaceState(null, "", "/series/42");
  wire.summaries.set("series:42", { ok: true, status: 200, data: seriesSummary(42) });
  void events.bootGate().then(() => {
    void applyRoute();
  });
  events.connect();
  lastFakeES().open();
  lastFakeES().epoch("boot-a", false, 5);
  await settle();
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0);
  status.polls = 0;
  rendered.series = 0;
  rendered.movies = 0;
  wire.seriesRaw = 0;
  wire.moviesRaw = 0;
  wire.series = 0;
  wire.movies = 0;
  wire.pairRows = [];
  wire.summaries.clear();
  wire.summaryCalls = [];
  wire.seriesDetailCalls = 0;
  wire.stateIDsCalls = 0;
  store.set("isUnconfigured", false);
  store.set("isAdmin", false);
  store.set("currentPage", "library");
  store.set("detailCtx", null);
  store.set("runningScansByScope", new Map());
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
  history.replaceState(null, "", HOME);
});

describe("cold deep-link boot on /series/{id}", () => {
  it("the transaction's collection leg is EMPTY: zero collection GETs, gate closed, nothing registered", async () => {
    await bootDeepLink();

    // The item resolved at item grain (the route loader's summary), never
    // through either collection — on the raw legs or the plain loader.
    expect(wire.summaryCalls).toStrictEqual([{ kind: "series", id: 42 }]);
    expect(wire.seriesRaw).toBe(0);
    expect(wire.moviesRaw).toBe(0);
    expect(wire.series).toBe(0);
    expect(wire.movies).toBe(0);

    // The transaction still committed (empty leg + page leg + status).
    expect(events._stateForTest().watermark).toBe(5);
    expect(status.polls).toBe(1);

    // The deep-link insert leaves the library incomplete: gate closed,
    // nothing registered for later collection legs.
    expect(libraryLoaded()).toBe(false);
    expect(registeredCollections().size).toBe(0);
    expect(coverageItems().map((i) => i.tvdb_id)).toStrictEqual([42]);
    expect(store.get("detailCtx")).not.toBeNull();
  });

  it("the heal gate stays closed for foreign roots — only the open root heals", async () => {
    await bootDeepLink();
    expect(wire.summaryCalls).toHaveLength(1); // the route resolution

    // A foreign root's coverage event: nothing on screen renders it, so it
    // must cost NO request (and never a collection read).
    await healFrame(99, 6);
    expect(wire.summaryCalls).toHaveLength(1);

    // The OPEN detail's own root heals: one summary GET, and the detail
    // coupling refreshes the pair (episode coverage + history ids).
    await healFrame(42, 7);
    expect(wire.summaryCalls).toHaveLength(2);
    expect(wire.summaryCalls[1]).toStrictEqual({ kind: "series", id: 42 });
    expect(wire.seriesRaw).toBe(0);
    expect(wire.series).toBe(0);
  });

  it("back-nav to the library still loads the pair via the loader", async () => {
    await bootDeepLink();
    expect(wire.series).toBe(0);

    // The user navigates back to the library (the deep-link insert left the
    // pair unloaded): the ROUTE LOADER fetches it — the load that sets
    // libraryLoaded and opens the heal gate.
    wire.pairRows = [seriesSummary(42)];
    history.replaceState(null, "", "/");
    await applyRoute();
    await settle();

    expect(wire.series).toBe(1);
    expect(wire.movies).toBe(1);
    expect(libraryLoaded()).toBe(true);
    expect([...registeredCollections()].sort()).toStrictEqual(["movies", "series"]);
  });
});
