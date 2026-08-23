// @vitest-environment happy-dom
//
// files.wiring.test.ts — the parts of files.ts files.test.ts cannot reach with
// its immediate-resolve listing: the anti-flicker skeleton (which needs fake
// timers and a listing that stays pending past the show delay), the optimistic
// window of a bulk delete (which needs the confirming refresh to stay pending),
// the binding disposal a discarded render owes the collection, and the ordering
// edges where a string sort and an episode sort disagree.
//
// Same doubles as files.test.ts and for the same reasons: the network and the
// toast/confirm seams are replaced, the reactive collection, the sorted view,
// the real DOM helpers and the real skeleton primitive are not — so what the
// assertions read is the DOM (and the timing) a browser would get.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// The apiAction double mirrors the parts of the contract files.ts leans on: it
// builds the request, runs `optimistic`, and on a configured failure runs
// `rollback` and resolves null (the framework's terminal-failure signal).
interface MockActionDef {
  name?: string;
  request?: (args: unknown) => { body?: unknown };
  optimistic?: (args: unknown) => unknown;
  rollback?: (args: unknown, op: unknown) => void;
}

const { mockListFiles } = vi.hoisted(() => ({ mockListFiles: vi.fn() }));
// Plain mutable records, NOT vi.fn state: vitest.config resets mocks between
// tests, so per-test wiring lives in objects the factories close over.
const { actionCalls, confirmState } = vi.hoisted(() => ({
  actionCalls: {
    results: {} as Record<string, unknown>,
    failing: {} as Record<string, boolean>,
  },
  confirmState: { answer: true, messages: [] as string[] },
}));

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./wire/client.gen.js", () => ({
  listFiles: mockListFiles,
  PATH_DELETE_FILE: "/api/files",
  PATH_BULK_DELETE_FILES: "/api/files/bulk",
}));
// Plain functions (NOT vi.fn): mockReset would strip an implementation
// registered at module load, and the dispatch must keep calling def.optimistic.
vi.mock("@cplieger/actions", () => ({
  apiAction: (def: MockActionDef) => ({
    dispatch: (args: unknown) => {
      const name = def.name ?? "";
      // Built for fidelity with the real dispatch (the body is derived from the
      // entry before anything else runs), then discarded.
      def.request?.(args);
      const op = def.optimistic?.(args);
      if (actionCalls.failing[name]) {
        def.rollback?.(args, op);
        return Promise.resolve(null);
      }
      return Promise.resolve(actionCalls.results[name] ?? { ok: true });
    },
  }),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
}));
vi.mock("./bus.js", () => ({
  emit: vi.fn(),
  BusEvent: { PanelConfigure: "panel:configure", NavRoute: "nav:route" },
}));
vi.mock("./sync.js", () => ({ openSyncDialog: vi.fn() }));
vi.mock("./dom.js", async (importOriginal) => {
  const actual = (await importOriginal()) as Record<string, unknown>;
  return {
    ...actual,
    confirm: (_title: string, message: string) => {
      confirmState.messages.push(message);
      return Promise.resolve(confirmState.answer);
    },
  };
});

import { openFileManager } from "./files.js";
import { openSyncDialog } from "./sync.js";
import * as store from "./store.js";

// Mirrors the wire FileEntry fields files.ts consumes. No paths on the wire
// (S7): ordinal separates manual siblings sharing a quad.
interface FileEntry {
  source: string;
  media_id: string;
  language: string;
  variant: string;
  codec: string;
  name?: string;
  ordinal: number;
  offset_ms: number;
  size: number;
  orphan_handle?: string;
}

function extFile(media_id: string, language: string, ordinal = 0): FileEntry {
  return {
    source: "external",
    media_id,
    language,
    variant: "standard",
    codec: "srt",
    name: `movie.${language}${ordinal > 0 ? `.${ordinal}` : ""}.srt`,
    ordinal,
    offset_ms: 0,
    size: 1024,
  };
}

/** One macrotask turn: enough for the mocked listing's microtask chain plus the
 *  render it settles. Real timers only. */
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

/** Drain the microtask queue without advancing fake timers, so a pending
 *  show-delay stays pending. */
async function drain(): Promise<void> {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
  }
}

/** A listing that never settles on its own; the returned resolve finishes it. */
function pendingListing(): (value: FileEntry[]) => void {
  let settle: (value: FileEntry[]) => void = () => undefined;
  mockListFiles.mockReturnValueOnce(
    new Promise<FileEntry[]>((resolve) => {
      settle = resolve;
    }),
  );
  return (value: FileEntry[]) => {
    settle(value);
  };
}

function reqTbody(): HTMLTableSectionElement {
  const tbody = document.querySelector<HTMLTableSectionElement>("table.files-table tbody");
  if (!tbody) {
    throw new Error("files tbody not mounted");
  }
  return tbody;
}

function reqRow(i: number): HTMLElement {
  const row = reqTbody().children.item(i);
  if (!(row instanceof HTMLElement)) {
    throw new Error(`row ${String(i)} missing`);
  }
  return row;
}

