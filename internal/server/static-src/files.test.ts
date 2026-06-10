// @vitest-environment happy-dom
import { describe, it, vi, beforeEach, expect } from "vitest";

// The apiAction mock runs each def's `optimistic` and resolves success (no
// rollback), so a single-file delete actually removes its collection entry
// (and its row) — the behaviour the delete test asserts.
interface MockActionDef {
  optimistic?: (args: unknown) => unknown;
}

// Hoisted so the api-client mock factory closes over the same fn the test
// configures with mockResolvedValueOnce(...).
const { mockApiGet } = vi.hoisted(() => ({ mockApiGet: vi.fn() }));

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./api-client.js", () => ({ apiGet: mockApiGet }));
// Plain functions (NOT vi.fn): vitest config has mockReset/clearMocks/
// restoreMocks=true, which strips a vi.fn's implementation before each test.
// The dispatch must keep calling def.optimistic, so it cannot be a vi.fn.
vi.mock("@cplieger/actions", () => ({
  apiAction: (def: MockActionDef) => ({
    dispatch: (args: unknown) => {
      def.optimistic?.(args);
      return Promise.resolve({ ok: true });
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
  return { ...actual, confirm: () => Promise.resolve(true) };
});

import { openFileManager } from "./files.js";

// Mirrors files.ts's unexported FileEntry (only the fields the row builder and
// the collection key read matter here).
interface FileEntry {
  source: string;
  media_id: string;
  language: string;
  variant: string;
  codec: string;
  path: string;
  offset_ms: number;
  size: number;
}

function extFile(media_id: string, language: string, path: string): FileEntry {
  return {
    source: "external",
    media_id,
    language,
    variant: "standard",
    codec: "srt",
    path,
    offset_ms: 0,
    size: 1024,
  };
}

// openFileManager() fire-and-forgets loadFiles(); apiGet is mocked and resolves
// on a microtask, so one macrotask turn settles the whole load -> setAll ->
// render chain. A delete click's confirm/dispatch settle the same way.
const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

function reqTbody(): HTMLTableSectionElement {
  const tbody = document.querySelector<HTMLTableSectionElement>("table.files-table tbody");
  if (!tbody) {
    throw new Error("files tbody not mounted");
  }
  return tbody;
}

describe("files: renderFiles", () => {
  beforeEach(() => {
    mockApiGet.mockReset();
    mockApiGet.mockResolvedValue([]);
    // ensureMounted() renders into #coverageContent; the bulk-delete button is
    // appended to #coveragePanel .card-head, both of which must exist.
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"></div><div id="coverageContent"></div></div>';
  });

  it("two external files for the same media_id+language render as two rows", async () => {
    // The old key `${media_id}-${language}-${source}` collided here
    // ("tmdb-5-fr-external" for both) and dropped the second row; keying on the
    // unique `path` keeps both.
    mockApiGet.mockResolvedValueOnce([
      extFile("tmdb-5", "fr", "/media/Movie.fr.srt"),
      extFile("tmdb-5", "fr", "/media/Movie.fr.1.srt"),
    ]);

    openFileManager("movie", "tmdb-5", "Movie", "/");
    await tick();

    expect(reqTbody().children.length).toBe(2);
  });

  it("single-row delete removes exactly one <tr> and preserves order (node identity stable)", async () => {
    // No videoPaths passed, so each row carries only the delete button.
    // Movie sort is by language: en, es, fr.
    mockApiGet.mockResolvedValueOnce([
      extFile("tmdb-7", "en", "/media/Movie.en.srt"),
      extFile("tmdb-7", "fr", "/media/Movie.fr.srt"),
      extFile("tmdb-7", "es", "/media/Movie.es.srt"),
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

  it.todo("renders empty state when no external files");

  it.todo("series files show episode column");

  it.todo("movie files omit episode column");

  it.todo("sync button opens sync dialog with correct params");

  it.todo("bulk delete button shows count of external files");
});
