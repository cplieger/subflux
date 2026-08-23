// @vitest-environment happy-dom
// The DOM-building half of utils.ts, which utils.test.ts deliberately skips
// ("DOM-dependent functions are excluded"): the view-transition wrapper, the
// empty-state placeholder and the language <select>.
import { describe, it, expect } from "vitest";

import { viewTransition, emptyState, langSelect } from "./utils.js";
import { LANGUAGES } from "./languages.js";

/** The ui-primitives view-transition wrapper always runs through a promise
 *  chain, even on the no-startViewTransition path, so the callback lands a
 *  macrotask later at the earliest. */
async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe("viewTransition", () => {
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
