// @vitest-environment happy-dom
import { describe, it, vi, beforeEach, expect } from "vitest";

// CRITICAL: vitest.config has clearMocks/mockReset/restoreMocks=true, which
// strips a vi.fn's implementation before each test. Any mock whose behavior
// must persist across tests (resolved values, factory shapes, no-op handlers
// called at module load) MUST be a PLAIN function, not a vi.fn().
vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: () => Promise.resolve(null),
  coverageSeriesDetail: () => Promise.resolve([]),
  stateIDs: () => Promise.resolve(null),
}));
vi.mock("@cplieger/actions", () => ({ registerCleanup: () => undefined }));
vi.mock("./bus.js", () => ({
  on: () => () => undefined,
  emit: () => undefined,
  BusEvent: {
    PanelConfigure: "panel:configure",
    NavHistory: "nav:history",
    OpenSeries: "open:series",
    OpenMovie: "open:movie",
    ScanSeries: "scan:series",
    ScanMovie: "scan:movie",
  },
}));
vi.mock("./search.js", () => ({ openSearchPopup: () => undefined }));
vi.mock("./sync.js", () => ({ openSyncDialog: () => undefined }));
vi.mock("./files.js", () => ({ openFileManager: () => undefined }));
// detail.ts imports openConfig (for the no-targets empty state); mock the
// whole module so its transitive graph (status.ts actions) stays out.
vi.mock("./config.js", () => ({ openConfig: () => undefined }));
vi.mock("./detail-scan.js", () => ({
  triggerSeriesScan: () => undefined,
  triggerSeasonScan: () => undefined,
  triggerMovieScan: () => undefined,
  applyScanButtonState: () => undefined,
}));
vi.mock("./detail-season-sync.js", () => ({ confirmSeasonSync: () => undefined }));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => {
    if (k === "ignoredCodecs") {
      return new Set<string>();
    }
    if (k === "isAdmin") {
      return false;
    }
    return null;
  },
  set: () => undefined,
}));

import { renderSeriesDetail, openMovieDetail } from "./detail.js";
import type { SeriesItem, SeasonGroup, SubtitleEntry, MovieDetail } from "./api-types.js";

const STAR = "\u2605"; // ★ — score prefix in coverage badge detail
const DASH = "\u2014"; // — — empty coverage badge

// --- Fixtures (hardcoded, DAMP) ---

function makeSeries(tvdbId: number, title: string): SeriesItem {
  return {
    title,
    audio_lang: "en",
    rule: "en",
    id: tvdbId,
    year: 2020,
    tvdb_id: tvdbId,
    episodes: 3,
    targets: [{ language: "en", variant: "standard", have: 0, total: 3, have_ignored: 0 }],
  };
}

function makeSeasons(t1: string, t2: string, t3: string): SeasonGroup[] {
  return [
    {
      season: 1,
      episodes: [
        { id: 101, season: 1, episode: 1, title: t1, has_file: true },
        { id: 102, season: 1, episode: 2, title: t2, has_file: true },
      ],
    },
    {
      season: 2,
      episodes: [
        { id: 201, season: 2, episode: 1, title: t3, has_file: true },
      ],
    },
  ];
}

function epSub(mediaId: string, score: number, ordinal = 0): SubtitleEntry {
  return {
    media_id: mediaId,
    language: "en",
    variant: "standard",
    source: "external",
    codec: "srt",
    score,
    ordinal,
  };
}

function makeMovie(tmdbId: number, subs: SubtitleEntry[]): MovieDetail {
  return {
    title: `Movie ${tmdbId}`,
    audio_lang: "en",
    rule: "en",
    targets: [
      { language: "en", variant: "standard", have: 0, total: 1, have_ignored: 0 },
      { language: "fr", variant: "standard", have: 0, total: 1, have_ignored: 0 },
    ],
    subs,
    tmdb_id: tmdbId,
    id: tmdbId,
    year: 2021,
    has_file: true,
  };
}

function movieSub(language: string, score: number, ordinal = 0): SubtitleEntry {
  return {
    media_id: "tmdb-50",
    language,
    variant: "standard",
    source: "external",
    codec: "srt",
    score,
    ordinal,
  };
}

function seriesTbody(): HTMLTableSectionElement {
  const tb = document.querySelector<HTMLTableSectionElement>("table.series-detail tbody");
  if (!tb) {
    throw new Error("series tbody not mounted");
  }
  return tb;
}

function movieTbody(): HTMLTableSectionElement {
  const tb = document.querySelector<HTMLTableSectionElement>("table.movie-detail tbody");
  if (!tb) {
    throw new Error("movie tbody not mounted");
  }
  return tb;
}

function covText(row: Element | null): string {
  if (!(row instanceof HTMLElement)) {
    throw new Error("row missing");
  }
  return row.querySelector("td.ep-coverage")?.textContent ?? "";
}

