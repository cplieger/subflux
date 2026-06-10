// DOM utility functions. All UI construction goes through these helpers
// to ensure DOM-safety (no innerHTML) and CSP compliance.

// The CSP-safe element factory now lives in @cplieger/reactive (paired with
// reconcile/patch). Re-export it so existing `import { el } from "./dom.js"`
// call sites are unchanged.
import { el } from "@cplieger/reactive";

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

export function closeDialog(dlg: HTMLDialogElement): (() => void) | undefined {
  if (!dlg.open) {
    return;
  }
  dlg.style.opacity = "0";
  dlg.style.translate = "0 0.5rem";
  let finished = false;
  const finish = () => {
    if (finished) {
      return;
    }
    finished = true;
    dlg.style.removeProperty("opacity");
    dlg.style.removeProperty("translate");
    dlg.close();
  };
  dlg.addEventListener(
    "transitionend",
    () => {
      finish();
    },
    { once: true },
  );
  setTimeout(() => {
    if (dlg.open) {
      finish();
    }
  }, 250);
  return finish;
}

export function onBackdropClose(dlg: HTMLDialogElement, closeFn: () => void): void {
  const ac = new AbortController();
  let downOnBackdrop = false;
  dlg.addEventListener(
    "mousedown",
    (e: MouseEvent) => {
      downOnBackdrop = e.target === dlg;
    },
    { signal: ac.signal },
  );
  dlg.addEventListener(
    "click",
    (e: MouseEvent) => {
      if (e.target === dlg && downOnBackdrop) {
        ac.abort();
        closeFn();
      }
    },
    { signal: ac.signal },
  );
  dlg.addEventListener(
    "close",
    () => {
      ac.abort();
    },
    { once: true },
  );
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

  // Status button in the header. Updated by status.ts, anchor for the
  // popup opened by the browser-native popover API.
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

export function confirm(title: string, message: string, confirmLabel?: string): Promise<boolean> {
  return new Promise((resolve) => {
    const dlg = dialog("confirmDialog");
    let resolved = false;
    const settle = (value: boolean) => {
      if (resolved) {
        return;
      }
      resolved = true;
      closeDialog(dlg);
      resolve(value);
    };
    const closeFn = () => {
      settle(false);
    };
    const header = dialogHead(title, closeFn);

    const body = el("div", { className: "dlg-body" }, el("p", null, message));

    const footer = el(
      "div",
      { className: "dlg-foot" },
      el(
        "button",
        {
          type: "button",
          onclick: () => {
            settle(true);
          },
        },
        confirmLabel ?? "Confirm",
      ),
      el(
        "button",
        {
          type: "button",
          className: "ghost",
          onclick: closeFn,
        },
        "Cancel",
      ),
    );

    dlg.replaceChildren(header, body, footer);
    dlg.showModal();
    onBackdropClose(dlg, closeFn);
    // Handle native Escape: browser fires cancel→close without calling closeFn.
    dlg.addEventListener("close", closeFn, { once: true });
  });
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
