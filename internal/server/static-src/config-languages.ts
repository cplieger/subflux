// config-languages.ts — Language builder section extracted from config.ts.

import * as store from "./store.js";
import { el, option, icon } from "./dom.js";
import { langSelect } from "./utils.js";
import { SUBTITLE_VARIANTS, DEFAULT_VARIANT } from "./constants.js";
import { createDisclosure } from "@cplieger/ui-primitives/disclosure";
// Language config shapes come from the generated wire types (registered in
// internal/wirespec); the former hand-mirrored interfaces are gone.
import type { AudioRule, LanguageRules, SubtitleTarget } from "./wire/types.gen.js";

// --- Shared helper (duplicated from config.ts to avoid circular import) ---

function cfgFieldEl(label: string, element: HTMLElement, tip: string | undefined): HTMLElement {
  const lbl = el("label", null, label);
  if (tip) {
    lbl.setAttribute("data-tip", tip);
  }
  return el("div", { className: "cfg-field" }, lbl, element);
}

// --- Exported functions ---

function variantSelect(id: string | null, value: string | undefined): HTMLSelectElement {
  const sel = el("select", { id, className: "variant-select" }) as HTMLSelectElement;
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

  // --- Defaults ---
  sec.appendChild(el("div", { className: "cfg-title" }, "Language Defaults"));
  const defaultsContainer = el("div", {
    id: "lang-defaults",
    className: "lang-subs",
  });
  for (const d of defaults) {
    defaultsContainer.appendChild(buildSubTarget(d, true));
  }
  sec.appendChild(defaultsContainer);
  sec.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: () => defaultsContainer.appendChild(buildSubTarget({ code: "en" }, true)),
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
    rulesContainer.appendChild(buildRuleBlock(rule));
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
            buildRuleBlock({
              audio: "en",
              subtitles: [{ code: "fr" }],
            }),
          ),
      },
      "+ Add rule",
    ),
  );

  return sec;
}

function buildSubTarget(sub: SubtitleTarget, isDefault: boolean): HTMLElement {
  // Block wrapper: the flex controls row and the collapsible advanced region are
  // siblings inside it (not the region nested in the flex row), so the region
  // collapses to zero height with no flex row-gap residual. This wrapper is the
  // serialize anchor — a direct child of .lang-subs / the defaults container.
  const wrapper = el("div", { className: "lang-sub" });

  const row = el("div", { className: "lang-row" });
  row.appendChild(langSelect(null, sub.code));
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
  createDisclosure(toggleBtn, adv, { open: hasAdvanced });

  return wrapper;
}

function buildRuleBlock(rule: AudioRule): HTMLElement {
  const block = el("div", { className: "lang-rule" });

  block.appendChild(el("span", { className: "lang-label" }, "Audio:"));
  const header = el("div", { className: "lang-row" });
  header.appendChild(langSelect(null, rule.audio));
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
    subsContainer.appendChild(buildSubTarget(sub, false));
  }
  block.appendChild(subsContainer);
  block.appendChild(
    el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: () => subsContainer.appendChild(buildSubTarget({ code: "en" }, false)),
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
