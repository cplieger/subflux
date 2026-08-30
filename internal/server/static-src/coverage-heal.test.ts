// coverage-heal.test.ts — A6: the per-root event-heal coalescer, driven
// against the REAL coverage.ts collection/renderer, store, and bus. Only the
// wire client, the reconcile-tick provider, and detail-scan are replaced.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// Plain factories over hoisted mutable records (mockReset strips vi.fn
// implementations between tests; see coverage.test.ts's header note).
interface RawResult {
  ok: boolean;
  status: number;
  data?: unknown;
}
const wire = vi.hoisted(() => ({
  series: [] as unknown[] | null,
  movies: [] as unknown[] | null,
  collectionCalls: 0,
  // Per-root summary responses keyed `${kind}:${id}`; a root without an
  // entry answers 500 (the escalation default).
  summaries: new Map<string, { ok: boolean; status: number; data?: unknown }>(),
  summaryCalls: [] as { kind: "series" | "movie"; id: number; signal: AbortSignal | undefined }[],
  // When true, summary GETs hang until resolved by hand (or aborted — an
  // abort resolves {ok:false,status:0}, the real transport's envelope).
  defer: false,
  pending: [] as { key: string; resolve: (r: RawResult) => void }[],
}));

function summaryImpl(
  kind: "series" | "movie",
  id: string | number,
  opts?: { signal?: AbortSignal },
): Promise<RawResult> {
  wire.summaryCalls.push({ kind, id: Number(id), signal: opts?.signal });
  const key = `${kind}:${String(id)}`;
  if (wire.defer) {
    return new Promise<RawResult>((resolve) => {
      opts?.signal?.addEventListener("abort", () => {
        resolve({ ok: false, status: 0 });
      });
      wire.pending.push({ key, resolve });
    });
  }
  return Promise.resolve(wire.summaries.get(key) ?? { ok: false, status: 500 });
}

vi.mock("./wire/client.gen.js", () => ({
  coverageSeries: () => {
    wire.collectionCalls++;
    return Promise.resolve(wire.series);
  },
  coverageMovies: () => {
    wire.collectionCalls++;
    return Promise.resolve(wire.movies);
  },
  coverageSeriesSummaryRaw: (
    id: string | number,
    _q?: Record<string, unknown>,
    opts?: { signal?: AbortSignal },
  ) => summaryImpl("series", id, opts),
  coverageMovieSummaryRaw: (
    id: string | number,
    _q?: Record<string, unknown>,
    opts?: { signal?: AbortSignal },
  ) => summaryImpl("movie", id, opts),
}));

// The real module registers apiActions at import time and pulls in the
// status.ts graph; coverage.ts only consumes applyScanButtonState.
vi.mock("./detail-scan.js", () => ({ applyScanButtonState: () => undefined }));

// The reconcile tick lives in status.ts; capture registrations so tests fire
// ticks by hand (the tick's own cadence/pause is pinned in status.test.ts).
const reconcile = vi.hoisted(() => ({ tasks: [] as (() => void)[] }));
vi.mock("./status.js", () => ({
  registerReconcileTask: (fn: () => void) => {
    reconcile.tasks.push(fn);
    return () => undefined;
  },
}));

import * as store from "./store.js";
import { on, BusEvent } from "./bus.js";
import { SUMMARY_COALESCE_MS, DIRTY_ROOT_CAP } from "./constants.js";
import {
  _resetHealForTest,
  healFromCoverageEvent,
  onHealReset,
  parseCoverageMediaId,
  resetCoverageHeal,
  subsumeDirtyRoots,
} from "./coverage-heal.js";
import {
  _resetCoverageForTest,
  coverageRow,
  fetchAndMergeCoverage,
  filterCoverage,
  libraryLoaded,
  loadCoverage,
  registeredCollections,
} from "./coverage.js";
import type { CoverageEvent, CoverageTarget, MediaType } from "./wire/types.gen.js";
import type { CoverageItem } from "./api-types.js";

// --- Fixtures: full wire shapes (the real summary endpoint payloads) ---

