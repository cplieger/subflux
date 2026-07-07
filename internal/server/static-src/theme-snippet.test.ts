// Drift guard: the anti-FOUC theme-init script is inlined into the <head> of
// index.html and login.html so the correct theme paints before stylesheets
// load. Its bytes MUST equal @cplieger/ui-primitives' themeInitSnippet output
// verbatim, because:
//   1. the Go CSP builder (internal/server/security.go) hashes the exact inline
//      bytes into script-src, so any drift silently blocks the script (FOUC), and
//   2. the storage key must match the one createTheme uses in theme.ts
//      ("subflux-theme"), or paint-time and runtime resolve different themes.
// If the library snippet changes upstream, this test fails loudly rather than
// letting the CSP hash or the theme key drift out of sync.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { themeInitSnippet } from "@cplieger/ui-primitives/theme";

// The storage key createTheme persists under (see theme.ts THEME_KEY).
const THEME_KEY = "subflux-theme";

// Mirrors internal/server/security.go's themeInitRe. Non-greedy so it stops at
// the theme-init script's own closing tag.
const themeInitRe = /<script data-theme-init>([\s\S]*?)<\/script>/;

function extractThemeInit(relPath: string): string {
  const html = readFileSync(new URL(relPath, import.meta.url), "utf8");
  const body = themeInitRe.exec(html)?.[1];
  if (body === undefined) {
    throw new Error(`no <script data-theme-init> block in ${relPath}`);
  }
  return body;
}

describe("theme-init anti-FOUC snippet (drift guard)", () => {
  const expected = themeInitSnippet(THEME_KEY);

  it("index.html inline bytes equal themeInitSnippet('subflux-theme')", () => {
    expect(extractThemeInit("../static/index.html")).toBe(expected);
  });

  it("login.html inline bytes equal themeInitSnippet('subflux-theme')", () => {
    expect(extractThemeInit("../static/login.html")).toBe(expected);
  });
});
