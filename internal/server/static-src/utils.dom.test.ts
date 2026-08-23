// The DOM-building half of utils.ts, which utils.test.ts deliberately skips
// ("DOM-dependent functions are excluded"): the view-transition wrapper, the
// empty-state placeholder and the language <select>.
import { describe, it, expect, beforeEach, afterEach } from "vitest";

import { viewTransition, emptyState, langSelect } from "./utils.js";
import { LANGUAGES } from "./languages.js";

/** The ui-primitives view-transition wrapper always runs through a promise
 *  chain, so the callback lands a macrotask later at the earliest. */
async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe("viewTransition", () => {
  // These two tests are about the wrapper's API-ABSENT path -- the branch
  // @cplieger/ui-primitives takes when `document.startViewTransition` is missing,
  // where it awaits `fn()` directly and the callback lands within the macrotask
  // settle() waits for. Chromium PROVIDES startViewTransition, so left alone the
  // library drives its real transition instead, whose update callback needs a
  // rendering opportunity; utils.ts's viewTransition() is declared `(fn) => void`
  // and intentionally discards the promise, so there is nothing for a test to
  // await and any wait would be a guessed frame count.
  //
  // So the premise is made explicit here instead of being inherited from the
  // environment: remove the one capability for the duration of this suite.
  // Assignment cannot do it -- the method lives on Document.prototype, so an
  // OWN-property shadow is what creates the absence, and deleting that shadow
  // afterwards is what restores the real method (there is no own descriptor to
  // put back, which is why the restore is a delete rather than a defineProperty).
  let savedDescriptor: PropertyDescriptor | undefined;

  beforeEach(() => {
    savedDescriptor = Object.getOwnPropertyDescriptor(document, "startViewTransition");
    Object.defineProperty(document, "startViewTransition", {
      value: undefined,
      configurable: true,
      writable: true,
    });
  });

  afterEach(() => {
    if (savedDescriptor) {
      Object.defineProperty(document, "startViewTransition", savedDescriptor);
    } else {
      Reflect.deleteProperty(document, "startViewTransition");
    }
  });

  it("runs the DOM update it is given", async () => {
    let ran = false;

    viewTransition(() => {
      ran = true;
    });
    await settle();

    expect(ran).toBe(true);
  });

  it("applies the update the caller made, so the new DOM is visible", async () => {
    const host = document.createElement("div");
    document.body.appendChild(host);

    viewTransition(() => {
      host.textContent = "swapped";
    });
    await settle();

    expect(host.textContent).toBe("swapped");
  });
});

describe("emptyState", () => {
  it("renders the message", () => {
    const frag = emptyState("Nothing here yet");

    expect(frag.textContent).toBe("Nothing here yet");
  });

  it("offers the action button when both a label and a handler are given", () => {
    const frag = emptyState("Nothing here yet", "Scan now", () => undefined);

    expect(frag.querySelector("button")?.textContent).toBe("Scan now");
  });

  it("runs the handler when the action button is clicked", () => {
    let clicked = 0;
    const frag = emptyState("Nothing here yet", "Scan now", () => {
      clicked += 1;
    });

    frag.querySelector("button")?.click();

    expect(clicked).toBe(1);
  });

  it("renders no button for a label with no handler, which would do nothing", () => {
    const frag = emptyState("Nothing here yet", "Scan now");

    expect(frag.querySelector("button")).toBeNull();
  });

  it("renders no button for a handler with no label, which nothing would name", () => {
    const frag = emptyState("Nothing here yet", undefined, () => undefined);

    expect(frag.querySelector("button")).toBeNull();
  });
});

describe("langSelect", () => {
  it("offers every supported language", () => {
    const sel = langSelect("lang-field");

    expect(sel.options).toHaveLength(LANGUAGES.length);
  });

  it("labels each option with its code and name", () => {
    const sel = langSelect("lang-field");

    expect([...sel.options].slice(0, 3).map((o) => [o.value, o.textContent])).toEqual([
      ["en", "en \u2014 English"],
      ["fr", "fr \u2014 French"],
      ["es", "es \u2014 Spanish"],
    ]);
  });

  it("preselects the requested language", () => {
    const sel = langSelect(null, "fr");

    expect(sel.value).toBe("fr");
  });

  it("selects the first language when none is requested", () => {
    const sel = langSelect(null);

    expect(sel.value).toBe("en");
  });
});
