// Curated public surface for the actions framework.
//
// Every re-export below has at least one consumer in subflux production
// code. Items consumed only inside actions/ (or with no consumer at all)
// are accessible by importing directly from their defining module — they
// stay out of the curated surface to keep the API small.
//
// Compared with vibekit's surface, subflux drops:
//   - hasErrorString: subflux's api-client doesn't need the predicate
//   - pendingCount: subflux has no global progress indicator
//   - bindLoadingState: subflux's button UIs use custom feedback
//     (busyClick wrapper, action-driven UI) and never bound this way
//   - debouncedDispatch / DebouncedDispatch: subflux has no typeahead
//   - ActionErrorLike: subflux has no listener that narrows the type
//
// Their underlying modules stay (loading.ts, debounce.ts, etc.) so a
// future feature can re-export them without re-porting the file.
//
// Ported from apps/vibekit/web/static-src/actions/index.ts. Subflux has
// no SSE transport layer, so transportAction is omitted.
// ---------------------------------------------------------------------------

// Action factories.
export { defineAction } from "./define.js";
export { apiAction } from "./api.js";

// Error class for callers throwing structured action errors from within
// run() (toast / retry classification reads status + code from this).
// classifyFetchError: normalise fetch catch-block errors into ActionError
// with canonical code (cancelled/timeout/network); used by action defs
// that call fetch directly outside apiAction.
// retryNetwork: preset retry classifier consumed by action defs as the
// `retryable` field. Custom classifiers compose with it.
export { ActionError, classifyFetchError, retryNetwork } from "./error.js";

// Registry surface for non-action consumers:
//   - subscribeToActions: per-event observer used by the boot logger.
export { subscribe as subscribeToActions } from "./registry.js";

// Cleanup hooks: register raw (non-action) cleanup for fetch
// controllers / timers / intervals; the framework auto-installs a
// beforeunload listener that drains everything.
export { registerCleanup } from "./cleanup.js";

// Polling helper: dispatch an action at a fixed interval with
// pause-when-hidden, focus refresh, and backoff-on-error built in.
// Replaces hand-rolled setInterval patterns. Used by app.ts for the
// status poll.
export { pollAction } from "./poll.js";
export type { PollOptions } from "./poll.js";

// Standard retry config constant: eliminates `retry: { count: 2, delay: 300 }`
// repetition across action definitions.
export { RETRY_STANDARD } from "./types.js";
