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
import { split } from "@cplieger/keyenc";
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
      episodes: [{ id: 201, season: 2, episode: 1, title: t3, has_file: true }],
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

function reqRow(row: Element | null): HTMLElement {
  if (!(row instanceof HTMLElement)) {
    throw new Error("row missing");
  }
  return row;
}

function reqSig(row: Element | null): string {
  const sig = reqRow(row).dataset["sig"];
  if (sig === undefined) {
    throw new Error("row carries no data-sig");
  }
  return sig;
}

/** The pre-keyenc per-entry / per-target block, kept so the collisions it
 *  allowed are pinned as facts rather than described in comments. */
function pipeJoinedEntryBlock(entries: SubtitleEntry[]): string {
  return entries
    .map((e) => `${e.source}:${e.codec ?? ""}:${e.score ?? 0}:${e.ordinal ?? 0}`)
    .join(",");
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

  it("keeps two targets in separate buckets where the pipe-joined index key collapsed them", () => {
    // The subtitle index is BUILT from each stored file's language/variant and
    // LOOKED UP with the config target's, so both ends run through the same
    // langVariantKey. The old `${lang}|${variant}` read both targets below as
    // "fr|forced|standard", so a file stored for the SECOND target also
    // satisfied the first: one download lit up two coverage badges.
    const series: SeriesItem = {
      ...makeSeries(300, "Show C"),
      targets: [
        { language: "fr|forced", variant: "standard", have: 0, total: 1, have_ignored: 0 },
        { language: "fr", variant: "forced|standard", have: 0, total: 1, have_ignored: 0 },
      ],
    };
    expect(`${series.targets[0]?.language}|${series.targets[0]?.variant}`).toBe(
      `${series.targets[1]?.language}|${series.targets[1]?.variant}`,
    ); // the defect
    const stored: SubtitleEntry = {
      media_id: "tvdb-300-s01e01",
      language: "fr",
      variant: "forced|standard",
      source: "external",
      codec: "srt",
      score: 80,
      ordinal: 0,
    };

    renderSeriesDetail(series, makeSeasons("Pilot", "Second", "Return"), [stored], new Set());

    // First target uncovered (dash), second covered — not both covered.
    expect(covText(seriesTbody().children.item(2))).toBe(
      `fr|forced${DASH}fr(forced|standard)srt: ext ${STAR}80`,
    );
  });

  it("assembles the row signature as nested joins that split back level by level", () => {
    // The signature nests three levels (entry fields, entries within a target,
    // targets within the row) plus the history/ignored-codec sections. Nesting
    // is composition: each level is its own join whose RESULT is one component
    // of the level above, so every level splits back unambiguously and the leaf
    // stays byte-identical to the old `source:codec:score:ordinal` form. (The
    // outer levels re-escape the inner separators — by design, and invisible
    // because the signature is only ever compared against data-sig.)
    renderSeriesDetail(
      makeSeries(302, "Show E"),
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-302-s01e01", 80)],
      new Set(),
    );

    const [targetsBlock, history, ignoredCodecs] = split(reqSig(seriesTbody().children.item(2)));
    expect([history, ignoredCodecs]).toEqual(["0", ""]);
    const [enBlock] = split(targetsBlock ?? "");
    const [entry] = split(enBlock ?? "");
    expect(entry).toBe("external:srt:80:0");
    expect(split(entry ?? "")).toEqual(["external", "srt", "80", "0"]);
  });

  it("repaints an episode where the old signature collapsed two coverage states", () => {
    const series = makeSeries(301, "Show D"); // single en/standard target
    const seasons = makeSeasons("Pilot", "Second", "Return");
    const entry = (source: string, codec: string): SubtitleEntry => ({
      media_id: "tvdb-301-s01e01",
      language: "en",
      variant: "standard",
      source,
      codec,
      score: 0,
      ordinal: 0,
    });
    // Two entries, versus one entry whose codec reproduces their joined form.
    // The old signature separated entry fields with ':' and entries with ',',
    // so "external:srt:0:0,x:srt:0:0" was reachable both ways and the refresh
    // from one state to the other short-circuited: the coverage cell kept
    // painting two badges for a single stored file (a stale badge, never a
    // wrong entity — the signature only gates the repaint).
    const twoEntries = [entry("external", "srt"), entry("x", "srt")];
    const oneEntry = [entry("external", "srt:0:0,x:srt")];
    expect(pipeJoinedEntryBlock(twoEntries)).toBe(pipeJoinedEntryBlock(oneEntry)); // the defect

    renderSeriesDetail(series, seasons, twoEntries, new Set());
    const row = reqRow(seriesTbody().children.item(2));
    const sigBefore = reqSig(row);
    const covBefore = covText(row);

    renderSeriesDetail(series, seasons, oneEntry, new Set()); // REUSE path

    expect(reqRow(seriesTbody().children.item(2))).toBe(row); // same node, repainted
    expect(reqSig(row)).not.toBe(sigBefore);
    expect(covText(row)).not.toBe(covBefore);
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
    openMovieDetail(makeMovie(50, [movieSub("en", 90), movieSub("fr", 85)]), true);

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

  it("assembles the language-row signature as nested joins that split back", () => {
    openMovieDetail(makeMovie(70, [movieSub("en", 90)]));

    const [entriesBlock, ignoredCodecs] = split(reqSig(movieTbody().children.item(0)));
    expect(ignoredCodecs).toBe("");
    const [entry] = split(entriesBlock ?? "");
    expect(entry).toBe("external:srt:90"); // leaf identical to the old inner form
    expect(split(entry ?? "")).toEqual(["external", "srt", "90"]);
  });

  it("repaints a language row where the old signature collapsed two coverage states", () => {
    const entry = (source: string, codec: string): SubtitleEntry => ({
      media_id: "tmdb-71",
      language: "en",
      variant: "standard",
      source,
      codec,
      score: 0,
      ordinal: 0,
    });
    // Same collapse as the episode signature, one level shallower (no ordinal):
    // ':' between an entry's fields, ',' between entries, so
    // "external:srt:0,x:srt:0" was reachable from two different entry lists.
    const twoEntries = [entry("external", "srt"), entry("x", "srt")];
    const oneEntry = [entry("external", "srt:0,x:srt")];
    const pipeJoinedMovieBlock = (entries: SubtitleEntry[]): string =>
      entries.map((e) => `${e.source}:${e.codec ?? ""}:${e.score ?? 0}`).join(",");
    expect(pipeJoinedMovieBlock(twoEntries)).toBe(pipeJoinedMovieBlock(oneEntry)); // the defect

    openMovieDetail(makeMovie(71, twoEntries));
    const row = reqRow(movieTbody().children.item(0));
    const sigBefore = reqSig(row);
    const covBefore = covText(row);

    openMovieDetail(makeMovie(71, oneEntry), true); // REUSE path

    expect(reqRow(movieTbody().children.item(0))).toBe(row); // same node, repainted
    expect(reqSig(row)).not.toBe(sigBefore);
    expect(covText(row)).not.toBe(covBefore);
  });
});
