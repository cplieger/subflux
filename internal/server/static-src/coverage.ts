// coverage.ts — library coverage view with filtering, sorting, and badges

import * as store from "./store.js";
import { $, el, text, icon, patch, errDiv, input, select, insertNavButton } from "./dom.js";
import { apiGetTyped } from "./api-client.js";
import { registerCleanup } from "./actions/index.js";
import { decodeSeriesItem, decodeMovieItem } from "./wire/decoders.gen.js";
import { decodeArray } from "./validators.js";
import { clickableRow, emptyState, langName, coverageMediaId, fmtLangVariant } from "./utils.js";
import { on, emit, BusEvent } from "./bus.js";
import type { DetailConfig } from "./bus.js";
import { createPagedList } from "./paged-list.js";
import type { PagedList, Page } from "./paged-list.js";
import type { CoverageTarget, CoverageItem } from "./api-types.js";
import { reconcile } from "./lib/reactive/reconcile.js";

// --- Coverage view ---

const COV_PAGE_SIZE = 50;

// Per-fetch AbortController so rapid view switches abort the previous
// in-flight coverage load instead of patching stale DOM. Registered with
// the framework so beforeunload also aborts it.
let coverageAbort: AbortController | null = null;
registerCleanup(() => {
  coverageAbort?.abort();
  coverageAbort = null;
});

/** Fetch series and movies coverage, merge with _type discriminant, and store.
 *  Aborts any prior in-flight fetch — only the latest call wins. */
export async function fetchAndMergeCoverage(): Promise<CoverageItem[]> {
  coverageAbort?.abort();
  coverageAbort = new AbortController();
  const { signal } = coverageAbort;
  const decodeSeriesList = (v: unknown) => decodeArray(v, decodeSeriesItem, "$.series");
  const decodeMovieList = (v: unknown) => decodeArray(v, decodeMovieItem, "$.movies");
  const [series, movies] = await Promise.all([
    apiGetTyped("/api/coverage/series", decodeSeriesList, signal),
    apiGetTyped("/api/coverage/movies", decodeMovieList, signal),
  ]);
  if (signal.aborted) {
    return store.get("coverageData") ?? [];
  }
  const merged: CoverageItem[] = [
    ...(series ?? []).map((s) => ({ ...s, _type: "series" as const })),
    ...(movies ?? []).map((m) => ({ ...m, _type: "movie" as const })),
  ];
  store.set("coverageData", merged);
  return merged;
}

let list: PagedList | null = null;

// Cache the last filtered+sorted result so fetchPage can slice from it.
let filteredCache: CoverageItem[] = [];
let filterVersion = 0;
let cachedFilterVersion = -1;

export async function loadCoverage(silent?: boolean): Promise<void> {
  if (store.get("isUnconfigured")) {
    return;
  }
  const out = $.coverageContent;
  if (!silent) {
    const skel = document.createDocumentFragment();
    for (let i = 0; i < 8; i++) {
      skel.appendChild(
        el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
      );
    }
    patch(out, skel);
  }
  try {
    await fetchAndMergeCoverage();
    filterVersion++;
    await ensureList().reload();
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    if (!silent) {
      patch(out, errDiv(msg));
    }
  }
}

export function configurePanel(visible: boolean, detail?: DetailConfig): void {
  const ctrl = $.coveragePanel.querySelector<HTMLElement>(".controls");
  if (ctrl) {
    ctrl.style.display = visible ? "" : "none";
  }
  const heading = $.libHeading;
  const headerEl = $.coveragePanel.querySelector<HTMLElement>(".card-head");
  if (!headerEl) {
    return;
  }
  headerEl.hidden = false;

  // Remove any previous detail nav elements.
  headerEl.querySelectorAll("[data-nav]").forEach((e: Element) => {
    e.remove();
  });

  if (!detail) {
    heading.textContent = "Library";
    store.set("detailCtx", null);
    return;
  }

  // Drilldown mode: set title with info, add back button and arr link.
  heading.textContent = "";
  heading.appendChild(text(detail.title));
  if (detail.info) {
    heading.appendChild(el("span", { className: "detail-info" }, detail.info));
  }

  // Back button before the heading.
  const backBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      "data-nav": "back",
      onclick: () => {
        history.back();
      },
    },
    icon("arrow-left"),
    el("span", { className: "btn-text" }, " Back"),
  );
  headerEl.insertBefore(backBtn, heading);

  // Arr link after the heading.
  if (detail.arrLink) {
    const name = detail.arrName ?? "Sonarr";
    const cls = name === "Radarr" ? "arr-radarr" : "arr-sonarr";
    headerEl.appendChild(
      el(
        "button",
        {
          type: "button",
          className: cls,
          "data-tip": `Open in ${name}`,
          "data-nav": "arr",
          onclick: () => {
            if (detail.arrLink) {
              window.open(detail.arrLink, "_blank", "noopener");
            }
          },
        },
        icon("external"),
        ` ${name}`,
      ),
    );
  }

  // History button between back and arr link.
  if (detail.historyAction) {
    const histBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        "data-nav": "hist",
        onclick: detail.historyAction,
      },
      icon("history"),
      el("span", { className: "btn-text" }, " History"),
    );
    insertNavButton(histBtn);
  }

  // Files button.
  if (detail.filesAction) {
    const filesBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        "data-nav": "files",
        "data-tip": "Manage subtitle files",
        onclick: detail.filesAction,
      },
      icon("file"),
      el("span", { className: "btn-text" }, " Files"),
    );
    insertNavButton(filesBtn);
  }
}

