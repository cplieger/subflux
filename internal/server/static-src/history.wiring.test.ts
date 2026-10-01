//
// history.wiring.test.ts — the parts of history.ts history.test.ts cannot
// reach: the first-mount skeleton (which needs fake timers plus a page that
// stays pending past the show delay, so the paint, its minimum visible window
// and the two staleness guards around it are all reachable), the bindings a
// discarded render owes the collection on both the re-mount and the error path,
// the monotonic token that keeps three in-flight fetches from interleaving, and
// the bus entry point the page is loaded through.
//
// Same doubles as history.test.ts and for the same reasons: the network and the
// bus are replaced, the reactive collection, the real DOM helpers, the real
// store and the real skeleton primitive are not — so what the assertions read
// is the DOM (and the timing) a browser would get.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// One controllable mock backs the generated listState function history.ts
// fetches pages through; per-call pages are queued with mockResolvedValueOnce
// or, for a page that must stay in flight, with deferPage().
const { dispatch } = vi.hoisted(() => ({ dispatch: vi.fn() }));
vi.mock("./wire/client.gen.js", () => ({
  // ONE arm. Every case queues a page; the one error case REJECTS (see the
  // error-path suite), which reaches runReload's catch without passing through
  // here, and no case in this file aborts a controller.
  listStateRaw: async (query?: unknown, opts?: unknown) => ({
    ok: true as const,
    status: 200,
    data: (await dispatch(query, opts)) as unknown,
  }),
}));

// PLAIN functions, never vi.fn: history.ts registers its LoadHistory handler in
// a module initializer, and vitest.config's mockReset would strip a vi.fn's
// implementation (and clear its recorded calls) before the first test ran.
const bus = vi.hoisted(() => ({
  handlers: new Map<string, () => void>(),
  emitted: [] as { event: string; payload: unknown }[],
}));
vi.mock("./bus.js", () => ({
  on: (event: string, handler: () => void) => {
    bus.handlers.set(event, handler);
    return () => undefined;
  },
  emit: (event: string, payload: unknown) => {
    bus.emitted.push({ event, payload });
  },
  BusEvent: { LoadHistory: "load:history", NavRoute: "nav:route" },
}));

import * as store from "./store.js";
import { reloadHistory } from "./history.js";
import { _resetPanelsForTest, historyPanel } from "./panels.js";
import { historyView } from "./view-scope.js";

// Mirrors the wire StateEntry fields buildHistoryRow reads.
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

function makeEntry(id: number, extra: Partial<Entry> = {}): Entry {
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
    ...extra,
  };
}

const PAGE = 50;

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

/** A page the server says is full, so hasMore turns on and Show more appears. */
function fullPage(): Entry[] {
  return Array.from({ length: PAGE }, (_, i) => makeEntry(i + 1));
}

/** One macrotask turn: enough for a resolved page's microtask chain plus the
 *  render it settles. Real timers only. */
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

/** Drain the microtask queue without advancing fake timers, so a pending
 *  show-delay stays pending. */
async function drain(): Promise<void> {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
  }
}

/** Queue a page that never settles on its own; the returned function finishes
 *  it. Each call owns its own promise, so several can be in flight at once. */
function deferPage(): (p: Page) => void {
  let settle: (p: Page) => void = () => undefined;
  dispatch.mockImplementationOnce(
    () =>
      new Promise<Page>((resolve) => {
        settle = resolve;
      }),
  );
  return (p: Page) => {
    settle(p);
  };
}

function mountShell(): void {
  _resetPanelsForTest();
  document.body.replaceChildren(historyPanel().root);
}

beforeEach(() => {
  mountShell();
  store.set("config", null);
  bus.emitted = [];
});

afterEach(() => {
  vi.useRealTimers();
});

function reqEl<T extends HTMLElement>(selector: string): T {
  const e = document.querySelector<T>(selector);
  if (e === null) {
    throw new Error(`missing ${selector}`);
  }
  return e;
}

function reqTbody(): HTMLTableSectionElement {
  return reqEl<HTMLTableSectionElement>("table.history tbody");
}

