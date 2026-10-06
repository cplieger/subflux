import { el, option, errDiv } from "./dom.js";
import { historyPanel } from "./panels.js";
import { listStateRaw } from "./wire/client.gen.js";
import type { QueryValue } from "./wire/client.gen.js";
import type { StateEntry } from "./wire/types.gen.js";
import { on, emit, BusEvent } from "./bus.js";
import { fmtDateTime, fmtEpisode, clickableRow, emptyState } from "./utils.js";
import {
  signal,
  effect,
  createCollection,
  bindList,
  patch,
  batch,
  reconcile,
  type ReconcileSpec,
} from "@cplieger/reactive";
import { historyView } from "./view-scope.js";
import { skeletonTiming } from "@cplieger/ui-primitives/skeleton";
import { HISTORY_DEPTH_CAP, SUMMARY_COALESCE_MS } from "./constants.js";
import { openTransaction } from "./transaction.js";
import * as store from "./store.js";

const PAGE_SIZE = 50;

// Key on the server's unique row id: media_id+media_imported collided (one
// row per language + a new row per manual download; media_imported is
// second-precision), dropping rows in the collection.
const historyKey = (e: StateEntry): string => String(e.id);
const history = createCollection<StateEntry>(historyKey);

// One of the two facts behind "Show more"; the other is loaded < HISTORY_DEPTH_CAP.
const hasMore = signal(false);

/** What the last completed read said about the history as a whole. Both facts land
 *  in ONE batch off ONE response, so no pair of them can describe two reads. */
interface HistoryRead {
  readonly filtered: boolean; // the query that produced these rows carried a filter
  readonly total: number; // rows the store holds with NO filter applied
}
const lastRead = signal<HistoryRead>({ filtered: false, total: 0 });

// --- THE SERIALIZER (E4) ---
//
// One serializer owns every fetch into the collection: user gestures
// (loadMore), event reloads, filter/navigation reloads, and the
// transaction's history leg. FOREGROUND PRIORITY: a gesture is never
// cancelled by an event reload — the event latches ONE pending reload that
// dispatches at the NEW depth once the gesture's slot closes. A filter
// change supersedes everything.

// Landings compare against liveGen — a queued (latched) generation takes
// an id without taking the slot, so it never discards the run it waits behind.
let genCounter = 0;
let liveGen = 0;

// Invariant: pendingReload !== null implies a run is in flight (the latch
// drains when the slot closes, and a route leave drops it).
//
// A reload carries the transaction it was dispatched under and the depth it
// asked for, so a leg can tell its own transaction's read from an older
// one's. A gesture carries neither: a page-N append can never answer for
// the newest window.
type Run =
  | { readonly kind: "reload"; readonly limit: number; readonly txn: number | null }
  | { readonly kind: "gesture" };
let running: (Run & { readonly gen: number }) | null = null;
let pendingReload: number | null = null;
let triggerTimer: ReturnType<typeof setTimeout> | null = null;

// --- THE SETTLEMENT MODEL ---
//
// Every generation settles exactly once as applied | failed |
// superseded(next) | abandoned. A superseder settles its priors at START
// (g !== liveGen proves it started, not that it applied), so a waiter
// resolves "superseded" only by chaining superseded(next) links to a
// generation that settles applied. `next: null` marks a re-route (route
// leave during a transaction): the continuation is the new route's page
// leg, never a latch. `abandoned` is a defensive terminal with no shipped
// producer — any settlement path not otherwise named rejects loudly.

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
// Settled records past the cap are dropped oldest-first (an evicted
// generation can no longer be awaited — chains are short-lived).
const SETTLEMENT_CAP = 64;

/** Allocate a generation with an open settlement record, evicting settled
 *  records past the ledger cap. */