// Build coverage badge fragment for a single item's targets.
function buildBadges(targets: CoverageTarget[]): DocumentFragment {
  const frag = document.createDocumentFragment();
  if (targets.length === 0) {
    frag.appendChild(el("span", { className: "badge", "data-status": "muted" }, "not scanned"));
    return frag;
  }
  for (const t of targets) {
    const label = fmtLangVariant(t.language, t.variant);
    const pct = t.total > 0 ? t.have / t.total : 0;
    const ignoredPct = t.total > 0 ? (t.have_ignored || 0) / t.total : 0;
    let status = "err";
    if (pct >= 1) {
      status = "ok";
    } else if (pct > 0) {
      status = "partial";
    } else if (ignoredPct > 0) {
      status = "warn";
    }
    const displayHave = t.have + (t.have_ignored || 0);
    frag.appendChild(
      el(
        "span",
        {
          className: "badge badge-split",
          "data-status": status,
          "data-tip":
            ignoredPct > 0 && pct < 1 ? `${t.have_ignored} with ignored codec only` : undefined,
        },
        el("span", { className: "badge-lang" }, label),
        el("span", { className: "badge-detail" }, `${displayHave}/${t.total}`),
      ),
    );
  }
  return frag;
}

// Patch a single item's coverage badges in the DOM without full re-render.
// Returns true if the patch was applied, false if the element wasn't found.
export function patchCoverageBadge(mediaId: string, targets: CoverageTarget[]): boolean {
  const cell = document.querySelector(`[data-cov-id="${CSS.escape(mediaId)}"]`);
  if (!cell) {
    return false;
  }
  cell.replaceChildren(buildBadges(targets));
  // Also update the in-memory store so the next full render is consistent.
  const data = store.get("coverageData");
  if (data) {
    const item = data.find((i: CoverageItem) => coverageMediaId(i) === mediaId);
    if (item) {
      item.targets = targets;
    }
  }
  return true;
}

/** Fetch a page from the in-memory filtered coverage data. */
function fetchCoveragePage(offset: number, limit: number): Promise<Page<CoverageItem>> {
  if (cachedFilterVersion !== filterVersion) {
    filteredCache = applyFilters(store.get("coverageData"));
    cachedFilterVersion = filterVersion;
  }
  const slice = filteredCache.slice(offset, offset + limit);
  return Promise.resolve({ items: slice, hasMore: offset + limit < filteredCache.length });
}

/** Render accumulated coverage items into a table fragment. */
function renderCoverageItems(items: CoverageItem[]): DocumentFragment {
  const covData = store.get("coverageData");
  if (items.length === 0 && (!covData || covData.length === 0)) {
    const frag = document.createDocumentFragment();
    frag.appendChild(
      emptyState("No media found. Data will appear after the first scheduled scan."),
    );
    return frag;
  }

  const thead = el(
    "thead",
    null,
    el(
      "tr",
      null,
      el("th", null, "Title"),
      el("th", null, "Year"),
      el("th", { "data-tip": "Primary audio language" }, "Audio"),
      el("th", null, "Subtitles"),
      el("th"),
    ),
  );
  const tbody = el("tbody");

  reconcile(tbody, items, {
    key: (item) => coverageMediaId(item),
    mount: (item) => buildCoverageRow(item),
    update: (row, item) => {
      updateCoverageRow(row, item);
    },
  });

  const frag = document.createDocumentFragment();
  const tbl = el("table", { className: "library" }, thead, tbody);
  const source = filteredCache.length > 0 ? filteredCache : items;
  const avgLen = Math.ceil(
    (source.reduce((sum: number, i: CoverageItem) => sum + i.title.length, 0) /
      (source.length || 1)) *
      2,
  );
  tbl.style.setProperty("--title-w", `${avgLen}ch`);
  frag.appendChild(tbl);
  return frag;
}

