// Core DOM helpers. Zero imports — safe to use from any entry point.

export function $(id: string): HTMLElement | null {
  return document.getElementById(id);
}

export function show(el: HTMLElement | null): void {
  if (el) {
    el.hidden = false;
  }
}

function hide(el: HTMLElement | null): void {
  if (el) {
    el.hidden = true;
  }
}

export function showPage(pageId: string): void {
  const pages = document.querySelectorAll(".auth-page");
  for (const p of pages) {
    (p as HTMLElement).hidden = true;
  }
  const page = $(pageId);
  show(page);
  focusFirstField(page);
}

/** focusFirstField focuses the revealed page's first usable form control.
 *
 *  login.html holds four page-states in one document and only JS knows which
 *  one applies, so `autofocus` cannot serve them: the attribute resolves once,
 *  at parse, against an element that is still hidden — and a second autofocus
 *  further down the document is ignored outright.
 *
 *  Focus is not only convenience here. A password manager classifies a text
 *  field when that field is visible, and offers its fill affordance on focus;
 *  a `type=password` field gets an unconditional offer either way. That
 *  asymmetry is why the setup card's password field triggered Bitwarden and
 *  Keychain while its username field, revealed after a fetch and never
 *  focused, triggered nothing.
 *
 *  Fields inside a `hidden` subtree are skipped (the OIDC link-on-login flow
 *  hides the username field), because focusing a display:none element is a
 *  no-op that leaves the page with nothing focused. */
function focusFirstField(page: HTMLElement | null): void {
  if (!page || page.contains(document.activeElement)) {
    return;
  }
  const fields = page.querySelectorAll<HTMLElement>(
    "input:not([type=hidden]):not([disabled]), select:not([disabled]), textarea:not([disabled])",
  );
  for (const field of fields) {
    if (!field.closest("[hidden]")) {
      field.focus();
      return;
    }
  }
}

export function showError(id: string, msg: string): void {
  const el = $(id);
  if (!el) {
    return;
  }
  el.textContent = msg;
  el.hidden = false;
}

export function hideError(id: string): void {
  hide($(id));
}
