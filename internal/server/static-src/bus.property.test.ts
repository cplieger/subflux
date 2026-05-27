// Property-based tests for bus.ts.
//
// Invariants tested:
// 1. Fan-out: every active subscriber to event E receives every emit(E, ...).
// 2. Order preservation: each subscriber sees emits for E in the order they
//    were emitted.
// 3. Unsubscribe: a returned unsubscribe function, once called, prevents
//    further deliveries to that subscriber, leaving siblings unaffected.
// 4. Cross-event isolation: emitting on event A delivers to A's subscribers
//    only, never to B's.
//
// These mirror the prose docs at the top of bus.ts — the bus exists to
// decouple modules, and that decoupling depends on the four invariants above.

import { describe, it, expect } from "vitest";
import fc from "fast-check";

import { on, emit, BusEvent } from "./bus.js";

describe("bus property", () => {
  it("every active subscriber receives every emit on the same event", () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 10 }), // subscriber count
        fc.array(fc.string({ maxLength: 50 }), { minLength: 0, maxLength: 30 }), // emitted payloads
        (subCount, payloads) => {
          const buckets: string[][] = [];
          const unsubs: Array<() => void> = [];
          for (let i = 0; i < subCount; i++) {
            const seen: string[] = [];
            buckets.push(seen);
            unsubs.push(
              on(BusEvent.NavRoute, (path: string) => {
                seen.push(path);
              }),
            );
          }
          try {
            for (const p of payloads) emit(BusEvent.NavRoute, p);
            for (const b of buckets) {
              expect(b).toEqual(payloads);
            }
          } finally {
            for (const u of unsubs) u();
          }
        },
      ),
    );
  });

  it("unsubscribed listener receives no further emits, siblings unaffected", () => {
    fc.assert(
      fc.property(
        fc.array(fc.string({ maxLength: 50 }), { minLength: 1, maxLength: 30 }),
        fc.integer({ min: 0, max: 30 }), // unsubscribe-after index, capped
        (payloads, rawCutoff) => {
          const cutoff = Math.min(rawCutoff, payloads.length);
          const a: string[] = [];
          const b: string[] = [];
          const unsubA = on(BusEvent.NavRoute, (p: string) => {
            a.push(p);
          });
          const unsubB = on(BusEvent.NavRoute, (p: string) => {
            b.push(p);
          });
          try {
            for (let i = 0; i < payloads.length; i++) {
              if (i === cutoff) unsubA();
              emit(BusEvent.NavRoute, payloads[i] as string);
            }
            // a saw exactly the first `cutoff` payloads (after cutoff,
            // unsubscribed); b saw all of them.
            expect(a).toEqual(payloads.slice(0, cutoff));
            expect(b).toEqual(payloads);
          } finally {
            unsubA();
            unsubB();
          }
        },
      ),
    );
  });

  it("emits cross between events: A's subscribers do not see B's emits", () => {
    fc.assert(
      fc.property(
        fc.array(fc.string({ maxLength: 50 }), { minLength: 0, maxLength: 20 }), // A payloads
        fc.array(fc.string({ maxLength: 50 }), { minLength: 0, maxLength: 20 }), // B payloads
        (aPayloads, bPayloads) => {
          const a: string[] = [];
          const b: string[] = [];
          const unsubA = on(BusEvent.NavRoute, (p: string) => {
            a.push(p);
          });
          const unsubB = on(BusEvent.NavHistory, (p: string | undefined) => {
            b.push(p ?? "");
          });
          try {
            for (const p of aPayloads) emit(BusEvent.NavRoute, p);
            for (const p of bPayloads) emit(BusEvent.NavHistory, p);
            expect(a).toEqual(aPayloads);
            expect(b).toEqual(bPayloads);
          } finally {
            unsubA();
            unsubB();
          }
        },
      ),
    );
  });
});
