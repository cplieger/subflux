// Notification system: stacked, auto-dismissing toasts.
//
// Backed by @cplieger/ui-primitives' toast primitive. The subflux-facing API
// (success / error / info) is preserved verbatim so call sites and the actions
// framework wiring (actions-boot.ts) don't churn; internally each delegates to
// a library toaster. Durations match the previous hand-rolled behaviour:
// success 4s, info 3s, error sticky-until-clicked. Error toasts can carry a
// Retry button (passed by the actions framework when an action def sets
// `retryable`). Screen-reader announcement is handled by the primitive's shared
// live region (the `announce` primitive), replacing the old aria-live stack.

import { createToaster } from "@cplieger/ui-primitives/toast";

// Auto-dismiss windows (ms). Errors are sticky (0) — handled by the library's
// per-level default, so `error()` passes no duration.
const SUCCESS_DURATION_MS = 4000;
const INFO_DURATION_MS = 3000;

// One app-lifetime toaster. maxVisible: 3 matches the previous stack cap; the
// overflow queue + Escape-to-dismiss-newest are the library's defaults.
const toaster = createToaster({ maxVisible: 3 });

export function success(message: string): void {
  toaster.show(message, { level: "success", duration: SUCCESS_DURATION_MS });
}

/** Error toast. Sticky until dismissed. Optional retry callback renders a Retry
 *  button inside the toast — used by the actions framework when an action def
 *  sets `retryable`. */
export function error(message: string, retry?: { onClick: () => void }): void {
  toaster.error(message, retry);
}

export function info(message: string): void {
  toaster.show(message, { level: "info", duration: INFO_DURATION_MS });
}
