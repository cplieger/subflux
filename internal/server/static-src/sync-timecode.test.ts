// @vitest-environment happy-dom
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

import { buildTimecodeInput, updateTimecodeDisplay, type TimecodeInput } from "./sync-timecode.js";

// formatOffsetMs is deliberately NOT retested here: sync-timecode.property.test.ts
// already pins its sign, format and boundary invariants. Stryker's vitest runner
// does not execute that file (it runs in the config's default `node`
// environment), so its mutants read as Survived — but a duplicate example test
// would buy a number, not confidence. What IS untested is buildTimecodeInput:
// the segmented widget's decomposition, its keyboard/wheel/touch adjustment
// paths and its hold-to-repeat timers.

interface Harness {
  root: TimecodeInput;
  changes: number[];
}

/** Mount the widget in the document (updateTimecodeDisplay looks it up by id). */
function mount(initialMs: number): Harness {
  const changes: number[] = [];
  const root = buildTimecodeInput(initialMs, (v) => {
    changes.push(v);
  }) as TimecodeInput;
  document.body.appendChild(root);
  return { root, changes };
}

function seg(root: ParentNode, label: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(`[aria-label="${label}"]`);
  if (found === null) {
    throw new Error(`missing segment: ${label}`);
  }
  return found;
}

function digits(root: ParentNode): string[] {
  return [
    seg(root, "Sign").textContent ?? "",
    seg(root, "Seconds").textContent ?? "",
    seg(root, "100ms").textContent ?? "",
    seg(root, "10ms").textContent ?? "",
    seg(root, "1ms").textContent ?? "",
  ];
}

function press(target: HTMLElement, type: string): Event {
  const ev = new MouseEvent(type, { bubbles: true, cancelable: true });
  target.dispatchEvent(ev);
  return ev;
}

function key(root: TimecodeInput, name: string): KeyboardEvent {
  const ev = new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true });
  root.handleKey(ev);
  return ev;
}

function wheel(target: HTMLElement, deltaY: number): Event {
  const ev = new Event("wheel", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "deltaY", { value: deltaY });
  target.dispatchEvent(ev);
  return ev;
}

/** happy-dom has no TouchEvent constructor with a touches list, so the touch
 *  point is attached to a plain cancelable Event — the handlers only read
 *  e.touches[0].clientY and e.preventDefault(). */
function touch(target: HTMLElement, type: string, clientY: number): Event {
  const ev = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "touches", { value: [{ clientY }] });
  target.dispatchEvent(ev);
  return ev;
}

beforeEach(() => {
  document.body.innerHTML = "";
});

afterEach(() => {
  vi.useRealTimers();
});

describe("buildTimecodeInput: how an offset is displayed", () => {
  it("splits a positive offset into sign, seconds and millisecond digits", () => {
    const h = mount(1234);

    expect(digits(h.root)).toEqual(["+", "1", "2", "3", "4"]);
  });

  it("shows a negative offset with a minus sign and its magnitude", () => {
    const h = mount(-1234);

    expect(digits(h.root)).toEqual(["-", "1", "2", "3", "4"]);
  });

  it("treats zero as a positive offset", () => {
    const h = mount(0);

    expect(digits(h.root)).toEqual(["+", "0", "0", "0", "0"]);
  });

  it("keeps leading zeros inside the millisecond digits", () => {
    const h = mount(1050);

    expect(digits(h.root)).toEqual(["+", "1", "0", "5", "0"]);
  });

  it("carries whole seconds out of the millisecond remainder", () => {
    const h = mount(65_432);

    expect(digits(h.root)).toEqual(["+", "65", "4", "3", "2"]);
  });

  it("announces the whole offset on every magnitude segment", () => {
    const h = mount(1234);

    expect([
      seg(h.root, "Seconds").getAttribute("aria-valuenow"),
      seg(h.root, "100ms").getAttribute("aria-valuenow"),
      seg(h.root, "10ms").getAttribute("aria-valuenow"),
      seg(h.root, "1ms").getAttribute("aria-valuenow"),
    ]).toEqual(["1234", "1234", "1234", "1234"]);
  });

  it("announces the offset in seconds as readable text", () => {
    const h = mount(-1234);

    expect(seg(h.root, "1ms").getAttribute("aria-valuetext")).toBe("-1.234 seconds");
  });

  it("exposes every magnitude segment as a focusable spinbutton", () => {
    const h = mount(0);

    expect([
      seg(h.root, "Seconds").getAttribute("role"),
      seg(h.root, "Seconds").getAttribute("tabindex"),
    ]).toEqual(["spinbutton", "0"]);
  });

  it("starts with the millisecond segment selected", () => {
    const h = mount(0);

    expect(seg(h.root, "1ms").className).toContain("tc-active");
  });
});

