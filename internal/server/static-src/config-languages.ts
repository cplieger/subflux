// config-languages.ts — Language builder section extracted from config.ts.

import * as store from "./store.js";
import { el, option, icon, withHelp } from "./dom.js";
import { langSelect } from "./utils.js";
import { SUBTITLE_VARIANTS, DEFAULT_VARIANT } from "./constants.js";
import { createDisclosure } from "@cplieger/ui-primitives/disclosure";
import { join } from "@cplieger/keyenc";
// Language config shapes come from the generated wire types (registered in
// internal/wirespec); the former hand-mirrored interfaces are gone.
import type { AudioRule, LanguageRules, SubtitleTarget } from "./wire/types.gen.js";

// --- Shared helper (duplicated from config.ts to avoid circular import) ---

function cfgFieldEl(label: string, element: HTMLElement, tip: string | undefined): HTMLElement {
  // Associate the label with whatever control was handed in; see the twin in
  // config-renderers.ts.
  const target = element.id || (element.querySelector("input, select, textarea")?.id ?? null);
  return el(
    "div",
    { className: "cfg-field" },
    withHelp(el("label", { for: target }, label), tip),
    element,
  );
}

// --- Advanced-gear reader state ---

/** Which list a subtitle target belongs to: one value decides both the controls
 *  it renders and the identity its gear is keyed by, so the two cannot disagree
 *  about whether a target is a default. */
type TargetScope = { readonly kind: "default" } | { readonly kind: "rule"; readonly audio: string };

const DEFAULTS_SCOPE: TargetScope = { kind: "default" };

const GEAR_KEY = "data-gear-key";

/** A gear's identity: the list it sits in plus the (code, variant) pair the
 *  config addresses the target by. Never a row index — adding a row above one
 *  would hand the new row the gear the reader opened below it. */
function gearKey(scope: TargetScope, sub: SubtitleTarget): string {
  if (scope.kind === "default") {
    return join("default", sub.code);
  }
  return join("rule", scope.audio, sub.code, sub.variant ?? DEFAULT_VARIANT);
}

/** Whether a gear is open is reader state no config carries, so the tree this
 *  render replaces is its only record — and it is still in the document while
 *  renderConfigForm builds its fragment. An ABSENT key means the reader has
 *  expressed nothing and `hasAdvanced` still decides; a present one carries the
 *  choice in both directions. */
function renderedGearStates(): Map<string, boolean> {
  const states = new Map<string, boolean>();
  for (const wrapper of document.querySelectorAll<HTMLElement>(`.lang-sub[${GEAR_KEY}]`)) {
    const key = wrapper.getAttribute(GEAR_KEY);
    const gear = wrapper.querySelector("button[aria-expanded]");
    if (key !== null && gear !== null) {
      states.set(key, gear.getAttribute("aria-expanded") === "true");
    }
  }
  return states;
}

/** What an ADD button hands a row the reader just created. Every add seeds a
 *  fixed target, so its key can collide with one already open — and the reader
 *  expressed nothing about a row that did not exist a moment ago. */
const NO_GEARS: ReadonlyMap<string, boolean> = new Map();

// --- Exported functions ---

function variantSelect(id: string | null, value: string | undefined): HTMLSelectElement {
  const sel = el("select", {
    id,
    className: "variant-select",
    "aria-label": "Subtitle variant",
  }) as HTMLSelectElement;
  for (const v of SUBTITLE_VARIANTS) {
    sel.appendChild(option(v.value, v.label));
  }
  if (value) {
    sel.value = value;
  }
  return sel;
}

