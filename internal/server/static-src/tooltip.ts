// tooltip.ts — Self-contained tooltip system for elements with data-tip attributes.

import { el, text } from "./dom.js";

let tipTimer: ReturnType<typeof setTimeout> | null = null;
let tipEl: HTMLElement | null = null;

export function showTip(target: HTMLElement): void {
  const msg = target.getAttribute("data-tip");
  if (!msg) {
    return;
  }
  if (tipEl) {
    tipEl.remove();
  }
  target.style.anchorName = "--tip-anchor";
  tipEl = el("div", { className: "tooltip" });
  const lines = msg.split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (i > 0) {
      tipEl.appendChild(el("br"));
    }
    const line = lines[i];
    if (line !== undefined) {
      tipEl.appendChild(text(line));
    }
  }
  const dlg = target.closest("dialog[open]");
  (dlg ?? document.body).appendChild(tipEl);
  requestAnimationFrame(() => {
    if (tipEl) {
      tipEl.classList.add("show");
    }
  });
}

export function hideTip(): void {
  clearTimeout(tipTimer ?? undefined);
  tipTimer = null;
  if (tipEl) {
    const anchored = document.querySelector<HTMLElement>('[style*="--tip-anchor"]');
    if (anchored) {
      (anchored.style as unknown as Record<string, unknown>)["anchorName"] = "";
    }
    tipEl.remove();
    tipEl = null;
  }
}

document.addEventListener("pointerover", (e: PointerEvent) => {
  const t = e.target as HTMLElement;
  const target = t.closest<HTMLElement>("[data-tip]");
  if (!target) {
    hideTip();
    return;
  }
  hideTip();
  tipTimer = setTimeout(() => {
    showTip(target);
  }, 300);
});

document.addEventListener("pointerout", (e: PointerEvent) => {
  const t = e.target as HTMLElement;
  const target = t.closest<HTMLElement>("[data-tip]");
  if (target) {
    hideTip();
  }
});