function target(
  language: string,
  have: number,
  total: number,
  have_ignored = 0,
  variant = "standard",
): CoverageTarget {
  return { language, variant, have, total, have_ignored };
}

function seriesWire(tvdbId: number, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    title: `Show ${tvdbId}`,
    imdb_id: "tt0100",
    first_aired: "2020-01-05",
    audio_lang: "en",
    rule: "en",
    targets: [target("en", 1, 3)],
    tags: [2],
    id: 1000 + tvdbId,
    year: 2020,
    tvdb_id: tvdbId,
    episodes: 3,
    excluded: false,
    ...extra,
  };
}

function movieWire(tmdbId: number, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    title: `Film ${tmdbId}`,
    imdb_id: "tt0200",
    scene_name: "Film.2021.1080p",
    in_cinemas: "2021-03-01",
    digital_release: "2021-06-01",
    audio_lang: "en",
    rule: "en",
    targets: [target("en", 0, 1)],
    tags: [3],
    tmdb_id: tmdbId,
    id: 2000 + tmdbId,
    year: 2021,
    has_file: true,
    excluded: false,
    ...extra,
  };
}

function ev(mediaId: string, mediaType: MediaType = "episode"): CoverageEvent {
  return {
    media_type: mediaType,
    media_id: mediaId,
    language: "en",
    variant: "standard",
    source: "opensubtitles",
  };
}

function okRes(data: unknown): { ok: boolean; status: number; data?: unknown } {
  return { ok: true, status: 200, data };
}

const FIXTURE = `
<section class="card" id="coveragePanel">
  <div class="card-head" hidden>
    <h2 id="lib-heading">Library</h2>
    <div class="controls">
      <input type="checkbox" id="cov-missing">
      <select id="cov-type-filter">
        <option value="all">All</option>
        <option value="series">Series</option>
        <option value="movies">Movies</option>
      </select>
      <select id="cov-sort">
        <option value="title">A-Z</option>
      </select>
      <input id="cov-filter" type="search">
    </div>
  </div>
  <div id="coverageContent"></div>
</section>`;

/** Land a pair through the real route-loader read (mounts the table). */
async function load(
  seriesRows: Record<string, unknown>[],
  movieRows: Record<string, unknown>[] = [],
): Promise<void> {
  wire.series = seriesRows;
  wire.movies = movieRows;
  await loadCoverage();
}

/** One coalescer window: fires the trailing flush and drains its microtasks. */
async function window_(): Promise<void> {
  await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
}

function fireReconcileTick(): void {
  for (const task of reconcile.tasks) {
    task();
  }
}

function rowEls(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>("table.library tbody tr"));
}

function rowTitles(): string[] {
  return rowEls().map((r) => r.querySelector('[data-col="title"]')?.textContent ?? "");
}

beforeEach(() => {
  vi.useFakeTimers();
  wire.series = [];
  wire.movies = [];
  wire.collectionCalls = 0;
  wire.summaries.clear();
  wire.summaryCalls = [];
  wire.defer = false;
  wire.pending = [];
  _resetHealForTest();
  _resetCoverageForTest();
  document.body.innerHTML = FIXTURE;
  store.set("currentPage", "library");
  store.set("detailCtx", null);
  filterCoverage();
});

afterEach(() => {
  vi.useRealTimers();
});

// --- The parser ---

