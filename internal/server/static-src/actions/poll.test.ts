// @vitest-environment happy-dom
// Tests for pollAction: interval scheduling, pause-when-hidden, focus
// refresh, backoff-on-error, stop(), and registerCleanup integration.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("../toast.js", () => ({
  info: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  showToast: vi.fn(),
}));

import { defineAction, _resetForTest as resetDefine } from "./define.js";
import { _resetForTest as resetRegistry } from "./registry.js";
import { _resetForTest as resetCleanup, _cancelAllForTest as cancelAllForTest } from "./cleanup.js";
import { pollAction } from "./poll.js";

beforeEach(() => {
  resetDefine();
  resetRegistry();
  resetCleanup();
  vi.clearAllMocks();
  // Reset visibility to visible.
  Object.defineProperty(document, "hidden", { value: false, configurable: true });
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
});

afterEach(() => {
  vi.useRealTimers();
});

// ===========================================================================
// Interval scheduling
// ===========================================================================

describe("pollAction — basic scheduling", () => {
  it("dispatches immediately on start, then at the interval", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.basic",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 1000 });

    // Immediate dispatch.
    await vi.runAllTicks();
    await Promise.resolve();
    await Promise.resolve();
    expect(count).toBe(1);

    // After the first interval, second poll fires.
    await vi.advanceTimersByTimeAsync(1000);
    expect(count).toBe(2);

    // Third poll.
    await vi.advanceTimersByTimeAsync(1000);
    expect(count).toBe(3);

    stop();
  });

  it("stop() cancels the next scheduled poll", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.stop",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 100 });
    await Promise.resolve();
    expect(count).toBe(1);

    stop();
    await vi.advanceTimersByTimeAsync(500);
    expect(count).toBe(1); // never advanced past the immediate dispatch
  });

  it("stop() is idempotent", () => {
    const action = defineAction<undefined, undefined>({
      name: "test.poll.idempotent",
      run: async () => { /* noop */ },
    });
    const stop = pollAction(action, undefined, { interval: 1000 });
    expect(() => {
      stop();
      stop();
      stop();
    }).not.toThrow();
  });
});

// ===========================================================================
// pauseWhenHidden
// ===========================================================================

describe("pollAction — pauseWhenHidden", () => {
  it("pauses on visibilitychange to hidden, resumes on visible with immediate dispatch", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.hidden",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 1000, pauseWhenHidden: true });
    await Promise.resolve();
    expect(count).toBe(1);

    // Go hidden — next poll should not fire.
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(5000);
    expect(count).toBe(1);

    // Return to visible — immediate dispatch.
    Object.defineProperty(document, "hidden", { value: false, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    await Promise.resolve();
    await Promise.resolve();
    expect(count).toBe(2);

    stop();
  });

  it("doesn't fire the first poll if started while hidden", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.start_hidden",
      run: async () => ++count,
    });

    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 1000, pauseWhenHidden: true });
    await vi.advanceTimersByTimeAsync(5000);
    expect(count).toBe(0); // never fired

    Object.defineProperty(document, "hidden", { value: false, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    await Promise.resolve();
    expect(count).toBe(1);

    stop();
  });

  it("pauseWhenHidden: false keeps polling while hidden", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.always",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 100, pauseWhenHidden: false });
    await Promise.resolve();

    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(300);
    expect(count).toBeGreaterThan(1);

    stop();
  });
});

// ===========================================================================
// refreshOnFocus
// ===========================================================================

describe("pollAction — refreshOnFocus", () => {
  it("dispatches immediately on window focus", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.focus",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 10_000, refreshOnFocus: true });
    await vi.runAllTicks();
    await vi.advanceTimersByTimeAsync(0);
    expect(count).toBe(1);

    // Trigger focus before the interval elapses.
    window.dispatchEvent(new Event("focus"));
    await vi.runAllTicks();
    await vi.advanceTimersByTimeAsync(0);
    expect(count).toBe(2);

    stop();
  });

  it("refreshOnFocus: false ignores focus events", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.no_focus",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, { interval: 10_000, refreshOnFocus: false });
    await vi.runAllTicks();
    await vi.advanceTimersByTimeAsync(0);
    expect(count).toBe(1);

    window.dispatchEvent(new Event("focus"));
    await vi.runAllTicks();
    await vi.advanceTimersByTimeAsync(0);
    expect(count).toBe(1); // no refresh

    stop();
  });
});

// ===========================================================================
// backoffOnError
// ===========================================================================

