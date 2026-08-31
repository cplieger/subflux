// history.ts — download history page (server-side paginated).
//
// The accumulated entries live in a createCollection keyed by the server's
// unique row id; bindList renders them. "Reload" (filter change)
// is setAll(page 0); "show more" fetches the next server page and upserts it
// (appended). The collection IS the bindList ListSource — no per-row updates
// (history is append-only display), so only the structure tier does work.

import { el, input, select, option, errDiv } from "./dom.js";
import { listStateRaw } from "./wire/client.gen.js";
import type { QueryValue } from "./wire/client.gen.js";
import type { StateEntry } from "./wire/types.gen.js";
import { on, emit, BusEvent } from "./bus.js";
import { fmtDateTime, fmtEpisode, clickableRow, emptyState } from "./utils.js";
import { signal, effect, createCollection, bindList, patch } from "@cplieger/reactive";
import { skeletonTiming } from "@cplieger/ui-primitives/skeleton";
import * as store from "./store.js";

const PAGE_SIZE = 50;

// Key on the server's unique row id. media_id+media_imported is NOT unique
// (one row per language + a new row per manual download, and media_imported is
// a second-precision timestamp), so it collided — the collection dropped rows
// and reconcile mounted duplicates.
const historyKey = (e: StateEntry): string => String(e.id);
const history = createCollection<StateEntry>(historyKey);

// Whether the server has more pages beyond what's loaded (drives "show more").
const hasMore = signal(false);
// Render tick so the filter-aware empty-state effect re-runs after every load
// even when the id list is unchanged (empty -> empty on a filter change, where
// setAll's shallow-equal order signal doesn't fire).
const renderTick = signal(0);
// Monotonic token: the latest reload/loadMore wins. A stale in-flight fetch
// (e.g. a filter-change reload superseding an in-flight show-more) is discarded
// on return, so the two never interleave into the collection.
let gen = 0;

// --- THE SETTLEMENT MODEL (E3, task 9) ---
//
// Every created or queued generation settles exactly once as
// applied | failed | superseded(next) | abandoned, published through this
// module-owned gen → settlement handle. A superseder settles its priors at
// START (g !== gen proves it started, not that it applied), so a waiter
// resolves "superseded" only by chaining superseded(next) links to a
// generation that settles applied; a chain ending failed or abandoned leaves
// the transaction leg REJECTING. `next: null` marks the dispatcher's
// RE-ROUTE (route leave during a transaction): the continuation is the new
// route's page leg, owned by the dispatcher's re-dispatch, never a latch.
// `abandoned` is a DEFENSIVE terminal with no shipped producer — any
// settlement path not otherwise named rejects loudly rather than hanging.
// Task 12 replaces the reload internals behind this same seam and contract.

type HistorySettlement =
  | { kind: "applied" }
  | { kind: "failed"; error: Error }
  | { kind: "superseded"; next: number | null }
  | { kind: "abandoned" };

interface SettlementRecord {
  promise: Promise<HistorySettlement>;
  resolve: (s: HistorySettlement) => void;
  settled: HistorySettlement | null;
}

const settlements = new Map<number, SettlementRecord>();
// Ledger bound: settled records past the cap are dropped oldest-first (an
// evicted generation can no longer be awaited — chains are short-lived).
const SETTLEMENT_CAP = 64;

/** Create the next generation; every prior UNSETTLED generation settles
 *  superseded(next = the new one) — the superseder started. */
function newGeneration(): number {
  const g = gen + 1;
  for (const [pg, rec] of settlements) {
    if (rec.settled === null) {
      settleGeneration(pg, { kind: "superseded", next: g });
    }
  }
  gen = g;
  let resolve!: (s: HistorySettlement) => void;
  const promise = new Promise<HistorySettlement>((res) => {
    resolve = res;
  });
  settlements.set(g, { promise, resolve, settled: null });
  for (const [pg, rec] of settlements) {
    if (settlements.size <= SETTLEMENT_CAP) {
      break;
    }
    if (rec.settled !== null) {
      settlements.delete(pg);
    }
  }
  return g;
}

