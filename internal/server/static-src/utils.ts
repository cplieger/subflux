// Shared utility functions used across multiple modules.

import { el, option, pad } from "./dom.js";
import { LANGUAGES, langNameMap } from "./languages.js";
import { DEFAULT_VARIANT } from "./constants.js";
import { viewTransition as uipViewTransition } from "@cplieger/ui-primitives/view-transition";

// Debounce: returns a wrapper that delays fn execution until ms
// milliseconds after the last invocation. Each call resets the timer.
export function debounce<T extends (...args: unknown[]) => void>(
  fn: T,
  ms: number,
): (...args: Parameters<T>) => void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  return (...args: Parameters<T>) => {
    if (timer) {
      clearTimeout(timer);
    }
    timer = setTimeout(() => {
      timer = null;
      fn(...args);
    }, ms);
  };
}

// Detect if the user's locale uses 12-hour time; default to 24h.
const use12h = (() => {
  try {
    const sample = new Intl.DateTimeFormat(undefined, { hour: "numeric" }).resolvedOptions();
    return sample.hourCycle === "h12" || sample.hourCycle === "h11";
  } catch {
    return false;
  }
})();

const timeFmt = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  hour12: use12h,
});

export function fmtDateTime(d: Date): string {
  const iso = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  return `${iso} ${timeFmt.format(d)}`;
}

export function fmtTime(d: Date): string {
  return timeFmt.format(d);
}

export function prettyLabel(s: string): string {
  return s.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export function viewTransition(fn: () => void): void {
  // Delegate to the queued, feature-detected @cplieger/ui-primitives wrapper
  // (overlapping calls serialize; resolves — never rejects — on skip). The
  // void-returning signature is kept so the fire-and-forget call sites don't
  // trip no-floating-promises; the returned promise is intentionally ignored.
  void uipViewTransition(fn);
}

// Build a table row that acts as a clickable link: responds to click,
// Enter, and Space for keyboard accessibility. Deliberately NO role
// override: role="link" on a <tr> would flatten the row/cell semantics
// assistive tech relies on for table navigation (the accessible name would
// become the concatenated text of every cell). The row stays focusable and
// operable while keeping its native table role.
export function clickableRow(
  handler: () => void,
  ...children: (string | Node | null | undefined)[]
): HTMLElement {
  return el(
    "tr",
    {
      className: "clickable",
      tabindex: "0",
      onclick: handler,
      onkeydown: (e: KeyboardEvent) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          handler();
        }
      },
    },
    ...children,
  );
}

// Set the document title to "Subflux · <context>" (or plain "Subflux"), so
// browser tabs, history entries, and screen-reader page announcements
// distinguish the routes.
export function setDocTitle(context?: string): void {
  document.title = context ? `Subflux \u00B7 ${context}` : "Subflux";
}

// Build a standardized empty-state placeholder with an optional action button.
export function emptyState(msg: string, actionLabel?: string, actionFn?: () => void): HTMLElement {
  const frag = el("div", { className: "empty" }, el("div", null, msg));
  if (actionLabel && actionFn) {
    frag.appendChild(
      el(
        "button",
        {
          type: "button",
          className: "ghost",
          onclick: actionFn,
        },
        actionLabel,
      ),
    );
  }
  return frag;
}

// Format season and episode numbers as S##E## (e.g. S01E05).
export function fmtEpisode(season: number, episode: number): string {
  return `S${pad(season)}E${pad(episode)}`;
}

// Build a composite media ID for an episode: tvdb-{id}-s{##}e{##}.
export function tvdbMediaId(tvdbId: number, season: number, episode: number): string {
  return `tvdb-${tvdbId}-s${pad(season)}e${pad(episode)}`;
}

// Build a top-level coverage media ID for a series or movie item.
export function coverageMediaId(item: {
  _type: "series" | "movie";
  tvdb_id?: number;
  tmdb_id?: number;
}): string {
  return item._type === "series" ? `tvdb-${item.tvdb_id ?? ""}` : `tmdb-${item.tmdb_id ?? ""}`;
}

// --- Language codes (imported from languages.ts) ---

// Format a language+variant label: "en" or "en(forced)" when variant is non-standard.
export function fmtLangVariant(lang: string, variant: string): string {
  return lang + (variant !== DEFAULT_VARIANT ? `(${variant})` : "");
}

// Resolve a language code to its display name, falling back to
// uppercase code for unknown values. Passes through sentinel
// values ('default', 'no targets') unchanged.
export function langName(code: string): string {
  if (!code || code === "default" || code === "no targets") {
    return code;
  }
  return langNameMap[code] ?? code.toUpperCase();
}

// Build a <select> dropdown populated with all supported language codes.
export function langSelect(id: string | null, value?: string): HTMLSelectElement {
  const sel = el("select", { id, className: "lang-select" }) as HTMLSelectElement;
  for (const [code, name] of LANGUAGES) {
    sel.appendChild(option(code, `${code} \u2014 ${name}`));
  }
  if (value) {
    sel.value = value;
  }
  return sel;
}
