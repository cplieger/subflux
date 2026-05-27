// wizard-languages.ts — Extracted wizard languages step.

import { $ } from "./dom-core.js";
import { el } from "./dom.js";
import { DEFAULT_VARIANT } from "./constants.js";
import {
  type WizardStep,
  langRules,
  langDefault,
  infoIcon,
  langSelect,
  variantSelect,
} from "./wizard.js";

export function buildLanguagesStep(): WizardStep {
  return {
    id: "languages",
    title: "Languages",
    render(container: HTMLElement): void {
      container.appendChild(
        el(
          "div",
          { className: "wiz-lang-section-title" },
          "Subtitles downloaded by default when no rules match",
        ),
      );

      const defContainer = el("div", { id: "wiz-lang-defaults" });
      container.appendChild(defContainer);
      if (langDefault.length === 0) {
        langDefault.push({ code: "", variant: DEFAULT_VARIANT });
      }
      renderLangDefaults(defContainer);

      container.appendChild(
        el(
          "button",
          {
            type: "button",
            className: "wiz-lang-add",
            onclick: () => {
              collectLangDefaults();
              langDefault.push({ code: "", variant: DEFAULT_VARIANT });
              renderLangDefaults(defContainer);
            },
          },
          "+ Add default",
        ),
      );

      const ruleTitle = el(
        "div",
        {
          className: "wiz-lang-section-title",
          style: "margin-block-start:var(--sp-5)",
        },
        "Audio-specific rules (optional)",
      );
      ruleTitle.appendChild(infoIcon("Override defaults for specific audio languages"));
      container.appendChild(ruleTitle);

      const ruleContainer = el("div", { id: "wiz-lang-rules" });
      container.appendChild(ruleContainer);
      renderLangRules(ruleContainer);

      container.appendChild(
        el(
          "button",
          {
            type: "button",
            className: "wiz-lang-add",
            onclick: () => {
              collectLangRules();
              langRules.push({ audio: "", code: "", variant: DEFAULT_VARIANT });
              renderLangRules(ruleContainer);
            },
          },
          "+ Add rule",
        ),
      );
    },
    collect(): void {
      collectLangDefaults();
      collectLangRules();
    },
    validate(): string {
      collectLangDefaults();
      collectLangRules();
      const vd = langDefault.filter((d: { code: string }) => d.code.trim() !== "");
      const vr = langRules.filter(
        (r: { audio: string; code: string }) => r.audio.trim() !== "" && r.code.trim() !== "",
      );
      if (vd.length === 0 && vr.length === 0) {
        return "At least one language default or rule must be configured";
      }
      return "";
    },
  };
}

function renderLangDefaults(container: HTMLElement): void {
  container.replaceChildren();
  for (let i = 0; i < langDefault.length; i++) {
    const entry = langDefault[i];
    if (!entry) {
      continue;
    }
    const row = el("div", { className: "wiz-lang-row" });
    row.appendChild(langSelect("wiz-lang-def-code-" + String(i), entry.code, "Subtitle language"));
    row.appendChild(variantSelect("wiz-lang-def-variant-" + String(i), entry.variant));
    if (langDefault.length > 1) {
      const idx = i;
      row.appendChild(
        el(
          "button",
          {
            type: "button",
            className: "wiz-lang-remove",
            onclick: () => {
              collectLangDefaults();
              langDefault.splice(idx, 1);
              renderLangDefaults(container);
            },
          },
          "\u00d7",
        ),
      );
    }
    container.appendChild(row);
  }
}

function renderLangRules(container: HTMLElement): void {
  container.replaceChildren();
  for (let i = 0; i < langRules.length; i++) {
    const entry = langRules[i];
    if (!entry) {
      continue;
    }
    const row = el("div", { className: "wiz-lang-row" });
    row.appendChild(langSelect("wiz-lang-rule-audio-" + String(i), entry.audio, "Audio language"));
    row.appendChild(el("span", { className: "wiz-lang-arrow" }, "\u2192"));
    row.appendChild(langSelect("wiz-lang-rule-code-" + String(i), entry.code, "Subtitle language"));
    row.appendChild(variantSelect("wiz-lang-rule-variant-" + String(i), entry.variant));
    const idx = i;
    row.appendChild(
      el(
        "button",
        {
          type: "button",
          className: "wiz-lang-remove",
          onclick: () => {
            collectLangRules();
            langRules.splice(idx, 1);
            renderLangRules(container);
          },
        },
        "\u00d7",
      ),
    );
    container.appendChild(row);
  }
}

function collectLangDefaults(): void {
  for (let i = 0; i < langDefault.length; i++) {
    const entry = langDefault[i];
    if (!entry) {
      continue;
    }
    const c = $("wiz-lang-def-code-" + String(i)) as HTMLSelectElement | null;
    const v = $("wiz-lang-def-variant-" + String(i)) as HTMLSelectElement | null;
    if (c) {
      entry.code = c.value;
    }
    if (v) {
      entry.variant = v.value;
    }
  }
}

function collectLangRules(): void {
  for (let i = 0; i < langRules.length; i++) {
    const entry = langRules[i];
    if (!entry) {
      continue;
    }
    const a = $("wiz-lang-rule-audio-" + String(i)) as HTMLSelectElement | null;
    const c = $("wiz-lang-rule-code-" + String(i)) as HTMLSelectElement | null;
    const v = $("wiz-lang-rule-variant-" + String(i)) as HTMLSelectElement | null;
    if (a) {
      entry.audio = a.value;
    }
    if (c) {
      entry.code = c.value;
    }
    if (v) {
      entry.variant = v.value;
    }
  }
}