export function buildLanguagesSection(): HTMLElement {
  const sec = el("div", { className: "cfg-section" });

  // Use parsed config if available, otherwise empty defaults.
  const cfgVal = store.get("config");
  const lr: LanguageRules = cfgVal?.language_rules ?? {};
  const rules: AudioRule[] = lr.rules ?? [];
  const defaults: SubtitleTarget[] = lr.default ?? [{ code: "en" }];
  const gears = renderedGearStates();

  // --- Defaults ---
  sec.appendChild(el("div", { className: "cfg-title" }, "Language Defaults"));
  const defaultsContainer = el("div", {
    id: "lang-defaults",
    className: "lang-subs",
  });
  for (const d of defaults) {
    defaultsContainer.appendChild(buildSubTarget(d, DEFAULTS_SCOPE, gears));
  }
  sec.appendChild(defaultsContainer);
  sec.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: () =>
          defaultsContainer.appendChild(buildSubTarget({ code: "en" }, DEFAULTS_SCOPE, NO_GEARS)),
      },
      "+ Add subtitle",
    ),
  );

  // --- Rules ---
  sec.appendChild(
    el(
      "div",
      {
        className: "cfg-title",
      },
      "Language Rules",
    ),
  );
  const rulesContainer = el("div", { id: "lang-rules" });
  for (const rule of rules) {
    rulesContainer.appendChild(buildRuleBlock(rule, gears));
  }
  sec.appendChild(rulesContainer);
  sec.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: () =>
          rulesContainer.appendChild(
            buildRuleBlock(
              {
                audio: "en",
                subtitles: [{ code: "fr" }],
              },
              NO_GEARS,
            ),
          ),
      },
      "+ Add rule",
    ),
  );

  return sec;
}

function buildSubTarget(
  sub: SubtitleTarget,
  scope: TargetScope,
  gears: ReadonlyMap<string, boolean>,
): HTMLElement {
  const isDefault = scope.kind === "default";
  // Block wrapper: the flex controls row and the collapsible advanced region are
  // siblings inside it (not the region nested in the flex row), so the region
  // collapses to zero height with no flex row-gap residual. This wrapper is the
  // serialize anchor — a direct child of .lang-subs / the defaults container.
  const key = gearKey(scope, sub);
  const wrapper = el("div", { className: "lang-sub", [GEAR_KEY]: key });

  const row = el("div", { className: "lang-row" });
  row.appendChild(langSelect(null, sub.code, "Subtitle language"));
  if (!isDefault) {
    row.appendChild(variantSelect(null, sub.variant ?? DEFAULT_VARIANT));
  }

  // Advanced fields (collapsible). `adv` is the disclosure region: createDisclosure
  // owns its height (0 <-> auto) and its aria-hidden/inert state. The visual
  // padding lives on the inner wrapper so it collapses with the height — no
  // residual sliver when closed.
  const adv = el("div", { className: "lang-advanced" });
  const advInner = el("div", { className: "lang-advanced-inner" });
  adv.appendChild(advInner);
  if (!isDefault) {
    const ms = el("input", {
      type: "number",
      className: "lang-min-score",
      placeholder: "min score",
      autocomplete: "off",
      value: sub.min_score != null ? String(sub.min_score) : "",
    });
    advInner.appendChild(
      cfgFieldEl("Min score", ms, "Override global minimum score for this target"),
    );
  }
  const prov = el("input", {
    type: "text",
    className: "lang-providers",
    placeholder: "providers (comma-sep)",
    autocomplete: "off",
    value: (sub.providers ?? []).join(", "),
  });
  advInner.appendChild(cfgFieldEl("Providers", prov, "Only use these providers (empty = all)"));
  const excl = el("input", {
    type: "text",
    className: "lang-exclude",
    placeholder: "exclude (comma-sep)",
    autocomplete: "off",
    value: (sub.exclude ?? []).join(", "),
  });
  advInner.appendChild(cfgFieldEl("Exclude", excl, "Skip these providers"));

  // Expand initially only if any advanced field already has a value. The `open`
  // option replaces the old adv.style.display bootstrap (same initial state).
  const hasAdvanced =
    sub.min_score != null ||
    (sub.providers != null && sub.providers.length > 0) ||
    (sub.exclude != null && sub.exclude.length > 0);

  // The gear is the disclosure trigger (a native <button>, so createDisclosure
  // handles Enter/Space via the native click). No onclick here — the disclosure
  // controller owns the toggle.
  const toggleBtn = el(
    "button",
    {
      type: "button",
      className: "close-btn ghost",
      "aria-label": "Advanced settings",
    },
    icon("settings"),
  );

  row.appendChild(toggleBtn);
  row.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "close-btn ghost",
        "aria-label": "Remove subtitle",
        onclick: () => {
          wrapper.remove();
        },
      },
      icon("close"),
    ),
  );

  wrapper.appendChild(row);
  wrapper.appendChild(adv);

  // WAI-ARIA disclosure: aria-expanded on the gear, aria-controls/aria-hidden/
  // inert on the region, and an animated height 0 <-> auto.
  createDisclosure(toggleBtn, adv, { open: gears.get(key) ?? hasAdvanced });

  return wrapper;
}

