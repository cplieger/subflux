// @vitest-environment happy-dom
import { describe, it, beforeEach, afterEach, expect, vi } from "vitest";
import {
  text,
  icon,
  option,
  emptyDiv,
  errDiv,
  dialogHead,
  onBackdropClose,
  insertNavButton,
  $,
  confirm,
  closeDialog,
} from "./dom.js";
import { _resetForTest as resetConfirm } from "@cplieger/ui-primitives/ask";

// `el` is not tested here: dom.ts only re-exports it from @cplieger/reactive
// (`export { el }`), so its factory belongs to that package's suite. Every
// branch of it is already exercised through real call sites in this package —
// tag + className and string children by the element builders below, `on*`
// handlers by dialogHead(), the BOOL_PROPS property path by status.test.ts
// (`disabled`) and security.test.ts (`hidden`), and the null-child skip by
// search.test.ts (a row's absent [HI]/on-disk spans). Adding a second,
// dependency-facing copy here would pin another repo's internals.

// confirm() now delegates to the @cplieger/ui-primitives ask primitive
// (boolean shape), which owns a reused <dialog class="uip-ask"> appended to
// the body. These
// tests cover the subflux WRAPPER's contract: (title, message, label) maps onto
// the primitive and the returned promise resolves true/false. resetConfirm()
// clears the primitive's cached dialog between tests.
describe("dom: confirm()", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = "";
    resetConfirm();
  });

  afterEach(() => {
    resetConfirm();
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
  });

  function dlg(): HTMLDialogElement | null {
    return document.querySelector<HTMLDialogElement>(".uip-ask");
  }

  function okBtn(): HTMLButtonElement | null {
    return document.querySelector<HTMLButtonElement>(".uip-ask-ok");
  }

  function cancelBtn(): HTMLButtonElement | null {
    return document.querySelector<HTMLButtonElement>(".uip-ask-cancel");
  }

  it("opens a modal showing the title and message", () => {
    void confirm("Delete file", "This cannot be undone");
    expect(dlg()?.open).toBe(true);
    expect(document.querySelector(".uip-ask-title")?.textContent).toBe("Delete file");
    expect(document.querySelector(".uip-ask-msg")?.textContent).toBe("This cannot be undone");
  });

  it("uses a custom confirm-button label when provided", () => {
    void confirm("Title", "Body", "Yes, delete");
    expect(okBtn()?.textContent).toBe("Yes, delete");
  });

  it("resolves true when the confirm button is clicked", async () => {
    const p = confirm("Title", "Body");
    okBtn()?.click();
    await expect(p).resolves.toBe(true);
  });

  it("resolves false when the cancel button is clicked", async () => {
    const p = confirm("Title", "Body");
    cancelBtn()?.click();
    await expect(p).resolves.toBe(false);
  });

  it("resolves false on a backdrop press (mousedown then mouseup on the dialog)", async () => {
    const p = confirm("Title", "Body");
    const d = dlg();
    d?.dispatchEvent(new MouseEvent("mousedown"));
    d?.dispatchEvent(new MouseEvent("mouseup"));
    await expect(p).resolves.toBe(false);
  });

  it("resolves false when the dialog emits a native cancel (Escape)", async () => {
    const p = confirm("Title", "Body");
    dlg()?.dispatchEvent(new Event("cancel"));
    await expect(p).resolves.toBe(false);
  });
});

// closeDialog() now delegates to the ui-primitives dialog primitive: it adds a
// namespaced `is-leaving` class, waits for the CSS transition (400ms fallback),
// then close()s. The subflux skin maps `dialog.is-leaving` to the visual exit.
describe("dom: closeDialog()", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = '<dialog id="dlg"></dialog>';
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
  });

  function dlg(): HTMLDialogElement {
    return document.getElementById("dlg") as HTMLDialogElement;
  }

  it("leaves an already-closed dialog closed and unmarked", () => {
    const d = dlg();
    expect(d.open).toBe(false);
    closeDialog(d);
    expect(d.classList.contains("is-leaving")).toBe(false);
    expect(d.open).toBe(false);
  });

  it("adds is-leaving and closes via the fallback timer when no transitionend fires", () => {
    const d = dlg();
    d.showModal();
    closeDialog(d);
    // Fade-out started; the dialog stays open until the transition ends (or the
    // fallback timer fires).
    expect(d.classList.contains("is-leaving")).toBe(true);
    expect(d.open).toBe(true);
    vi.advanceTimersByTime(400);
    expect(d.open).toBe(false);
  });

  it("closes on transitionend and does not close a second time on the fallback timer", () => {
    const d = dlg();
    d.showModal();
    const closeSpy = vi.spyOn(d, "close");
    closeDialog(d);
    d.dispatchEvent(new Event("transitionend"));
    expect(d.open).toBe(false);
    vi.advanceTimersByTime(400);
    expect(closeSpy).toHaveBeenCalledTimes(1);
  });
});