function rowCount(): number {
  return reqTbody().children.length;
}

function titleCell(i = 0): string {
  return reqTbody().children.item(i)?.children.item(1)?.textContent ?? "";
}

function skeletonRows(): Element[] {
  return Array.from(document.querySelectorAll("#historyContent > div.skeleton-row"));
}

function clickShowMore(): void {
  reqEl<HTMLButtonElement>(".more-btn").click();
}

describe("history: first-mount skeleton", () => {
  it("paints six skeleton rows only after the show delay", async () => {
    vi.useFakeTimers();
    const settle = deferPage();

    reloadHistory();
    await drain();

    // A page that lands inside the show-delay window paints no skeleton.
    expect(skeletonRows()).toHaveLength(0);

    vi.advanceTimersByTime(150);

    const rows = skeletonRows();
    expect(rows).toHaveLength(6);
    // Each row carries the shimmer element the stylesheet animates; a row
    // without it is an invisible blank line.
    expect(rows.every((r) => r.querySelector("div.skeleton") !== null)).toBe(true);

    settle(page([makeEntry(1)]));
    await drain();
    vi.advanceTimersByTime(300);
  });

  it("keeps the skeleton up its minimum once painted, then swaps in the table", async () => {
    vi.useFakeTimers();
    const settle = deferPage();

    reloadHistory();
    await drain();
    vi.advanceTimersByTime(150);
    expect(skeletonRows()).toHaveLength(6);

    settle(page([makeEntry(1)]));
    await drain();

    // Painting the table now would blink the skeleton away the instant it
    // appeared — that is what the 300ms min-visible window prevents.
    expect(skeletonRows()).toHaveLength(6);
    expect(document.querySelector("table.history")).toBeNull();

    vi.advanceTimersByTime(300);

    expect(skeletonRows()).toHaveLength(0);
    expect(rowCount()).toBe(1);
  });

  it("suppresses the skeleton of a reload a newer one superseded", async () => {
    vi.useFakeTimers();
    const settleStale = deferPage();
    dispatch.mockResolvedValueOnce(page([makeEntry(9)]));

    reloadHistory();
    await drain();
    reloadHistory();
    await drain();
    expect(rowCount()).toBe(1);

    // The stale reload's show delay elapses only now, with the newer reload's
    // table already live: painting its skeleton would patch that table — and
    // the bindings behind it — straight out of the DOM.
    vi.advanceTimersByTime(500);

    expect(skeletonRows()).toHaveLength(0);
    expect(rowCount()).toBe(1);

    settleStale(page([]));
    await drain();
  });

  it("does not mount a page that was superseded while its skeleton served out its minimum", async () => {
    vi.useFakeTimers();
    const settleFirst = deferPage();

    reloadHistory();
    await drain();
    vi.advanceTimersByTime(150);
    expect(skeletonRows()).toHaveLength(6);

    // The page lands, so its mount is queued behind the min-visible window.
    settleFirst(page([makeEntry(1), makeEntry(2)]));
    await drain();
    expect(document.querySelector("table.history")).toBeNull();

    // A newer reload starts inside that window and is still in flight.
    const settleSecond = deferPage();
    reloadHistory();
    await drain();

    vi.advanceTimersByTime(400);

    // The superseded commit must not mount: the view belongs to the newer
    // reload now, and it has nothing to show yet.
    expect(document.querySelector("table.history")).toBeNull();

    settleSecond(page([makeEntry(3)]));
    await drain();
    vi.advanceTimersByTime(500);
    expect(rowCount()).toBe(1);
  });
});

