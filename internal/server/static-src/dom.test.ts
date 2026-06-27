// @vitest-environment happy-dom
import { describe, it, beforeEach, afterEach, expect, vi } from "vitest";
import {
  el as _el,
  text as _text,
  icon as _icon,
  option as _option,
  emptyDiv as _emptyDiv,
  errDiv as _errDiv,
  dialogHead as _dialogHead,
  confirm,
  closeDialog,
} from "./dom.js";

describe("dom: el()", () => {
  it.todo("creates element with tag name");

  it.todo("sets className from attrs");

  it.todo("sets on* event handlers as properties");

  it.todo("sets boolean attributes (hidden, disabled, checked)");

  it.todo("appends string children as text nodes");

  it.todo("appends Node children directly");

  it.todo("skips null/undefined children");
});

describe("dom: confirm()", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = '<dialog id="confirmDialog"></dialog>';
  });

  afterEach(() => {
    // confirm() resolves synchronously on the click; closeDialog() leaves a
    // 250ms fallback timer behind. Flush it so it does not leak into the
    // next test.
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
  });

  function dlg(): HTMLDialogElement {
    return document.getElementById("confirmDialog") as HTMLDialogElement;
  }

  function footButtons(): HTMLButtonElement[] {
    return [...dlg().querySelectorAll<HTMLButtonElement>(".dlg-foot button")];
  }

  it("opens as a modal showing the title and message", () => {
    void confirm("Delete file", "This cannot be undone");
    expect(dlg().open).toBe(true);
    expect(dlg().querySelector("h2")?.textContent).toBe("Delete file");
    expect(dlg().querySelector(".dlg-body p")?.textContent).toBe("This cannot be undone");
  });

  it("uses a custom confirm-button label when provided", () => {
    void confirm("Title", "Body", "Yes, delete");
    expect(footButtons()[0]?.textContent).toBe("Yes, delete");
  });

  it("resolves true when the confirm button is clicked", async () => {
    const p = confirm("Title", "Body");
    footButtons()[0]?.click();
    await expect(p).resolves.toBe(true);
  });

  it("resolves false when the cancel button is clicked", async () => {
    const p = confirm("Title", "Body");
    footButtons()
      .find((b) => b.textContent === "Cancel")
      ?.click();
    await expect(p).resolves.toBe(false);
  });

  it("resolves false on a backdrop click (mousedown then click on the dialog itself)", async () => {
    const p = confirm("Title", "Body");
    dlg().dispatchEvent(new MouseEvent("mousedown"));
    dlg().dispatchEvent(new MouseEvent("click"));
    await expect(p).resolves.toBe(false);
  });

  it("resolves false when the dialog emits a native close (Escape)", async () => {
    const p = confirm("Title", "Body");
    dlg().dispatchEvent(new Event("close"));
    await expect(p).resolves.toBe(false);
  });
});

describe("dom: closeDialog()", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = '<dialog id="dlg"></dialog>';
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function dlg(): HTMLDialogElement {
    return document.getElementById("dlg") as HTMLDialogElement;
  }

  it("is a no-op returning undefined when the dialog is already closed", () => {
    expect(dlg().open).toBe(false);
    expect(closeDialog(dlg())).toBeUndefined();
  });

  it("starts the fade-out and closes via the 250ms fallback when no transitionend fires", () => {
    const d = dlg();
    d.showModal();
    closeDialog(d);
    // Fade-out started, but the dialog stays open until the transition ends
    // (or the fallback timer fires).
    expect(d.style.opacity).toBe("0");
    expect(d.open).toBe(true);
    vi.advanceTimersByTime(250);
    expect(d.open).toBe(false);
    // Inline transition styles are cleared once the dialog actually closes.
    expect(d.style.opacity).toBe("");
  });

  it("closes on transitionend and does not close a second time on the fallback timer", () => {
    const d = dlg();
    d.showModal();
    const closeSpy = vi.spyOn(d, "close");
    closeDialog(d);
    d.dispatchEvent(new Event("transitionend"));
    expect(d.open).toBe(false);
    vi.advanceTimersByTime(250);
    expect(closeSpy).toHaveBeenCalledTimes(1);
  });
});
