// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("./actions/index.js", () => ({
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue([]) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {}),
  emit: vi.fn(),
  BusEvent: { LoadHistory: "load:history", NavRoute: "nav:route" },
}));

describe("history: renderItems", () => {
  beforeEach(() => {
    document.body.innerHTML =
      '<div id="historyPanel"><div id="coverageContent"></div><select id="h-lang"></select><select id="h-provider"></select></div>';
  });

  it.todo("renders table with thead and tbody");

  it.todo("rows are keyed by media_id-media_imported");

  it.todo("each row shows time, media label, language, provider, mode, release");

  it.todo("series entries format season/episode in label");

  it.todo("clickable rows emit NavRoute with correct href");

  it.todo("non-clickable rows (no href) render plain tr");

  it.todo("reconcile preserves existing rows on Show More append");
});

describe("history: filters", () => {
  it.todo("language dropdown populated from fetched entries");

  it.todo("provider dropdown populated from fetched entries");

  it.todo("filter change triggers reload");
});
