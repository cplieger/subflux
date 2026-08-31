// detail-table-layout.test.ts — C1/C3 geometry: the DESKTOP series episode
// table leaves the table formatting context; the mobile branch and the
// movie-detail table must not move.
//
// Unlike detail.test.ts (markup-level, no CSS), this suite injects the REAL
// stylesheets and asserts computed styles and geometry in Chromium. The card
// wrapper mirrors index.html, so the `card` container queries evaluate as
// shipped; breakpoints are driven by card WIDTH (container queries), never by
// viewport size. Pixel-level screenshot parity at both breakpoints stays a
// manual sidecar sign-off (UX flags 6/7); these tests pin the geometry a
// screenshot diff would otherwise have to catch. The desktop parity numbers
// were measured on the shipped <table> BEFORE the conversion (2 seasons x 3
// episodes, 1280x720 viewport, row content width 1238px) and re-asserted
// against the grid.
import { describe, it, vi, beforeEach, expect } from "vitest";

import tokensCSS from "./css/_shared-tokens.css?raw";
import baseCSS from "./css/02-base.css?raw";
import componentsCSS from "./css/03-components.css?raw";
import cardCSS from "./css/05-card.css?raw";
import tableCSS from "./css/06-table.css?raw";

// Per-test wiring lives in hoisted mutable records read by plain-function
// factories (vitest.config's mockReset strips vi.fn implementations; see
// detail.test.ts's header note).
const clientState = vi.hoisted(() => ({
  movieSubs: [] as unknown[] | null,
}));
vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: () => Promise.resolve(null),
  coverageSeriesDetail: () => Promise.resolve([]),
  coverageMovieSubs: () => Promise.resolve(clientState.movieSubs),
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
vi.mock("./sync.js", () => ({
  openSyncDialog: () => undefined,
  confirmSeasonSync: () => undefined,
}));
vi.mock("./files.js", () => ({ openFileManager: () => undefined }));
vi.mock("./config.js", () => ({ openConfig: () => undefined }));
vi.mock("./detail-scan.js", () => ({
  triggerSeriesScan: () => undefined,
  triggerSeasonScan: () => undefined,
  triggerMovieScan: () => undefined,
  registerScanButton: () => undefined,
}));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => (k === "ignoredCodecs" ? new Set<string>() : null),
  set: () => undefined,
}));

import { renderSeriesDetail, openMovieDetail } from "./detail.js";
import type { SeriesItem, SeasonGroup, SubtitleEntry, MovieDetail } from "./api-types.js";

// The real stylesheets, once per file. Order matches the MANIFEST slice this
// suite exercises (tokens -> base -> components -> card -> table).
const style = document.createElement("style");
style.textContent = [tokensCSS, baseCSS, componentsCSS, cardCSS, tableCSS].join("\n");
document.head.appendChild(style);

// --- Fixtures ---

function makeSeries(tvdbId: number, episodes = 3): SeriesItem {
  return {
    title: `Show ${tvdbId}`,
    audio_lang: "en",
    rule: "en",
    id: tvdbId,
    year: 2020,
    tvdb_id: tvdbId,
    episodes,
    targets: [{ language: "en", variant: "standard", have: 0, total: episodes, have_ignored: 0 }],
  };
}

/** `seasonCount` seasons of `epsPerSeason` file-bearing episodes each. */
function makeSeasons(seasonCount: number, epsPerSeason: number): SeasonGroup[] {
  const seasons: SeasonGroup[] = [];
  for (let s = 1; s <= seasonCount; s++) {
    const episodes = [];
    for (let e = 1; e <= epsPerSeason; e++) {
      episodes.push({
        id: s * 100 + e,
        season: s,
        episode: e,
        title: `Episode ${s}x${e}`,
        has_file: true,
      });
    }
    seasons.push({ season: s, episodes });
  }
  return seasons;
}

