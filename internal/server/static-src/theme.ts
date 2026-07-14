// Theme management: light / dark / system three-state cycle.
//
// Backed by @cplieger/ui-primitives' theme primitive, using its native
// light -> dark -> system -> light cycle. "system" resolves live via
// matchMedia and follows the OS while selected, so a user who cycles back to
// it regains OS-following without clearing storage by hand (the old binary
// toggle pinned dark/light forever and made "system" unreachable).
//
// Storage key + values are unchanged ("subflux-theme": "light" | "dark" |
// "system", or absent = system), so the paint-time anti-FOUC script in
// theme-init.ts stays byte-compatible.

import { createTheme, type ThemeChoice, type ThemeController } from "@cplieger/ui-primitives/theme";

const THEME_KEY = "subflux-theme";

let controller: ThemeController | null = null;

function ensureController(): ThemeController {
  controller ??= createTheme({ storageKey: THEME_KEY });
  return controller;
}

export function init(): void {
  // Applies the resolved theme immediately (sets [data-theme] on <html>) and
  // follows the OS live while the stored choice is "system".
  ensureController();
}

export function cycle(): void {
  ensureController().cycle();
}

// choice returns the STORED preference ("light" | "dark" | "system") — the
// user menu labels the cycle button with what clicking it switches TO.
export function choice(): ThemeChoice {
  return ensureController().get();
}
