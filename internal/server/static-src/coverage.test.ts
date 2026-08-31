import { describe, it, vi, beforeEach, expect } from "vitest";

// CRITICAL: vitest.config has clearMocks/mockReset/restoreMocks=true, which
// strips a vi.fn's implementation before each test. Any mock whose behavior
// must persist across tests (resolved values, factory shapes, handlers called
// at module load) MUST be a PLAIN function, not a vi.fn(). Per-test wiring
// therefore lives in hoisted mutable records the plain factories close over.
const clientState = vi.hoisted(() => ({
  series: [] as unknown[] | null,
  movies: [] as unknown[] | null,
  seriesError: null as Error | null,
  // Signals handed to the generated client, newest last — the abort tests read
  // them to prove a superseded load was cancelled.
  signals: [] as (AbortSignal | undefined)[],
  // When set, the series fetch hands back a promise the test resolves by hand,
  // so a second load can start while the first is still in flight.
  defer: false,
  pending: [] as ((v: unknown) => void)[],
}));
vi.mock("./wire/client.gen.js", () => ({
  coverageSeries: (_query?: Record<string, unknown>, opts?: { signal?: AbortSignal }) => {
    clientState.signals.push(opts?.signal);
    if (clientState.defer) {
      return new Promise((resolve) => {
        clientState.pending.push(resolve as (v: unknown) => void);
      });
    }
    return clientState.seriesError
      ? Promise.reject(clientState.seriesError)
      : Promise.resolve(clientState.series);
  },
  coverageMovies: (_query?: Record<string, unknown>, opts?: { signal?: AbortSignal }) => {
    clientState.signals.push(opts?.signal);
    return Promise.resolve(clientState.movies);
  },
}));

// registerCleanup is called at module load with the abort-on-unload closure;
// capturing it is the only way to drive that path.
const cleanups = vi.hoisted(() => ({ fns: [] as (() => void)[] }));
vi.mock("@cplieger/actions", () => ({
  registerCleanup: (fn: () => void) => {
    cleanups.fns.push(fn);
  },
}));

// coverage.ts registers its PanelConfigure handler at import time; capturing
// the handlers lets a test drive that entry point, while `emitted` records the
// row-click / scan-button emissions.
const bus = vi.hoisted(() => ({
  handlers: new Map<string, (p: never) => void>(),
  emitted: [] as { event: string; payload: unknown }[],
}));
vi.mock("./bus.js", () => ({
  on: (event: string, handler: (p: never) => void) => {
    bus.handlers.set(event, handler);
    return () => undefined;
  },
  emit: (event: string, payload: unknown) => {
    bus.emitted.push({ event, payload });
  },
  BusEvent: {
    PanelConfigure: "panel:configure",
    OpenSeries: "open:series",
    OpenMovie: "open:movie",
    ScanSeries: "scan:series",
    ScanMovie: "scan:movie",
    CoverageOverwrite: "coverage:overwrite",
  },
}));
// Mocked whole: the real module registers apiActions at import time and pulls
// in the status.ts graph. coverage.ts only consumes registerScanButton.
vi.mock("./detail-scan.js", () => ({ registerScanButton: () => undefined }));
// Mutable store state so a test can flip the admin / unconfigured flags without
// re-mocking, and assert what coverage.ts wrote.
const storeState = vi.hoisted(() => ({
  isAdmin: false,
  isUnconfigured: false,
  sets: [] as [string, unknown][],
}));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => {
    if (k === "isAdmin") {
      return storeState.isAdmin;
    }
    if (k === "isUnconfigured") {
      return storeState.isUnconfigured;
    }
    return null;
  },
  set: (k: string, v: unknown) => {
    storeState.sets.push([k, v]);
  },
}));

import {
  _resetCoverageForTest,
  applyHealedRow,
  configurePanel,
  coverageItems,
  coverageRow,
  fetchAndMergeCoverage,
  filterCoverage,
  libraryLoaded,
  loadCoverage,
  registeredCollections,
  removeCoverageRow,
  renderCoverage,
} from "./coverage.js";
import type { CoverageItem, CoverageTarget } from "./api-types.js";

// --- Fixtures (hardcoded, DAMP) ---

function target(
  language: string,
  have: number,
  total: number,
  have_ignored = 0,
  variant = "standard",
): CoverageTarget {
  return { language, variant, have, total, have_ignored };
}

/** Series row as the wire delivers it — no `_type`, that is the client's merge. */
function series(
  tvdbId: number,
  title: string,
  extra: Partial<CoverageItem> = {},
): Record<string, unknown> {
  return {
    title,
    audio_lang: "en",
    rule: "en",
    id: tvdbId,
    year: 2020,
    tvdb_id: tvdbId,
    episodes: 3,
    targets: [target("en", 1, 3)],
    ...extra,
  };
}

