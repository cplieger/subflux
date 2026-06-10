// @vitest-environment happy-dom
import { describe, it, vi, beforeEach, expect } from "vitest";

// One controllable dispatch mock backs the history.load apiAction. Per-call
// page returns are queued with dispatch.mockResolvedValueOnce(...). Hoisted so
// the vi.mock factory below can close over the same reference history.ts
// captures at import time (apiAction is called once on module load).
const { dispatch } = vi.hoisted(() => ({ dispatch: vi.fn() }));

vi.mock("@cplieger/actions", () => ({
  apiAction: vi.fn(() => ({ dispatch })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {
    /* noop */
  }),
  emit: vi.fn(),
  BusEvent: { LoadHistory: "load:history", NavRoute: "nav:route" },
}));

import { reloadHistory } from "./history.js";

// Mirrors history.ts's unexported HistoryEntry (only the fields buildHistoryRow
// reads matter for the assertions here).
interface Entry {
  id: number;
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

function makeEntry(id: number): Entry {
  return {
    id,
    media_id: `tmdb-${id}`,
    media_type: "movie",
    language: "en",
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
