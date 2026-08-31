import { describe, it, vi, beforeEach, expect } from "vitest";

// CRITICAL: vitest.config has clearMocks/mockReset/restoreMocks=true, which
// strips a vi.fn's implementation before each test. Any mock whose behavior
// must persist across tests (resolved values, factory shapes, no-op handlers
// called at module load) MUST be a PLAIN function, not a vi.fn().
//
// Per-test wiring therefore lives in hoisted mutable records the plain-function
// factories close over.
const clientState = vi.hoisted(() => ({
  stateIDs: null as string[] | null,
  seasons: null as unknown,
  seasonsError: null as Error | null,
  // Movie /subs rows (A5: openMovieDetail reads them on demand) + a call
  // counter for the one-bounded-fetch pin.
  movieSubs: [] as unknown[] | null,
  movieSubsCalls: 0,
  // When set, mediaEpisodes hands back a promise the test resolves by hand, so
  // two navigations can be interleaved in a chosen order.
  defer: false,
  pending: [] as ((v: unknown) => void)[],
  // The movie /subs mirror of defer/pending.
  subsDefer: false,
  subsPending: [] as ((v: unknown) => void)[],
}));
vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: () => {
    if (clientState.defer) {
      return new Promise((resolve) => {
        clientState.pending.push(resolve as (v: unknown) => void);
      });
    }
    return clientState.seasonsError
      ? Promise.reject(clientState.seasonsError)
      : Promise.resolve(clientState.seasons);
  },
  coverageSeriesDetail: () => Promise.resolve([]),
  coverageMovieSubs: () => {
    clientState.movieSubsCalls += 1;
    if (clientState.subsDefer) {
      return new Promise((resolve) => {
        clientState.subsPending.push(resolve as (v: unknown) => void);
      });
    }
    return Promise.resolve(clientState.movieSubs);
  },
  stateIDs: () => Promise.resolve(clientState.stateIDs),
}));
vi.mock("@cplieger/actions", () => ({ registerCleanup: () => undefined }));
// The ui-primitives view-transition wrapper: renders and in-place heals must
// NEVER invoke it (navigation-only view transitions — the router owns the
// only call site, pinned in router.test.ts).
const uipViewTransition = vi.hoisted(() => ({ calls: 0 }));
vi.mock("@cplieger/ui-primitives/view-transition", () => ({
  viewTransition: () => {
    uipViewTransition.calls += 1;
    return Promise.resolve();
  },
}));
// The bus handlers detail.ts registers at import time are the only entry point
// to openSeriesDetail; capturing them lets a test drive that path the way
// coverage.ts does, while `emit` stays a spy for the panel assertions.
const busHandlers = vi.hoisted(() => ({ map: new Map<string, (p: never) => void>() }));
vi.mock("./bus.js", () => ({
  on: (event: string, handler: (p: never) => void) => {
    busHandlers.map.set(event, handler);
    return () => undefined;
  },
  emit: vi.fn(),
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
vi.mock("./sync.js", () => ({ openSyncDialog: vi.fn(), confirmSeasonSync: vi.fn() }));
vi.mock("./files.js", () => ({ openFileManager: vi.fn() }));
// detail.ts imports openConfig (for the no-targets empty state); mock the
// whole module so its transitive graph (status.ts actions) stays out.
vi.mock("./config.js", () => ({ openConfig: vi.fn() }));
vi.mock("./detail-scan.js", () => ({
  triggerSeriesScan: vi.fn(),
  triggerSeasonScan: vi.fn(),
  triggerMovieScan: vi.fn(),
  registerScanButton: () => undefined,
}));
// Mutable store state: the getters below read it, so a test can set the admin
// flag or the ignored-codec set without re-mocking the module.
const storeState = vi.hoisted(() => ({
  ignoredCodecs: new Set<string>(),
  ignoredCodecsReads: 0,
  isAdmin: false,
  config: null as { sonarr_url?: string; radarr_url?: string } | null,
  sets: [] as [string, unknown][],
}));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => {
    if (k === "ignoredCodecs") {
      storeState.ignoredCodecsReads += 1;
      return storeState.ignoredCodecs;
    }
    if (k === "isAdmin") {
      return storeState.isAdmin;
    }
    if (k === "config") {
      return storeState.config;
    }
    return null;
  },
  set: (k: string, v: unknown) => {
    storeState.sets.push([k, v]);
  },
}));

import {
  renderSeriesDetail,
  renderMovieDetailFromLeg,
  openMovieDetail,
  disposeDetailBindings,
} from "./detail.js";
import { split } from "@cplieger/keyenc";
import { openSyncDialog } from "./sync.js";
import { openFileManager } from "./files.js";
import { openConfig } from "./config.js";
import { triggerSeriesScan, triggerMovieScan } from "./detail-scan.js";
import { emit, BusEvent } from "./bus.js";
import type { SeriesItem, SeasonGroup, SubtitleEntry, MovieDetail } from "./api-types.js";

const STAR = "\u2605"; // ★ — score prefix in coverage badge detail
const DASH = "\u2014"; // — — empty coverage badge
const EMBEDDED = "embedded"; // constants.EMBEDDED_PROVIDER, as it arrives on the wire

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

function makeMovie(tmdbId: number): MovieDetail {
  return {
    title: `Movie ${tmdbId}`,
    audio_lang: "en",
    rule: "en",
    targets: [
      { language: "en", variant: "standard", have: 0, total: 1, have_ignored: 0 },
      { language: "fr", variant: "standard", have: 0, total: 1, have_ignored: 0 },
    ],
    tmdb_id: tmdbId,
    id: tmdbId,
    year: 2021,
    has_file: true,
  };
}

/** Open a movie detail and let its on-demand /subs read land: the mocked
 *  fetch resolves inside the skeleton's 150ms show-delay, so the commit
 *  renders on the microtask queue — two ticks drain the then/commit chain. */
