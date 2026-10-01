import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// One controllable mock backs the generated listState function history.ts
// fetches pages through. Per-call page returns are queued with
// dispatch.mockResolvedValueOnce(...). Hoisted so the vi.mock factory below
// can close over the same reference history.ts captures at import time.
const { dispatch, emit } = vi.hoisted(() => ({ dispatch: vi.fn(), emit: vi.fn() }));

vi.mock("./wire/client.gen.js", () => ({
  // THREE arms, and the abort one is load-bearing: runReload's re-route path is
  // `res.status === 0 && signal?.aborted`, which two cases reach through a real
  // controller. A queued page resolves ok, `null` resolves a non-2xx envelope,
  // and a rejection propagates to the catch.
  listStateRaw: async (query?: unknown, opts?: { signal?: AbortSignal }) => {
    const v = (await dispatch(query, opts)) as unknown;
    if (opts?.signal?.aborted) {
      return { ok: false, status: 0, error: "aborted" };
    }
    if (v === null) {
      return { ok: false, status: 502, error: "history load failed" };
    }
    return { ok: true, status: 200, data: v };
  },
}));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {
    /* noop */
  }),
  emit,
  BusEvent: { LoadHistory: "load:history", NavRoute: "nav:route" },
}));
// The real cap is 10 000; the suites below exercise the cap BEHAVIOR without
// rendering ten thousand rows. Everything else keeps its real value (the
// trigger window in particular).
// Type-only: erased at runtime, so the hoisted vi.mock factory may reference it.
import type * as ConstantsModule from "./constants.js";
vi.mock("./constants.js", async (importOriginal) => ({
  ...(await importOriginal<typeof ConstantsModule>()),
  HISTORY_DEPTH_CAP: 250,
}));

// Structural-reconcile spy: bindList's structure tier reads `source.ids.value`
// exactly once per pass, so wrapping the ListSource counts reconcile passes
// without changing rendering (the getter delegates to the real signal, so
// effect tracking is untouched).
const structural = vi.hoisted(() => ({ runs: 0 }));
import type * as ReactiveModule from "@cplieger/reactive";
vi.mock("@cplieger/reactive", async (importOriginal) => {
  const real = await importOriginal<typeof ReactiveModule>();
  const bindList = <T>(
    parent: ParentNode,
    source: ReactiveModule.ListSource<T>,
    spec: ReactiveModule.ListSpec<T>,
  ): (() => void) =>
    real.bindList<T>(
      parent,
      {
        ids: {
          get value(): readonly string[] {
            structural.runs += 1;
            return source.ids.value;
          },
          peek: (): readonly string[] => source.ids.peek(),
        },
        signalFor: (id: string) => source.signalFor(id),
      },
      spec,
    );
  return { ...real, bindList };
});

import * as store from "./store.js";
import {
  reloadHistory,
  reloadHistoryForTransaction,
  noteHistoryMutation,
  reArmHistoryLatch,
  _resetHistoryForTest,
} from "./history.js";
import { SUMMARY_COALESCE_MS } from "./constants.js";
import { beginTransaction, settleTransaction } from "./transaction.js";
import type { ParsedConfig } from "./wire/types.gen.js";
import { _resetPanelsForTest, historyPanel } from "./panels.js";

// Mirrors the wire StateEntry fields buildHistoryRow reads (only those matter
// for the assertions here).
interface Entry {
  id: number;
  media_id: string;
  media_type: string;
  language: string;
  variant: string;
  provider: string;
  release_name: string;
  title: string;
  season: number;
  episode: number;
  manual: boolean;
  media_imported: string;
}

function makeEntry(id: number): Entry {
  return {
    id,
    media_id: `tmdb-${id}`,
    media_type: "movie",
    language: "en",
    variant: "standard",
    provider: "opensubtitles",
    release_name: `Release ${id}`,
    title: `Title ${id}`,
    season: 0,
    episode: 0,
    manual: false,
    media_imported: "2024-01-01T00:00:00Z",
  };
}

/** One StatePage response. Most cases are about the ROWS, so the total defaults
 *  to the page's own length; the cases that are about the total pass it. */
interface Page {
  entries: Entry[];
  unfiltered_total: number;
}
const page = (rows: Entry[], total = rows.length): Page => ({
  entries: rows,
  unfiltered_total: total,
});

// reload()/loadMore() await a resolved dispatch promise, so one macrotask turn
// guarantees the whole async chain (fetchPage -> setAll/upsert -> render) has
// settled.
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

function reqTbody(): HTMLTableSectionElement {
  const tbody = document.querySelector<HTMLTableSectionElement>("table.history tbody");
  if (!tbody) {
    throw new Error("history tbody not mounted");
  }
  return tbody;
}

function clickShowMore(): void {
  const btn = document.querySelector<HTMLButtonElement>(".more-btn");
  if (!btn) {
    throw new Error("show-more button not mounted");
  }
  btn.click();
}

// The shipped panel, so h-type carries its real options — a <select> silently
// refuses a value it has no option for.
function mountShell(): void {
  _resetPanelsForTest();
  document.body.replaceChildren(historyPanel().root);
}

function sel(id: string): HTMLSelectElement {
  const e = document.getElementById(id);
  if (!(e instanceof HTMLSelectElement)) {
    throw new Error(`#${id} is not a select`);
  }
  return e;
}

function filterInput(): HTMLInputElement {
  const e = document.getElementById("h-filter");
  if (!(e instanceof HTMLInputElement)) {
    throw new Error("#h-filter is not an input");
  }
  return e;
}

/** The query object of the most recent listState call. */
function lastQuery(): Record<string, unknown> {
  const call = dispatch.mock.calls.at(-1);
  if (!call) {
    throw new Error("listState was never called");
  }
  return call[0] as Record<string, unknown>;
}

function firstRow(): HTMLTableRowElement {
  const row = reqTbody().children.item(0);
  if (!(row instanceof HTMLTableRowElement)) {
    throw new Error("no history row mounted");
  }
  return row;
}

function cellText(): (string | null)[] {
  return [...firstRow().children].map((c) => c.textContent);
}

function optionValues(id: string): (string | null)[] {
  return [...sel(id).options].map((o) => o.value);
}

/** updateHistoryFilters reads only languages + providers off the store config;
 *  one localized cast keeps the fixture to the fields under test. */
function configOf(languages: string[], providers: Record<string, boolean>): ParsedConfig {
  return { languages, providers } as unknown as ParsedConfig;
}

const PAGE = 50;

function fullPage(): Entry[] {
  return Array.from({ length: PAGE }, (_, i) => makeEntry(i + 1));
}

