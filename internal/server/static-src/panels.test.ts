// panels.ts owns the property every other reader rests on: a panel's controls
// are reachable through the accessor whether or not the panel is attached. The
// router detaches the departing panel, so a reader that went through the
// document would hold `null` for the whole time that page is off screen.
import { describe, it, expect, afterEach } from "vitest";
import { _resetPanelsForTest, historyPanel, insertNavButton, libraryPanel } from "./panels.js";
import { contentView, historyView } from "./view-scope.js";

// Browser Mode isolates per FILE, not per test, and nothing clears the body for
// us: a panel left attached would answer another case's document lookups.
afterEach(() => {
  document.body.replaceChildren();
  _resetPanelsForTest();
});

describe("panels: the built shell", () => {
  it("exposes the library panel's four controls, each carrying its own id", () => {
    const p = libraryPanel();

    expect([p.filter.id, p.missingOnly.id, p.typeFilter.id, p.sort.id]).toEqual([
      "cov-filter",
      "cov-missing",
      "cov-type-filter",
      "cov-sort",
    ]);
    expect([p.heading.id, p.content.id]).toEqual(["lib-heading", "coverageContent"]);
    expect(p.root.dataset["page"]).toBe("coverage");
    expect(p.head.className).toBe("card-head");
    expect(p.controls.parentNode).toBe(p.head);
  });

  it("exposes the history panel's four controls, each carrying its own id", () => {
    const p = historyPanel();

    expect([p.filter.id, p.type.id, p.lang.id, p.provider.id]).toEqual([
      "h-filter",
      "h-type",
      "h-lang",
      "h-provider",
    ]);
    expect([p.heading.id, p.content.id]).toEqual(["hist-heading", "historyContent"]);
    expect(p.root.dataset["page"]).toBe("history");
  });

  it("carries no hidden attribute anywhere in either panel", () => {
    // The shipped shell authors no region for the app to reveal, in ANY
    // spelling — including the bracket form no grep pattern would see.
    for (const root of [libraryPanel().root, historyPanel().root]) {
      expect(root.hasAttribute("hidden")).toBe(false);
      expect(root.querySelector("[hidden]")).toBeNull();
    }
  });

  it("keeps a detached panel's controls reachable, and their values, through the accessor", () => {
    document.body.replaceChildren(libraryPanel().root);
    const filter = libraryPanel().filter;
    filter.value = "the wire";

    libraryPanel().root.remove();

    // The document cannot see it any more; the accessor still can, and it is
    // the SAME element with the reader's text in it.
    expect(document.getElementById("cov-filter")).toBeNull();
    expect(libraryPanel().filter).toBe(filter);
    expect(libraryPanel().filter.value).toBe("the wire");
  });
});

describe("panels: _resetPanelsForTest", () => {
  it("rebuilds both panels rather than handing back the old ones", () => {
    const beforeLibrary = libraryPanel();
    const beforeHistory = historyPanel();
    const beforeFilter = libraryPanel().filter;

    _resetPanelsForTest();

    expect(libraryPanel()).not.toBe(beforeLibrary);
    expect(historyPanel()).not.toBe(beforeHistory);
    expect(libraryPanel().filter).not.toBe(beforeFilter);
  });

  it("releases both view hosts, so the next render mounts instead of early-returning", () => {
    // The view ids are coverage.ts's and history.ts's own (VIEW_LIBRARY /
    // VIEW_HISTORY); both mount paths gate on OCCUPANCY, so an occupant left
    // behind makes ensureMounted skip and render into the old panel.
    contentView.mount("library");
    historyView.mount("history");

    _resetPanelsForTest();

    expect(contentView.scopeFor("library")).toBeNull();
    expect(historyView.scopeFor("history")).toBeNull();
  });
});

describe("panels: insertNavButton()", () => {
  function navButton(): HTMLElement {
    const b = document.createElement("button");
    b.textContent = "History";
    return b;
  }

  function arrLink(): HTMLElement {
    const a = document.createElement("a");
    a.textContent = "Sonarr";
    a.setAttribute("data-nav", "arr");
    return a;
  }

  it("inserts the button ahead of the arr link", () => {
    // The arr link is always the head's last child (configurePanel appends it),
    // so a nav button that lands after it reads as trailing the panel's chrome.
    const arr = libraryPanel().head.appendChild(arrLink());
    const btn = navButton();

    insertNavButton(btn);

    expect(btn.nextElementSibling).toBe(arr);
  });

  it("appends the button when there is no arr link to sit before", () => {
    const btn = navButton();

    insertNavButton(btn);

    expect(libraryPanel().head.lastElementChild).toBe(btn);
  });
});
