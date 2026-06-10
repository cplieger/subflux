// Theme management: dark/light/auto with system preference detection.

const THEME_KEY = "subflux-theme";

function resolveTheme(pref: string | null): string {
  if (pref === "dark" || pref === "light") {
    return pref;
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(pref: string | null): void {
  document.documentElement.setAttribute("data-theme", resolveTheme(pref));
}

export function init(): void {
  const saved = localStorage.getItem(THEME_KEY);
  applyTheme(saved);

  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (!localStorage.getItem(THEME_KEY)) {
      applyTheme(null);
    }
  });
}

export function cycle(): void {
  const current = resolveTheme(localStorage.getItem(THEME_KEY));
  const next = current === "dark" ? "light" : "dark";
  localStorage.setItem(THEME_KEY, next);
  applyTheme(next);
}