async function openMovieSettled(m: MovieDetail, skipPush?: boolean): Promise<void> {
  openMovieDetail(m, skipPush);
  await Promise.resolve();
  await Promise.resolve();
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

/** Everything the movie-detail render entry decides from its payload: the
 *  coverage text of each language row (from the subtitle rows) and each header
 *  button the payload gates (History from the history ids, sync/Files from the
 *  rows). The comparable view, for the two callers' shared-seam pin. */
function movieViewSignature(): {
  rows: string[];
  history: boolean;
  sync: boolean;
  files: boolean;
} {
  return {
    rows: [...movieTbody().children].map((r) => covText(r)),
    history: document.querySelector('[data-nav="hist"]') !== null,
    sync: document.querySelector('[data-nav="sync"]') !== null,
    files: document.querySelector('[data-nav="files"]') !== null,
  };
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

/** Drive detail.ts through the bus handler coverage.ts publishes to. */
function openSeriesViaBus(item: SeriesItem, skipPush?: boolean): void {
  const handler = busHandlers.map.get("open:series");
  if (!handler) {
    throw new Error("open:series handler not registered");
  }
  const payload = skipPush === undefined ? { item } : { item, skipPush };
  (handler as (p: { item: SeriesItem; skipPush?: boolean }) => void)(payload);
}

/** The most recent PanelConfigure payload the module emitted. */
function panelDetail(): Record<string, unknown> {
  const call = vi
    .mocked(emit)
    .mock.calls.filter(([event]) => event === BusEvent.PanelConfigure)
    .at(-1);
  if (!call) {
    throw new Error("no PanelConfigure emitted");
  }
  return (call[1] as unknown as { detail: Record<string, unknown> }).detail;
}

/** The pre-keyenc per-entry / per-target block, kept so the collisions it
 *  allowed are pinned as facts rather than described in comments. */
function pipeJoinedEntryBlock(entries: SubtitleEntry[]): string {
  return entries
    .map((e) => `${e.source}:${e.codec ?? ""}:${e.score ?? 0}:${e.ordinal ?? 0}`)
    .join(",");
}

/** A history set that counts its own traversals: the C3 hoists are pinned by
 *  EXECUTION COUNT (one walk per rebuild, point lookups per row), which no
 *  output assertion can observe. */
class CountingSet extends Set<string> {
  iterations = 0;
  hasCalls = 0;

  override [Symbol.iterator](): SetIterator<string> {
    this.iterations += 1;
    return super[Symbol.iterator]();
  }

  override has(v: string): boolean {
    this.hasCalls += 1;
    return super.has(v);
  }
}

describe("detail: renderSeriesDetail", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
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
    // A per-target badge is split: the language side is addressable on its own
    // so the two halves can be styled (and read) separately.
    const empty = reqRow(tbody.children.item(3)).querySelector("span.badge");
    expect(empty?.className).toBe("badge badge-split");
    expect(empty?.getAttribute("data-status")).toBe("err");
    expect(empty?.querySelector("span.badge-lang")?.textContent).toBe("en");
    expect(empty?.querySelector("span.badge-detail")?.textContent).toBe(DASH);
    const covered = reqRow(tbody.children.item(2)).querySelector("span.badge");
    expect(covered?.querySelector("span.badge-lang")?.textContent).toBe("en");
    expect(covered?.querySelector("span.badge-detail")?.textContent).toBe(`srt: ext ${STAR}80`);
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
  it("shows the top score and groups the badge detail by codec", () => {
    const entry = (codec: string, source: string, score: number): SubtitleEntry => ({
      media_id: "tvdb-303-s01e01",
      language: "en",
      variant: "standard",
      source,
      codec,
      score,
      ordinal: 0,
    });

    renderSeriesDetail(
      makeSeries(303, "Show F"),
      makeSeasons("Pilot", "Second", "Return"),
      [entry("srt", "external", 40), entry("ass", EMBEDDED, 90)],
      new Set(),
    );

    // One group per codec, "+"-joined, then the highest score of the row.
    expect(covText(seriesTbody().children.item(2))).toBe(`ensrt: ext + ass: emb ${STAR}90`);
  });

  it("omits the score marker when nothing scored", () => {
    const unscored: SubtitleEntry = {
      media_id: "tvdb-304-s01e01",
      language: "en",
      variant: "standard",
      source: "external",
      codec: "srt",
      score: 0,
      ordinal: 0,
    };

    renderSeriesDetail(
      makeSeries(304, "Show G"),
      makeSeasons("Pilot", "Second", "Return"),
      [unscored],
      new Set(),
    );

    expect(covText(seriesTbody().children.item(2))).toBe("ensrt: ext");
  });

  it("warns when every entry is an embedded track in an ignored codec", () => {
    storeState.ignoredCodecs = new Set(["pgs"]);
    const embedded = (codec: string): SubtitleEntry => ({
      media_id: "tvdb-305-s01e01",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec,
      score: 0,
      ordinal: 0,
    });

    renderSeriesDetail(
      makeSeries(305, "Show H"),
      makeSeasons("Pilot", "Second", "Return"),
      [embedded("pgs")],
      new Set(),
    );

    const badge = reqRow(seriesTbody().children.item(2)).querySelector("span.badge");
    expect(badge?.getAttribute("data-status")).toBe("warn");
    expect(badge?.getAttribute("data-tip")).toBe("Ignored codec");
  });

  it("stays ok when one entry is usable alongside an ignored embedded track", () => {
    storeState.ignoredCodecs = new Set(["pgs"]);
    const sub = (source: string, codec: string): SubtitleEntry => ({
      media_id: "tvdb-306-s01e01",
      language: "en",
      variant: "standard",
      source,
      codec,
      score: 0,
      ordinal: 0,
    });

    renderSeriesDetail(
      makeSeries(306, "Show I"),
      makeSeasons("Pilot", "Second", "Return"),
      [sub(EMBEDDED, "pgs"), sub("external", "srt")],
      new Set(),
    );

    const badge = reqRow(seriesTbody().children.item(2)).querySelector("span.badge");
    expect(badge?.getAttribute("data-status")).toBe("ok");
    expect(badge?.getAttribute("data-tip")).toBeNull();
  });

  it("sorts the ignored-codec set into the row signature", () => {
    // Set iteration order is insertion order; the signature sorts so two
    // clients with the same ignored set produce the same signature.
    storeState.ignoredCodecs = new Set(["pgs", "ass"]);

    renderSeriesDetail(
      makeSeries(307, "Show J"),
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-307-s01e01", 80, 3)],
      new Set(),
    );

    const [, , ignoredCodecs] = split(reqSig(seriesTbody().children.item(2)));
    expect(ignoredCodecs).toBe("ass:pgs");
  });

  it("carries the manual ordinal in the row signature", () => {
    renderSeriesDetail(
      makeSeries(308, "Show K"),
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-308-s01e01", 80, 3)],
      new Set(),
    );

    const [targetsBlock] = split(reqSig(seriesTbody().children.item(2)));
    const [enBlock] = split(targetsBlock ?? "");
    expect(split(enBlock ?? "")[0]).toBe("external:srt:80:3");
  });

  it("orders regular seasons ascending and puts specials last", () => {
    const seasons: SeasonGroup[] = [
      { season: 2, episodes: [{ id: 21, season: 2, episode: 1, title: "S2", has_file: true }] },
      { season: 0, episodes: [{ id: 1, season: 0, episode: 1, title: "Special", has_file: true }] },
      { season: 1, episodes: [{ id: 11, season: 1, episode: 1, title: "S1", has_file: true }] },
    ];

    renderSeriesDetail(makeSeries(309, "Show L"), seasons, [], new Set());

    const heads = [...seriesTbody().querySelectorAll("tr.season-head td:first-child")].map(
      (td) => td.textContent ?? "",
    );
    expect(heads).toEqual(["Season 1", "Season 2", "Specials"]);
  });

  it("skips a season whose episodes have no video files", () => {
    const seasons: SeasonGroup[] = [
      { season: 1, episodes: [{ id: 11, season: 1, episode: 1, title: "S1", has_file: true }] },
      { season: 2, episodes: [{ id: 21, season: 2, episode: 1, title: "S2", has_file: false }] },
    ];

    renderSeriesDetail(makeSeries(310, "Show M"), seasons, [], new Set());

    const heads = [...seriesTbody().querySelectorAll("tr.season-head td:first-child")].map(
      (td) => td.textContent ?? "",
    );
    expect(heads).toEqual(["Season 1"]);
    expect(seriesTbody().textContent).not.toContain("S2");
  });

  it("labels episodes by aired number when absolute numbering just counts up", () => {
    // Sonarr sets absolute_episode on every multi-season show; a running count
    // is not alternate ordering, so the aired label stays.
    const seasons: SeasonGroup[] = [
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "A", has_file: true, absolute_episode: 1 },
          { id: 12, season: 1, episode: 2, title: "B", has_file: true, absolute_episode: 2 },
        ],
      },
      {
        season: 2,
        episodes: [
          { id: 21, season: 2, episode: 1, title: "C", has_file: true, absolute_episode: 3 },
        ],
      },
    ];

    renderSeriesDetail(makeSeries(311, "Show N"), seasons, [], new Set());

    expect(reqRow(seriesTbody().children.item(2)).textContent).toContain("E01");
    expect(seriesTbody().textContent).not.toContain("#");
  });

  it("labels episodes by absolute number when it diverges from aired order", () => {
    const seasons: SeasonGroup[] = [
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "A", has_file: true, absolute_episode: 1 },
        ],
      },
      {
        season: 2,
        episodes: [
          { id: 21, season: 2, episode: 1, title: "C", has_file: true, absolute_episode: 27 },
        ],
      },
    ];

    renderSeriesDetail(makeSeries(312, "Show O"), seasons, [], new Set());

    // S2 row: absolute label with the aired number kept alongside.
    const s2 = reqRow(seriesTbody().children.item(6));
    expect(s2.querySelector("td.ep-num")?.textContent).toBe("#27 E01");
    expect(s2.querySelector("td.ep-num")?.getAttribute("data-tip")).toBe(
      "Absolute #27, aired S02E01",
    );
  });

  it("counts a specials season out of the absolute-order baseline", () => {
    // Specials are skipped when accumulating the prior-episode count, so a
    // season-1 absolute that matches its aired number is still aired order —
    // even when the special itself carries an unrelated absolute number.
    const seasons: SeasonGroup[] = [
      {
        season: 0,
        episodes: [
          { id: 1, season: 0, episode: 1, title: "Sp", has_file: true, absolute_episode: 99 },
        ],
      },
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "A", has_file: true, absolute_episode: 1 },
          { id: 12, season: 1, episode: 2, title: "B", has_file: true, absolute_episode: 2 },
        ],
      },
    ];

    renderSeriesDetail(makeSeries(313, "Show P"), seasons, [], new Set());

    expect(seriesTbody().textContent).not.toContain("#");
  });

  it("offers sync on an episode with an external subtitle and a video file", () => {
    const series = makeSeries(314, "Show Q");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    const external = epSub("tvdb-314-s01e01", 80);

    renderSeriesDetail(series, seasons, [external], new Set());

    const row = reqRow(seriesTbody().children.item(2));
    const syncBtn = row.querySelector<HTMLButtonElement>(
      '[data-col="actions"] .action-group [data-tip="Adjust subtitle timing"]',
    );
    if (!syncBtn) {
      throw new Error("sync button missing");
    }
    expect(syncBtn.querySelector(".btn-text")?.textContent).toBe(" Sync");
    syncBtn.click();

    expect(openSyncDialog).toHaveBeenCalledWith([external], "series", 314, "S01E01");
    // The uncovered sibling episode has nothing to sync.
    expect(
      reqRow(seriesTbody().children.item(3)).querySelector("[data-tip='Adjust subtitle timing']"),
    ).toBeNull();
  });

  it("offers no sync for an embedded-only episode", () => {
    const embedded: SubtitleEntry = {
      media_id: "tvdb-315-s01e01",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    renderSeriesDetail(
      makeSeries(315, "Show R"),
      makeSeasons("Pilot", "Second", "Return"),
      [embedded],
      new Set(),
    );

    expect(
      reqRow(seriesTbody().children.item(2)).querySelector("[data-tip='Adjust subtitle timing']"),
    ).toBeNull();
  });

  it("offers season sync only where the season has external subtitles", () => {
    const series = makeSeries(316, "Show S");

    renderSeriesDetail(series, makeSeasons("Pilot", "Second", "Return"), [], new Set());
    expect(
      document.querySelector("tr.season-head [data-tip='Audio sync all subtitles in this season']"),
    ).toBeNull();

    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"></div><div id="coverageContent"></div></div>';
    renderSeriesDetail(
      series,
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-316-s01e01", 80)],
      new Set(),
    );

    const heads = [...seriesTbody().querySelectorAll("tr.season-head")];
    const seasonSync = heads[0]?.querySelector<HTMLButtonElement>(
      '[data-col="actions"] .action-group [data-tip="Audio sync all subtitles in this season"]',
    );
    expect(seasonSync?.querySelector(".btn-text")?.textContent).toBe(" Sync");
    // Season 2 has no external subs of its own.
    expect(
      heads[1]?.querySelector("[data-tip='Audio sync all subtitles in this season']"),
    ).toBeNull();
  });

  it("shows history buttons for the episodes and seasons that have downloads", () => {
    renderSeriesDetail(
      makeSeries(317, "Show T"),
      makeSeasons("Pilot", "Second", "Return"),
      [],
      // One matching id among unrelated ones: a season needs ANY of its
      // episodes in the set, not all of them.
      new Set(["tvdb-317-s01e02", "tvdb-999-s04e01"]),
    );

    const tbody = seriesTbody();
    // Season 1 head: one of its episodes has history.
    const seasonHist = reqRow(tbody.children.item(0)).querySelector<HTMLButtonElement>(
      '[data-col="actions"] .action-group [data-tip="View download history for this season"]',
    );
    expect(seasonHist?.querySelector(".btn-text")?.textContent).toBe(" History");
    expect(
      reqRow(tbody.children.item(2)).querySelector(
        "[data-tip='View download history for this episode']",
      ),
    ).toBeNull();
    const epHist = reqRow(tbody.children.item(3)).querySelector<HTMLButtonElement>(
      '[data-col="actions"] .action-group [data-tip="View download history for this episode"]',
    );
    expect(epHist?.querySelector(".btn-text")?.textContent).toBe(" History");
    epHist?.click();
    expect(emit).toHaveBeenCalledWith(BusEvent.NavHistory, "Show T S01E02");
    seasonHist?.click();
    expect(emit).toHaveBeenCalledWith(BusEvent.NavHistory, "Show T S01");
    // Season 2 head: no history for any of its episodes.
    expect(
      reqRow(tbody.children.item(5)).querySelector(
        "[data-tip='View download history for this season']",
      ),
    ).toBeNull();
  });

  it("counts subtitles instead of targets when the series has no language rule", () => {
    const series: SeriesItem = { ...makeSeries(318, "Show U"), targets: [] };

    renderSeriesDetail(series, makeSeasons("Pilot", "Second", "Return"), [
      epSub("tvdb-318-s01e01", 80),
    ]);

    expect(covText(seriesTbody().children.item(2))).toBe("1 subs");
    expect(covText(seriesTbody().children.item(3))).toBe(DASH);
    // With no targets the signature counts the stored subtitles instead.
    expect(split(reqSig(seriesTbody().children.item(2)))[0]).toBe("subs:1");
  });

  it("offers the Files button to an admin when an external subtitle exists", () => {
    storeState.isAdmin = true;
    const series = makeSeries(319, "Show V");
    const embedded: SubtitleEntry = {
      media_id: "tvdb-319-s01e02",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    // Mixed list: one external among embedded tracks is enough.
    renderSeriesDetail(series, makeSeasons("Pilot", "Second", "Return"), [
      embedded,
      epSub("tvdb-319-s01e01", 80),
    ]);

    const btn = document.querySelector<HTMLButtonElement>('[data-nav="files"]');
    if (!btn) {
      throw new Error("files button missing");
    }
    expect(btn.querySelector(".btn-text")?.textContent).toBe(" Files");
    btn.click();

    expect(openFileManager).toHaveBeenCalledWith(
      "episode",
      "tvdb-319-",
      "Show V",
      "/series/319",
      319,
    );
  });

  it("withholds the Files button from a non-admin and where subs are embedded", () => {
    const embedded: SubtitleEntry = {
      media_id: "tvdb-320-s01e01",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    storeState.isAdmin = false;
    renderSeriesDetail(makeSeries(320, "Show W"), makeSeasons("a", "b", "c"), [
      epSub("tvdb-320-s01e01", 80),
    ]);
    expect(document.querySelector('[data-nav="files"]')).toBeNull();

    storeState.isAdmin = true;
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"></div><div id="coverageContent"></div></div>';
    renderSeriesDetail(makeSeries(321, "Show X"), makeSeasons("a", "b", "c"), [embedded]);
    expect(document.querySelector('[data-nav="files"]')).toBeNull();
  });

  it("explains an empty season list instead of rendering a table", () => {
    renderSeriesDetail(makeSeries(322, "Show Y"), [], [], new Set());

    expect(document.querySelector("table.series-detail")).toBeNull();
    expect(document.querySelector("#coverageContent")?.textContent).toContain(
      "No episodes with video files were found",
    );
  });

  it("keeps a partially-imported season, dropping only its fileless episodes", () => {
    const seasons: SeasonGroup[] = [
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "Aired", has_file: true },
          { id: 12, season: 1, episode: 2, title: "Not imported", has_file: false },
        ],
      },
    ];

    renderSeriesDetail(makeSeries(324, "Show AA"), seasons, [], new Set());

    // head, cols, the one imported episode.
    expect(seriesTbody().children.length).toBe(3);
    expect(seriesTbody().textContent).toContain("Aired");
    expect(seriesTbody().textContent).not.toContain("Not imported");
  });

  it("shrinks an episode's action group when its subtitle disappears", () => {
    const series = makeSeries(325, "Show AB");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    renderSeriesDetail(series, seasons, [epSub("tvdb-325-s01e01", 80)], new Set());

    const row = reqRow(seriesTbody().children.item(2));
    expect(row.querySelectorAll('[data-col="actions"] .action-group button')).toHaveLength(2);

    // The file was deleted elsewhere: only Search may remain, with no leftover
    // nodes or stray text where the Sync button was.
    renderSeriesDetail(series, seasons, [], new Set());

    const group = row.querySelector('[data-col="actions"] .action-group');
    expect(group?.children).toHaveLength(1);
    expect(group?.textContent).toBe(" Search");
  });

  it("shrinks a season head's action group when the season loses its subtitles", () => {
    const series = makeSeries(326, "Show AC");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    renderSeriesDetail(series, seasons, [epSub("tvdb-326-s01e01", 80)], new Set());

    const head = reqRow(seriesTbody().children.item(0));
    expect(head.querySelectorAll('[data-col="actions"] .action-group button')).toHaveLength(2);

    renderSeriesDetail(series, seasons, [], new Set());

    const group = head.querySelector('[data-col="actions"] .action-group');
    expect(group?.children).toHaveLength(1);
    expect(group?.textContent).toBe(" Search");
  });

  it("dispatches the bus scan events to the scan triggers", () => {
    const series = makeSeries(327, "Show AD");
    const scanSeries = busHandlers.map.get("scan:series");
    const scanMovie = busHandlers.map.get("scan:movie");
    if (!scanSeries || !scanMovie) {
      throw new Error("scan handlers not registered");
    }

    (scanSeries as (p: { item: SeriesItem }) => void)({ item: series });
    (scanMovie as (p: { item: MovieDetail }) => void)({ item: makeMovie(328) });

    expect(triggerSeriesScan).toHaveBeenCalledWith(series);
    expect(triggerMovieScan).toHaveBeenCalledWith(makeMovie(328));
  });

  it("leaves an unchanged row's badge nodes in place across a refresh", () => {
    const series = makeSeries(323, "Show Z");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    renderSeriesDetail(series, seasons, [epSub("tvdb-323-s01e01", 80)], new Set());

    const covered = reqRow(seriesTbody().children.item(2));
    const badgeBefore = covered.querySelector("span.badge");
    const headBefore = reqRow(seriesTbody().children.item(0)).querySelector("button");

    // A change in ANOTHER season: season 1's head signature is untouched.
    renderSeriesDetail(
      series,
      seasons,
      [epSub("tvdb-323-s01e01", 80), epSub("tvdb-323-s02e01", 70)],
      new Set(),
    );

    // Signature match short-circuits the repaint: the badge span itself is the
    // same node, not an equal-looking replacement.
    expect(badgeBefore).not.toBeNull();
    expect(covered.querySelector("span.badge")).toBe(badgeBefore);
    expect(headBefore).not.toBeNull();
    expect(reqRow(seriesTbody().children.item(0)).querySelector("button")).toBe(headBefore);
  });
});

