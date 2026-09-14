// Named constants for timeout and interval values used across the UI.

// Degraded-mode status poll cadence (E2): ONLY while the SSE stream is DOWN
// (the library's backoff and offline states, and the connect a backoff
// retries), per tab: the worker fans its state changes to every tab.
// While connected, events feed status and STATUS_RECONCILE_MS is the floor.
export const SSE_DOWN_POLL_MS = 5_000;
export const STATUS_RECONCILE_MS = 60_000;
// Per-root coverage-heal window: events arriving inside one window share one
// trailing flush (one summary GET per root).
export const SUMMARY_COALESCE_MS = 300;
// Best-effort belt for twice-failed heal roots; dropped roots converge via the
// next event, replay, or transaction.
export const DIRTY_ROOT_CAP = 64;
export const SEARCH_TIMEOUT_MS = 30_000;
export const YAML_TIMEOUT_MS = 15_000;
export const ROUTE_TRANSITION_MS = 200;
// History depth cap (E4): the server clamps ?limit at 10 000 SILENTLY, so a
// depth-preserving reload past it could not detect the truncation — the
// client never asks for more. "Show more" hides at the cap while hasMore
// stays true internally.
export const HISTORY_DEPTH_CAP = 10_000;

// Default subtitle variant value — the sentinel used when no variant is specified.
export const DEFAULT_VARIANT = "standard" as const;

// Coverage source string for subtitles embedded in the video container
// (mirrors Go's subflux.SourceEmbedded; persisted in subtitle_files rows).
export const EMBEDDED_PROVIDER = "embedded" as const;

// The setup wizard's own address. The wizard is a page-state of login.html
// rather than a document of its own, so without an address a reload mid-setup
// resolves to whichever bundle the session state implies — and once the admin
// exists (creating them issues a session) that is the app, whose unconfigured
// settings dialog is not where the operator left off. wizard.ts replaces the
// URL with this on entry, login.ts re-enters from it, and finishing navigates
// away to "/". Mirrored server-side as setupPath in internal/server/server.go,
// which must serve login.html here for either auth state.
export const SETUP_PATH = "/setup";

// Subtitle variant options — single source of truth for config, wizard, and badge logic.
export const SUBTITLE_VARIANTS = [
  { value: DEFAULT_VARIANT, label: "standard" },
  { value: "forced", label: "forced" },
  { value: "hi", label: "hearing impaired" },
] as const;
