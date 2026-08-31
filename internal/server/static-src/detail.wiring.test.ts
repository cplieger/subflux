//
// detail.wiring.test.ts — the parts of detail.ts that detail.test.ts cannot
// reach with its mock set: the action buttons' click wiring (which needs the
// collaborators as spies rather than no-ops), the anti-flicker skeleton (which
// needs fake timers and a load that stays pending past the show delay), the
// abort plumbing (which needs a deferred REJECT and a capturable page-teardown
// hook), and the request arguments each navigation issues.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// vitest.config sets clearMocks/mockReset/restoreMocks, which strips a vi.fn's
// implementation before each test. Factories whose BEHAVIOUR must survive are
// plain functions closing over hoisted mutable records; vi.fn() is used only
// where call recording is the whole job.
const clientState = vi.hoisted(() => ({
  calls: [] as { name: string; args: unknown[] }[],
  seasons: null as unknown,
  stateIDs: null as string[] | null,
  deferSeasons: false,
  deferStateIDs: false,
  pendingSeasons: [] as { resolve: (v: unknown) => void; reject: (e: unknown) => void }[],
  pendingStateIDs: [] as ((v: unknown) => void)[],
}));
vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: (...args: unknown[]) => {
    clientState.calls.push({ name: "mediaEpisodes", args });
    if (clientState.deferSeasons) {
      return new Promise((resolve, reject) => {
        clientState.pendingSeasons.push({ resolve: resolve as (v: unknown) => void, reject });
      });
    }
    return Promise.resolve(clientState.seasons);
  },
  coverageSeriesDetail: (...args: unknown[]) => {
    clientState.calls.push({ name: "coverageSeriesDetail", args });
    return Promise.resolve([]);
  },
  coverageMovieSubs: (...args: unknown[]) => {
    clientState.calls.push({ name: "coverageMovieSubs", args });
    return Promise.resolve([]);
  },
  stateIDs: (...args: unknown[]) => {
    clientState.calls.push({ name: "stateIDs", args });
    if (clientState.deferStateIDs) {
      return new Promise((resolve) => {
        clientState.pendingStateIDs.push(resolve as (v: unknown) => void);
      });
    }
    return Promise.resolve(clientState.stateIDs);
  },
}));
// detail.ts registers ONE page-teardown hook at import time; capturing it is
// the only way to drive the unload path.
const cleanupState = vi.hoisted(() => ({ fns: [] as (() => void)[] }));
vi.mock("@cplieger/actions", () => ({
  registerCleanup: (fn: () => void) => {
    cleanupState.fns.push(fn);
  },
}));
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
vi.mock("./search.js", () => ({ openSearchPopup: vi.fn() }));
vi.mock("./sync.js", () => ({ openSyncDialog: vi.fn(), confirmSeasonSync: vi.fn() }));
vi.mock("./files.js", () => ({ openFileManager: vi.fn() }));
vi.mock("./config.js", () => ({ openConfig: vi.fn() }));
vi.mock("./detail-scan.js", () => ({
  triggerSeriesScan: vi.fn(),
  triggerSeasonScan: vi.fn(),
  triggerMovieScan: vi.fn(),
  registerScanButton: vi.fn(),
}));
const storeState = vi.hoisted(() => ({
  ignoredCodecs: new Set<string>(),
  isAdmin: false,
  config: null as { sonarr_url?: string; radarr_url?: string } | null,
  sets: [] as [string, unknown][],
}));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => {
    if (k === "ignoredCodecs") {
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

import { renderSeriesDetail, openMovieDetail } from "./detail.js";
import { openSearchPopup } from "./search.js";
import { confirmSeasonSync } from "./sync.js";
import { registerScanButton } from "./detail-scan.js";
import { seasonScopeKey } from "./scan-scope.js";
import type { SeriesItem, SeasonGroup, SubtitleEntry, MovieDetail } from "./api-types.js";

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
    targets: [{ language: "en", variant: "standard", have: 0, total: 1, have_ignored: 0 }],
    tmdb_id: tmdbId,
    id: tmdbId,
    year: 2021,
    has_file: true,
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

function reqRow(row: Element | null): HTMLElement {
  if (!(row instanceof HTMLElement)) {
    throw new Error("row missing");
  }
  return row;
}

/** Drive detail.ts through the bus handler coverage.ts publishes to. */
function openSeriesViaBus(item: SeriesItem): void {
  const handler = busHandlers.map.get("open:series");
  if (!handler) {
    throw new Error("open:series handler not registered");
  }
  (handler as (p: { item: SeriesItem }) => void)({ item });
}

/** Drain the microtask queue so a settled fetch's `.then` chain has run. Does
 *  not advance timers, so a pending show-delay stays pending. */
async function flush(): Promise<void> {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
  }
}

const PANEL_HTML =
  '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div>' +
  '<div id="coverageContent"></div></div>';

function resetEnv(): void {
  storeState.ignoredCodecs = new Set<string>();
  storeState.isAdmin = false;
  storeState.config = null;
  storeState.sets = [];
  clientState.calls = [];
  clientState.seasons = null;
  clientState.stateIDs = null;
  clientState.deferSeasons = false;
  clientState.deferStateIDs = false;
  clientState.pendingSeasons = [];
  clientState.pendingStateIDs = [];
  history.replaceState(null, "", "/");
  document.body.innerHTML = PANEL_HTML;
}

describe("detail: episode and season action buttons", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("wires the episode Search button to the manual search popup", () => {
    const series = makeSeries(350, "Show AQ");
    const seasons = makeSeasons("Pilot", "Second", "Return");

    renderSeriesDetail(series, seasons, [], new Set());

    const group = reqRow(seriesTbody().children.item(2)).querySelector(
      '[data-col="actions"] .action-group',
    );
    const searchBtn = group?.lastElementChild as HTMLButtonElement | null;
    if (!searchBtn) {
      throw new Error("episode search button missing");
    }
    expect(searchBtn.tagName).toBe("BUTTON");
    // type=button so the row's button never submits an enclosing form; the
    // tooltip is how the manual path is distinguished from the season scan.
    expect(searchBtn.getAttribute("type")).toBe("button");
    expect(searchBtn.className).toBe("ghost");
    expect(searchBtn.getAttribute("data-tip")).toBe(
      "Manual: browse and pick subtitles for this episode",
    );
    expect(searchBtn.querySelector("span.btn-text")?.textContent).toBe(" Search");

    searchBtn.click();

    expect(openSearchPopup).toHaveBeenCalledWith("episode", series, 1, seasons[0]?.episodes[0]);
  });

  it("hands the season Search button to the scan-state applier", () => {
    // A row painted while a scan is already running must come up disabled with
    // a spinner; the applier is what reads the shared running-scans map.
    const series = makeSeries(351, "Show AR");

    renderSeriesDetail(series, makeSeasons("Pilot", "Second", "Return"), [], new Set());

    const seasonSearch = document.querySelector<HTMLButtonElement>(
      "tr.season-head button[data-scan-scope]",
    );
    if (!seasonSearch) {
      throw new Error("season search button missing");
    }
    expect(seasonSearch.getAttribute("data-scan-scope")).toBe(seasonScopeKey(351, 1));
    expect(registerScanButton).toHaveBeenCalledWith(seasonSearch);
  });

  it("confirms a season audio sync with the season's external subtitles", () => {
    const series = makeSeries(352, "Show AS");

    renderSeriesDetail(
      series,
      makeSeasons("Pilot", "Second", "Return"),
      [epSub("tvdb-352-s01e01", 80)],
      new Set(),
    );

    const syncBtn = document.querySelector<HTMLButtonElement>(
      "tr.season-head [data-tip='Audio sync all subtitles in this season']",
    );
    if (!syncBtn) {
      throw new Error("season sync button missing");
    }
    syncBtn.click();

    // Season sync routes through a confirmation; the dialog needs only the
    // batch scope (the server enumerates the files at acceptance) plus the
    // client-side count hint the confirm text shows.
    expect(confirmSeasonSync).toHaveBeenCalledWith("Show AS", 1, 352, 1);
  });

  it("wires a movie language row's Search button and keeps the click off the row", async () => {
    const movie = makeMovie(98);

    openMovieDetail(movie);
    await flush();

    const row = reqRow(movieTbody().children.item(0));
    const btn = row.querySelector<HTMLButtonElement>('[data-col="actions"] button');
    if (!btn) {
      throw new Error("movie search button missing");
    }
    let rowClicks = 0;
    row.addEventListener("click", () => {
      rowClicks++;
    });

    btn.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    // The popup opens for THIS row's language, and the row itself must not
    // also react — a row-level handler would fire a second navigation.
    expect(openSearchPopup).toHaveBeenCalledWith("movie", movie, null, null, "en");
    expect(rowClicks).toBe(0);
  });
});

