// Public surface of the actions framework. Callers import only from
// here. Internal modules (registry, define, error) re-export through
// this to keep the public/private boundary clear.
//
// Surface is curated: every re-export below has at least one consumer
// outside the framework. Items consumed only inside actions/ are
// imported directly from their defining module by those internal
// callers and not re-exported here, so the public API stays small.
//
// Ported from apps/vibekit/web/static-src/actions/index.ts. Subflux
// has no SSE transport layer, so transportAction is omitted.
// ---------------------------------------------------------------------------

// Action factories.
export { defineAction } from "./define.js";
export { apiAction } from "./api.js";

// Error class for callers throwing structured action errors from within
// run() (toast / retry classification reads status + code from this).
// hasErrorString: predicate for narrowing parsed JSON bodies that may
// have an `{ error: "..." }` shape.
// classifyFetchError: normalise fetch catch-block errors into ActionError
// with canonical code (cancelled/timeout/network); used by action defs
// that call fetch directly outside apiAction.
// retryNetwork: preset retry classifier consumed by action defs as the
// `retryable` field. Custom classifiers compose with it.
export { ActionError, hasErrorString, classifyFetchError, retryNetwork } from "./error.js";

// Registry surface for non-action consumers:
//   - subscribeToActions: per-event observer (e.g. for status banner).
//   - pendingCount: 0-arg returns total in-flight count for the global
//     progress indicator; array form returns count for the named
//     actions (batch-settled detection).
export { subscribe as subscribeToActions, pendingCount } from "./registry.js";

// Loading-state helper: bind a button's disabled + aria-busy state to
// one or more named actions' pending count. Overloaded — pass a single
// name or an array.
export { bindLoadingState } from "./loading.js";

// Cleanup hooks: register raw (non-action) cleanup for fetch
// controllers / timers; the framework auto-installs a beforeunload
// listener that drains everything.
export { registerCleanup } from "./cleanup.js";

// Debounce helper: wrap an action so rapid calls coalesce into a
// single dispatch after a quiet window. Used by typeahead search.
export { debouncedDispatch } from "./debounce.js";
export type { DebouncedDispatch } from "./debounce.js";

// Standard retry config constant: eliminates `retry: { count: 2, delay: 300 }`
// repetition across action definitions.
export { RETRY_STANDARD } from "./types.js";

// One type used by external callers narrowing the registry listener arg.
// The rest of the framework's types stay internal.
export type { ActionErrorLike } from "./types.js";
