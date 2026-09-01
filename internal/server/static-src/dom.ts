// DOM utility functions. All UI construction goes through these helpers
// to ensure DOM-safety (no innerHTML) and CSP compliance.

// Re-exported from @cplieger/reactive so existing `./dom.js` imports of `el`
// are unchanged.
import { el } from "@cplieger/reactive";
import { wireBackdropDismiss } from "@cplieger/ui-primitives/dialog";
import { ask } from "@cplieger/ui-primitives/ask";

export { el };

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

/** Sets `data-tip` and appends a decorative `aria-hidden` "?" marker; the
 *  label's accessible name is unaffected. Shared by the settings dialog and
 *  setup wizard for the schema `help` field. Styling in 16-uip-skin.css. */
export function withHelp<T extends HTMLElement>(label: T, tip: string | undefined): T {
  if (tip) {
    label.setAttribute("data-tip", tip);
    label.appendChild(el("sup", { className: "help-mark", "aria-hidden": "true" }, "?"));
  }
  return label;
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

// Re-exported (not wrapped) so dom.ts stays the facade call sites import from.
export { closeDialog } from "@cplieger/ui-primitives/dialog";

// Wire ONCE per dialog at boot; the `dismissed` latch (re-armed by the
// dialog's `close` event) caps closeFn at one call per open, since a second
// press during the close fade must not run history.back() twice.
export function onBackdropClose(dlg: HTMLDialogElement, closeFn: () => void): void {
  let dismissed = false;
  wireBackdropDismiss(dlg, () => {
    if (dismissed) {
      return;
    }
    dismissed = true;
    closeFn();
  });
  dlg.addEventListener("close", () => {
    dismissed = false;
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

// Cross-module DOM registry: lazy getters that throw if the element is
// missing (fail fast instead of a silently-wrong null). Single-module or
// possibly-absent elements stay raw `document.getElementById` lookups.

// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters -- caller specifies T for type narrowing
function req<T extends HTMLElement>(id: string): T {
  const e = document.getElementById(id);
  if (e === null) {
    throw new Error(`Missing element: #${id}`);
  }
  return e as T;
}

export const $ = {
  get coverageContent(): HTMLElement {
    return req("coverageContent");
  },
  get libHeading(): HTMLElement {
    return req("lib-heading");
  },
  get coveragePanel(): HTMLElement {
    return req("coveragePanel");
  },
  get historyPanel(): HTMLElement {
    return req("historyPanel");
  },
  get historyBtn(): HTMLElement {
    return req("historyBtn");
  },
  get configClose(): HTMLElement {
    return req("configClose");
  },
  get statusPopup(): HTMLElement {
    return req("statusPopup");
  },
  get statusBtn(): HTMLElement {
    return req("statusBtn");
  },
};

// themeBtn/configBtn/userBtn stay raw lookups: user-menu.ts removes
// themeBtn/configBtn from the DOM after init, and a $ getter would throw on
// post-removal access.

export function dialog(id: string): HTMLDialogElement {
  return document.getElementById(id) as HTMLDialogElement;
}

export function input(id: string): HTMLInputElement {
  return document.getElementById(id) as HTMLInputElement;
}

export function select(id: string): HTMLSelectElement {
  return document.getElementById(id) as HTMLSelectElement;
}

// Cancel / Escape / backdrop-click all resolve false.
export function confirm(title: string, message: string, confirmLabel?: string): Promise<boolean> {
  return ask(message, confirmLabel !== undefined ? { title, confirmLabel } : { title });
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