describe("history: renderItems", () => {
  beforeEach(() => {
    mountShell();
  });

  it("rows are keyed by unique subtitle_state id", async () => {
    // Two distinct rows that collide on media_id + language + media_imported.
    // The old key (media_id + media_imported) deduped them into ONE collection
    // entry (yielding a single row); keying on the unique subtitle_state id
    // keeps both.
    const shared = makeEntry(1);
    const a: Entry = { ...shared, id: 1 };
    const b: Entry = { ...shared, id: 2 };
    dispatch.mockResolvedValueOnce(page([a, b]));

    reloadHistory();
    await tick();

    expect(reqTbody().children.length).toBe(2);
  });

  it("reconcile preserves existing rows on Show More append", async () => {
    // Page 0 must be a full page (>= PAGE_SIZE = 50) for hasMore to flip true
    // and enable "Show more"; loadMore then appends the next (short) page.
    // The 2->4 counts in the original plan are not reachable through the public
    // loadMore path (its hasMore gate requires a full first page), so this
    // exercises the same reconcile-preservation invariant with 50 -> 52.
    const rows0 = Array.from({ length: 50 }, (_, i) => makeEntry(i + 1));
    const rows1 = [makeEntry(51), makeEntry(52)];
    dispatch.mockResolvedValueOnce(page(rows0)).mockResolvedValueOnce(page(rows1));

    reloadHistory();
    await tick();
    const tbody = reqTbody();
    expect(tbody.children.length).toBe(50);
    const firstBefore = tbody.children.item(0);
    const secondBefore = tbody.children.item(1);

    clickShowMore();
    await tick();

    expect(tbody.children.length).toBe(52);
    // Keyed reconcile reuses the already-mounted nodes rather than rebuilding.
    expect(tbody.children.item(0)).toBe(firstBefore);
    expect(tbody.children.item(1)).toBe(secondBefore);
  });

  it("Show More appends the page in ONE structural reconcile (R8.3)", async () => {
    const rows0 = Array.from({ length: 50 }, (_, i) => makeEntry(i + 1));
    const rows1 = Array.from({ length: 50 }, (_, i) => makeEntry(100 + i));
    dispatch.mockResolvedValueOnce(page(rows0)).mockResolvedValueOnce(page(rows1));
    reloadHistory();
    await tick();
    expect(reqTbody().children.length).toBe(50);

    const before = structural.runs;
    clickShowMore();
    await tick();

    expect(reqTbody().children.length).toBe(100);
    // Unbatched, every appended row's order write ran its own reconcile
    // pass — 50 passes for this page.
    expect(structural.runs - before).toBe(1);
  });

  it("loadMore re-serving a page-0 id stays one node", async () => {
    // Overlap probe: loadMore returns an id already shown on page 0. upsert is
    // idempotent — the existing node is reused (=== stable), not duplicated, so
    // the row count grows only by the genuinely-new id.
    const rows0 = Array.from({ length: 50 }, (_, i) => makeEntry(i + 1));
    const rows1 = [makeEntry(1), makeEntry(51)]; // id 1 overlaps page 0
    dispatch.mockResolvedValueOnce(page(rows0)).mockResolvedValueOnce(page(rows1));

    reloadHistory();
    await tick();
    const tbody = reqTbody();
    expect(tbody.children.length).toBe(50);
    const idOneNode = tbody.children.item(0);

    clickShowMore();
    await tick();

    expect(tbody.children.length).toBe(51); // 52 would mean id 1 was duplicated
    expect(tbody.children.item(0)).toBe(idOneNode);
  });
});

// The dropdown suite is "history: filter dropdowns" below. It asserts the
// CURRENT rule — options come from the configured languages/providers merged
// with the values seen in loaded rows — rather than "populated from fetched
// entries", which updateHistoryFilters' own comment records as the behavior
// that was removed (options that depended on how many pages happened to be
// loaded). The filter change -> reload wiring is not in this module: app.ts
// (263-273) binds `change`/`input` on #h-type/#h-lang/#h-provider/#h-filter to
// reloadHistory, and every suite here drives that entry point directly.

describe("history: page query", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("asks for the first page with no offset and no filter params", async () => {
    dispatch.mockResolvedValueOnce(page([]));

    reloadHistory();
    await tick();

    expect(lastQuery()).toStrictEqual({
      limit: 50,
      offset: undefined,
      type: undefined,
      lang: undefined,
      provider: undefined,
      search: undefined,
    });
  });

  it("passes every active filter through and trims the free-text one", async () => {
    store.set("config", configOf(["fr"], { subdl: true }));
    sel("h-type").value = "movie";
    filterInput().value = "  the wire  ";
    // Pages with a row, so the filtered-and-empty probe never runs and the last
    // query is the page query this case is about.
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();
    // h-lang / h-provider only hold a value once updateHistoryFilters has
    // built their options, so the second reload is the one under test.
    sel("h-lang").value = "fr";
    sel("h-provider").value = "subdl";
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();

    expect(lastQuery()).toStrictEqual({
      limit: 50,
      offset: undefined,
      type: "movie",
      lang: "fr",
      provider: "subdl",
      search: "the wire",
    });
  });

  it("drops a whitespace-only free-text filter", async () => {
    filterInput().value = "   ";
    dispatch.mockResolvedValueOnce(page([]));

    reloadHistory();
    await tick();

    expect(lastQuery()["search"]).toBeUndefined();
  });

  it("offsets the next page by the number of rows already loaded", async () => {
    dispatch.mockResolvedValueOnce(page(fullPage())).mockResolvedValueOnce(page([]));

    reloadHistory();
    await tick();
    clickShowMore();
    await tick();

    expect(lastQuery()).toStrictEqual({
      limit: 50,
      offset: 50,
      type: undefined,
      lang: undefined,
      provider: undefined,
      search: undefined,
    });
  });

  it("collapses a failed page fetch into the error panel", async () => {
    dispatch.mockRejectedValueOnce(new Error("history unavailable"));

    reloadHistory();
    await tick();

    expect(document.querySelector('#historyContent [data-status="err"]')?.textContent).toBe(
      "history unavailable",
    );
  });
});