describe("history: empty states over the first-mount skeleton", () => {
  // The count, not the first match: the two messages are mutually exclusive, and
  // a querySelector read returns the first one whatever the second is doing.
  // Presence is the whole question — nothing hides a message any more.
  function emptyTexts(): string[] {
    return Array.from(document.querySelectorAll("#historyContent .empty")).map(
      (e) => e.textContent ?? "",
    );
  }

  it("mounts the filtered-empty shape through the skeleton path", async () => {
    vi.useFakeTimers();
    reqEl<HTMLInputElement>("#h-filter").value = "nothing matches";
    const settle = deferPage();

    reloadHistory();
    await drain();
    vi.advanceTimersByTime(150);
    expect(skeletonRows()).toHaveLength(6);

    // An empty page whose unfiltered total is nonzero: the filter is what
    // emptied this one.
    settle(page([], 1));
    await drain();
    vi.advanceTimersByTime(300);
    await drain();

    expect(emptyTexts()).toEqual(["No downloads matching filter."]);
    expect(document.querySelector("table.history")).toBeNull();
  });

  it("shows no empty state once rows land over a mount that started out empty", async () => {
    vi.useFakeTimers();
    const settle = deferPage();

    reloadHistory();
    await drain();
    vi.advanceTimersByTime(150);
    settle(page([]));
    await drain();
    vi.advanceTimersByTime(300);
    await drain();
    expect(emptyTexts()).toEqual(["No downloads yet."]);

    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    reloadHistory();
    await drain();

    // A placeholder left painted above a populated table is what a write to a
    // node the mount never inserted looks like from the reader's seat.
    expect(emptyTexts()).toEqual([]);
    expect(rowCount()).toBe(1);
  });

  it("mounts the empty-dataset shape through the skeleton path", async () => {
    vi.useFakeTimers();
    const settle = deferPage();

    reloadHistory();
    await drain();
    vi.advanceTimersByTime(150);

    settle(page([]));
    await drain();
    vi.advanceTimersByTime(300);
    await drain();

    expect(emptyTexts()).toEqual(["No downloads yet."]);
    expect(document.querySelector("table.history")).toBeNull();
  });

  it("mounts the rows shape through the skeleton path", async () => {
    vi.useFakeTimers();
    const settle = deferPage();

    reloadHistory();
    await drain();
    vi.advanceTimersByTime(150);
    expect(skeletonRows()).toHaveLength(6);

    settle(page([makeEntry(1), makeEntry(2)]));
    await drain();
    vi.advanceTimersByTime(300);
    await drain();

    expect(emptyTexts()).toEqual([]);
    expect(rowCount()).toBe(2);
  });
});

describe("history: row labelling", () => {
  it("marks an entry with a season but no episode number as an episode", async () => {
    // Either coordinate alone makes it an episode; a season-scoped row that
    // fell through would render like a movie and lose the distinction.
    dispatch.mockResolvedValueOnce(
      page([makeEntry(1, { title: "The Wire", season: 2, episode: 0 })]),
    );

    reloadHistory();
    await tick();

    expect(titleCell()).toBe("The Wire \u00B7 S02E00");
  });
});

describe("history: media id recognition", () => {
  it("navigates only from an id whose whole shape is one it recognises", async () => {
    dispatch.mockResolvedValueOnce(
      page([
        // A tmdb id is a prefix AND the complete value: trailing extra means this
        // is not a movie, and linking it to /movie/12 sends the user elsewhere.
        makeEntry(1, { media_id: "tmdb-12-s01e01" }),
        // A recognised prefix has to be the START of the id, not a substring:
        // otherwise any id merely containing one links to the wrong item.
        makeEntry(2, { media_id: "x-tmdb-13" }),
        makeEntry(3, { media_id: "x-tvdb-14" }),
        // Control: a well-formed movie id does navigate, so the three above are
        // rejected on their shape rather than by a broken fixture.
        makeEntry(4, { media_id: "tmdb-15" }),
      ]),
    );

    reloadHistory();
    await tick();
    expect(rowCount()).toBe(4);

    for (const row of Array.from(reqTbody().children)) {
      (row as HTMLElement).click();
    }

    // An unrecognised id navigates NOWHERE — not to the item its substring
    // happens to resemble.
    expect(bus.emitted).toEqual([{ event: "nav:route", payload: "/movie/15" }]);
    expect(Array.from(reqTbody().children).map((r) => r.classList.contains("clickable"))).toEqual([
      false,
      false,
      false,
      true,
    ]);
  });
});

