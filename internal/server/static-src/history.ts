// history.ts — download history page (server-side paginated).
//
// The accumulated entries live in a createCollection keyed by the server's
// unique row id; bindList renders them. "Reload" (filter change)
// is setAll(page 0); "show more" fetches the next server page and upserts it
// (appended). The collection IS the bindList ListSource — no per-row updates
// (history is append-only display), so only the structure tier does work.

import { el, input, select, option, errDiv } from "./dom.js";
import { listState } from "./wire/client.gen.js";
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

/** Fetch a page of history entries via the generated typed client. Failures
 *  collapse to an empty page (the empty-state IS the error UI). */
async function fetchPage(
  offset: number,
  limit: number,
): Promise<{ items: StateEntry[]; hasMore: boolean }> {
  const items = (await listState(buildQuery(offset, limit))) ?? [];
  return { items, hasMore: items.length >= limit };
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

async function reload(): Promise<void> {
  const g = ++gen;
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
    const page = await fetchPage(0, PAGE_SIZE);
    if (g !== gen) {
      return; // superseded by a newer reload/loadMore
    }
    history.setAll(page.items);
    hasMore.value = page.hasMore;
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
  } catch (e: unknown) {
    // Settle the anti-flicker controller on the failure path too: a pending
    // (not yet painted) skeleton must never land OVER the error panel.
    timing?.cancel();
    if (g === gen) {
      showError(e);
    }
  }
}

async function loadMore(): Promise<void> {
  if (!hasMore.value) {
    return;
  }
  const g = ++gen;
  const scrollPos = window.scrollY;
  try {
    const page = await fetchPage(history.size, PAGE_SIZE);
    if (g !== gen) {
      return; // superseded (e.g. a filter-change reload won)
    }
    for (const entry of page.items) {
      history.upsert(entry);
    }
    hasMore.value = page.hasMore;
    updateHistoryFilters(history.items());
    renderTick.value += 1;
    window.scrollTo(0, scrollPos);
  } catch (e: unknown) {
    if (g === gen) {
      showError(e);
    }
  }
}

export function reloadHistory(): void {
  void reload();
}

on(BusEvent.LoadHistory, () => {
  void reload();
});
