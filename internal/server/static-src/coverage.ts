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
import { $, el, text, icon, errDiv, input, select } from "./dom.js";
import { coverageSeries, coverageMovies } from "./wire/client.gen.js";
import { registerCleanup } from "@cplieger/actions";
import { clickableRow, emptyState, langName, coverageMediaId, fmtLangVariant } from "./utils.js";
import { on, emit, BusEvent } from "./bus.js";
import type { DetailConfig } from "./bus.js";
import type { CoverageTarget, CoverageItem } from "./api-types.js";
import type { SeriesItem, MovieItem } from "./wire/types.gen.js";
import { registerScanButton } from "./detail-scan.js";
import { seriesScopeKey, movieScopeKey } from "./scan-scope.js";
import {
  signal,
  computed,
  effect,
  createCollection,
  bindList,
  patch,
  batch,
} from "@cplieger/reactive";
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

// --- Task 9 transaction seams: tombstones, covered writers, the leg join ---

// Whether an SSE transaction is open (events.ts brackets it). While open, a
// heal 404-delete records its root as a TOMBSTONE, and every full-pair
// snapshot application drops tombstoned rows — the pair may have been read
// before the arr delete, so an un-dropped row would resurrect what the heal
// already removed.
let coverageTransactionOpen = false;
const tombstones = new Set<string>();
// Full-pair writers whose fetch began while the transaction was open
// ("covered" writers). Tombstones clear at settle only when none is in
// flight — else when the last one lands, so a loader dispatched during the
// transaction that lands after settle still drops.
let coveredPairWriters = 0;

// The transaction's in-flight collection leg (null when the leg is empty or
// no transaction runs). A route loader arriving mid-transaction whose pair
// the leg covers JOINS it instead of issuing a second read.
type CollectionLegJoin = "landed" | "failed" | "uncovered";
let pendingCollectionLeg: Promise<CollectionLegJoin> | null = null;

/** Task 9: bracket an SSE transaction (events.ts). */
export function beginCoverageTransaction(): void {
  coverageTransactionOpen = true;
}

/** Task 9: settle (commit or abort) — tombstones clear only when no covered
 *  full-pair writer is still in flight. Idempotent. */
export function settleCoverageTransaction(): void {
  coverageTransactionOpen = false;
  if (coveredPairWriters === 0) {
    tombstones.clear();
  }
}

/** Task 9: register (or clear) the transaction's in-flight collection leg
 *  for loaders to join. */
export function setCollectionLegJoin(p: Promise<CollectionLegJoin> | null): void {
  pendingCollectionLeg = p;
}

/** Task 9: mark a full-pair write in flight; returns the matching end call.
 *  Only a write that BEGINS during an open transaction is covered. */
export function beginCoveredPairWrite(): () => void {
  if (!coverageTransactionOpen) {
    return () => {
      /* not covered */
    };
  }
  coveredPairWriters += 1;
  return () => {
    coveredPairWriters -= 1;
    if (!coverageTransactionOpen && coveredPairWriters === 0) {
      tombstones.clear();
    }
  };
}

/** Task 9: supersede the in-flight plain pair fetch (the degraded boot's
 *  ungated load) before a fresher full-pair application lands. */
export function abortInFlightPairFetch(): void {
  coverageAbort?.abort();
  coverageAbort = null;
}

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

/** A6 heal delete: a summary 404 means the collection omits this row now.
 *  During a transaction the root is TOMBSTONED so a full-pair snapshot read
 *  before the delete cannot resurrect it (task 9). */
export function removeCoverageRow(rootKey: string): void {
  if (coverageTransactionOpen) {
    tombstones.add(rootKey);
  }
  coverage.remove(rootKey);
}

/** Untracked read of one row by collection key (`tvdb-{n}` / `tmdb-{n}`). */
export function coverageRow(rootKey: string): CoverageItem | undefined {
  return coverage.get(rootKey);
}

/** Test-only: drop all rows, close the heal gate, forget registrations, and
 *  clear the transaction seams. */
export function _resetCoverageForTest(): void {
  coverage.clear();
  pairLanded = false;
  registeredCollectionNames.clear();
  coverageTransactionOpen = false;
  tombstones.clear();
  coveredPairWriters = 0;
  pendingCollectionLeg = null;
  coverageAbort?.abort();
  coverageAbort = null;
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

/** THE shared full-pair application site (E3, task 9's extraction): the
 *  loader's null-collapsing read and the transaction's failure-preserving
 *  collection leg both land here, so the tombstone drop and the A6 reset
 *  rule cannot drift between writers. Rows are merged identity-preservingly
 *  (an unchanged signature keeps the CURRENT object so nothing repaints);
 *  tombstoned roots are DROPPED (a heal 404-delete during the transaction is
 *  newer authority than a pair read before the arr delete); the pair opens
 *  the heal gate and registers only when BOTH sides landed. */
export function applyCoveragePair(
  series: SeriesItem[] | null,
  movies: MovieItem[] | null,
): CoverageItem[] {
  const merged: CoverageItem[] = [
    ...(series ?? []).map((s) => ({ ...s, _type: "series" as const })),
    ...(movies ?? []).map((m) => ({ ...m, _type: "movie" as const })),
  ]
    .filter((item) => !tombstones.has(coverageMediaId(item)))
    .map((item) => {
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
    // transaction collection legs — set by WHICHEVER caller lands it. A null
    // leg is a failed read (the generated client null-collapses), and a
    // failed pair load must open nothing.
    pairLanded = true;
    registeredCollectionNames.add("series").add("movies");
  }
  return merged;
}

/** Fetch series and movies coverage and apply the pair through the shared
 *  application site. A route loader arriving while a transaction's collection
 *  leg covers the pair JOINS that leg (per-collection join, E3) instead of
 *  issuing a second read; a joined loader whose leg FAILS performs only its
 *  non-fetch duties — the pair registration that makes the latch's forced
 *  transaction fetch it — and renders the route's normal loading/empty state.
 *  Aborts any prior in-flight fetch — only the latest wins. */
export async function fetchAndMergeCoverage(): Promise<CoverageItem[]> {
  const leg = pendingCollectionLeg;
  if (leg) {
    const joined = await leg;
    if (joined !== "uncovered") {
      if (joined === "failed") {
        registeredCollectionNames.add("series").add("movies");
      }
      return coverage.items();
    }
    // "uncovered": the leg turned out empty — run the normal load below.
  }
  coverageAbort?.abort();
  coverageAbort = new AbortController();
  const { signal: sig } = coverageAbort;
  const endWrite = beginCoveredPairWrite();
  try {
    const [series, movies] = await Promise.all([
      coverageSeries(undefined, { signal: sig }),
      coverageMovies(undefined, { signal: sig }),
    ]);
    if (sig.aborted) {
      return coverage.items();
    }
    return applyCoveragePair(series, movies);
  } finally {
    endWrite();
  }
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
    registerScanButton(scanBtn);
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

/** Called by the filter/sort controls — recompute the view from page 0.
 *  One flush (R8.4): unbatched, the pageLimit reset alone would reconcile
 *  the table against the OLD filter sliced to page 0, and the tick bump
 *  would reconcile it again — rows surviving the gesture were unmounted by
 *  the first pass and remounted by the second. */
export function filterCoverage(): void {
  batch(() => {
    pageLimit.value = COV_PAGE_SIZE;
    filterTick.value += 1;
  });
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