describe("detail: openSeriesDetail", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    storeState.sets = [];
    clientState.stateIDs = null;
    clientState.seasons = null;
    clientState.seasonsError = null;
    clientState.defer = false;
    clientState.pending = [];
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("pushes the series URL, titles the tab and summarises the item", () => {
    openSeriesViaBus(makeSeries(400, "Breaking Bad"));

    expect(location.pathname).toBe("/series/400");
    expect(document.title).toBe("Subflux \u00B7 Breaking Bad");
    expect(panelDetail()["info"]).toBe("3 ep \u00B7 audio: English \u00B7 subs: en");
    expect(panelDetail()["backPath"]).toBe("/");
    expect(panelDetail()["arrName"]).toBe("Sonarr");
  });

  it("leaves the URL alone when the caller already owns the history entry", () => {
    history.replaceState(null, "", "/library");

    openSeriesViaBus(makeSeries(401, "Show"), true);

    expect(location.pathname).toBe("/library");
  });

  it("summarises a series with no language targets and no audio rule", () => {
    const series: SeriesItem = { ...makeSeries(402, "Show"), targets: [], rule: "" };

    openSeriesViaBus(series);

    // No subs clause at all, and the audio rule falls back to "default".
    expect(panelDetail()["info"]).toBe("3 ep \u00B7 audio: default");
  });

  it("builds the Sonarr deep link from a slugified title", () => {
    storeState.config = { sonarr_url: "http://sonarr:8989///" };

    openSeriesViaBus(makeSeries(403, "  Marvel's Agents of S.H.I.E.L.D.!  "));

    // Lowercased, every run of non-alphanumerics collapsed to one dash, and no
    // leading or trailing dashes.
    expect(panelDetail()["arrLink"]).toBe(
      "http://sonarr:8989/series/marvel-s-agents-of-s-h-i-e-l-d",
    );
  });

  it("offers no Sonarr link when no Sonarr URL is configured", () => {
    storeState.config = { sonarr_url: "" };

    openSeriesViaBus(makeSeries(404, "Show"));

    expect(panelDetail()["arrLink"]).toBeNull();
  });

  it("renders the fetched seasons once the requests resolve", async () => {
    clientState.seasons = makeSeasons("Pilot", "Second", "Return");
    clientState.stateIDs = ["tvdb-405-s01e01"];

    openSeriesViaBus(makeSeries(405, "Show"));
    await vi.waitFor(() => {
      expect(document.querySelector("table.series-detail")).not.toBeNull();
    });

    expect(seriesTbody().children.length).toBe(8);
    // The history ids fetched alongside drive the per-episode History button.
    expect(
      reqRow(seriesTbody().children.item(2)).querySelector(
        "[data-tip='View download history for this episode']",
      ),
    ).not.toBeNull();
  });

  it("renders the fetch error instead of a table", async () => {
    clientState.seasonsError = new Error("episodes unavailable");

    openSeriesViaBus(makeSeries(406, "Show"));
    await vi.waitFor(() => {
      expect(document.querySelector('#coverageContent [data-status="err"]')).not.toBeNull();
    });

    expect(document.querySelector('#coverageContent [data-status="err"]')?.textContent).toBe(
      "episodes unavailable",
    );
    expect(document.querySelector("table.series-detail")).toBeNull();
  });

  it("drops a superseded series response instead of painting it", async () => {
    // The stale fetch resolves LAST, so only the abort check keeps it from
    // painting over the view the user actually navigated to.
    clientState.defer = true;
    openSeriesViaBus(makeSeries(407, "First"));
    openSeriesViaBus(makeSeries(408, "Second"));
    expect(clientState.pending).toHaveLength(2);

    clientState.pending[1]?.(makeSeasons("Fresh One", "Fresh Two", "Fresh Three"));
    await vi.waitFor(() => {
      expect(document.querySelector("table.series-detail")).not.toBeNull();
    });
    clientState.pending[0]?.(makeSeasons("Stale One", "Stale Two", "Stale Three"));
    await Promise.resolve();
    await Promise.resolve();

    expect(document.querySelector('table.series-detail[data-series-id="408"]')).not.toBeNull();
    expect(seriesTbody().textContent).toContain("Fresh One");
    expect(seriesTbody().textContent).not.toContain("Stale One");
  });
});