describe("buildTimecodeInput: keyboard adjustment", () => {
  it("adds the active segment's magnitude on arrow up", () => {
    const h = mount(1234);

    key(h.root, "ArrowUp");

    expect(digits(h.root)).toEqual(["+", "1", "2", "3", "5"]);
  });

  it("reports every adjustment to the caller", () => {
    const h = mount(1234);

    key(h.root, "ArrowUp");

    expect(h.changes).toEqual([1235]);
  });

  it("subtracts the active segment's magnitude on arrow down", () => {
    const h = mount(1234);

    key(h.root, "ArrowDown");

    expect(h.changes).toEqual([1233]);
  });

  it("swallows the arrow key so the dialog does not scroll", () => {
    const h = mount(1234);

    const ev = key(h.root, "ArrowUp");

    expect(ev.defaultPrevented).toBe(true);
  });

  it("swallows the down arrow too", () => {
    const h = mount(1234);

    const ev = key(h.root, "ArrowDown");

    expect(ev.defaultPrevented).toBe(true);
  });

  it("ignores keys other than the two arrows", () => {
    const h = mount(1234);

    key(h.root, "ArrowLeft");

    expect(h.changes).toEqual([]);
  });

  it("adjusts by whole seconds once the seconds segment is clicked", () => {
    const h = mount(1234);

    press(seg(h.root, "Seconds"), "click");
    key(h.root, "ArrowUp");

    expect(h.changes).toEqual([2234]);
  });

  it("adjusts by a hundred milliseconds once that segment is clicked", () => {
    const h = mount(1234);

    press(seg(h.root, "100ms"), "click");
    key(h.root, "ArrowUp");

    expect(h.changes).toEqual([1334]);
  });

  it("adjusts by ten milliseconds once that segment is clicked", () => {
    const h = mount(1234);

    press(seg(h.root, "10ms"), "click");
    key(h.root, "ArrowDown");

    expect(h.changes).toEqual([1224]);
  });

  it("selects a segment reached by keyboard focus", () => {
    const h = mount(1234);

    seg(h.root, "Seconds").dispatchEvent(new Event("focus"));
    key(h.root, "ArrowUp");

    expect(h.changes).toEqual([2234]);
  });

  it("marks the newly selected segment as active", () => {
    const h = mount(1234);

    press(seg(h.root, "Seconds"), "click");

    expect(seg(h.root, "Seconds").className).toContain("tc-active");
  });

  it("clears the active mark from the previously selected segment", () => {
    const h = mount(1234);

    press(seg(h.root, "Seconds"), "click");

    expect(seg(h.root, "1ms").className).not.toContain("tc-active");
  });

  it("keeps a mousedown on a segment from stealing the selection", () => {
    const h = mount(1234);

    const ev = press(seg(h.root, "Seconds"), "mousedown");

    expect(ev.defaultPrevented).toBe(true);
  });
});

describe("buildTimecodeInput: wheel adjustment", () => {
  it("increases the offset when the wheel turns up", () => {
    const h = mount(1234);

    wheel(seg(h.root, "100ms"), -1);

    expect(h.changes).toEqual([1334]);
  });

  it("decreases the offset when the wheel turns down", () => {
    const h = mount(1234);

    wheel(seg(h.root, "100ms"), 1);

    expect(h.changes).toEqual([1134]);
  });

  it("swallows the wheel event so the dialog does not scroll", () => {
    const h = mount(1234);

    const ev = wheel(seg(h.root, "10ms"), -1);

    expect(ev.defaultPrevented).toBe(true);
  });

  it("adjusts by the magnitude of the wheeled segment, not the selected one", () => {
    const h = mount(1234);

    wheel(seg(h.root, "Seconds"), -1);

    expect(h.changes).toEqual([2234]);
  });

  it("ignores a wheel event that carries no vertical movement", () => {
    const h = mount(1234);

    wheel(seg(h.root, "100ms"), 0);

    expect(h.changes).toEqual([]);
  });

  it("lets a purely horizontal scroll through", () => {
    const h = mount(1234);

    const ev = wheel(seg(h.root, "100ms"), 0);

    expect(ev.defaultPrevented).toBe(false);
  });
});