describe("history: row navigation", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  async function loadOne(partial: Partial<Entry>): Promise<void> {
    dispatch.mockResolvedValueOnce(page([{ ...makeEntry(1), ...partial }]));
    reloadHistory();
    await tick();
  }

  it("a movie row navigates to the movie route with the tmdb id", async () => {
    await loadOne({ media_id: "tmdb-603" });

    firstRow().click();

    expect(emit).toHaveBeenCalledWith("nav:route", "/movie/603");
  });

  it("an episode row navigates to the series route with the tvdb id", async () => {
    await loadOne({ media_id: "tvdb-81189-s01e02" });

    firstRow().click();

    expect(emit).toHaveBeenCalledWith("nav:route", "/series/81189");
  });

  it("a clickable row is focusable and keyboard-operable", async () => {
    await loadOne({ media_id: "tmdb-603" });
    const row = firstRow();
    expect(row.className).toBe("clickable");
    expect(row.getAttribute("tabindex")).toBe("0");

    const enter = new KeyboardEvent("keydown", { key: "Enter", cancelable: true });
    row.dispatchEvent(enter);

    expect(emit).toHaveBeenCalledWith("nav:route", "/movie/603");
    // Without preventDefault, Space would scroll the focused row's page.
    expect(enter.defaultPrevented).toBe(true);
  });

  it("space activates a row too, and other keys do not", async () => {
    await loadOne({ media_id: "tmdb-603" });
    const row = firstRow();

    row.dispatchEvent(new KeyboardEvent("keydown", { key: " ", cancelable: true }));
    expect(emit).toHaveBeenCalledTimes(1);

    row.dispatchEvent(new KeyboardEvent("keydown", { key: "a", cancelable: true }));
    expect(emit).toHaveBeenCalledTimes(1);
  });

  it("an unrecognised media id renders a plain, non-navigating row", async () => {
    await loadOne({ media_id: "imdb-tt0306414" });
    const row = firstRow();
    expect(row.className).toBe("");

    row.click();

    expect(emit).not.toHaveBeenCalled();
  });

  it("an entry with no media id at all renders a plain row", async () => {
    await loadOne({ media_id: "" });

    firstRow().click();

    expect(emit).not.toHaveBeenCalled();
  });
});

describe("history: row cells", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  async function loadOne(partial: Partial<Entry>): Promise<void> {
    dispatch.mockResolvedValueOnce(page([{ ...makeEntry(1), ...partial }]));
    reloadHistory();
    await tick();
  }

  it("renders time, media, language, provider, mode and release in that order", async () => {
    await loadOne({
      media_imported: "2026-07-19T12:00:00Z",
      title: "The Wire",
      language: "fr",
      provider: "subdl",
      manual: false,
      release_name: "The.Wire.S01E01.1080p",
    });

    expect(cellText().slice(1)).toEqual([
      "The Wire",
      "fr",
      "subdl",
      "auto",
      "The.Wire.S01E01.1080p",
    ]);
  });

  it("dates the row from the import timestamp", async () => {
    await loadOne({ media_imported: "2026-07-19T12:00:00Z" });

    // The clock half is locale-formatted; the calendar half is not.
    expect(cellText()[0]).toMatch(/^2026-07-19 /);
  });

  it("tags each cell with the column it belongs to", async () => {
    await loadOne({});

    expect([...firstRow().children].map((c) => c.getAttribute("data-col"))).toEqual([
      "meta",
      "title",
      "meta",
      "meta",
      "meta",
      "meta",
    ]);
  });

  it("appends the episode marker to an episode's title", async () => {
    await loadOne({ title: "The Wire", season: 1, episode: 2 });

    expect(cellText()[1]).toBe("The Wire \u00B7 S01E02");
  });

  it("marks a specials episode, whose season is zero", async () => {
    await loadOne({ title: "The Wire", season: 0, episode: 5 });

    expect(cellText()[1]).toBe("The Wire \u00B7 S00E05");
  });

  it("leaves a movie title bare", async () => {
    await loadOne({ title: "Bladerunner", season: 0, episode: 0 });

    expect(cellText()[1]).toBe("Bladerunner");
  });

  it("qualifies a non-standard variant in the language cell", async () => {
    await loadOne({ language: "fr", variant: "forced" });

    expect(cellText()[2]).toBe("fr forced");
  });

  it("marks manual downloads apart from automatic ones", async () => {
    await loadOne({ manual: true });

    expect(cellText()[4]).toBe("manual");
  });

  it("renders an empty release cell when the server sent no release name", async () => {
    await loadOne({ release_name: "" });

    expect(cellText()[5]).toBe("");
  });

  it("repaints a row whose fields moved under a stable id", async () => {
    // An auto row that is re-downloaded keeps its subtitle_state id:
    // saveAutoRow updates the existing row in place, preserving id and
    // media_imported while provider, release_name and the release metadata
    // move. So the collection takes a content change with an UNCHANGED id
    // sequence, the structure tier never re-runs, and the row repaints only
    // if the binding asked for it.
    await loadOne({ provider: "opensubtitles", release_name: "Show.S01E01.720p.HDTV" });
    const row = firstRow();
    expect(cellText().slice(3)).toEqual(["opensubtitles", "auto", "Show.S01E01.720p.HDTV"]);
    const structuralBefore = structural.runs;

    dispatch.mockResolvedValueOnce(
      page([{ ...makeEntry(1), provider: "subdl", release_name: "Show.S01E01.1080p.WEB-DL" }]),
    );
    reloadHistory();
    await tick();

    expect(firstRow()).toBe(row); // repainted in place, not re-mounted
    expect(structural.runs).toBe(structuralBefore); // and not via a reconcile
    expect(cellText().slice(3)).toEqual(["subdl", "auto", "Show.S01E01.1080p.WEB-DL"]);
  });
});

describe("history: filter dropdowns", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("offers configured languages and providers merged with those seen in rows", async () => {
    store.set("config", configOf(["en", "fr"], { subdl: true, gestdown: false }));
    dispatch.mockResolvedValueOnce(
      page([
        { ...makeEntry(1), language: "de", provider: "opensubtitles" },
        { ...makeEntry(2), language: "", provider: "" },
      ]),
    );

    reloadHistory();
    await tick();

    expect(optionValues("h-lang")).toEqual(["", "de", "en", "fr"]);
    expect(optionValues("h-provider")).toEqual(["", "gestdown", "opensubtitles", "subdl"]);
  });

  it("falls back to the loaded rows alone when no config is available", async () => {
    dispatch.mockResolvedValueOnce(page([{ ...makeEntry(1), language: "de", provider: "subdl" }]));

    reloadHistory();
    await tick();

    expect(optionValues("h-lang")).toEqual(["", "de"]);
    expect(optionValues("h-provider")).toEqual(["", "subdl"]);
  });

  it("labels the leading option as the all-values choice", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();

    expect(sel("h-lang").options.item(0)?.textContent).toBe("All languages");
    expect(sel("h-provider").options.item(0)?.textContent).toBe("All providers");
  });

  it("keeps the current selection across a reload", async () => {
    store.set("config", configOf(["en", "fr"], { subdl: true }));
    dispatch
      .mockResolvedValueOnce(page([makeEntry(1)]))
      .mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();
    sel("h-lang").value = "fr";
    reloadHistory();
    await tick();

    expect(sel("h-lang").value).toBe("fr");
  });
});