function buildRuleBlock(rule: AudioRule, gears: ReadonlyMap<string, boolean>): HTMLElement {
  const block = el("div", { className: "lang-rule" });
  const scope: TargetScope = { kind: "rule", audio: rule.audio };

  block.appendChild(el("span", { className: "lang-label" }, "Audio:"));
  const header = el("div", { className: "lang-row" });
  header.appendChild(langSelect(null, rule.audio, "Audio language"));
  header.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "close-btn ghost",
        "aria-label": "Remove rule",
        onclick: () => {
          block.remove();
        },
      },
      icon("close"),
    ),
  );
  block.appendChild(header);

  block.appendChild(el("span", { className: "lang-label" }, "Subtitles:"));
  const subsContainer = el("div", { className: "lang-subs" });
  for (const sub of rule.subtitles) {
    subsContainer.appendChild(buildSubTarget(sub, scope, gears));
  }
  block.appendChild(subsContainer);
  block.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: () => subsContainer.appendChild(buildSubTarget({ code: "en" }, scope, NO_GEARS)),
      },
      "+ Add subtitle",
    ),
  );

  return block;
}

// serializeLanguagesFromForm reads the language builder DOM back into the
// languages section value for the structured save. Shape mirrors what the
// old YAML emitter produced: rules (each audio + subtitles, [] when a rule
// has no targets) and default, both omitted entirely when their container
// is empty.
export function serializeLanguagesFromForm(): LanguageRules {
  const languages: LanguageRules = {};

  // Rules.
  const rulesEl = document.getElementById("lang-rules");
  if (rulesEl && rulesEl.children.length > 0) {
    const rules: AudioRule[] = [];
    for (const block of Array.from(rulesEl.children)) {
      const audioSel = block.querySelector<HTMLSelectElement>(".lang-row .lang-select");
      if (!audioSel) {
        continue;
      }
      const subtitles: SubtitleTarget[] = [];
      for (const row of Array.from(block.querySelectorAll(".lang-subs > .lang-sub"))) {
        const st = subTargetFromForm(row as HTMLElement, false);
        if (st) {
          subtitles.push(st);
        }
      }
      rules.push({ audio: audioSel.value, subtitles });
    }
    languages.rules = rules;
  }

  // Defaults.
  const defaultsEl = document.getElementById("lang-defaults");
  if (defaultsEl && defaultsEl.children.length > 0) {
    const defaults: SubtitleTarget[] = [];
    for (const row of Array.from(defaultsEl.children)) {
      const st = subTargetFromForm(row as HTMLElement, true);
      if (st) {
        defaults.push(st);
      }
    }
    languages.default = defaults;
  }

  return languages;
}

function subTargetFromForm(row: HTMLElement, isDefault: boolean): SubtitleTarget | null {
  const langSel = row.querySelector<HTMLSelectElement>(".lang-select");
  if (!langSel) {
    return null;
  }
  const st: SubtitleTarget = { code: langSel.value };
  if (!isDefault) {
    const varSel = row.querySelector<HTMLSelectElement>(".variant-select");
    const v = varSel ? varSel.value : DEFAULT_VARIANT;
    if (v !== DEFAULT_VARIANT) {
      st.variant = v;
    }
    const msEl = row.querySelector<HTMLInputElement>(".lang-min-score");
    if (msEl?.value) {
      const ms = Number(msEl.value);
      if (Number.isFinite(ms)) {
        st.min_score = ms;
      }
    }
  }
  const provEl = row.querySelector<HTMLInputElement>(".lang-providers");
  if (provEl?.value.trim()) {
    st.providers = provEl.value
      .split(",")
      .map((s: string) => s.trim())
      .filter(Boolean);
  }
  const exclEl = row.querySelector<HTMLInputElement>(".lang-exclude");
  if (exclEl?.value.trim()) {
    st.exclude = exclEl.value
      .split(",")
      .map((s: string) => s.trim())
      .filter(Boolean);
  }
  return st;
}
