// router.test.ts — client-side routing.
//
// Two properties are worth the most and both are invisible to any single-route
// smoke test:
//
//  - the route table is ordered most-specific-first, so /series/42/search/fr
//    must NOT be swallowed by /series/42. A regex reorder is a silent
//    regression: every URL still resolves, just to the wrong view.
//  - the library filters round-trip through the query string. The serialiser
//    omits defaults and the restorer supplies them, so an asymmetry either
//    loses a shared link's filters or writes noise into every URL.
//
// The router reads location.pathname directly, so the tests drive the real
// History API (Chromium gives real pushState/replaceState) and restore the
// runner's own URL afterwards. It also resolves the four filter controls at
// IMPORT time, so the fixture is built once at module scope, before the
// import, and reset between tests rather than rebuilt — replacing it would
// leave the module holding detached elements.
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import * as bus from "./bus.js";
import * as store from "./store.js";
import type { CoverageItem } from "./api-types.js";

const coverage = vi.hoisted(() => ({
  pairLanded: true,
  items: [] as unknown[],
  healedRows: [] as unknown[],
  loadCalls: 0,
  renderCalls: 0,
  panelCalls: [] as boolean[],
}));
vi.mock("./coverage.js", () => ({
  loadCoverage: () => {
    coverage.loadCalls++;
    coverage.pairLanded = true;
    return Promise.resolve();
  },
  renderCoverage: () => {
    coverage.renderCalls++;
  },
  configurePanel: (v: boolean) => {
    coverage.panelCalls.push(v);
  },
  libraryLoaded: () => coverage.pairLanded,
  coverageItems: () => coverage.items,
  applyHealedRow: (item: unknown) => {
    coverage.healedRows.push(item);
  },
}));

// The wire client behind A7's item-grain deep-link resolution. Per-root
// summary responses keyed `${kind}:${id}`; a root without an entry answers
// 500 (the transport's failure envelope).
const wire = vi.hoisted(() => ({
  summaries: new Map<string, { ok: boolean; status: number; data?: unknown; error?: string }>(),
  calls: [] as { kind: "series" | "movie"; id: number }[],
}));
vi.mock("./wire/client.gen.js", () => ({
  coverageSeriesSummaryRaw: (id: string | number) => {
    wire.calls.push({ kind: "series", id: Number(id) });
    return Promise.resolve(
      wire.summaries.get(`series:${String(id)}`) ?? { ok: false, status: 500, error: "boom" },
    );
  },
  coverageMovieSummaryRaw: (id: string | number) => {
    wire.calls.push({ kind: "movie", id: Number(id) });
    return Promise.resolve(
      wire.summaries.get(`movie:${String(id)}`) ?? { ok: false, status: 500, error: "boom" },
    );
  },
}));

const collaborators = vi.hoisted(() => ({
  configOpens: [] as (boolean | undefined)[],
  searchCalls: [] as unknown[],
  fileManagerCalls: [] as unknown[],
}));
vi.mock("./config.js", () => ({
  openConfig: (fromRoute?: boolean) => {
    collaborators.configOpens.push(fromRoute);
  },
}));
vi.mock("./search.js", () => ({
  openSearchPopup: (...args: unknown[]) => {
    collaborators.searchCalls.push(args);
  },
}));
vi.mock("./files.js", () => ({
  openFileManager: (...args: unknown[]) => {
    collaborators.fileManagerCalls.push(args);
  },
}));

// The page-leg dispatcher: the router owns its leave path (applyRoute aborts
// the departing route's in-flight page leg). Mocked so the dispatcher's own
// module graph (detail, coverage, history renderers) stays out of this suite.
const pageLeg = vi.hoisted(() => ({ abortCalls: 0 }));
vi.mock("./page-leg.js", () => ({
  abortPageLeg: () => {
    pageLeg.abortCalls++;
  },
  refreshCurrentPage: () => Promise.resolve("applied"),
}));