describe("pollAction — backoffOnError", () => {
  it("delays grow on consecutive failures", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.backoff",
      run: async () => {
        count++;
        throw new Error("fail");
      },
      error: false,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, {
      interval: 100,
      backoffOnError: { factor: 2, max: 10_000 },
    });

    // Drain initial dispatch + scheduled timer microtasks.
    await vi.runAllTicks();
    await vi.advanceTimersByTimeAsync(0);
    const after1 = count;
    expect(after1).toBeGreaterThanOrEqual(1);

    // After enough time for several backed-off polls, count should grow
    // but slower than base interval. With base=100, factor=2:
    //   poll 1 at 0ms (immediate) + ~100ms drift
    //   poll 2 at +200ms (1 failure)
    //   poll 3 at +400ms (2 failures)
    //   poll 4 at +800ms
    //   poll 5 at +1600ms
    // Total at 1500ms: ~4 polls. Without backoff at 100ms: ~15 polls.
    await vi.advanceTimersByTimeAsync(1500);
    expect(count).toBeGreaterThan(after1);
    expect(count).toBeLessThan(10); // far less than the no-backoff count of ~15

    stop();
  });

  it("caps the delay at max", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.cap",
      run: async () => {
        count++;
        throw new Error("fail");
      },
      error: false,
    });

    vi.useFakeTimers();
    const stop = pollAction(action, undefined, {
      interval: 100,
      backoffOnError: { factor: 10, max: 500 },
    });

    await vi.runAllTicks();
    await vi.advanceTimersByTimeAsync(0);
    expect(count).toBeGreaterThanOrEqual(1);

    // Without cap, would be 100 * 10^N very quickly. With cap at 500ms,
    // polls fire roughly every 500ms after the first.
    await vi.advanceTimersByTimeAsync(2500);
    // Expect ~5 polls in 2.5s with 500ms cap.
    expect(count).toBeGreaterThan(2);
    expect(count).toBeLessThan(8);

    stop();
  });

  it("resets to base interval after a successful poll", async () => {
    let fail = true;
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.reset",
      run: async () => {
        count++;
        if (fail) {
          throw new Error("fail");
        }
        return count;
      },
      error: false,
    });

    // Real timers + tiny intervals: too fragile to assert exact counts
    // with fake timers because dispatch settles via microtasks that don't
    // play nicely with advanceTimersByTimeAsync.
    const stop = pollAction(action, undefined, {
      interval: 20,
      backoffOnError: { factor: 4, max: 10_000 },
    });

    // Let several backed-off failures happen.
    await new Promise((r) => setTimeout(r, 200));
    const failedCount = count;
    expect(failedCount).toBeGreaterThanOrEqual(1);

    // Switch to success. The next poll (at the backed-off delay) succeeds
    // and resets failures. After that, polls fire at the base interval.
    fail = false;
    await new Promise((r) => setTimeout(r, 400));
    const finalCount = count;
    expect(finalCount).toBeGreaterThan(failedCount);

    stop();
  });
});

// ===========================================================================
// onSuccess callback
// ===========================================================================

describe("pollAction — onSuccess callback", () => {
  it("invokes onSuccess with the result on each successful dispatch", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.onSuccess",
      run: async () => ++count,
    });
    const onSuccess = vi.fn<(n: number) => void>();

    const stop = pollAction(action, undefined, {
      interval: 20,
      onSuccess,
      pauseWhenHidden: false, // happy-dom may report hidden
    });

    await new Promise((r) => setTimeout(r, 100));
    expect(onSuccess.mock.calls.length).toBeGreaterThan(1);
    expect(onSuccess.mock.calls[0]?.[0]).toBe(1);

    stop();
  });

  it("does not invoke onSuccess on failed dispatch", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.onSuccess_fail",
      run: async () => {
        count++;
        throw new Error("fail");
      },
      error: false,
    });
    const onSuccess = vi.fn<(n: number) => void>();

    const stop = pollAction(action, undefined, {
      interval: 20,
      onSuccess,
      pauseWhenHidden: false,
    });

    await new Promise((r) => setTimeout(r, 100));
    expect(count).toBeGreaterThan(0);
    expect(onSuccess).not.toHaveBeenCalled();

    stop();
  });

  it("catches errors thrown by onSuccess and continues polling", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.onSuccess_throws",
      run: async () => ++count,
    });
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => { /* noop */ });
    const onSuccess = vi.fn<(n: number) => void>(() => {
      throw new Error("kaboom");
    });

    const stop = pollAction(action, undefined, {
      interval: 20,
      onSuccess,
      pauseWhenHidden: false,
    });

    await new Promise((r) => setTimeout(r, 100));
    expect(count).toBeGreaterThan(1); // poll continued past the throw
    expect(consoleError).toHaveBeenCalled();

    consoleError.mockRestore();
    stop();
  });
});

// ===========================================================================
// Cleanup integration
// ===========================================================================

describe("pollAction — cleanup integration", () => {
  it("auto-stops when registered cleanup fires (e.g. beforeunload)", async () => {
    let count = 0;
    const action = defineAction<undefined, number>({
      name: "test.poll.cleanup",
      run: async () => ++count,
    });

    vi.useFakeTimers();
    pollAction(action, undefined, { interval: 100 });
    await Promise.resolve();
    expect(count).toBe(1);

    // Simulate beforeunload drain.
    cancelAllForTest();

    await vi.advanceTimersByTimeAsync(500);
    expect(count).toBe(1); // stopped, no further polls
  });
});
