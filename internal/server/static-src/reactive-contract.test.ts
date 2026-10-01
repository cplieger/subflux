// One property of `@cplieger/reactive` that subflux code reasons from and the
// library's own docs do not state. A DEPENDENCY contract: its subject is the
// library, and its value is a version bump going red rather than a per-row throw
// found later in the detail view.
//
// `detail.ts`'s `rowCell` THROWS on a missed cell lookup instead of no-opping,
// and that is only safe because a paint only ever receives the node its own
// `mount` returned for that key. `bindList` guarantees it by passing NO
// structural `update` to `reconcile`: the only caller of `ListSpec.update` is the
// per-row effect created inside `reconcile`'s `mount`, closing over that `el`.
// Add a structural `update` and `reconcile` starts handing the paint whatever
// element it found in the parent under that key — including one it ADOPTED
// rather than mounted, which carries none of the cells `rowCell` requires.
//
// Behavioural, against the installed library: both cases drive a real `bindList`
// and read what each `update` was handed. A row's own effect error is reported to
// the effect error handler and the run continues, so a throw in a paint would not
// fail a suite on its own either.

import { describe, it, expect } from "vitest";
import { bindList, createCollection, el, KEY_ATTR, type ListSpec } from "@cplieger/reactive";

interface Row {
  readonly id: string;
  readonly label: string;
}

interface Spy {
  readonly spec: ListSpec<Row>;
  readonly mounted: Map<string, HTMLElement>;
  readonly painted: { id: string; node: HTMLElement }[];
}

/** A row whose `mount` builds the one cell its `update` writes into — the shape
 *  `detail.ts` relies on, so an adopted element is observably different. */
function spy(): Spy {
  const mounted = new Map<string, HTMLElement>();
  const painted: { id: string; node: HTMLElement }[] = [];
  return {
    mounted,
    painted,
    spec: {
      mount: (_r, id) => {
        const node = el("div", null, el("span", { className: "cell" }));
        mounted.set(id, node);
        return node;
      },
      update: (node, _r, id) => {
        painted.push({ id, node });
      },
    },
  };
}

/** The ids painted so far, deduplicated and sorted: `reconcile` mounts backwards,
 *  so the order of a first render is not the list's. */
function paintedIds(s: Spy): string[] {
  return [...new Set(s.painted.map((p) => p.id))].sort();
}

describe("bindList drives no structural update path", () => {
  it("paints a surviving row only when its OWN entity changes", () => {
    // Stable references: the content tier compares with Object.is, so a setAll
    // carrying fresh objects is a real per-entity change and would paint.
    const a: Row = { id: "a", label: "a" };
    const b: Row = { id: "b", label: "b" };
    const c: Row = { id: "c", label: "c" };
    const host = document.createElement("div");
    document.body.replaceChildren(host);
    const coll = createCollection<Row>((r) => r.id);
    coll.setAll([a, b]);
    const s = spy();
    const dispose = bindList(host, coll, s.spec);
    try {
      expect(paintedIds(s)).toStrictEqual(["a", "b"]);
      s.painted.length = 0;

      coll.setAll([c, a, b]); // insert above
      coll.remove("b"); // remove below
      coll.setAll([a, c]); // reorder

      // Only the row that was newly mounted, never the ones that survived.
      expect(paintedIds(s)).toStrictEqual(["c"]);

      // The content tier still reaches a survivor.
      coll.update("a", { id: "a", label: "moved" });

      expect(paintedIds(s)).toStrictEqual(["a", "c"]);
    } finally {
      dispose();
      host.remove();
    }
  });

  it("never paints an element it ADOPTED from the parent rather than mounted", () => {
    const host = document.createElement("div");
    // A node already carrying the reconcile key: `reconcile` adopts it by key
    // instead of calling `mount`, so it holds none of the cells a paint writes
    // into. `detail.ts` cannot produce one today, and its `rowCell` throw is what
    // a build that could would hit.
    const planted = document.createElement("div");
    planted.setAttribute(KEY_ATTR, "a");
    host.appendChild(planted);
    document.body.replaceChildren(host);
    const coll = createCollection<Row>((r) => r.id);
    coll.setAll([{ id: "a", label: "a" }]);
    const s = spy();
    const dispose = bindList(host, coll, s.spec);
    try {
      expect(host.children[0]).toBe(planted);
      expect(s.mounted.has("a")).toBe(false);
      expect(s.painted).toStrictEqual([]);

      // Nor on a later content change: with no row effect, nothing paints it.
      coll.update("a", { id: "a", label: "moved" });

      expect(s.painted).toStrictEqual([]);
    } finally {
      dispose();
      host.remove();
    }
  });
});