describe("detail: openMovieDetail", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    storeState.sets = [];
    clientState.stateIDs = null;
    clientState.movieSubs = [];
    clientState.movieSubsCalls = 0;
    clientState.subsDefer = false;
    clientState.subsPending = [];
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("coverage refresh repaints only the changed language row and keeps row identity", async () => {
    // Initial: en covered, fr uncovered.
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(50));

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
    // A movie badge is NOT split: there is no language half to show.
    expect(frBefore.querySelector("span.badge")?.className).toBe("badge");
    expect(frBefore.querySelector("span.badge")?.getAttribute("data-status")).toBe("err");
    expect(enBefore.querySelector("span.badge")?.className).toBe("badge");

    // Refresh (same tmdb_id, fresh /subs rows): fr now covered, en unchanged.
    clientState.movieSubs = [movieSub("en", 90), movieSub("fr", 85)];
    await openMovieSettled(makeMovie(50), true);

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

  it("switching to a different movie rebuilds the table", async () => {
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(60));
    const tbodyA = movieTbody();
    const aRow = tbodyA.children.item(0); // en row of movie A
    expect(aRow instanceof HTMLElement).toBe(true);

    // Different movie id -> REBUILD: keyed <table> forces patch to replace the
    // table (and its freshly-bound <tbody>), so A's rows are gone and the live
    // binding is attached to the in-DOM node rather than a detached one.
    clientState.movieSubs = [movieSub("en", 80)];
    await openMovieSettled(makeMovie(61));
    const tbodyB = movieTbody();

    expect(tbodyB).not.toBe(tbodyA);
    expect(document.body.contains(aRow as Node)).toBe(false);
    expect(covText(tbodyB.children.item(0))).toBe(`srt: ext ${STAR}80`);
  });

  it("fetches the movie's rows on demand: one bounded /subs read per open", async () => {
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(65));

    expect(clientState.movieSubsCalls).toBe(1);
    expect(covText(movieTbody().children.item(0))).toBe(`srt: ext ${STAR}90`);

    // A refresh of the same movie is one more bounded read, never a
    // collection fetch (detail.ts does not even import the collection
    // functions; this pins the count stays 1:1 with opens).
    await openMovieSettled(makeMovie(65), true);
    expect(clientState.movieSubsCalls).toBe(2);
  });

  it("paints the loading skeleton at 150ms and holds it 300ms once shown", async () => {
    vi.useFakeTimers();
    try {
      clientState.subsDefer = true;
      openMovieDetail(makeMovie(66));

      // Inside the show-delay window nothing paints.
      await vi.advanceTimersByTimeAsync(149);
      expect(document.querySelector("#coverageContent .skeleton")).toBeNull();
      // 150ms: the skeleton is up.
      await vi.advanceTimersByTimeAsync(1);
      expect(document.querySelector("#coverageContent .skeleton")).not.toBeNull();

      // The fetch resolves while the skeleton is fresh: the commit defers
      // until the 300ms min-visible elapses, then the table replaces it.
      clientState.subsPending[0]?.([movieSub("en", 90)]);
      await vi.advanceTimersByTimeAsync(0);
      expect(document.querySelector("table.movie-detail")).toBeNull();
      expect(document.querySelector("#coverageContent .skeleton")).not.toBeNull();

      await vi.advanceTimersByTimeAsync(300);
      expect(document.querySelector("table.movie-detail")).not.toBeNull();
      expect(document.querySelector("#coverageContent .skeleton")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("never paints the skeleton for a fast /subs read", async () => {
    vi.useFakeTimers();
    try {
      clientState.movieSubs = [movieSub("en", 90)];
      openMovieDetail(makeMovie(67));
      // The mocked fetch settles on the microtask queue, inside the 150ms
      // show-delay; advancing past it must find the table, never a skeleton.
      await vi.advanceTimersByTimeAsync(500);
      expect(document.querySelector("#coverageContent .skeleton")).toBeNull();
      expect(document.querySelector("table.movie-detail")).not.toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops a superseded /subs response instead of painting it", async () => {
    // The stale fetch resolves LAST, so only the abort check keeps it from
    // painting over the view the user actually navigated to.
    clientState.subsDefer = true;
    openMovieDetail(makeMovie(68));
    openMovieDetail(makeMovie(69));
    expect(clientState.subsPending).toHaveLength(2);

    clientState.subsPending[1]?.([movieSub("en", 80)]);
    await vi.waitFor(() => {
      expect(document.querySelector("table.movie-detail")).not.toBeNull();
    });
    clientState.subsPending[0]?.([movieSub("en", 90)]);
    await Promise.resolve();
    await Promise.resolve();

    expect(document.querySelector('table.movie-detail[data-movie-id="69"]')).not.toBeNull();
    expect(covText(movieTbody().children.item(0))).toBe(`srt: ext ${STAR}80`);
  });

  it("aborts the on-demand reads when the page leg's route signal aborts", async () => {
    // B2: the dispatcher's movie arm threads its route controller in, so a
    // route leave kills the /subs read mid-flight instead of painting it.
    clientState.subsDefer = true;
    const leg = new AbortController();
    openMovieDetail(makeMovie(72), true, leg.signal);
    expect(clientState.subsPending).toHaveLength(1);

    leg.abort(); // the router's leave path aborted the movie page leg
    clientState.subsPending[0]?.([movieSub("en", 90)]);
    await Promise.resolve();
    await Promise.resolve();

    expect(document.querySelector("table.movie-detail")).toBeNull();
  });

  it("assembles the language-row signature as nested joins that split back", async () => {
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(70));

    const [entriesBlock, ignoredCodecs] = split(reqSig(movieTbody().children.item(0)));
    expect(ignoredCodecs).toBe("");
    const [entry] = split(entriesBlock ?? "");
    expect(entry).toBe("external:srt:90"); // leaf identical to the old inner form
    expect(split(entry ?? "")).toEqual(["external", "srt", "90"]);
  });

  it("repaints a language row where the old signature collapsed two coverage states", async () => {
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

    clientState.movieSubs = twoEntries;
    await openMovieSettled(makeMovie(71));
    const row = reqRow(movieTbody().children.item(0));
    const sigBefore = reqSig(row);
    const covBefore = covText(row);

    clientState.movieSubs = oneEntry;
    await openMovieSettled(makeMovie(71), true); // REUSE path

    expect(reqRow(movieTbody().children.item(0))).toBe(row); // same node, repainted
    expect(reqSig(row)).not.toBe(sigBefore);
    expect(covText(row)).not.toBe(covBefore);
  });

  it("sorts the ignored-codec set into the language-row signature", async () => {
    storeState.ignoredCodecs = new Set(["pgs", "ass"]);

    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(72));

    const [, ignoredCodecs] = split(reqSig(movieTbody().children.item(0)));
    expect(ignoredCodecs).toBe("ass:pgs");
  });

  it("leaves an unchanged language row's badge node in place across a refresh", async () => {
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(73));
    const enRow = reqRow(movieTbody().children.item(0));
    const badgeBefore = enRow.querySelector("span.badge");

    clientState.movieSubs = [movieSub("en", 90), movieSub("fr", 85)];
    await openMovieSettled(makeMovie(73), true);

    expect(badgeBefore).not.toBeNull();
    expect(enRow.querySelector("span.badge")).toBe(badgeBefore);
  });

  it("labels a non-standard variant and offers a per-language search button", async () => {
    const movie: MovieDetail = {
      ...makeMovie(74),
      targets: [
        { language: "fr", variant: "forced", have: 0, total: 1, have_ignored: 0 },
        { language: "en", variant: "standard", have: 0, total: 1, have_ignored: 0 },
      ],
    };

    await openMovieSettled(movie);

    const rows = movieTbody().children;
    expect(rows.item(0)?.children.item(0)?.textContent).toBe("French (forced)");
    expect(rows.item(1)?.children.item(0)?.textContent).toBe("English");
    const searchBtn = reqRow(rows.item(1)).querySelector('[data-col="actions"] button');
    expect(searchBtn?.getAttribute("data-tip")).toBe("Search English subtitles");
    expect(searchBtn?.querySelector(".btn-text")?.textContent).toBe(" Search");
  });

  it("pushes the movie URL unless the caller already owns the history entry", async () => {
    await openMovieSettled(makeMovie(75));
    expect(location.pathname).toBe("/movie/75");
    expect(document.title).toBe("Subflux \u00B7 Movie 75");

    history.replaceState(null, "", "/library");
    await openMovieSettled(makeMovie(76), true);
    expect(location.pathname).toBe("/library");
  });

  it("configures the panel from the cached row alone: no files action rides the bus", async () => {
    storeState.config = { radarr_url: "http://radarr:7878//" };

    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(77));

    expect(emit).toHaveBeenCalledWith(BusEvent.PanelConfigure, {
      visible: false,
      detail: {
        title: "Movie 77",
        info: "2021 \u00B7 audio: English \u00B7 subs: en, fr",
        backPath: "/",
        // Trailing slashes stripped, so the path is appended exactly once.
        arrLink: "http://radarr:7878/movie/77",
        arrName: "Radarr",
      },
    });
    expect(storeState.sets).toContainEqual(["detailCtx", { movie: true, tmdbId: 77 }]);
  });

  it("offers no Files or sync button when every subtitle is an embedded track", async () => {
    storeState.isAdmin = true; // absence must come from the rows, not the role
    const embedded: SubtitleEntry = {
      media_id: "tmdb-78",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    clientState.movieSubs = [embedded];
    await openMovieSettled(makeMovie(78));

    expect(document.querySelector('[data-nav="files"]')).toBeNull();
    expect(document.querySelector('[data-nav="sync"]')).toBeNull();
    // No config in the store, so no Radarr deep link either.
    expect(panelDetail()["arrLink"]).toBeNull();
  });

  it("adds the Files button for an admin once the rows arrive, wired to the file manager", async () => {
    storeState.isAdmin = true;
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(79));

    const filesBtn = document.querySelector<HTMLButtonElement>('[data-nav="files"]');
    if (!filesBtn) {
      throw new Error("files button missing");
    }
    expect(filesBtn.querySelector(".btn-text")?.textContent).toBe(" Files");
    filesBtn.click();

    expect(openFileManager).toHaveBeenCalledWith("movie", "tmdb-79", "Movie 79", "/movie/79", 79);
  });

  it("withholds the Files button from a non-admin", async () => {
    storeState.isAdmin = false;
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(86));

    expect(document.querySelector('[data-nav="files"]')).toBeNull();
  });

  it("offers a header sync button for the external subtitles only", async () => {
    const external = movieSub("en", 90);
    const embedded: SubtitleEntry = {
      media_id: "tmdb-80",
      language: "fr",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    clientState.movieSubs = [embedded, external];
    await openMovieSettled(makeMovie(80));

    const syncBtn = document.querySelector<HTMLButtonElement>('[data-nav="sync"]');
    if (!syncBtn) {
      throw new Error("sync button missing");
    }
    expect(syncBtn.querySelector(".btn-text")?.textContent).toBe(" Sync");
    syncBtn.click();

    expect(openSyncDialog).toHaveBeenCalledWith([external], "movie", 80, "Movie 80");
  });

  it("replaces the sync button on a same-movie refresh instead of adding a second one", async () => {
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(87));
    expect(document.querySelectorAll('[data-nav="sync"]').length).toBe(1);

    await openMovieSettled(makeMovie(87), true);

    expect(document.querySelectorAll('[data-nav="sync"]').length).toBe(1);
  });

  it("adds a history button when the movie has downloads, and wires it to the log", async () => {
    clientState.stateIDs = ["tmdb-81"];

    await openMovieSettled(makeMovie(81));

    const histBtn = document.querySelector<HTMLButtonElement>('[data-nav="hist"]');
    if (!histBtn) {
      throw new Error("history button missing");
    }
    expect(histBtn.querySelector(".btn-text")?.textContent).toBe(" History");
    histBtn.click();

    expect(emit).toHaveBeenCalledWith(BusEvent.NavHistory, "Movie 81");
  });

  it("adds no history button when the movie has no downloads", async () => {
    clientState.stateIDs = [];

    await openMovieSettled(makeMovie(82));

    expect(document.querySelector('[data-nav="hist"]')).toBeNull();
  });

  it("drops a superseded history response instead of painting it", async () => {
    // Opening a second movie aborts the first fetch; its late resolution must
    // not add a button describing the movie the user left.
    clientState.stateIDs = ["tmdb-83"];
    openMovieDetail(makeMovie(83));
    clientState.stateIDs = [];
    openMovieDetail(makeMovie(84));

    await Promise.resolve();
    await Promise.resolve();

    expect(document.querySelector('[data-nav="hist"]')).toBeNull();
  });

  it("explains a movie with no language rule and offers a way to fix it", async () => {
    const movie: MovieDetail = { ...makeMovie(85), targets: [] };

    await openMovieSettled(movie);

    expect(document.querySelector("table.movie-detail")).toBeNull();
    const out = document.querySelector("#coverageContent");
    expect(out?.textContent).toContain("No language rule matches this movie");
    const btn = out?.querySelector("button");
    expect(btn?.textContent).toBe("Open Settings");
    (btn as HTMLButtonElement | null)?.click();
    expect(openConfig).toHaveBeenCalled();
  });
});

