// history.ts — download history page (server-side paginated).
//
// The accumulated entries live in a createCollection keyed by the server's
// unique row id; bindList renders them. "Reload" (filter change)
// is setAll(page 0); "show more" fetches the next server page and upserts it
// (appended). The collection IS the bindList ListSource — no per-row updates
// (history is append-only display), so only the structure tier does work.

import { el, input, select, option, errDiv } from "./dom.js";
import { apiAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import { on, emit, BusEvent } from "./bus.js";
import { fmtDateTime, fmtEpisode, clickableRow, emptyState } from "./utils.js";
import { signal, effect, createCollection, bindList, patch } from "@cplieger/reactive";

interface HistoryEntry {
  id: number; // unique subtitle_state row id (stable collection key)
  media_id: string;
  media_type: string;
  language: string;
  provider: string;
  release_name: string;
  title: string;
  season: number;
  episode: number;
  manual: boolean;
  media_imported: string;
}

const PAGE_SIZE = 50;

// Key on the server's unique row id. media_id+media_imported is NOT unique
// (one row per language + a new row per manual download, and media_imported is
// a second-precision timestamp), so it collided — the collection dropped rows
// and reconcile mounted duplicates.
const historyKey = (e: HistoryEntry): string => String(e.id);
const history = createCollection<HistoryEntry>(historyKey);

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

function buildApiUrl(offset: number, limit: number): string {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  if (offset > 0) {
    params.set("offset", String(offset));
  }
  const type = select("h-type").value;
  const lang = select("h-lang").value;
  const prov = select("h-provider").value;
  const search = input("h-filter").value.trim();
  if (type) {
    params.set("type", type);
  }
  if (lang) {
    params.set("lang", lang);
  }
  if (prov) {
    params.set("provider", prov);
  }
  if (search) {
    params.set("search", search);
  }
  return `/api/state?${params.toString()}`;
}

/** Fetch a page of history entries. Pure GET; failures show as empty
 *  page (caller renders empty-state) but retryNetwork handles transient
 *  blips so a single network hiccup doesn't leave the user staring at
 *  "no downloads". error: false because the empty-state IS the error UI. */
const loadHistoryAction = apiAction<string, HistoryEntry[]>({
  name: "history.load",
  request: (path) => ({ method: "GET", path }),
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: false,
});

async function fetchPage(
  offset: number,
  limit: number,
): Promise<{ items: HistoryEntry[]; hasMore: boolean }> {
  const items = (await loadHistoryAction.dispatch(buildApiUrl(offset, limit))) ?? [];
  return { items, hasMore: items.length >= limit };
}

function historyMediaHref(entry: HistoryEntry): string {
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

function buildHistoryRow(entry: HistoryEntry): HTMLElement {
  const time = fmtDateTime(new Date(entry.media_imported));
  let label = entry.title || "";
  if (entry.season > 0 || entry.episode > 0) {
    label += ` \u00B7 ${fmtEpisode(entry.season, entry.episode)}`;
  }
  const href = historyMediaHref(entry);
  const cells = [
    el("td", { "data-col": "meta" }, time),
    el("td", { "data-col": "title" }, label),
    el("td", { "data-col": "meta" }, entry.language),
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

/** Populate language and provider dropdowns from accumulated data. */
function updateHistoryFilters(data: HistoryEntry[]): void {
  const langs = new Set<string>();
  const provs = new Set<string>();
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
  try {
    const page = await fetchPage(0, PAGE_SIZE);
    if (g !== gen) {
      return; // superseded by a newer reload/loadMore
    }
    history.setAll(page.items);
    hasMore.value = page.hasMore;
    updateHistoryFilters(history.items());
    ensureMounted();
    renderTick.value += 1;
  } catch (e: unknown) {
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