describe("coverage-heal: parser", () => {
  it("maps an episode id onto its series root", () => {
    expect(parseCoverageMediaId("tvdb-81189-s01e05", "episode")).toEqual({
      kind: "series",
      numericID: 81189,
      rootKey: "tvdb-81189",
    });
  });

  it("maps a series-level id onto the same root", () => {
    expect(parseCoverageMediaId("tvdb-81189", "episode")).toEqual({
      kind: "series",
      numericID: 81189,
      rootKey: "tvdb-81189",
    });
  });

  it("maps a movie id onto its movie root", () => {
    expect(parseCoverageMediaId("tmdb-550", "movie")).toEqual({
      kind: "movie",
      numericID: 550,
      rootKey: "tmdb-550",
    });
  });

  const rejected: [string, string, string][] = [
    ["malformed", "garbage", "episode"],
    ["malformed empty", "", "episode"],
    ["malformed trailing dash", "tvdb-", "episode"],
    ["malformed negative", "tvdb--5", "episode"],
    ["zero series id", "tvdb-0", "episode"],
    ["zero movie id", "tmdb-0", "movie"],
    ["zero id with episode marker", "tvdb-0-s01e01", "episode"],
    ["leading-zero series id", "tvdb-081189", "episode"],
    ["leading-zero movie id", "tmdb-007", "movie"],
    ["imdb fallback movie", "tt0903747", "movie"],
    ["imdb fallback episode", "tt0903747-s01e05", "episode"],
    ["type mismatch: tvdb id on a movie event", "tvdb-5", "movie"],
    ["type mismatch: tmdb id on an episode event", "tmdb-5", "episode"],
    ["type mismatch: series media_type", "tvdb-5", "series"],
    ["episode marker on a movie id", "tmdb-5-s01e01", "movie"],
  ];
  for (const [name, mediaId, mediaType] of rejected) {
    it(`rejects ${name}`, () => {
      expect(parseCoverageMediaId(mediaId, mediaType)).toBeNull();
    });
  }

  it("a rejected id makes NO request even with the gate open", async () => {
    await load([seriesWire(1)]);
    wire.summaryCalls = [];

    for (const [, mediaId, mediaType] of rejected) {
      healFromCoverageEvent(ev(mediaId, mediaType as MediaType));
    }
    await window_();
    await window_();

    expect(wire.summaryCalls).toHaveLength(0);
  });
});

// --- The gate ---

describe("coverage-heal: gate", () => {
  it("an event with the gate closed makes NO request", async () => {
    // Nothing loaded, no detail open.
    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();
    await window_();

    expect(wire.summaryCalls).toHaveLength(0);
  });

  it("a /history session opens the gate via the library route loader (mid-session)", async () => {
    store.set("currentPage", "history");
    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();
    expect(wire.summaryCalls).toHaveLength(0);
    expect(libraryLoaded()).toBe(false);

    // Navigating to the library runs the route loader's plain pair read;
    // landing it sets libraryLoaded, registers the pair, opens this gate.
    store.set("currentPage", "library");
    await load([seriesWire(1)]);
    expect(libraryLoaded()).toBe(true);
    expect([...registeredCollections()].sort()).toEqual(["movies", "series"]);

    wire.summaries.set("series:1", okRes(seriesWire(1, { targets: [target("en", 2, 3)] })));
    healFromCoverageEvent(ev("tvdb-1-s01e02"));
    await window_();

    expect(wire.summaryCalls).toHaveLength(1);
    expect(coverageRow("tvdb-1")?.targets).toEqual([target("en", 2, 3)]);
  });

  it("an open detail admits its own root while the library is unloaded", async () => {
    store.set("detailCtx", { movie: true, tmdbId: 9 });
    wire.summaries.set("movie:9", okRes(movieWire(9)));

    healFromCoverageEvent(ev("tmdb-9", "movie"));
    // A foreign root stays gated: nothing on screen renders it.
    healFromCoverageEvent(ev("tvdb-4-s01e01"));
    await window_();

    expect(wire.summaryCalls).toEqual([
      expect.objectContaining({ kind: "movie", id: 9 }) as unknown,
    ]);
    // Upsert-into-incomplete: the row lands, the gate stays closed.
    expect(coverageRow("tmdb-9")?.title).toBe("Film 9");
    expect(libraryLoaded()).toBe(false);
  });
});

// --- The coalescer ---

