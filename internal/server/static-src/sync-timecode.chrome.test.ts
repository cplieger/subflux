// @vitest-environment happy-dom
// The timecode widget's chrome, which sync-timecode.test.ts (values, keyboard,
// wheel, hold-repeat, touch drag) does not look at: the composition the
// stylesheet styles, and whether a touch on a chevron is swallowed.
//
// The class names asserted here are a contract with css/12-sync.css, which
// styles `.tc-numbers` (the flex row that aligns the digits), `.tc-dot` and
// `.tc-unit` (the muted separators) and `.tc-btn-stack` (the vertical chevron
// stack). Drop one and the widget renders as a run of unaligned text.
import { describe, it, expect, afterEach, vi } from "vitest";

import { buildTimecodeInput, type TimecodeInput } from "./sync-timecode.js";

function mount(initialMs: number): TimecodeInput {
  const root = buildTimecodeInput(initialMs, () => undefined) as TimecodeInput;
  document.body.appendChild(root);
  return root;
}

function labelled(root: ParentNode, label: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`[aria-label="${label}"]`);
  if (found === null) {
    throw new Error(`missing element: ${label}`);
  }
  return found;
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.useRealTimers();
});

describe("buildTimecodeInput: how the widget is composed", () => {
  it("groups the sign, the digits and the unit in one aligned row", () => {
    const root = mount(1234);

    const numbers = root.querySelector(".tc-numbers");

    expect(numbers?.textContent).toBe("+1.234s");
  });

  it("separates the seconds from the milliseconds with a decimal point", () => {
    const root = mount(1234);

    const dot = root.querySelector(".tc-dot");

    expect(dot?.textContent).toBe(".");
  });

  it("puts the decimal point between the seconds and the 100ms digit", () => {
    const root = mount(1234);

    const dot = root.querySelector(".tc-dot");

    expect([dot?.previousElementSibling, dot?.nextElementSibling]).toEqual([
      labelled(root, "Seconds"),
      labelled(root, "100ms"),
    ]);
  });

  it("marks the value as seconds with a trailing unit", () => {
    const root = mount(1234);

    const unit = root.querySelector(".tc-unit");

    expect(unit?.textContent).toBe("s");
  });

  it("stacks the two chevrons together so they sit above one another", () => {
    const root = mount(1234);

    const stack = root.querySelector(".tc-btn-stack");

    expect([...(stack?.children ?? [])]).toEqual([
      labelled(root, "Increase"),
      labelled(root, "Decrease"),
    ]);
  });
});

describe("buildTimecodeInput: holding a chevron on a touchscreen", () => {
  it("swallows the touch so the page does not scroll while the chevron is held", () => {
    vi.useFakeTimers();
    const root = mount(0);

    const ev = new Event("touchstart", { bubbles: true, cancelable: true });
    labelled(root, "Increase").dispatchEvent(ev);

    // A passive touchstart listener cannot cancel the scroll the browser would
    // otherwise start, and the offset would run away under the user's finger.
    expect(ev.defaultPrevented).toBe(true);
  });
});
