// view-scope.ts — THE disposal contract: one scope per mounted subtree.
//
// Anything that outlives a DOM node unless something releases it — a bindList
// binding, an effect, a registry entry, a timer, an abort controller — is
// registered into its subtree's scope and released when that subtree's OWNER
// swaps or leaves. The owner is whoever writes the container, so the statement
// that discards the nodes is the statement that releases what hung off them.
//
// That replaces sampling for detachment (`isConnected` sweeps, `querySelector`
// mount probes), which is only as good as its timing: a freshly bound node is
// momentarily indistinguishable from a discarded one, and that is how a rebuild
// once left a live binding pointing at a detached <tbody> while a stale copy
// stayed on screen.
//
// Two owners release, because a view has two ways to end: its container takes
// another view (ViewHost), or the route that mounted it is left
// (`ownedByRoute`). Disposal is idempotent, so a view both hold dies once.

/** Registrations that live exactly as long as one mounted subtree. */
export interface Scope {
  /** Register a disposer, run once when this scope is disposed. Registering
   *  on an already-disposed scope runs it immediately — the subtree it would
   *  have belonged to is gone, so deferring would leak. */
  add(dispose: () => void): void;
  /** The scope of one child subtree (a list row). Re-opening the same key
   *  disposes the previous child first: a row repaint discards what the last
   *  paint built, so it must release what that paint registered. */
  child(key: string): Scope;
  /** Release one child early — the row left the list. */
  release(key: string): void;
  dispose(): void;
}

function createScope(): Scope {
  const disposers: (() => void)[] = [];
  const children = new Map<string, Scope>();
  let alive = true;

  const scope: Scope = {
    add(dispose: () => void): void {
      if (!alive) {
        dispose();
        return;
      }
      disposers.push(dispose);
    },
    child(key: string): Scope {
      children.get(key)?.dispose();
      const inner = createScope();
      if (!alive) {
        inner.dispose();
        return inner;
      }
      children.set(key, inner);
      return inner;
    },
    release(key: string): void {
      children.get(key)?.dispose();
      children.delete(key);
    },
    dispose(): void {
      if (!alive) {
        return;
      }
      alive = false;
      // Children first (they are nested subtrees), then this scope's own
      // registrations in reverse order of registration.
      for (const inner of children.values()) {
        inner.dispose();
      }
      children.clear();
      for (let i = disposers.length - 1; i >= 0; i--) {
        // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- index is loop-bounded
        disposers[i]!();
      }
      disposers.length = 0;
    },
  };
  return scope;
}

/** A container that renders ONE view at a time. */
export interface ViewHost {
  /** Take the container for `viewId`, releasing the previous occupant. */
  mount(viewId: string): Scope;
  /** The live scope IF `viewId` is still the occupant — the reuse fast-path's
   *  question, answered from ownership rather than from the DOM. */
  scopeFor(viewId: string): Scope | null;
  /** Release the occupant: the container is being written with content that
   *  owns no registrations (a skeleton, an error, an empty state). */
  clear(): void;
}

function createViewHost(): ViewHost {
  let occupant: Scope | null = null;
  let occupantId = "";
  return {
    mount(viewId: string): Scope {
      occupant?.dispose();
      occupant = createScope();
      occupantId = viewId;
      return occupant;
    },
    scopeFor(viewId: string): Scope | null {
      return occupant !== null && occupantId === viewId ? occupant : null;
    },
    clear(): void {
      occupant?.dispose();
      occupant = null;
      occupantId = "";
    },
  };
}

/** THE coverage-panel content host (`$.coverageContent`): the library table,
 *  the series detail, the movie detail and the file manager are four views of
 *  one container, so every write to it goes through this host. */
export const contentView: ViewHost = createViewHost();

/** THE history-panel content host (`#historyContent`). */
export const historyView: ViewHost = createViewHost();

// Views a route mounted. Self-pruning: each scope drops its own entry when it
// dies, which matters because several views can mount between two route leaves
// (the heal's movie arm re-opens the detail once per window).
const routeViews = new Set<Scope>();

/** Hand a view to the ROUTER as well as to its container: a detail view must
 *  not outlive the route that mounted it, and a navigation to another page
 *  writes no container the view lives in. */
export function ownedByRoute(scope: Scope): void {
  routeViews.add(scope);
  scope.add(() => {
    routeViews.delete(scope);
  });
}

/** The router's leave path (page-leg's abortPageLeg). */
export function releaseRouteViews(): void {
  for (const scope of [...routeViews]) {
    scope.dispose();
  }
  routeViews.clear();
}

/** View ids for the two detail views, shared by the render that mounts one and
 *  the reuse check that asks whether it is still up. */
export function seriesViewId(tvdbId: string): string {
  return `series:${tvdbId}`;
}

export function movieViewId(tmdbId: string): string {
  return `movie:${tmdbId}`;
}