describe("detail: loading skeleton", () => {
  beforeEach(() => {
    resetEnv();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("paints six skeleton rows only once the show delay has elapsed", () => {
    const out = document.querySelector("#coverageContent");
    const stale = document.createElement("div");
    stale.id = "stale";
    out?.appendChild(stale);
    clientState.deferSeasons = true;

    openSeriesViaBus(makeSeries(360, "Show AT"));

    // A load that settles inside the show-delay window paints no skeleton.
    expect(document.querySelectorAll("#coverageContent div.skeleton-row")).toHaveLength(0);
    expect(document.getElementById("stale")).not.toBeNull();

    vi.advanceTimersByTime(150);

    const rows = [...document.querySelectorAll("#coverageContent div.skeleton-row")];
    expect(rows).toHaveLength(6);
    // Each row is one shimmer bar; the row is the layout, the child animates.
    expect(rows.map((r) => r.children.length)).toEqual([1, 1, 1, 1, 1, 1]);
    expect(rows.map((r) => r.children.item(0)?.className)).toEqual([
      "skeleton",
      "skeleton",
      "skeleton",
      "skeleton",
      "skeleton",
      "skeleton",
    ]);
    // The skeleton REPLACES the outgoing view rather than stacking under it.
    expect(document.getElementById("stale")).toBeNull();
  });

  it("keeps the skeleton up for its minimum visible window before painting", async () => {
    clientState.deferSeasons = true;
    openSeriesViaBus(makeSeries(361, "Show AU"));
    vi.advanceTimersByTime(150);
    expect(document.querySelectorAll("#coverageContent div.skeleton-row")).toHaveLength(6);

    clientState.pendingSeasons[0]?.resolve(makeSeasons("Pilot", "Second", "Return"));
    await flush();

    // A load that settles just after the skeleton appeared must not yank it
    // away in the same frame — that flash is exactly what the delay prevents.
    expect(document.querySelector("table.series-detail")).toBeNull();
    expect(document.querySelectorAll("#coverageContent div.skeleton-row")).toHaveLength(6);

    vi.advanceTimersByTime(300);

    expect(document.querySelector("table.series-detail")).not.toBeNull();
    expect(document.querySelectorAll("#coverageContent div.skeleton-row")).toHaveLength(0);
  });

  it("suppresses a superseded navigation's delayed skeleton", async () => {
    clientState.deferSeasons = true;
    openSeriesViaBus(makeSeries(362, "First"));
    clientState.deferSeasons = false;
    clientState.seasons = makeSeasons("Fresh One", "Fresh Two", "Fresh Three");

    openSeriesViaBus(makeSeries(363, "Second"));
    await flush();
    expect(document.querySelector('table.series-detail[data-series-id="363"]')).not.toBeNull();

    // The abandoned navigation's show-delay timer still fires. Its aborted
    // signal is what keeps it from painting a skeleton over the live view.
    vi.advanceTimersByTime(200);

    expect(document.querySelectorAll("#coverageContent div.skeleton-row")).toHaveLength(0);
    expect(document.querySelector('table.series-detail[data-series-id="363"]')).not.toBeNull();
  });
});

describe("detail: navigation and cancellation", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("drops a superseded series rejection instead of painting its error", async () => {
    clientState.deferSeasons = true;
    openSeriesViaBus(makeSeries(364, "First"));
    clientState.deferSeasons = false;
    clientState.seasons = makeSeasons("Fresh One", "Fresh Two", "Fresh Three");

    openSeriesViaBus(makeSeries(365, "Second"));
    await flush();
    expect(document.querySelector('table.series-detail[data-series-id="365"]')).not.toBeNull();

    // The stale request fails LAST; its error belongs to a view the user left.
    clientState.pendingSeasons[0]?.reject(new Error("episodes unavailable"));
    await flush();

    // `div.empty[data-status="err"]` is the error panel; an empty coverage
    // badge carries the same data-status, so the class is what distinguishes.
    expect(document.querySelector('#coverageContent div.empty[data-status="err"]')).toBeNull();
    expect(seriesTbody().textContent).toContain("Fresh One");
  });

  it("rebuilds the table when the user navigates to the same series again", async () => {
    clientState.seasons = makeSeasons("Pilot", "Second", "Return");
    openSeriesViaBus(makeSeries(366, "Show AV"));
    await flush();
    const first = seriesTbody();

    openSeriesViaBus(makeSeries(366, "Show AV"));
    await flush();

    // A navigation always rebuilds. Reusing the previous binding would paint
    // the new response through row state that belongs to the old view; only a
    // coverage refresh (which paints no skeleton) reuses.
    expect(first.isConnected).toBe(false);
    expect(seriesTbody()).not.toBe(first);
    expect(seriesTbody().children.length).toBe(8);
  });

  it("requests episodes, coverage and history for the series it opens, all cancellable", async () => {
    clientState.seasons = makeSeasons("Pilot", "Second", "Return");

    openSeriesViaBus(makeSeries(367, "Show AW"));
    await flush();

    const eps = clientState.calls.find((c) => c.name === "mediaEpisodes");
    const cov = clientState.calls.find((c) => c.name === "coverageSeriesDetail");
    const ids = clientState.calls.find((c) => c.name === "stateIDs");
    expect(eps?.args[0]).toBe(367);
    expect(cov?.args[0]).toBe(367);
    // History is scoped to THIS series' episodes: an unscoped query would put
    // History buttons on rows whose downloads belong to another show.
    expect(ids?.args[0]).toEqual({ type: "episode", prefix: "tvdb-367-" });
    // A steady-state navigation is a PLAIN read: no ?recovery=1 query rides
    // the episodes request (task 1's recovery legs set it themselves).
    expect(eps?.args[1]).toBeUndefined();
    // Every request carries the navigation's signal, so the next navigation
    // CANCELS the transfer rather than only discarding its result.
    expect(eps?.args[2]).toEqual({ signal: expect.any(AbortSignal) });
    expect(cov?.args[1]).toEqual({ signal: expect.any(AbortSignal) });
    expect(ids?.args[1]).toEqual({ signal: expect.any(AbortSignal) });

    const epsSignal = (eps?.args[2] as { signal: AbortSignal } | undefined)?.signal;
    const covSignal = (cov?.args[1] as { signal: AbortSignal } | undefined)?.signal;
    const idsSignal = (ids?.args[1] as { signal: AbortSignal } | undefined)?.signal;
    const signals = [epsSignal, covSignal, idsSignal];
    expect(signals.map((s) => s?.aborted)).toEqual([false, false, false]);

    openSeriesViaBus(makeSeries(368, "Other"));
    await flush();

    expect(signals.map((s) => s?.aborted)).toEqual([true, true, true]);
  });

  it("requests the movie's rows and download history under its own id, cancellably", async () => {
    clientState.stateIDs = [];

    openMovieDetail(makeMovie(99));
    await flush();

    const subs = clientState.calls.find((c) => c.name === "coverageMovieSubs");
    expect(subs?.args[0]).toBe(99);
    expect(subs?.args[1]).toEqual({ signal: expect.any(AbortSignal) });
    const ids = clientState.calls.find((c) => c.name === "stateIDs");
    expect(ids?.args[0]).toEqual({ type: "movie", prefix: "tmdb-99" });
    expect(ids?.args[1]).toEqual({ signal: expect.any(AbortSignal) });

    const signal = (ids?.args[1] as { signal: AbortSignal } | undefined)?.signal;
    const subsSignal = (subs?.args[1] as { signal: AbortSignal } | undefined)?.signal;
    expect(signal?.aborted).toBe(false);
    expect(subsSignal?.aborted).toBe(false);

    openMovieDetail(makeMovie(100));
    await flush();

    expect(signal?.aborted).toBe(true);
    expect(subsSignal?.aborted).toBe(true);
  });

  it("adds no second History button when the header already has one", async () => {
    clientState.stateIDs = ["tmdb-101"];
    const movie = makeMovie(101);

    openMovieDetail(movie);
    await flush();
    expect(document.querySelectorAll('[data-nav="hist"]')).toHaveLength(1);

    // A re-open of the same movie resolves history again against a header that
    // already carries the button.
    openMovieDetail(movie, true);
    await flush();

    expect(document.querySelectorAll('[data-nav="hist"]')).toHaveLength(1);
  });

  it("aborts an in-flight detail fetch when the page tears down", async () => {
    expect(cleanupState.fns).toHaveLength(1);
    clientState.deferStateIDs = true;
    openMovieDetail(makeMovie(102));

    // Page unload: the module's cleanup hook aborts whatever is in flight, so
    // a late response cannot touch a document that is going away.
    for (const fn of cleanupState.fns) {
      fn();
    }
    clientState.pendingStateIDs[0]?.(["tmdb-102"]);
    await flush();

    expect(document.querySelectorAll('[data-nav="hist"]')).toHaveLength(0);
  });
});