/** Publish a generation's settlement — exactly once; later calls no-op. */
function settleGeneration(g: number, s: HistorySettlement): void {
  const rec = settlements.get(g);
  if (rec?.settled !== null) {
    return;
  }
  rec.settled = s;
  rec.resolve(s);
}

/** Resolve a generation's outcome through the settlement chain. Returns
 *  "applied" (this run landed), "superseded" (a superseder's chain ended
 *  applied), or "rerouted" (the dispatcher's re-route owns the
 *  continuation); rejects when the chain ends failed or abandoned. */
async function chainSettlement(g: number): Promise<"applied" | "superseded" | "rerouted"> {
  let cur = g;
  for (;;) {
    const rec = settlements.get(cur);
    if (!rec) {
      // Evicted record: the chain outlived the ledger bound. Nothing left to
      // await — treat the chain as landed.
      return cur === g ? "applied" : "superseded";
    }
    const s = await rec.promise;
    switch (s.kind) {
      case "applied":
        return cur === g ? "applied" : "superseded";
      case "superseded":
        if (s.next === null) {
          return "rerouted";
        }
        cur = s.next;
        break;
      case "failed":
        throw s.error;
      case "abandoned":
        throw new Error("history reload abandoned");
    }
  }
}

/** Query for a history page: limit always, offset only past page 0, and the
 *  filter params only when non-empty (undefined entries are skipped at
 *  serialization — exactly the params the hand-built URLSearchParams sent). */
function buildQuery(offset: number, limit: number): Record<string, QueryValue> {
  const type = select("h-type").value;
  const lang = select("h-lang").value;
  const prov = select("h-provider").value;
  const search = input("h-filter").value.trim();
  return {
    limit,
    offset: offset > 0 ? offset : undefined,
    type: type || undefined,
    lang: lang || undefined,
    provider: prov || undefined,
    search: search || undefined,
  };
}

function historyMediaHref(entry: StateEntry): string {
  const mid = entry.media_id || "";
  const tm = /^tmdb-(\d+)$/.exec(mid);
  if (tm) {
    return `/movie/${tm[1] ?? ""}`;
  }
  const tv = /^tvdb-(\d+)/.exec(mid);
  if (tv) {
    return `/series/${tv[1] ?? ""}`;
  }
  return "";
}

function buildHistoryRow(entry: StateEntry): HTMLElement {
  const time = fmtDateTime(new Date(entry.media_imported));
  let label = entry.title || "";
  const season = entry.season ?? 0;
  const episode = entry.episode ?? 0;
  if (season > 0 || episode > 0) {
    label += ` \u00B7 ${fmtEpisode(season, episode)}`;
  }
  const href = historyMediaHref(entry);
  // Non-standard variants (forced/hi) qualify the language cell; standard
  // stays bare so the common case reads clean.
  const lang = entry.variant !== "standard" ? `${entry.language} ${entry.variant}` : entry.language;
  const cells = [
    el("td", { "data-col": "meta" }, time),
    el("td", { "data-col": "title" }, label),
    el("td", { "data-col": "meta" }, lang),
    el("td", { "data-col": "meta" }, entry.provider),
    el("td", { "data-col": "meta" }, entry.manual ? "manual" : "auto"),
    el("td", { "data-col": "meta" }, entry.release_name || ""),
  ];
  return href
    ? clickableRow(
        () => {
          emit(BusEvent.NavRoute, href);
        },
        ...cells,
      )
    : el("tr", null, ...cells);
}

/** Populate language and provider dropdowns. Options come from the STABLE
 *  sources — configured languages and the provider map — merged with the
 *  values seen in loaded rows (covers history from since-removed providers or
 *  languages). Deriving from loaded pages alone made options depend on how
 *  many "Show more" pages happened to be loaded: filtering is server-side, so
 *  a provider present only in older history was unselectable. */
