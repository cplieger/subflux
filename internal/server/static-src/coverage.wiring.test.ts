//
// coverage.wiring.test.ts — the parts of coverage.ts coverage.test.ts cannot
// reach with its immediately-resolving client: the anti-flicker skeleton (which
// needs fake timers and a fetch that stays pending past the show delay), the
// suppression rules that decide WHICH loads are allowed to paint one, the
// bindings a discarded render owes the collection, and the class/tooltip
// contracts the stylesheet and the tooltip primitive read off the rendered
// nodes.
//
// Same doubles as coverage.test.ts and for the same reasons: the network, the
// bus and the scan-state seams are replaced, the reactive collection, the
// computed view, the real DOM helpers and the real skeleton primitive are not —
// so what the assertions read is the DOM (and the timing) a browser would get.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// CRITICAL: vitest.config sets clearMocks/mockReset/restoreMocks, which strips
// a vi.fn's implementation before each test. Every factory below therefore uses
// PLAIN functions over hoisted mutable records — coverage.ts calls
// registerCleanup and on() in module initializers, and those must survive.
const clientState = vi.hoisted(() => ({
  series: [] as unknown[] | null,
  movies: [] as unknown[] | null,
  // One-shot overrides for the next series fetches, in order: a deferred load
  // parks its resolver here so it can still be in flight while timers advance.
  next: [] as (() => Promise<unknown>)[],
}));
vi.mock("./wire/client.gen.js", () => ({
  coverageSeries: (): Promise<unknown> =>
    clientState.next.shift()?.() ?? Promise.resolve(clientState.series),
  coverageMovies: (): Promise<unknown> => Promise.resolve(clientState.movies),
}));

vi.mock("@cplieger/actions", () => ({
  registerCleanup: () => undefined,
}));

const bus = vi.hoisted(() => ({ emitted: [] as string[] }));
vi.mock("./bus.js", () => ({
  on: () => () => undefined,
  emit: (event: string) => {
    bus.emitted.push(event);
  },
  BusEvent: {
    PanelConfigure: "panel:configure",
    OpenSeries: "open:series",
    OpenMovie: "open:movie",
    ScanSeries: "scan:series",
    ScanMovie: "scan:movie",
  },
}));

// A faithful stand-in for detail-scan.ts: the real one disables the button and
// swaps in a spinner when THIS row's scope is in the running set, so the double
// derives the same thing from a per-test flag rather than answering a constant.
const scanState = vi.hoisted(() => ({ running: false }));
vi.mock("./detail-scan.js", () => ({
  registerScanButton: (btn: HTMLButtonElement) => {
    if (scanState.running) {
      btn.disabled = true;
    }
  },
}));

const storeState = vi.hoisted(() => ({ isUnconfigured: false }));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => {
    if (k === "isUnconfigured") {
      return storeState.isUnconfigured;
    }
    return null;
  },
  set: () => undefined,
}));

import { configurePanel, fetchAndMergeCoverage, filterCoverage, loadCoverage } from "./coverage.js";
import type { CoverageItem, CoverageTarget } from "./api-types.js";

// --- Fixtures (hardcoded, DAMP) ---

