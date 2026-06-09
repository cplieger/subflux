// @vitest-environment happy-dom
import { describe, it, beforeEach } from "vitest";
import {
  el as _el,
  text as _text,
  icon as _icon,
  option as _option,
  emptyDiv as _emptyDiv,
  errDiv as _errDiv,
  confirm as _confirm,
  closeDialog as _closeDialog,
  dialogHead as _dialogHead,
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
    document.body.innerHTML = '<dialog id="confirmDialog"></dialog>';
  });

  it.todo("shows modal dialog with title and message");

  it.todo("resolves true on confirm click");

  it.todo("resolves false on cancel click");

  it.todo("resolves false on backdrop click");

  it.todo("resolves false on Escape key");
});

describe("dom: closeDialog()", () => {
  it.todo("sets opacity transition and closes after transitionend");

  it.todo("closes after 250ms timeout if transitionend never fires");
});