function buildCoverageRow(item: CoverageItem): HTMLElement {
  const isSeries = item._type === "series";
  const targets = item.targets;
  const covFrag = buildBadges(targets);
  const covId = coverageMediaId(item);

  const arrType = isSeries ? "Sonarr" : "Radarr";
  let actionBtn: HTMLElement;
  if (item.excluded) {
    actionBtn = el(
      "span",
      {
        className: "badge",
        "data-status": "muted",
        "data-tip": `Excluded by arr tag. Remove the tag in ${arrType} to enable.`,
      },
      icon("excluded"),
    );
  } else {
    const scanEvent = isSeries ? BusEvent.ScanSeries : BusEvent.ScanMovie;
    actionBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        "data-tip": "Auto: scan and download missing subtitles",
        onclick: (e: MouseEvent) => {
          e.stopPropagation();
          emit(scanEvent, item, e.currentTarget as HTMLButtonElement | null);
        },
      },
      icon("search"),
      el("span", { className: "btn-text" }, " Search"),
    );
  }

  const openDetail = isSeries
    ? () => {
        emit(BusEvent.OpenSeries, item);
      }
    : () => {
        emit(BusEvent.OpenMovie, item);
      };

  return clickableRow(
    openDetail,
    el("td", { "data-col": "title" }, item.title),
    el("td", { "data-col": "meta" }, String(item.year || "")),
    el("td", { "data-col": "meta" }, langName(item.rule || "default")),
    el("td", { "data-col": "badges", "data-cov-id": covId }, covFrag),
    el("td", { "data-col": "actions" }, actionBtn),
  );
}

function updateCoverageRow(row: HTMLElement, item: CoverageItem): void {
  const covId = coverageMediaId(item);
  const cell = row.querySelector(`[data-cov-id="${CSS.escape(covId)}"]`);
  if (cell) {
    cell.replaceChildren(buildBadges(item.targets));
  }
}

function ensureList(): PagedList {
  list ??= createPagedList<CoverageItem>({
    container: $.coverageContent,
    fetchPage: fetchCoveragePage,
    renderItems: renderCoverageItems,
    pageSize: COV_PAGE_SIZE,
    emptyMessage: "No matching items.",
    emptyNoData: "No media found. Data will appear after the first scheduled scan.",
  });
  return list;
}

export function renderCoverage(): void {
  configurePanel(true);
  void ensureList().reload();
}

export function filterCoverage(): void {
  filterVersion++;
  void ensureList().reload();
}

function applyFilters(data: CoverageItem[] | null | undefined): CoverageItem[] {
  const filter = input("cov-filter").value.toLowerCase();
  const missingOnly = input("cov-missing").checked;
  const typeFilter = select("cov-type-filter").value;
  const sortBy = select("cov-sort").value;

  let filtered: CoverageItem[] = data ?? [];
  if (typeFilter === "series") {
    filtered = filtered.filter((item: CoverageItem) => item._type === "series");
  } else if (typeFilter === "movies") {
    filtered = filtered.filter((item: CoverageItem) => item._type === "movie");
  }
  if (filter) {
    filtered = filtered.filter((item: CoverageItem) => item.title.toLowerCase().includes(filter));
  }
  if (missingOnly) {
    filtered = filtered.filter((item: CoverageItem) => {
      const targets = item.targets;
      if (targets.length === 0) {
        return true;
      }
      return targets.some((t: CoverageTarget) => t.have < t.total);
    });
  }

  // Sort.
  const dateOf = (item: CoverageItem): string =>
    item.first_aired ?? item.in_cinemas ?? item.digital_release ?? "";

  return filtered.slice().sort((a: CoverageItem, b: CoverageItem): number => {
    if (sortBy === "title-desc") {
      return b.title.localeCompare(a.title);
    }
    if (sortBy === "newest") {
      const dateA = dateOf(a);
      const dateB = dateOf(b);
      if (dateA || dateB) {
        const cmp = dateB.localeCompare(dateA);
        if (cmp !== 0) {
          return cmp;
        }
      }
      const diff = (b.year || 0) - (a.year || 0);
      return diff !== 0 ? diff : a.title.localeCompare(b.title);
    }
    if (sortBy === "oldest") {
      const dateA = dateOf(a);
      const dateB = dateOf(b);
      if (dateA || dateB) {
        const cmp = dateA.localeCompare(dateB);
        if (cmp !== 0) {
          return cmp;
        }
      }
      const diff = (a.year || 0) - (b.year || 0);
      return diff !== 0 ? diff : a.title.localeCompare(b.title);
    }
    return a.title.localeCompare(b.title);
  });
}

// --- Bus handler: detail.js emits panel:configure ---
on(BusEvent.PanelConfigure, (visible, detail) => {
  configurePanel(visible, detail);
});
