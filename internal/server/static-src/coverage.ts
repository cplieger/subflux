// coverage.ts — library coverage view with filtering, sorting, and badges.
//
// Two-tier reactive model: coverage rows live in a `createCollection` keyed by
// media id (per-row signals); the table is rendered once via `bindList` over a
// computed `visibleIds` view (filter + sort + pagination as a sliced id list).
// A refresh merges identity-preservingly: a row whose content signature is
// unchanged keeps its CURRENT object, so its signal never fires and nothing
// repaints; a changed row repaints whole, gated by `data-sig`. A filter/sort/
// page change recomputes the view and reconciles structure. No paged-list, no
// manual per-badge DOM patching.

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
import { join } from "@cplieger/keyenc";

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

// --- A6 pair-landing state: the heal gate + task 9's collection-leg seam ---

// True once a full series+movies pair has LANDED this tab. Row-level upserts
// (a heal or deep-link insert into an incomplete collection) never set it, so
// an incomplete collection cannot open the heal gate.
let pairLanded = false;
// Collections a landed pair registered for later transaction collection legs
// (task 9 reads registeredCollections ∪ the current route's needs). Tab
// state — survives SSE boot_id changes.
const registeredCollectionNames = new Set<string>();

/** A6's heal gate: true once the full library pair has landed this tab. */
export function libraryLoaded(): boolean {
  return pairLanded;
}

/** Collections registered by landed pair loads (task 9's collection leg). */
export function registeredCollections(): ReadonlySet<string> {
  return registeredCollectionNames;
}

/** A6 heal write: identity-preserving single-row upsert. A row whose
 *  signature is unchanged keeps its CURRENT object (its signal never fires,
 *  nothing repaints); a changed row lands whole and repaints through the
 *  data-sig-gated full-row updater; a new root is appended (display order is
 *  the filtered view's sort, not collection order). */
export function applyHealedRow(item: CoverageItem): void {
  const cur = coverage.get(coverageMediaId(item));
  if (cur !== undefined && coverageItemSignature(cur) === coverageItemSignature(item)) {
    return;
  }
  coverage.upsert(item);
}

/** A6 heal delete: a summary 404 means the collection omits this row now. */
export function removeCoverageRow(rootKey: string): void {
  coverage.remove(rootKey);
}

/** Untracked read of one row by collection key (`tvdb-{n}` / `tmdb-{n}`). */
export function coverageRow(rootKey: string): CoverageItem | undefined {
  return coverage.get(rootKey);
}

/** Test-only: drop all rows, close the heal gate, forget registrations. */
export function _resetCoverageForTest(): void {
  coverage.clear();
  pairLanded = false;
  registeredCollectionNames.clear();
}

/** Snapshot of the current coverage rows (for non-reactive lookups). */
export function coverageItems(): CoverageItem[] {
  return coverage.items();
}

/** Canonical content signature over EVERY field the library view renders,
 *  keys, filters, sorts, or hands onward through a row action (row click →
 *  detail, Search → scan): the full SeriesItem/MovieItem wire structs plus
 *  the client `_type`. Audited by coverage.test.ts's per-field mutation
 *  tables, typed over `keyof Required<CoverageItem>` and CoverageTarget so a
 *  wire field change fails compilation there and re-runs the audit. Nested
 *  keyenc `join`s (detail.ts's epSig discipline): a separator-carrying value
 *  cannot alias field boundaries; a collision only skips a repaint, never
 *  misidentifies a row. */
function coverageItemSignature(item: CoverageItem): string {
  return join(
    item._type,
    String(item.id),
    String(item.tvdb_id ?? ""),
    String(item.tmdb_id ?? ""),
    item.imdb_id ?? "",
    item.title,
    String(item.year),
    item.first_aired ?? "",
    item.in_cinemas ?? "",
    item.digital_release ?? "",
    item.rule,
    item.audio_lang,
    join(...(item.tags ?? []).map(String)),
    item.excluded ? "1" : "0",
    String(item.has_file ?? ""),
    item.scene_name ?? "",
    String(item.episodes ?? ""),
    join(
      ...item.targets.map((t) =>
        join(t.language, t.variant, String(t.have), String(t.have_ignored), String(t.total)),
      ),
    ),
  );
}