// The view transition is cosmetic and asynchronous; running the callback
// straight through keeps the assertions about routing. Only the primitive is
// replaced, so utils.ts (setDocTitle included) stays real. The call count
// backs the navigation-only pin: route changes queue through this wrapper
// (heals never do — pinned from the render side in detail.test.ts).
const viewTransitions = vi.hoisted(() => ({ calls: 0 }));
vi.mock("@cplieger/ui-primitives/view-transition", () => ({
  viewTransition: (fn: () => void) => {
    viewTransitions.calls++;
    fn();
    return Promise.resolve();
  },
}));

/** The subset of index.html the router touches. Built before the import
 *  because router.ts resolves the filter controls at module scope. */
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

const router = await import("./router.js");

const HOME = location.pathname + location.search;

function series(tvdbId: number): CoverageItem {
  return { _type: "series", tvdb_id: tvdbId, id: 100 + tvdbId, title: "Show" } as CoverageItem;
}

function movie(tmdbId: number): CoverageItem {
  return { _type: "movie", tmdb_id: tmdbId, id: 200 + tmdbId, title: "Film" } as CoverageItem;
}

/** Point location at `path` without adding a history entry. */
function at(path: string): void {
  history.replaceState(null, "", path);
}

/** Spy on the History API instead of driving it. Every test iframe shares the
 *  runner page's joint session history and Chromium caps it at 50 entries, so
 *  a suite that really pushes both burns that budget and reads a saturated
 *  `history.length` — the assertion stops discriminating a push from a replace
 *  exactly when some other file has pushed enough. Spying names the intended
 *  URL directly and costs the shared history nothing. */
function spyOnHistory(): {
  push: ReturnType<typeof vi.spyOn>;
  replace: ReturnType<typeof vi.spyOn>;
  restore: () => void;
} {
  const push = vi.spyOn(history, "pushState").mockImplementation(() => undefined);
  const replace = vi.spyOn(history, "replaceState").mockImplementation(() => undefined);
  return {
    push,
    replace,
    restore: () => {
      push.mockRestore();
      replace.mockRestore();
    },
  };
}

function el<T extends HTMLElement>(id: string): T {
  const found = document.getElementById(id);
  if (!found) {
    throw new Error(`fixture lost #${id}`);
  }
  return found as T;
}

function filters(): {
  type: HTMLSelectElement;
  q: HTMLInputElement;
  missing: HTMLInputElement;
  sort: HTMLSelectElement;
} {
  return {
    type: el<HTMLSelectElement>("cov-type-filter"),
    q: el<HTMLInputElement>("cov-filter"),
    missing: el<HTMLInputElement>("cov-missing"),
    sort: el<HTMLSelectElement>("cov-sort"),
  };
}

/** Run the ROUTE_TRANSITION_MS-deferred half of the search/sync handlers. */
async function afterRouteTransition(): Promise<void> {
  await new Promise((r) => setTimeout(r, 250));
}

beforeEach(() => {
  coverage.pairLanded = true;
  coverage.items = [series(42), movie(7)];
  coverage.healedRows.length = 0;
  coverage.loadCalls = 0;
  coverage.renderCalls = 0;
  coverage.panelCalls.length = 0;
  wire.summaries.clear();
  wire.calls.length = 0;
  collaborators.configOpens.length = 0;
  collaborators.searchCalls.length = 0;
  collaborators.fileManagerCalls.length = 0;
  pageLeg.abortCalls = 0;
  viewTransitions.calls = 0;
  const f = filters();
  f.type.value = "all";
  f.q.value = "";
  f.missing.checked = false;
  f.sort.value = "title";
  el("lib-heading").textContent = "Library";
  el("coverageContent").replaceChildren();
  el<HTMLInputElement>("h-filter").value = "";
  document.querySelectorAll("#historyPanel .detail-nav").forEach((e) => {
    e.remove();
  });
  at(HOME);
});