function allocGeneration(): number {
  genCounter += 1;
  const g = genCounter;
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

/** Settle every prior unsettled generation superseded(next). `except`
 *  shields the pending latch from a gesture's supersession. */
function supersedePriors(next: number, except?: number | null): void {
  for (const [pg, rec] of settlements) {
    if (pg !== next && pg !== except && rec.settled === null) {
      settleGeneration(pg, { kind: "superseded", next });
    }
  }
}

/** Publish a generation's settlement exactly once. */
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
      // Evicted: the chain outlived the ledger bound. Treat as landed.
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

// --- Run lifecycle: the slot, the latch, the trigger ---

/** Take the slot for a reload of the newest `limit` rows, stamping the
 *  transaction open at dispatch. */
function beginReload(g: number, limit: number): void {
  running = { gen: g, kind: "reload", limit, txn: openTransaction() };
  liveGen = g;
}

/** Take the slot for a "Show more" append. */
function beginGesture(g: number): void {
  running = { gen: g, kind: "gesture" };
  liveGen = g;
}

/** Close a run's slot — only the owner clears it — then drain the latch. */
function endRun(g: number): void {
  if (running?.gen !== g) {
    return;
  }
  running = null;
  drainPending();
}

/** The depth an event/leg reload preserves: loaded row count, floored at
 *  one page and capped at HISTORY_DEPTH_CAP (the server clamps a larger
 *  ?limit silently). */
function eventDepth(): number {
  return Math.min(Math.max(history.size, PAGE_SIZE), HISTORY_DEPTH_CAP);
}

/** Latch the ONE pending reload (idempotent). */
function ensurePendingLatch(): number {
  pendingReload ??= allocGeneration();
  return pendingReload;
}

/** Drop the pending latch and armed trigger window on a route leave. */
function dropPendingLatch(): void {
  if (triggerTimer !== null) {
    clearTimeout(triggerTimer);
    triggerTimer = null;
  }
  if (pendingReload !== null) {
    settleGeneration(pendingReload, { kind: "superseded", next: null });
    pendingReload = null;
  }
}

function drainPending(): void {
  if (pendingReload === null) {
    return;
  }
  if (store.get("currentPage") !== "history") {
    dropPendingLatch();
    return;
  }
  const g = allocGeneration();
  supersedePriors(g); // the latch settles superseded(next: g)
  pendingReload = null;
  void runReload(g, eventDepth());
}

/** Coverage/activity events (via events.ts, outside the heal gate): while
 *  the history page is open, notes coalesce in one trailing window into a
 *  single depth-preserving reload, latched behind any in-flight run. */
export function noteHistoryMutation(): void {
  if (store.get("currentPage") !== "history") {
    return;
  }
  triggerTimer ??= setTimeout(() => {
    triggerTimer = null;
    requestEventReload();
  }, SUMMARY_COALESCE_MS);
}

function requestEventReload(): void {
  if (store.get("currentPage") !== "history") {
    return; // left mid-window
  }
  if (running !== null) {
    ensurePendingLatch();
    return;
  }
  const g = allocGeneration();
  supersedePriors(g);
  void runReload(g, eventDepth());
}

/** app.ts wires this to coverage-heal's onHealReset: a full-pair overwrite
 *  just aborted in-flight heals — churn an in-flight reload's read may
 *  predate — so the trailing latch re-arms behind that run. */
export function reArmHistoryLatch(): void {
  if (store.get("currentPage") !== "history" || running === null) {
    return;
  }
  ensurePendingLatch();
}

// A route leave drops the pending latch: the dispatcher's re-route arm
// owns the continuation.
store.subscribe("currentPage", (page) => {
  if (page !== "history") {
    dropPendingLatch();
  }
});

/** The history filter controls, read once. Both readers fold `filterParams`,
 *  which is keyed by `keyof HistoryFilters`, so a fifth control is one new
 *  field the compiler will not let either of them forget. Enumerating the
 *  fields instead costs more than a missing query param: a control
 *  `anyFilterActive` does not know about makes `runReload` record a FILTERED
 *  read as the whole dataset. */
interface HistoryFilters {
  readonly type: string;
  readonly lang: string;
  readonly provider: string;
  readonly search: string;
}

function readFilters(): HistoryFilters {
  const p = historyPanel();
  return {
    type: p.type.value,
    lang: p.lang.value,
    provider: p.provider.value,
    search: p.filter.value.trim(),
  };
}

const EMPTY_FILTERS: HistoryFilters = { type: "", lang: "", provider: "", search: "" };

/** The query param each filter field sends, absent while the control is at its
 *  DEFAULT — compared against `EMPTY_FILTERS` rather than tested for emptiness,
 *  so a control whose default is not `""` cannot read as active at rest, and a
 *  control the server reads as a filter cannot be dropped from the query while
 *  `anyFilterActive` still reports "unfiltered". The `keyof HistoryFilters`
 *  return type is load-bearing twice over: a new field fails the typecheck until
 *  it is named here, and "a filter is active" cannot disagree with "the query
 *  carried a filter", because both answers fold this one record.
 *
 *  Unguarded, knowingly: every control this page ships defaults to `""`, so no
 *  test can tell this comparison from `filters[k] || undefined`, and a revert to
 *  that form would go green. The type forces a fifth control to be NAMED here;
 *  nothing forces it to be compared against its own default. */
function filterParams(filters: HistoryFilters): Record<keyof HistoryFilters, QueryValue> {
  const at = (k: keyof HistoryFilters): QueryValue =>
    filters[k] === EMPTY_FILTERS[k] ? undefined : filters[k];
  return {
    type: at("type"),
    lang: at("lang"),
    provider: at("provider"),
    search: at("search"),
  };
}

/** Query for a history page: limit always, offset only past page 0, filter
 *  params only when non-empty. `filters` is never defaulted: every dispatcher
 *  snapshots the controls before its first await, so "nothing reads the controls
 *  except at dispatch" is checkable by reading this signature. */
function buildQuery(
  offset: number,
  limit: number,
  filters: HistoryFilters,
): Record<string, QueryValue> {
  return {
    limit,
    offset: offset > 0 ? offset : undefined,
    ...filterParams(filters),
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

/** The row's six cells in column order, each the CSS column role plus the text
 *  the entry yields. The shell and the repaint both fold this one list, so a
 *  seventh column cannot reach the markup and miss the repaint. */
function historyCells(entry: StateEntry): readonly { col: string; text: string }[] {
  let label = entry.title || "";
  const season = entry.season ?? 0;
  const episode = entry.episode ?? 0;
  if (season > 0 || episode > 0) {
    label += ` \u00B7 ${fmtEpisode(season, episode)}`;
  }
  // Non-standard variants qualify the language cell; standard stays bare.
  const lang = entry.variant !== "standard" ? `${entry.language} ${entry.variant}` : entry.language;
  return [
    { col: "meta", text: fmtDateTime(new Date(entry.media_imported)) },
    { col: "title", text: label },
    { col: "meta", text: lang },
    { col: "meta", text: entry.provider },
    { col: "meta", text: entry.manual ? "manual" : "auto" },
    { col: "meta", text: entry.release_name || "" },
  ];
}

/** The row shell, with EMPTY cells: paintHistoryRow is the only writer, so a
 *  binding that drops it renders blanks rather than values silently frozen at
 *  their first render. */
function buildHistoryRow(entry: StateEntry): HTMLElement {
  const cells = historyCells(entry).map((c) => el("td", { "data-col": c.col }));
  const href = historyMediaHref(entry);
  return href
    ? clickableRow(
        () => {
          emit(BusEvent.NavRoute, href);
        },
        ...cells,
      )
    : el("tr", null, ...cells);
}

function paintHistoryRow(row: HTMLElement, entry: StateEntry): void {
  const cells = historyCells(entry);
  for (const [i, cell] of [...row.children].entries()) {
    cell.textContent = cells[i]?.text ?? "";
  }
}

/** Populate language and provider dropdowns from the stable sources
 *  (configured languages, provider map) merged with values seen in loaded
 *  rows — covers history from since-removed providers or languages. */
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

  const hLang = historyPanel().lang;
  {
    const current = hLang.value;
    hLang.replaceChildren(option("", "All languages"));
    for (const l of [...langs].sort()) {
      hLang.appendChild(option(l, l));
    }
    hLang.value = current;
  }

  const hProv = historyPanel().provider;
  {
    const current = hProv.value;
    hProv.replaceChildren(option("", "All providers"));
    for (const p of [...provs].sort()) {
      hProv.appendChild(option(p, p));
    }
    hProv.value = current;
  }
}

function anyFilterActive(filters: HistoryFilters): boolean {
  return Object.values(filterParams(filters)).some((v) => v !== undefined);
}

// --- Render: build the table shell once, bind the tbody, react for the rest ---

/** Which of the three shapes the panel is in. Loading and error are the HOST's
 *  occupant rather than a shape of the panel, so neither is in here. */
type HistoryShape = "rows" | "filtered" | "no-data";

/** An empty history beats an active filter: you have nothing at all, whatever
 *  you asked for. */
function shapeOf(loaded: number, read: HistoryRead): HistoryShape {
  if (loaded > 0) {
    return "rows";
  }
  return read.filtered && read.total > 0 ? "filtered" : "no-data";
}

type Slot = "empty:no-data" | "empty:filtered" | "table" | "more";

/** Exhaustive over HistoryShape, so a fourth shape fails the typecheck here
 *  rather than rendering nothing. At most one `empty:*` key, so two messages at
 *  once — or a message above a populated table — is unrepresentable. */
function slotsOf(shape: HistoryShape, canLoadMore: boolean): readonly Slot[] {
  switch (shape) {
    case "rows":
      return canLoadMore ? ["table", "more"] : ["table"];
    case "filtered":
      return ["empty:filtered"];
    case "no-data":
      return ["empty:no-data"];
  }
}

/** The history table's view id in the history-panel host. */
const VIEW_HISTORY = "history";

function ensureMounted(): void {
  const out = historyPanel().content;
  // Already the host's occupant — the live binding renders the table.
  if (historyView.scopeFor(VIEW_HISTORY) !== null) {
    return;
  }
  const scope = historyView.mount(VIEW_HISTORY);

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
  // EMPTY: every child of this container is reconcile's, because an unkeyed one
  // would sit there forever — reconcile tracks only the keyed ones.
  const list = el("div", { className: "hist-list" });
  // NOT patch: it may copy a fresh node's content into a recycled child and
  // never insert the node itself, so the references the effect below closes
  // over would be writing to detached nodes. The skeleton this container holds
  // at commit is exactly that case.
  out.replaceChildren(list);

  // update, not mount alone: a re-downloaded auto row keeps its subtitle_state
  // id while its provider and release_name move, so the id sequence is
  // unchanged and no reconcile runs — the content tier is what repaints it.
  scope.add(
    bindList(tbody, history, {
      mount: (entry) => buildHistoryRow(entry),
      update: (row, entry) => {
        paintHistoryRow(row, entry);
      },
    }),
  );

  const slots: ReconcileSpec<Slot> = {
    key: (s) => s,
    // tbl and showMore are per-MOUNT singletons, so the row binding outlives
    // every absence: reconcile detaches the table when the shape leaves "rows"
    // and re-inserts THIS node, with its bindings and its rows, when it returns.
    mount: (s) =>
      s === "table"
        ? tbl
        : s === "more"
          ? showMore
          : emptyState(
              s === "empty:no-data" ? "No downloads yet." : "No downloads matching filter.",
            ),
  };

  scope.add(
    effect(() => {
      // Read every signal UNCONDITIONALLY before any branch: slotsOf consults
      // `more` only in the rows arm, and a read inside a branch leaves this
      // effect unsubscribed from the signal that would flip it.
      const loaded = history.ids.value.length;
      const read = lastRead.value;
      const more = hasMore.value;
      // Below the cap as well as hasMore: past it the server's silent LIMIT
      // clamp would truncate a depth-preserving reload undetectably.
      reconcile(list, slotsOf(shapeOf(loaded, read), more && loaded < HISTORY_DEPTH_CAP), slots);
    }),
  );
}

function showError(e: unknown): void {
  historyView.clear();
  patch(historyPanel().content, errDiv(e instanceof Error ? e.message : String(e)));
}

// runReload takes the slot, fetches the newest window through the raw list
// read (a transaction leg must observe a non-2xx), applies it as a keyed
// setAll, and publishes the settlement. An abort by a re-route settles
// superseded(next: null); a genuine failure settles failed with prior rows
// intact. Filters and depth are captured at dispatch, before the first await.
async function runReload(g: number, limit: number, signal?: AbortSignal): Promise<void> {
  beginReload(g, limit);
  // Snapshotted before the first await so a control changed mid-flight cannot
  // be read back as the one this query was built from.
  const filters = readFilters();
  // Anti-flicker skeleton for the first mount only: filter-change reloads
  // keep current rows until data lands (a skeleton over a live reactive
  // table would drop bindings).
  const out = historyPanel().content;
  const firstMount = historyView.scopeFor(VIEW_HISTORY) === null;
  const timing = firstMount
    ? skeletonTiming(
        () => {
          if (g !== liveGen) {
            return;
          }
          historyView.clear();
          const skel = document.createDocumentFragment();
          for (let i = 0; i < 6; i++) {
            skel.appendChild(
              el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
            );
          }
          patch(out, skel);
        },
        // The library default is min-visible 0; subflux keeps 300ms.
        { minVisibleMs: 300 },
      )
    : null;
  try {
    const res = await listStateRaw(buildQuery(0, limit, filters), signal ? { signal } : undefined);
    if (g !== liveGen) {
      timing?.cancel();
      return; // superseded — the superseder settled this generation at start
    }
    if (!res.ok) {
      timing?.cancel();
      if (res.status === 0 && signal?.aborted) {
        // A re-route aborted this run: the new route's page leg owns the
        // continuation.
        settleGeneration(g, { kind: "superseded", next: null });
      } else {
        settleGeneration(g, {
          kind: "failed",
          error: new Error(res.error ?? `history load failed (${String(res.status)})`),
        });
      }
      return;
    }
    const page = res.data ?? { entries: [], unfiltered_total: 0 };
    // One batch, so the render that setAll triggers cannot read a fact left
    // over from the dataset the PREVIOUS query described. `filters` is the
    // dispatch snapshot, so an empty page is judged against the filters its OWN
    // query carried.
    batch(() => {
      history.setAll(page.entries);
      hasMore.value = page.entries.length >= limit;
      lastRead.value = { filtered: anyFilterActive(filters), total: page.unfiltered_total };
    });
    updateHistoryFilters(history.items());
    const mount = (): void => {
      if (g !== liveGen) {
        return;
      }
      ensureMounted();
    };
    if (timing) {
      timing.commit(mount);
    } else {
      mount();
    }
    settleGeneration(g, { kind: "applied" });
  } catch (e: unknown) {
    // A pending (not yet shown) skeleton must never land over the error panel.
    timing?.cancel();
    settleGeneration(g, {
      kind: "failed",
      error: e instanceof Error ? e : new Error(String(e)),
    });
  } finally {
    endRun(g);
  }
}

async function loadMore(): Promise<void> {
  // FOREGROUND PRIORITY: the gesture takes the slot now. An in-flight
  // event/leg reload's fetch is superseded, but it re-latches at the new
  // depth after the append commits.
  if (running !== null && running.kind === "reload") {
    ensurePendingLatch();
  }
  const g = allocGeneration();
  supersedePriors(g, pendingReload);
  beginGesture(g);
  // Snapshotted before the first await, like runReload's.
  const filters = readFilters();
  const scrollPos = window.scrollY;
  try {
    const res = await listStateRaw(buildQuery(history.size, PAGE_SIZE, filters));
    if (g !== liveGen) {
      return; // superseded (e.g. a filter-change reload won)
    }
    if (!res.ok) {
      // Keep today's visible degradation; the settlement carries the
      // failure so a chained transaction rejects rather than committing on
      // masked data.
      hasMore.value = false;
      settleGeneration(g, {
        kind: "failed",
        error: new Error(res.error ?? `history load failed (${String(res.status)})`),
      });
      return;
    }
    const page = res.data ?? { entries: [], unfiltered_total: 0 };
    // NO lastRead write: this runs only while hasMore, which is only true after
    // a page came back at >= limit, so `loaded > 0` both on entry and after —
    // and shapeOf answers "rows" for every loaded > 0 whatever lastRead holds.
    //
    // One structural reconcile per appended page: each upsert writes the
    // collection's order signal, so an unbatched loop costs a full pass per row.
    batch(() => {
      for (const entry of page.entries) {
        history.upsert(entry);
      }
      hasMore.value = page.entries.length >= PAGE_SIZE;
      updateHistoryFilters(history.items());
    });
    window.scrollTo(0, scrollPos);
    settleGeneration(g, { kind: "applied" });
  } catch (e: unknown) {
    settleGeneration(g, {
      kind: "failed",
      error: e instanceof Error ? e : new Error(String(e)),
    });
    if (g === liveGen) {
      showError(e);
    }
  } finally {
    endRun(g);
  }
}

/** UI adapter (routes navigating to /history, filter changes): a page-0
 *  reload that supersedes everything, routing a chain-final rejection to
 *  the error panel. */
export function reloadHistory(): void {
  if (triggerTimer !== null) {
    clearTimeout(triggerTimer);
    triggerTimer = null;
  }
  pendingReload = null; // its generation settles superseded(next: g) below
  const g = allocGeneration();
  supersedePriors(g);
  void runReload(g, PAGE_SIZE);
  chainSettlement(g).catch((e: unknown) => {
    showError(e);
  });
}

/** The transaction's history page leg. Joins a reload this same
 *  transaction already has in flight (the /history boot case); otherwise
 *  latches like any event reload — behind an in-flight gesture too, so
 *  commit waits for the depth-preserving reload that runs after the
 *  append commits. Resolves "applied" | "superseded" | "rerouted";
 *  rejects when the chain ends failed or abandoned. */
export function reloadHistoryForTransaction(
  signal?: AbortSignal,
): Promise<"applied" | "superseded" | "rerouted"> {
  // A latch already queued is strictly fresher than anything in flight, so
  // it wins over a join.
  const joinable = pendingReload === null ? joinableRun() : null;
  if (joinable !== null) {
    return joinRun(joinable, signal);
  }
  if (running !== null || pendingReload !== null) {
    return chainSettlement(ensurePendingLatch());
  }
  const g = allocGeneration();
  supersedePriors(g);
  void runReload(g, eventDepth(), signal);
  return chainSettlement(g);
}

/** The in-flight run's generation when it already answers a transaction
 *  leg's question, else null: a transaction must be open; the run must be
 *  a reload of the newest window this same transaction dispatched (an
 *  older transaction's run predates the current one's mutations); and its
 *  window must be at least as deep as this leg would ask for. */
function joinableRun(): number | null {
  const txn = openTransaction();
  if (txn === null || running?.kind !== "reload") {
    return null;
  }
  return running.txn === txn && running.limit >= eventDepth() ? running.gen : null;
}

/** Await a run the leg joined rather than dispatched. The run belongs to
 *  the route loader, so a route leave is reported as the re-route the
 *  dispatcher re-dispatches from. */
async function joinRun(
  g: number,
  signal?: AbortSignal,
): Promise<"applied" | "superseded" | "rerouted"> {
  const r = await chainSettlement(g);
  return signal?.aborted ? "rerouted" : r;
}

on(BusEvent.LoadHistory, () => {
  reloadHistory();
});

/** Test-only: clear the collection, the ledger, the serializer state, and
 *  the generation counters. */
export function _resetHistoryForTest(): void {
  history.clear();
  settlements.clear();
  genCounter = 0;
  liveGen = 0;
  running = null;
  pendingReload = null;
  if (triggerTimer !== null) {
    clearTimeout(triggerTimer);
    triggerTimer = null;
  }
  hasMore.value = false;
  lastRead.value = { filtered: false, total: 0 };
  historyView.clear();
}