describe("coverage-heal: coalescer", () => {
  /** applyFilters reads #cov-filter exactly once per run, so counting that
   *  lookup counts derives of the filtered+sorted view. */
  function countDerives(): () => number {
    const byId = vi.spyOn(document, "getElementById");
    return () => byId.mock.calls.filter(([id]) => id === "cov-filter").length;
  }

  it("a synchronous 200-event burst across 3 roots costs 3 GETs, one flush, one derive", async () => {
    await load([seriesWire(1), seriesWire(2)], [movieWire(3)]);
    wire.summaries.set("series:1", okRes(seriesWire(1, { targets: [target("en", 3, 3)] })));
    wire.summaries.set("series:2", okRes(seriesWire(2, { targets: [target("en", 3, 3)] })));
    wire.summaries.set("movie:3", okRes(movieWire(3, { targets: [target("en", 1, 1)] })));
    const collectionCallsAfterLoad = wire.collectionCalls;
    const derives = countDerives();

    for (let i = 0; i < 200; i++) {
      const root = (i % 3) + 1;
      healFromCoverageEvent(
        root === 3 ? ev("tmdb-3", "movie") : ev(`tvdb-${root}-s01e${String((i % 9) + 1)}`),
      );
    }
    // Trailing: nothing is fetched inside the window.
    expect(wire.summaryCalls).toHaveLength(0);
    await window_();

    // ≤ k first-attempt GETs for k distinct roots — here exactly k.
    expect(wire.summaryCalls).toHaveLength(3);
    // Zero full-collection GETs: the heal path never touches the pair.
    expect(wire.collectionCalls).toBe(collectionCallsAfterLoad);
    // One batch per flush: the filtered view derived once for 3 row writes.
    expect(derives()).toBe(1);
    // And the rows actually healed.
    expect(coverageRow("tvdb-1")?.targets).toEqual([target("en", 3, 3)]);
    expect(coverageRow("tmdb-3")?.targets).toEqual([target("en", 1, 1)]);
  });

  it("movie and series roots heal through the SAME path in one flush", async () => {
    await load([seriesWire(1)], [movieWire(2)]);
    wire.summaries.set("series:1", okRes(seriesWire(1, { title: "Show 1 renamed" })));
    wire.summaries.set("movie:2", okRes(movieWire(2, { title: "Film 2 renamed" })));

    healFromCoverageEvent(ev("tvdb-1-s02e04"));
    healFromCoverageEvent(ev("tmdb-2", "movie"));
    await window_();

    expect(wire.summaryCalls.map((c) => c.kind).sort()).toEqual(["movie", "series"]);
    expect(rowTitles().sort()).toEqual(["Film 2 renamed", "Show 1 renamed"]);
  });

  it("a window-spanning stream costs one GET per root per window plus the trailing heal", async () => {
    await load([seriesWire(1)]);
    wire.summaries.set("series:1", okRes(seriesWire(1, { targets: [target("en", 2, 3)] })));

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS / 2);
    healFromCoverageEvent(ev("tvdb-1-s01e02")); // joins the open window
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS / 2); // flush 1
    expect(wire.summaryCalls).toHaveLength(1);

    healFromCoverageEvent(ev("tvdb-1-s01e03")); // opens the next window
    await window_(); // the trailing heal
    expect(wire.summaryCalls).toHaveLength(2);

    // Quiet stream: no further requests.
    await window_();
    await window_();
    expect(wire.summaryCalls).toHaveLength(2);
  });

  it("aborts a delayed older response — the latest GET wins", async () => {
    await load([seriesWire(1)]);
    wire.defer = true;

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_(); // GET 1 in flight
    healFromCoverageEvent(ev("tvdb-1-s01e02"));
    await window_(); // GET 2 dispatched, GET 1 superseded

    expect(wire.summaryCalls).toHaveLength(2);
    expect(wire.summaryCalls[0]?.signal?.aborted).toBe(true);
    expect(wire.summaryCalls[1]?.signal?.aborted).toBe(false);

    // The newer response lands; the older (already-settled-by-abort) resolve
    // is a no-op even when the transport answers late with stale data.
    wire.pending[1]?.resolve(okRes(seriesWire(1, { title: "Fresh" })));
    wire.pending[0]?.resolve(okRes(seriesWire(1, { title: "Stale" })));
    await vi.advanceTimersByTimeAsync(0);

    expect(coverageRow("tvdb-1")?.title).toBe("Fresh");
    expect(rowTitles()).toEqual(["Fresh"]);
    // The superseded outcome neither re-enqueues nor dirties: no more GETs.
    await window_();
    expect(wire.summaryCalls).toHaveLength(2);
  });

  it("a 404 DELETES the row", async () => {
    await load([seriesWire(1), seriesWire(2)]);
    wire.summaries.set("series:1", { ok: false, status: 404 });

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();

    expect(coverageRow("tvdb-1")).toBeUndefined();
    expect(rowTitles()).toEqual(["Show 2"]);
  });
});

