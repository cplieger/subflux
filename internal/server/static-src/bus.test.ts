// Unit tests for bus.ts — typed event bus.
import { describe, it, expect } from "vitest";
import { on, emit, BusEvent } from "./bus.js";

describe("bus", () => {
  describe("on/emit", () => {
    const cases = [
      {
        name: "delivers event to subscriber",
        event: BusEvent.NavRoute as const,
        args: ["/test"] as [string],
      },
      {
        name: "delivers event with no args",
        event: BusEvent.LoadHistory as const,
        args: [] as [],
      },
      {
        name: "delivers event with optional arg",
        event: BusEvent.NavHistory as const,
        args: ["filter"] as [string],
      },
    ];

    for (const tc of cases) {
      it(tc.name, () => {
        const received: unknown[] = [];
        const unsub = on(tc.event, (...args: unknown[]) => {
          received.push(args);
        });
        emit(tc.event, ...tc.args);
        unsub();
        expect(received).toHaveLength(1);
        expect(received[0]).toEqual(tc.args);
      });
    }
  });

  it("unsubscribe removes listener", () => {
    let count = 0;
    const unsub = on(BusEvent.LoadHistory, () => {
      count++;
    });
    emit(BusEvent.LoadHistory);
    expect(count).toBe(1);
    unsub();
    emit(BusEvent.LoadHistory);
    expect(count).toBe(1);
  });

  it("multiple subscribers all receive event", () => {
    const calls: number[] = [];
    const unsub1 = on(BusEvent.LoadHistory, () => calls.push(1));
    const unsub2 = on(BusEvent.LoadHistory, () => calls.push(2));
    emit(BusEvent.LoadHistory);
    unsub1();
    unsub2();
    expect(calls).toEqual([1, 2]);
  });

  it("listener error does not prevent other listeners", () => {
    const calls: number[] = [];
    const unsub1 = on(BusEvent.LoadHistory, () => {
      throw new Error("boom");
    });
    const unsub2 = on(BusEvent.LoadHistory, () => calls.push(2));
    emit(BusEvent.LoadHistory);
    unsub1();
    unsub2();
    expect(calls).toEqual([2]);
  });

  it("emit with no subscribers does not throw", () => {
    expect(() => emit(BusEvent.OpenSecurity)).not.toThrow();
  });
});
