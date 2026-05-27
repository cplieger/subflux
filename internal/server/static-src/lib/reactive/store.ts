// Typed per-key reactive store with auto-tracked effects and computed.
// Merges subflux's typed StoreMap pattern with vibekit's MessageChannel
// batching and effect-cleanup semantics.
//
// Usage:
//   import { createStore } from './store.js';
//   interface MyMap { count: number; name: string }
//   const { get, set, subscribe, effect, computed, batch } = createStore<MyMap>();

type Callback = (value: unknown) => void;
// eslint-disable-next-line @typescript-eslint/no-invalid-void-type -- void is intentional for effect return
type Cleanup = void | (() => void);

export interface Store<M> {
  get<K extends keyof M & string>(key: K): M[K];
  set<K extends keyof M & string>(key: K, value: M[K]): void;
  subscribe<K extends keyof M & string>(key: K, cb: (value: M[K]) => void): () => void;
  effect(fn: () => Cleanup): () => void;
  computed<K extends keyof M & string>(outputKey: K, fn: () => M[K]): () => void;
  batch(fn: () => void): void;
}

export function createStore<M>(): Store<M> {
  const state: Record<string, unknown> = {};
  const subscribers: Record<string, Callback[]> = {};

  let tracking: Set<string> | null = null;
  let batchDepth = 0;
  let pendingKeys: Map<string, unknown> | null = null;

  function notifySubs(key: string, value: unknown): void {
    const cbs = subscribers[key];
    if (!cbs) {
      return;
    }
    for (const cb of cbs) {
      try {
        cb(value);
      } catch (e) {
        console.error("store subscriber error:", e);
      }
    }
  }

  function get<K extends keyof M & string>(key: K): M[K] {
    if (tracking) {
      tracking.add(key);
    }
    return state[key] as M[K];
  }

  function set<K extends keyof M & string>(key: K, value: M[K]): void {
    const prev = state[key];
    state[key] = value;
    if (prev === value) {
      return;
    }
    if (batchDepth > 0) {
      pendingKeys ??= new Map();
      pendingKeys.set(key, value);
    } else {
      notifySubs(key, value);
    }
  }

  function subscribe<K extends keyof M & string>(key: K, cb: (value: M[K]) => void): () => void {
    subscribers[key] ??= [];
    subscribers[key].push(cb as Callback);
    return () => {
      const arr = subscribers[key];
      if (arr) {
        subscribers[key] = arr.filter((c) => c !== cb);
      }
    };
  }

  function storeBatch(fn: () => void): void {
    batchDepth++;
    try {
      fn();
    } finally {
      batchDepth--;
      if (batchDepth === 0 && pendingKeys) {
        const p = pendingKeys;
        pendingKeys = null;
        for (const [k, v] of p) {
          notifySubs(k, v);
        }
      }
    }
  }

  function storeEffect(fn: () => Cleanup): () => void {
    let unsubs: (() => void)[] = [];
    let cleanup: Cleanup;
    let disposed = false;

    const run = (): void => {
      if (disposed) {
        return;
      }
      for (const u of unsubs) {
        u();
      }
      unsubs = [];
      if (cleanup) {
        cleanup();
        cleanup = undefined;
      }
      const prev = tracking;
      tracking = new Set();
      try {
        cleanup = fn();
      } catch (e) {
        console.error("store effect error:", e);
      }
      const deps = tracking;
      tracking = prev;
      for (const dep of deps) {
        unsubs.push(
          subscribe(dep as keyof M & string, run as (value: M[keyof M & string]) => void),
        );
      }
    };
    run();
    return () => {
      disposed = true;
      for (const u of unsubs) {
        u();
      }
      unsubs = [];
      if (cleanup) {
        cleanup();
        cleanup = undefined;
      }
    };
  }

  function computed<K extends keyof M & string>(outputKey: K, fn: () => M[K]): () => void {
    return storeEffect(() => {
      set(outputKey, fn());
    });
  }

  return { get, set, subscribe, effect: storeEffect, computed, batch: storeBatch };
}
