// @vitest-environment happy-dom
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// One controllable mock backs the generated listState function history.ts
// fetches pages through. Per-call page returns are queued with
// dispatch.mockResolvedValueOnce(...). Hoisted so the vi.mock factory below
// can close over the same reference history.ts captures at import time.
const { dispatch, emit } = vi.hoisted(() => ({ dispatch: vi.fn(), emit: vi.fn() }));

vi.mock("./wire/client.gen.js", () => ({
  listState: dispatch,
}));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {
    /* noop */
  }),
  emit,
  BusEvent: { LoadHistory: "load:history", NavRoute: "nav:route" },
}));

import * as store from "./store.js";
import { reloadHistory } from "./history.js";
import type { ParsedConfig } from "./wire/types.gen.js";

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

// The history panel shell every suite below renders into. h-type carries real
// options because a <select> silently refuses a value it has no option for;
// h-lang and h-provider are rebuilt by updateHistoryFilters, so they start
// bare like the page ships them.
function mountShell(): void {
  document.body.innerHTML =
    '<div id="historyPanel">' +
    '<select id="h-type">' +
    '<option value=""></option>' +
    '<option value="movie">Movies</option>' +
    '<option value="episode">Episodes</option>' +
    "</select>" +
    '<select id="h-lang"></select>' +
    '<select id="h-provider"></select>' +
    '<input id="h-filter" />' +
    '<div id="historyContent"></div>' +
    "</div>";
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
    // ensureMounted() renders into #historyContent; buildApiUrl() and
    // anyFilterActive() read #h-type/#h-lang/#h-provider/#h-filter, all of
    // which must exist or select(...).value / input(...).value throws on null.
    document.body.innerHTML =
      '<div id="historyPanel">' +
      '<select id="h-type"></select>' +
      '<select id="h-lang"></select>' +
      '<select id="h-provider"></select>' +
      '<input id="h-filter" />' +
      '<div id="historyContent"></div>' +
      "</div>";
  });

  it.todo("renders table with thead and tbody");

  it("rows are keyed by unique subtitle_state id", async () => {
    // Two distinct rows that collide on media_id + language + media_imported.
    // The old key (media_id + media_imported) deduped them into ONE collection
    // entry (yielding a single row); keying on the unique subtitle_state id
    // keeps both.
    const shared = makeEntry(1);
    const a: Entry = { ...shared, id: 1 };
    const b: Entry = { ...shared, id: 2 };
    dispatch.mockResolvedValueOnce([a, b]);

    reloadHistory();
    await tick();

    expect(reqTbody().children.length).toBe(2);
  });

  it.todo("each row shows time, media label, language, provider, mode, release");

  it.todo("series entries format season/episode in label");

  it.todo("clickable rows emit NavRoute with correct href");

  it.todo("non-clickable rows (no href) render plain tr");

  it("reconcile preserves existing rows on Show More append", async () => {
    // Page 0 must be a full page (>= PAGE_SIZE = 50) for hasMore to flip true
    // and enable "Show more"; loadMore then appends the next (short) page.
    // The 2->4 counts in the original plan are not reachable through the public
    // loadMore path (its hasMore gate requires a full first page), so this
    // exercises the same reconcile-preservation invariant with 50 -> 52.
    const page0 = Array.from({ length: 50 }, (_, i) => makeEntry(i + 1));
    const page1 = [makeEntry(51), makeEntry(52)];
    dispatch.mockResolvedValueOnce(page0).mockResolvedValueOnce(page1);

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

  it("loadMore re-serving a page-0 id stays one node", async () => {
    // Overlap probe: loadMore returns an id already shown on page 0. upsert is
    // idempotent — the existing node is reused (=== stable), not duplicated, so
    // the row count grows only by the genuinely-new id.
    const page0 = Array.from({ length: 50 }, (_, i) => makeEntry(i + 1));
    const page1 = [makeEntry(1), makeEntry(51)]; // id 1 overlaps page 0
    dispatch.mockResolvedValueOnce(page0).mockResolvedValueOnce(page1);

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

describe("history: filters", () => {
  it.todo("language dropdown populated from fetched entries");

  it.todo("provider dropdown populated from fetched entries");

  it.todo("filter change triggers reload");
});

describe("history: page query", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("asks for the first page with no offset and no filter params", async () => {
    dispatch.mockResolvedValueOnce([]);

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
    dispatch.mockResolvedValueOnce([]);

    reloadHistory();
    await tick();
    // h-lang / h-provider only hold a value once updateHistoryFilters has
    // built their options, so the second reload is the one under test.
    sel("h-lang").value = "fr";
    sel("h-provider").value = "subdl";
    dispatch.mockResolvedValueOnce([]);

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
    dispatch.mockResolvedValueOnce([]);

    reloadHistory();
    await tick();

    expect(lastQuery()["search"]).toBeUndefined();
  });

  it("offsets the next page by the number of rows already loaded", async () => {
    dispatch.mockResolvedValueOnce(fullPage()).mockResolvedValueOnce([]);

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
    dispatch.mockResolvedValueOnce([{ ...makeEntry(1), ...partial }]);
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
    dispatch.mockResolvedValueOnce([{ ...makeEntry(1), ...partial }]);
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
});

describe("history: filter dropdowns", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("offers configured languages and providers merged with those seen in rows", async () => {
    store.set("config", configOf(["en", "fr"], { subdl: true, gestdown: false }));
    dispatch.mockResolvedValueOnce([
      { ...makeEntry(1), language: "de", provider: "opensubtitles" },
      { ...makeEntry(2), language: "", provider: "" },
    ]);

    reloadHistory();
    await tick();

    expect(optionValues("h-lang")).toEqual(["", "de", "en", "fr"]);
    expect(optionValues("h-provider")).toEqual(["", "gestdown", "opensubtitles", "subdl"]);
  });

  it("falls back to the loaded rows alone when no config is available", async () => {
    dispatch.mockResolvedValueOnce([{ ...makeEntry(1), language: "de", provider: "subdl" }]);

    reloadHistory();
    await tick();

    expect(optionValues("h-lang")).toEqual(["", "de"]);
    expect(optionValues("h-provider")).toEqual(["", "subdl"]);
  });

  it("labels the leading option as the all-values choice", async () => {
    dispatch.mockResolvedValueOnce([makeEntry(1)]);

    reloadHistory();
    await tick();

    expect(sel("h-lang").options.item(0)?.textContent).toBe("All languages");
    expect(sel("h-provider").options.item(0)?.textContent).toBe("All providers");
  });

  it("keeps the current selection across a reload", async () => {
    store.set("config", configOf(["en", "fr"], { subdl: true }));
    dispatch.mockResolvedValueOnce([makeEntry(1)]).mockResolvedValueOnce([makeEntry(1)]);

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
  });

  function visibleEmptyText(): string | undefined {
    return document.querySelector(".empty:not([hidden])")?.textContent ?? undefined;
  }

  function table(): HTMLElement | null {
    return document.querySelector<HTMLElement>("table.history");
  }

  function showMore(): HTMLElement | null {
    return document.querySelector<HTMLElement>(".more-btn");
  }

  async function loadRows(rows: Entry[]): Promise<void> {
    dispatch.mockResolvedValueOnce(rows);
    reloadHistory();
    await tick();
  }

  it("offers the no-downloads-yet placeholder when nothing has been downloaded", async () => {
    await loadRows([]);

    expect(visibleEmptyText()).toBe("No downloads yet.");
    expect(table()?.hidden).toBe(true);
    expect(showMore()?.hidden).toBe(true);
  });

  it("carries no action button on a placeholder that was given no action", async () => {
    await loadRows([]);

    expect(document.querySelector(".empty button")).toBeNull();
  });

  it("blames the filter when a filter is what emptied the list", async () => {
    filterInput().value = "nothing matches";

    await loadRows([]);

    expect(visibleEmptyText()).toBe("No downloads matching filter.");
  });

  it("does not treat a whitespace-only filter box as filtering", async () => {
    filterInput().value = "   ";

    await loadRows([]);

    expect(visibleEmptyText()).toBe("No downloads yet.");
  });

  it("treats a type filter alone as filtering", async () => {
    sel("h-type").value = "movie";

    await loadRows([]);

    expect(visibleEmptyText()).toBe("No downloads matching filter.");
  });

  it("treats a language filter alone as filtering", async () => {
    store.set("config", configOf(["fr"], {}));
    await loadRows([]);
    sel("h-lang").value = "fr";

    await loadRows([]);

    expect(visibleEmptyText()).toBe("No downloads matching filter.");
  });

  it("treats a provider filter alone as filtering", async () => {
    store.set("config", configOf([], { subdl: true }));
    await loadRows([]);
    sel("h-provider").value = "subdl";

    await loadRows([]);

    expect(visibleEmptyText()).toBe("No downloads matching filter.");
  });

  it("shows the table and neither placeholder once rows land", async () => {
    await loadRows([makeEntry(1)]);

    expect(document.querySelectorAll(".empty:not([hidden])").length).toBe(0);
    expect(table()?.hidden).toBe(false);
  });

  it("keeps the filtered placeholder hidden when the filter did match rows", async () => {
    filterInput().value = "Title";

    await loadRows([makeEntry(1)]);

    expect(document.querySelectorAll(".empty:not([hidden])").length).toBe(0);
    expect(table()?.hidden).toBe(false);
  });

  it("offers show-more only when the server filled the page", async () => {
    await loadRows(fullPage());

    expect(showMore()?.hidden).toBe(false);
  });

  it("hides show-more on a short page", async () => {
    await loadRows([makeEntry(1)]);

    expect(showMore()?.hidden).toBe(true);
  });

  it("wraps the panel in the list container the stylesheet targets", async () => {
    await loadRows([makeEntry(1)]);

    expect(document.querySelector("#historyContent > .hist-list")).not.toBeNull();
  });
});

describe("history: reload lifecycle", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("builds the table shell once and keeps it across reloads", async () => {
    dispatch.mockResolvedValueOnce([makeEntry(1)]).mockResolvedValueOnce([makeEntry(1)]);

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
    dispatch.mockResolvedValueOnce([makeEntry(1)]);

    reloadHistory();
    await tick();

    expect(
      [...document.querySelectorAll("table.history thead th")].map((th) => th.textContent),
    ).toEqual(["Time", "Media", "Lang", "Provider", "Mode", "Release"]);
  });

  it("discards a superseded reload rather than letting it overwrite the newer one", async () => {
    // The stale (filter-change) reload resolves LAST but must not land.
    let releaseStale: (rows: Entry[]) => void = () => undefined;
    dispatch
      .mockImplementationOnce(
        () =>
          new Promise<Entry[]>((resolve) => {
            releaseStale = resolve;
          }),
      )
      .mockResolvedValueOnce([makeEntry(99)]);

    reloadHistory();
    reloadHistory();
    await tick();
    expect(reqTbody().children.length).toBe(1);

    releaseStale([makeEntry(1), makeEntry(2), makeEntry(3)]);
    await tick();

    expect(reqTbody().children.length).toBe(1);
  });
});

describe("history: show more", () => {
  beforeEach(() => {
    mountShell();
    store.set("config", null);
  });

  it("does not fetch another page once the server said there are none left", async () => {
    dispatch.mockResolvedValueOnce([makeEntry(1)]);
    reloadHistory();
    await tick();
    expect(dispatch).toHaveBeenCalledTimes(1);

    clickShowMore();
    await tick();

    expect(dispatch).toHaveBeenCalledTimes(1);
  });

  it("merges a later page's providers and languages into the dropdowns", async () => {
    dispatch
      .mockResolvedValueOnce(fullPage())
      .mockResolvedValueOnce([{ ...makeEntry(51), language: "de", provider: "subdl" }]);

    reloadHistory();
    await tick();
    expect(optionValues("h-provider")).toEqual(["", "opensubtitles"]);

    clickShowMore();
    await tick();

    expect(optionValues("h-provider")).toEqual(["", "opensubtitles", "subdl"]);
    expect(optionValues("h-lang")).toEqual(["", "de", "en"]);
  });

  it("surfaces a failed next page in the error panel", async () => {
    dispatch.mockResolvedValueOnce(fullPage()).mockRejectedValueOnce(new Error("page 2 failed"));

    reloadHistory();
    await tick();
    clickShowMore();
    await tick();

    expect(document.querySelector('#historyContent [data-status="err"]')?.textContent).toBe(
      "page 2 failed",
    );
  });

  it("discards a next page superseded by a filter-change reload", async () => {
    let releasePage2: (rows: Entry[]) => void = () => undefined;
    dispatch
      .mockResolvedValueOnce(fullPage())
      .mockImplementationOnce(
        () =>
          new Promise<Entry[]>((resolve) => {
            releasePage2 = resolve;
          }),
      )
      .mockResolvedValueOnce([makeEntry(1)]);

    reloadHistory();
    await tick();
    clickShowMore();
    reloadHistory();
    await tick();
    expect(reqTbody().children.length).toBe(1);

    releasePage2([makeEntry(51), makeEntry(52)]);
    await tick();

    expect(reqTbody().children.length).toBe(1);
  });

  it("does not replace the winning page with a superseded next page's error", async () => {
    let rejectPage2: (e: Error) => void = () => undefined;
    dispatch
      .mockResolvedValueOnce(fullPage())
      .mockImplementationOnce(
        () =>
          new Promise<Entry[]>((_resolve, reject) => {
            rejectPage2 = reject;
          }),
      )
      .mockResolvedValueOnce([makeEntry(1)]);

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
          new Promise<Entry[]>((_resolve, reject) => {
            rejectStale = reject;
          }),
      )
      .mockResolvedValueOnce([makeEntry(7)]);

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
    dispatch.mockResolvedValueOnce([makeEntry(1)]);
    reloadHistory();
    await tick();
    expect(document.querySelector("table.history")).not.toBeNull();

    vi.useFakeTimers();
    let release: (rows: Entry[]) => void = () => undefined;
    dispatch.mockImplementationOnce(
      () =>
        new Promise<Entry[]>((resolve) => {
          release = resolve;
        }),
    );
    reloadHistory();
    // Well past the skeleton's 150ms show delay: a first-mount reload would
    // have painted by now and dropped the live table's bindings.
    await vi.advanceTimersByTimeAsync(500);

    expect(document.querySelector(".skeleton-row")).toBeNull();
    expect(document.querySelector("table.history")).not.toBeNull();

    release([makeEntry(1)]);
    await vi.advanceTimersByTimeAsync(500);
  });
});
