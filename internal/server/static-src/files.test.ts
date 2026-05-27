// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./api-client.js", () => ({ apiGet: vi.fn().mockResolvedValue([]) }));
vi.mock("./actions/index.js", () => ({
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue({ ok: true }) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./bus.js", () => ({
  emit: vi.fn(),
  BusEvent: { NavRoute: "nav:route" },
}));
vi.mock("./sync.js", () => ({ openSyncDialog: vi.fn() }));
vi.mock("./dom.js", async (importOriginal) => {
  const actual = (await importOriginal()) as Record<string, unknown>;
  return { ...actual, confirm: vi.fn().mockResolvedValue(true) };
});

describe("files: renderFiles", () => {
  beforeEach(() => {
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"></div><div id="coverageContent"></div></div>';
  });

  it.todo("renders empty state when no external files");

  it.todo("renders table with one row per external file");

  it.todo("rows are keyed by media_id-language-source");

  it.todo("series files show episode column");

  it.todo("movie files omit episode column");

  it.todo("delete button calls deleteFile with correct entry");

  it.todo("sync button opens sync dialog with correct params");

  it.todo("bulk delete button shows count of external files");

  it.todo("after delete, removed file row disappears without full rebuild");
});