describe("history: empty states", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
    // Every case here decides its own answer to "is the underlying history
    // empty", so it must not inherit the previous case's.
    _resetHistoryForTest();
  });

  // An ARRAY, not the first match: the two messages are mutually exclusive, so
  // the count is half of what every case below asserts, and a querySelector
  // helper cannot express "both are in the DOM" at all. IN THE DOM, not
  // "visible": nothing hides a message any more, so presence is the whole
  // question. Scoped to #historyContent because the coverage panel builds
  // `.empty` nodes too.
  function emptyTexts(): string[] {
    return Array.from(document.querySelectorAll("#historyContent .empty")).map(
      (e) => e.textContent ?? "",
    );
  }

  function table(): HTMLElement | null {
    return document.querySelector<HTMLElement>("table.history");
  }

  function showMore(): HTMLElement | null {
    return document.querySelector<HTMLElement>(".more-btn");
  }

  /** The list container's children as `TAG.class`, a message also carrying its
   *  text: ONE value per rendered state, so each row of the design's
   *  what-the-user-sees table is asserted against a single read and a failure
   *  prints the whole subtree's tag/class/text list rather than one bit of it. */
  function panelShape(): string[] {
    return Array.from(document.querySelectorAll("#historyContent > .hist-list > *")).map((e) =>
      e.classList.contains("empty")
        ? `${e.tagName}.empty "${e.textContent ?? ""}"`
        : `${e.tagName}.${e.className}`,
    );
  }

  async function loadRows(rows: Entry[]): Promise<void> {
    dispatch.mockResolvedValueOnce(page(rows));
    reloadHistory();
    await tick();
  }

  it("offers the no-downloads-yet placeholder when nothing has been downloaded", async () => {
    // The reported scenario: zero downloads ever, every control at the value
    // the page ships it with. ONE message, no table, no show-more.
    await loadRows([]);

    expect(panelShape()).toEqual(['DIV.empty "No downloads yet."']);
  });

  it("carries no action button on a placeholder that was given no action", async () => {
    await loadRows([]);

    expect(document.querySelector(".empty button")).toBeNull();
  });

  it("blames the filter when a filter is what emptied the list", async () => {
    filterInput().value = "nothing matches";

    // An empty page whose unfiltered total is nonzero: the read is filtered
    // SERVER-side, so the rows alone cannot tell the two messages apart and the
    // total is what does.
    dispatch.mockResolvedValueOnce(page([], 1));
    reloadHistory();
    await tick();

    expect(panelShape()).toEqual(['DIV.empty "No downloads matching filter."']);
  });

  it("keeps blaming the empty history when a filter is set over a history that has nothing in it", async () => {
    // An empty history beats an active filter: you have nothing at all,
    // whatever you asked for.
    await loadRows([]);
    filterInput().value = "nothing matches";

    dispatch.mockResolvedValueOnce(page([]));
    reloadHistory();
    await tick();

    expect(emptyTexts()).toEqual(["No downloads yet."]);
  });

  it("blames the filter once the read says the store holds a row", async () => {
    // The other side of the case above: same empty filtered page, and the
    // answer flips purely on the total that came back with it.
    await loadRows([makeEntry(1)]);
    filterInput().value = "nothing matches";

    dispatch.mockResolvedValueOnce(page([], 1));
    reloadHistory();
    await tick();

    expect(emptyTexts()).toEqual(["No downloads matching filter."]);
  });

  it("does not treat a whitespace-only filter box as filtering", async () => {
    filterInput().value = "   ";

    await loadRows([]);

    expect(emptyTexts()).toEqual(["No downloads yet."]);
  });

  it("treats a type filter alone as filtering", async () => {
    sel("h-type").value = "movie";

    dispatch.mockResolvedValueOnce(page([], 1));
    reloadHistory();
    await tick();

    expect(emptyTexts()).toEqual(["No downloads matching filter."]);
  });

  it("treats a language filter alone as filtering", async () => {
    store.set("config", configOf(["fr"], {}));
    // The first load is what populates the dropdown: a <select> silently
    // refuses a value it has no option for.
    await loadRows([]);
    sel("h-lang").value = "fr";

    dispatch.mockResolvedValueOnce(page([], 1));
    reloadHistory();
    await tick();

    expect(emptyTexts()).toEqual(["No downloads matching filter."]);
  });

  it("treats a provider filter alone as filtering", async () => {
    store.set("config", configOf([], { subdl: true }));
    await loadRows([]);
    sel("h-provider").value = "subdl";

    dispatch.mockResolvedValueOnce(page([], 1));
    reloadHistory();
    await tick();

    expect(emptyTexts()).toEqual(["No downloads matching filter."]);
  });

  it("an empty unfiltered read never blames a filter, whatever the controls say now", async () => {
    // The read asked for the WHOLE history and got nothing back, so the box the
    // reader has typed into since cannot turn this into a filtered miss. The
    // total is nonzero so that the dispatch snapshot is the only thing standing
    // between the two messages: a control read back after the await answers
    // "filtered" here and paints the other one.
    const release = deferOnePage();

    reloadHistory();
    await tick();
    filterInput().value = "nothing matches";
    release(page([], 1));
    await tick();

    expect(emptyTexts()).toEqual(["No downloads yet."]);
  });

  it("shows the table and neither placeholder once rows land", async () => {
    await loadRows([makeEntry(1)]);

    expect(panelShape()).toEqual(["TABLE.history"]);
  });

  it("the table NODE and its bindings survive leaving the DOM", async () => {
    await loadRows([makeEntry(1)]);
    const first = table();
    expect(first).not.toBeNull();

    filterInput().value = "nothing matches";
    dispatch.mockResolvedValueOnce(page([], 1));
    reloadHistory();
    await tick();
    // Rows were on screen a moment ago: the message REPLACES the table rather
    // than joining it, which is the state the reported bug rendered wrong.
    expect(panelShape()).toEqual(['DIV.empty "No downloads matching filter."']);

    filterInput().value = "";
    await loadRows([makeEntry(1), makeEntry(2)]);

    // toBe, never toEqual: toEqual compares DOM nodes structurally and passes
    // for a rebuilt twin, which is what a per-render build would produce.
    expect(table()).toBe(first);
    // The binding survived the detach: bindList reconciles into the detached
    // tbody, so the rows are already there when the table re-attaches.
    expect(panelShape()).toEqual(["TABLE.history"]);
    expect(reqTbody().children.length).toBe(2);
    expect(cellText()[1]).toBe("Title 1");
  });

  it("leaves the rendered list in place when a reload does not change the shape", async () => {
    await loadRows([makeEntry(1)]);
    const row = firstRow();
    row.focus();
    expect(document.activeElement).toBe(row);

    await loadRows([makeEntry(1)]);

    // Re-seating a node that is already in place rebuilds its rendering, which
    // blurs a focused descendant and restarts every animation in the subtree.
    // This effect re-runs on every reload, and element identity survives a
    // re-seat, so focus is the only observable that can see one.
    expect(document.activeElement).toBe(row);
  });

  it("keeps the filtered placeholder away when the filter did match rows", async () => {
    filterInput().value = "Title";

    await loadRows([makeEntry(1)]);

    expect(emptyTexts()).toEqual([]);
    expect(table()).not.toBeNull();
  });

  it("offers show-more only when the server filled the page", async () => {
    await loadRows(fullPage());

    expect(showMore()).not.toBeNull();
  });

  it("orders the slots table then show-more", async () => {
    // A FULL page is the only state in which both slots are present, so it is
    // the only one where the order is observable at all.
    await loadRows(fullPage());

    expect(panelShape()).toEqual(["TABLE.history", "BUTTON.more-btn"]);
  });

  it("leaves show-more out of the DOM on a short page", async () => {
    await loadRows([makeEntry(1)]);

    expect(showMore()).toBeNull();
  });

  it("puts every child of the list container there through the reconcile", async () => {
    // `.hist-list` is in no stylesheet: it is the ONE parent whose children are
    // all reconcile's, which is what keeps the reconcile away from the skeleton
    // and error writes that land in #historyContent itself.
    await loadRows([makeEntry(1)]);

    expect(document.querySelector("#historyContent > .hist-list")).not.toBeNull();
    expect(panelShape()).toEqual(["TABLE.history"]);
  });
});