describe("dom: element builders", () => {
  it("names an icon span by the icon it carries", () => {
    expect(icon("close").className).toBe("icon icon-close");
  });

  it("builds an empty-state div carrying its message", () => {
    const d = emptyDiv("nothing here");
    expect(d.className).toBe("empty");
    expect(d.textContent).toBe("nothing here");
  });

  it("marks an error div with the status the stylesheet keys on", () => {
    const d = errDiv("boom");
    expect(d.className).toBe("empty");
    expect(d.getAttribute("data-status")).toBe("err");
    expect(d.textContent).toBe("boom");
  });

  it("keeps an option's value and its label apart", () => {
    const o = option("fr", "fr \u2014 French");
    expect(o.value).toBe("fr");
    expect(o.textContent).toBe("fr \u2014 French");
  });

  it("turns markup-looking input into literal text, never into elements", () => {
    const t = text("<b>not bold</b>");
    expect(t.nodeType).toBe(Node.TEXT_NODE);
    expect(t.textContent).toBe("<b>not bold</b>");
  });
});

describe("dom: dialogHead()", () => {
  it("renders a string title as a heading beside a close button", () => {
    const closeFn = vi.fn();
    const head = dialogHead("Security", closeFn);
    expect(head.className).toBe("dlg-head");
    expect(head.querySelector("h2")?.textContent).toBe("Security");

    head.querySelector<HTMLButtonElement>('button[aria-label="Close"]')?.click();

    expect(closeFn).toHaveBeenCalledTimes(1);
  });

  it("passes an element title straight through without wrapping it", () => {
    const title = document.createElement("span");
    title.textContent = "Sync S01";
    const head = dialogHead(title, () => undefined);
    expect(head.querySelector("h2")).toBeNull();
    expect(head.firstElementChild).toBe(title);
  });
});

describe("dom: onBackdropClose()", () => {
  beforeEach(() => {
    document.body.innerHTML = '<dialog id="dlg"></dialog>';
  });

  function dlg(): HTMLDialogElement {
    return document.getElementById("dlg") as HTMLDialogElement;
  }

  function press(d: HTMLDialogElement): void {
    d.dispatchEvent(new MouseEvent("mousedown"));
    d.dispatchEvent(new MouseEvent("mouseup"));
  }

  it("closes on a backdrop press, and only on the first one", () => {
    const closeFn = vi.fn();
    const d = dlg();
    d.showModal();
    onBackdropClose(d, closeFn);

    press(d);
    expect(closeFn).toHaveBeenCalledTimes(1);

    press(d);
    expect(closeFn).toHaveBeenCalledTimes(1);
  });

  it("detaches its listeners when the dialog closes by another route", () => {
    const closeFn = vi.fn();
    const d = dlg();
    d.showModal();
    onBackdropClose(d, closeFn);

    d.dispatchEvent(new Event("close"));
    press(d);

    expect(closeFn).not.toHaveBeenCalled();
  });
});

describe("dom: the $ registry", () => {
  it("returns the registered element when it is present", () => {
    document.body.innerHTML = '<div id="statusPopup"></div>';
    expect($.statusPopup).toBe(document.getElementById("statusPopup"));
  });

  it("fails fast and names the element rather than handing back null", () => {
    document.body.innerHTML = "";
    expect(() => $.statusPopup).toThrow("Missing element: #statusPopup");
  });
});

describe("dom: insertNavButton()", () => {
  beforeEach(() => {
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head">' +
      '<h1>Library</h1><a data-nav="arr">Sonarr</a>' +
      "</div></div>";
  });

  function headText(): (string | null)[] {
    return [...document.querySelectorAll("#coveragePanel .card-head > *")].map(
      (n) => n.textContent,
    );
  }

  function navButton(): HTMLElement {
    const b = document.createElement("button");
    b.textContent = "History";
    return b;
  }

  it("inserts the button ahead of the arr link", () => {
    insertNavButton(navButton());
    expect(headText()).toEqual(["Library", "History", "Sonarr"]);
  });

  it("appends the button when there is no arr link to sit before", () => {
    document.querySelector('[data-nav="arr"]')?.remove();

    insertNavButton(navButton());

    expect(headText()).toEqual(["Library", "History"]);
  });
});
