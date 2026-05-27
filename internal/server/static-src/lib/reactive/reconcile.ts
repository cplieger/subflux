// Keyed-list DOM reconciliation with mount/update/onRemove lifecycle.
// Identity-preserving: existing elements survive across renders.

export const KEY_ATTR = "data-reconcile-key";

export interface ReconcileSpec<T> {
  key: (item: T) => string;
  mount: (item: T) => HTMLElement;
  update?: (el: HTMLElement, item: T) => void;
  onRemove?: (el: HTMLElement, key: string) => void;
}

export function reconcile<T>(
  parent: ParentNode,
  items: readonly T[],
  spec: ReconcileSpec<T>,
): void {
  const existing = new Map<string, HTMLElement>();
  for (let n = parent.firstChild; n !== null; n = n.nextSibling) {
    if (n.nodeType !== 1) {
      continue;
    }
    const el = n as HTMLElement;
    const k = el.getAttribute(KEY_ATTR);
    if (k !== null) {
      existing.set(k, el);
    }
  }

  let target: Node | null = null;
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i] as T;
    const k = spec.key(item);
    let el = existing.get(k);
    if (el === undefined) {
      el = spec.mount(item);
      el.setAttribute(KEY_ATTR, k);
    } else {
      existing.delete(k);
      if (spec.update) {
        spec.update(el, item);
      }
    }
    parent.insertBefore(el, target);
    target = el;
  }

  for (const [k, el] of existing) {
    spec.onRemove?.(el, k);
    el.remove();
  }
}
