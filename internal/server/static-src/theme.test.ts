import { describe, it, expect, beforeEach, vi } from "vitest";
import type * as ThemeModule from "./theme.js";

// theme.ts holds ONE lazily-created controller for the module's lifetime, so
// each test re-imports the module fresh (vi.resetModules) to get a controller
// that reads the storage this test seeded.
//
// The storage key is a contract, not an implementation detail: the paint-time
// anti-FOUC snippet inlined into index.html reads the same key (see
// theme-snippet.test.ts), so a key mismatch resolves one theme before paint and
// another at runtime. These tests read/write the literal key for that reason.
const THEME_KEY = "subflux-theme";

beforeEach(() => {
  vi.resetModules();
  window.localStorage.clear();
  document.documentElement.removeAttribute("data-theme");
});

// The `?boot=` query is what makes the re-import re-execute theme.ts, and it is
// not decoration. Browser Mode resolves a dynamic import through the browser's
// own module map, which is keyed by URL and holds evaluated modules for the life
// of the page: vi.resetModules() clears the runner's registry but cannot evict an
// entry from that map, so a bare import("./theme.js") returns the controller an
// earlier test already created and every test after the first reads storage it
// never seeded. A distinct query is a distinct URL and therefore a fresh
// evaluation. `@vite-ignore` opts out of Vite's variable-dynamic-import rewrite.
//
// The `.ts` extension is load-bearing: this specifier is built at runtime, so the
// URL the browser requests is the one written here, and that URL is what v8
// coverage attributes the evaluation to. Written `./theme.js` it names a file that
// does not exist, and theme.ts reports 0% coverage while this suite stays green.
let bootCount = 0;
async function loadTheme(): Promise<typeof ThemeModule> {
  return (await import(/* @vite-ignore */ `./theme.ts?boot=${++bootCount}`)) as typeof ThemeModule;
}

function applied(): string | null {
  return document.documentElement.getAttribute("data-theme");
}

describe("theme", () => {
  it("init applies the resolved theme to the document element", async () => {
    const { init } = await loadTheme();

    init();

    // Nothing stored resolves to the OS preference, which headless Chromium
    // reports as light — the point is that SOMETHING is applied, before any
    // click.
    expect(applied()).toBe("light");
  });

  it("init applies the stored preference", async () => {
    window.localStorage.setItem(THEME_KEY, "dark");
    const { init } = await loadTheme();

    init();

    expect(applied()).toBe("dark");
  });

  it("cycle moves an unset preference to light", async () => {
    const { cycle, choice } = await loadTheme();

    cycle();

    expect(choice()).toBe("light");
    expect(applied()).toBe("light");
  });

  it("cycle walks light to dark", async () => {
    window.localStorage.setItem(THEME_KEY, "light");
    const { cycle, choice } = await loadTheme();

    cycle();

    expect(choice()).toBe("dark");
    expect(applied()).toBe("dark");
  });

  it("cycle walks dark back to system", async () => {
    window.localStorage.setItem(THEME_KEY, "dark");
    const { cycle, choice } = await loadTheme();

    cycle();

    expect(choice()).toBe("system");
  });

  it("cycle persists the new preference under the shared storage key", async () => {
    window.localStorage.setItem(THEME_KEY, "light");
    const { cycle } = await loadTheme();

    cycle();

    expect(window.localStorage.getItem(THEME_KEY)).toBe("dark");
  });

  it("choice reports the stored preference, not the resolved theme", async () => {
    window.localStorage.setItem(THEME_KEY, "system");
    const { init, choice } = await loadTheme();

    init();

    // Resolved is "light" here; the user menu labels its button from the STORED
    // choice, so the two must not be conflated.
    expect(choice()).toBe("system");
    expect(applied()).toBe("light");
  });
});
