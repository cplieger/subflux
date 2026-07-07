// DOM utility functions. All UI construction goes through these helpers
// to ensure DOM-safety (no innerHTML) and CSP compliance.

// The CSP-safe element factory now lives in @cplieger/reactive (paired with
// reconcile/patch). Re-export it so existing `import { el } from "./dom.js"`
// call sites are unchanged.
import { el } from "@cplieger/reactive";
import { closeDialog as uipCloseDialog, wireBackdropDismiss } from "@cplieger/ui-primitives/dialog";
import { confirm as uipConfirm } from "@cplieger/ui-primitives/confirm";

export { el };
export type { AttrValue } from "@cplieger/reactive";

export function text(s: string): Text {
  return document.createTextNode(s);
}

export function option(value: string, label: string): HTMLOptionElement {
  const o = document.createElement("option");
  o.value = value;
  o.textContent = label;
  return o;
}

export function icon(name: string): HTMLElement {
  return el("span", { className: `icon icon-${name}` });
}

export function emptyDiv(msg: string): HTMLElement {
  return el("div", { className: "empty" }, msg);
}

export function errDiv(msg: string): HTMLElement {
  return el("div", { className: "empty", "data-status": "err" }, msg);
}

export function pad(n: number): string {
  return String(n).padStart(2, "0");
}

// Fade-out-then-close a native <dialog>, delegating the lifecycle to
// @cplieger/ui-primitives' dialog primitive: it adds the namespaced
// `is-leaving` class, waits for the CSS transition (or a fallback), then
// close()s. The subflux skin maps `dialog.is-leaving` to the exact
// opacity + `translate: 0 0.5rem` exit the old inline-style version produced,
// so the animation is unchanged. Returns a truthy force-close fn when the
// dialog was open (preserved for the one caller — config.ts — that checks it),
// or undefined when already closed.
export function closeDialog(dlg: HTMLDialogElement): (() => void) | undefined {
  if (!dlg.open) {
    return undefined;
  }
  uipCloseDialog(dlg);
  return () => {
    uipCloseDialog(dlg);
  };
}

// Drag-safe backdrop dismissal via the library's wireBackdropDismiss (a press
// must both start and end on the dialog element itself, so a drag-select ending
// on the backdrop doesn't count). Listeners are detached both after the first
// dismissal and on the dialog's `close` event, matching the previous
// AbortController-based cleanup so nothing leaks across reopens.
export function onBackdropClose(dlg: HTMLDialogElement, closeFn: () => void): void {
  let cleanup: () => void = () => undefined;
  cleanup = wireBackdropDismiss(dlg, () => {
    cleanup();
    closeFn();
  });
  dlg.addEventListener("close", () => {
    cleanup();
  });
}

export function dialogHead(title: string | HTMLElement, closeFn: () => void): HTMLElement {
  return el(
    "div",
    { className: "dlg-head" },
    typeof title === "string" ? el("h2", null, title) : title,
    el(
      "button",
      {
        type: "button",
        className: "close-btn ghost",
        "aria-label": "Close",
        onclick: closeFn,
      },
      icon("close"),
    ),
  );
}

// --- Cross-module DOM registry ($) ---
//
// Lazy getters for DOM elements referenced from multiple modules. Each
// getter throws if the element is missing — this fails fast at first
// access instead of returning silently-wrong nulls. Use a local
// `document.getElementById` for elements touched by only one module, or
// for elements that may be absent (dynamically created, removed after
// startup).
//
// Pattern mirrors apps/vibekit/web/static-src/dom.ts `$`.

// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters -- caller specifies T for type narrowing
function req<T extends HTMLElement>(id: string): T {
  const e = document.getElementById(id);
  if (e === null) {
    throw new Error(`Missing element: #${id}`);
  }
  return e as T;
}

export const $ = {
  // Coverage content area: the primary render target for the library,
  // detail, and files views. Touched by coverage.ts, detail.ts,
  // router.ts, files.ts.
  get coverageContent(): HTMLElement {
    return req("coverageContent");
  },

  // Library heading: shown on the coverage list. Mutated by router.ts
  // and coverage.ts.
  get libHeading(): HTMLElement {
    return req("lib-heading");
  },

  // Coverage panel root: toggled by router.ts, referenced for filter
  // controls from coverage.ts.
  get coveragePanel(): HTMLElement {
    return req("coveragePanel");
  },

  // History panel root: toggled by router.ts.
  get historyPanel(): HTMLElement {
    return req("historyPanel");
  },

  // History navigation button in the header. Wired by app.ts (click),
  // toggled active/inactive by router.ts.
  get historyBtn(): HTMLElement {
    return req("historyBtn");
  },

  // Config dialog close button. Wired by app.ts (click listener),
  // show/hide toggled by config.ts based on unconfigured state.
  get configClose(): HTMLElement {
    return req("configClose");
  },

  // Status popup container. Rendered into by status.ts, wired to the
  // toggle event by app.ts.
  get statusPopup(): HTMLElement {
    return req("statusPopup");
  },

  // Status button in the header. Updated by status.ts; the anchor for the
  // status popup (a @cplieger/ui-primitives popover — see status.ts
  // initStatusPopover).
  get statusBtn(): HTMLElement {
    return req("statusBtn");
  },
};

// Note on "startup-only" elements (themeBtn, configBtn, userBtn):
// user-menu.ts removes themeBtn and configBtn from the DOM after
// initialization because their controls move into the user-menu
// popover. A $ getter would throw on post-removal access; these stay
// as raw document.getElementById lookups with the null-guard pattern.

export function dialog(id: string): HTMLDialogElement {
  return document.getElementById(id) as HTMLDialogElement;
}

export function input(id: string): HTMLInputElement {
  return document.getElementById(id) as HTMLInputElement;
}

export function select(id: string): HTMLSelectElement {
  return document.getElementById(id) as HTMLSelectElement;
}

// Promise-based confirmation, delegating to @cplieger/ui-primitives' confirm
// primitive (its own reused, lazily-created <dialog class="uip-confirm">).
// subflux always supplies a title; `confirmLabel` maps to the OK button.
// Cancel / Escape / backdrop-click all resolve false. The skin styles the
// `.uip-confirm` dialog (which inherits subflux's base dialog chrome) + its
// parts to match the previous look. Signature kept as (title, message, label)
// so the files.ts / security.ts call sites are unchanged.
export function confirm(title: string, message: string, confirmLabel?: string): Promise<boolean> {
  return uipConfirm(message, confirmLabel !== undefined ? { title, confirmLabel } : { title });
}

// Insert an element into the card header, before the arr link if present.
export function insertNavButton(btn: HTMLElement): void {
  const headerEl = document.querySelector("#coveragePanel .card-head");
  if (!headerEl) {
    return;
  }
  const arrEl = headerEl.querySelector('[data-nav="arr"]');
  if (arrEl) {
    headerEl.insertBefore(btn, arrEl);
  } else {
    headerEl.appendChild(btn);
  }
}
