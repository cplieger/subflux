// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// We need to re-import the module fresh for each test since it has module-level state.
// Use dynamic import with vi.resetModules().
beforeEach(() => {
  document.body.innerHTML = "";
  vi.useFakeTimers();
  vi.resetModules();
});

afterEach(() => {
  vi.useRealTimers();
});

function getStack(): HTMLElement | null {
  return document.querySelector(".toast-stack");
}

function toasts(): NodeListOf<Element> {
  return document.querySelectorAll(".toast");
}

async function loadNotify() {
  return await import("./notify.js");
}

describe("notify", () => {
  it("success creates toast with data-level ok", async () => {
    expect.assertions(2);
    const { success } = await loadNotify();
    success("done");
    const stack = getStack();
    expect(stack).not.toBeNull();
    const toast = stack!.querySelector(".toast");
    expect(toast?.getAttribute("data-level")).toBe("ok");
  });

  it("error creates toast with data-level err", async () => {
    expect.assertions(1);
    const { error } = await loadNotify();
    error("fail");
    const toast = document.querySelector(".toast");
    expect(toast?.getAttribute("data-level")).toBe("err");
  });

  it("info creates toast with data-level info", async () => {
    expect.assertions(1);
    const { info } = await loadNotify();
    info("note");
    const toast = document.querySelector(".toast");
    expect(toast?.getAttribute("data-level")).toBe("info");
  });

  it("4th toast is queued when 3 visible", async () => {
    expect.assertions(1);
    const { success } = await loadNotify();
    success("1");
    success("2");
    success("3");
    success("4");
    expect(toasts().length).toBe(3);
  });

  it("queued toast promoted on dismiss", async () => {
    expect.assertions(2);
    const { success } = await loadNotify();
    success("1");
    success("2");
    success("3");
    success("4");
    expect(toasts().length).toBe(3);
    // Simulate dismiss via click + animationend.
    const first = toasts()[0] as HTMLElement;
    first.click();
    first.dispatchEvent(new Event("animationend"));
    expect(toasts().length).toBe(3); // queued promoted
  });

  it("auto-dismiss fires after duration for success", async () => {
    expect.assertions(2);
    const { success } = await loadNotify();
    success("auto");
    expect(toasts().length).toBe(1);
    vi.advanceTimersByTime(4000);
    const toast = document.querySelector(".toast") as HTMLElement;
    if (toast) {
      toast.dispatchEvent(new Event("animationend"));
    }
    vi.advanceTimersByTime(400);
    expect(toasts().length).toBe(0);
  });
});
