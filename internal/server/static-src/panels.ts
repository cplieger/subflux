// A detached panel is invisible to getElementById, so every reader reaches a
// control through libraryPanel()/historyPanel() rather than through the document.

import { el, option } from "./dom.js";
import { contentView, historyView } from "./view-scope.js";

interface PanelShell {
  readonly root: HTMLElement; // section.card[data-page]
  readonly head: HTMLElement; // div.card-head — heading, controls, nav buttons
  readonly heading: HTMLElement; // h2#lib-heading / h2#hist-heading
  readonly controls: HTMLElement; // div.controls, a node moved in and out by presence
  readonly content: HTMLElement; // div#coverageContent / div#historyContent
}

export interface LibraryPanel extends PanelShell {
  readonly filter: HTMLInputElement; // #cov-filter
  readonly missingOnly: HTMLInputElement; // #cov-missing
  readonly typeFilter: HTMLSelectElement; // #cov-type-filter
  readonly sort: HTMLSelectElement; // #cov-sort
}

export interface HistoryPanel extends PanelShell {
  readonly filter: HTMLInputElement; // #h-filter
  readonly type: HTMLSelectElement; // #h-type
  readonly lang: HTMLSelectElement; // #h-lang
  readonly provider: HTMLSelectElement; // #h-provider
}

function filterInput(id: string, placeholder: string, label: string): HTMLInputElement {
  return el("input", {
    id,
    type: "search",
    placeholder,
    "aria-label": label,
    autocomplete: "off",
    enterkeyhint: "search",
    spellcheck: "false",
  }) as HTMLInputElement;
}

function buildLibraryPanel(): LibraryPanel {
  const heading = el("h2", { id: "lib-heading" }, "Library");
  const missingOnly = el("input", { type: "checkbox", id: "cov-missing" }) as HTMLInputElement;
  const typeFilter = el(
    "select",
    { id: "cov-type-filter", "aria-label": "Media type filter" },
    option("all", "All"),
    option("series", "Series"),
    option("movies", "Movies"),
  ) as HTMLSelectElement;
  const sort = el(
    "select",
    { id: "cov-sort", "aria-label": "Sort by" },
    option("title", "A-Z"),
    option("title-desc", "Z-A"),
    option("newest", "Newest"),
    option("oldest", "Oldest"),
  ) as HTMLSelectElement;
  const filter = filterInput("cov-filter", "Filter library\u2026", "Filter library");
  const controls = el(
    "div",
    { className: "controls" },
    el(
      "label",
      { className: "toggle", "aria-label": "Missing only" },
      el(
        "span",
        { "data-tip": "Show only items with incomplete subtitle coverage" },
        "Missing only",
      ),
      missingOnly,
      // The trailing empty span IS the slider the .toggle rule paints.
      el("span"),
    ),
    typeFilter,
    sort,
    filter,
  );
  const head = el("div", { className: "card-head" }, heading, controls);
  const content = el("div", { id: "coverageContent" });
  const root = el(
    "section",
    { className: "card", "data-page": "coverage", "aria-labelledby": "lib-heading" },
    head,
    content,
  );
  return { root, head, heading, controls, content, filter, missingOnly, typeFilter, sort };
}

function buildHistoryPanel(): HistoryPanel {
  const heading = el("h2", { id: "hist-heading" }, "History");
  const type = el(
    "select",
    { id: "h-type", "aria-label": "Media type filter" },
    option("", "All types"),
    option("episode", "Episodes"),
    option("movie", "Movies"),
  ) as HTMLSelectElement;
  const lang = el(
    "select",
    { id: "h-lang", "aria-label": "Language filter" },
    option("", "All languages"),
  ) as HTMLSelectElement;
  const provider = el(
    "select",
    { id: "h-provider", "aria-label": "Provider filter" },
    option("", "All providers"),
  ) as HTMLSelectElement;
  const filter = filterInput("h-filter", "Filter media\u2026", "Filter downloads");
  const controls = el("div", { className: "controls" }, type, lang, provider, filter);
  const head = el("div", { className: "card-head" }, heading, controls);
  const content = el("div", { id: "historyContent" });
  const root = el(
    "section",
    { className: "card", "data-page": "history", "aria-labelledby": "hist-heading" },
    head,
    content,
  );
  return { root, head, heading, controls, content, filter, type, lang, provider };
}

// `let`, not `const`: _resetPanelsForTest REBUILDS both, so no accessor ever
// has a build-on-first-call arm.
let library = buildLibraryPanel();
let history = buildHistoryPanel();

export function libraryPanel(): LibraryPanel {
  return library;
}

export function historyPanel(): HistoryPanel {
  return history;
}

export function _resetPanelsForTest(): void {
  // Release the hosts BEFORE rebuilding: both mount paths gate on host
  // OCCUPANCY rather than on the container node, so a rebuilt panel with a live
  // occupant early-returns and the live binding keeps rendering into the
  // detached predecessor.
  contentView.clear();
  historyView.clear();
  library = buildLibraryPanel();
  history = buildHistoryPanel();
}

/** Insert an element into the library panel's header, before the arr link if
 *  present. */
export function insertNavButton(btn: HTMLElement): void {
  const { head } = libraryPanel();
  const arrEl = head.querySelector('[data-nav="arr"]');
  if (arrEl) {
    head.insertBefore(btn, arrEl);
  } else {
    head.appendChild(btn);
  }
}
