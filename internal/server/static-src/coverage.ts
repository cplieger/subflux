// coverage.ts — library coverage view with filtering, sorting, and badges.
//
// Two-tier reactive model: coverage rows live in a `createCollection` keyed by
// media id (per-row signals); the table is rendered once via `bindList` over a
// computed `visibleIds` view (filter + sort + pagination as a sliced id list).
// A per-row SSE update repaints just that row; a filter/sort/page change
// recomputes the view and reconciles structure. No paged-list, no manual
// per-badge DOM patching.

import * as store from "./store.js";
import { $, el, text, icon, errDiv, input, select, insertNavButton } from "./dom.js";
import { coverageSeries, coverageMovies } from "./wire/client.gen.js";
import { registerCleanup } from "@cplieger/actions";
import { clickableRow, emptyState, langName, coverageMediaId, fmtLangVariant } from "./utils.js";
import { on, emit, BusEvent } from "./bus.js";
import type { DetailConfig } from "./bus.js";
import type { CoverageTarget, CoverageItem } from "./api-types.js";
import { applyScanButtonState } from "./detail-scan.js";
import { seriesScopeKey, movieScopeKey } from "./scan-scope.js";
import { signal, computed, effect, createCollection, bindList, patch } from "@cplieger/reactive";
import { skeletonTiming } from "@cplieger/ui-primitives/skeleton";

// --- Coverage view ---

const COV_PAGE_SIZE = 50;

// The reactive coverage collection (per-row signals + structure signal).
const coverage = createCollection<CoverageItem>(coverageMediaId);

// View state. `filterTick` bumps whenever a filter/sort control changes so the
// `visible` computed re-reads the (DOM-backed) filter inputs; `pageLimit` is
// the "show more" window.
const filterTick = signal(0);
const pageLimit = signal(COV_PAGE_SIZE);

// Filtered + sorted full list (reactive on the collection + filter changes).
const filteredItems = computed(() => {
  void filterTick.value; // dep: re-run when a filter/sort control changes
  return applyFilters(coverage.items());
});

// Paged id list — the structure tier `bindList` renders. Shallow-equal so a
// per-row content update (badge change) does not trigger a structural
// reconcile unless the visible set/order actually changes.
const visibleIds = computed<readonly string[]>(
  () => filteredItems.value.slice(0, pageLimit.value).map(coverageMediaId),
  { equals: (a, b) => a.length === b.length && a.every((x, i) => x === b[i]) },
);

// Per-fetch AbortController so rapid view switches abort the previous
// in-flight coverage load instead of patching stale DOM. Registered with
// the framework so beforeunload also aborts it.
let coverageAbort: AbortController | null = null;
registerCleanup(() => {
  coverageAbort?.abort();
  coverageAbort = null;
});

/** Whether any coverage rows have been loaded. */
export function coverageLoaded(): boolean {
  return coverage.size > 0;
}

/** Snapshot of the current coverage rows (for non-reactive lookups). */
export function coverageItems(): CoverageItem[] {
  return coverage.items();
}

/** Fetch series and movies coverage, merge with _type discriminant, and load
 *  the collection. Aborts any prior in-flight fetch — only the latest wins. */
export async function fetchAndMergeCoverage(): Promise<CoverageItem[]> {
  coverageAbort?.abort();
  coverageAbort = new AbortController();
  const { signal: sig } = coverageAbort;
  const [series, movies] = await Promise.all([
    coverageSeries({ signal: sig }),
    coverageMovies({ signal: sig }),
  ]);
  if (sig.aborted) {
    return coverage.items();
  }
  const merged: CoverageItem[] = [
    ...(series ?? []).map((s) => ({ ...s, _type: "series" as const })),
    ...(movies ?? []).map((m) => ({ ...m, _type: "movie" as const })),
  ];
  coverage.setAll(merged);
  return merged;
}

/** Build the 8-row coverage-table loading skeleton fragment. */
function coverageSkeleton(): DocumentFragment {
  const skel = document.createDocumentFragment();
  for (let i = 0; i < 8; i++) {
    skel.appendChild(
      el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
    );
  }
  return skel;
}

