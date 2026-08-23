import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import type * as NotifyModule from "./notify.js";

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

// The `?boot=` query is what makes the re-import build a fresh toaster, and it is
// not decoration. Browser Mode resolves a dynamic import through the browser's
// own module map, which is keyed by URL and holds evaluated modules for the life
// of the page: vi.resetModules() clears the runner's registry but cannot evict an
// entry from that map, so a bare import("./notify.js") returns the toaster an
// earlier test already created and the stack is never isolated. A distinct query
// is a distinct URL and therefore a fresh evaluation. `@vite-ignore` opts out of
// Vite's variable-dynamic-import rewrite.
//
// The `.ts` extension is load-bearing: this specifier is built at runtime, so the
// URL the browser requests is the one written here, and that URL is what v8
// coverage attributes the evaluation to. Written `./notify.js` it names a file
// that does not exist, and notify.ts reports 0% coverage while this suite stays
// green.
let bootCount = 0;
async function loadNotify(): Promise<typeof NotifyModule> {
  return (await import(
    /* @vite-ignore */ `./notify.ts?boot=${++bootCount}`
  )) as typeof NotifyModule;
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

  // The info window is 3s, one second SHORTER than the library's own default.
  // Dropping the explicit duration would silently stretch every info toast to
  // 4s, which only a test that advances to 3s can see.
  it("auto-dismisses an info toast after its 3s duration", async () => {
    expect.assertions(2);
    const { info } = await loadNotify();
    info("note");
    vi.advanceTimersByTime(3000);
    document.querySelector(".uip-toast")?.dispatchEvent(new Event("transitionend"));
    vi.advanceTimersByTime(400);
    expect(toasts().length).toBe(0);
    // A success toast at the same point is still up: the two windows differ.
    const { success } = await loadNotify();
    success("still here");
    vi.advanceTimersByTime(3000);
    document.querySelector(".uip-toast")?.dispatchEvent(new Event("transitionend"));
    vi.advanceTimersByTime(400);
    expect(toasts().length).toBe(1);
  });
});
