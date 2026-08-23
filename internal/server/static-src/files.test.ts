import { describe, it, vi, beforeEach, expect } from "vitest";

// The apiAction mock mirrors the parts of the real contract files.ts depends
// on: it builds the request (so the body under test is observable), runs
// `optimistic`, and on a configured failure runs `rollback` and resolves null.
interface MockActionDef {
  name?: string;
  request?: (args: unknown) => { body?: unknown };
  optimistic?: (args: unknown) => unknown;
  rollback?: (args: unknown, op: unknown) => void;
}

// Hoisted so the generated-client mock factory closes over the same fn the
// test configures with mockResolvedValueOnce(...).
const { mockListFiles } = vi.hoisted(() => ({ mockListFiles: vi.fn() }));
// Plain mutable records (NOT vi.fn state): vitest config resets mocks between
// tests, so per-test wiring lives in objects the factories close over.
const { actionCalls, confirmState } = vi.hoisted(() => ({
  actionCalls: {
    results: {} as Record<string, unknown>,
    failing: {} as Record<string, boolean>,
    bodies: [] as { name: string; body: unknown }[],
  },
  confirmState: { answer: true, messages: [] as string[] },
}));

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./wire/client.gen.js", () => ({
  listFiles: mockListFiles,
  PATH_DELETE_FILE: "/api/files",
  PATH_BULK_DELETE_FILES: "/api/files/bulk",
}));
// Plain functions (NOT vi.fn): vitest config has mockReset/clearMocks/
// restoreMocks=true, which strips a vi.fn's implementation before each test.
// The dispatch must keep calling def.optimistic, so it cannot be a vi.fn.
vi.mock("@cplieger/actions", () => ({
  apiAction: (def: MockActionDef) => ({
    dispatch: (args: unknown) => {
      const name = def.name ?? "";
      actionCalls.bodies.push({ name, body: def.request?.(args)?.body });
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
import { join } from "@cplieger/keyenc";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { openSyncDialog } from "./sync.js";
import * as store from "./store.js";

// Mirrors the wire FileEntry fields files.ts consumes (only the fields the
// row builder and the collection key read matter here). No paths on the
// wire (S7): ordinal separates manual siblings sharing a quad.
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

// openFileManager() fire-and-forgets loadFiles(); listFiles is mocked and
// resolves on a microtask, so one macrotask turn settles the whole load ->
// setAll -> render chain. A delete click's confirm/dispatch settle the same way.
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

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

/** Text of one column of one row, addressed the way the markup addresses it. */
function cell(i: number, col: string): string {
  return reqRow(i).querySelector(`[data-col="${col}"]`)?.textContent ?? "";
}

function headerLabels(): string[] {
  return [...document.querySelectorAll("table.files-table thead th")].map(
    (th) => th.textContent ?? "",
  );
}

function reqBulkButton(): HTMLButtonElement {
  const btn = document.querySelector<HTMLButtonElement>('[data-nav="bulk-delete"]');
  if (!btn) {
    throw new Error("bulk-delete button not mounted");
  }
  return btn;
}

describe("files: renderFiles", () => {
  beforeEach(() => {
    mockListFiles.mockReset();
    mockListFiles.mockResolvedValue([]);
    actionCalls.results = {};
    actionCalls.failing = {};
    actionCalls.bodies = [];
    confirmState.answer = true;
    confirmState.messages = [];
    history.replaceState(null, "", "/");
    // ensureMounted() renders into #coverageContent; the bulk-delete button is
    // appended to #coveragePanel .card-head, both of which must exist.
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"></div><div id="coverageContent"></div></div>';
  });

  it("two external files for the same media_id+language render as two rows", async () => {
    // The old key `${media_id}-${language}-${source}` collided here
    // ("tmdb-5-fr-external" for both) and dropped the second row; the FileRef
    // key includes the manual-sibling ordinal, keeping both.
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-5", "fr"), extFile("tmdb-5", "fr", 1)]);

    openFileManager("movie", "tmdb-5", "Movie", "/");
    await tick();

    expect(reqTbody().children.length).toBe(2);
  });

  it("single-row delete removes exactly one <tr> and preserves order (node identity stable)", async () => {
    // No videoPaths passed, so each row carries only the delete button.
    // Movie sort is by language: en, es, fr.
    mockListFiles.mockResolvedValueOnce([
      extFile("tmdb-7", "en"),
      extFile("tmdb-7", "fr"),
      extFile("tmdb-7", "es"),
    ]);

    openFileManager("movie", "tmdb-7", "Movie", "/");
    await tick();

    const tbody = reqTbody();
    expect(tbody.children.length).toBe(3);
    const firstBefore = tbody.children.item(0); // en
    const middle = tbody.children.item(1); // es
    const thirdBefore = tbody.children.item(2); // fr
    if (!(middle instanceof HTMLElement)) {
      throw new Error("middle row missing");
    }
    const delBtn = middle.querySelector<HTMLButtonElement>("button.btn-delete");
    if (!delBtn) {
      throw new Error("delete button missing");
    }

    delBtn.click();
    await tick();

    // Exactly one row gone, not a full rebuild: the surviving rows are the SAME
    // DOM nodes (keyed reconcile reuses them).
    expect(tbody.children.length).toBe(2);
    expect(tbody.children.item(0)).toBe(firstBefore);
    expect(tbody.children.item(1)).toBe(thirdBefore);
  });

  it("two files the pipe-joined key collapsed now keep separate rows", async () => {
    // The old key `${media_id}|${language}|${variant}|${ordinal}` read both of
    // these as "tmdb-9|fr|forced|standard|0". createCollection dedupes by key,
    // so one row DISAPPEARED from the manager — and a row that is not rendered
    // has no delete button, making that file undeletable from the UI.
    // `language`/`variant` come from the operator's config.yaml via the stored
    // row, so a value carrying the separator is operator-reachable.
    const a = { ...extFile("tmdb-9", "fr|forced"), variant: "standard" };
    const b = { ...extFile("tmdb-9", "fr"), variant: "forced|standard" };
    const pipeJoined = (f: FileEntry): string =>
      `${f.media_id}|${f.language}|${f.variant}|${f.ordinal}`;
    expect(pipeJoined(a)).toBe(pipeJoined(b)); // the defect

    mockListFiles.mockResolvedValueOnce([a, b]);
    openFileManager("movie", "tmdb-9", "Movie", "/");
    await tick();

    expect(reqTbody().children.length).toBe(2);
  });

  it("keeps rows separate where a plain ':' join would also collapse them", async () => {
    // Proof the fix is an encoding change, not a separator swap: the same
    // forgery aimed at the NEW separator still fails.
    const a = { ...extFile("tmdb-11", "fr:forced"), variant: "standard" };
    const b = { ...extFile("tmdb-11", "fr"), variant: "forced:standard" };
    const colonJoined = (f: FileEntry): string =>
      [f.media_id, f.language, f.variant, String(f.ordinal)].join(":");
    expect(colonJoined(a)).toBe(colonJoined(b)); // naive form collapses

    mockListFiles.mockResolvedValueOnce([a, b]);
    openFileManager("movie", "tmdb-11", "Movie", "/");
    await tick();

    expect(reqTbody().children.length).toBe(2);
  });

  it("encodes an ordinary entry verbatim as the plain ':' join", () => {
    // fileKey is module-private (it is the collection key), so this pins the
    // encoding it builds rather than the function: a component free of both
    // reserved characters is emitted untouched, so an ordinary
    // media_id/language/variant/ordinal tuple is exactly the ':' join and row
    // identity stays readable. Guards against a keyenc change that started
    // escaping ordinary input.
    expect(join("tmdb-5", "fr", "standard", "0")).toBe("tmdb-5:fr:standard:0");
  });

  it("renders the empty state and hides the table when there are no external files", async () => {
    mockListFiles.mockResolvedValueOnce([]);

    openFileManager("movie", "tmdb-13", "Movie", "/");
    await tick();

    const emptyEl = document.querySelector<HTMLElement>(".files-list .empty");
    if (!emptyEl) {
      throw new Error("empty state missing");
    }
    expect(emptyEl.hidden).toBe(false);
    expect(emptyEl.textContent).toBe("No external subtitles.");
    const tbl = document.querySelector<HTMLElement>("table.files-table");
    expect(tbl?.hidden).toBe(true);
    expect(reqBulkButton().hidden).toBe(true);
  });

  it("shows the table with a counted bulk-delete button once files exist", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-15", "en"), extFile("tmdb-15", "fr")]);

    openFileManager("movie", "tmdb-15", "Movie", "/");
    await tick();

    const emptyEl = document.querySelector<HTMLElement>(".files-list .empty");
    expect(emptyEl?.hidden).toBe(true);
    expect(document.querySelector<HTMLElement>("table.files-table")?.hidden).toBe(false);
    const bulk = reqBulkButton();
    expect(bulk.hidden).toBe(false);
    expect(bulk.querySelector(".btn-text")?.textContent).toBe(" Delete all (2)");
  });

  it("series files carry an episode column, sorted by season then episode", async () => {
    // Languages deliberately disagree with episode order: a comparator that
    // sorted by language (or fell through to the fileKey tiebreaker's own
    // ordering) would put S01E12 first.
    mockListFiles.mockResolvedValueOnce([
      extFile("tvdb-81189-s02e01", "en"),
      extFile("tvdb-81189-s01e12", "de"),
      extFile("tvdb-81189-s01e02", "en"),
    ]);

    openFileManager("episode", "tvdb-81189-", "Breaking Bad", "/series/81189", 7);
    await tick();

    expect(headerLabels()).toEqual(["Episode", "Language", "Format", "Offset", "Size", ""]);
    expect(reqRow(0).children.item(0)?.textContent).toBe("S01E02");
    expect(reqRow(1).children.item(0)?.textContent).toBe("S01E12");
    expect(reqRow(2).children.item(0)?.textContent).toBe("S02E01");
  });

  it("same-episode files fall back to language, then to the manual ordinal", async () => {
    // The full comparator chain: season, episode, language, fileKey. Sizes make
    // the ordinal-only pair distinguishable in the rendered row.
    mockListFiles.mockResolvedValueOnce([
      { ...extFile("tvdb-5-s01e01", "en", 1), size: 2048 },
      { ...extFile("tvdb-5-s01e01", "en", 0), size: 512 },
      extFile("tvdb-5-s01e01", "de"),
    ]);

    openFileManager("episode", "tvdb-5-", "Show", "/series/5", 7);
    await tick();

    expect(cell(0, "size")).toBe("1.0 KB"); // de, ordinal 0
    expect(reqRow(0).children.item(1)?.textContent).toBe("German");
    expect(cell(1, "size")).toBe("512 B"); // en ordinal 0 before ordinal 1
    expect(cell(2, "size")).toBe("2.0 KB");
  });

  it("movie files omit the episode column and sort by language", async () => {
    mockListFiles.mockResolvedValueOnce([
      extFile("tmdb-17", "fr"),
      extFile("tmdb-17", "de"),
      extFile("tmdb-17", "en"),
    ]);

    openFileManager("movie", "tmdb-17", "Movie", "/");
    await tick();

    expect(headerLabels()).toEqual(["Language", "Format", "Offset", "Size", ""]);
    expect(reqRow(0).children.item(0)?.textContent).toBe("German");
    expect(reqRow(1).children.item(0)?.textContent).toBe("English");
    expect(reqRow(2).children.item(0)?.textContent).toBe("French");
  });

  it("movie manual siblings sort by ordinal within one language", async () => {
    // Same language for both, so only the fileKey tiebreaker can order them.
    mockListFiles.mockResolvedValueOnce([
      { ...extFile("tmdb-18", "fr", 1), size: 2048 },
      { ...extFile("tmdb-18", "fr", 0), size: 512 },
    ]);

    openFileManager("movie", "tmdb-18", "Movie", "/");
    await tick();

    expect(cell(0, "size")).toBe("512 B");
    expect(cell(1, "size")).toBe("2.0 KB");
  });

  it("formats sizes as B, KB and MB at the unit boundaries", async () => {
    mockListFiles.mockResolvedValueOnce([
      { ...extFile("tmdb-19", "de"), size: 512 },
      { ...extFile("tmdb-19", "en"), size: 1024 },
      { ...extFile("tmdb-19", "es"), size: 1536 },
      { ...extFile("tmdb-19", "fr"), size: 1024 * 1024 },
    ]);

    openFileManager("movie", "tmdb-19", "Movie", "/");
    await tick();

    expect(cell(0, "size")).toBe("512 B");
    expect(cell(1, "size")).toBe("1.0 KB");
    expect(cell(2, "size")).toBe("1.5 KB");
    expect(cell(3, "size")).toBe("1.0 MB");
  });

  it("formats the sync offset in signed seconds", async () => {
    mockListFiles.mockResolvedValueOnce([
      { ...extFile("tmdb-21", "de"), offset_ms: 1500 },
      { ...extFile("tmdb-21", "en"), offset_ms: -2500 },
      { ...extFile("tmdb-21", "fr"), offset_ms: 0 },
    ]);

    openFileManager("movie", "tmdb-21", "Movie", "/");
    await tick();

    expect(cell(0, "offset")).toBe("+1.5s");
    expect(cell(1, "offset")).toBe("-2.5s");
    expect(cell(2, "offset")).toBe("0.0s");
  });

  it("labels a non-standard variant next to the language name", async () => {
    mockListFiles.mockResolvedValueOnce([{ ...extFile("tmdb-23", "fr"), variant: "forced" }]);

    openFileManager("movie", "tmdb-23", "Movie", "/");
    await tick();

    expect(reqRow(0).children.item(0)?.textContent).toBe("French (forced)");
  });

  it("derives a missing codec from the filename extension", async () => {
    mockListFiles.mockResolvedValueOnce([
      { ...extFile("tmdb-25", "en"), codec: "", name: "movie.en.ASS" },
    ]);

    openFileManager("movie", "tmdb-25", "Movie", "/");
    await tick();

    expect(reqRow(0).querySelector("span.badge")?.textContent).toBe("ass: ext");
  });

  it("an orphan row shows its basename and an orphan format badge", async () => {
    const named: FileEntry = {
      source: "external",
      media_id: "",
      language: "",
      variant: "standard",
      codec: "",
      name: "stray.srt",
      ordinal: 0,
      offset_ms: 0,
      size: 1024,
      orphan_handle: "a".repeat(32),
    };
    // `name` absent on the wire (Go omitempty): no basename, no extension.
    const nameless: FileEntry = {
      source: "external",
      media_id: "",
      language: "",
      variant: "standard",
      codec: "",
      ordinal: 0,
      offset_ms: 0,
      size: 1024,
      orphan_handle: "b".repeat(32),
    };
    mockListFiles.mockResolvedValueOnce([named, nameless]);

    openFileManager("movie", "tmdb-27", "Movie", "/", 9);
    await tick();

    expect(reqRow(0).children.item(0)?.textContent).toBe("stray.srt");
    expect(reqRow(0).querySelector("span.badge")?.textContent).toBe("srt: orphan");
    // No name to read an extension from: the badge still says orphan.
    expect(reqRow(1).children.item(0)?.textContent).toBe("unknown file");
    expect(reqRow(1).querySelector("span.badge")?.textContent).toBe("ext: orphan");
    // An orphan has neither a store row nor a quad, so it cannot be synced.
    expect(reqRow(0).querySelector("[data-tip='Adjust subtitle timing']")).toBeNull();
  });

  it("lists only external files, dropping embedded tracks", async () => {
    mockListFiles.mockResolvedValueOnce([
      extFile("tmdb-29", "en"),
      { ...extFile("tmdb-29", "fr"), source: "embedded" },
    ]);

    openFileManager("movie", "tmdb-29", "Movie", "/");
    await tick();

    expect(reqTbody().children.length).toBe(1);
    expect(reqRow(0).children.item(0)?.textContent).toBe("English");
  });

  it("requests the listing for the open item, adding arr_id only when known", async () => {
    mockListFiles.mockResolvedValueOnce([]);
    openFileManager("movie", "tmdb-31", "Movie", "/");
    await tick();
    expect(mockListFiles).toHaveBeenCalledWith({ media_type: "movie", media_id: "tmdb-31" });

    mockListFiles.mockResolvedValueOnce([]);
    openFileManager("episode", "tvdb-33-", "Show", "/series/33", 42);
    await tick();
    expect(mockListFiles).toHaveBeenLastCalledWith({
      media_type: "episode",
      media_id: "tvdb-33-",
      arr_id: "42",
    });
  });

  it("renders an error when the listing fails", async () => {
    mockListFiles.mockResolvedValueOnce(null);

    openFileManager("movie", "tmdb-35", "Movie", "/");
    await tick();

    const err = document.querySelector<HTMLElement>('#coverageContent [data-status="err"]');
    expect(err?.textContent).toBe("Failed to load files");
    expect(document.querySelector("table.files-table")).toBeNull();
  });

  it("pushes the files URL and marks the page in the store", async () => {
    openFileManager("movie", "tmdb-37", "Movie", "/");
    await tick();
    expect(location.pathname).toBe("/movie/37/files");

    // Re-opening the same view must not stack a duplicate history entry. The
    // baseline is read AFTER the first open, not before it: every test iframe
    // shares the runner page's joint session history, Chromium caps that at 50
    // entries, and at the cap a push stops incrementing history.length — so a
    // `depth + 1` assertion is a flake whose trigger is how many entries the
    // rest of the suite happened to add first.
    const stacked = history.length;
    openFileManager("movie", "tmdb-37", "Movie", "/");
    await tick();
    expect(history.length).toBe(stacked);

    openFileManager("episode", "tvdb-81189-", "Breaking Bad", "/series/81189", 7);
    await tick();
    expect(location.pathname).toBe("/series/81189/files");
    expect(store.get("currentPage")).toBe("files");
    expect(store.get("detailCtx")).toEqual({ files: true });
  });

  it("configures the panel with the caller's back path", async () => {
    openFileManager("episode", "tvdb-81189-", "Breaking Bad", "", 7);
    await tick();

    expect(emit).toHaveBeenCalledWith(BusEvent.PanelConfigure, {
      visible: false,
      detail: { title: "Breaking Bad", info: "Subtitle Files", backPath: "/" },
    });
  });

  it("sync opens the dialog for the clicked file with its episode label", async () => {
    const f = extFile("tvdb-81189-s02e05", "en");
    mockListFiles.mockResolvedValueOnce([f]);

    openFileManager("episode", "tvdb-81189-", "Breaking Bad", "/series/81189", 42);
    await tick();

    const syncBtn = reqRow(0).querySelector<HTMLButtonElement>(
      '[data-col="actions"] .action-group [data-tip="Adjust subtitle timing"]',
    );
    if (!syncBtn) {
      throw new Error("sync button missing");
    }
    expect(syncBtn.querySelector(".btn-text")?.textContent).toBe(" Sync");
    syncBtn.click();

    expect(openSyncDialog).toHaveBeenCalledWith([f], "series", 42, "S02E05");
  });

  it("offers no sync button when the arr id is unknown", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-39", "en")]);

    openFileManager("movie", "tmdb-39", "Movie", "/");
    await tick();

    expect(reqRow(0).querySelector("[data-tip='Adjust subtitle timing']")).toBeNull();
    const delBtn = reqRow(0).querySelector<HTMLButtonElement>(
      '[data-col="actions"] .action-group button.btn-delete',
    );
    expect(delBtn?.querySelector(".btn-text")?.textContent).toBe(" Delete");
  });

  it("a declined confirmation deletes nothing", async () => {
    confirmState.answer = false;
    const unnamed: FileEntry = {
      source: "external",
      media_id: "tmdb-41",
      language: "fr",
      variant: "standard",
      codec: "srt",
      ordinal: 0,
      offset_ms: 0,
      size: 1024,
    };
    mockListFiles.mockResolvedValueOnce([unnamed]);

    openFileManager("movie", "tmdb-41", "Movie", "/");
    await tick();
    const delBtn = reqRow(0).querySelector<HTMLButtonElement>("button.btn-delete");
    if (!delBtn) {
      throw new Error("delete button missing");
    }

    delBtn.click();
    await tick();

    expect(reqTbody().children.length).toBe(1);
    // A file with no name is described by its language in the prompt.
    expect(confirmState.messages).toEqual(['Delete "French subtitle"? This cannot be undone.']);
  });

  it("bulk delete empties the table optimistically, then adopts the server's list", async () => {
    actionCalls.results["files.delete_bulk"] = { deleted: 2 };
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-43", "en"), extFile("tmdb-43", "fr")]);

    openFileManager("movie", "tmdb-43", "Movie", "/");
    await tick();
    // One file survived the sweep server-side (a partial delete).
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-43", "fr")]);

    reqBulkButton().click();
    await tick();

    expect(notify.success).toHaveBeenCalledWith("Deleted 2 file(s)");
    // The refresh repaints from the response rather than trusting the clear.
    expect(reqTbody().children.length).toBe(1);
    expect(reqRow(0).children.item(0)?.textContent).toBe("French");
    expect(mockListFiles).toHaveBeenCalledTimes(2);
  });

  it("a declined bulk confirmation deletes nothing", async () => {
    confirmState.answer = false;
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-44", "en"), extFile("tmdb-44", "fr")]);

    openFileManager("movie", "tmdb-44", "Movie", "/");
    await tick();

    reqBulkButton().click();
    await tick();

    expect(reqTbody().children.length).toBe(2);
    expect(notify.success).not.toHaveBeenCalled();
    expect(mockListFiles).toHaveBeenCalledTimes(1);
  });

  it("a failed delete restores the row at its sorted position", async () => {
    actionCalls.failing["files.delete"] = true;
    mockListFiles.mockResolvedValueOnce([
      extFile("tmdb-46", "en"),
      extFile("tmdb-46", "es"),
      extFile("tmdb-46", "fr"),
    ]);

    openFileManager("movie", "tmdb-46", "Movie", "/");
    await tick();
    const delBtn = reqRow(1).querySelector<HTMLButtonElement>("button.btn-delete");
    if (!delBtn) {
      throw new Error("delete button missing");
    }

    delBtn.click();
    await tick();

    expect(reqTbody().children.length).toBe(3);
    expect(reqRow(1).children.item(0)?.textContent).toBe("Spanish");
  });

  it("addresses a stored file by its FileRef and an orphan by its handle", async () => {
    const orphan: FileEntry = {
      source: "external",
      media_id: "",
      language: "",
      variant: "standard",
      codec: "srt",
      name: "stray.srt",
      ordinal: 0,
      offset_ms: 0,
      size: 1024,
      orphan_handle: "c".repeat(32),
    };
    mockListFiles.mockResolvedValueOnce([orphan, extFile("tmdb-47", "fr", 2)]);

    openFileManager("movie", "tmdb-47", "Movie", "/");
    await tick();
    reqRow(0).querySelector<HTMLButtonElement>("button.btn-delete")?.click();
    await tick();
    reqRow(0).querySelector<HTMLButtonElement>("button.btn-delete")?.click();
    await tick();

    expect(actionCalls.bodies).toEqual([
      { name: "files.delete", body: { orphan_handle: "c".repeat(32) } },
      {
        name: "files.delete",
        body: {
          media_type: "movie",
          media_id: "tmdb-47",
          language: "fr",
          variant: "standard",
          source: "external",
          ordinal: 2,
        },
      },
    ]);
  });

  it("a refresh reuses the mounted table instead of rebuilding it", async () => {
    mockListFiles.mockResolvedValueOnce([extFile("tmdb-45", "en")]);
    openFileManager("movie", "tmdb-45", "Movie", "/");
    await tick();
    const tbody = reqTbody();
    const row = reqRow(0);

    mockListFiles.mockResolvedValueOnce([extFile("tmdb-45", "en")]);
    openFileManager("movie", "tmdb-45", "Movie", "/");
    await tick();

    expect(document.querySelectorAll("table.files-table")).toHaveLength(1);
    expect(reqTbody()).toBe(tbody);
    expect(reqRow(0)).toBe(row);
  });
});
