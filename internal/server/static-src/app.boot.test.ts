// app.ts exports nothing and wires everything at module evaluation, so it is
// booted once, at /history, against the shell index.html authors.
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type * as ClientGen from "./wire/client.gen.js";
import { historyPanel, libraryPanel } from "./panels.js";

const seen = vi.hoisted(() => ({ filterCoverage: 0 }));

vi.mock("./actions-boot.js", () => ({ initActions: () => undefined }));
vi.mock("./events.js", () => ({ connect: () => undefined }));
vi.mock("./status.js", () => ({
  initStatusPopover: () => undefined,
  initStatusReconcile: () => undefined,
}));
vi.mock("./detail-scan.js", () => ({ initScanButtons: () => undefined }));
vi.mock("./user-menu.js", () => ({ initUserMenu: () => undefined }));
vi.mock("./security.js", () => ({ initSecurity: () => undefined }));
vi.mock("./webauthn-utils.js", () => ({ sendWebAuthnSignals: () => Promise.resolve() }));
vi.mock("./coverage-heal.js", () => ({ onHealReset: () => () => undefined }));
vi.mock("./config.js", () => ({
  openConfig: () => undefined,
  closeConfig: () => undefined,
  saveConfig: () => Promise.resolve(),
  initLanguages: () => Promise.resolve(),
}));
vi.mock("./search.js", () => ({
  closeSearchPopup: () => undefined,
  openSearchPopup: () => undefined,
}));
vi.mock("./sync.js", () => ({ consumeSyncClosing: () => false }));
vi.mock("./history.js", () => ({
  reloadHistory: () => undefined,
  reArmHistoryLatch: () => undefined,
}));
vi.mock("./files.js", () => ({ openFileManager: () => undefined }));
vi.mock("./page-leg.js", () => ({
  abortPageLeg: () => undefined,
  refreshCurrentPage: () => Promise.resolve("applied"),
}));
// filterCoverage is the library filter's other half; counting it proves the
// handler ran even before the debounced URL write lands.
vi.mock("./coverage.js", () => ({
  filterCoverage: () => {
    seen.filterCoverage++;
  },
  loadCoverage: () => Promise.resolve(),
  renderCoverage: () => undefined,
  configurePanel: () => undefined,
  showLibraryControls: () => undefined,
}));
vi.mock("./coverage-store.js", () => ({
  libraryLoaded: () => true,
  coverageItems: () => [],
  applyHealedRow: () => undefined,
}));
vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<typeof ClientGen>()),
  configParsed: () => Promise.resolve(null),
}));

document.body.innerHTML = `
  <header><h1><a href="/">Subflux</a></h1>
  <button type="button" id="statusBtn">Status</button>
  <button type="button" id="historyBtn">History</button>
  <button type="button" id="userBtn">User</button></header>
  <dialog id="searchResultPopup"></dialog>
  <main id="main"></main>
  <footer><span id="footerYear">2026</span></footer>
  <dialog id="configDialog"><form>
    <button type="button" id="configClose">Close</button>
    <div id="configBody"></div>
    <button type="submit" id="saveConfigBtn">Save</button>
  </form></dialog>`;

const runnerPath = location.pathname + location.search;

// The boot reads location once, so it happens here rather than in a hook: the
// panel switch is synchronous inside applyRoute's history arm.
history.replaceState(null, "", "/history");
await import("./app.js");
await new Promise((r) => setTimeout(r, 0));

beforeEach(() => {
  history.replaceState(null, "", "/history");
  seen.filterCoverage = 0;
  // Panel state outlives a case: both hosts are built once per file, so a
  // filter one case typed into is still filled for the next one and the URL
  // it derives carries both values.
  const p = libraryPanel();
  p.filter.value = "";
  p.typeFilter.value = "all";
  p.missingOnly.checked = false;
  p.sort.value = "title";
});

afterEach(() => {
  history.replaceState(null, "", runnerPath);
});

describe("app.ts boot on /history", () => {
  it("attaches the history panel and leaves the library panel detached (the premise)", () => {
    expect(historyPanel().root.isConnected).toBe(true);
    expect(libraryPanel().root.isConnected).toBe(false);
  });

  it("wires the library filter even though its panel is detached", async () => {
    libraryPanel().filter.value = "inception";
    libraryPanel().filter.dispatchEvent(new Event("input"));

    await vi.waitFor(() => {
      expect(location.pathname + location.search).toBe("/?q=inception");
    });
    expect(seen.filterCoverage).toBe(1);
  });

  it("wires the library type filter even though its panel is detached", () => {
    libraryPanel().typeFilter.value = "movies";
    libraryPanel().typeFilter.dispatchEvent(new Event("change"));

    expect(location.pathname + location.search).toBe("/?type=movies");
  });

  it("focuses the history filter on / because history is the current page", () => {
    historyPanel().filter.blur();

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "/", bubbles: true }));

    expect(document.activeElement).toBe(historyPanel().filter);
  });
});