describe("detail: renderMovieDetailFromLeg (the transaction's movie render)", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    storeState.sets = [];
    clientState.stateIDs = null;
    clientState.movieSubs = [];
    clientState.movieSubsCalls = 0;
    clientState.subsDefer = false;
    clientState.subsPending = [];
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("paints from the leg's pre-fetched reads — no fetch of its own, context set", () => {
    renderMovieDetailFromLeg(makeMovie(90), [movieSub("en", 88)], []);

    expect(document.querySelector('table.movie-detail[data-movie-id="90"]')).not.toBeNull();
    expect(covText(movieTbody().children.item(0))).toBe(`srt: ext ${STAR}88`);
    expect(clientState.movieSubsCalls).toBe(0); // the LEG owned the /subs read
    expect(storeState.sets).toContainEqual(["detailCtx", { movie: true, tmdbId: 90 }]);
    expect(document.querySelector('[data-nav="hist"]')).toBeNull(); // no history rows
  });

  it("adds the History button from the leg's own stateIDs read", () => {
    renderMovieDetailFromLeg(makeMovie(91), [], ["tmdb-91"]);

    expect(document.querySelector('[data-nav="hist"]')).not.toBeNull();
  });

  it("supersedes an in-flight plain open: the stale /subs landing paints nothing", async () => {
    clientState.subsDefer = true;
    openMovieDetail(makeMovie(92));
    expect(clientState.subsPending).toHaveLength(1);

    renderMovieDetailFromLeg(makeMovie(92), [movieSub("en", 77)], []);
    expect(covText(movieTbody().children.item(0))).toBe(`srt: ext ${STAR}77`);

    clientState.subsPending[0]?.([movieSub("en", 10)]);
    await Promise.resolve();
    await Promise.resolve();

    // The superseded open landed as a no-op: the leg's paint stands.
    expect(covText(movieTbody().children.item(0))).toBe(`srt: ext ${STAR}77`);
  });
});