function target(language: string, have: number, total: number, have_ignored = 0): CoverageTarget {
  return { language, variant: "standard", have, total, have_ignored };
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

const FIXTURE = `
<section class="card" id="coveragePanel">
  <div class="card-head" hidden>
    <h2 id="lib-heading">Library</h2>
    <div class="controls">
      <input type="checkbox" id="cov-missing">
      <select id="cov-type-filter"><option value="all">All</option></select>
      <select id="cov-sort"><option value="title">A-Z</option></select>
      <input id="cov-filter" type="search">
    </div>
  </div>
  <div id="coverageContent"></div>
</section>`;

// The module holds ONE collection plus the filter/page signals for the whole
// file, and every path under test branches on whether that collection is EMPTY
// (a first load paints through the skeleton controller, a refresh paints
// directly). So each test starts from a cleared collection and a fresh DOM.
beforeEach(async () => {
  clientState.series = [];
  clientState.movies = [];
  clientState.next = [];
  await fetchAndMergeCoverage();
  document.body.innerHTML = FIXTURE;
  bus.emitted = [];
  storeState.isUnconfigured = false;
  scanState.running = false;
  filterCoverage();
});

afterEach(() => {
  vi.useRealTimers();
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

function skeletonRows(): Element[] {
  return Array.from(document.querySelectorAll("#coverageContent > div.skeleton-row"));
}

/** Drain the microtask queue without advancing fake timers, so a pending
 *  show-delay stays pending. */
async function drain(): Promise<void> {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
  }
}

/** Queue a series fetch that never settles on its own; the returned function
 *  finishes it. Each call owns its own promise, so several can be in flight. */
function deferSeries(): (rows: Record<string, unknown>[]) => void {
  let settle: (rows: Record<string, unknown>[]) => void = () => undefined;
  clientState.next.push(
    () =>
      new Promise<unknown>((resolve) => {
        settle = resolve as (rows: Record<string, unknown>[]) => void;
      }),
  );
  return (rows: Record<string, unknown>[]) => {
    settle(rows);
  };
}

/** Render the given wire rows through the real load path (real timers: an
 *  immediately-settling fetch never reaches the show delay). */
async function load(seriesRows: Record<string, unknown>[]): Promise<void> {
  clientState.series = seriesRows;
  clientState.movies = [];
  await loadCoverage();
}

describe("coverage: loading skeleton", () => {
  it("paints eight skeleton rows only after the show delay", async () => {
    vi.useFakeTimers();
    const settle = deferSeries();

    void loadCoverage();
    await drain();

    // A load that settles inside the show-delay window paints no skeleton.
    expect(skeletonRows()).toHaveLength(0);

    vi.advanceTimersByTime(150);

    const rows = skeletonRows();
    expect(rows).toHaveLength(8);
    // Each row carries the shimmer element the stylesheet animates; a row
    // without it is an invisible blank line.
    expect(rows.every((r) => r.querySelector("div.skeleton") !== null)).toBe(true);

    settle([series(1, "Show")]);
    await drain();
    vi.advanceTimersByTime(300);
  });

  it("keeps the skeleton up its minimum once painted, then swaps in the table", async () => {
    vi.useFakeTimers();
    const settle = deferSeries();

    void loadCoverage();
    await drain();
    vi.advanceTimersByTime(150);
    expect(skeletonRows()).toHaveLength(8);

    settle([series(1, "Show")]);
    await drain();

    // Painting the table now would blink the skeleton away the instant it
    // appeared — that is what the 300ms min-visible window prevents.
    expect(skeletonRows()).toHaveLength(8);
    expect(document.querySelector("table.library")).toBeNull();

    vi.advanceTimersByTime(300);

    expect(skeletonRows()).toHaveLength(0);
    expect(rowTitles()).toEqual(["Show"]);
  });

  it("never paints a skeleton over the rows a refresh is replacing", async () => {
    await load([series(1, "Show")]);

    vi.useFakeTimers();
    const settle = deferSeries();
    void loadCoverage();
    await drain();
    // Well past the show delay: a refresh that painted a skeleton would have
    // patched the live reactive table out of the DOM by now.
    vi.advanceTimersByTime(500);

    expect(skeletonRows()).toHaveLength(0);
    expect(rowTitles()).toEqual(["Show"]);

    settle([series(1, "Show")]);
    await drain();
  });

  it("suppresses the skeleton of a load a newer one superseded", async () => {
    vi.useFakeTimers();
    const settleStale = deferSeries();
    void loadCoverage();
    await drain();

    // The newer load aborts the stale one's controller and mounts its own rows
    // before the stale load's show delay elapses.
    clientState.series = [series(1, "Show")];
    clientState.movies = [];
    await loadCoverage();
    expect(rowTitles()).toEqual(["Show"]);

    vi.advanceTimersByTime(500);

    // The stale skeleton must never land over the newer load's content.
    expect(skeletonRows()).toHaveLength(0);
    expect(rowTitles()).toEqual(["Show"]);

    settleStale([]);
    await drain();
  });
});

describe("coverage: table chrome", () => {
  it("explains the Audio column with a tooltip and leaves the others bare", async () => {
    await load([series(1, "Show")]);

    expect(
      Array.from(document.querySelectorAll("table.library thead th")).map((th) =>
        th.getAttribute("data-tip"),
      ),
    ).toEqual([null, null, "Primary audio language", null, null]);
  });
});

describe("coverage: badge structure", () => {
  it("splits a badge into its language half and its count half", async () => {
    await load([series(1, "Show", { targets: [target("en", 1, 3)] })]);

    // .badge-split lays the two halves out edge to edge; a label or a count
    // rendered without its class collapses into unstyled inline text.
    const badge = reqEl(".badge-split");
    expect(badge.querySelector(".badge-lang")?.textContent).toBe("en");
    expect(badge.querySelector(".badge-detail")?.textContent).toBe("1/3");
  });
});

describe("coverage: row scan button", () => {
  it("paints a row's Search button disabled while that scan is running", async () => {
    scanState.running = true;

    await load([series(1, "Show")]);

    expect(reqEl<HTMLButtonElement>("[data-scan-scope]").disabled).toBe(true);
  });

  it("leaves the Search button live when no scan is running", async () => {
    await load([series(1, "Show")]);

    expect(reqEl<HTMLButtonElement>("[data-scan-scope]").disabled).toBe(false);
  });
});

describe("coverage: nav button labels", () => {
  it("wraps the Back button's label in the span the narrow layout hides", () => {
    configurePanel(true, {
      title: "The Wire",
      arrLink: "https://sonarr.example/series/1",
    });

    // Below the mobile breakpoint `.card-head .btn-text` is display:none, so a
    // label outside that span cannot be hidden and overflows the header. Back
    // is the only labelled button configurePanel builds — the detail nav
    // buttons are detail.ts's own, and pinned there.
    expect(reqEl('[data-nav="back"] .btn-text').textContent).toBe(" Back");
  });
});

describe("coverage: render disposal", () => {
  /** Row titles of one specific table element (the global rowTitles() only
   *  sees the table attached to the document). */
  function titlesOf(tbl: HTMLElement): string[] {
    return Array.from(tbl.querySelectorAll('[data-col="title"]')).map((td) => td.textContent ?? "");
  }

  it("a re-mounted table leaves its predecessor's bindings disposed", async () => {
    await load([series(1, "AA"), series(2, "BB")]);
    const discarded = reqEl<HTMLElement>("table.library");
    expect(titlesOf(discarded)).toEqual(["AA", "BB"]);

    // Detail navigation replaces #coverageContent, so the next load re-mounts.
    reqEl("#coverageContent").replaceChildren();
    await load([series(3, "CC")]);
    const live = reqEl<HTMLElement>("table.library");
    expect(live).not.toBe(discarded);
    // The re-mounting load writes the collection BEFORE ensureMounted
    // disposes the old bindings, so the discarded table tracked that one
    // last write...
    expect(titlesOf(discarded)).toEqual(["CC"]);

    await load([series(4, "DD")]);

    // ...and is frozen from the re-mount on: only the live render tracks the
    // collection, while the discarded table's rows and visibility stay
    // exactly as dropped — an undisposed structural binding would have
    // reconciled its tbody to "DD" too.
    expect(titlesOf(live)).toEqual(["DD"]);
    expect(titlesOf(discarded)).toEqual(["CC"]);

    await load([]);

    expect(live.hidden).toBe(true);
    expect(discarded.hidden).toBe(false);
  });
});