/** The first column's text — the episode label for a series, the language for
 *  a movie. */
function firstCol(i: number): string {
  return reqRow(i).children.item(0)?.textContent ?? "";
}

function reqBulkButton(): HTMLButtonElement {
  const btn = document.querySelector<HTMLButtonElement>('[data-nav="bulk-delete"]');
  if (!btn) {
    throw new Error("bulk-delete button not mounted");
  }
  return btn;
}

/** The live bulk button's label node. Captured before a re-render, it is the
 *  handle on the DISCARDED render: whether it still moves tells whether that
 *  render's bindings were disposed. */
function bulkLabel(): Element {
  const label = reqBulkButton().querySelector(".btn-text");
  if (!label) {
    throw new Error("bulk-delete label missing");
  }
  return label;
}

function skeletonRows(): NodeListOf<Element> {
  return document.querySelectorAll("#coverageContent div.skeleton-row");
}

function resetEnv(): void {
  mockListFiles.mockReset();
  mockListFiles.mockResolvedValue([]);
  actionCalls.results = {};
  actionCalls.failing = {};
  confirmState.answer = true;
  confirmState.messages = [];
  history.replaceState(null, "", "/");
  store.set("currentPage", "library");
  // ensureMounted() renders into #coverageContent; the bulk-delete button is
  // appended to #coveragePanel .card-head, both of which must exist.
  document.body.innerHTML =
    '<div id="coveragePanel"><div class="card-head"></div><div id="coverageContent"></div></div>';
}

describe("files: loading skeleton", () => {
  beforeEach(() => {
    resetEnv();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("paints four skeleton rows only after the show delay and keeps them their minimum", async () => {
    const settle = pendingListing();

    openFileManager("movie", "tmdb-51", "Movie", "/");
    await drain();

    // A listing that answers inside the show-delay window paints no skeleton.
    expect(skeletonRows()).toHaveLength(0);

    vi.advanceTimersByTime(150);

    const rows = skeletonRows();
    expect(rows).toHaveLength(4);
    expect(rows[0]?.querySelector("div.skeleton")).not.toBeNull();
    expect(rows[3]?.querySelector("div.skeleton")).not.toBeNull();

    settle([extFile("tmdb-51", "en")]);
    await drain();

    // Painting the table now would blink the skeleton away the instant it
    // appeared — that is what the 300ms min-visible window prevents.
    expect(skeletonRows()).toHaveLength(4);
    expect(document.querySelector("table.files-table")).toBeNull();

    vi.advanceTimersByTime(300);

    expect(skeletonRows()).toHaveLength(0);
    expect(reqTbody().children.length).toBe(1);
  });
});

describe("files: sorted view", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("orders episode 100 after episode 99 rather than by string order", async () => {
    // Both files share a language, so only the season/episode legs of the
    // comparator can order them; the fileKey tiebreaker (a string compare over
    // the media_id) would put "…s01e100" first.
    mockListFiles.mockResolvedValueOnce([
      extFile("tvdb-71-s01e100", "en"),
      extFile("tvdb-71-s01e99", "en"),
    ]);

    openFileManager("episode", "tvdb-71-", "Long Show", "/series/71", 7);
    await tick();

    expect(firstCol(0)).toBe("S01E99");
    expect(firstCol(1)).toBe("S01E100");
  });

  it("repaints a refresh that swaps only a later row for a different file", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-67", "en"), extFile("tmdb-67", "fr")]);
    openFileManager("movie", "tmdb-67", "Movie", "/");
    await tick();
    expect(firstCol(1)).toBe("French");

    // Same row count and same FIRST id, different second id: the sorted view's
    // shallow-equality guard has to compare every position, because a guard
    // satisfied by one matching index would call the two lists equal and the
    // structural render would never learn the row changed.
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-67", "en"), extFile("tmdb-67", "ja")]);
    openFileManager("movie", "tmdb-67", "Movie", "/");
    await tick();

    expect(reqTbody().children.length).toBe(2);
    expect(firstCol(1)).toBe("Japanese");
  });
});

describe("files: page state", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("clears the previous detail context in the same batch that flips the page", async () => {
    store.set("detailCtx", { movie: true, tmdbId: 5 });
    const seenWithPageFlip: unknown[] = [];
    const stop = store.subscribe("currentPage", () => {
      seenWithPageFlip.push(store.get("detailCtx"));
    });

    openFileManager("movie", "tmdb-73", "Movie", "/");
    stop();

    // Anything reacting to the page flip reads detailCtx as it does; a stale
    // detail context surviving that notification points the files view's
    // consumers at the item the user just left.
    expect(seenWithPageFlip).toEqual([null]);
    await tick();
    expect(store.get("detailCtx")).toEqual({ files: true });
  });
});