/** Fetch series and movies coverage, merge with _type discriminant, and load
 *  the collection identity-preservingly: a row whose signature is unchanged
 *  keeps its CURRENT object, so its per-row signal never fires (signals skip
 *  Object.is-equal writes) and unchanged rows never repaint — the filtered/
 *  sorted views recompute only when something real changed. Aborts any prior
 *  in-flight fetch — only the latest wins. */
export async function fetchAndMergeCoverage(): Promise<CoverageItem[]> {
  coverageAbort?.abort();
  coverageAbort = new AbortController();
  const { signal: sig } = coverageAbort;
  const [series, movies] = await Promise.all([
    coverageSeries(undefined, { signal: sig }),
    coverageMovies(undefined, { signal: sig }),
  ]);
  if (sig.aborted) {
    return coverage.items();
  }
  const merged: CoverageItem[] = [
    ...(series ?? []).map((s) => ({ ...s, _type: "series" as const })),
    ...(movies ?? []).map((m) => ({ ...m, _type: "movie" as const })),
  ].map((item) => {
    const cur = coverage.get(coverageMediaId(item));
    return cur !== undefined && coverageItemSignature(cur) === coverageItemSignature(item)
      ? cur
      : item;
  });
  // A6 RESET RULE: every row is about to be overwritten, so the heal
  // coalescer aborts its in-flight per-root GETs and drops its pending window
  // before the snapshot lands.
  emit(BusEvent.CoverageOverwrite);
  coverage.setAll(merged);
  if (series !== null && movies !== null) {
    // The pair LANDED: open A6's heal gate and register the pair for task 9's
    // transaction collection legs. A null leg is a failed read (the generated
    // client null-collapses), and a failed pair load must open nothing.
    pairLanded = true;
    registeredCollectionNames.add("series").add("movies");
  }
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

/** The five cells of a coverage row, all derived from the item. Shared by
 *  mount and the full-row updater so the two can never drift; the button
 *  closures capture the item they were painted from, which the `data-sig`
 *  gate keeps current up to signature equality. */
function coverageRowCells(item: CoverageItem): HTMLElement[] {
  const isSeries = item._type === "series";
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

  return [
    el("td", { "data-col": "title" }, item.title),
    el("td", { "data-col": "meta" }, String(item.year || "")),
    el("td", { "data-col": "meta" }, langName(item.rule || "default")),
    el("td", { "data-col": "badges", "data-cov-id": covId }, buildBadges(item.targets)),
    el("td", { "data-col": "actions" }, actionBtn),
  ];
}

function buildCoverageRow(item: CoverageItem): HTMLElement {
  const covId = coverageMediaId(item);
  const row = clickableRow(
    () => {
      // Late-bound: the collection is the source of truth, so a click always
      // hands the CURRENT entity to the detail view — a closure over the
      // mount-time item would go stale the first time this row is updated.
      const cur = coverage.get(covId);
      if (!cur) {
        return;
      }
      emit(cur._type === "series" ? BusEvent.OpenSeries : BusEvent.OpenMovie, { item: cur });
    },
    ...coverageRowCells(item),
  );
  row.dataset["sig"] = coverageItemSignature(item);
  return row;
}

/** Full-row in-place updater, gated by `data-sig` (the detail.ts discipline):
 *  a signature-equal update is a no-op, and a real change rebuilds every cell
 *  from the fresh item — title, year, audio, badges, action — so no cell can
 *  serve stale content. The row element itself is kept (focus and structure
 *  tier untouched). */
function updateCoverageRow(row: HTMLElement, item: CoverageItem): void {
  const sig = coverageItemSignature(item);
  if (row.dataset["sig"] === sig) {
    return;
  }
  row.replaceChildren(...coverageRowCells(item));
  row.dataset["sig"] = sig;
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

  // Empty-state / show-more visibility, derived from the collection +
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