function epSub(mediaId: string, score: number): SubtitleEntry {
  return {
    media_id: mediaId,
    language: "en",
    variant: "standard",
    source: "external",
    codec: "srt",
    score,
    ordinal: 0,
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

/** Open a movie detail and let the on-demand /subs read land (mock resolves
 *  inside the skeleton's 150ms show-delay, so two ticks drain the chain). */
async function openMovieSettled(m: MovieDetail): Promise<void> {
  openMovieDetail(m, true);
  await Promise.resolve();
  await Promise.resolve();
}

/** Build the index.html slice the detail views render into: a real `.card`
 *  container section. `width` pins the card's inline size so the container
 *  query resolves the chosen breakpoint (>=700px desktop, <700px mobile). */
function mountPanel(width?: string): HTMLElement {
  document.body.innerHTML =
    '<section class="card" id="coveragePanel">' +
    '<div class="card-head"><h2 id="lib-heading">Show 1</h2></div>' +
    '<div id="coverageContent"></div></section>';
  const panel = document.getElementById("coveragePanel");
  if (!panel) {
    throw new Error("panel missing");
  }
  if (width) {
    panel.style.width = width;
  }
  return panel;
}

/** Two rendering opportunities: content-visibility relevancy is determined
 *  at frame production, so a row's skipped/rendered state (and any auto
 *  track sized by its contents) settles one frame after mutation. */
function settled(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        resolve();
      });
    });
  });
}

function seriesTable(): HTMLTableElement {
  const t = document.querySelector<HTMLTableElement>("table.series-detail");
  if (!t) {
    throw new Error("series table not mounted");
  }
  return t;
}

function rows(): HTMLElement[] {
  return [...seriesTable().querySelectorAll<HTMLElement>("tbody tr")];
}

/** First episode row (no class; season-head/season-gap rows carry one). */
function episodeRow(): HTMLElement {
  const r = rows().find((tr) => tr.className === "");
  if (!r) {
    throw new Error("no episode row");
  }
  return r;
}

function episodeRows(): HTMLElement[] {
  return rows().filter((tr) => tr.className === "");
}

function seasonHeadRow(): HTMLElement {
  const r = rows().find((tr) => tr.className === "season-head" && tr.querySelector("td"));
  if (!r) {
    throw new Error("no season-head row");
  }
  return r;
}

function colHeaderRow(): HTMLElement {
  const r = rows().find((tr) => tr.querySelector("th"));
  if (!r) {
    throw new Error("no column-header row");
  }
  return r;
}

function seasonGapRow(): HTMLElement {
  const r = rows().find((tr) => tr.className === "season-gap");
  if (!r) {
    throw new Error("no season-gap row");
  }
  return r;
}

function renderDefault(): void {
  renderSeriesDetail(
    makeSeries(1),
    makeSeasons(2, 3),
    [epSub("tvdb-1-s01e01", 80)],
    new Set(["tvdb-1-s01e01"]),
  );
}

beforeEach(() => {
  window.scrollTo(0, 0);
  clientState.movieSubs = [];
});