// --- Failure escalation ---

describe("coverage-heal: failure escalation", () => {
  it("a failed GET re-enqueues once, then joins the dirty set and converges at a tick (live stream)", async () => {
    await load([seriesWire(1)]);
    const collectionCallsAfterLoad = wire.collectionCalls;
    // No summary entry → 502-shaped failure (the mock's 500 default).
    wire.summaries.set("series:1", { ok: false, status: 502 });

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_(); // attempt 0 fails → re-enqueued
    expect(wire.summaryCalls).toHaveLength(1);
    await window_(); // attempt 1 fails → dirty
    expect(wire.summaryCalls).toHaveLength(2);

    // No third automatic retry, however long the stream stays quiet.
    for (let i = 0; i < 10; i++) {
      await window_();
    }
    expect(wire.summaryCalls).toHaveLength(2);

    // The reconcile tick retries the dirty root; success converges WITHOUT a
    // reconnect (the SSE stream never dropped, no full-collection refetch).
    wire.summaries.set("series:1", okRes(seriesWire(1, { title: "Healed" })));
    fireReconcileTick();
    await window_();
    expect(wire.summaryCalls).toHaveLength(3);
    expect(coverageRow("tvdb-1")?.title).toBe("Healed");
    expect(wire.collectionCalls).toBe(collectionCallsAfterLoad);

    // Converged: the next tick has nothing to retry.
    fireReconcileTick();
    await window_();
    expect(wire.summaryCalls).toHaveLength(3);
  });

  it("caps the dirty set at 64 roots, dropping the oldest", async () => {
    await load([seriesWire(999)]);
    const total = DIRTY_ROOT_CAP + 1;

    for (let i = 1; i <= total; i++) {
      healFromCoverageEvent(ev(`tvdb-${i}-s01e01`));
    }
    await window_(); // first attempts fail (500 default)
    await window_(); // retries fail → all dirty; root 1 dropped at the cap
    expect(wire.summaryCalls).toHaveLength(2 * total);

    wire.summaryCalls = [];
    fireReconcileTick();
    await window_();

    const retried = wire.summaryCalls.map((c) => c.id);
    expect(retried).toHaveLength(DIRTY_ROOT_CAP);
    expect(retried).not.toContain(1); // the oldest was dropped
    expect(retried).toContain(2);
    expect(retried).toContain(total);
  });

  it("a committing transaction subsumes the dirty set (task 9 seam)", async () => {
    await load([seriesWire(1)]);
    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();
    await window_(); // twice failed → dirty (500 default)
    expect(wire.summaryCalls).toHaveLength(2);

    subsumeDirtyRoots();

    fireReconcileTick();
    await window_();
    expect(wire.summaryCalls).toHaveLength(2); // nothing left to retry
  });

  it("detail-coupled dirty entries clear on route leave", async () => {
    // Library never loaded: the movie's own open detail is what admits it.
    store.set("detailCtx", { movie: true, tmdbId: 7 });

    healFromCoverageEvent(ev("tmdb-7", "movie"));
    await window_();
    await window_(); // twice failed → dirty, scoped to the detail
    expect(wire.summaryCalls).toHaveLength(2);

    store.set("detailCtx", null); // route leave

    fireReconcileTick();
    await window_();
    expect(wire.summaryCalls).toHaveLength(2); // nothing left to retry
  });

  it("library-scoped dirty entries survive a route change", async () => {
    await load([seriesWire(1)]);
    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();
    await window_(); // dirty (500 default)

    store.set("detailCtx", { movie: true, tmdbId: 3 });
    store.set("detailCtx", null);

    wire.summaries.set("series:1", okRes(seriesWire(1, { title: "Persisted" })));
    fireReconcileTick();
    await window_();
    expect(coverageRow("tvdb-1")?.title).toBe("Persisted");
  });
});

// --- Detail couplings ---

