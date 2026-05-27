// bindLoadingState: bind a button or input element's disabled / aria-busy
// state to one or more named actions' pending count.
//
// Usage:
//
//   const unbind = bindLoadingState("git.commit", commitBtn);
//   const unbind = bindLoadingState(["git.push", "git.pull"], pushPullBtn);
//   // ...later, on teardown:
//   unbind();
//
// While ANY of the named actions is pending, the element gets:
//   - disabled = true
//   - aria-busy = "true"  (omit by passing { ariaBusy: false })
//   - optionally an extra CSS class via { pendingClass: "btn-loading" }
//
// On every state transition for any of the named actions, the bound
// element is re-evaluated. Multiple in-flight instances of the same
// action keep the element disabled until ALL complete.
//
// Returns an unsubscribe function. Call it from the view's teardown
// hook to stop receiving updates and avoid leaking listeners.
// ---------------------------------------------------------------------------

import { subscribeByName, isPending, pendingCount } from "./registry.js";

/** Element types that have a `.disabled` writable boolean. */
type DisableableElement =
  | HTMLButtonElement
  | HTMLInputElement
  | HTMLSelectElement
  | HTMLTextAreaElement;

interface BindLoadingOptions {
  /** When true (default), set `aria-busy="true"` while pending. */
  ariaBusy?: boolean;
  /** When true, don't manage aria-busy at all — lets external code own
   *  the attribute. Overrides `ariaBusy`. Default: false. */
  preserveAriaBusy?: boolean;
  /** CSS class to add while pending (in addition to the disabled prop). */
  pendingClass?: string;
  /** When true, also OR the element's existing disabled state with the
   *  pending state (i.e. element stays disabled if it was disabled for
   *  another reason). Default: false (the helper takes full ownership
   *  of disabled). Use this when the element has a separate
   *  validation-driven disabled state. */
  preserveDisabled?: boolean;
  /** Reactive disabled predicate. When provided, the element's disabled
   *  state is `pending || disabledFn()` on every transition. Solves the
   *  `preserveDisabled` limitation where external mutations during the
   *  pending phase are overwritten — the predicate is always re-evaluated.
   *  Takes precedence over `preserveDisabled` when both are set. */
  disabledFn?: () => boolean;
}

/**
 * Bind a button/input element's disabled / aria-busy state to one or
 * more named actions. The element is disabled while ANY of the named
 * actions is pending.
 *
 * @param actionName - A single action name (e.g. "git.commit") or an
 *   array of names (e.g. `["git.push", "git.pull"]`). The element is
 *   disabled while ANY of the names has at least one pending instance.
 * @param el - Button, input, select, or textarea whose `disabled` property
 *   will be toggled while pending.
 * @param opts - Optional configuration for aria-busy, CSS class, and
 *   disabled-state preservation.
 * @returns An unsubscribe function that restores the element and detaches
 *   the registry listener.
 *
 * **Limitation (preserveDisabled):** External mutations to `el.disabled`
 * DURING the pending phase are overwritten on completion. Set the desired
 * disabled state AFTER the action completes if needed.
 */
export function bindLoadingState(
  actionName: string | readonly string[],
  el: DisableableElement,
  opts: BindLoadingOptions = {},
): () => void {
  // Normalise to an array of names. Single-string callers and array
  // callers go through the same code path so the binding semantics
  // (focus restore, disposal on DOM removal, dedup of base-disabled
  // capture) are identical.
  const names: readonly string[] = typeof actionName === "string" ? [actionName] : actionName;
  if (names.length === 0) {
    return () => {
      /* noop */
    };
  }

  const {
    ariaBusy = true,
    preserveAriaBusy = false,
    pendingClass,
    preserveDisabled = false,
    disabledFn,
  } = opts;
  const manageAriaBusy = ariaBusy && !preserveAriaBusy;
  // Track pending transitions to snapshot disabled state lazily —
  // avoids stale bind-time capture when external code mutates disabled.
  let wasPending = false;
  let baseDisabled = el.disabled;
  let hadFocus = false;
  let disposed = false;
  let wasConnected = el.isConnected;

  /** Resolve the base disabled state: disabledFn takes precedence over
   *  the snapshot-based preserveDisabled approach. If disabledFn throws,
   *  fall back to false so the element is at least re-enabled (safe
   *  default) rather than stuck in the pending visual state. */
  const resolveBase = (): boolean => {
    if (disabledFn !== undefined) {
      try {
        return disabledFn();
      } catch {
        return false;
      }
    }
    return preserveDisabled ? baseDisabled : false;
  };

  /** Restore element to idle state. */
  const setIdle = (): void => {
    el.disabled = resolveBase();
    if (manageAriaBusy) {
      el.removeAttribute("aria-busy");
    }
    if (pendingClass) {
      el.classList.remove(pendingClass);
    }
    // Restore focus only if the user hasn't explicitly moved focus
    // elsewhere during the pending phase. When a button is disabled,
    // focus moves to <body>; if it's still there, the user didn't
    // intentionally navigate away, so restoring is correct.
    if (hadFocus && el.isConnected && !el.disabled) {
      const active = document.activeElement;
      if (active === null || active === document.body) {
        el.focus();
      }
    }
    hadFocus = false;
  };

  /** Read the current pending state across all bound names. Single-name
   *  case uses the cheaper isPending() (one Map lookup); multi-name uses
   *  pendingCount(names) > 0. */
  const readPending = (): boolean =>
    names.length === 1 ? isPending(names[0] ?? "") : pendingCount(names) > 0;

  let unsubs!: (() => void)[]; // eslint-disable-line prefer-const -- assigned after apply() closure captures it

  const apply = (): void => {
    if (disposed) {
      return;
    }
    // Auto-dispose if the element was removed from the DOM — prevents
    // stale bindings and keeps the closure from leaking the element.
    if (wasConnected && !el.isConnected) {
      disposed = true;
      for (const u of unsubs) {
        u();
      }
      return;
    }
    if (el.isConnected) {
      wasConnected = true;
    }
    const pending = readPending();
    // Snapshot the live disabled state on the pending edge (before we
    // clobber it) so we can restore it when the action completes.
    if (pending && !wasPending) {
      baseDisabled = el.disabled;
      hadFocus = document.activeElement === el;
    }
    if (pending) {
      el.disabled = true;
      if (manageAriaBusy) {
        el.setAttribute("aria-busy", "true");
      }
      if (pendingClass) {
        el.classList.add(pendingClass);
      }
    } else if (wasPending) {
      // Transition pending→idle: restore element state.
      setIdle();
    }
    wasPending = pending;
  };

  /** Restore element state as if the action completed. */
  const restore = (): void => {
    if (wasPending) {
      setIdle();
      wasPending = false;
    }
  };

  // Initial paint.
  apply();

  // Re-evaluate on every transition for any of the named actions.
  // Per-name subscription to avoid O(n) fan-out across all bindings.
  unsubs = names.map((name) => subscribeByName(name, apply));

  // Unsubscribe restores element state if still mid-pending.
  return () => {
    disposed = true;
    restore();
    for (const u of unsubs) {
      u();
    }
  };
}