// --- C1 desktop conversion: out of the table formatting context ---
describe("series-detail desktop conversion", () => {
  beforeEach(async () => {
    mountPanel();
    renderDefault();
    await settled();
  });

  it("takes the table and tbody out of the table formatting context", () => {
    const table = seriesTable();
    expect(getComputedStyle(table).display).toBe("block");
    expect(getComputedStyle(table.tBodies[0]!).display).toBe("block");
  });

  it("lays every row kind on ONE shared column template custom property", () => {
    const table = seriesTable();
    // The property is the single owner of the widths the <colgroup> carried.
    expect(getComputedStyle(table).getPropertyValue("--series-detail-cols").trim()).toBe(
      "8% 34% 34% 24%",
    );
    const kinds = [episodeRow(), seasonHeadRow(), colHeaderRow(), seasonGapRow()];
    const templates = kinds.map((r) => getComputedStyle(r).gridTemplateColumns);
    for (const [i, r] of kinds.entries()) {
      expect(getComputedStyle(r).display).toBe("grid");
      // Resolved track lists are identical across the four row kinds.
      expect(templates[i]).toBe(templates[0]);
    }
    expect(templates[0]!.split(" ")).toHaveLength(4);
  });

  it("keeps the retired colgroup's 8/34/34/24 column ratios", () => {
    const ep = episodeRow();
    const width = ep.getBoundingClientRect().width;
    const tracks = getComputedStyle(ep).gridTemplateColumns.split(" ").map(parseFloat);
    const expected = [0.08, 0.34, 0.34, 0.24].map((f) => f * width);
    for (const [i, track] of tracks.entries()) {
      expect(Math.abs(track - expected[i]!)).toBeLessThan(1);
    }
  });

  it("spans the season-head label across the data columns without clipping", () => {
    const head = seasonHeadRow();
    const label = head.children[0] as HTMLElement;
    const cs = getComputedStyle(label);
    expect(cs.gridColumnStart).toBe("1");
    expect(cs.gridColumnEnd).toBe("-2");
    // Spans tracks 1-3 (76% of the row), not the old 8% first column.
    const rowWidth = head.getBoundingClientRect().width;
    expect(Math.abs(label.getBoundingClientRect().width - rowWidth * 0.76)).toBeLessThan(1);
    // Unclipped: the label text fits its box.
    expect(label.scrollWidth).toBeLessThanOrEqual(label.clientWidth);
    // The two empty layout cells stay hidden; actions takes the last column.
    expect(getComputedStyle(head.children[1] as HTMLElement).display).toBe("none");
    expect(getComputedStyle(head.children[2] as HTMLElement).display).toBe("none");
    const actions = head.children[3] as HTMLElement;
    expect(getComputedStyle(actions).gridColumnStart).toBe("-2");
    expect(getComputedStyle(actions).gridColumnEnd).toBe("-1");
  });

  it("spans the season-gap spacer across every column (the colSpan:999 successor)", () => {
    const gap = seasonGapRow();
    const cell = gap.querySelector("td") as HTMLElement;
    const cs = getComputedStyle(cell);
    expect(cs.gridColumnStart).toBe("1");
    expect(cs.gridColumnEnd).toBe("-1");
    expect(
      Math.abs(cell.getBoundingClientRect().width - gap.getBoundingClientRect().width),
    ).toBeLessThan(1);
  });

  it("keeps the shipped row heights (measured on the table before conversion)", () => {
    // Episode row: 48px border-box (47px content+padding, 1px row border) —
    // identical to the shipped table. Season-head 48px and gap 16px carry
    // the 0.5px the collapsed-border model used to attribute to neighbours;
    // the boundary geometry (content edge to content edge) is unchanged.
    expect(episodeRow().getBoundingClientRect().height).toBe(48);
    expect(episodeRow().clientHeight).toBe(47);
    expect(seasonHeadRow().getBoundingClientRect().height).toBe(48);
    expect(Math.abs(colHeaderRow().getBoundingClientRect().height - 34.27)).toBeLessThan(0.5);
    expect(seasonGapRow().getBoundingClientRect().height).toBe(16);
  });

  it("keeps the action buttons at the row's end edge", () => {
    const ep = episodeRow();
    const group = ep.querySelector<HTMLElement>('[data-col="actions"] .action-group');
    if (!group) {
      throw new Error("action group missing");
    }
    const rowRect = ep.getBoundingClientRect();
    const groupRect = group.getBoundingClientRect();
    // text-align: end + the cell's --sp-4 padding: the group's right edge
    // sits 8px inside the row's right edge, as in the shipped table.
    expect(Math.abs(rowRect.right - 8 - groupRect.right)).toBeLessThan(1);
  });
});

// --- Containment: episode rows render on demand (C1) ---
describe("series-detail containment", () => {
  it("marks episode rows content-visibility:auto with the per-breakpoint estimate (desktop)", async () => {
    mountPanel();
    renderDefault();
    await settled();
    const ep = episodeRow();
    expect(getComputedStyle(ep).contentVisibility).toBe("auto");
    expect(getComputedStyle(ep).containIntrinsicSize).toBe("auto 47px");
    // Structural rows stay always-rendered: they are the scroll landmarks.
    expect(getComputedStyle(seasonHeadRow()).contentVisibility).toBe("visible");
    expect(getComputedStyle(seasonGapRow()).contentVisibility).toBe("visible");
  });

  it("marks episode rows content-visibility:auto with the per-breakpoint estimate (mobile)", async () => {
    mountPanel("400px");
    renderDefault();
    await settled();
    const ep = episodeRow();
    expect(getComputedStyle(ep).contentVisibility).toBe("auto");
    expect(getComputedStyle(ep).containIntrinsicSize).toBe("auto 54px");
  });

  it("skips far-away rows and renders them as they scroll into view", async () => {
    mountPanel();
    renderSeriesDetail(makeSeries(1), makeSeasons(3, 30), [], new Set());
    await settled();
    const eps = episodeRows();
    const last = eps[eps.length - 1]!;
    const lastCell = last.querySelector("td") as HTMLElement;

    // Far below the viewport: the row's CONTENTS are skipped (checkVisibility
    // reads the skip state; box geometry is cached for skipped content, so a
    // rect is not the observable) while the row itself stands in at the
    // intrinsic estimate. Skipping starts on a rendering opportunity after
    // the initial layout, so the first observation waits for it.
    await vi.waitFor(() => {
      expect(lastCell.checkVisibility({ contentVisibilityAuto: true })).toBe(false);
    });
    expect(last.getBoundingClientRect().height).toBeGreaterThan(0);

    // Driven scroll INTO view: contents come back.
    last.scrollIntoView();
    await vi.waitFor(() => {
      expect(lastCell.checkVisibility({ contentVisibilityAuto: true })).toBe(true);
    });
    expect(lastCell.getBoundingClientRect().height).toBeGreaterThan(0);

    // And the rows scrolled AWAY (top of the table) are now skipped.
    const first = eps[0]!;
    const firstCell = first.querySelector("td") as HTMLElement;
    await vi.waitFor(() => {
      expect(firstCell.checkVisibility({ contentVisibilityAuto: true })).toBe(false);
    });
  });

  it("keeps row heights and scroll offset stable across an in-place heal", async () => {
    mountPanel();
    const series = makeSeries(1);
    const seasons = makeSeasons(2, 20);
    renderSeriesDetail(series, seasons, [], new Set());
    await settled();
    const tbody = seriesTable().tBodies[0]!;
    const heightsBefore = episodeRows().map((r) => r.getBoundingClientRect().height);

    window.scrollTo(0, 400);
    await settled();
    const scrollBefore = window.scrollY;
    expect(scrollBefore).toBe(400);

    // A heal repaints ONE row in place (REUSE path: same series, same tbody).
    renderSeriesDetail(series, seasons, [epSub("tvdb-1-s01e05", 90)], new Set());
    await settled();

    expect(seriesTable().tBodies[0]).toBe(tbody); // reused, not rebuilt
    expect(window.scrollY).toBe(scrollBefore);
    const heightsAfter = episodeRows().map((r) => r.getBoundingClientRect().height);
    expect(heightsAfter).toStrictEqual(heightsBefore);
  });
});

