// pollAction: repeat-an-action-on-interval primitive that integrates with
// the framework lifecycle. Replaces hand-rolled `setInterval(() => fn(),
// 5000)` patterns with a single helper that adds:
//
//   - Pause when document.hidden = true (saves battery on background tabs)
//   - Refresh on window focus (instant freshness when the user returns)
//   - Exponential backoff on consecutive failures (don't hammer dead servers)
//   - Auto cleanup via registerCleanup so beforeunload drains the timer
//   - Registry visibility for every poll dispatch (failures show in console-log)
//
// Use cases:
//   - Periodic refresh of cheap reads (git-badge status, app-status panel)
//   - OAuth device-flow polling with rate-limit-aware backoff
//   - Library-list reconciliation when SSE isn't available
//
// Not for:
//   - UI ticks that don't hit the network (use raw setInterval — the
//     framework can't add value without a dispatched action)
//   - One-shot delayed calls (use setTimeout directly)
// ---------------------------------------------------------------------------

import { registerCleanup } from "./cleanup.js";
import type { Action } from "./types.js";

export interface PollOptions<TResult = unknown> {
  /** Quiet window between polls in ms. After a successful dispatch,
   *  the next poll fires at this interval. */
  readonly interval: number;
  /** When true (default), polls pause while `document.hidden === true`.
   *  Resumes at the next visibility change to visible. */
  readonly pauseWhenHidden?: boolean;
  /** When true (default), an extra dispatch fires on window `focus` to
   *  refresh stale data immediately when the tab regains focus. Skipped
   *  if a dispatch is currently in-flight. */
  readonly refreshOnFocus?: boolean;
  /** Exponential backoff on consecutive failures. After N failed polls,
   *  the next poll fires at `min(interval * factor^N, max)`. Reset to
   *  the base interval on the next success. When undefined, polls fire
   *  at the base interval regardless of failure history. */
  readonly backoffOnError?: { readonly factor: number; readonly max: number };
  /** Called with the action's result after each successful dispatch.
   *  Use for UI projection — derive view state from the result and
   *  apply to the DOM. Errors thrown by the callback are caught and
   *  logged (never break the poll loop). */
  readonly onSuccess?: (result: TResult) => void;
}

/**
 * Repeatedly dispatch `action(args)` at the given interval. Returns a
 * `stop()` function that cancels the next scheduled poll and removes
 * the visibility/focus listeners.
 *
 * Lifecycle:
 *   1. Immediate dispatch on start.
 *   2. On dispatch settle:
 *      - success: reset failure count, schedule next at base interval
 *      - error (dispatch resolves to null): increment failure count,
 *        schedule next at `min(interval * factor^failures, max)` if
 *        backoffOnError is set; otherwise schedule at base interval
 *   3. On `visibilitychange` to hidden + pauseWhenHidden: clear the
 *      timer, set paused=true. On change back to visible: clear paused,
 *      dispatch immediately, schedule next.
 *   4. On `focus` + refreshOnFocus + not currently dispatching: dispatch
 *      immediately, reset the timer.
 *   5. On stop() OR on `beforeunload` via registerCleanup: clear timer,
 *      remove listeners.
 *
 * The action's own dedupe/scope/retry semantics still apply per
 * dispatch — pollAction doesn't add retry logic; it relies on the
 * action's `retry` config for transient-failure recovery within a
 * single poll cycle. backoffOnError handles failure across cycles.
 *
 * @param action - Action to poll.
 * @param args - Args passed to each dispatch (same value every cycle;
 *   pollAction is for refresh patterns, not arg-varying loops).
 * @param opts - interval + optional pauseWhenHidden / refreshOnFocus /
 *   backoffOnError flags.
 * @returns A stop() function. Idempotent — safe to call multiple times.
 */
export function pollAction<TArgs, TResult>(
  action: Action<TArgs, TResult>,
  args: TArgs,
  opts: PollOptions<TResult>,
): () => void {
  const pauseWhenHidden = opts.pauseWhenHidden !== false;
  const refreshOnFocus = opts.refreshOnFocus !== false;
  const baseInterval = opts.interval;
  const backoff = opts.backoffOnError;
  const onSuccess = opts.onSuccess;

  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let paused = false;
  let inFlight = false;
  let failures = 0;

  /** Compute the next interval, applying backoff on consecutive failures. */
  function nextDelay(): number {
    if (backoff === undefined || failures === 0) {
      return baseInterval;
    }
    return Math.min(baseInterval * Math.pow(backoff.factor, failures), backoff.max);
  }

  /** Schedule the next poll, unless stopped or paused. */
  function schedule(): void {
    if (stopped || paused) {
      return;
    }
    if (timer !== undefined) {
      clearTimeout(timer);
    }
    timer = setTimeout(() => {
      void tick();
    }, nextDelay());
  }

  /** One poll cycle: dispatch, track success/failure, schedule next. */
  async function tick(): Promise<void> {
    if (stopped || paused) {
      return;
    }
    if (inFlight) {
      return;
    } // Defensive: focus-trigger collapsed onto in-flight
    inFlight = true;
    try {
      const result = await action.dispatch(args);
      if (stopped) {
        return;
      }
      // dispatch resolves to null on error/cancel and to T on success.
      if (result === null) {
        failures += 1;
      } else {
        failures = 0;
        if (onSuccess !== undefined) {
          try {
            onSuccess(result);
          } catch (e) {
            console.error("[pollAction] onSuccess threw", e);
          }
        }
      }
    } finally {
      inFlight = false;
    }
    schedule();
  }

  /** visibilitychange handler: pause/resume the poll loop. */
  const onVisibility = (): void => {
    if (typeof document === "undefined") {
      return;
    }
    if (document.hidden) {
      paused = true;
      if (timer !== undefined) {
        clearTimeout(timer);
        timer = undefined;
      }
    } else if (paused) {
      paused = false;
      void tick(); // immediate refresh on return
    }
  };

  /** focus handler: refresh on tab regain (skip if already dispatching). */
  const onFocus = (): void => {
    if (stopped || paused || inFlight) {
      return;
    }
    void tick();
  };

  /** Stop the poll. Idempotent. */
  function stop(): void {
    if (stopped) {
      return;
    }
    stopped = true;
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
    if (pauseWhenHidden && typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", onVisibility);
    }
    if (refreshOnFocus && typeof window !== "undefined") {
      window.removeEventListener("focus", onFocus);
    }
  }

  // Wire listeners.
  if (pauseWhenHidden && typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onVisibility);
    // Initialize paused state from current visibility — if the page
    // loaded while hidden, don't fire the first poll until it's visible.
    if (document.hidden) {
      paused = true;
    }
  }
  if (refreshOnFocus && typeof window !== "undefined") {
    window.addEventListener("focus", onFocus);
  }

  // Auto-cleanup on framework beforeunload drain. The hook is
  // idempotent (stop() short-circuits on second call), so explicit
  // stop() callers don't conflict with the registered drain.
  registerCleanup(stop);

  // Initial dispatch (skipped when starting in hidden tab).
  if (!paused) {
    void tick();
  }

  return stop;
}