afterEach(() => {
  // Leave the runner's own URL as it was found.
  at(HOME);
});

describe("library filter query string", () => {
  it("writes nothing for the default filter state", () => {
    router.updateLibraryFilters();

    expect(location.pathname + location.search).toBe("/");
  });

  it("serialises every non-default filter", () => {
    const f = filters();
    f.type.value = "movies";
    f.q.value = "expanse";
    f.missing.checked = true;
    f.sort.value = "missing";

    router.updateLibraryFilters();

    expect(location.search).toBe("?type=movies&q=expanse&missing=1&sort=missing");
  });

  it("round-trips the filters through the URL", async () => {
    const f = filters();
    f.type.value = "movies";
    f.q.value = "the expanse";
    f.missing.checked = true;
    f.sort.value = "missing";
    router.updateLibraryFilters();

    // A fresh load of that URL — the shared-link case.
    f.type.value = "all";
    f.q.value = "";
    f.missing.checked = false;
    f.sort.value = "title";
    await router.applyRoute();

    expect(f.type.value).toBe("movies");
    expect(f.q.value).toBe("the expanse");
    expect(f.missing.checked).toBe(true);
    expect(f.sort.value).toBe("missing");
  });

  it("restores defaults for a bare URL rather than leaving stale values", async () => {
    const f = filters();
    f.type.value = "movies";
    f.missing.checked = true;
    at("/");

    await router.applyRoute();

    expect(f.type.value).toBe("all");
    expect(f.missing.checked).toBe(false);
  });

  it("replaces rather than pushes, so filtering does not fill the back stack", () => {
    filters().q.value = "dune";
    const h = spyOnHistory();

    router.updateLibraryFilters();

    expect(h.replace).toHaveBeenCalledWith(null, "", "/?q=dune");
    expect(h.push).not.toHaveBeenCalled();
    h.restore();
  });
});

describe("navigate", () => {
  it("pushes a history entry and applies the route", () => {
    at("/history");
    // Applying the route needs the real location, so this one navigates for
    // real and lands on a path it is already on — the push is asserted below.
    router.navigate("/history", true);

    expect(el("historyPanel").hidden).toBe(false);
  });

  it("pushes by default and replaces only when asked", () => {
    at("/");
    const h = spyOnHistory();

    router.navigate("/history");
    expect(h.push).toHaveBeenCalledWith(null, "", "/history");
    expect(h.replace).not.toHaveBeenCalled();

    h.push.mockClear();
    router.navigate("/history", true);
    expect(h.replace).toHaveBeenCalledWith(null, "", "/history");
    expect(h.push).not.toHaveBeenCalled();
    h.restore();
  });

  it("touches history not at all when already on the path", () => {
    at("/history");
    const h = spyOnHistory();

    router.navigate("/history");

    expect(h.push).not.toHaveBeenCalled();
    expect(h.replace).not.toHaveBeenCalled();
    h.restore();
  });
});

