// Theme management: dark/light with system-preference fallback.
//
// Backed by @cplieger/ui-primitives' theme primitive. The subflux-facing API
// (init / cycle) and the binary dark <-> light toggle are preserved exactly;
// the library controller now owns persistence + live OS-preference following.
//
// Storage key + values are unchanged ("subflux-theme": "dark" | "light", or
// absent = follow the system preference), so the paint-time anti-FOUC script in
// theme-init.ts stays byte-compatible. The library treats an absent/unknown
// stored value as "system" (resolved live via matchMedia), matching the
// previous null -> system behaviour; the binary toggle below only ever pins
// "dark"/"light" (never "system"), so it can never cycle into a third state.

import { createTheme, type ThemeController } from "@cplieger/ui-primitives/theme";

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
  const t = ensureController();
  // Binary toggle: flip the currently-resolved theme and pin it. Matches the
  // previous dark <-> light behaviour (never enters the library's "system"
  // leg of its default light -> dark -> system cycle).
  t.set(t.resolved() === "dark" ? "light" : "dark");
}
