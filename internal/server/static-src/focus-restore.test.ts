import { describe, it, expect, afterEach, onTestFinished } from "vitest";

import { FOCUS_KEY, trackFocus, captureReaderState } from "./focus-restore.js";

// Browser Mode isolates per FILE, not per test, and nothing here clears the
// page: a host left behind decides what the next case's queries find.
const hosts: HTMLElement[] = [];

afterEach(() => {
  while (hosts.length > 0) {
    hosts.pop()?.remove();
  }
});

function host(): HTMLElement {
  const el = document.createElement("div");
  document.body.appendChild(el);
  hosts.push(el);
  return el;
}

function keyed(key: string): HTMLButtonElement {
  const btn = document.createElement("button");
  btn.setAttribute(FOCUS_KEY, key);
  btn.textContent = key;
  return btn;
}

function control(container: HTMLElement, key: string): HTMLElement {
  const found = container.querySelector<HTMLElement>(`[${FOCUS_KEY}="${key}"]`);
  if (found === null) {
    throw new Error(`missing control: ${key}`);
  }
  return found;
}

function tall(): HTMLElement {
  const filler = document.createElement("div");
  filler.style.height = "500px";
  return filler;
}

describe("focus-restore: the tracked path", () => {
  it("focuses the rebuilt control the tracker last saw, not the one focus moved to", () => {
    const container = host();
    trackFocus(container);
    container.replaceChildren(keyed("first"), keyed("second"));
    const before = control(container, "second");
    before.focus();
    // Production captures while focus sits in a sub-dialog that has not closed
    // yet, so the tracker rather than document.activeElement is what knows
    // which control the reader left.
    const elsewhere = document.createElement("button");
    document.body.appendChild(elsewhere);
    onTestFinished(() => {
      elsewhere.remove();
    });
    elsewhere.focus();

    const restore = captureReaderState(container);
    container.replaceChildren(keyed("first"), keyed("second"));
    restore();

    const after = control(container, "second");
    expect([document.activeElement === after, after === before]).toEqual([true, false]);
  });
});

describe("focus-restore: the untracked path", () => {
  it("resolves the key off document.activeElement", () => {
    const container = host();
    container.replaceChildren(keyed("only"));
    control(container, "only").focus();

    const restore = captureReaderState(container);
    container.replaceChildren(keyed("only"));
    restore();

    expect(document.activeElement).toBe(control(container, "only"));
  });
});

describe("focus-restore: a key the rebuild does not carry", () => {
  it("leaves focus where the install put it", () => {
    const container = host();
    trackFocus(container);
    container.replaceChildren(keyed("gone"));
    control(container, "gone").focus();

    const restore = captureReaderState(container);
    container.replaceChildren(keyed("other"));
    restore();

    expect(document.activeElement).toBe(document.body);
  });
});

describe("focus-restore: the scroll offset", () => {
  it("puts the scroller back where the reader left it", () => {
    const container = host();
    container.style.height = "50px";
    container.style.overflowY = "auto";
    container.replaceChildren(tall());
    container.scrollTop = 80;

    const restore = captureReaderState(container, { scrollEl: container });
    container.replaceChildren();
    // Reading a layout property while the container is empty is what makes the
    // engine clamp the offset, the way a re-render whose new content is shorter
    // does; replacing tall content with equally tall content in one statement
    // never runs layout in between and keeps the offset by accident.
    void container.scrollHeight;
    container.replaceChildren(tall());
    const afterInstall = container.scrollTop;
    restore();

    expect([afterInstall, container.scrollTop]).toEqual([0, 80]);
  });

  it("carries the offset onto a scroller the install replaced", () => {
    const outer = host();
    const build = (): HTMLElement => {
      const scroller = document.createElement("div");
      scroller.className = "scroller";
      scroller.style.height = "50px";
      scroller.style.overflowY = "auto";
      scroller.appendChild(tall());
      return scroller;
    };
    outer.replaceChildren(build());
    const first = outer.querySelector<HTMLElement>(".scroller");
    first!.scrollTop = 80;

    const restore = captureReaderState(outer, {
      scrollEl: () => outer.querySelector<HTMLElement>(".scroller"),
    });
    // The whole scroller is discarded, so an offset written to the element the
    // capture read would land on a detached node.
    outer.replaceChildren(build());
    const second = outer.querySelector<HTMLElement>(".scroller");
    const afterInstall = second!.scrollTop;
    restore();

    expect([first === second, afterInstall, second!.scrollTop]).toEqual([false, 0, 80]);
  });
});

describe("focus-restore: a modal above the container", () => {
  it("claims focus back when the blocking dialog closes", async () => {
    const container = host();
    trackFocus(container);
    container.replaceChildren(keyed("save"));
    control(container, "save").focus();

    const blocker = document.createElement("dialog");
    const inside = document.createElement("button");
    blocker.appendChild(inside);
    document.body.appendChild(blocker);
    onTestFinished(() => {
      blocker.remove();
    });
    blocker.showModal();
    inside.focus();

    const restore = captureReaderState(container);
    container.replaceChildren(keyed("save"));
    restore();
    const refused = document.activeElement === inside;

    const closed = new Promise<void>((resolve) => {
      blocker.addEventListener("close", () => resolve(), { once: true });
    });
    blocker.close();
    await closed;

    expect([refused, document.activeElement === control(container, "save")]).toEqual([true, true]);
  });
});
