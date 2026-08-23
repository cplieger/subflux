// popover-menu.test.ts — the header dropdowns' breakpoint decision.
//
// This module exists because the popover is positioned in JS, so the
// desktop/mobile switch that used to be a CSS media query is now a matchMedia
// handler. That makes it the one place where a responsive regression is
// invisible to CSS review, and its own header names the two failure modes it
// was written against: patching the LIVE controller instead of disposing and
// rebuilding it, and a NAMED listener so dispose() can remove it (an anonymous
// one would outlive the controller and patch a disposed one on the next flip).
// Both are asserted here.
//
// The primitive is doubled, because the subject is the options subflux passes,
// not whether the library positions correctly. matchMedia is stubbed at the
// site so a test states the viewport it means instead of inheriting the runner's
// 1280x720 one.
import { describe, it, expect, beforeEach, vi } from "vitest";
import { createMenuPopover } from "./popover-menu.js";

interface CapturedOptions {
  placement?: string;
  align?: string;
  offset?: number;
  margin?: number;
  stretch?: string;
  haspopup?: "menu" | true;
  onOpen?: () => void;
}

const popover = vi.hoisted(() => ({
  created: [] as unknown[],
  patches: [] as unknown[],
  calls: [] as string[],
  isOpen: false,
}));
vi.mock("@cplieger/ui-primitives/popover", () => ({
  createPopover: (_anchor: unknown, _panel: unknown, opts: unknown) => {
    popover.created.push(opts);
    const controller = {
      show: () => popover.calls.push("show"),
      hide: () => popover.calls.push("hide"),
      toggle: () => popover.calls.push("toggle"),
      reposition: () => popover.calls.push("reposition"),
      get isOpen() {
        return popover.isOpen;
      },
      el: document.createElement("div"),
      setOptions: (patch: unknown) => {
        popover.patches.push(patch);
        popover.calls.push("setOptions");
      },
      dispose: () => popover.calls.push("dispose"),
    };
    return controller;
  },
}));

/** A controllable MediaQueryList: `flip` changes `matches` and fires `change`,
 *  which is what a real viewport crossing 600px does. */
class FakeMediaQueryList {
  matches: boolean;
  readonly media: string;
  private readonly listeners = new Set<() => void>();
  removed = 0;

  constructor(media: string, matches: boolean) {
    this.media = media;
    this.matches = matches;
  }
  addEventListener(_type: string, fn: () => void): void {
    this.listeners.add(fn);
  }
  removeEventListener(_type: string, fn: () => void): void {
    this.removed++;
    this.listeners.delete(fn);
  }
  flip(matches: boolean): void {
    this.matches = matches;
    for (const fn of this.listeners) {
      fn();
    }
  }
  get listenerCount(): number {
    return this.listeners.size;
  }
}

let mql: FakeMediaQueryList;

/** Install matchMedia answering `narrow` for the module's own query. A
 *  different query would be a wiring bug, so answering it would hide one. */
function stubViewport(narrow: boolean): void {
  mql = new FakeMediaQueryList("(width < 600px)", narrow);
  vi.stubGlobal("matchMedia", (q: string) => {
    if (q !== "(width < 600px)") {
      throw new Error(`unexpected media query ${q}`);
    }
    return mql;
  });
}

/** A header 60px tall with a 20px trigger flush at its top, so the header's
 *  bottom sits exactly 40px below the trigger's bottom. */
function mountHeader(): { anchor: HTMLElement; panel: HTMLElement } {
  document.body.replaceChildren();
  const header = document.createElement("header");
  header.style.cssText = "position:fixed;inset-block-start:0;inset-inline:0;height:60px;margin:0";
  const anchor = document.createElement("button");
  anchor.style.cssText =
    "position:absolute;inset-block-start:0;inset-inline-start:0;height:20px;width:20px;margin:0;padding:0;border:0";
  header.appendChild(anchor);
  const panel = document.createElement("div");
  document.body.append(header, panel);
  return { anchor, panel };
}

function created(): CapturedOptions {
  return popover.created[0] as CapturedOptions;
}

beforeEach(() => {
  popover.created.length = 0;
  popover.patches.length = 0;
  popover.calls.length = 0;
  popover.isOpen = false;
  stubViewport(false);
});