function updateHistoryFilters(data: StateEntry[]): void {
  const cfg = store.get("config");
  const langs = new Set<string>(cfg?.languages ?? []);
  const provs = new Set<string>(Object.keys(cfg?.providers ?? {}));
  for (const entry of data) {
    if (entry.language) {
      langs.add(entry.language);
    }
    if (entry.provider) {
      provs.add(entry.provider);
    }
  }

  const hLang = select("h-lang");
  {
    const current = hLang.value;
    hLang.replaceChildren(option("", "All languages"));
    for (const l of [...langs].sort()) {
      hLang.appendChild(option(l, l));
    }
    hLang.value = current;
  }

  const hProv = select("h-provider");
  {
    const current = hProv.value;
    hProv.replaceChildren(option("", "All providers"));
    for (const p of [...provs].sort()) {
      hProv.appendChild(option(p, p));
    }
    hProv.value = current;
  }
}

function anyFilterActive(): boolean {
  return Boolean(
    select("h-type").value ||
    select("h-lang").value ||
    select("h-provider").value ||
    input("h-filter").value.trim(),
  );
}

// --- Render: build the table shell once, bind the tbody, react for the rest ---

let bindings: (() => void)[] = [];

function disposeBindings(): void {
  for (const dispose of bindings) {
    dispose();
  }
  bindings = [];
}

function ensureMounted(): void {
  const out = document.getElementById("historyContent");
  if (!out) {
    throw new Error("historyContent not found");
  }
  if (out.querySelector("table.history") !== null) {
    return;
  }
  disposeBindings();

  const tbody = el("tbody");
  const thead = el(
    "thead",
    null,
    el(
      "tr",
      null,
      el("th", null, "Time"),
      el("th", null, "Media"),
      el("th", null, "Lang"),
      el("th", null, "Provider"),
      el("th", null, "Mode"),
      el("th", null, "Release"),
    ),
  );
  const tbl = el("table", { className: "history" }, thead, tbody);
  const emptyNoData = emptyState("No downloads yet.");
  const emptyFiltered = emptyState("No downloads matching filter.");
  const showMore = el(
    "button",
    {
      type: "button",
      className: "more-btn",
      onclick: () => {
        void loadMore();
      },
    },
    "Show more\u2026",
  );
  patch(out, el("div", { className: "hist-list" }, emptyNoData, emptyFiltered, tbl, showMore));

  bindings.push(bindList(tbody, history, { mount: (entry) => buildHistoryRow(entry) }));
  bindings.push(
    effect(() => {
      void renderTick.value;
      const empty = history.ids.value.length === 0;
      const filtered = anyFilterActive();
      emptyNoData.hidden = !(empty && !filtered);
      emptyFiltered.hidden = !(empty && filtered);
      tbl.hidden = empty;
      showMore.hidden = empty || !hasMore.value;
    }),
  );
}

function showError(e: unknown): void {
  disposeBindings();
  const out = document.getElementById("historyContent");
  if (out) {
    patch(out, errDiv(e instanceof Error ? e.message : String(e)));
  }
}