function movie(
  tmdbId: number,
  title: string,
  extra: Partial<CoverageItem> = {},
): Record<string, unknown> {
  return {
    title,
    audio_lang: "en",
    rule: "en",
    id: tmdbId,
    year: 2021,
    tmdb_id: tmdbId,
    has_file: true,
    targets: [target("en", 0, 1)],
    ...extra,
  };
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
        <option value="title-desc">Z-A</option>
        <option value="newest">Newest</option>
        <option value="oldest">Oldest</option>
      </select>
      <input id="cov-filter" type="search">
    </div>
  </div>
  <div id="coverageContent"></div>
</section>`;

// The module holds ONE collection plus the filter/page signals for the whole
// file, and several paths branch on whether that collection is EMPTY (a first
// load paints through the skeleton controller, a refresh paints directly). So
// every test starts from a cleared collection, a fresh DOM, controls back to
// defaults, and filterCoverage() to reset pageLimit and bump the filter tick —
// nothing is inherited from whichever sibling ran before.
beforeEach(async () => {
  clientState.series = [];
  clientState.movies = [];
  clientState.seriesError = null;
  clientState.defer = false;
  clientState.pending = [];
  await fetchAndMergeCoverage();
  _resetCoverageForTest(); // gate + registrations back to a fresh tab
  clientState.signals = [];
  document.body.innerHTML = FIXTURE;
  bus.emitted = [];
  storeState.isAdmin = false;
  storeState.isUnconfigured = false;
  storeState.sets = [];
  filterCoverage();
});

function reqEl<T extends HTMLElement>(selector: string): T {
  const e = document.querySelector<T>(selector);
  if (e === null) {
    throw new Error(`missing ${selector}`);
  }
  return e;
}

function tbody(): HTMLTableSectionElement {
  return reqEl<HTMLTableSectionElement>("table.library tbody");
}

function rowTitles(): string[] {
  return Array.from(tbody().querySelectorAll('[data-col="title"]')).map(
    (td) => td.textContent ?? "",
  );
}

function reqRow(i: number): HTMLElement {
  const row = tbody().children.item(i);
  if (!(row instanceof HTMLElement)) {
    throw new Error(`row ${String(i)} missing`);
  }
  return row;
}

/** All badges of one row, as `status|text` pairs — the two things the badge
 *  ladder actually decides. */
function badges(i: number): string[] {
  return Array.from(reqRow(i).querySelectorAll(".badge")).map(
    (b) => `${b.getAttribute("data-status") ?? ""}|${b.textContent ?? ""}`,
  );
}

/** The row's content signature, read off the DOM the way update() compares it. */
function rowSig(i: number): string {
  const sig = reqRow(i).dataset["sig"];
  if (sig === undefined) {
    throw new Error("row carries no data-sig");
  }
  return sig;
}

/** One row's cell elements by identity — a repaint replaces them, a skipped
 *  repaint keeps every reference. */
function cellsOf(i: number): Element[] {
  return Array.from(reqRow(i).children);
}

/** Render the given wire rows through the real load path. Drops the load's
 *  own `coverage:overwrite` emission (pinned by its own suite below) so
 *  interaction tests assert only what the interaction emitted. */
async function load(
  seriesRows: Record<string, unknown>[],
  movieRows: Record<string, unknown>[] = [],
): Promise<void> {
  clientState.series = seriesRows;
  clientState.movies = movieRows;
  await loadCoverage();
  bus.emitted = bus.emitted.filter((e) => e.event !== "coverage:overwrite");
}

describe("coverage: fetchAndMergeCoverage", () => {
  it("merges series and movies under one _type discriminant", async () => {
    clientState.series = [series(1, "Show")];
    clientState.movies = [movie(2, "Film")];

    const merged = await fetchAndMergeCoverage();

    expect(merged.map((i) => `${i._type}:${i.title}`)).toEqual(["series:Show", "movie:Film"]);
  });

  it("loads the merged rows into the collection", async () => {
    clientState.series = [series(1, "Show")];
    clientState.movies = [movie(2, "Film")];

    await fetchAndMergeCoverage();

    expect(coverageItems().map((i) => i.title)).toEqual(["Show", "Film"]);
  });

  it("treats a null response as an empty list", async () => {
    clientState.series = null;
    clientState.movies = null;

    const merged = await fetchAndMergeCoverage();

    expect(merged).toEqual([]);
  });

  it("aborts the in-flight load when a newer one starts", async () => {
    clientState.defer = true;
    const first = fetchAndMergeCoverage();
    const firstSignals = [...clientState.signals];
    clientState.defer = false;
    clientState.series = [series(9, "Newer")];

    await fetchAndMergeCoverage();
    clientState.pending[0]?.([series(1, "Stale")]);
    await first;

    // Both halves of the merge carry the same signal, so a supersede cancels
    // the movies request too, not just the series one.
    expect(firstSignals.length).toBe(2);
    expect(firstSignals.map((s) => s?.aborted)).toEqual([true, true]);
    // The superseded load returns the collection untouched: "Stale" never lands.
    expect(coverageItems().map((i) => i.title)).toEqual(["Newer"]);
  });

  it("the registered cleanup aborts an in-flight load", async () => {
    clientState.defer = true;
    const pendingLoad = fetchAndMergeCoverage();
    const signals = [...clientState.signals];

    for (const fn of cleanups.fns) {
      fn();
    }
    clientState.pending[0]?.([]);
    await pendingLoad;

    expect(signals.map((s) => s?.aborted)).toEqual([true, true]);
  });

  it("holds no rows for an empty library", async () => {
    await fetchAndMergeCoverage();

    expect(coverageItems()).toEqual([]);
  });
});

describe("coverage: loadCoverage", () => {
  it("does not fetch while the app is unconfigured", async () => {
    storeState.isUnconfigured = true;

    await loadCoverage();

    expect(clientState.signals).toEqual([]);
    expect(document.querySelector("table.library")).toBeNull();
  });

  // Which of the two error paints runs depends on whether this load was the
  // FIRST one (empty collection ⇒ the skeleton controller owns the paint) or a
  // refresh (⇒ the direct paint). Both tests seed that state themselves rather
  // than inheriting whatever a sibling test left in the module's collection.
  it("renders a first load's error into the content area", async () => {
    clientState.seriesError = new Error("coverage endpoint down");

    await loadCoverage();

    expect(reqEl('#coverageContent [data-status="err"]').textContent).toBe(
      "coverage endpoint down",
    );
  });

  it("renders a refresh's error into the content area", async () => {
    await load([series(1, "Show")]);
    clientState.seriesError = new Error("coverage endpoint down");

    await loadCoverage();

    expect(reqEl('#coverageContent [data-status="err"]').textContent).toBe(
      "coverage endpoint down",
    );
  });

  it("keeps a silent refresh's failure off the screen", async () => {
    await load([series(1, "Show")]);
    clientState.seriesError = new Error("coverage endpoint down");

    await loadCoverage(true);

    expect(document.querySelector('#coverageContent [data-status="err"]')).toBeNull();
  });

  it("keeps a silent first load's failure off the screen too", async () => {
    // An SSE-driven refresh that arrives before any data must not paint an
    // error over the empty state, even though nothing is on screen yet.
    clientState.seriesError = new Error("coverage endpoint down");

    await loadCoverage(true);

    expect(document.querySelector('#coverageContent [data-status="err"]')).toBeNull();
  });

  it("mounts the table on a successful load", async () => {
    await load([series(1, "Show")]);

    expect(rowTitles()).toEqual(["Show"]);
  });

  it("remounts the table on a refresh after the container was replaced", async () => {
    // Detail navigation replaces #coverageContent; the next refresh has to
    // mount the reactive table again rather than leave the view blank.
    await load([series(1, "Show")]);
    reqEl("#coverageContent").replaceChildren();

    await loadCoverage(true);

    expect(rowTitles()).toEqual(["Show"]);
  });
});

describe("coverage: badge ladder", () => {
  it('renders "not scanned" for an item with no targets', async () => {
    await load([series(1, "Show", { targets: [] })]);

    expect(badges(0)).toEqual(["muted|not scanned"]);
  });

  it("marks a fully covered target ok", async () => {
    await load([series(1, "Show", { targets: [target("en", 3, 3)] })]);

    expect(badges(0)).toEqual(["ok|en3/3"]);
  });

  it("marks a partly covered target partial", async () => {
    await load([series(1, "Show", { targets: [target("en", 1, 3)] })]);

    expect(badges(0)).toEqual(["partial|en1/3"]);
  });

  it("marks an uncovered target err", async () => {
    await load([series(1, "Show", { targets: [target("en", 0, 3)] })]);

    expect(badges(0)).toEqual(["err|en0/3"]);
  });

  it("marks a target covered only by an ignored codec warn", async () => {
    await load([series(1, "Show", { targets: [target("en", 0, 3, 2)] })]);

    expect(badges(0)).toEqual(["warn|en2/3"]);
  });

  it("treats a zero-total target as uncovered, not complete", async () => {
    // have/total would be 0/0 = NaN and (have+ignored)/total = Infinity; the
    // guard is what keeps an episode-less series off the green badge.
    await load([series(1, "Show", { targets: [target("en", 0, 0)] })]);

    expect(badges(0)).toEqual(["err|en0/0"]);
  });

  it("treats a zero-total target with files as uncovered too", async () => {
    // A stale row can carry files against a total of zero; dividing by it would
    // read Infinity and paint the badge green.
    await load([series(1, "Show", { targets: [target("en", 2, 0)] })]);

    expect(badges(0)).toEqual(["err|en0/0"]);
  });

  it("treats a zero-total target with ignored files as uncovered too", async () => {
    await load([series(1, "Show", { targets: [target("en", 0, 0, 2)] })]);

    expect(badges(0)).toEqual(["err|en0/0"]);
  });

  it("caps the displayed count at the total", async () => {
    // Covered by BOTH a real sub and an ignored-codec one: 3+1 must not
    // render "4/3".
    await load([series(1, "Show", { targets: [target("en", 3, 3, 1)] })]);

    expect(badges(0)).toEqual(["ok|en3/3"]);
  });

  it("explains the ignored-codec count in the badge tooltip", async () => {
    await load([series(1, "Show", { targets: [target("en", 1, 3, 2)] })]);

    expect(reqRow(0).querySelector(".badge")?.getAttribute("data-tip")).toBe(
      "2 with ignored codec only",
    );
  });

  it("carries no tooltip when nothing is ignored", async () => {
    await load([series(1, "Show", { targets: [target("en", 1, 3)] })]);

    expect(reqRow(0).querySelector(".badge")?.hasAttribute("data-tip")).toBe(false);
  });

  it("labels a non-standard variant with its variant name", async () => {
    await load([series(1, "Show", { targets: [target("fr", 1, 3, 0, "forced")] })]);

    expect(badges(0)).toEqual(["partial|fr(forced)1/3"]);
  });

  it("renders one badge per target", async () => {
    await load([series(1, "Show", { targets: [target("en", 3, 3), target("fr", 0, 3)] })]);

    expect(badges(0)).toEqual(["ok|en3/3", "err|fr0/3"]);
  });
});

describe("coverage: row content", () => {
  it("renders title, year and the audio-language rule name", async () => {
    await load([series(1, "Show", { year: 1999, rule: "fr" })]);

    const row = reqRow(0);
    expect(row.querySelector('[data-col="title"]')?.textContent).toBe("Show");
    expect(row.querySelector('[data-col="meta"]')?.textContent).toBe("1999");
    expect(Array.from(row.querySelectorAll('[data-col="meta"]'))[1]?.textContent).toBe("French");
  });

  it("renders an empty year cell for a year-less item", async () => {
    await load([series(1, "Show", { year: 0 })]);

    expect(reqRow(0).querySelector('[data-col="meta"]')?.textContent).toBe("");
  });

  it("keys the badge cell by the item's coverage media id", async () => {
    await load([series(7, "Show")]);

    expect(reqRow(0).querySelector('[data-col="badges"]')?.getAttribute("data-cov-id")).toBe(
      "tvdb-7",
    );
  });

  it("offers a Search button on a scannable item", async () => {
    await load([series(1, "Show")]);

    const btn = reqEl<HTMLButtonElement>('[data-col="actions"] button');
    expect(btn.textContent).toBe(" Search");
    expect(btn.dataset["scanScope"]).toBe("series::1:0:0");
  });

  it("replaces the Search button with a muted badge on an excluded item", async () => {
    await load([series(1, "Show", { excluded: true })]);

    expect(document.querySelector('[data-col="actions"] button')).toBeNull();
    expect(
      reqRow(0).querySelector('[data-col="actions"] .badge')?.getAttribute("data-tip"),
    ).toContain("Remove the tag in Sonarr");
  });

  it("names Radarr in an excluded movie's tooltip", async () => {
    await load([], [movie(2, "Film", { excluded: true })]);

    expect(
      reqRow(0).querySelector('[data-col="actions"] .badge')?.getAttribute("data-tip"),
    ).toContain("Remove the tag in Radarr");
  });
});

describe("coverage: row interaction", () => {
  it("opens the series detail on a series row click", async () => {
    await load([series(1, "Show")]);

    reqRow(0).click();

    expect(bus.emitted.map((e) => e.event)).toEqual(["open:series"]);
  });

  it("opens the movie detail on a movie row click", async () => {
    await load([], [movie(2, "Film")]);

    reqRow(0).click();

    expect(bus.emitted.map((e) => e.event)).toEqual(["open:movie"]);
  });

  it("scans the series from the row's Search button without opening the detail", async () => {
    await load([series(1, "Show")]);

    reqEl<HTMLButtonElement>('[data-col="actions"] button').click();

    // stopPropagation keeps the row's own open handler out of it.
    expect(bus.emitted.map((e) => e.event)).toEqual(["scan:series"]);
  });

  it("scans the movie from a movie row's Search button", async () => {
    await load([], [movie(2, "Film")]);

    reqEl<HTMLButtonElement>('[data-col="actions"] button').click();

    expect(bus.emitted.map((e) => e.event)).toEqual(["scan:movie"]);
  });

  it("hands the clicked item to the scan event", async () => {
    await load([series(1, "Show")]);

    reqEl<HTMLButtonElement>('[data-col="actions"] button').click();

    expect(bus.emitted[0]?.payload).toEqual({ item: expect.objectContaining({ title: "Show" }) });
  });
});

describe("coverage: identity-preserving merge", () => {
  /** applyFilters reads #cov-filter exactly once per run, so counting that
   *  lookup counts recomputes of the filtered+sorted view (sort included). */
  function countRecomputes(): () => number {
    const byId = vi.spyOn(document, "getElementById");
    return () => byId.mock.calls.filter(([id]) => id === "cov-filter").length;
  }

  it("a no-op refresh repaints zero rows and recomputes nothing", async () => {
    await load([series(1, "One"), series(2, "Two")], [movie(3, "Film")]);
    const beforeCells = [...cellsOf(0), ...cellsOf(1), ...cellsOf(2)];
    const sigs = [rowSig(0), rowSig(1), rowSig(2)];
    const recomputes = countRecomputes();

    // Same wire payload again: every merged object is fresh, every signature
    // is equal, so the merge must keep the CURRENT objects and no per-row
    // signal may fire.
    await loadCoverage(true);

    const afterCells = [...cellsOf(0), ...cellsOf(1), ...cellsOf(2)];
    expect(afterCells.length).toBe(beforeCells.length);
    for (const [k, cell] of afterCells.entries()) {
      expect(cell).toBe(beforeCells[k]);
    }
    expect([rowSig(0), rowSig(1), rowSig(2)]).toEqual(sigs);
    // The filtered view never recomputed, so the sort never ran either.
    expect(recomputes()).toBe(0);
  });

  it("a one-row change repaints exactly that row and recomputes once", async () => {
    await load([series(1, "One"), series(2, "Two")]);
    const changed = reqRow(0);
    const untouched = cellsOf(1);
    const recomputes = countRecomputes();

    clientState.series = [series(1, "One", { targets: [target("en", 3, 3)] }), series(2, "Two")];
    await loadCoverage(true);

    // The changed row keeps its node (structure tier untouched) but every
    // cell is rebuilt from the fresh item; the sibling keeps every cell.
    expect(reqRow(0)).toBe(changed);
    expect(badges(0)).toEqual(["ok|en3/3"]);
    expect(badges(1)).toEqual(["partial|en1/3"]);
    for (const [j, cell] of untouched.entries()) {
      expect(cellsOf(1)[j]).toBe(cell);
    }
    expect(recomputes()).toBe(1);
  });

  it("a title-only change repaints the title cell in place", async () => {
    // The full-row updater's reason to exist: the shipped one repainted only
    // the badge cell, so a renamed title kept its old cell forever.
    await load([series(1, "One"), series(2, "Two")]);
    const row = reqRow(0);

    clientState.series = [series(1, "Onf"), series(2, "Two")];
    await loadCoverage(true);

    expect(reqRow(0)).toBe(row);
    expect(rowTitles()).toEqual(["Onf", "Two"]);
  });

  it("an episode-count change repaints its row and refreshes the click payload", async () => {
    await load([series(1, "One", { episodes: 3 }), series(2, "Two")]);
    const before = cellsOf(0);
    const untouched = cellsOf(1);

    clientState.series = [series(1, "One", { episodes: 4 }), series(2, "Two")];
    await loadCoverage(true);

    expect(cellsOf(0).some((cell, j) => cell !== before[j])).toBe(true);
    for (const [j, cell] of untouched.entries()) {
      expect(cellsOf(1)[j]).toBe(cell);
    }
    // The row click hands the CURRENT entity to the detail view — a closure
    // over the mount-time object would deliver the stale count.
    reqRow(0).click();
    expect(bus.emitted.at(-1)?.event).toBe("open:series");
    expect(bus.emitted.at(-1)?.payload).toEqual({
      item: expect.objectContaining({ title: "One", episodes: 4 }),
    });
  });

  it("keeps focus on the focused row through a heal and a no-op refresh", async () => {
    await load([series(1, "One"), series(2, "Two")]);
    const row = reqRow(0);
    row.focus();
    expect(document.activeElement).toBe(row);

    // A heal that repaints the focused row must not re-seat or blur it.
    clientState.series = [series(1, "One", { targets: [target("en", 3, 3)] }), series(2, "Two")];
    await loadCoverage(true);
    expect(badges(0)).toEqual(["ok|en3/3"]);
    expect(document.activeElement).toBe(row);

    // A no-op refresh touches nothing at all.
    await loadCoverage(true);
    expect(document.activeElement).toBe(row);
  });
});

describe("coverage: signature field audit", () => {
  // The coverageItemSignature audit: every wire field of SeriesItem/MovieItem
  // must flip the signature, because each one is rendered, keyed, filtered,
  // sorted, or handed onward through a row action —
  //   id                 scan scope keys, detail file/sync actions
  //   tvdb_id/tmdb_id    collection key, detail routes
  //   imdb_id            search popup identity query
  //   title              cell, filter, sort
  //   year               cell, sort tiebreak
  //   first_aired / in_cinemas / digital_release   date sort
  //   rule               Audio cell
  //   audio_lang         detail header info
  //   tags               exclusion inputs riding the item
  //   excluded           action cell (badge vs Search)
  //   has_file           movie detail sync gating
  //   scene_name         search popup release matching
  //   episodes           series detail header ("N ep")
  //   targets            coverage badges (every field, own table below)
  // The mapped type is the enforcement: a field added to or removed from
  // CoverageItem fails compilation here, forcing the audit to re-run. `_type`
  // is the client merge's own discriminant (never on the wire), pinned by the
  // dedicated series/movie pair test below.
  const sigCases: {
    [K in keyof Required<Omit<CoverageItem, "_type">>]: {
      kind: "series" | "movie";
      change: Pick<Required<CoverageItem>, K>;
    };
  } = {
    id: { kind: "series", change: { id: 99 } },
    tvdb_id: { kind: "series", change: { tvdb_id: 99 } },
    tmdb_id: { kind: "movie", change: { tmdb_id: 99 } },
    imdb_id: { kind: "series", change: { imdb_id: "tt0903747" } },
    title: { kind: "series", change: { title: "Other" } },
    year: { kind: "series", change: { year: 1999 } },
    first_aired: { kind: "series", change: { first_aired: "2021-06-01" } },
    in_cinemas: { kind: "movie", change: { in_cinemas: "2021-06-01" } },
    digital_release: { kind: "movie", change: { digital_release: "2021-06-01" } },
    rule: { kind: "series", change: { rule: "fr" } },
    audio_lang: { kind: "series", change: { audio_lang: "fr" } },
    tags: { kind: "series", change: { tags: [5] } },
    excluded: { kind: "series", change: { excluded: true } },
    has_file: { kind: "movie", change: { has_file: false } },
    scene_name: { kind: "movie", change: { scene_name: "Film.2021.1080p" } },
    episodes: { kind: "series", change: { episodes: 4 } },
    targets: { kind: "series", change: { targets: [target("en", 1, 3), target("fr", 0, 3)] } },
  };

  /** Load a base row of the given kind, then reload it with one field
   *  changed; the two signatures the DOM carried are the audit's verdict. */
  async function sigAfterMutation(
    kind: "series" | "movie",
    change: Partial<CoverageItem>,
  ): Promise<{ before: string; after: string }> {
    const mk = (extra: Partial<CoverageItem>): Record<string, unknown> =>
      kind === "series" ? series(1, "Show", extra) : movie(1, "Show", extra);
    await load(kind === "series" ? [mk({})] : [], kind === "movie" ? [mk({})] : []);
    const before = rowSig(0);
    clientState.series = kind === "series" ? [mk(change)] : [];
    clientState.movies = kind === "movie" ? [mk(change)] : [];
    await loadCoverage(true);
    return { before, after: rowSig(0) };
  }

  for (const [field, c] of Object.entries(sigCases)) {
    it(`the signature covers ${field}`, async () => {
      const { before, after } = await sigAfterMutation(c.kind, c.change);
      expect(after).not.toBe(before);
    });
  }

  // Each case differs from the base fixture's target("en", 1, 3) in exactly
  // the named field; the mapped type forces a case per CoverageTarget field.
  const targetCases: { [K in keyof Required<CoverageTarget>]: CoverageTarget } = {
    language: target("fr", 1, 3),
    variant: target("en", 1, 3, 0, "forced"),
    have: target("en", 2, 3),
    have_ignored: target("en", 1, 3, 1),
    total: target("en", 1, 4),
  };

  for (const [field, mutated] of Object.entries(targetCases)) {
    it(`the signature covers targets[].${field}`, async () => {
      const { before, after } = await sigAfterMutation("series", { targets: [mutated] });
      expect(after).not.toBe(before);
    });
  }

  it("the signature covers the targets vector length", async () => {
    const { before, after } = await sigAfterMutation("series", { targets: [] });
    expect(after).not.toBe(before);
  });

  it("a series row and a movie row never share a signature", async () => {
    // Same title, year, rule and targets; the discriminant (with its id
    // fields) keeps the signatures apart.
    await load(
      [series(1, "Show")],
      [movie(1, "Show", { year: 2020, targets: [target("en", 1, 3)] })],
    );

    expect(rowSig(0)).not.toBe(rowSig(1));
  });

  it("an identical reload leaves the signature unchanged", async () => {
    const { before, after } = await sigAfterMutation("series", {});
    expect(after).toBe(before);
  });
});

describe("coverage: empty states", () => {
  it("shows the no-media empty state and hides the table with no data", async () => {
    await load([]);

    expect(reqEl(".cov-list .empty").hidden).toBe(false);
    expect(reqEl<HTMLElement>("table.library").hidden).toBe(true);
  });

  it("shows the no-match empty state when a filter excludes everything", async () => {
    await load([series(1, "Show")]);

    reqEl<HTMLInputElement>("#cov-filter").value = "nothing matches this";
    filterCoverage();

    const empties = Array.from(document.querySelectorAll<HTMLElement>(".cov-list .empty"));
    expect(empties.map((e) => e.hidden)).toEqual([true, false]);
    expect(reqEl<HTMLElement>("table.library").hidden).toBe(true);
  });

  it("hides both empty states once rows are visible", async () => {
    await load([series(1, "Show")]);

    const empties = Array.from(document.querySelectorAll<HTMLElement>(".cov-list .empty"));
    expect(empties.map((e) => e.hidden)).toEqual([true, true]);
    expect(reqEl<HTMLElement>("table.library").hidden).toBe(false);
  });
});

// 60 rows: one page of 50 plus a partial second page.
function manySeries(): Record<string, unknown>[] {
  return Array.from({ length: 60 }, (_unused, i) =>
    series(100 + i, `Show ${String(i).padStart(2, "0")}`),
  );
}

describe("coverage: pagination", () => {
  it("renders only the first page and offers Show more", async () => {
    await load(manySeries());

    expect(tbody().children.length).toBe(50);
    expect(reqEl<HTMLElement>(".more-btn").hidden).toBe(false);
  });

  it("extends the window by a page per Show more click", async () => {
    await load(manySeries());

    reqEl<HTMLButtonElement>(".more-btn").click();

    expect(tbody().children.length).toBe(60);
    expect(reqEl<HTMLElement>(".more-btn").hidden).toBe(true);
  });

  it("hides Show more when everything fits on one page", async () => {
    await load([series(1, "Show")]);

    expect(reqEl<HTMLElement>(".more-btn").hidden).toBe(true);
  });

  it("returns to the first page when a filter changes", async () => {
    await load(manySeries());
    reqEl<HTMLButtonElement>(".more-btn").click();

    filterCoverage();

    expect(tbody().children.length).toBe(50);
  });

  it("a filter gesture from a deeper page reconciles once: surviving rows keep their nodes (R8.4)", async () => {
    // 30 series then 30 movies; the titles sort every movie past the first
    // page boundary, so with the window at 100 the probed movie row exists
    // only because of the deeper page.
    const movies = Array.from({ length: 30 }, (_unused, i) =>
      movie(200 + i, `ZFilm ${String(i).padStart(2, "0")}`),
    );
    await load(manySeries().slice(0, 30), movies);
    reqEl<HTMLButtonElement>(".more-btn").click();
    expect(tbody().children.length).toBe(60);
    const surviving = reqRow(55); // "ZFilm 25"

    reqEl<HTMLSelectElement>("#cov-type-filter").value = "movies";
    filterCoverage();

    // One flush (R8.4): unbatched, the pageLimit reset alone reconciled the
    // OLD view sliced to 50 — unmounting this row — before the filter pass
    // remounted it as a fresh node.
    expect(tbody().children.length).toBe(30);
    expect(reqRow(25)).toBe(surviving);
  });
});

describe("coverage: filtering", () => {
  it("matches the title filter case-insensitively", async () => {
    await load([series(1, "The Wire"), series(2, "Breaking Bad")]);

    reqEl<HTMLInputElement>("#cov-filter").value = "WIRE";
    filterCoverage();

    expect(rowTitles()).toEqual(["The Wire"]);
  });

  it("matches the filter anywhere in the title", async () => {
    await load([series(1, "The Wire"), series(2, "Breaking Bad")]);

    reqEl<HTMLInputElement>("#cov-filter").value = "reaking";
    filterCoverage();

    expect(rowTitles()).toEqual(["Breaking Bad"]);
  });

  it("keeps every item when the filter is empty", async () => {
    await load([series(1, "Alpha"), series(2, "Beta")]);

    expect(rowTitles()).toEqual(["Alpha", "Beta"]);
  });

  it("keeps only series under the series type filter", async () => {
    await load([series(1, "Show")], [movie(2, "Film")]);

    reqEl<HTMLSelectElement>("#cov-type-filter").value = "series";
    filterCoverage();

    expect(rowTitles()).toEqual(["Show"]);
  });

  it("keeps only movies under the movies type filter", async () => {
    await load([series(1, "Show")], [movie(2, "Film")]);

    reqEl<HTMLSelectElement>("#cov-type-filter").value = "movies";
    filterCoverage();

    expect(rowTitles()).toEqual(["Film"]);
  });

  it("keeps both kinds under the all type filter", async () => {
    await load([series(1, "Show")], [movie(2, "Film")]);

    reqEl<HTMLSelectElement>("#cov-type-filter").value = "all";
    filterCoverage();

    // Default sort is title A-Z, so the movie leads whatever the merge order was.
    expect(rowTitles()).toEqual(["Film", "Show"]);
  });

  it("drops fully covered items under Missing only", async () => {
    await load([
      series(1, "Complete", { targets: [target("en", 3, 3)] }),
      series(2, "Partial", { targets: [target("en", 1, 3)] }),
    ]);

    reqEl<HTMLInputElement>("#cov-missing").checked = true;
    filterCoverage();

    expect(rowTitles()).toEqual(["Partial"]);
  });

  it("keeps a never-scanned item under Missing only", async () => {
    // No targets at all is the strongest form of "missing", not a covered item.
    await load([
      series(1, "Complete", { targets: [target("en", 3, 3)] }),
      series(2, "Unscanned", { targets: [] }),
    ]);

    reqEl<HTMLInputElement>("#cov-missing").checked = true;
    filterCoverage();

    expect(rowTitles()).toEqual(["Unscanned"]);
  });

  it("keeps an item whose second target is incomplete under Missing only", async () => {
    await load([
      series(1, "Mixed", { targets: [target("en", 3, 3), target("fr", 0, 3)] }),
      series(2, "Complete", { targets: [target("en", 3, 3), target("fr", 3, 3)] }),
    ]);

    reqEl<HTMLInputElement>("#cov-missing").checked = true;
    filterCoverage();

    expect(rowTitles()).toEqual(["Mixed"]);
  });

  it("combines the type filter with the title filter", async () => {
    await load([series(1, "Shared Name")], [movie(2, "Shared Name")]);

    reqEl<HTMLInputElement>("#cov-filter").value = "shared";
    reqEl<HTMLSelectElement>("#cov-type-filter").value = "movies";
    filterCoverage();

    expect(rowTitles()).toEqual(["Shared Name"]);
    expect(reqRow(0).querySelector('[data-col="badges"]')?.getAttribute("data-cov-id")).toBe(
      "tmdb-2",
    );
  });
});

describe("coverage: sorting", () => {
  // Titles, dates and years all disagree so no two sort modes share an order.
  function sortFixture(): Record<string, unknown>[] {
    return [
      series(1, "Mid", { year: 2001, first_aired: "2015-06-01" }),
      series(2, "Zulu", { year: 1999, first_aired: "2020-01-01" }),
      series(3, "Alpha", { year: 2005, first_aired: "2010-01-01" }),
    ];
  }

  it("sorts by title ascending by default", async () => {
    await load(sortFixture());

    expect(rowTitles()).toEqual(["Alpha", "Mid", "Zulu"]);
  });

  it("sorts by title descending", async () => {
    await load(sortFixture());

    reqEl<HTMLSelectElement>("#cov-sort").value = "title-desc";
    filterCoverage();

    expect(rowTitles()).toEqual(["Zulu", "Mid", "Alpha"]);
  });

  it("sorts newest by air date, not by year", async () => {
    await load(sortFixture());

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Zulu", "Mid", "Alpha"]);
  });

  it("sorts oldest by air date, not by year", async () => {
    await load(sortFixture());

    reqEl<HTMLSelectElement>("#cov-sort").value = "oldest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Alpha", "Mid", "Zulu"]);
  });

  it("falls back to a movie's cinema date when it never aired", async () => {
    await load(
      [series(1, "Aired", { first_aired: "2010-01-01" })],
      [movie(2, "Cinema", { in_cinemas: "2020-01-01" })],
    );

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Cinema", "Aired"]);
  });

  it("falls back to a movie's digital release when it has no cinema date", async () => {
    await load(
      [series(1, "Aired", { first_aired: "2010-01-01" })],
      [movie(2, "Digital", { digital_release: "2020-01-01" })],
    );

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Digital", "Aired"]);
  });

  it("ranks a dated item above an undated one, whatever the years say", async () => {
    await load([
      series(1, "Dated", { year: 1990, first_aired: "2020-01-01" }),
      series(2, "Undated", { year: 2010 }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Dated", "Undated"]);
  });

  it("ranks an undated item above a dated one under oldest", async () => {
    // The mirror of the newest rule: an empty date sorts before any real one,
    // and the years must not be consulted while one side has a date.
    await load([
      series(1, "Dated", { year: 1990, first_aired: "2020-01-01" }),
      series(2, "Undated", { year: 2010 }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "oldest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Undated", "Dated"]);
  });

  it("breaks a newest tie on equal dates by year descending", async () => {
    // Three rows, because a 2-row sort can be produced by a comparator with a
    // constant sign: titles, arrival order and the expected order all differ.
    await load([
      series(1, "Bravo", { year: 1999, first_aired: "2020-01-01" }),
      series(2, "Alpha", { year: 2005, first_aired: "2020-01-01" }),
      series(3, "Zulu", { year: 2001, first_aired: "2020-01-01" }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Alpha", "Zulu", "Bravo"]);
  });

  it("breaks an oldest tie on equal dates by year ascending", async () => {
    await load([
      series(1, "Bravo", { year: 2005, first_aired: "2020-01-01" }),
      series(2, "Alpha", { year: 1999, first_aired: "2020-01-01" }),
      series(3, "Zulu", { year: 2001, first_aired: "2020-01-01" }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "oldest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Alpha", "Zulu", "Bravo"]);
  });

  it("breaks a newest tie on equal dates and years by title", async () => {
    await load([
      series(1, "Zulu", { year: 2000, first_aired: "2020-01-01" }),
      series(2, "Alpha", { year: 2000, first_aired: "2020-01-01" }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Alpha", "Zulu"]);
  });

  it("breaks an oldest tie on equal dates and years by title", async () => {
    await load([
      series(1, "Zulu", { year: 2000, first_aired: "2020-01-01" }),
      series(2, "Alpha", { year: 2000, first_aired: "2020-01-01" }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "oldest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Alpha", "Zulu"]);
  });

  it("sorts year-less items last under newest", async () => {
    // Titles ascend as the years ascend, so the expected order is neither the
    // arrival order nor the alphabet.
    await load([
      series(1, "Ace", { year: 0 }),
      series(2, "Mid", { year: 1999 }),
      series(3, "Zed", { year: 2000 }),
    ]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "newest";
    filterCoverage();

    expect(rowTitles()).toEqual(["Zed", "Mid", "Ace"]);
  });

  it("reorders the existing rows when the sort flips", async () => {
    // Same ids, reversed order: the structural tier must notice, or the table
    // keeps painting yesterday's order.
    await load(sortFixture());
    expect(rowTitles()).toEqual(["Alpha", "Mid", "Zulu"]);

    reqEl<HTMLSelectElement>("#cov-sort").value = "title-desc";
    filterCoverage();

    expect(rowTitles()).toEqual(["Zulu", "Mid", "Alpha"]);
  });
});

describe("coverage: configurePanel", () => {
  it("reveals the card header", () => {
    configurePanel(true);

    expect(reqEl<HTMLElement>("#coveragePanel .card-head").hidden).toBe(false);
  });

  it("shows the filter controls in library mode", () => {
    configurePanel(true);

    expect(reqEl<HTMLElement>("#coveragePanel .controls").style.display).toBe("");
  });

  it("hides the filter controls in detail mode", () => {
    configurePanel(false, { title: "Show" });

    expect(reqEl<HTMLElement>("#coveragePanel .controls").style.display).toBe("none");
  });

  it("restores the Library heading and clears the detail context", () => {
    configurePanel(false, { title: "Show" });

    configurePanel(true);

    expect(reqEl("#lib-heading").textContent).toBe("Library");
    expect(storeState.sets).toEqual([["detailCtx", null]]);
  });

  it("titles the heading with the detail title", () => {
    configurePanel(false, { title: "The Wire" });

    expect(reqEl("#lib-heading").textContent).toBe("The Wire");
  });

  it("appends the detail info beside the title", () => {
    configurePanel(false, { title: "The Wire", info: "5 seasons" });

    expect(reqEl("#lib-heading .detail-info").textContent).toBe("5 seasons");
  });

  it("omits the info span when there is no info", () => {
    configurePanel(false, { title: "The Wire" });

    expect(document.querySelector("#lib-heading .detail-info")).toBeNull();
  });

  it("puts the Back button before the heading", () => {
    configurePanel(false, { title: "The Wire" });

    const head = reqEl("#coveragePanel .card-head");
    expect(head.children.item(0)?.getAttribute("data-nav")).toBe("back");
    expect(head.children.item(1)?.id).toBe("lib-heading");
  });

  it("navigates back on a Back click", () => {
    const back = vi.spyOn(history, "back").mockImplementation(() => undefined);
    configurePanel(false, { title: "The Wire" });

    reqEl<HTMLButtonElement>('[data-nav="back"]').click();

    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it("labels the arr button Sonarr by default", () => {
    configurePanel(false, { title: "The Wire", arrLink: "http://sonarr/series/1" });

    const arr = reqEl<HTMLButtonElement>('[data-nav="arr"]');
    expect(arr.className).toBe("arr-sonarr");
    expect(arr.dataset["tip"]).toBe("Open in Sonarr");
  });

  it("labels the arr button Radarr for a movie", () => {
    configurePanel(false, {
      title: "Heat",
      arrLink: "http://radarr/movie/1",
      arrName: "Radarr",
    });

    const arr = reqEl<HTMLButtonElement>('[data-nav="arr"]');
    expect(arr.className).toBe("arr-radarr");
    expect(arr.dataset["tip"]).toBe("Open in Radarr");
  });

  it("opens the arr deep link in a new tab", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    configurePanel(false, { title: "The Wire", arrLink: "http://sonarr/series/1" });

    reqEl<HTMLButtonElement>('[data-nav="arr"]').click();

    expect(open).toHaveBeenCalledWith("http://sonarr/series/1", "_blank", "noopener");
    open.mockRestore();
  });

  it("omits the arr button without a link", () => {
    configurePanel(false, { title: "The Wire" });

    expect(document.querySelector('[data-nav="arr"]')).toBeNull();
  });

  it("runs the history action on a History click", () => {
    let clicked = 0;
    configurePanel(false, {
      title: "The Wire",
      historyAction: () => {
        clicked += 1;
      },
    });

    reqEl<HTMLButtonElement>('[data-nav="hist"]').click();

    expect(clicked).toBe(1);
  });

  it("inserts the History button before the arr link", () => {
    configurePanel(false, {
      title: "The Wire",
      arrLink: "http://sonarr/series/1",
      historyAction: () => undefined,
    });

    const navs = Array.from(document.querySelectorAll("[data-nav]")).map((e) =>
      e.getAttribute("data-nav"),
    );
    expect(navs).toEqual(["back", "hist", "arr"]);
  });

  it("offers the Files button to an admin", () => {
    storeState.isAdmin = true;

    configurePanel(false, { title: "The Wire", filesAction: () => undefined });

    expect(document.querySelector('[data-nav="files"]')).not.toBeNull();
  });

  it("withholds the Files button from a non-admin", () => {
    storeState.isAdmin = false;

    configurePanel(false, { title: "The Wire", filesAction: () => undefined });

    expect(document.querySelector('[data-nav="files"]')).toBeNull();
  });

  it("runs the files action on a Files click", () => {
    storeState.isAdmin = true;
    let clicked = 0;
    configurePanel(false, {
      title: "The Wire",
      filesAction: () => {
        clicked += 1;
      },
    });

    reqEl<HTMLButtonElement>('[data-nav="files"]').click();

    expect(clicked).toBe(1);
  });

  it("drops the previous detail's nav buttons on the next configure", () => {
    configurePanel(false, {
      title: "The Wire",
      arrLink: "http://sonarr/series/1",
      historyAction: () => undefined,
    });

    configurePanel(false, { title: "Heat" });

    const navs = Array.from(document.querySelectorAll("[data-nav]")).map((e) =>
      e.getAttribute("data-nav"),
    );
    expect(navs).toEqual(["back"]);
  });

  it("configures the panel from the bus event", () => {
    const handler = bus.handlers.get("panel:configure");
    if (!handler) {
      throw new Error("PanelConfigure handler not registered");
    }

    (handler as (p: { visible: boolean; detail?: { title: string } }) => void)({
      visible: false,
      detail: { title: "From the bus" },
    });

    expect(reqEl("#lib-heading").textContent).toBe("From the bus");
  });
});

describe("coverage: renderCoverage", () => {
  it("shows the library chrome and mounts the table", async () => {
    await load([series(1, "Show")]);
    document.body.innerHTML = FIXTURE;

    renderCoverage();

    expect(reqEl<HTMLElement>("#coveragePanel .card-head").hidden).toBe(false);
    // Library mode, so the filter controls come back with it.
    expect(reqEl<HTMLElement>("#coveragePanel .controls").style.display).toBe("");
    expect(rowTitles()).toEqual(["Show"]);
  });

  it("remounts the table after the container was replaced", async () => {
    await load([series(1, "Show")]);
    reqEl("#coverageContent").replaceChildren();

    renderCoverage();

    expect(rowTitles()).toEqual(["Show"]);
  });
});

describe("coverage: A6 pair landing (heal gate + task 9 seam)", () => {
  it("a landed pair opens the gate and registers both collections", async () => {
    expect(libraryLoaded()).toBe(false);
    expect(registeredCollections().size).toBe(0);

    await load([series(1, "Show")], [movie(2, "Film")]);

    expect(libraryLoaded()).toBe(true);
    expect([...registeredCollections()].sort()).toEqual(["movies", "series"]);
  });

  it("a failed leg (null read) lands nothing: gate closed, nothing registered", async () => {
    clientState.series = null; // the generated client null-collapses failures
    clientState.movies = [movie(2, "Film")];

    await fetchAndMergeCoverage();

    expect(libraryLoaded()).toBe(false);
    expect(registeredCollections().size).toBe(0);
  });

  it("emits coverage:overwrite before every pair snapshot (the reset-rule trigger)", async () => {
    clientState.series = [series(1, "Show")];
    clientState.movies = [];

    await fetchAndMergeCoverage();

    expect(bus.emitted.map((e) => e.event)).toContain("coverage:overwrite");
  });

  it("applyHealedRow upserts a changed row and keeps an unchanged row's object", async () => {
    await load([series(1, "Show")]);
    const cur = coverageRow("tvdb-1");

    // Fresh object, equal signature: the CURRENT object must survive.
    applyHealedRow({ ...series(1, "Show"), _type: "series" } as CoverageItem);
    expect(coverageRow("tvdb-1")).toBe(cur);

    // A real change lands whole and repaints the row.
    applyHealedRow({
      ...series(1, "Show", { targets: [target("en", 3, 3)] }),
      _type: "series",
    } as CoverageItem);
    expect(coverageRow("tvdb-1")).not.toBe(cur);
    expect(badges(0)).toEqual(["ok|en3/3"]);
  });

  it("applyHealedRow inserts a new root without opening the gate", async () => {
    await fetchAndMergeCoverage(); // empty pair: gate opens, rows stay empty
    _resetCoverageForTest(); // ...so close it again: an incomplete tab

    applyHealedRow({ ...series(7, "New Show"), _type: "series" } as CoverageItem);

    expect(coverageRow("tvdb-7")?.title).toBe("New Show");
    expect(coverageItems()).toHaveLength(1); // the row exists...
    expect(libraryLoaded()).toBe(false); // ...but the pair never landed
  });

  it("removeCoverageRow drops the row from the collection and the DOM", async () => {
    await load([series(1, "One"), series(2, "Two")]);

    removeCoverageRow("tvdb-1");

    expect(coverageRow("tvdb-1")).toBeUndefined();
    expect(rowTitles()).toEqual(["Two"]);
  });
});