export async function loadCoverage(silent?: boolean): Promise<void> {
  if (store.get("isUnconfigured")) {
    return;
  }
  const out = $.coverageContent;
  const showSkeleton = !silent && coverage.size === 0;
  // Start the fetch first so the anti-flicker timing can honor THIS load's
  // AbortSignal: fetchAndMergeCoverage aborts any prior in-flight load and
  // installs a fresh controller synchronously, so `coverageAbort` is now ours.
  const pending = fetchAndMergeCoverage();
  const ctrl = coverageAbort;
  // Anti-flicker: hold the skeleton back 150ms (a fast load skips it entirely)
  // and, once shown, keep it up at least 300ms so it never appears then
  // instantly vanishes. The skeleton is suppressed when this load is aborted
  // (superseded), so a stale skeleton never lands over a newer load's content.
  // ensureMounted() still runs on commit even when aborted: it mounts the live
  // reactive collection (idempotent, never a stale paint), which the aborting
  // path — an SSE-driven fetchAndMergeCoverage — does not do itself.
  const timing =
    showSkeleton && ctrl
      ? skeletonTiming(
          () => {
            patch(out, coverageSkeleton());
          },
          // The library default is min-visible 0; subflux keeps its 300ms
          // never-blink window explicitly.
          { minVisibleMs: 300, signal: ctrl.signal },
        )
      : null;
  try {
    await pending;
    if (timing) {
      timing.commit(() => {
        ensureMounted();
      });
    } else {
      ensureMounted();
    }
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    if (timing) {
      timing.commit(() => {
        patch(out, errDiv(msg));
      });
    } else if (!silent) {
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
  if (detail.filesAction && store.get("isAdmin")) {
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
    // Cap the displayed count at the total: an item covered by BOTH a real
    // sub and an ignored-codec one would otherwise render "53/52". The
    // ignored-codec detail moves to the tooltip whenever it exists, not only
    // below full coverage.
    const displayHave = Math.min(t.have + (t.have_ignored || 0), t.total);
    frag.appendChild(
      el(
        "span",
        {
          className: "badge badge-split",
          "data-status": status,
          "data-tip":
            (t.have_ignored || 0) > 0 ? `${t.have_ignored} with ignored codec only` : undefined,
        },
        el("span", { className: "badge-lang" }, label),
        el("span", { className: "badge-detail" }, `${displayHave}/${t.total}`),
      ),
    );
  }
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
    const scanBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        "data-tip": "Auto: scan and download missing subtitles",
        "data-scan-scope": isSeries ? seriesScopeKey(item.id) : movieScopeKey(item.id),
        onclick: (e: MouseEvent) => {
          e.stopPropagation();
          emit(scanEvent, { item });
        },
      },
      icon("search"),
      el("span", { className: "btn-text" }, " Search"),
    ) as HTMLButtonElement;
    // Rows painted while a scan runs restore the disabled+spinner state.
    applyScanButtonState(scanBtn);
    actionBtn = scanBtn;
  }

  const openDetail = isSeries
    ? () => {
        emit(BusEvent.OpenSeries, { item });
      }
    : () => {
        emit(BusEvent.OpenMovie, { item });
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

// --- Render: build the table shell once, bind the tbody, react for the rest ---

let bindings: (() => void)[] = [];

function ensureMounted(): void {
  const out = $.coverageContent;
  // Already mounted and still in the DOM (detail navigation replaces the
  // container, so re-mount when the table is gone).
  if (out.querySelector("table.library") !== null) {
    return;
  }
  for (const dispose of bindings) {
    dispose();
  }
  bindings = [];

  const tbody = el("tbody");
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
  const tbl = el("table", { className: "library" }, thead, tbody);
  const emptyEl = emptyState("No media found. Data will appear after the first scheduled scan.");
  const noMatchEl = emptyState("No matching items.");
  // Same pagination affordance as the history table (.more-btn is the one
  // sanctioned full-width pagination style; the old "cov-show-more" class had
  // no CSS rule at all).
  const showMore = el(
    "button",
    {
      type: "button",
      className: "more-btn",
      onclick: () => {
        pageLimit.value += COV_PAGE_SIZE;
      },
    },
    "Show more\u2026",
  );
  patch(out, el("div", { className: "cov-list" }, emptyEl, noMatchEl, tbl, showMore));

  // Content + structure tiers: per-row repaint on entity change, structural
  // reconcile on visibleIds change.
  bindings.push(
    bindList(
      tbody,
      { ids: visibleIds, signalFor: (id: string) => coverage.signalFor(id) },
      {
        mount: (item) => buildCoverageRow(item),
        update: (row, item) => {
          updateCoverageRow(row, item);
        },
      },
    ),
  );

  // Empty-state / show-more / title-width, all derived from the collection +
  // filtered view.
  bindings.push(
    effect(() => {
      const hasData = coverage.ids.value.length > 0;
      const filtered = filteredItems.value;
      const visibleCount = visibleIds.value.length;
      emptyEl.hidden = hasData;
      noMatchEl.hidden = !(hasData && filtered.length === 0);
      const tableEmpty = !hasData || filtered.length === 0;
      tbl.hidden = tableEmpty;
      showMore.hidden = tableEmpty || visibleCount >= filtered.length;
      if (!tableEmpty) {
        const avgLen = Math.ceil(
          (filtered.reduce((sum, i) => sum + i.title.length, 0) / (filtered.length || 1)) * 2,
        );
        tbl.style.setProperty("--title-w", `${avgLen}ch`);
      }
    }),
  );
}

export function renderCoverage(): void {
  configurePanel(true);
  ensureMounted();
}

/** Called by the filter/sort controls — recompute the view from page 0. */
export function filterCoverage(): void {
  pageLimit.value = COV_PAGE_SIZE;
  filterTick.value += 1;
}

function applyFilters(data: CoverageItem[]): CoverageItem[] {
  const filter = input("cov-filter").value.toLowerCase();
  const missingOnly = input("cov-missing").checked;
  const typeFilter = select("cov-type-filter").value;
  const sortBy = select("cov-sort").value;

  let filtered: CoverageItem[] = data;
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
on(BusEvent.PanelConfigure, ({ visible, detail }) => {
  configurePanel(visible, detail);
});