describe("buildTimecodeInput: the sign toggle", () => {
  it("flips a positive offset to negative when clicked", () => {
    const h = mount(1234);

    press(seg(h.root, "Sign"), "click");

    expect(digits(h.root)).toEqual(["-", "1", "2", "3", "4"]);
  });

  it("reports the flipped offset to the caller", () => {
    const h = mount(1234);

    press(seg(h.root, "Sign"), "click");

    expect(h.changes).toEqual([-1234]);
  });

  it("flips a negative offset back to positive", () => {
    const h = mount(-1234);

    press(seg(h.root, "Sign"), "click");

    expect(h.changes).toEqual([1234]);
  });

  it("flips the offset on Enter", () => {
    const h = mount(1234);

    seg(h.root, "Sign").dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", cancelable: true }),
    );

    expect(h.changes).toEqual([-1234]);
  });

  it("flips the offset on Space", () => {
    const h = mount(1234);

    seg(h.root, "Sign").dispatchEvent(new KeyboardEvent("keydown", { key: " ", cancelable: true }));

    expect(h.changes).toEqual([-1234]);
  });

  it("swallows Space so the page does not scroll", () => {
    const h = mount(1234);
    const ev = new KeyboardEvent("keydown", { key: " ", cancelable: true });

    seg(h.root, "Sign").dispatchEvent(ev);

    expect(ev.defaultPrevented).toBe(true);
  });

  it("ignores other keys on the sign", () => {
    const h = mount(1234);

    seg(h.root, "Sign").dispatchEvent(new KeyboardEvent("keydown", { key: "a", cancelable: true }));

    expect(h.changes).toEqual([]);
  });

  it("exposes the sign as a button rather than a spinbutton", () => {
    const h = mount(1234);

    expect(seg(h.root, "Sign").getAttribute("role")).toBe("button");
  });
});

describe("buildTimecodeInput: the chevron buttons", () => {
  it("adjusts once as soon as Increase is pressed", () => {
    const h = mount(1234);

    press(seg(h.root, "Increase"), "mousedown");

    expect(h.changes).toEqual([1235]);
  });

  it("adjusts downward as soon as Decrease is pressed", () => {
    const h = mount(1234);

    press(seg(h.root, "Decrease"), "mousedown");

    expect(h.changes).toEqual([1233]);
  });

  it("adjusts by the selected segment's magnitude", () => {
    const h = mount(1234);

    press(seg(h.root, "Seconds"), "click");
    press(seg(h.root, "Increase"), "mousedown");

    expect(h.changes).toEqual([2234]);
  });

  it("keeps the press from selecting text", () => {
    const h = mount(1234);

    const ev = press(seg(h.root, "Increase"), "mousedown");

    expect(ev.defaultPrevented).toBe(true);
  });

  it("repeats while the button is held", () => {
    vi.useFakeTimers();
    const h = mount(0);

    press(seg(h.root, "Increase"), "mousedown");
    vi.advanceTimersByTime(400);

    expect(h.changes).toEqual([1, 2]);
  });

  it("accelerates the repeat the longer the button is held", () => {
    vi.useFakeTimers();
    const h = mount(0);

    press(seg(h.root, "Increase"), "mousedown");
    vi.advanceTimersByTime(400);
    vi.advanceTimersByTime(300);

    expect(h.changes).toEqual([1, 2, 3]);
  });

  it("stops repeating when the button is released", () => {
    vi.useFakeTimers();
    const h = mount(0);

    press(seg(h.root, "Increase"), "mousedown");
    press(seg(h.root, "Increase"), "mouseup");
    vi.advanceTimersByTime(2000);

    expect(h.changes).toEqual([1]);
  });

  it("stops repeating when the pointer leaves the button", () => {
    vi.useFakeTimers();
    const h = mount(0);

    press(seg(h.root, "Increase"), "mousedown");
    press(seg(h.root, "Increase"), "mouseleave");
    vi.advanceTimersByTime(2000);

    expect(h.changes).toEqual([1]);
  });

  it("starts repeating from a touch press", () => {
    vi.useFakeTimers();
    const h = mount(0);

    touch(seg(h.root, "Increase"), "touchstart", 0);
    vi.advanceTimersByTime(400);

    expect(h.changes).toEqual([1, 2]);
  });

  it("stops repeating when the touch ends", () => {
    vi.useFakeTimers();
    const h = mount(0);

    touch(seg(h.root, "Increase"), "touchstart", 0);
    touch(seg(h.root, "Increase"), "touchend", 0);
    vi.advanceTimersByTime(2000);

    expect(h.changes).toEqual([1]);
  });

  it("stops repeating when the touch is cancelled", () => {
    vi.useFakeTimers();
    const h = mount(0);

    touch(seg(h.root, "Increase"), "touchstart", 0);
    touch(seg(h.root, "Increase"), "touchcancel", 0);
    vi.advanceTimersByTime(2000);

    expect(h.changes).toEqual([1]);
  });

  it("stops a pending repeat when the widget is disposed", () => {
    vi.useFakeTimers();
    const h = mount(0);

    press(seg(h.root, "Increase"), "mousedown");
    h.root.dispose();
    vi.advanceTimersByTime(2000);

    expect(h.changes).toEqual([1]);
  });

  it("survives a second disposal", () => {
    vi.useFakeTimers();
    const h = mount(0);

    press(seg(h.root, "Decrease"), "mousedown");
    h.root.dispose();
    h.root.dispose();
    vi.advanceTimersByTime(2000);

    expect(h.changes).toEqual([-1]);
  });
});