describe("route table", () => {
  it("routes a bare series path to the detail view from the cache, zero-fetch", async () => {
    const opened: unknown[] = [];
    const off = bus.on(bus.BusEvent.OpenSeries, (p) => opened.push(p));
    at("/series/42");

    await router.applyRoute();
    off();

    expect(opened).toStrictEqual([{ item: series(42), skipPush: true }]);
    expect(wire.calls).toStrictEqual([]);
    expect(coverage.loadCalls).toBe(0);
  });

  it("prefers the more specific search route over the detail route", async () => {
    // The whole reason the table is ordered: /series/42 also matches the
    // PREFIX of this path, so a reorder sends the user to the plain detail
    // view and the search popup never opens.
    at("/series/42/search/fr");

    await router.applyRoute();
    await afterRouteTransition();

    expect(collaborators.searchCalls).toStrictEqual([["episode", series(42), null, null, "fr"]]);
  });

  it("routes the series files path to the file manager with a back path", async () => {
    at("/series/42/files");

    await router.applyRoute();

    expect(collaborators.fileManagerCalls).toStrictEqual([
      ["episode", "tvdb-42-", "Show", "/series/42", 142],
    ]);
  });

  it("falls back to the series detail when a sync route finds no sync button", async () => {
    at("/series/42/sync");

    await router.applyRoute();
    await afterRouteTransition();

    // No [data-nav="sync"] in the fixture, so the handler corrects the URL
    // instead of leaving the user on a route that does nothing.
    expect(location.pathname).toBe("/series/42");
  });

  it("clicks the sync button when the detail view rendered one", async () => {
    const clicks: string[] = [];
    const btn = document.createElement("button");
    btn.dataset["nav"] = "sync";
    btn.addEventListener("click", () => clicks.push("sync"));
    el("coverageContent").appendChild(btn);
    at("/series/42/sync");

    await router.applyRoute();
    await afterRouteTransition();
    btn.remove();

    expect(clicks).toStrictEqual(["sync"]);
    expect(location.pathname).toBe("/series/42/sync");
  });

  it("routes movie paths to their movie handlers", async () => {
    const opened: unknown[] = [];
    const off = bus.on(bus.BusEvent.OpenMovie, (p) => opened.push(p));
    at("/movie/7");

    await router.applyRoute();
    off();

    expect(opened).toStrictEqual([{ item: movie(7), skipPush: true }]);
  });

  it("prefers the movie search route over the movie detail route", async () => {
    at("/movie/7/search/pb");

    await router.applyRoute();
    await afterRouteTransition();

    expect(collaborators.searchCalls).toStrictEqual([["movie", movie(7), null, null, "pb"]]);
  });

  it("routes the movie files path to the file manager", async () => {
    at("/movie/7/files");

    await router.applyRoute();

    expect(collaborators.fileManagerCalls).toStrictEqual([
      ["movie", "tmdb-7", "Film", "/movie/7", 207],
    ]);
  });

  it("falls back to the movie detail when a sync route finds no sync button", async () => {
    at("/movie/7/sync");

    await router.applyRoute();
    await afterRouteTransition();

    expect(location.pathname).toBe("/movie/7");
  });

  it("renders the not-found empty state for a 404 deep link, no collection fetch", async () => {
    // 999 misses the warm cache too: a miss reads through at item grain and
    // the summary 404s exactly where the collection omits — never a
    // collection fetch, never a crash.
    const opened: unknown[] = [];
    const off = bus.on(bus.BusEvent.OpenSeries, (p) => opened.push(p));
    wire.summaries.set("series:999", { ok: false, status: 404, error: "not found" });
    at("/series/999");

    await router.applyRoute();
    off();

    expect(opened).toStrictEqual([]);
    const empty = document.querySelector<HTMLElement>("#coverageContent .empty");
    expect(empty?.textContent).toContain("Not found");
    expect(empty?.querySelector("button")?.textContent).toBe("Back to library");
    expect(wire.calls).toStrictEqual([{ kind: "series", id: 999 }]);
    expect(coverage.loadCalls).toBe(0);
    expect(coverage.healedRows).toStrictEqual([]);
  });

  it("resolves a cold-cache series deep link through ONE summary GET, no collection GET", async () => {
    coverage.pairLanded = false;
    coverage.items = [];
    wire.summaries.set("series:42", {
      ok: true,
      status: 200,
      data: { tvdb_id: 42, id: 142, title: "Show" },
    });
    const opened: unknown[] = [];
    const off = bus.on(bus.BusEvent.OpenSeries, (p) => opened.push(p));
    at("/series/42");

    await router.applyRoute();
    off();

    // The detail renders directly from the summary row (+ the client
    // discriminant), and the row lands in the collection as the deep-link
    // insert — the real module pins that this insert never flips
    // libraryLoaded (coverage.test.ts) nor opens the heal gate for other
    // roots (coverage-heal.test.ts).
    expect(opened).toStrictEqual([{ item: series(42), skipPush: true }]);
    expect(wire.calls).toStrictEqual([{ kind: "series", id: 42 }]);
    expect(coverage.healedRows).toStrictEqual([series(42)]);
    expect(coverage.loadCalls).toBe(0);
  });

  it("resolves a cold-cache movie deep link through the movie summary endpoint", async () => {
    coverage.pairLanded = false;
    coverage.items = [];
    wire.summaries.set("movie:7", {
      ok: true,
      status: 200,
      data: { tmdb_id: 7, id: 207, title: "Film" },
    });
    const opened: unknown[] = [];
    const off = bus.on(bus.BusEvent.OpenMovie, (p) => opened.push(p));
    at("/movie/7");

    await router.applyRoute();
    off();

    expect(opened).toStrictEqual([{ item: movie(7), skipPush: true }]);
    expect(wire.calls).toStrictEqual([{ kind: "movie", id: 7 }]);
    expect(coverage.healedRows).toStrictEqual([movie(7)]);
    expect(coverage.loadCalls).toBe(0);
  });

  it("renders the error state when a deep-link summary read fails, without crashing", async () => {
    coverage.pairLanded = false;
    coverage.items = [];
    // No summary configured: the wire answers the 500 failure envelope.
    at("/series/42");

    await expect(router.applyRoute()).resolves.toBeUndefined();

    const err = document.querySelector<HTMLElement>('#coverageContent .empty[data-status="err"]');
    expect(err?.textContent).toBe("boom");
    expect(coverage.loadCalls).toBe(0);
    expect(coverage.healedRows).toStrictEqual([]);
  });

  it("opens the settings drawer for /settings without leaving the library page", async () => {
    at("/settings");

    await router.applyRoute();

    expect(collaborators.configOpens).toStrictEqual([true]);
    expect(store.get("currentPage")).toBe("library");
    expect(document.title).toBe("Subflux \u00B7 Settings");
  });

  it("redirects the legacy /movies path onto the filtered library", async () => {
    at("/movies");

    await router.applyRoute();

    expect(location.pathname + location.search).toBe("/?type=movies");
  });

  it("treats an unknown path as the library", async () => {
    at("/nope");

    await router.applyRoute();

    expect(store.get("currentPage")).toBe("library");
    expect(el("coveragePanel").hidden).toBe(false);
  });
});

