// Client-side routing: URL-driven page navigation with view transitions.

import * as store from "./store.js";
import { $, el, errDiv, icon, input, select } from "./dom.js";
import { openConfig } from "./config.js";
import { loadCoverage, configurePanel, renderCoverage } from "./coverage.js";
import { applyHealedRow, coverageItems, libraryLoaded } from "./coverage-store.js";
import { coverageMovieSummaryRaw, coverageSeriesSummaryRaw } from "./wire/client.gen.js";
import { on, emit, BusEvent } from "./bus.js";
import { openSearchPopup } from "./search.js";
import { openFileManager } from "./files.js";
import { abortPageLeg } from "./page-leg.js";
import { viewTransition, setDocTitle, emptyState } from "./utils.js";
import { contentView } from "./view-scope.js";
import { ROUTE_TRANSITION_MS } from "./constants.js";
import { buildPath, parseRoute } from "./route-path.js";
import type { LibraryFilters, Route } from "./route-path.js";
import type { CoverageItem } from "./api-types.js";

// --- Page navigation and client-side routing ---

// Immediately prepare the card for a detail route: hide library
// controls and heading so they don't flash before the detail loads.
function prepareDetailView(): void {
  showPage("library", true);
  const ctrl = $.coveragePanel.querySelector<HTMLElement>(".controls");
  if (ctrl) {
    ctrl.style.display = "none";
  }
  $.libHeading.textContent = "";
  // No eager skeleton here: the detail/files loaders own their loading paint
  // via skeletonTiming (150ms show-delay + 300ms min-visible), so a cached
  // load swaps content in directly without a skeleton flash. The previous
  // view simply remains during the show-delay window.
}

const covTypeFilter = select("cov-type-filter");
const covFilter = input("cov-filter");
const covMissing = input("cov-missing");
const covSort = select("cov-sort");

// Read the four filter controls. route-path.ts owns the query-string codec, so
// this half never sees a URL and that half never sees a control.
function readLibraryFilters(): LibraryFilters {
  return {
    type: covTypeFilter.value,
    q: covFilter.value,
    missing: covMissing.checked,
    sort: covSort.value,
  };
}

// Write the four filter controls.
function applyLibraryFilters(f: LibraryFilters): void {
  covTypeFilter.value = f.type;
  covFilter.value = f.q;
  covMissing.checked = f.missing;
  covSort.value = f.sort;
}

// Push a new URL and apply the route. Use replace=true for initial load
// or when correcting the URL without adding a history entry.
export function navigate(path: string, replace?: boolean): void {
  if (path !== location.pathname + location.search) {
    if (replace) {
      history.replaceState(null, "", path);
    } else {
      history.pushState(null, "", path);
    }
  }
  viewTransition(() => {
    void applyRoute();
  });
}

// Update library filter query params in the URL without a full
// navigation. Called when filter controls change.
export function updateLibraryFilters(): void {
  const newUrl = buildPath({ kind: "library", filters: readLibraryFilters() });
  if (newUrl !== location.pathname + location.search) {
    history.replaceState(null, "", newUrl);
  }
}

// --- Route handlers ---

async function withSeries(
  id: number,
  action: (s: CoverageItem) => void | Promise<void>,
): Promise<void> {
  prepareDetailView();
  const s = await findCoverageItem("series", "tvdb_id", id);
  if (s) {
    await action(s);
  }
}

async function withMovie(
  id: number,
  action: (mv: CoverageItem) => void | Promise<void>,
): Promise<void> {
  prepareDetailView();
  const mv = await findCoverageItem("movie", "tmdb_id", id);
  if (mv) {
    await action(mv);
  }
}

async function handleSeriesSearch(id: number, lang: string): Promise<void> {
  await withSeries(id, (s) => {
    emit(BusEvent.OpenSeries, { item: s, skipPush: true });
    setTimeout(() => {
      openSearchPopup("episode", s, null, null, lang);
    }, ROUTE_TRANSITION_MS);
  });
}

