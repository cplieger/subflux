// Property-based tests for store.ts.
//
// Invariants tested:
// 1. Getter/setter symmetry: for any key+value, set(k,v) then get(k) returns v.
// 2. Subscriber-receives-final-value: subscribers see the most recent set()
//    value at the moment they were notified.
// 3. Batch coalescing: inside batch(), N set(k, v_i) calls produce at most
//    one subscriber notification per key, and the value seen is v_N (the last).
//
// These mirror invariants in the prose docs at the top of store.ts; without
// property-based generation they get tested only on hand-picked examples.

import { describe, it, expect } from "vitest";
import fc from "fast-check";

import * as store from "./store.js";

// Reset subflux store keys we touch between properties so prior runs do not
// leak state. The store is module-scoped; tests share it. Only flip the
// keys this file uses.
function resetStoreKeys(): void {
  store.set("currentPage", "");
  store.set("scanInFlight", false);
  store.set("refreshPending", false);
  store.set("needsRefresh", false);
  store.set("isUnconfigured", false);
  store.set("isReady", false);
}

describe("store property", () => {
  it("set then get returns the same value for boolean keys", () => {
    fc.assert(
      fc.property(fc.boolean(), (v) => {
        resetStoreKeys();
        store.set("scanInFlight", v);
        expect(store.get("scanInFlight")).toBe(v);
      }),
    );
  });

  it("set then get returns the same value for string keys", () => {
    fc.assert(
      fc.property(fc.string(), (s) => {
        resetStoreKeys();
        store.set("currentPage", s);
        expect(store.get("currentPage")).toBe(s);
      }),
    );
  });

  it("subscriber sees the last set() value", () => {
    fc.assert(
      fc.property(fc.array(fc.boolean(), { minLength: 1, maxLength: 50 }), (values) => {
        resetStoreKeys();
        const finalVal = values[values.length - 1] as boolean;
        // Seed the store with the opposite of the final value so the
        // last set() in the sequence is guaranteed to be a transition
        // and therefore guaranteed to fire a notification. Without this
        // the property is too strong: subscribers don't fire on no-op
        // sets, and a sequence of all-same-as-current values produces
        // no notifications.
        store.set("scanInFlight", !finalVal);
        let observed: boolean | undefined;
        const unsub = store.subscribe("scanInFlight", (v) => {
          observed = v;
        });
        try {
          for (const v of values) {store.set("scanInFlight", v);}
          expect(observed).toBe(finalVal);
        } finally {
          unsub();
        }
      }),
    );
  });

  it("batch coalesces N writes into <=1 notification per key with final value", () => {
    fc.assert(
      fc.property(fc.array(fc.boolean(), { minLength: 1, maxLength: 50 }), (values) => {
        resetStoreKeys();
        const observed: boolean[] = [];
        const unsub = store.subscribe("scanInFlight", (v) => {
          observed.push(v);
        });
        try {
          store.batch(() => {
            for (const v of values) {store.set("scanInFlight", v);}
          });
          // At most one notification fired (could be zero if final value
          // equals the prior store state, which after reset is `false`).
          expect(observed.length).toBeLessThanOrEqual(1);
          // Whichever notification fired (if any), it carries the final value.
          if (observed.length === 1) {
            expect(observed[0]).toBe(values[values.length - 1]);
          }
        } finally {
          unsub();
        }
      }),
    );
  });

  it("unsubscribed callback receives no further notifications", () => {
    fc.assert(
      fc.property(fc.array(fc.boolean(), { minLength: 1, maxLength: 20 }), (values) => {
        resetStoreKeys();
        let count = 0;
        const unsub = store.subscribe("scanInFlight", () => {
          count += 1;
        });
        unsub();
        for (const v of values) {store.set("scanInFlight", v);}
        expect(count).toBe(0);
      }),
    );
  });
});
