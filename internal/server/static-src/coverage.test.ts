// @vitest-environment happy-dom
import { describe, it, vi, beforeEach } from "vitest";

// Mock dependencies before importing the module under test.
vi.mock("./api-client.js", () => ({
  apiGetTyped: vi.fn().mockResolvedValue(null),
}));
vi.mock("./actions/index.js", () => ({
  registerCleanup: vi.fn(),
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue(null) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => { /* noop */ }),
  emit: vi.fn(),
  BusEvent: {
    ScanSeries: "scan:series",
    ScanMovie: "scan:movie",
    OpenSeries: "open:series",
    OpenMovie: "open:movie",
  },
}));

import * as store from "./store.js";
import type { CoverageItem as _CoverageItem } from "./api-types.js";

describe("coverage: renderCoverageItems", () => {
  beforeEach(() => {
    store.set("coverageData", null);
    store.set("currentPage", "library");
    store.set("detailCtx", null);
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div><div id="coverageContent"></div></div>';
  });

  it.todo("renders empty state when no items and no coverageData");

  it.todo("renders a table with one row per coverage item");

  it.todo("uses reconcile keys matching coverageMediaId");

  it.todo("updateCoverageRow patches only the badge cell");

  it.todo("excluded items show muted badge instead of search button");

  it.todo("series items emit OpenSeries on row click");

  it.todo("movie items emit OpenMovie on row click");

  it.todo("patchCoverageBadge updates an existing row's badge cell by media ID");

  it.todo("patchCoverageBadge returns false when media ID not in DOM");
});

describe("coverage: filtering", () => {
  it.todo("applyFilters respects text filter (case-insensitive title match)");

  it.todo("applyFilters respects missing-only checkbox");

  it.todo("applyFilters respects type filter (series/movie)");

  it.todo("applyFilters respects sort selection (title/year/coverage)");

  it.todo("filterCoverage increments filterVersion and reloads the paged list");
});

describe("coverage: data fetching", () => {
  it.todo("fetchAndMergeCoverage merges series + movies into coverageData store key");

  it.todo("fetchAndMergeCoverage aborts previous in-flight request");

  it.todo("loadCoverage shows skeleton during fetch");

  it.todo("loadCoverage shows error div on fetch failure");
});
