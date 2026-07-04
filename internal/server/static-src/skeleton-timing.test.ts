import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { skeletonTiming } from "./skeleton-timing.js";

describe("skeletonTiming", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("skips the skeleton and renders immediately when the load settles before the show-delay", () => {
    const show = vi.fn();
    const render = vi.fn();
    const t = skeletonTiming(show, { showDelayMs: 150, minVisibleMs: 300 });

    vi.advanceTimersByTime(100); // still inside the 150ms show-delay
    t.commit(render);

    expect(show).not.toHaveBeenCalled();
    expect(render).toHaveBeenCalledTimes(1);

    // The pending show timer must not fire after commit cleared it.
    vi.advanceTimersByTime(1000);
    expect(show).not.toHaveBeenCalled();
  });

  it("paints the skeleton at the show-delay while the load is still pending", () => {
    const show = vi.fn();
    skeletonTiming(show, { showDelayMs: 150, minVisibleMs: 300 });

    expect(show).not.toHaveBeenCalled();
    vi.advanceTimersByTime(149);
    expect(show).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1); // 150ms
    expect(show).toHaveBeenCalledTimes(1);
  });

  it("holds a shown skeleton for min-visible before rendering the content", () => {
    const show = vi.fn();
    const render = vi.fn();
    const t = skeletonTiming(show, { showDelayMs: 150, minVisibleMs: 300 });

    vi.advanceTimersByTime(150); // skeleton painted at 150ms
    expect(show).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(50); // content ready at 200ms — only 50ms into min-visible
    t.commit(render);
    expect(render).not.toHaveBeenCalled();

    vi.advanceTimersByTime(249); // 449ms — one tick short of the 300ms floor
    expect(render).not.toHaveBeenCalled();

    vi.advanceTimersByTime(1); // 450ms — 300ms after the skeleton showed
    expect(render).toHaveBeenCalledTimes(1);
  });

  it("renders immediately when the skeleton has already outlived min-visible", () => {
    const show = vi.fn();
    const render = vi.fn();
    const t = skeletonTiming(show, { showDelayMs: 150, minVisibleMs: 300 });

    vi.advanceTimersByTime(500); // skeleton at 150ms, now well past 150 + 300
    expect(show).toHaveBeenCalledTimes(1);

    t.commit(render);
    expect(render).toHaveBeenCalledTimes(1);
  });

  it("suppresses the skeleton when the signal aborts during the show-delay", () => {
    const show = vi.fn();
    const render = vi.fn();
    const ac = new AbortController();
    const t = skeletonTiming(show, {
      showDelayMs: 150,
      minVisibleMs: 300,
      signal: ac.signal,
    });

    vi.advanceTimersByTime(100);
    ac.abort();
    vi.advanceTimersByTime(1000);
    expect(show).not.toHaveBeenCalled(); // no orphaned skeleton for a cancelled load

    // The content paint is the caller's own; the helper still lets it run (the
    // skeleton was the only thing the abort suppressed). Since no skeleton
    // showed, commit renders immediately.
    t.commit(render);
    expect(render).toHaveBeenCalledTimes(1);
  });

  it("uses 150ms/300ms defaults when no options are given", () => {
    const show = vi.fn();
    const render = vi.fn();
    const t = skeletonTiming(show);

    vi.advanceTimersByTime(149);
    expect(show).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1); // 150ms default show-delay
    expect(show).toHaveBeenCalledTimes(1);

    t.commit(render); // immediately after show → full 300ms min-visible remains
    expect(render).not.toHaveBeenCalled();
    vi.advanceTimersByTime(299);
    expect(render).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1); // 300ms default min-visible
    expect(render).toHaveBeenCalledTimes(1);
  });
});
