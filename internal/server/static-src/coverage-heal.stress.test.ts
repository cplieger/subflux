// coverage-heal.stress.test.ts — task 19's STRESS LANE for the A6 heal
// coalescer at the REFERENCE library (500 series / 4,360 movies from
// reference-fixture.ts): a 200-event synchronous burst OUTSIDE transactions
// against the real coverage.ts collection/renderer, store, and bus. Same
// harness shape as coverage-heal.test.ts (only the wire client, the
// reconcile-tick provider, and detail-scan are replaced); the burst cost
// bounds are asserted, the main-thread times are recorded as evidence
// (fake timers exclude `performance`, so performance.now() is real).
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

interface RawResult {
  ok: boolean;
  status: number;
  data?: unknown;
}
const wire = vi.hoisted(() => ({
  series: [] as unknown[] | null,
  movies: [] as unknown[] | null,
  collectionCalls: 0,
  summaries: new Map<string, { ok: boolean; status: number; data?: unknown }>(),
  summaryCalls: [] as { kind: "series" | "movie"; id: number }[],
}));

function summaryImpl(kind: "series" | "movie", id: string | number): Promise<RawResult> {
  wire.summaryCalls.push({ kind, id: Number(id) });
  const key = `${kind}:${String(id)}`;
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
  coverageSeriesSummaryRaw: (id: string | number) => summaryImpl("series", id),
  coverageMovieSummaryRaw: (id: string | number) => summaryImpl("movie", id),
}));
vi.mock("./detail-scan.js", () => ({ registerScanButton: () => undefined }));
vi.mock("./status.js", () => ({
  registerReconcileTask: () => () => undefined,
}));

import * as store from "./store.js";
import { SUMMARY_COALESCE_MS } from "./constants.js";
import { _resetHealForTest, healFromCoverageEvent } from "./coverage-heal.js";
import { _resetCoverageForTest, filterCoverage, loadCoverage } from "./coverage.js";
import { coverageRow } from "./coverage-store.js";
import { refMovieWire, refSeriesWire } from "./reference-fixture.js";
import type { CoverageEvent, MediaType, MovieItem, SeriesItem } from "./wire/types.gen.js";

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

function ev(mediaId: string, mediaType: MediaType = "episode"): CoverageEvent {
  return {
    media_type: mediaType,
    media_id: mediaId,
    language: "en",
    variant: "standard",
    source: "opensubtitles",
  };
}

function visibleRows(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>("table.library tbody tr"));
}

/** covId → first cell node, the repaint detector (updateCoverageRow rebuilds
 *  every cell of a changed row; an unchanged row keeps its nodes). */
function cellIdentity(): Map<string, Element> {
  const map = new Map<string, Element>();
  for (const row of visibleRows()) {
    const covId = row.querySelector<HTMLElement>("td[data-cov-id]")?.dataset["covId"];
    const first = row.firstElementChild;
    if (covId !== undefined && first !== null) {
      map.set(covId, first);
    }
  }
  return map;
}