async function handleSeriesSync(id: number): Promise<void> {
  await withSeries(id, (s) => {
    emit(BusEvent.OpenSeries, { item: s, skipPush: true });
    setTimeout(() => {
      const btn = document.querySelector<HTMLElement>('[data-nav="sync"]');
      if (btn) {
        btn.click();
      } else {
        navigate(buildPath({ kind: "series", id }), true);
      }
    }, ROUTE_TRANSITION_MS);
  });
}

function handleSeriesFiles(id: number): Promise<void> {
  return withSeries(id, (s) => {
    openFileManager("episode", `tvdb-${id}-`, s.title, buildPath({ kind: "series", id }), s.id);
  });
}

async function handleSeriesDetail(id: number): Promise<void> {
  await withSeries(id, (s) => {
    emit(BusEvent.OpenSeries, { item: s, skipPush: true });
  });
}

async function handleMovieSearch(id: number, lang: string): Promise<void> {
  await withMovie(id, (mv) => {
    emit(BusEvent.OpenMovie, { item: mv, skipPush: true });
    setTimeout(() => {
      openSearchPopup("movie", mv, null, null, lang);
    }, ROUTE_TRANSITION_MS);
  });
}

async function handleMovieSync(id: number): Promise<void> {
  await withMovie(id, (mv) => {
    emit(BusEvent.OpenMovie, { item: mv, skipPush: true });
    setTimeout(() => {
      const btn = document.querySelector<HTMLElement>('[data-nav="sync"]');
      if (btn) {
        btn.click();
      } else {
        navigate(buildPath({ kind: "movie", id }), true);
      }
    }, ROUTE_TRANSITION_MS);
  });
}

async function handleMovieFiles(id: number): Promise<void> {
  await withMovie(id, (mv) => {
    openFileManager("movie", `tmdb-${id}`, mv.title, buildPath({ kind: "movie", id }), mv.id);
  });
}

async function handleMovieDetail(id: number): Promise<void> {
  await withMovie(id, (mv) => {
    emit(BusEvent.OpenMovie, { item: mv, skipPush: true });
  });
}

// Read location and render the matching view. Called on initial load, pushState
// navigation, and popstate.
export async function applyRoute(): Promise<void> {
  // THE LEAVE PATH (B2 + C2): the view on screen is being left or re-applied,
  // so any in-flight page-leg work belongs to a departed view — abort its
  // controller and release the views that route mounted before the new route
  // renders (abortPageLeg owns both; a released detail view also drops the
  // heal's detail-scoped dirty entries, coverage-heal.ts).
  abortPageLeg();
  // Answered before the parse because parseRoute folds /movies onto the library
  // with DEFAULT filters, which drops the type=movies this alias means.
  if (location.pathname === "/movies") {
    navigate(
      buildPath({
        kind: "library",
        filters: { type: "movies", q: "", missing: false, sort: "title" },
      }),
      true,
    );
    return;
  }
  await applyParsedRoute(parseRoute(location.pathname, location.search));
}

// Every arm returns a value, so noImplicitReturns fails a Route kind added
// without an effect here rather than letting it fall through silently.
function applyParsedRoute(route: Route): Promise<void> {
  switch (route.kind) {
    case "settings":
      showPage("library");
      setDocTitle("Settings");
      openConfig(true);
      return Promise.resolve();
    case "history":
      showPage("history");
      return Promise.resolve();
    case "series":
      return handleSeriesDetail(route.id);
    case "series-sync":
      return handleSeriesSync(route.id);
    case "series-files":
      return handleSeriesFiles(route.id);
    case "series-search":
      return handleSeriesSearch(route.id, route.lang);
    case "movie":
      return handleMovieDetail(route.id);
    case "movie-sync":
      return handleMovieSync(route.id);
    case "movie-files":
      return handleMovieFiles(route.id);
    case "movie-search":
      return handleMovieSearch(route.id, route.lang);
    case "library":
      applyLibraryFilters(route.filters);
      showPage("library");
      return Promise.resolve();
  }
}