describe("history: reload lifecycle", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("builds the table shell once and keeps it across reloads", async () => {
    dispatch
      .mockResolvedValueOnce(page([makeEntry(1)]))
      .mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();
    const tbody = reqTbody();
    const row = tbody.children.item(0);

    reloadHistory();
    await tick();

    expect(reqTbody()).toBe(tbody);
    expect(reqTbody().children.item(0)).toBe(row);
  });

  it("names every column in the header row", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();

    expect(
      [...document.querySelectorAll("table.history thead th")].map((th) => th.textContent),
    ).toEqual(["Time", "Media", "Lang", "Provider", "Mode", "Release"]);
  });

  it("discards a superseded reload rather than letting it overwrite the newer one", async () => {
    // The stale (filter-change) reload resolves LAST but must not land.
    let releaseStale: (p: Page) => void = () => undefined;
    dispatch
      .mockImplementationOnce(
        () =>
          new Promise<Page>((resolve) => {
            releaseStale = resolve;
          }),
      )
      .mockResolvedValueOnce(page([makeEntry(99)]));

    reloadHistory();
    reloadHistory();
    await tick();
    expect(reqTbody().children.length).toBe(1);

    releaseStale(page([makeEntry(1), makeEntry(2), makeEntry(3)]));
    await tick();

    expect(reqTbody().children.length).toBe(1);
  });
});

describe("history: show more", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("merges a later page's providers and languages into the dropdowns", async () => {
    dispatch
      .mockResolvedValueOnce(page(fullPage()))
      .mockResolvedValueOnce(page([{ ...makeEntry(51), language: "de", provider: "subdl" }]));

    reloadHistory();
    await tick();
    expect(optionValues("h-provider")).toEqual(["", "opensubtitles"]);

    clickShowMore();
    await tick();

    expect(optionValues("h-provider")).toEqual(["", "opensubtitles", "subdl"]);
    expect(optionValues("h-lang")).toEqual(["", "de", "en"]);
  });

  it("surfaces a failed next page in the error panel", async () => {
    dispatch
      .mockResolvedValueOnce(page(fullPage()))
      .mockRejectedValueOnce(new Error("page 2 failed"));

    reloadHistory();
    await tick();
    clickShowMore();
    await tick();

    expect(document.querySelector('#historyContent [data-status="err"]')?.textContent).toBe(
      "page 2 failed",
    );
  });

  it("discards a next page superseded by a filter-change reload", async () => {
    let releasePage2: (p: Page) => void = () => undefined;
    dispatch
      .mockResolvedValueOnce(page(fullPage()))
      .mockImplementationOnce(
        () =>
          new Promise<Page>((resolve) => {
            releasePage2 = resolve;
          }),
      )
      .mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();
    clickShowMore();
    reloadHistory();
    await tick();
    expect(reqTbody().children.length).toBe(1);

    releasePage2(page([makeEntry(51), makeEntry(52)]));
    await tick();

    expect(reqTbody().children.length).toBe(1);
  });

  it("does not replace the winning page with a superseded next page's error", async () => {
    let rejectPage2: (e: Error) => void = () => undefined;
    dispatch
      .mockResolvedValueOnce(page(fullPage()))
      .mockImplementationOnce(
        () =>
          new Promise<Page>((_resolve, reject) => {
            rejectPage2 = reject;
          }),
      )
      .mockResolvedValueOnce(page([makeEntry(1)]));

    reloadHistory();
    await tick();
    clickShowMore();
    reloadHistory();
    await tick();

    rejectPage2(new Error("stale page 2"));
    await tick();

    expect(document.querySelector('#historyContent [data-status="err"]')).toBeNull();
    expect(reqTbody().children.length).toBe(1);
  });
});

describe("history: superseded reloads", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does not replace the winning page with a superseded reload's error", async () => {
    let rejectStale: (e: Error) => void = () => undefined;
    dispatch
      .mockImplementationOnce(
        () =>
          new Promise<Page>((_resolve, reject) => {
            rejectStale = reject;
          }),
      )
      .mockResolvedValueOnce(page([makeEntry(7)]));

    reloadHistory();
    reloadHistory();
    await tick();
    expect(reqTbody().children.length).toBe(1);

    rejectStale(new Error("stale failure"));
    await tick();

    expect(document.querySelector('#historyContent [data-status="err"]')).toBeNull();
    expect(reqTbody().children.length).toBe(1);
  });

  it("never paints the first-mount skeleton over an already-live table", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    reloadHistory();
    await tick();
    expect(document.querySelector("table.history")).not.toBeNull();

    vi.useFakeTimers();
    let release: (p: Page) => void = () => undefined;
    dispatch.mockImplementationOnce(
      () =>
        new Promise<Page>((resolve) => {
          release = resolve;
        }),
    );
    reloadHistory();
    // Well past the skeleton's 150ms show delay: a first-mount reload would
    // have painted by now and dropped the live table's bindings.
    await vi.advanceTimersByTimeAsync(500);

    expect(document.querySelector(".skeleton-row")).toBeNull();
    expect(document.querySelector("table.history")).not.toBeNull();

    release(page([makeEntry(1)]));
    await vi.advanceTimersByTimeAsync(500);
  });
});