// runReload is the reload internals behind both entry points: fetch page 0
// through the RAW list read (a transaction leg must observe a non-2xx — the
// null-collapsing read maps failure to an empty page and no wrapper can see
// it), apply on landing, and publish this generation's settlement. An abort
// by the dispatcher's re-route settles superseded(next: null); a genuine
// failure settles failed with prior rows intact.
async function runReload(g: number, signal?: AbortSignal): Promise<void> {
  // Design-system skeleton (same anti-flicker timing as the library table)
  // for the first mount only: previously the panel stayed BLANK for the whole
  // fetch. Filter-change reloads keep the current rows until data lands
  // (patching a skeleton over a live reactive table would drop bindings).
  const out = document.getElementById("historyContent");
  const firstMount = out !== null && out.querySelector("table.history") === null;
  const timing = firstMount
    ? skeletonTiming(
        () => {
          if (g !== gen) {
            return;
          }
          const skel = document.createDocumentFragment();
          for (let i = 0; i < 6; i++) {
            skel.appendChild(
              el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
            );
          }
          patch(out, skel);
        },
        // The library default is min-visible 0; subflux keeps its 300ms
        // never-blink window explicitly.
        { minVisibleMs: 300 },
      )
    : null;
  try {
    const res = await listStateRaw(buildQuery(0, PAGE_SIZE), signal ? { signal } : undefined);
    if (g !== gen) {
      timing?.cancel();
      return; // superseded — the superseder settled this generation at start
    }
    if (!res.ok) {
      timing?.cancel();
      if (res.status === 0 && signal?.aborted) {
        // The dispatcher's RE-ROUTE aborted this run: the new route's page
        // leg owns the continuation — no latch, nothing painted.
        settleGeneration(g, { kind: "superseded", next: null });
      } else {
        settleGeneration(g, {
          kind: "failed",
          error: new Error(res.error ?? `history load failed (${String(res.status)})`),
        });
      }
      return;
    }
    const items = res.data ?? [];
    history.setAll(items);
    hasMore.value = items.length >= PAGE_SIZE;
    updateHistoryFilters(history.items());
    const mount = (): void => {
      if (g !== gen) {
        return;
      }
      ensureMounted();
      renderTick.value += 1;
    };
    if (timing) {
      timing.commit(mount);
    } else {
      mount();
    }
    settleGeneration(g, { kind: "applied" });
  } catch (e: unknown) {
    // Settle the anti-flicker controller on the failure path too: a pending
    // (not yet painted) skeleton must never land OVER the error panel.
    timing?.cancel();
    settleGeneration(g, {
      kind: "failed",
      error: e instanceof Error ? e : new Error(String(e)),
    });
  }
}

async function loadMore(): Promise<void> {
  if (!hasMore.value) {
    return;
  }
  const g = newGeneration();
  const scrollPos = window.scrollY;
  try {
    const res = await listStateRaw(buildQuery(history.size, PAGE_SIZE));
    if (g !== gen) {
      return; // superseded (e.g. a filter-change reload won)
    }
    if (!res.ok) {
      // Keep today's visible degradation (the button hides; loaded rows
      // stay); the settlement carries the failure so a transaction chained
      // through this run rejects instead of committing on masked data.
      hasMore.value = false;
      renderTick.value += 1;
      settleGeneration(g, {
        kind: "failed",
        error: new Error(res.error ?? `history load failed (${String(res.status)})`),
      });
      return;
    }
    const items = res.data ?? [];
    for (const entry of items) {
      history.upsert(entry);
    }
    hasMore.value = items.length >= PAGE_SIZE;
    updateHistoryFilters(history.items());
    renderTick.value += 1;
    window.scrollTo(0, scrollPos);
    settleGeneration(g, { kind: "applied" });
  } catch (e: unknown) {
    settleGeneration(g, {
      kind: "failed",
      error: e instanceof Error ? e : new Error(String(e)),
    });
    if (g === gen) {
      showError(e);
    }
  }
}

/** The UI adapter (routes navigating to /history, filter changes): kick a
 *  reload and route a chain-final rejection to the error panel. */
export function reloadHistory(): void {
  const g = newGeneration();
  void runReload(g);
  chainSettlement(g).catch((e: unknown) => {
    showError(e);
  });
}

/** Task 9's REQUIRED EXTRACTION: the transaction's history page leg. Backed
 *  by the raw list read; resolves once this run's settlement chain
 *  terminates — "applied" (this run landed), "superseded" (a superseder
 *  applied), "rerouted" (a route leave re-routed the leg; the dispatcher
 *  re-dispatches) — and REJECTS when the chain ends failed or abandoned
 *  (prior rows intact, watermark untouched; the transaction aborts). */
export function reloadHistoryForTransaction(
  signal?: AbortSignal,
): Promise<"applied" | "superseded" | "rerouted"> {
  const g = newGeneration();
  void runReload(g, signal);
  return chainSettlement(g);
}

on(BusEvent.LoadHistory, () => {
  reloadHistory();
});

/** Test-only: clear the collection, the ledger, and the generation counter. */
export function _resetHistoryForTest(): void {
  history.clear();
  settlements.clear();
  gen = 0;
  hasMore.value = false;
  renderTick.value = 0;
  disposeBindings();
}