describe("detail: the movie-detail render seam", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    storeState.sets = [];
    clientState.stateIDs = null;
    clientState.movieSubs = [];
    clientState.movieSubsCalls = 0;
    clientState.subsDefer = false;
    clientState.subsPending = [];
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("paints the same view from the same payload whichever caller read it", async () => {
    // The two callers read differently on purpose — the plain open on the
    // null-collapsing client behind its skeleton, the leg on the raw client so
    // a failure can refuse a commit — but everything DOWNSTREAM of the read is
    // one entry taking one payload. Hand both the same rows and ids and the
    // painted view has to be identical, so neither can grow a rule the other
    // lacks (the History button was decided twice before this seam existed).
    const subs = [movieSub("en", 90)];
    const historyIDs = ["tmdb-93"];

    clientState.movieSubs = subs;
    clientState.stateIDs = historyIDs;
    await openMovieSettled(makeMovie(93));
    const painted = movieViewSignature();
    // The rows and the ids both reached the paint, so the view is worth
    // comparing: coverage from the rows, History from the ids.
    expect(painted).toStrictEqual({
      rows: [`srt: ext ${STAR}90`, DASH],
      history: true,
      sync: true,
      files: false,
    });
    expect(clientState.movieSubsCalls).toBe(1);

    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
    disposeDetailBindings();

    renderMovieDetailFromLeg(makeMovie(93), subs, historyIDs);

    expect(movieViewSignature()).toStrictEqual(painted);
    // Still one: the leg was handed its payload and read nothing of its own.
    expect(clientState.movieSubsCalls).toBe(1);
  });

  it("paints the shipped empty surface when the plain read fails, and never rejects", async () => {
    // A navigation has no commit to refuse, so the plain caller maps a failed
    // read to an empty payload: the language rows render uncovered and the page
    // stays usable. It must NOT surface the failure — a rejection out of this
    // path is what the transaction leg turns into a latch, and this open is not
    // part of a transaction.
    const rejected: unknown[] = [];
    const onRejection = (e: PromiseRejectionEvent): void => {
      rejected.push(e.reason);
      e.preventDefault();
    };
    window.addEventListener("unhandledrejection", onRejection);
    try {
      // Both reads fail: the generated client collapses any non-2xx to null.
      clientState.movieSubs = null;
      clientState.stateIDs = null;

      await openMovieSettled(makeMovie(94));
      await new Promise((r) => setTimeout(r, 0)); // let a rejection surface

      expect(movieViewSignature()).toStrictEqual({
        rows: [DASH, DASH],
        history: false,
        sync: false,
        files: false,
      });
      // The error panel is the series path's surface for a rejected read; a
      // null-collapsed one is not a rejection and must not paint it.
      expect(document.querySelector('#coverageContent div.empty[data-status="err"]')).toBeNull();
      expect(rejected).toStrictEqual([]);
    } finally {
      window.removeEventListener("unhandledrejection", onRejection);
    }
  });
});