// --- Task 9: the settlement model behind reloadHistoryForTransaction ---
//
// Every created generation settles exactly once as applied | failed |
// superseded(next) | abandoned; a superseded run resolves "superseded" only
// by chaining to a generation that APPLIES, and a chain ending failed leaves
// the transaction leg rejecting (prior rows intact). The dispatcher's
// re-route (an abort) settles superseded(next: null) → "rerouted".

describe("history: the settlement model (task 9)", () => {
  beforeEach(() => {
    _resetHistoryForTest();
    mountShell();
    store.set("config", null);
  });

  /** Queue one page that stays in flight until the returned release runs. */
  function deferPage(): (items: unknown) => void {
    let release!: (v: unknown) => void;
    dispatch.mockImplementationOnce(
      () =>
        new Promise((res) => {
          release = res;
        }),
    );
    return (items: unknown) => {
      release(items);
    };
  }

  it("an applied run resolves 'applied' and lands its rows", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1), makeEntry(2)]));

    const r = await reloadHistoryForTransaction();

    expect(r).toBe("applied");
    expect(reqTbody().children.length).toBe(2);
  });

  it("a current-run 502 REJECTS with prior rows intact and no error panel", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1), makeEntry(2)]));
    await reloadHistoryForTransaction();

    dispatch.mockResolvedValueOnce(null); // the raw read answers non-2xx

    await expect(reloadHistoryForTransaction()).rejects.toThrow("history load failed");
    // Prior rows intact; the transaction owns recovery, so the leg painted
    // no error panel over them.
    expect(reqTbody().children.length).toBe(2);
    expect(document.querySelector('#historyContent [data-status="err"]')).toBeNull();
  });

  it("a superseded run whose superseder APPLIES resolves 'superseded'", async () => {
    const releaseT = deferPage();
    const t = reloadHistoryForTransaction();
    await Promise.resolve();

    // S starts (bumps the generation) and applies a fresh page.
    dispatch.mockResolvedValueOnce(page([makeEntry(9)]));
    reloadHistory();
    await tick();

    // T lands late: its landing is discarded, its chain ends in S's apply.
    releaseT(page([makeEntry(1)]));
    expect(await t).toBe("superseded");
    expect(reqTbody().children.length).toBe(1);
    expect(cellText()[1]).toBe("Title 9"); // S's row, not T's
  });

  it("the LATCHED CHAIN: the leg latched behind a run whose follow-up 502s — the leg REJECTS", async () => {
    store.set("currentPage", "history");
    dispatch.mockResolvedValueOnce(page([makeEntry(1), makeEntry(2)]));
    await reloadHistoryForTransaction(); // prior rows

    // A reload is in flight; the second leg arrives and LATCHES.
    const releaseFirst = deferPage();
    const first = reloadHistoryForTransaction();
    const second = reloadHistoryForTransaction();
    // Attach the rejection expectation before the chain settles, so the
    // rejection never sits unhandled across a macrotask.
    const secondOutcome = expect(second).rejects.toThrow("history load failed");
    await Promise.resolve();

    // The first run applies; the drained latch execution answers 502.
    dispatch.mockResolvedValueOnce(null);
    releaseFirst(page([makeEntry(1), makeEntry(2)]));
    await tick();

    expect(await first).toBe("applied");
    await secondOutcome;
    // Prior rows intact; the transaction owns recovery, so the leg painted
    // no error panel over them.
    expect(reqTbody().children.length).toBe(2);
    expect(document.querySelector('#historyContent [data-status="err"]')).toBeNull();
  });

  it("the dispatcher's RE-ROUTE (abort) settles 'rerouted' — no latch, no rejection", async () => {
    const release = deferPage();
    const ctrl = new AbortController();
    const t = reloadHistoryForTransaction(ctrl.signal);
    await Promise.resolve();

    ctrl.abort(); // the route left; the re-routed leg owns the continuation
    release(page([makeEntry(1)]));

    expect(await t).toBe("rerouted");
    expect(document.querySelector("table.history tbody")?.children.length ?? 0).toBe(0);
  });

  it("the UI adapter routes a chain-final failure to the error panel", async () => {
    dispatch.mockResolvedValueOnce(null);

    reloadHistory();
    await tick();

    const err = document.querySelector('#historyContent [data-status="err"]');
    expect(err?.textContent).toBe("history load failed");
  });
});

// --- Task 12: the event trigger, foreground priority, and the depth cap ---
//
// E4: coverage events and terminal activity events (noted by events.ts via
// noteHistoryMutation, OUTSIDE the A6 gate) reload the OPEN history page
// through one serializer. A gesture is never cancelled by an event reload —
// the event latches ONE pending reload that runs at the NEW depth after the
// append commits; a filter change supersedes everything; a route leave drops
// the latch. Reloads are depth-preserving newest-window fetches, capped at
// HISTORY_DEPTH_CAP (mocked to 250 here so the cap is reachable without ten
// thousand rows).

function entries(startId: number, count: number): Entry[] {
  return Array.from({ length: count }, (_, i) => makeEntry(startId + i));
}

/** Flush the microtask + due-timer queue under fake timers. */
const flushFake = async (): Promise<void> => {
  await vi.advanceTimersByTimeAsync(0);
};

/** Queue one page that stays in flight until the returned release runs. */
function deferOnePage(): (items: unknown) => void {
  let release!: (v: unknown) => void;
  dispatch.mockImplementationOnce(
    () =>
      new Promise((res) => {
        release = res;
      }),
  );
  return (items: unknown) => {
    release(items);
  };
}

/** Load exactly `n` rows (a page-0 reload plus Show-more appends of full
 *  pages), asserting the depth landed. */
async function loadDepth(n: number): Promise<void> {
  dispatch.mockResolvedValueOnce(page(entries(1, PAGE)));
  reloadHistory();
  await flushFake();
  for (let start = PAGE + 1; start <= n; start += PAGE) {
    dispatch.mockResolvedValueOnce(page(entries(start, PAGE)));
    clickShowMore();
    await flushFake();
  }
  expect(reqTbody().children.length).toBe(n);
}

/** The show-more button IF it is in the DOM. Its ABSENCE is what the short-page
 *  rule and the depth cap express now, so this read must not throw. */
function moreBtn(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(".more-btn");
}