describe("buildTimecodeInput: touch drag", () => {
  it("adjusts once per twenty pixels of upward swipe", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 80);

    expect(h.changes).toEqual([1]);
  });

  it("adjusts twice for a swipe covering two thresholds", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 55);

    expect(h.changes).toEqual([2]);
  });

  it("ignores a swipe shorter than the threshold", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 85);

    expect(h.changes).toEqual([]);
  });

  it("keeps the unconsumed remainder for the next move", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 85);
    touch(target, "touchmove", 75);

    expect(h.changes).toEqual([1]);
  });

  it("subtracts on a downward swipe", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 120);

    expect(h.changes).toEqual([-1]);
  });

  it("adjusts by the dragged segment's magnitude", () => {
    const h = mount(0);
    const target = seg(h.root, "Seconds");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 80);

    expect(h.changes).toEqual([1000]);
  });

  it("swallows the move so the page does not scroll with the drag", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    const ev = touch(target, "touchmove", 80);

    expect(ev.defaultPrevented).toBe(true);
  });

  it("consumes one threshold per adjustment across a long drag", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 80);
    touch(target, "touchmove", 60);
    touch(target, "touchmove", 40);

    expect(h.changes).toEqual([1, 2, 3]);
  });

  it("measures each swipe from its own start point", () => {
    const h = mount(0);
    const target = seg(h.root, "1ms");

    touch(target, "touchstart", 100);
    touch(target, "touchmove", 80);
    touch(target, "touchstart", 500);
    touch(target, "touchmove", 480);

    expect(h.changes).toEqual([1, 2]);
  });
});

describe("updateTimecodeDisplay", () => {
  it("writes a new offset into the mounted widget", () => {
    const h = mount(1234);

    updateTimecodeDisplay(-2500);

    expect(digits(h.root)).toEqual(["-", "2", "5", "0", "0"]);
  });

  it("does not report an externally set offset as a user change", () => {
    const h = mount(1234);

    updateTimecodeDisplay(-2500);

    expect(h.changes).toEqual([]);
  });

  it("leaves the arrow keys operating on the externally set offset", () => {
    const h = mount(1234);

    updateTimecodeDisplay(5000);
    key(h.root, "ArrowUp");

    expect(h.changes).toEqual([5001]);
  });

  it("does nothing when no widget is mounted", () => {
    expect(() => {
      updateTimecodeDisplay(1000);
    }).not.toThrow();
  });
});
