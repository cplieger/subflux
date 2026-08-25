// Named constants for timeout and interval values used across the UI.

export const STATUS_POLL_MS = 5_000;
export const SEARCH_TIMEOUT_MS = 30_000;
export const DOWNLOAD_POLL_MS = 2_000;
export const DOWNLOAD_DEADLINE_MS = 5 * 60_000;
export const SSE_RECONNECT_MS = 5_000;
export const SSE_MAX_RECONNECT_MS = 60_000;
export const YAML_TIMEOUT_MS = 15_000;
export const ROUTE_TRANSITION_MS = 200;

// Default subtitle variant value — the sentinel used when no variant is specified.
export const DEFAULT_VARIANT = "standard" as const;

// Coverage source string for subtitles embedded in the video container
// (mirrors Go's subflux.SourceEmbedded; persisted in subtitle_files rows).
export const EMBEDDED_PROVIDER = "embedded" as const;

// Concurrency limit for season audio sync (parallel requests).
export const SEASON_SYNC_CONCURRENCY = 3;

// Debounce delay for SSE reconnect on visibility change.
export const VISIBILITY_DEBOUNCE_MS = 2_000;

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
