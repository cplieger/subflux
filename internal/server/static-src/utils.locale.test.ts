// How utils.ts renders a clock: 12-hour or 24-hour, decided from the user's
// locale. utils.test.ts covers the pure helpers; this file covers the two
// MODULE-SCOPE constants (`use12h` and the `timeFmt` formatter built from it),
// which resolve from Intl once at import time. That is why every test pins the
// locale FIRST and then imports the module fresh — a static import would
// freeze whatever locale the very first import happened to see.
import { describe, it, expect, vi } from "vitest";
import type * as UtilsModule from "./utils.js";

// The real constructor, captured before any stub. vitest.config sets
// unstubGlobals, so each test's stub is undone for the next one.
const RealDateTimeFormat = Intl.DateTimeFormat;

// 15:45 local time: the same instant reads "03:45 PM" on a 12-hour clock and
// "15:45" on a 24-hour one, so the two renderings are never ambiguous.
const afternoon = new Date(2026, 0, 2, 15, 45, 30);

/** Replace Intl with one whose DateTimeFormat always resolves `locale`,
 *  whatever the host default is — `locale` IS the "user's locale" that the
 *  detection reads. Everything else about the formatter stays real, so the
 *  hour cycle is genuinely derived rather than asserted into place. */
function pinLocale(locale: string): void {
  class PinnedLocale extends RealDateTimeFormat {
    constructor(_locales?: unknown, options?: Intl.DateTimeFormatOptions) {
      super(locale, options);
    }
  }
  vi.stubGlobal("Intl", { DateTimeFormat: PinnedLocale });
}

/** Same, but the formatter refuses to report its resolved options — the shape
 *  of engine the detection's try/catch exists for. */
function pinLocaleWithoutResolvedOptions(locale: string): void {
  class NoResolvedOptions extends RealDateTimeFormat {
    constructor(_locales?: unknown, options?: Intl.DateTimeFormatOptions) {
      super(locale, options);
    }
    override resolvedOptions(): Intl.ResolvedDateTimeFormatOptions {
      throw new TypeError("resolvedOptions is not supported here");
    }
  }
  vi.stubGlobal("Intl", { DateTimeFormat: NoResolvedOptions });
}

/** Re-execute utils.ts so its module-scope locale detection runs again.
 *
 *  The `?boot=` query is what makes the re-execution happen, and it is not
 *  decoration. Browser Mode resolves a dynamic import through the browser's own
 *  module map, which is keyed by URL and holds evaluated modules for the life of
 *  the page: `vi.resetModules()` clears the runner's registry but cannot evict an
 *  entry from that map, so a bare `import("./utils.js")` returns the instance an
 *  earlier test already evaluated and the locale detection never re-runs. A
 *  distinct query is a distinct URL and therefore a fresh evaluation.
 *  `@vite-ignore` opts out of Vite's variable-dynamic-import rewrite, which
 *  otherwise resolves the specifier against a generated glob map no query
 *  matches.
 *
 *  The `.ts` extension is load-bearing too, and it is the one thing here that
 *  looks like a typo and is not. A statically-analyzable `import("./utils.js")`
 *  is rewritten to the resolved `utils.ts` id at transform time, but this
 *  specifier is built at runtime, so the URL the browser requests is the one
 *  written here -- and that URL is what v8 coverage reports against. Written
 *  `./utils.js` every evaluation is attributed to a file that does not exist and
 *  utils.ts reports 0% coverage while this suite stays green. */
let bootCount = 0;
async function freshUtils(): Promise<typeof UtilsModule> {
  vi.resetModules();
  return (await import(/* @vite-ignore */ `./utils.ts?boot=${++bootCount}`)) as typeof UtilsModule;
}

describe("fmtTime: the clock the locale asks for", () => {
  it("renders a 12-hour clock for a locale whose hour cycle is h12", async () => {
    pinLocale("en-US");

    const utils = await freshUtils();

    expect(utils.fmtTime(afternoon)).toBe("03:45 PM");
  });

  it("renders a 24-hour clock for a locale whose hour cycle is h23", async () => {
    pinLocale("de-DE");

    const utils = await freshUtils();

    expect(utils.fmtTime(afternoon)).toBe("15:45");
  });

  it("treats an h11 locale as a 12-hour clock too", async () => {
    // h11 is the other 12-hour cycle (0-11 rather than 1-12); Japanese and
    // Korean calendars use it. Only the second half of the detection's `||`
    // recognises it.
    pinLocale("en-US-u-hc-h11");

    const utils = await freshUtils();

    expect(utils.fmtTime(afternoon)).toBe("03:45 PM");
  });

  it("falls back to a 24-hour clock when the engine cannot report an hour cycle", async () => {
    pinLocaleWithoutResolvedOptions("en-US");

    const utils = await freshUtils();

    expect(utils.fmtTime(afternoon)).toBe("15:45");
  });
});

describe("fmtDateTime: an ISO date beside that clock", () => {
  it("prefixes the ISO date to the 24-hour time", async () => {
    pinLocale("de-DE");

    const utils = await freshUtils();

    expect(utils.fmtDateTime(afternoon)).toBe("2026-01-02 15:45");
  });

  it("prefixes the ISO date to the 12-hour time", async () => {
    pinLocale("en-US");

    const utils = await freshUtils();

    expect(utils.fmtDateTime(afternoon)).toBe("2026-01-02 03:45 PM");
  });
});