describe("detail: renderSeriesDetail table chrome", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("frames the table with explicit table semantics, column headers and a season gap", () => {
    renderSeriesDetail(
      makeSeries(335, "Show AJ"),
      makeSeasons("Pilot", "Second", "Return"),
      [],
      new Set(),
    );

    // RETIRED: the <colgroup> pin (the widths' owner until C1). The desktop
    // tree left the table formatting context; the widths live in ONE shared
    // grid-template-columns custom property, asserted with computed styles
    // in detail-table-layout.test.ts (the shared-template pin).
    expect(document.querySelector("table.series-detail colgroup")).toBeNull();

    // Leaving the table formatting context strips the implicit table
    // semantics, so the tree carries them explicitly.
    const table = document.querySelector("table.series-detail");
    expect(table?.getAttribute("role")).toBe("table");
    expect(table?.getAttribute("aria-labelledby")).toBe("lib-heading");

    const tbody = seriesTbody();
    // S1 -> head(0), cols(1), ep01(2), ep02(3) ; S2 -> gap(4), head(5), ...
    const colHead = reqRow(tbody.children.item(1));
    expect(colHead.className).toBe("season-head");
    expect([...colHead.querySelectorAll("th")].map((th) => th.textContent)).toEqual([
      "Ep",
      "Title",
      "Subtitles",
      "",
    ]);

    const gap = reqRow(tbody.children.item(4));
    expect(gap.className).toBe("season-gap");
    const gapCell = gap.children.item(0) as HTMLTableCellElement | null;
    // RETIRED with the colgroup: colSpan 999. The spacer spans every grid
    // column via grid-column: 1 / -1 (geometry pinned in the layout suite).
    expect(gapCell?.hasAttribute("colspan")).toBe(false);
  });

  it("carries the full row semantics on every row kind (rowgroup/row/columnheader/cell)", () => {
    // The harness populates the heading BEFORE the render, the order the
    // panel-configure path guarantees, so the accessible name has a source.
    const heading = document.getElementById("lib-heading");
    if (!heading) {
      throw new Error("lib-heading missing");
    }
    heading.textContent = "Show AV";

    renderSeriesDetail(
      makeSeries(344, "Show AV"),
      makeSeasons("Pilot", "Second", "Return"),
      [],
      new Set(),
    );

    const tbody = seriesTbody();
    expect(tbody.getAttribute("role")).toBe("rowgroup");
    const trs = [...tbody.querySelectorAll("tr")];
    expect(trs.length).toBeGreaterThan(0);
    for (const tr of trs) {
      expect(tr.getAttribute("role")).toBe("row");
    }
    const ths = [...tbody.querySelectorAll("th")];
    expect(ths).toHaveLength(8); // one 4-column header row per season
    for (const th of ths) {
      expect(th.getAttribute("role")).toBe("columnheader");
    }
    const tds = [...tbody.querySelectorAll("td")];
    expect(tds.length).toBeGreaterThan(0);
    for (const td of tds) {
      expect(td.getAttribute("role")).toBe("cell");
    }
    // The accessible name resolves through aria-labelledby to the populated
    // panel heading.
    const table = document.querySelector("table.series-detail");
    const labelledBy = table?.getAttribute("aria-labelledby");
    const nameSource = labelledBy ? document.getElementById(labelledBy) : null;
    expect(nameSource?.textContent).toBe("Show AV");
  });

  it("labels the episode cells with the aired number, the title and the episode key", () => {
    renderSeriesDetail(
      makeSeries(334, "Show AI"),
      makeSeasons("Pilot", "Second", "Return"),
      [],
      new Set(),
    );

    const row = reqRow(seriesTbody().children.item(2));
    const numCell = row.children.item(0);
    expect(numCell?.className).toBe("ep-num");
    expect(numCell?.textContent).toBe("E01");
    const titleCell = row.children.item(1);
    expect(titleCell?.className).toBe("ep-title");
    // data-ep is how the CSS labels a narrow-viewport row with its episode.
    expect(titleCell?.getAttribute("data-ep")).toBe("E01");
    expect(titleCell?.textContent).toBe("Pilot");
  });

  it("derives the codec signature and history membership once per rebuild, not per row", () => {
    // C3 hoists, pinned by execution count: 2 seasons and 3 episode rows must
    // cost ONE ignored-codec read (was one per episode row) and ONE history
    // walk (was one full scan per season); per-episode membership stays a
    // point lookup.
    const historySet = new CountingSet(["tvdb-342-s01e01"]);
    storeState.ignoredCodecsReads = 0;

    renderSeriesDetail(
      makeSeries(342, "Show AT"),
      makeSeasons("Pilot", "Second", "Return"),
      [],
      historySet,
    );

    expect(storeState.ignoredCodecsReads).toBe(1);
    expect(historySet.iterations).toBe(1);
    expect(historySet.hasCalls).toBe(3);
    // The hoisted membership answers exactly as the per-season scan did.
    const s1Head = reqRow(seriesTbody().children.item(0));
    expect(
      s1Head.querySelector('[data-tip="View download history for this season"]'),
    ).not.toBeNull();
    const s2Head = reqRow(seriesTbody().children.item(5));
    expect(s2Head.querySelector('[data-tip="View download history for this season"]')).toBeNull();
  });

  it("paints renders and in-place heals without invoking a view transition", () => {
    // Navigation-only view transitions (R9): the router wraps route changes;
    // a detail rebuild or a coverage heal repainting rows in place never
    // queues one.
    const series = makeSeries(343, "Show AU");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    uipViewTransition.calls = 0;

    renderSeriesDetail(series, seasons, [], new Set()); // REBUILD
    renderSeriesDetail(series, seasons, [epSub("tvdb-343-s01e01", 80)], new Set()); // REUSE heal

    expect(uipViewTransition.calls).toBe(0);
  });

  it("keeps the aired number beside an absolute label as its own element", () => {
    const seasons: SeasonGroup[] = [
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "A", has_file: true, absolute_episode: 1 },
        ],
      },
      {
        season: 2,
        episodes: [
          { id: 21, season: 2, episode: 1, title: "C", has_file: true, absolute_episode: 27 },
        ],
      },
    ];

    renderSeriesDetail(makeSeries(336, "Show AK"), seasons, [], new Set());

    // S1 -> head(0), cols(1), ep(2) ; S2 -> gap(3), head(4), cols(5), ep(6).
    const numCell = reqRow(seriesTbody().children.item(6)).children.item(0);
    expect(numCell?.className).toBe("ep-num");
    const aired = numCell?.querySelector("span");
    // Its own class so the aired number can be de-emphasised beside the
    // absolute one instead of reading as part of the same label.
    expect(aired?.className).toBe("ep-aired");
    expect(aired?.textContent).toBe(" E01");
  });

  it("adds no absolute-order chrome when the absolute number just counts up", () => {
    // Sonarr sets absolute_episode on every multi-season show, so a running
    // count must not turn every row into a two-number cell with a tooltip.
    const seasons: SeasonGroup[] = [
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "A", has_file: true, absolute_episode: 1 },
          { id: 12, season: 1, episode: 2, title: "B", has_file: true, absolute_episode: 2 },
        ],
      },
    ];

    renderSeriesDetail(makeSeries(337, "Show AL"), seasons, [], new Set());

    const numCell = reqRow(seriesTbody().children.item(2)).children.item(0);
    expect(numCell?.textContent).toBe("E01");
    expect(numCell?.getAttribute("data-tip")).toBeNull();
    expect(numCell?.querySelector("span.ep-aired")).toBeNull();
  });

  it("marks the subtitle-count badge ok and the empty badge err with no language rule", () => {
    const series: SeriesItem = { ...makeSeries(338, "Show AM"), targets: [] };

    renderSeriesDetail(
      series,
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-338-s01e01", 80)],
      new Set(),
    );

    const counted = reqRow(seriesTbody().children.item(2)).querySelector("td.ep-coverage span");
    expect(counted?.className).toBe("badge");
    expect(counted?.getAttribute("data-status")).toBe("ok");
    expect(counted?.textContent).toBe("1 subs");
    const empty = reqRow(seriesTbody().children.item(3)).querySelector("td.ep-coverage span");
    expect(empty?.className).toBe("badge");
    expect(empty?.getAttribute("data-status")).toBe("err");
    expect(empty?.textContent).toBe(DASH);
  });

  it("reads an embedded track in a codec nobody ignores as ordinary coverage", () => {
    // "Ignored codec" needs BOTH halves: an embedded track AND an ignored
    // codec. An embedded ass track with only pgs ignored is usable.
    storeState.ignoredCodecs = new Set(["pgs"]);
    const embedded: SubtitleEntry = {
      media_id: "tvdb-339-s01e01",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    renderSeriesDetail(
      makeSeries(339, "Show AN"),
      makeSeasons("Pilot", "Second", "Return"),
      [embedded],
      new Set(),
    );

    const badge = reqRow(seriesTbody().children.item(2)).querySelector("td.ep-coverage span.badge");
    expect(badge?.getAttribute("data-status")).toBe("ok");
    expect(badge?.getAttribute("data-tip")).toBeNull();
  });

  it("reads a downloaded subtitle as ordinary coverage even in an ignored codec", () => {
    // The ignore settings filter EMBEDDED tracks; a file subflux downloaded
    // itself is coverage whatever its codec.
    storeState.ignoredCodecs = new Set(["srt"]);

    renderSeriesDetail(
      makeSeries(340, "Show AO"),
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-340-s01e01", 80)],
      new Set(),
    );

    const badge = reqRow(seriesTbody().children.item(2)).querySelector("td.ep-coverage span.badge");
    expect(badge?.getAttribute("data-status")).toBe("ok");
    expect(badge?.getAttribute("data-tip")).toBeNull();
  });

  it("leaves a fileless episode's subtitles out of the season sync set", () => {
    // Audio sync aligns a subtitle against the video's audio track, so an
    // episode Sonarr has not imported has nothing to sync against.
    const seasons: SeasonGroup[] = [
      {
        season: 1,
        episodes: [
          { id: 11, season: 1, episode: 1, title: "Imported", has_file: true },
          { id: 12, season: 1, episode: 2, title: "Not imported", has_file: false },
        ],
      },
    ];

    renderSeriesDetail(
      makeSeries(330, "Show AE"),
      seasons,
      [epSub("tvdb-330-s01e02", 80)],
      new Set(),
    );

    expect(seriesTbody().children.length).toBe(3); // head, cols, the imported ep
    expect(
      document.querySelector("tr.season-head [data-tip='Audio sync all subtitles in this season']"),
    ).toBeNull();
  });

  it("leaves embedded tracks out of the season sync set", () => {
    // An embedded track lives inside the container; there is no sidecar file
    // to retime, so a season of them offers no season sync.
    const embedded: SubtitleEntry = {
      media_id: "tvdb-331-s01e01",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    renderSeriesDetail(
      makeSeries(331, "Show AF"),
      makeSeasons("Pilot", "Second", "Return"),
      [embedded],
      new Set(),
    );

    expect(
      document.querySelector("tr.season-head [data-tip='Audio sync all subtitles in this season']"),
    ).toBeNull();
  });

  it("replaces the Files button on a re-render instead of adding a second one", () => {
    storeState.isAdmin = true;
    const series = makeSeries(332, "Show AG");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    const subs = [epSub("tvdb-332-s01e01", 80)];

    renderSeriesDetail(series, seasons, subs, new Set());
    expect(document.querySelectorAll('[data-nav="files"]')).toHaveLength(1);

    // A coverage refresh runs the whole header path again.
    renderSeriesDetail(series, seasons, subs, new Set());
    expect(document.querySelectorAll('[data-nav="files"]')).toHaveLength(1);
  });

  it("leaves an unchanged row's own element untouched across a coverage refresh", () => {
    // The refresh contract is an IN-PLACE row update, not a table re-render.
    // A marker attribute is the probe: a re-render patches the live table and
    // equalises attributes against freshly-built rows, which strips an
    // attribute those rows do not carry, while an in-place update whose
    // signature matched never touches the row at all.
    const series = makeSeries(333, "Show AH");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    renderSeriesDetail(series, seasons, [epSub("tvdb-333-s01e01", 80)], new Set());

    reqRow(seriesTbody().children.item(2)).setAttribute("data-probe", "kept");

    renderSeriesDetail(
      series,
      seasons,
      [epSub("tvdb-333-s01e01", 80), epSub("tvdb-333-s01e02", 70)],
      new Set(),
    );

    expect(covText(seriesTbody().children.item(3))).toBe(`ensrt: ext ${STAR}70`);
    expect(reqRow(seriesTbody().children.item(2)).getAttribute("data-probe")).toBe("kept");
  });
});