describe("coverage-heal stress: 200-event burst at the reference library", () => {
  beforeEach(() => {
    // Fake the timers the coalescer uses; leave `performance` REAL so the
    // recorded main-thread times are wall time.
    vi.useFakeTimers({
      toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"],
    });
    wire.series = [];
    wire.movies = [];
    wire.collectionCalls = 0;
    wire.summaries.clear();
    wire.summaryCalls = [];
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

  it(
    "costs ≤k first-attempt GETs, zero collection GETs, one derive, ≤1 repaint per healed row",
    { timeout: 30_000 },
    async () => {
      const seriesWire = refSeriesWire();
      const movieWire = refMovieWire();
      wire.series = seriesWire;
      wire.movies = movieWire;

      const loadStart = performance.now();
      await loadCoverage();
      const loadMs = performance.now() - loadStart;
      const collectionCallsAfterLoad = wire.collectionCalls;

      // The visible page: first 50 of 4,860 sorted by title — the movie rows
      // 0000..0049 (movies sort before series). Heal two VISIBLE movies and
      // one off-page series, so both DOM repaint accounting and the
      // collection-only update path are covered.
      const rowsBefore = cellIdentity();
      expect(rowsBefore.size).toBe(50);
      expect(rowsBefore.has("tmdb-500001")).toBe(true);
      expect(rowsBefore.has("tmdb-500050")).toBe(true);

      const healedMovieA = { ...(movieWire[0] as MovieItem) };
      healedMovieA.targets = healedMovieA.targets.map((t) => ({ ...t, have: t.total }));
      const healedMovieB = { ...(movieWire[49] as MovieItem) };
      healedMovieB.targets = healedMovieB.targets.map((t) => ({ ...t, have: t.total }));
      const healedSeries = {
        ...(seriesWire[0] as SeriesItem),
        title: "Reference Series 0000 (healed)",
      };
      wire.summaries.set("movie:500001", { ok: true, status: 200, data: healedMovieA });
      wire.summaries.set("movie:500050", { ok: true, status: 200, data: healedMovieB });
      wire.summaries.set("series:100001", { ok: true, status: 200, data: healedSeries });

      // applyFilters reads #cov-filter exactly once per run: counting that
      // lookup counts derives of the filtered+sorted 4,860-row view.
      const byId = vi.spyOn(document, "getElementById");
      const derives = (): number => byId.mock.calls.filter(([id]) => id === "cov-filter").length;

      // THE BURST: 200 synchronous events round-robin across k=3 roots,
      // outside any transaction.
      const roots = ["tmdb-500001", "tmdb-500050", "tvdb-100001"] as const;
      const enqueueStart = performance.now();
      for (let i = 0; i < 200; i++) {
        const root = roots[i % 3];
        if (root === "tvdb-100001") {
          healFromCoverageEvent(ev(`tvdb-100001-s01e${String((i % 9) + 1)}`));
        } else {
          healFromCoverageEvent(ev(root as string, "movie"));
        }
      }
      const enqueueMs = performance.now() - enqueueStart;

      // Trailing coalescer: nothing fetched inside the window.
      expect(wire.summaryCalls).toHaveLength(0);

      const flushStart = performance.now();
      await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
      const flushMs = performance.now() - flushStart;

      // ≤ k FIRST-ATTEMPT summary GETs for k=3 distinct roots — here exactly
      // k, one per root (belt/dirty retries would be extra calls; all three
      // summaries succeed, so none happen — pinned below).
      expect(wire.summaryCalls).toHaveLength(3);
      expect(wire.summaryCalls.map((c) => `${c.kind}:${String(c.id)}`).sort()).toEqual([
        "movie:500001",
        "movie:500050",
        "series:100001",
      ]);
      // Zero full-collection GETs: the heal path never touches the pair.
      expect(wire.collectionCalls).toBe(collectionCallsAfterLoad);
      // One batch per flush: the 4,860-row view derived exactly once.
      expect(derives()).toBe(1);

      // ≤1 row repaint per event, and only the HEALED rows repaint: the two
      // healed visible rows got fresh cells, every other visible row kept
      // node identity.
      const rowsAfter = cellIdentity();
      let repainted = 0;
      for (const [covId, cell] of rowsAfter) {
        const before = rowsBefore.get(covId);
        expect(before).toBeDefined();
        if (before !== cell) {
          repainted++;
          expect(["tmdb-500001", "tmdb-500050"]).toContain(covId);
        }
      }
      expect(repainted).toBe(2);

      // The heals actually landed (the off-page series row updated in the
      // collection without a DOM repaint).
      expect(coverageRow("tmdb-500001")?.targets.every((t) => t.have === t.total)).toBe(true);
      expect(coverageRow("tvdb-100001")?.title).toBe("Reference Series 0000 (healed)");

      // No belt/dirty retries follow a clean burst: two quiet windows later
      // the count is unchanged (first-attempt GETs were the whole cost).
      await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 2);
      expect(wire.summaryCalls).toHaveLength(3);

      // EVIDENCE (recorded, not gated — the sanity bounds are generous):
      console.warn(
        `[stress] reference-library burst: load(4860 rows)=${loadMs.toFixed(1)}ms ` +
          `enqueue(200 events)=${enqueueMs.toFixed(2)}ms ` +
          `flush(coalesce+3 heals+derive+repaint)=${flushMs.toFixed(1)}ms`,
      );
      expect(enqueueMs).toBeLessThan(1000);
      expect(flushMs).toBeLessThan(3000);
    },
  );
});
