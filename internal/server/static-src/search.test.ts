// @vitest-environment happy-dom
import { describe, it, vi, beforeEach } from "vitest";

vi.mock("./api-client.js", () => ({ apiGet: vi.fn().mockResolvedValue(null) }));
vi.mock("./actions/index.js", () => ({
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue(null) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {
    /* noop */
  }),
  emit: vi.fn(),
  BusEvent: {},
}));

describe("search: openSearchPopup", () => {
  beforeEach(() => {
    document.body.innerHTML = '<dialog id="searchResultPopup"></dialog>';
  });

  it.todo("opens dialog as modal");

  it.todo("shows loading state during search");

  it.todo("renders result rows with score, provider, match, release, download button");

  it.todo("top result row has 'top' class");

  it.todo("download button triggers download action");

  it.todo("empty results show 'No results found' message");

  it.todo("error response shows error div");

  it.todo("closing dialog cleans up DOM");
});
