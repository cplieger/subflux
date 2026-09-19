// wizard-fields.ts — The wizard's shared field builders, below both wizard.ts
// and the step modules that call them.
//
// Carved out of wizard.ts with the state (see wizard-store.ts): the step modules
// imported these back from "./wizard.js", which is half of what closed the
// per-step cycle. They belong under both layers because both build fields —
// wizard.ts for the review screen, each step module for its own rows.
//
// A field's schema `help` text goes through dom.ts's withHelp, the one carrier
// for both this wizard and the settings dialog.

import { el, option, withHelp } from "./dom.js";
import { SUBTITLE_VARIANTS, DEFAULT_VARIANT } from "./constants.js";
import { LANGUAGES } from "./languages.js";

export function wizField(
  id: string,
  label: string,
  type: string,
  value: string,
  placeholder: string,
  tip: string | undefined,
): HTMLElement {
  const lbl = withHelp(el("label", { for: id }, label), tip);
  const inp = el("input", {
    type: type === "number" ? "number" : "text",
    id,
    placeholder,
    value,
    autocomplete: "off",
    "data-1p-ignore": "",
    "data-lpignore": "true",
    "data-bwignore": "",
    "data-form-type": "other",
    className: type === "secret" ? "wiz-masked" : "",
  });
  return el("div", { className: "wiz-field" }, lbl, inp);
}

export function wizToggle(
  id: string,
  label: string,
  checked: boolean,
  tip: string | undefined,
): HTMLElement {
  // `for` gives the checkbox its accessible name: the .wiz-toggle wrapper
  // holds only the slider span, so without it the control announces
  // unlabelled.
  const lbl = withHelp(el("label", { for: id }, label), tip);
  const cb = el("input", { type: "checkbox", id }) as HTMLInputElement;
  cb.checked = checked;
  const toggle = el("label", { className: "wiz-toggle" }, cb, el("span"));
  return el("div", { className: "wiz-field" }, lbl, toggle);
}

export function langSelect(id: string, value: string, placeholder: string): HTMLElement {
  // The placeholder doubles as the accessible name: these rows caption
  // their selects with a layout element, not a label.
  const sel = el("select", {
    id,
    className: "wiz-lang-select",
    "aria-label": placeholder,
  }) as HTMLSelectElement;
  sel.appendChild(option("", placeholder));
  for (const [code, name] of LANGUAGES) {
    sel.appendChild(option(code, name + " (" + code + ")"));
  }
  sel.value = value;
  return sel;
}

export function variantSelect(id: string, value: string): HTMLElement {
  const sel = el("select", {
    id,
    className: "wiz-lang-variant",
    "aria-label": "Subtitle variant",
  }) as HTMLSelectElement;
  for (const v of SUBTITLE_VARIANTS) {
    sel.appendChild(option(v.value, v.label));
  }
  sel.value = value || DEFAULT_VARIANT;
  return sel;
}