describe("detail: renderSeriesDetail", () => {
  beforeEach(() => {
    // The real dom.js `$.coverageContent` getter reads #coverageContent; the
    // Files button (admin-gated, off here) targets #coveragePanel .card-head.
    // Wiping innerHTML between tests detaches any previously-bound <tbody>, so
    // the isConnected/contains guard correctly forces a REBUILD next render.
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("initial render builds season heads, column headers, and episode rows", () => {
    const series = makeSeries(100, "Show A");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    const subs = [epSub("tvdb-100-s01e01", 80)];

    renderSeriesDetail(series, seasons, subs, new Set());

    const tbody = seriesTbody();
    // S1 -> head, cols, ep01, ep02 ; S2 -> gap, head, cols, ep01 = 8 rows.
    expect(tbody.children.length).toBe(8);
    // children[2] is S01E01, which has one en external srt @ score 80.
    expect(covText(tbody.children.item(2))).toBe(`ensrt: ext ${STAR}80`);
    // children[3] is S01E02, uncovered -> empty split badge "en" + dash.
    expect(covText(tbody.children.item(3))).toBe(`en${DASH}`);
  });

  it("coverage refresh repaints only the changed episode and keeps row node identity", () => {
    const series = makeSeries(101, "Show B");
    const seasons = makeSeasons("Pilot", "Second", "Return");

    // Initial: only S01E01 covered.
    renderSeriesDetail(series, seasons, [epSub("tvdb-101-s01e01", 80)], new Set());

    const tbody = seriesTbody();
    const e1Before = tbody.children.item(2); // S01E01 — covered, will NOT change
    const e2Before = tbody.children.item(3); // S01E02 — uncovered, WILL change
    if (!(e1Before instanceof HTMLElement) || !(e2Before instanceof HTMLElement)) {
      throw new Error("episode rows missing");
    }
    const e1CovBefore = covText(e1Before);
    const e2CovBefore = covText(e2Before);
    expect(e2CovBefore).toBe(`en${DASH}`); // confirm S01E02 starts empty

    // Refresh (same series + seasons objects): S01E02 now covered, S01E01 same.
    renderSeriesDetail(
      series,
      seasons,
      [epSub("tvdb-101-s01e01", 80), epSub("tvdb-101-s01e02", 70)],
      new Set(),
    );

    // REUSE: the <tbody> node is the SAME (no table rebuild).
    expect(seriesTbody()).toBe(tbody);
    // (a) Changed row is the SAME DOM node, but its coverage cell changed.
    expect(tbody.children.item(3)).toBe(e2Before);
    expect(covText(e2Before)).not.toBe(e2CovBefore);
    expect(covText(e2Before)).toBe(`ensrt: ext ${STAR}70`);
    // (b) Unchanged row is the SAME DOM node, coverage cell unchanged.
    expect(tbody.children.item(2)).toBe(e1Before);
    expect(covText(e1Before)).toBe(e1CovBefore);
  });

  it("switching to a different series rebuilds the table", () => {
    const a = makeSeries(200, "Series A");
    renderSeriesDetail(a, makeSeasons("Pilot", "Second", "Return"), [], new Set());
    const tbodyA = seriesTbody();
    const aRow = tbodyA.children.item(2); // S01E01 of A ("Pilot")
    expect(aRow instanceof HTMLElement).toBe(true);

    const b = makeSeries(201, "Series B");
    renderSeriesDetail(b, makeSeasons("Bravo One", "Bravo Two", "Bravo Three"), [], new Set());
    const tbodyB = seriesTbody();

    // Different series id -> REBUILD: new <tbody>, A's rows gone, B's present.
    expect(tbodyB).not.toBe(tbodyA);
    expect(document.body.contains(aRow as Node)).toBe(false);
    expect(tbodyB.textContent).toContain("Bravo One");
    expect(tbodyB.textContent).not.toContain("Pilot");
  });
});

describe("detail: openMovieDetail", () => {
  beforeEach(() => {
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("coverage refresh repaints only the changed language row and keeps row identity", () => {
    // Initial: en covered, fr uncovered.
    openMovieDetail(makeMovie(50, [movieSub("en", 90)]));

    const tbody = movieTbody();
    expect(tbody.children.length).toBe(2); // en, fr (target order)
    const enBefore = tbody.children.item(0); // en — will NOT change
    const frBefore = tbody.children.item(1); // fr — WILL change
    if (!(enBefore instanceof HTMLElement) || !(frBefore instanceof HTMLElement)) {
      throw new Error("movie rows missing");
    }
    const enCovBefore = covText(enBefore);
    const frCovBefore = covText(frBefore);
    expect(enCovBefore).toBe(`srt: ext ${STAR}90`);
    expect(frCovBefore).toBe(DASH); // movie empty badge has no lang prefix

    // Refresh (new movie object, same tmdb_id): fr now covered, en unchanged.
    openMovieDetail(
      makeMovie(50, [movieSub("en", 90), movieSub("fr", 85)]),
      true,
    );

    // REUSE: same <tbody> node.
    expect(movieTbody()).toBe(tbody);
    // Changed fr row: SAME node, coverage changed.
    expect(tbody.children.item(1)).toBe(frBefore);
    expect(covText(frBefore)).not.toBe(frCovBefore);
    expect(covText(frBefore)).toBe(`srt: ext ${STAR}85`);
    // Unchanged en row: SAME node, coverage unchanged.
    expect(tbody.children.item(0)).toBe(enBefore);
    expect(covText(enBefore)).toBe(enCovBefore);
  });

  it("switching to a different movie rebuilds the table", () => {
    openMovieDetail(makeMovie(60, [movieSub("en", 90)]));
    const tbodyA = movieTbody();
    const aRow = tbodyA.children.item(0); // en row of movie A
    expect(aRow instanceof HTMLElement).toBe(true);

    // Different movie id -> REBUILD: keyed <table> forces patch to replace the
    // table (and its freshly-bound <tbody>), so A's rows are gone and the live
    // binding is attached to the in-DOM node rather than a detached one.
    openMovieDetail(makeMovie(61, [movieSub("en", 80)]));
    const tbodyB = movieTbody();

    expect(tbodyB).not.toBe(tbodyA);
    expect(document.body.contains(aRow as Node)).toBe(false);
    expect(covText(tbodyB.children.item(0))).toBe(`srt: ext ${STAR}80`);
  });
});