describe("page switching", () => {
  it("renders the library zero-fetch when the full pair is loaded (back-nav)", async () => {
    at("/");

    await router.applyRoute();

    expect(coverage.renderCalls).toBe(1);
    expect(coverage.loadCalls).toBe(0);
    expect(coverage.panelCalls).toStrictEqual([true]);
  });

  it("fetches the library when the cache is cold", async () => {
    coverage.pairLanded = false;
    coverage.items = [];
    at("/");

    await router.applyRoute();

    expect(coverage.loadCalls).toBe(1);
    expect(coverage.renderCalls).toBe(0);
  });

  it("fetches the full collection when only a deep-link insert is cached", async () => {
    // A deep-link insert leaves a row behind without landing the pair, so
    // the library route must treat the cache as incomplete and run the
    // route loader — the load that sets libraryLoaded and opens the gate.
    coverage.pairLanded = false;
    coverage.items = [series(42)];
    at("/");

    await router.applyRoute();

    expect(coverage.loadCalls).toBe(1);
    expect(coverage.renderCalls).toBe(0);
  });

  it("shows the history panel, marks the nav button active and asks for data", async () => {
    const loads: string[] = [];
    const off = bus.on(bus.BusEvent.LoadHistory, () => loads.push("load"));
    at("/history");

    await router.applyRoute();
    off();

    expect(el("historyPanel").hidden).toBe(false);
    expect(el("coveragePanel").hidden).toBe(true);
    expect(el("historyBtn").classList.contains("active")).toBe(true);
    expect(loads).toStrictEqual(["load"]);
    expect(document.title).toBe("Subflux \u00B7 History");
  });

  it("pre-hides the library chrome on a detail route so it cannot flash", async () => {
    at("/series/42");

    await router.applyRoute();

    // The detail loader paints later; leaving the controls and heading up
    // shows the library for a frame first.
    expect(document.querySelector<HTMLElement>("#coveragePanel .controls")?.style.display).toBe(
      "none",
    );
    expect(el("lib-heading").textContent).toBe("");
    // skipRender: the detail route replaces the content itself.
    expect(coverage.renderCalls).toBe(0);
  });
});

