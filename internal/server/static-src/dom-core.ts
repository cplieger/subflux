// Core DOM helpers. Zero imports — safe to use from any entry point.

export function $(id: string): HTMLElement | null {
  return document.getElementById(id);
}

export function show(el: HTMLElement | null): void {
  if (el) {
    el.hidden = false;
  }
}

export function hide(el: HTMLElement | null): void {
  if (el) {
    el.hidden = true;
  }
}

export function showPage(pageId: string): void {
  const pages = document.querySelectorAll(".auth-page");
  for (const p of pages) {
    (p as HTMLElement).hidden = true;
  }
  show($(pageId));
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