describe("detail: openSeriesDetail panel", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    storeState.sets = [];
    clientState.stateIDs = null;
    clientState.seasons = null;
    clientState.seasonsError = null;
    clientState.defer = false;
    clientState.pending = [];
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("configures the panel for a detail view with the library controls hidden", () => {
    openSeriesViaBus(makeSeries(410, "Show AP"));

    // `visible: false` is what hides the library's filter row: a detail view
    // has nothing to filter, and leaving it up offers controls that do nothing.
    expect(emit).toHaveBeenCalledWith(BusEvent.PanelConfigure, {
      visible: false,
      detail: {
        title: "Show AP",
        info: "3 ep \u00B7 audio: English \u00B7 subs: en",
        backPath: "/",
        arrLink: null,
        arrName: "Sonarr",
      },
    });
  });

  it("collapses each run of non-alphanumerics in the Sonarr slug to one dash", () => {
    storeState.config = { sonarr_url: "http://sonarr:8989" };

    openSeriesViaBus(makeSeries(411, "Law & Order: Special Victims Unit"));

    // " & " and ": " are runs of three and two non-alphanumerics; Sonarr's
    // slug carries one dash per run, so a per-character replacement would
    // produce a URL that resolves to nothing.
    expect(panelDetail()["arrLink"]).toBe(
      "http://sonarr:8989/series/law-order-special-victims-unit",
    );
  });
});

describe("detail: openMovieDetail chrome", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    storeState.sets = [];
    clientState.stateIDs = null;
    clientState.movieSubs = [];
    clientState.movieSubsCalls = 0;
    clientState.subsDefer = false;
    clientState.subsPending = [];
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("offers the Files button when only SOME subtitles are embedded tracks", async () => {
    // The file manager lists sidecar files; one downloaded subtitle among
    // embedded tracks is enough for there to be something to manage.
    storeState.isAdmin = true;
    const embedded: SubtitleEntry = {
      media_id: "tmdb-95",
      language: "en",
      variant: "standard",
      source: EMBEDDED,
      codec: "ass",
      score: 0,
      ordinal: 0,
    };

    clientState.movieSubs = [embedded, movieSub("fr", 70)];
    await openMovieSettled(makeMovie(95));

    expect(document.querySelector('[data-nav="files"]')).not.toBeNull();
  });

  it("renders the movie table for the bus event coverage.ts publishes", async () => {
    const handler = busHandlers.map.get("open:movie");
    if (!handler) {
      throw new Error("open:movie handler not registered");
    }

    clientState.movieSubs = [movieSub("en", 90)];
    (handler as (p: { item: MovieDetail }) => void)({ item: makeMovie(96) });
    await Promise.resolve();
    await Promise.resolve();

    expect(movieTbody().children.length).toBe(2); // en, fr (target order)
    expect(covText(movieTbody().children.item(0))).toBe(`srt: ext ${STAR}90`);
  });

  it("leaves an unchanged language row's own element untouched across a refresh", async () => {
    // Same probe as the series table: a marker attribute survives an in-place
    // row update and is equalised away by a table re-render.
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(97));

    reqRow(movieTbody().children.item(0)).setAttribute("data-probe", "kept");

    clientState.movieSubs = [movieSub("en", 90), movieSub("fr", 85)];
    await openMovieSettled(makeMovie(97), true);

    expect(covText(movieTbody().children.item(1))).toBe(`srt: ext ${STAR}85`);
    expect(reqRow(movieTbody().children.item(0)).getAttribute("data-probe")).toBe("kept");
  });

  it("renders the exact markup shipped before the series-detail conversion (no-change control)", async () => {
    // C1 converts the SERIES detail out of the table formatting context;
    // movie-detail stays a real table. This pins the movie table's whole
    // rendered artifact so any drift the conversion causes here fails loudly.
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(98));

    const table = document.querySelector("table.movie-detail");
    if (!table) {
      throw new Error("movie table missing");
    }
    expect(table.outerHTML).toMatchInlineSnapshot(
      `"<table class="movie-detail" data-movie-id="98"><thead><tr><th>Language</th><th>Subtitles</th><th></th></tr></thead><tbody><tr data-sig="external\\\\\\:srt\\\\\\:90:" data-reconcile-key="en:standard"><td>English</td><td class="ep-coverage"><span class="badge" data-status="ok">srt: ext ★90</span></td><td data-col="actions"><button type="button" class="ghost" data-tip="Search English subtitles"><span class="icon icon-search"></span><span class="btn-text"> Search</span></button></td></tr><tr data-sig=":" data-reconcile-key="fr:standard"><td>French</td><td class="ep-coverage"><span class="badge" data-status="err">—</span></td><td data-col="actions"><button type="button" class="ghost" data-tip="Search French subtitles"><span class="icon icon-search"></span><span class="btn-text"> Search</span></button></td></tr></tbody></table>"`,
    );
  });
});

describe("detail: release on leave (C2)", () => {
  beforeEach(() => {
    storeState.ignoredCodecs = new Set<string>();
    storeState.isAdmin = false;
    storeState.config = null;
    clientState.movieSubs = [];
    clientState.subsDefer = false;
    clientState.subsPending = [];
    clientState.stateIDs = null;
    history.replaceState(null, "", "/");
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
      '<div id="coverageContent"></div></div>';
  });

  it("a disposed series binding never reuses: the next render rebuilds fresh row effects", () => {
    const series = makeSeries(345, "Show AW");
    const seasons = makeSeasons("Pilot", "Second", "Return");
    renderSeriesDetail(series, seasons, [], new Set());
    const before = seriesTbody();

    // Same series, still-connected tbody: the render path REUSES the binding.
    renderSeriesDetail(series, seasons, [], new Set());
    expect(seriesTbody()).toBe(before);

    disposeDetailBindings();

    // The leave path dropped the binding (page-leg's abortPageLeg owns the
    // call): nothing stays subscribed to the departed view, so an identical
    // render REBUILDS instead of feeding the released collection.
    renderSeriesDetail(series, seasons, [], new Set());
    expect(seriesTbody()).not.toBe(before);
  });

  it("a disposed movie binding never reuses either", async () => {
    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(99));
    const before = movieTbody();

    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(99), true);
    expect(movieTbody()).toBe(before);

    disposeDetailBindings();

    clientState.movieSubs = [movieSub("en", 90)];
    await openMovieSettled(makeMovie(99), true);
    expect(movieTbody()).not.toBe(before);
  });
});
