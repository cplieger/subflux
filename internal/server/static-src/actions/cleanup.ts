// Global cleanup: cancel all in-flight actions + run registered
// cleanup hooks. Wired to window.beforeunload so navigation away
// from the page (or tab close) aborts everything cleanly.
//
// Cleanup hooks must be idempotent. Cancellation is allowed to fire
// multiple times (e.g., cancelled navigation followed by confirmed
// navigation). Aborting an already-aborted controller is a no-op by spec;
// ensure your hook has equivalent semantics.
//
// Two surfaces:
//
//   1. Actions registered via defineAction are auto-tracked. Their
//      .cancel() is called on global cleanup.
//
//   2. Modules with raw fetch controllers (transport.ts inflight,
//      msgControllers in store.ts, browserFetchHolder in files.ts,
//      pollGitHubDevice timer chain) call registerCleanup(fn) at
//      init time. The fn is invoked on global cleanup.
// ---------------------------------------------------------------------------

import type { Action } from "./types.js";

/** Minimal shape needed for cleanup — avoids variance-unsafe casts.
 *  cancel() here means "abort in-flight requests for this action",
 *  not "perform a domain-level cancel operation". */
interface Cancellable {
  readonly name: string;
  cancel(): void;
}

const trackedActions = new Set<Cancellable>();
const cleanupHooks = new Set<() => void>();
let beforeunloadInstalled = false;

/** Internal: register an Action so cancelAllPending() can iterate it.
 *  Called from defineAction(); not part of the public API. */
export function _registerAction<TArgs, TResult>(action: Action<TArgs, TResult>): void {
  trackedActions.add(action);
  installBeforeunloadOnce();
}

/**
 * Register a cleanup function to run on page unload (or test invoke).
 * Use this for raw fetch controllers, timer chains, polling loops,
 * or any in-flight work outside the action framework that should
 * abort on navigation.
 *
 * @param fn - Idempotent teardown callback (abort controllers, clear timers).
 * @returns An unregister function; call it when the module re-initializes
 *   to detach the stale hook.
 */
export function registerCleanup(fn: () => void): () => void {
  cleanupHooks.add(fn);
  installBeforeunloadOnce();
  return () => cleanupHooks.delete(fn);
}

/** Cancel every in-flight action + run every cleanup hook. Errors from
 *  individual hooks are caught + logged; one bad hook does not stop
 *  the rest from running. Internal — invoked by the beforeunload handler
 *  installed via installBeforeunloadOnce(). */
function cancelAllPending(): void {
  // trackedActions is safe to iterate directly: action.cancel() does
  // not modify the Set (only _registerAction adds, _resetForTest clears).
  for (const action of trackedActions) {
    try {
      action.cancel();
    } catch (e) {
      console.error(`[actions] cancel for ${action.name} threw`, e);
    }
  }
  // Snapshot before iterating: cleanup hooks may register/unregister
  // others as they run (e.g. during test resets).
  for (const fn of [...cleanupHooks]) {
    try {
      fn();
    } catch (e) {
      console.error("[actions] cleanup hook threw", e);
    }
  }
}

/** Install the beforeunload listener exactly once. Called lazily on
 *  first action registration or cleanup hook registration. Uses
 *  beforeunload (not pagehide) so abort fires before navigation. */
function installBeforeunloadOnce(): void {
  if (beforeunloadInstalled) {
    return;
  }
  beforeunloadInstalled = true;
  if (typeof window !== "undefined") {
    // Use beforeunload (vs pagehide) because we want to fire BEFORE
    // the navigation begins so in-flight requests can actually be
    // aborted client-side. pagehide is fired too late on most browsers.
    window.addEventListener("beforeunload", cancelAllPending);
  }
}

/** Test-only: invoke the same cleanup logic that beforeunload runs. */
export function _cancelAllForTest(): void {
  cancelAllPending();
}

/** Test-only: clear both registries + uninstall the listener. */
export function _resetForTest(): void {
  trackedActions.clear();
  cleanupHooks.clear();
  if (beforeunloadInstalled && typeof window !== "undefined") {
    window.removeEventListener("beforeunload", cancelAllPending);
  }
  beforeunloadInstalled = false;
}
