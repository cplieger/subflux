// Theme management: dark/light/auto with system preference detection.

import { icon } from './dom.js';

const THEME_KEY = 'subflux-theme';

function resolveTheme(pref: string | null): string {
  if (pref === 'dark' || pref === 'light') return pref;
  return window.matchMedia('(prefers-color-scheme: dark)').matches
    ? 'dark' : 'light';
}

function applyTheme(pref: string | null): void {
  document.documentElement.setAttribute('data-theme', resolveTheme(pref));
  updateThemeIcon();
}

export function init(): void {
  const saved = localStorage.getItem(THEME_KEY);
  applyTheme(saved);

  window.matchMedia('(prefers-color-scheme: dark)')
    .addEventListener('change', () => {
      if (!localStorage.getItem(THEME_KEY)) {
        applyTheme(null);
      }
    });
}

export function cycle(): void {
  const current = resolveTheme(localStorage.getItem(THEME_KEY));
  const next = current === 'dark' ? 'light' : 'dark';
  localStorage.setItem(THEME_KEY, next);
  applyTheme(next);
}

function updateThemeIcon(): void {
  const active = document.documentElement.getAttribute('data-theme');
  const icons: Record<string, string> = { dark: 'moon', light: 'sun' };
  const btn = document.getElementById('themeBtn');
  if (!btn) return;
  const iconEl = btn.querySelector('.nav-icon');
  if (!iconEl) return;

  const newName = (active && icons[active]) || 'moon';
  const current = iconEl.querySelector('.icon');
  const isSame = current
    && current.classList.contains(`icon-${newName}`);
  if (isSame) return;

  const isDark = newName === 'moon';
  const glowClass = isDark ? 'glow-moon' : 'glow-sun';

  let settled = false;
  const settle = () => {
    if (settled) return;
    settled = true;
    iconEl.textContent = '';
    iconEl.appendChild(icon(newName));
    iconEl.classList.remove('setting');
    iconEl.classList.add('rising');
    btn.classList.add(glowClass);
    iconEl.getBoundingClientRect();
    iconEl.classList.remove('rising');
    iconEl.addEventListener('transitionend', () => {
      btn.classList.remove('glow-sun', 'glow-moon');
    }, { once: true });
    setTimeout(() => btn.classList.remove('glow-sun', 'glow-moon'), 300);
  };

  iconEl.classList.add('setting');
  iconEl.addEventListener('transitionend', settle, { once: true });
  setTimeout(settle, 300);
}