describe("coverage-heal: detail couplings", () => {
  it("a series event with that detail open runs the refresh pair once per window", async () => {
    await load([seriesWire(1)]);
    const s = coverageRow("tvdb-1");
    if (!s) {
      throw new Error("row missing");
    }
    store.set("detailCtx", { series: s as never, seasons: [], tvdbId: 1 });
    wire.summaries.set("series:1", okRes(seriesWire(1, { targets: [target("en", 2, 3)] })));
    const invalidates: number[] = [];
    const off = on(BusEvent.DataInvalidate, () => invalidates.push(1));

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    healFromCoverageEvent(ev("tvdb-1-s01e02"));
    healFromCoverageEvent(ev("tvdb-1-s01e03"));
    await window_();
    expect(invalidates).toHaveLength(1);

    healFromCoverageEvent(ev("tvdb-1-s01e04"));
    await window_();
    expect(invalidates).toHaveLength(2);
    off();
  });

  it("a movie event with that detail open re-opens the detail from the healed row", async () => {
    await load([], [movieWire(2)]);
    store.set("detailCtx", { movie: true, tmdbId: 2 });
    wire.summaries.set("movie:2", okRes(movieWire(2, { targets: [target("fr", 0, 1)] })));
    const opened: { item: CoverageItem; skipPush?: boolean }[] = [];
    const off = on(BusEvent.OpenMovie, (p) => opened.push(p as never));

    healFromCoverageEvent(ev("tmdb-2", "movie"));
    healFromCoverageEvent(ev("tmdb-2", "movie"));
    await window_();

    // Once per window, from the FRESH row (the /subs + state/ids reads are
    // openMovieDetail's own), and without a history push.
    expect(opened).toHaveLength(1);
    expect(opened[0]?.skipPush).toBe(true);
    expect(opened[0]?.item.targets).toEqual([target("fr", 0, 1)]);
    off();
  });

  it("a foreign detail open couples nothing", async () => {
    await load([seriesWire(1)], [movieWire(2)]);
    store.set("detailCtx", { movie: true, tmdbId: 2 });
    wire.summaries.set("series:1", okRes(seriesWire(1, { title: "X" })));
    const invalidates: number[] = [];
    const opened: number[] = [];
    const offA = on(BusEvent.DataInvalidate, () => invalidates.push(1));
    const offB = on(BusEvent.OpenMovie, () => opened.push(1));

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();

    expect(invalidates).toHaveLength(0);
    expect(opened).toHaveLength(0);
    offA();
    offB();
  });
});

// --- The reset rule ---

describe("coverage-heal: reset rule", () => {
  it("a route-loader overwrite aborts in-flight heals, clears the window, re-arms the seam", async () => {
    await load([seriesWire(1), seriesWire(2)]);
    wire.defer = true;
    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_(); // GET for tvdb-1 in flight
    healFromCoverageEvent(ev("tvdb-2-s01e01")); // queued in the next window
    const rearm = vi.fn();
    const off = onHealReset(rearm);

    wire.defer = false;
    wire.series = [seriesWire(1, { title: "From the pair" }), seriesWire(2)];
    wire.movies = [];
    await loadCoverage(true); // the overwrite runs the reset rule

    expect(wire.summaryCalls).toHaveLength(1);
    expect(wire.summaryCalls[0]?.signal?.aborted).toBe(true); // in-flight aborted
    expect(rearm).toHaveBeenCalledTimes(1); // task 12's latch re-arm seam

    // The pending window was cleared: no GET for tvdb-2 ever fires, and the
    // aborted root's outcome applies nothing over the fresh snapshot.
    await window_();
    await window_();
    expect(wire.summaryCalls).toHaveLength(1);
    expect(coverageRow("tvdb-1")?.title).toBe("From the pair");
    off();
  });

  it("resetCoverageHeal is directly callable (the task-9 transaction seam)", async () => {
    await load([seriesWire(1)]);
    healFromCoverageEvent(ev("tvdb-1-s01e01"));

    resetCoverageHeal();
    await window_();
    await window_();

    expect(wire.summaryCalls).toHaveLength(0);
  });
});

// --- R7.1 parity ---

