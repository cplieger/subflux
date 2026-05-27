// Reactive signals: signal<T>, effect (with cleanup), batch.
// Outside batch(): effects run synchronously on signal write.
// Inside batch(): effects are deferred and coalesced, flushed via
// MessageChannel on the next microtask (React scheduler pattern).

interface Subscriber {
  execute(): void;
  deps: Set<Set<Subscriber>>;
}
let tracking: Subscriber | null = null;
let batchDepth = 0;
const pending = new Set<Subscriber>();
const channel = new MessageChannel();
let scheduled = false;
let flushing = false;

channel.port1.onmessage = flush;

function flush(): void {
  scheduled = false;
  drainPending();
}

function drainPending(): void {
  if (flushing) {
    return;
  }
  flushing = true;
  // Drain iteratively — effects may enqueue more effects.
  while (pending.size > 0) {
    const fns = [...pending];
    pending.clear();
    for (const s of fns) {
      s.execute();
    }
  }
  flushing = false;
}

function notify(subs: Set<Subscriber>): void {
  for (const s of subs) {
    pending.add(s);
  }
  if (batchDepth === 0) {
    drainPending();
  }
}

function schedulePending(): void {
  if (pending.size === 0 || scheduled) {
    return;
  }
  scheduled = true;
  channel.port2.postMessage(null);
}

/** Flush all pending effects synchronously. No-op inside batch(). */
export function flushSync(): void {
  if (batchDepth > 0) {
    return;
  }
  if (scheduled) {
    scheduled = false;
  }
  drainPending();
}

export interface Signal<T> {
  value: T;
  peek(): T;
}

export function signal<T>(initial: T): Signal<T> {
  let val = initial;
  const subs = new Set<Subscriber>();
  return {
    get value(): T {
      if (tracking !== null) {
        subs.add(tracking);
        tracking.deps.add(subs);
      }
      return val;
    },
    set value(v: T) {
      if (Object.is(val, v)) {
        return;
      }
      val = v;
      notify(subs);
    },
    peek(): T {
      return val;
    },
  };
}

export type Cleanup = undefined | (() => void);

export function effect(fn: () => Cleanup): () => void {
  let cleanup: Cleanup;
  let disposed = false;
  const sub: Subscriber = {
    deps: new Set(),
    execute() {
      if (disposed) {
        return;
      }
      for (const depSet of sub.deps) {
        depSet.delete(sub);
      }
      sub.deps.clear();
      if (cleanup) {
        cleanup();
        cleanup = undefined;
      }
      const prev = tracking;
      tracking = sub;
      try {
        cleanup = fn();
      } catch (e) {
        console.error("effect error:", e);
      } finally {
        tracking = prev;
      }
    },
  };
  sub.execute();
  return () => {
    disposed = true;
    for (const depSet of sub.deps) {
      depSet.delete(sub);
    }
    sub.deps.clear();
    if (cleanup) {
      cleanup();
      cleanup = undefined;
    }
  };
}

export function batch(fn: () => void): void {
  batchDepth++;
  try {
    fn();
  } finally {
    batchDepth--;
    if (batchDepth === 0) {
      schedulePending();
    }
  }
}
