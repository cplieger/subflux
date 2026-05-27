// Structural tree-diff: reconcile a parent's children against new nodes.
// Handles attribute patching, text node updates, element reordering,
// and recursive child reconciliation. Keying via data-* or *-id attrs.

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

export function reconcileChildren(parent: Node, newChildren: Node[]): void {
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
    const newChild = newChildren[i]!;
    const newKey = nodeKey(newChild);

    let matched = newKey ? (oldByKey.get(newKey) ?? null) : null;
    if (matched) {
      oldByKey.delete(newKey);
    } else if (!newKey) {
      while (oldIdx < oldChildren.length && nodeKey(oldChildren[oldIdx]!)) {
        oldIdx++;
      }
      if (oldIdx < oldChildren.length) {
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
  if (node?.nodeType !== 1) {
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
}