describe("route leave", () => {
  it("aborts the in-flight page leg on every route application", async () => {
    // The departing route's refresh work (the detail pair included) must die
    // when the URL-driven navigation paths run: back/forward, navigate(), and
    // the initial route apply all funnel through applyRoute.
    at("/series/42");
    await router.applyRoute();
    expect(pageLeg.abortCalls).toBe(1);

    at("/");
    await router.applyRoute();
    expect(pageLeg.abortCalls).toBe(2);
  });

  it("navigate() runs the leave path too", () => {
    at("/history");
    router.navigate("/history", true);

    expect(pageLeg.abortCalls).toBe(1);
  });

  it("navigation queues through the view-transition wrapper (navigation-only transitions)", () => {
    // The router is the ONE caller of the queued wrapper: every navigate()
    // rides a view transition, and nothing else does (heals are pinned
    // transition-free from the render side in detail.test.ts).
    at("/history");
    router.navigate("/history", true);

    expect(viewTransitions.calls).toBe(1);
  });
});

describe("history panel back button", () => {
  it("offers Library when the user came straight to the history page", async () => {
    at("/history");

    await router.applyRoute();

    const back = document.querySelector("#historyPanel .detail-nav .btn-text");
    expect(back?.textContent).toBe(" Library");
  });

  it("offers Back and returns to the media page it was opened from", async () => {
    at("/series/42");
    const opening = spyOnHistory();
    router.navigateToHistory("Show");
    expect(opening.push).toHaveBeenCalledWith(null, "", "/history");
    expect(el<HTMLInputElement>("h-filter").value).toBe("Show");
    opening.restore();

    // Render the history page the navigation asked for.
    at("/history");
    await router.applyRoute();
    const back = document.querySelector<HTMLElement>("#historyPanel .detail-nav");
    expect(back?.querySelector(".btn-text")?.textContent).toBe(" Back");

    const returning = spyOnHistory();
    back?.click();
    expect(returning.push).toHaveBeenCalledWith(null, "", "/series/42");
    returning.restore();
  });

  it("clears the media filter when opened with no filter", () => {
    el<HTMLInputElement>("h-filter").value = "stale";
    const h = spyOnHistory();

    router.navigateToHistory();

    expect(el<HTMLInputElement>("h-filter").value).toBe("");
    expect(h.push).toHaveBeenCalledWith(null, "", "/history");
    h.restore();
  });

  it("rebuilds one back button per visit rather than stacking them", async () => {
    at("/history");
    await router.applyRoute();

    await router.applyRoute();

    expect(document.querySelectorAll("#historyPanel .detail-nav")).toHaveLength(1);
  });
});

describe("bus navigation", () => {
  it("navigates on a nav:route event", () => {
    at("/");
    const h = spyOnHistory();

    bus.emit(bus.BusEvent.NavRoute, "/history");

    expect(h.push).toHaveBeenCalledWith(null, "", "/history");
    h.restore();
  });

  it("opens the history page with a filter on a nav:history event", () => {
    at("/");
    const h = spyOnHistory();

    bus.emit(bus.BusEvent.NavHistory, "Film");

    expect(h.push).toHaveBeenCalledWith(null, "", "/history");
    expect(el<HTMLInputElement>("h-filter").value).toBe("Film");
    h.restore();
  });
});
