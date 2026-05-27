// Named constants for timeout and interval values used across the UI.

export const STATUS_POLL_MS = 5_000;
export const SEARCH_TIMEOUT_MS = 30_000;
export const DOWNLOAD_POLL_MS = 2_000;
export const DOWNLOAD_DEADLINE_MS = 5 * 60_000;
export const SSE_RECONNECT_MS = 5_000;
export const SSE_MAX_RECONNECT_MS = 60_000;
export const YAML_TIMEOUT_MS = 15_000;
export const ROUTE_TRANSITION_MS = 200;
// Security: how long a user can perform sensitive operations without re-authenticating.
export const REAUTH_WINDOW_MS = 5 * 60 * 1_000;

// Default subtitle variant value — the sentinel used when no variant is specified.
export const DEFAULT_VARIANT = "standard" as const;

// Built-in embedded subtitle provider name.
export const EMBEDDED_PROVIDER = "embedded" as const;

// Concurrency limit for season audio sync (parallel requests).
export const SEASON_SYNC_CONCURRENCY = 3;

// Debounce delay for SSE reconnect on visibility change.
export const VISIBILITY_DEBOUNCE_MS = 2_000;

// Subtitle variant options — single source of truth for config, wizard, and badge logic.
export const SUBTITLE_VARIANTS = [
  { value: DEFAULT_VARIANT, label: "standard" },
  { value: "forced", label: "forced" },
  { value: "hi", label: "hearing impaired" },
] as const;