describe("createMenuPopover placement", () => {
  it("drops a content-sized panel below the trigger on desktop", () => {
    const { anchor, panel } = mountHeader();

    createMenuPopover(anchor, panel);

    expect(created().placement).toBe("bottom");
    expect(created().align).toBe("end");
    expect(created().offset).toBe(6);
    expect(created().margin).toBe(8);
  });

  it("passes no stretch on desktop, which is what keeps the panel content-sized", () => {
    const { anchor, panel } = mountHeader();

    createMenuPopover(anchor, panel);

    expect("stretch" in created()).toBe(false);
  });

  it("goes full-bleed and flush to the viewport edges below 600px", () => {
    stubViewport(true);
    const { anchor, panel } = mountHeader();

    createMenuPopover(anchor, panel);

    expect(created().stretch).toBe("viewport");
    expect(created().margin).toBe(0);
  });

  it("measures the mobile offset from the trigger's bottom to the header's", () => {
    // The old CSS anchored the panel to the HEADER; ARIA has to stay on the
    // button, so the gap is measured instead. The fixture's header is 60px tall
    // with a 20px trigger at its top.
    stubViewport(true);
    const { anchor, panel } = mountHeader();

    createMenuPopover(anchor, panel);

    expect(created().offset).toBe(40);
  });

  it("uses a zero mobile offset for a trigger outside any header", () => {
    stubViewport(true);
    document.body.replaceChildren();
    const anchor = document.createElement("button");
    const panel = document.createElement("div");
    document.body.append(anchor, panel);

    createMenuPopover(anchor, panel);

    expect(created().offset).toBe(0);
  });

  it("advertises aria-haspopup=true by default and the caller's value when given", () => {
    const { anchor, panel } = mountHeader();

    createMenuPopover(anchor, panel);
    expect(created().haspopup).toBe(true);

    popover.created.length = 0;
    createMenuPopover(anchor, panel, { haspopup: "menu" });
    expect(created().haspopup).toBe("menu");
  });
});

describe("createMenuPopover onOpen", () => {
  it("rebuilds the caller's content and then re-measures the panel", () => {
    const { anchor, panel } = mountHeader();
    const rebuild = vi.fn();
    createMenuPopover(anchor, panel, { onOpen: rebuild });

    created().onOpen?.();

    // Placement ran before the rebuild, so a reposition after it is the whole
    // point: without it the panel is clamped against its pre-rebuild height.
    expect(rebuild).toHaveBeenCalledTimes(1);
    expect(popover.calls).toStrictEqual(["reposition"]);
  });

  it("still re-measures when the caller passed no onOpen", () => {
    const { anchor, panel } = mountHeader();
    createMenuPopover(anchor, panel);

    created().onOpen?.();

    expect(popover.calls).toStrictEqual(["reposition"]);
  });
});

describe("createMenuPopover breakpoint flips", () => {
  it("patches the live controller instead of rebuilding it", () => {
    const { anchor, panel } = mountHeader();
    createMenuPopover(anchor, panel);

    mql.flip(true);

    expect(popover.patches).toStrictEqual([{ stretch: "viewport", offset: 40, margin: 0 }]);
    expect(popover.calls).toStrictEqual(["setOptions"]);
    expect(popover.created).toHaveLength(1);
  });

  it("clears stretch explicitly on the way back to desktop", () => {
    stubViewport(true);
    const { anchor, panel } = mountHeader();
    createMenuPopover(anchor, panel);

    mql.flip(false);

    // An explicit undefined is setOptions' documented way to clear full-bleed;
    // omitting the key would leave the panel stretched on desktop.
    expect(popover.patches).toStrictEqual([{ stretch: undefined, offset: 6, margin: 8 }]);
  });

  it("stops patching once disposed", () => {
    const { anchor, panel } = mountHeader();
    const menu = createMenuPopover(anchor, panel);

    menu.dispose();
    mql.flip(true);

    expect(popover.calls).toStrictEqual(["dispose"]);
    expect(mql.listenerCount).toBe(0);
  });
});

describe("createMenuPopover facade", () => {
  it("forwards toggle, hide and reposition to the controller", () => {
    const { anchor, panel } = mountHeader();
    const menu = createMenuPopover(anchor, panel);

    menu.toggle();
    menu.hide();
    menu.reposition();

    expect(popover.calls).toStrictEqual(["toggle", "hide", "reposition"]);
  });

  it("reads isOpen from the controller rather than caching it", () => {
    const { anchor, panel } = mountHeader();
    const menu = createMenuPopover(anchor, panel);
    expect(menu.isOpen).toBe(false);

    popover.isOpen = true;

    expect(menu.isOpen).toBe(true);
  });
});
