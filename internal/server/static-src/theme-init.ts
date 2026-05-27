// ---------------------------------------------------------------------------
// Boot-time theme application. Sets [data-theme] on <html> SYNCHRONOUSLY
// before stylesheets parse, to prevent flash-of-wrong-theme on the splash
// screen (both the unauthenticated login page and the authenticated app).
// Loaded as a classic blocking <script src="/theme-init.js"> in <head> —
// NOT type="module" (modules are deferred and would defeat the purpose).
//
// Compiled to static/theme-init.js by tsconfig.json. tsgo emits a
// non-module script (with "use strict" and no module wrapper) when the
// file has no top-level imports or exports.
//
// Reads the same per-device state that theme.ts writes:
//   key:    subflux-theme
//   value:  "dark" | "light" | null
//
// If the key/values change in theme.ts they MUST change here too. The
// runtime applyTheme() in theme.ts re-sets the same attribute during
// init(), so this file is a paint-time hint — but the two must agree to
// avoid a re-flip.
//
// Covered by CSP `default-src 'self'` (no explicit script-src directive
// in middleware; same-origin scripts pass through default-src). No hash
// maintenance required.
// ---------------------------------------------------------------------------

(function () {
  try {
    const raw = localStorage.getItem("subflux-theme");
    const stored: "dark" | "light" | null = raw === "dark" || raw === "light" ? raw : null;
    const theme: "dark" | "light" =
      stored ?? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    document.documentElement.setAttribute("data-theme", theme);
  } catch {
    document.documentElement.setAttribute("data-theme", "dark");
  }
})();