// Show a page without pushing history (used by applyRoute).
// skipRender: true to toggle panels without re-rendering content
// (used by detail routes that replace content themselves).
function showPage(page: string, skipRender?: boolean): void {
  store.set("currentPage", page);
  $.coveragePanel.hidden = page !== "library";
  $.historyPanel.hidden = page !== "history";
  $.historyBtn.classList.toggle("active", page === "history");
  // Detail routes overwrite this with the item title once resolved.
  setDocTitle(page === "history" ? "History" : undefined);
  if (page === "history") {
    setHistoryHeader();
  }
  if (skipRender) {
    return;
  }
  if (page === "history") {
    emit(BusEvent.LoadHistory);
  }
  if (page === "library") {
    configurePanel(true);
    // Re-render from cache only when the full pair has landed (a deep-link
    // insert leaves rows behind without completing the library); otherwise
    // the ROUTE LOADER fetches the pair — the load that sets libraryLoaded
    // and opens the heal gate.
    if (libraryLoaded()) {
      renderCoverage();
    } else {
      void loadCoverage();
    }
  }
}

let historyBackPath: string | null = null;

// Manage the history panel header: add/remove back button.
function setHistoryHeader(): void {
  const headerEl = document.querySelector("#historyPanel .card-head");
  const heading = document.getElementById("hist-heading");
  if (!headerEl || !heading) {
    return;
  }
  headerEl.querySelectorAll(".detail-nav").forEach((e: Element) => {
    e.remove();
  });

  heading.textContent = "History";

  const backPath = historyBackPath ?? "/";
  const backText = historyBackPath ? " Back" : " Library";
  const backBtn = el(
    "button",
    {
      type: "button",
      className: "ghost detail-nav",
      onclick: () => {
        historyBackPath = null;
        navigate(backPath);
      },
    },
    icon("arrow-left"),
    el("span", { className: "btn-text" }, backText),
  );
  headerEl.insertBefore(backBtn, heading);
}

// Resolve a detail route's media item: from the coverage cache when held,
// else item-grain through the routed type's summary (A7) — never a collection
// fetch. The resolved row is a deep-link insert: it neither marks the library
// complete nor opens the heal gate. A 404 renders the not-found empty state,
// any other failure the error state.
async function findCoverageItem(
  type: "series" | "movie",
  idField: "tvdb_id" | "tmdb_id",
  id: number,
): Promise<CoverageItem | null> {
  const cached = coverageItems().find(
    (item: CoverageItem) => item._type === type && item[idField] === id,
  );
  if (cached) {
    return cached;
  }
  let status: number;
  let error: string | undefined;
  let row: CoverageItem | null = null;
  if (type === "series") {
    const res = await coverageSeriesSummaryRaw(id);
    ({ status, error } = res);
    if (res.ok && res.data !== undefined) {
      row = { ...res.data, _type: "series" };
    }
  } else {
    const res = await coverageMovieSummaryRaw(id);
    ({ status, error } = res);
    if (res.ok && res.data !== undefined) {
      row = { ...res.data, _type: "movie" };
    }
  }
  if (row) {
    applyHealedRow(row);
    return row;
  }
  // Either way the pane is written with a state that owns no registrations, so
  // it is released rather than mounted (view-scope.ts).
  contentView.clear();
  if (status === 404) {
    $.coverageContent.replaceChildren(
      emptyState("Not found. This title is not in the library.", "Back to library", () => {
        navigate("/");
      }),
    );
  } else {
    $.coverageContent.replaceChildren(errDiv(error ?? "failed to load item"));
  }
  return null;
}

export function navigateToHistory(mediaFilter?: string): void {
  const filterEl = input("h-filter");
  if (mediaFilter) {
    filterEl.value = mediaFilter;
  } else {
    filterEl.value = "";
  }
  // Remember where we came from so the history page can show a back button.
  historyBackPath = mediaFilter ? location.pathname : null;
  navigate("/history");
}
// --- Bus handlers ---
on(BusEvent.NavRoute, (path) => {
  navigate(path);
});
on(BusEvent.NavHistory, (filter) => {
  navigateToHistory(filter);
});
