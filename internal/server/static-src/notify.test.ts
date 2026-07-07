// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// notify.ts wraps a @cplieger/ui-primitives toaster created at module load, so
// re-import fresh per test (vi.resetModules) to get an isolated toaster + stack.
// These tests cover the subflux WRAPPER contract (level → toast class, durations,
// stack cap); the primitive's own lifecycle is covered in its package.
beforeEach(() => {
  document.body.innerHTML = "";
  vi.useFakeTimers();
  vi.resetModules();
});

afterEach(() => {
  vi.runOnlyPendingTimers();
  vi.useRealTimers();
});

function stack(): HTMLElement | null {
  return document.querySelector(".uip-toast-stack");
}

function toasts(): NodeListOf<Element> {
  return document.querySelectorAll(".uip-toast");
}

async function loadNotify() {
  return await import("./notify.js");
}

describe("notify", () => {
  it("success creates a success toast in the stack", async () => {
    expect.assertions(2);
    const { success } = await loadNotify();
    success("done");
    expect(stack()).not.toBeNull();
    expect(document.querySelector(".uip-toast--success")?.textContent).toContain("done");
  });

  it("error creates an error toast", async () => {
    expect.assertions(1);
    const { error } = await loadNotify();
    error("fail");
    expect(document.querySelector(".uip-toast--error")).not.toBeNull();
  });

  it("info creates an info toast", async () => {
    expect.assertions(1);
    const { info } = await loadNotify();
    info("note");
    expect(document.querySelector(".uip-toast--info")).not.toBeNull();
  });

  it("caps the visible stack at 3, queueing the rest", async () => {
    expect.assertions(1);
    const { success } = await loadNotify();
    success("1");
    success("2");
    success("3");
    success("4");
    expect(toasts().length).toBe(3);
  });

  it("auto-dismisses a success toast after its 4s duration", async () => {
    expect.assertions(2);
    const { success } = await loadNotify();
    success("auto");
    expect(toasts().length).toBe(1);
    // Enter rAF + the 4s auto-dismiss timer fire; then the leave completes on
    // transitionend (with a 400ms fallback for good measure).
    vi.advanceTimersByTime(4000);
    document.querySelector(".uip-toast")?.dispatchEvent(new Event("transitionend"));
    vi.advanceTimersByTime(400);
    expect(toasts().length).toBe(0);
  });
});