describe("coverage-heal: R7.1 parity", () => {
  // Same v1 → v2 inputs, one row through the heal path and one through the
  // full-refetch path, byte-identical rendered rows. Every discriminating
  // field differs between v1 and v2: title, rule, targets, excluded, and the
  // episode count.
  const seriesV2 = (): Record<string, unknown> =>
    seriesWire(1, {
      title: "Show 1 renamed",
      rule: "fr",
      targets: [target("fr", 2, 4), target("de", 0, 4)],
      excluded: true,
      episodes: 9,
    });

  it("a healed series row renders byte-identical to a full-refetch row", async () => {
    // Heal path.
    await load([seriesWire(1)]);
    wire.summaries.set("series:1", okRes(seriesV2()));
    healFromCoverageEvent(ev("tvdb-1-s01e02"));
    await window_();
    const healed = rowEls()[0]?.outerHTML;
    expect(coverageRow("tvdb-1")?.excluded).toBe(true);

    // Full-refetch path, from the same v1 mount.
    _resetHealForTest();
    _resetCoverageForTest();
    document.body.innerHTML = FIXTURE;
    filterCoverage();
    await load([seriesWire(1)]);
    wire.series = [seriesV2()];
    await loadCoverage(true);
    const refetched = rowEls()[0]?.outerHTML;

    expect(healed).toBeDefined();
    expect(healed).toBe(refetched);
  });

  it("a healed movie row renders byte-identical to a full-refetch row", async () => {
    const movieV2 = (): Record<string, unknown> =>
      movieWire(2, {
        title: "Film 2 renamed",
        rule: "fr",
        targets: [target("fr", 1, 1)],
        excluded: true,
      });

    await load([], [movieWire(2)]);
    wire.summaries.set("movie:2", okRes(movieV2()));
    healFromCoverageEvent(ev("tmdb-2", "movie"));
    await window_();
    const healed = rowEls()[0]?.outerHTML;

    _resetHealForTest();
    _resetCoverageForTest();
    document.body.innerHTML = FIXTURE;
    filterCoverage();
    await load([], [movieWire(2)]);
    wire.movies = [movieV2()];
    await loadCoverage(true);
    const refetched = rowEls()[0]?.outerHTML;

    expect(healed).toBeDefined();
    expect(healed).toBe(refetched);
  });

  it("a signature-equal heal repaints nothing (identity preserved through the heal path)", async () => {
    await load([seriesWire(1)]);
    const rowBefore = rowEls()[0];
    const cellsBefore = Array.from(rowBefore?.children ?? []);
    const cur = coverageRow("tvdb-1");
    wire.summaries.set("series:1", okRes(seriesWire(1))); // fresh object, equal content

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();

    expect(coverageRow("tvdb-1")).toBe(cur); // current object kept
    const cellsAfter = Array.from(rowEls()[0]?.children ?? []);
    expect(cellsAfter).toHaveLength(cellsBefore.length);
    for (const [i, cell] of cellsAfter.entries()) {
      expect(cell).toBe(cellsBefore[i]); // no repaint
    }
  });
});

// --- fetchAndMergeCoverage keeps its loader/transaction shape ---

describe("coverage-heal: full-pair reads stay legal in exactly two places", () => {
  it("the heal path performs zero full-collection GETs across every outcome", async () => {
    await load([seriesWire(1)]);
    const after = wire.collectionCalls;
    wire.summaries.set("series:1", { ok: false, status: 502 });

    healFromCoverageEvent(ev("tvdb-1-s01e01"));
    await window_();
    await window_(); // fail → retry → dirty
    fireReconcileTick();
    await window_(); // tick retry

    expect(wire.collectionCalls).toBe(after);
  });

  it("the loader/transaction read is fetchAndMergeCoverage, unchanged in shape", async () => {
    wire.series = [seriesWire(1)];
    wire.movies = [movieWire(2)];

    const merged = await fetchAndMergeCoverage();

    expect(merged.map((i) => `${i._type}:${String(i.tvdb_id ?? i.tmdb_id)}`)).toEqual([
      "series:1",
      "movie:2",
    ]);
  });
});