describe("history: render disposal", () => {
  it("a re-mounted table leaves its predecessor's bindings disposed", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    reloadHistory();
    await tick();
    const discarded = reqEl<HTMLElement>("table.history");
    // The premise the freeze claim at the end rests on, in the same terms:
    // this render put its one row in, so the identical read down there reads
    // "unchanged" rather than "never populated".
    expect(discarded.querySelector("tbody")?.children.length).toBe(1);

    // An error paint releases the render target (the only thing that replaces
    // the history pane), so the next reload re-mounts. Release is immediate,
    // so the discarded table is frozen from here on.
    historyView.clear();
    reqEl("#historyContent").replaceChildren();
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));
    reloadHistory();
    await tick();
    const live = reqEl<HTMLElement>("table.history");
    expect(live).not.toBe(discarded);

    dispatch.mockResolvedValueOnce(page([]));
    reloadHistory();
    await tick();

    // Only the live render reacts to the collection now: the zero-row reload
    // took the live table out of the DOM, and left the discarded one holding
    // the row it was frozen with.
    expect(live.isConnected).toBe(false);
    expect(discarded.querySelector("tbody")?.children.length).toBe(1);
  });

  it("a failed page leaves the render its error replaced disposed", async () => {
    dispatch.mockResolvedValueOnce(page([makeEntry(1), makeEntry(2)]));
    reloadHistory();
    await tick();
    const discarded = reqTbody();
    expect(discarded.children.length).toBe(2);

    dispatch.mockRejectedValueOnce(new Error("history endpoint down"));
    reloadHistory();
    await tick();
    expect(reqEl('#historyContent [data-status="err"]').textContent).toBe("history endpoint down");

    // The recovering reload loads the collection BEFORE it re-mounts, so a
    // detached tbody still bound to it would reconcile to the new page first.
    dispatch.mockResolvedValueOnce(page([makeEntry(3), makeEntry(4), makeEntry(5)]));
    reloadHistory();
    await tick();

    expect(rowCount()).toBe(3);
    expect(discarded.children.length).toBe(2);
  });
});

describe("history: show more", () => {
  it("restores the scroll offset it captured before appending the next page", async () => {
    dispatch.mockResolvedValueOnce(page(fullPage()));
    reloadHistory();
    await tick();

    window.scrollTo(0, 420);
    const scrollTo = vi.spyOn(window, "scrollTo");
    dispatch.mockResolvedValueOnce(page([makeEntry(51)]));

    clickShowMore();
    await tick();

    expect(rowCount()).toBe(PAGE + 1);
    // Appending rows above the viewport shifts the page under the reader; the
    // captured offset is put back so the list stays where they left it.
    expect(scrollTo).toHaveBeenCalledWith(0, 420);
  });
});

describe("history: staleness token", () => {
  it("discards the oldest of three in-flight fetches whichever kind bumped the token last", async () => {
    // A full page turns Show more on and gives the stale reload something
    // distinguishable to overwrite.
    dispatch.mockResolvedValueOnce(page(fullPage()));
    reloadHistory();
    await tick();
    expect(rowCount()).toBe(PAGE);

    const settleStale = deferPage();
    const settleNewer = deferPage();
    const settleMore = deferPage();
    reloadHistory();
    reloadHistory();
    await drain();
    clickShowMore();
    await drain();

    settleStale(page([makeEntry(999)]));
    await tick();

    // The token has moved twice since this fetch started — once by a reload and
    // once by a show-more — so it is stale under both, and a token that did not
    // keep moving in one direction would let it land.
    expect(rowCount()).toBe(PAGE);

    settleNewer(page([makeEntry(7)]));
    settleMore(page([]));
    await tick();
  });
});

describe("history: bus entry point", () => {
  it("loads the page when the bus asks for history", async () => {
    const handler = bus.handlers.get("load:history");
    expect(handler).toBeDefined();
    dispatch.mockResolvedValueOnce(page([makeEntry(1)]));

    handler?.();
    await tick();

    expect(rowCount()).toBe(1);
  });
});