// --- The transaction leg's JOIN of an in-flight route reload ---
//
// /history boot dispatches the route's page-0 reload from the gate release,
// before the transaction's page leg is asked for one. That read was issued after
// the epoch head, so the leg has no reason to pay for a second: it joins the run
// and resolves on its settlement. What makes the join safe is the stamp — the
// transaction a run was dispatched under, and the depth it asked for.

describe("history: the transaction leg's join (task 12)", () => {
  beforeEach(() => {
    _resetHistoryForTest();
    mountShell();
    store.set("config", null);
    store.set("currentPage", "history");
    vi.useFakeTimers();
  });

  afterEach(() => {
    settleTransaction();
    _resetHistoryForTest();
    vi.useRealTimers();
  });

  it("JOINS the route's in-flight reload at boot: ONE list read, not two", async () => {
    beginTransaction();
    const release = deferOnePage();
    reloadHistory(); // the gate release's page-0 reload
    await flushFake();

    const leg = reloadHistoryForTransaction();
    release(page(entries(1, 2)));
    await flushFake();

    expect(await leg).toBe("applied");
    expect(dispatch.mock.calls.length).toBe(1);
    expect(reqTbody().children.length).toBe(2);
  });

  it("latches instead when NO transaction is open, so an idle-page reload is never joined", async () => {
    const release = deferOnePage();
    reloadHistory();
    await flushFake();

    const leg = reloadHistoryForTransaction();
    dispatch.mockResolvedValueOnce(page(entries(1, 2))); // the drained latch's own read
    release(page(entries(1, 2)));
    await flushFake();

    // The latch's own generation lands, so the leg's chain ends in ITS apply.
    expect(await leg).toBe("superseded");
    expect(dispatch.mock.calls.length).toBe(2);
  });

  it("does not join a run stamped with an EARLIER transaction", async () => {
    beginTransaction();
    const release = deferOnePage();
    reloadHistory(); // stamped with the first transaction
    await flushFake();

    // The first transaction aborted and a second opened: the in-flight read
    // predates the second one's mutations, so it cannot answer for it.
    settleTransaction();
    beginTransaction();
    const leg = reloadHistoryForTransaction();
    dispatch.mockResolvedValueOnce(page(entries(1, 2)));
    release(page(entries(1, 2)));
    await flushFake();

    expect(await leg).toBe("superseded");
    expect(dispatch.mock.calls.length).toBe(2);
  });

  it("does not join a SHALLOWER window than the leg would ask for", async () => {
    await loadDepth(100);
    const baseline = dispatch.mock.calls.length;

    // A filter-change reload reads one page; the leg owes 100 rows, so joining
    // would silently shorten the table it promised to preserve.
    beginTransaction();
    const release = deferOnePage();
    reloadHistory();
    await flushFake();

    const leg = reloadHistoryForTransaction();
    dispatch.mockResolvedValueOnce(page(entries(1, PAGE)));
    release(page(entries(1, PAGE)));
    await flushFake();

    expect(await leg).toBe("superseded");
    expect(dispatch.mock.calls.length).toBe(baseline + 2);
  });

  it("reports 'rerouted' when the route leaves during a JOINED run", async () => {
    beginTransaction();
    const release = deferOnePage();
    reloadHistory();
    await flushFake();

    const ctrl = new AbortController();
    const leg = reloadHistoryForTransaction(ctrl.signal);
    ctrl.abort(); // abortPageLeg: the route left, the next dispatch owns it
    release(page(entries(1, 2)));
    await flushFake();

    expect(await leg).toBe("rerouted");
  });

  it("REJECTS when the JOINED run fails, so the transaction aborts", async () => {
    beginTransaction();
    const release = deferOnePage();
    reloadHistory();
    await flushFake();

    const leg = reloadHistoryForTransaction();
    const outcome = expect(leg).rejects.toThrow("history load failed");
    release(null); // the raw read answers non-2xx
    await flushFake();

    await outcome;
    expect(dispatch.mock.calls.length).toBe(1);
  });
});

describe("history: the event trigger (task 12)", () => {
  beforeEach(() => {
    _resetHistoryForTest();
    mountShell();
    store.set("config", null);
    store.set("currentPage", "history");
    vi.useFakeTimers();
  });

  afterEach(() => {
    _resetHistoryForTest();
    vi.useRealTimers();
  });

  it("a burst of notes with history OPEN reloads once per window", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    reloadHistory();
    await flushFake();
    expect(dispatch).toHaveBeenCalledTimes(1);

    dispatch.mockResolvedValueOnce(page([makeEntry(1), makeEntry(2)]));
    noteHistoryMutation();
    noteHistoryMutation();
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await flushFake();

    expect(dispatch).toHaveBeenCalledTimes(2); // ONE reload for the burst
    expect(reqTbody().children.length).toBe(2);
  });

  it("notes with the history page closed reload nothing", async () => {
    store.set("currentPage", "library");

    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 2);

    expect(dispatch).not.toHaveBeenCalled();
  });

  it("an event reload on an EMPTY page asks for one full page", async () => {
    // A fresh tab at /history with nothing loaded (the poller-import case):
    // the trigger's reload uses the page floor, not a zero limit.
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));

    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await flushFake();

    expect(lastQuery()).toMatchObject({ limit: 50 });
    expect(reqTbody().children.length).toBe(1);
  });

  it("the event reload preserves the loaded depth in one fetch", async () => {
    await loadDepth(150);

    dispatch.mockResolvedValueOnce(page(entries(1, 150)));
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await flushFake();

    expect(lastQuery()).toMatchObject({ limit: 150, offset: undefined });
    expect(reqTbody().children.length).toBe(150);
  });

  it("a note mid-flight of an event reload latches ONE trailing reload", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    reloadHistory();
    await flushFake();

    const release = deferOnePage();
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // reload dispatched, in flight
    noteHistoryMutation(); // a fresh event arrives mid-flight
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // its window fires → latch
    const inFlightCount = dispatch.mock.calls.length;

    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    release(page([makeEntry(1)]));
    await flushFake();

    expect(dispatch.mock.calls.length).toBe(inFlightCount + 1); // the trailing reload
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 3);
    expect(dispatch.mock.calls.length).toBe(inFlightCount + 1); // and only one
  });

  it("a route leave clears an armed trigger window", async () => {
    noteHistoryMutation();

    store.set("currentPage", "library");
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 2);

    expect(dispatch).not.toHaveBeenCalled();
  });
});

