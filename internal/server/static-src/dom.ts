// DOM utility functions. All UI construction goes through these helpers
// to ensure DOM-safety (no innerHTML) and CSP compliance.

// Track which on* handler keys were set on each element. Prototype
// setters don't create own properties, so Object.keys() can't see
// them. A WeakMap avoids leaking memory when elements are GC'd.
const handlerKeysMap = new WeakMap<HTMLElement, Set<string>>();

// Attribute value types accepted by el(). Covers event handlers, booleans,
// numbers, strings, and null (to skip an attribute).
// Event handlers use a broad function signature to accept MouseEvent, KeyboardEvent, etc.
// without requiring callers to widen their parameter types.
type AttrValue = string | number | boolean | null | undefined | ((...args: never[]) => unknown);

// Create an element with attributes and children.
export function el(
  tag: string,
  attrs?: Record<string, AttrValue> | null,
  ...children: (string | Node | null | undefined)[]
): HTMLElement {
  const e = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null) {
        continue;
      }
      if (k === "className") {
        e.className = v as string;
      } else if (k.startsWith("on")) {
        (e as unknown as Record<string, unknown>)[k] = v;
        let keys = handlerKeysMap.get(e);
        if (!keys) {
          keys = new Set();
          handlerKeysMap.set(e, keys);
        }
        keys.add(k);
      } else if (k === "hidden") {
        e.hidden = v as boolean;
      } else if (k === "disabled") {
        (e as unknown as HTMLButtonElement).disabled = v as boolean;
      } else if (k === "checked") {
        (e as unknown as HTMLInputElement).checked = v as boolean;
      } else if (k === "value") {
        (e as unknown as HTMLInputElement).value = v as string;
      } else if (k === "colSpan") {
        (e as unknown as HTMLTableCellElement).colSpan = v as number;
      } else if (k === "default") {
        (e as unknown as HTMLTrackElement).default = true;
      } else {
        e.setAttribute(k, String(v));
      }
    }
  }
  for (const child of children) {
    if (child == null) {
      continue;
    }
    e.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
  }
  return e;
}

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

// Diff-patch: reconcile parent's children against new content.
export function patch(
  parent: Node,
  ...children: (string | Node | DocumentFragment | null | undefined)[]
): void {
  const newChildren: Node[] = [];
  for (const child of children) {
    if (child == null) {
      continue;
    }
    if ((child as Node).nodeType === 11) {
      newChildren.push(...Array.from((child as Node).childNodes));
    } else if (typeof child === "string") {
      newChildren.push(document.createTextNode(child));
    } else {
      newChildren.push(child);
    }
  }
  reconcileChildren(parent, newChildren);
}

function reconcileChildren(parent: Node, newChildren: Node[]): void {
  const oldChildren = Array.from(parent.childNodes);

  const oldByKey = new Map<string, Node>();
  for (const child of oldChildren) {
    const key = nodeKey(child);
    if (key) {
      oldByKey.set(key, child);
    }
  }

  let oldIdx = 0;
  for (let i = 0; i < newChildren.length; i++) {
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- bounds checked
    const newChild = newChildren[i]!;
    const newKey = nodeKey(newChild);

    let matched = newKey ? (oldByKey.get(newKey) ?? null) : null;
    if (matched) {
      oldByKey.delete(newKey);
    } else if (!newKey) {
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- bounds checked
      while (oldIdx < oldChildren.length && nodeKey(oldChildren[oldIdx]!)) {
        oldIdx++;
      }
      if (oldIdx < oldChildren.length) {
        // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- bounds checked
        matched = oldChildren[oldIdx]!;
        oldIdx++;
      }
    }

    if (!matched) {
      const ref = parent.childNodes.item(i);
      parent.insertBefore(newChild, ref);
      continue;
    }

    if (!canPatch(matched, newChild)) {
      parent.replaceChild(newChild, matched);
      continue;
    }

    const ref = parent.childNodes.item(i);
    if (ref !== matched) {
      parent.insertBefore(matched, ref);
    }

    if (matched.nodeType === 3) {
      if (matched.textContent !== newChild.textContent) {
        matched.textContent = newChild.textContent;
      }
    } else if (matched.nodeType === 1) {
      patchAttrs(matched as HTMLElement, newChild as HTMLElement);
      reconcileChildren(matched, Array.from(newChild.childNodes));
    }
  }

  while (parent.childNodes.length > newChildren.length) {
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- length > 0 guarantees lastChild
    parent.lastChild!.remove();
  }
}

function canPatch(oldNode: Node, newNode: Node): boolean {
  if (oldNode.nodeType !== newNode.nodeType) {
    return false;
  }
  if (oldNode.nodeType === 3) {
    return true;
  }
  if (oldNode.nodeType !== 1) {
    return false;
  }
  return oldNode.nodeName === newNode.nodeName;
}

function nodeKey(node: Node): string {
  if (node.nodeType !== 1) {
    return "";
  }
  for (const attr of (node as Element).attributes) {
    if (attr.name === "data-col" || attr.name.endsWith("-id")) {
      return `${attr.name}=${attr.value}`;
    }
  }
  return "";
}

function patchAttrs(oldEl: HTMLElement, newEl: HTMLElement): void {
  for (const attr of newEl.attributes) {
    if (oldEl.getAttribute(attr.name) !== attr.value) {
      oldEl.setAttribute(attr.name, attr.value);
    }
  }
  for (const attr of Array.from(oldEl.attributes)) {
    if (!newEl.hasAttribute(attr.name)) {
      oldEl.removeAttribute(attr.name);
    }
  }
  if (oldEl.hidden !== newEl.hidden) {
    oldEl.hidden = newEl.hidden;
  }

  const newKeys = handlerKeysMap.get(newEl);
  const oldKeys = handlerKeysMap.get(oldEl);
  // Remove handlers that the new element doesn't have.
  if (oldKeys) {
    for (const key of oldKeys) {
      if (!newKeys?.has(key)) {
        (oldEl as unknown as Record<string, unknown>)[key] = null;
      }
    }
  }
  // Copy handlers from new to old and update tracked keys.
  if (newKeys) {
    const oldRec = oldEl as unknown as Record<string, unknown>;
    const newRec = newEl as unknown as Record<string, unknown>;
    for (const key of newKeys) {
      oldRec[key] = newRec[key];
    }
    handlerKeysMap.set(oldEl, new Set(newKeys));
  } else if (oldKeys) {
    handlerKeysMap.delete(oldEl);
  }
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