describe("files: render disposal", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("a re-mounted table leaves its predecessor's bindings disposed", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-63", "en"), extFile("tmdb-63", "fr")]);
    openFileManager("movie", "tmdb-63", "Movie", "/");
    await tick();
    const discarded = bulkLabel();
    expect(discarded.textContent).toBe(" Delete all (2)");

    // Navigating away replaces the render target, so the next load re-mounts.
    document.getElementById("coverageContent")?.replaceChildren();
    mockListFiles.mockResolvedValueOnce([
      extFile("tmdb-63", "de"),
      extFile("tmdb-63", "en"),
      extFile("tmdb-63", "fr"),
    ]);
    openFileManager("movie", "tmdb-63", "Movie", "/");
    await tick();
    expect(bulkLabel()).not.toBe(discarded);

    mockListFiles.mockResolvedValueOnce([extFile("tmdb-63", "en")]);
    openFileManager("movie", "tmdb-63", "Movie", "/");
    await tick();

    // Only the live render tracks the collection now.
    expect(bulkLabel().textContent).toBe(" Delete all (1)");
    expect(discarded.textContent).toBe(" Delete all (3)");
  });

  it("a failed listing leaves the render it replaced disposed", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-65", "en"), extFile("tmdb-65", "fr")]);
    openFileManager("movie", "tmdb-65", "Movie", "/");
    await tick();
    const discarded = bulkLabel();
    expect(discarded.textContent).toBe(" Delete all (2)");

    mockListFiles.mockResolvedValueOnce(null);
    openFileManager("movie", "tmdb-65", "Movie", "/");
    await tick();
    expect(document.querySelector('#coverageContent [data-status="err"]')).not.toBeNull();

    mockListFiles.mockResolvedValueOnce([extFile("tmdb-65", "en")]);
    openFileManager("movie", "tmdb-65", "Movie", "/");
    await tick();

    expect(bulkLabel().textContent).toBe(" Delete all (1)");
    expect(discarded.textContent).toBe(" Delete all (2)");
  });
});

describe("files: row controls contain their click", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("sync and delete both act without letting the click reach the row", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tvdb-79-s01e01", "en")]);

    openFileManager("episode", "tvdb-79-", "Show", "/series/79", 7);
    await tick();
    const row = reqRow(0);
    let rowClicks = 0;
    row.addEventListener("click", () => {
      rowClicks += 1;
    });

    row
      .querySelector<HTMLButtonElement>('[data-tip="Adjust subtitle timing"]')
      ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(openSyncDialog).toHaveBeenCalledTimes(1);
    expect(rowClicks).toBe(0);

    row
      .querySelector<HTMLButtonElement>("button.btn-delete")
      ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();

    expect(confirmState.messages).toHaveLength(1);
    expect(rowClicks).toBe(0);
  });
});

describe("files: delete rollback", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("a delete of a file the collection no longer holds cannot resurrect its row", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-61", "en"), extFile("tmdb-61", "fr")]);
    openFileManager("movie", "tmdb-61", "Movie", "/");
    await tick();
    const delBtn = reqRow(0).querySelector<HTMLButtonElement>("button.btn-delete");
    if (!delBtn) {
      throw new Error("delete button missing");
    }

    delBtn.click();
    await tick();
    expect(reqTbody().children.length).toBe(1);

    // The same entry dispatched a second time (a click that raced the repaint,
    // a queued retry): the file is already gone server-side, so a failure has
    // nothing to roll back and must not re-add the row.
    actionCalls.failing["files.delete"] = true;
    delBtn.click();
    await tick();

    expect(reqTbody().children.length).toBe(1);
    expect(firstCol(0)).toBe("French");
  });
});

describe("files: bulk delete", () => {
  beforeEach(() => {
    resetEnv();
  });

  it("empties the table optimistically while the confirming refresh is still in flight", async () => {
    actionCalls.results["files.delete_bulk"] = { deleted: 2 };
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-75", "en"), extFile("tmdb-75", "fr")]);
    openFileManager("movie", "tmdb-75", "Movie", "/");
    await tick();

    // The refresh stays pending, so what these assertions read is the
    // optimistic state on its own.
    const settle = pendingListing();
    reqBulkButton().click();
    await tick();

    expect(reqTbody().children.length).toBe(0);
    expect(document.querySelector<HTMLElement>("table.files-table")?.hidden).toBe(true);
    expect(document.querySelector<HTMLElement>(".files-list .empty")?.hidden).toBe(false);

    settle([extFile("tmdb-75", "fr")]);
    await tick();

    expect(reqTbody().children.length).toBe(1);
    expect(firstCol(0)).toBe("French");
  });

  it("a failed bulk delete restores every row it cleared", async () => {
    actionCalls.failing["files.delete_bulk"] = true;
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-77", "en"), extFile("tmdb-77", "fr")]);
    openFileManager("movie", "tmdb-77", "Movie", "/");
    await tick();

    reqBulkButton().click();
    await tick();

    expect(reqTbody().children.length).toBe(2);
    expect(firstCol(0)).toBe("English");
    expect(firstCol(1)).toBe("French");
    // A failure repaints from the rollback, not from a fresh listing.
    expect(mockListFiles).toHaveBeenCalledTimes(1);
  });
});