describe("history: foreground priority (task 12)", () => {
  beforeEach(() => {
    _resetHistoryForTest();
    mountShell();
    store.set("config", null);
    store.set("currentPage", "history");
    vi.useFakeTimers();
  });

  afterEach(() => {
    _resetHistoryForTest();
    vi.useRealTimers();
  });

  it("THE STORM ORACLE: N events during an in-flight Show more latch ONE reload at the new depth", async () => {
    await loadDepth(150);
    const baseline = dispatch.mock.calls.length; // 3: the page-0 reload + two appends
    const firstNode = reqTbody().children.item(0);

    const releaseAppend = deferOnePage();
    clickShowMore(); // the gesture: offset 150, in flight
    await flushFake();

    for (let i = 0; i < 5; i++) {
      noteHistoryMutation();
    }
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // window → latch (gesture running)
    for (let i = 0; i < 5; i++) {
      noteHistoryMutation();
    }
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // second window → same latch
    // The gesture was never cancelled and no reload was dispatched past it.
    expect(dispatch.mock.calls.length).toBe(baseline + 1);

    dispatch.mockResolvedValueOnce(page(entries(1, 200))); // the trailing reload's window
    releaseAppend(page(entries(151, 50))); // the append commits: 150 → 200
    await flushFake();

    // Exactly ONE pending reload ran, at the NEW depth, all 200 rows keyed.
    expect(dispatch.mock.calls.length).toBe(baseline + 2);
    expect(lastQuery()).toMatchObject({ limit: 200, offset: undefined });
    expect(reqTbody().children.length).toBe(200);
    expect(reqTbody().children.item(0)).toBe(firstNode); // keyed setAll reused the node
  });

  it("the depth pin under a storm: count preserved, survivors keep identity, displaced drop", async () => {
    await loadDepth(150);
    const survivor = reqTbody().children.item(5); // entry id 6's node

    // The newest 150 rows are now 6..155: five new rows arrived server-side,
    // five oldest displaced off the bottom.
    dispatch.mockResolvedValueOnce(page(entries(6, 150)));
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await flushFake();

    expect(lastQuery()).toMatchObject({ limit: 150 });
    const rows = reqTbody().children;
    expect(rows.length).toBe(150); // COUNT preserved
    expect(rows.item(0)).toBe(survivor); // surviving row keeps its node
    expect(rows.item(0)?.textContent).toContain("Title 6");
    expect(rows.item(149)?.textContent).toContain("Title 155"); // newest tail
  });

  it("a filter change supersedes both the in-flight gesture and the pending latch", async () => {
    await loadDepth(150);
    const releaseAppend = deferOnePage();
    clickShowMore();
    await flushFake();
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // latch armed behind the gesture

    dispatch.mockResolvedValueOnce(page([makeEntry(999)]));
    reloadHistory(); // the filter change
    await flushFake();
    expect(lastQuery()).toMatchObject({ limit: 50, offset: undefined }); // page-0 semantics
    expect(reqTbody().children.length).toBe(1);

    const afterFilter = dispatch.mock.calls.length;
    releaseAppend(page(entries(151, 50))); // the superseded gesture lands late
    await flushFake();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 3);

    expect(reqTbody().children.length).toBe(1); // discarded — the filter page stands
    expect(reqTbody().children.item(0)?.textContent).toContain("Title 999");
    expect(dispatch.mock.calls.length).toBe(afterFilter); // and no trailing reload either
  });

  it("the transaction leg latches behind an in-flight gesture: COMMIT WAITS for the trailing reload", async () => {
    await loadDepth(50);
    const releaseAppend = deferOnePage();
    clickShowMore();
    await flushFake();

    const outcomes: string[] = [];
    const leg = reloadHistoryForTransaction().then((r) => {
      outcomes.push(r);
      return r;
    });
    await flushFake();
    expect(outcomes).toEqual([]); // the leg waits behind the gesture

    dispatch.mockResolvedValueOnce(page(entries(1, 100))); // the trailing reload at the NEW depth
    releaseAppend(page(entries(51, 50)));
    await flushFake();

    expect(lastQuery()).toMatchObject({ limit: 100 });
    expect(await leg).toBe("superseded"); // the latch's chain ended in the trailing apply
    expect(reqTbody().children.length).toBe(100);
  });

  it("a route leave drops the pending latch: the leg resolves 'rerouted', the gesture still lands", async () => {
    await loadDepth(50);
    const releaseAppend = deferOnePage();
    clickShowMore();
    await flushFake();
    const leg = reloadHistoryForTransaction(); // latches behind the gesture

    store.set("currentPage", "library"); // the route leave drops the latch

    releaseAppend(page(entries(51, 50)));
    await flushFake();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 3);

    expect(await leg).toBe("rerouted");
    expect(reqTbody().children.length).toBe(100); // foreground append landed
    expect(dispatch.mock.calls.length).toBe(2); // page-0 + gesture; NO trailing reload
  });

  it("the re-arm seam latches one trailing reload behind an in-flight reload", async () => {
    await loadDepth(50);
    const release = deferOnePage();
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS); // event reload dispatched, in flight

    reArmHistoryLatch(); // a full-pair overwrite reset heals mid-flight

    dispatch.mockResolvedValueOnce(page(entries(1, 50)));
    release(page(entries(1, 50)));
    await flushFake();

    expect(dispatch.mock.calls.length).toBe(3); // page-0 + event reload + re-armed trailing
  });

  it("the re-arm seam is a no-op when idle or off /history", async () => {
    await loadDepth(50);

    reArmHistoryLatch(); // idle: nothing in flight, nothing in doubt
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 3);
    expect(dispatch.mock.calls.length).toBe(1);

    store.set("currentPage", "library");
    reArmHistoryLatch();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS * 3);
    expect(dispatch.mock.calls.length).toBe(1);
  });
});

describe("history: the depth cap (task 12)", () => {
  beforeEach(() => {
    _resetHistoryForTest();
    mountShell();
    store.set("config", null);
    store.set("currentPage", "history");
    vi.useFakeTimers();
  });

  afterEach(() => {
    _resetHistoryForTest();
    vi.useRealTimers();
  });

  it("leaves show-more out of the DOM at the cap while hasMore stays true internally", async () => {
    await loadDepth(250); // the mocked cap; the last page was full, so hasMore is true

    expect(moreBtn()).toBeNull();

    // hasMore is still TRUE internally: a full event reload at the cap depth
    // keeps it true, and the button stays out rather than flickering back in.
    dispatch.mockResolvedValueOnce(page(entries(1, 250)));
    noteHistoryMutation();
    await vi.advanceTimersByTimeAsync(SUMMARY_COALESCE_MS);
    await flushFake();

    expect(lastQuery()).toMatchObject({ limit: 250 }); // clamped to the cap
    expect(reqTbody().children.length).toBe(250);
    expect(moreBtn()).toBeNull();
  });

  it("below the cap the full-page rule still shows the button", async () => {
    await loadDepth(200);

    expect(moreBtn()).not.toBeNull();
  });
});
