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
import { _resetForTest as resetConfirm } from "@cplieger/ui-primitives/confirm";

describe("dom: el()", () => {
  it.todo("creates element with tag name");

  it.todo("sets className from attrs");

  it.todo("sets on* event handlers as properties");

  it.todo("sets boolean attributes (hidden, disabled, checked)");

  it.todo("appends string children as text nodes");

  it.todo("appends Node children directly");

  it.todo("skips null/undefined children");
});

// confirm() now delegates to the @cplieger/ui-primitives confirm primitive,
// which owns a reused <dialog class="uip-confirm"> appended to the body. These
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
    return document.querySelector<HTMLDialogElement>(".uip-confirm");
  }

  function okBtn(): HTMLButtonElement | null {
    return document.querySelector<HTMLButtonElement>(".uip-confirm-ok");
  }

  function cancelBtn(): HTMLButtonElement | null {
    return document.querySelector<HTMLButtonElement>(".uip-confirm-cancel");
  }

  it("opens a modal showing the title and message", () => {
    void confirm("Delete file", "This cannot be undone");
    expect(dlg()?.open).toBe(true);
    expect(document.querySelector(".uip-confirm-title")?.textContent).toBe("Delete file");
    expect(document.querySelector(".uip-confirm-msg")?.textContent).toBe("This cannot be undone");
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

  it("is a no-op returning undefined when the dialog is already closed", () => {
    expect(dlg().open).toBe(false);
    expect(closeDialog(dlg())).toBeUndefined();
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