// --- Mobile branch: VERBATIM across the conversion (C1). Characterized
// against the shipped tree; these pins held before the conversion too. ---
describe("series-detail mobile branch (verbatim)", () => {
  beforeEach(async () => {
    mountPanel("400px");
    renderDefault();
    await settled();
  });

  it("keeps the episode row a 3-column card grid at the shipped height", () => {
    const ep = episodeRow();
    const cs = getComputedStyle(ep);
    expect(cs.display).toBe("grid");
    // 2.5rem sidebar resolves to 40px; title takes the fraction, actions auto.
    expect(cs.gridTemplateColumns.startsWith("40px ")).toBe(true);
    expect(cs.gridTemplateColumns.split(" ")).toHaveLength(3);
    // Shipped mobile card geometry, measured before the conversion.
    expect(ep.getBoundingClientRect().height).toBe(72);
    expect(ep.clientHeight).toBe(70);
  });

  it("keeps the season head a flex row and hides the column headers", () => {
    expect(getComputedStyle(seasonHeadRow()).display).toBe("flex");
    expect(getComputedStyle(colHeaderRow()).display).toBe("none");
  });

  it("keeps the season gap a block with its cell hidden", () => {
    const gap = seasonGapRow();
    expect(getComputedStyle(gap).display).toBe("block");
    const cell = gap.querySelector("td");
    expect(cell).not.toBeNull();
    expect(getComputedStyle(cell as HTMLElement).display).toBe("none");
  });
});

// --- Movie detail: the no-change CONTROL (stays a table at desktop, the
// shipped grid card on mobile). Characterized against the shipped tree. ---
describe("movie-detail control (no change)", () => {
  it("desktop: stays a real table formatting context", async () => {
    mountPanel();
    await openMovieSettled(makeMovie(9));
    const table = document.querySelector<HTMLElement>("table.movie-detail");
    if (!table) {
      throw new Error("movie table missing");
    }
    expect(getComputedStyle(table).display).toBe("table");
    const tr = table.querySelector<HTMLElement>("tbody tr");
    expect(getComputedStyle(tr as HTMLElement).display).toBe("table-row");
    const td = table.querySelector<HTMLElement>("tbody td");
    expect(getComputedStyle(td as HTMLElement).display).toBe("table-cell");
  });

  it("mobile: keeps the shipped single-row grid", async () => {
    mountPanel("400px");
    await openMovieSettled(makeMovie(9));
    const tr = document.querySelector<HTMLElement>("table.movie-detail tbody tr");
    if (!tr) {
      throw new Error("movie row missing");
    }
    const cs = getComputedStyle(tr);
    expect(cs.display).toBe("grid");
    // 4.8rem label column resolves to ~76.8px (serialized rounded).
    expect(parseFloat(cs.gridTemplateColumns)).toBeCloseTo(76.8, 1);
    expect(cs.gridTemplateColumns.split(" ")).toHaveLength(3);
  });
});
