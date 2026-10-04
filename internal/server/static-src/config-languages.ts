// config-languages.ts — Language builder section extracted from config.ts.

import * as store from "./store.js";
import { el, option, icon, withHelp } from "./dom.js";
import { langSelect } from "./utils.js";
import { SUBTITLE_VARIANTS, DEFAULT_VARIANT } from "./constants.js";
import { createDisclosure } from "@cplieger/ui-primitives/disclosure";
import { join } from "@cplieger/keyenc";
import {
  cfgLanguageRules,
  type RawTarget,
  type StoredLanguages,
  type StoredRule,
  type StoredTarget,
} from "./config-values.js";
// Language config shapes come from the generated wire types (registered in
// internal/wirespec); the former hand-mirrored interfaces are gone.
import type { LanguageRules, SubtitleTarget } from "./wire/types.gen.js";

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

/** Rows expanded from one stored `variants` target, which a save writes back as
 *  that target while every one of them is present and untouched. */
interface VariantGroup {
  readonly stored: RawTarget;
  readonly size: number;
}

/** The stored target a rendered row stands for. */
interface TargetSource {
  readonly stored: RawTarget;
  readonly group: VariantGroup | null;
}

/** A row's source plus the form's reading of it as built: a field still equal
 *  to `pristine` is written back from `stored`, so an unrendered field or a
 *  `${VAR}` value survives the save. */
interface RowOrigin extends TargetSource {
  readonly pristine: SubtitleTarget;
}

interface PlannedTarget {
  readonly shown: SubtitleTarget;
  readonly source?: TargetSource;
}

interface PlannedRule {
  readonly audio: string;
  readonly storedAudio?: string;
  readonly subtitles: readonly PlannedTarget[];
}

interface LanguagesPlan {
  readonly rules: readonly PlannedRule[];
  readonly defaults: readonly PlannedTarget[];
}

const rowOrigin = new WeakMap<Element, RowOrigin>();
const ruleOrigin = new WeakMap<Element, { readonly stored: string; readonly pristine: string }>();

/** Rows show the parsed config, which expands `variants` and `${VAR}` values,
 *  when it lines up target for target with the stored section; otherwise they
 *  show the stored section. */
function languagesPlan(): LanguagesPlan {
  const stored = cfgLanguageRules();
  const effective = store.get("config")?.language_rules;
  return (effective && pairSection(stored, effective)) ?? selfPlan(stored);
}

function selfPlan(lr: StoredLanguages): LanguagesPlan {
  const self = (ts: readonly StoredTarget[]): PlannedTarget[] =>
    ts.map((t) => ({ shown: t.typed, source: { stored: t.raw, group: null } }));
  return {
    rules: (lr.rules ?? []).map((r) => ({
      audio: r.audio,
      storedAudio: r.audio,
      subtitles: self(r.subtitles),
    })),
    defaults: self(lr.default ?? [asStored({ code: "en" })]),
  };
}

/** A target with no stored form of its own stands for itself. */
function asStored(t: SubtitleTarget): StoredTarget {
  return { raw: { ...t }, typed: t };
}

function pairSection(stored: StoredLanguages, effective: LanguageRules): LanguagesPlan | null {
  const effRules = effective.rules ?? [];
  const storedRules: readonly StoredRule[] =
    stored.rules ?? effRules.map((r) => ({ audio: r.audio, subtitles: r.subtitles.map(asStored) }));
  if (storedRules.length !== effRules.length) {
    return null;
  }
  const rules: PlannedRule[] = [];
  for (const [i, s] of storedRules.entries()) {
    const e = effRules[i];
    const subtitles = e && sameSource(s.audio, e.audio) && pairTargets(s.subtitles, e.subtitles);
    if (!e || !subtitles) {
      return null;
    }
    rules.push({ audio: e.audio, storedAudio: s.audio, subtitles });
  }
  if (stored.default === undefined && effective.default === undefined) {
    return { rules, defaults: selfPlan({}).defaults };
  }
  const defaults = pairTargets(
    stored.default ?? (effective.default ?? []).map(asStored),
    effective.default ?? [],
  );
  return defaults ? { rules, defaults } : null;
}

/** Pairs each effective target with the stored target it was loaded from: a
 *  stored target with `variants` loads as one target per listed variant. */
function pairTargets(
  stored: readonly StoredTarget[],
  effective: readonly SubtitleTarget[],
): PlannedTarget[] | null {
  const out: PlannedTarget[] = [];
  for (const { raw, typed: s } of stored) {
    const variants = s.variants ?? [];
    const group = variants.length > 0 ? { stored: raw, size: variants.length } : null;
    for (const v of group ? variants : [s.variant]) {
      const e = effective[out.length];
      if (!e || !sameSource(s.code, e.code) || !sameSource(variantOf(v), variantOf(e.variant))) {
        return null;
      }
      out.push({ shown: e, source: { stored: group ? withVariant(raw, v) : raw, group } });
    }
  }
  return out.length === effective.length ? out : null;
}

function variantOf(v: string | undefined): string {
  return v === undefined || v === "" ? DEFAULT_VARIANT : v;
}

/** A stored value matches its loaded one, or names an environment variable
 *  the loader expanded. */
function sameSource(stored: string, loaded: string): boolean {
  return stored === loaded || stored.includes("${");
}

/** One expanded member of a stored `variants` target. */
function withVariant(s: RawTarget, v: string | undefined): RawTarget {
  const t: Record<string, unknown> = { ...s };
  delete t["variants"];
  if (variantOf(v) !== DEFAULT_VARIANT) {
    t["variant"] = variantOf(v);
  }
  return t;
}

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

  const plan = languagesPlan();
  const gears = renderedGearStates();

  // --- Defaults ---
  sec.appendChild(el("div", { className: "cfg-title" }, "Language Defaults"));
  const defaultsContainer = el("div", {
    id: "lang-defaults",
    className: "lang-subs",
  });
  for (const d of plan.defaults) {
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
          defaultsContainer.appendChild(
            buildSubTarget({ shown: { code: "en" } }, DEFAULTS_SCOPE, NO_GEARS),
          ),
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
  for (const rule of plan.rules) {
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
            buildRuleBlock({ audio: "en", subtitles: [{ shown: { code: "fr" } }] }, NO_GEARS),
          ),
      },
      "+ Add rule",
    ),
  );

  return sec;
}

function buildSubTarget(
  planned: PlannedTarget,
  scope: TargetScope,
  gears: ReadonlyMap<string, boolean>,
): HTMLElement {
  const sub = planned.shown;
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
  advInner.appendChild(
    cfgFieldEl("Providers", prov, "Only use these providers. Leave empty to use all of them."),
  );
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

  const pristine = subTargetFromForm(wrapper, isDefault);
  if (planned.source && pristine) {
    rowOrigin.set(wrapper, { ...planned.source, pristine });
  }
  return wrapper;
}

function buildRuleBlock(rule: PlannedRule, gears: ReadonlyMap<string, boolean>): HTMLElement {
  const block = el("div", { className: "lang-rule" });
  const scope: TargetScope = { kind: "rule", audio: rule.audio };

  block.appendChild(el("span", { className: "lang-label" }, "Audio:"));
  const header = el("div", { className: "lang-row" });
  const audioSel = langSelect(null, rule.audio, "Audio language");
  header.appendChild(audioSel);
  if (rule.storedAudio !== undefined) {
    ruleOrigin.set(block, { stored: rule.storedAudio, pristine: audioSel.value });
  }
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
        onclick: () =>
          subsContainer.appendChild(buildSubTarget({ shown: { code: "en" } }, scope, NO_GEARS)),
      },
      "+ Add subtitle",
    ),
  );

  return block;
}

/** The languages section a save writes; a stored target goes back as the file
 *  held it wherever the reader left it as built. */
export interface SavedLanguages {
  rules?: { audio: string; subtitles: RawTarget[] }[];
  default?: RawTarget[];
}

// serializeLanguagesFromForm reads the language builder DOM back into the
// languages section value for the structured save. Shape mirrors what the
// old YAML emitter produced: rules (each audio + subtitles, [] when a rule
// has no targets) and default, both omitted entirely when their container
// is empty.
export function serializeLanguagesFromForm(): SavedLanguages {
  const languages: SavedLanguages = {};

  // Rules.
  const rulesEl = document.getElementById("lang-rules");
  if (rulesEl && rulesEl.children.length > 0) {
    const rules: { audio: string; subtitles: RawTarget[] }[] = [];
    for (const block of Array.from(rulesEl.children)) {
      const audioSel = block.querySelector<HTMLSelectElement>(".lang-row .lang-select");
      if (!audioSel) {
        continue;
      }
      const origin = ruleOrigin.get(block);
      const audio = origin?.pristine === audioSel.value ? origin.stored : audioSel.value;
      const rows = Array.from(block.querySelectorAll(".lang-subs > .lang-sub"));
      rules.push({ audio, subtitles: targetsFromForm(rows, false) });
    }
    languages.rules = rules;
  }

  // Defaults.
  const defaultsEl = document.getElementById("lang-defaults");
  if (defaultsEl && defaultsEl.children.length > 0) {
    languages.default = targetsFromForm(Array.from(defaultsEl.children), true);
  }

  return languages;
}

function targetsFromForm(rows: readonly Element[], isDefault: boolean): RawTarget[] {
  const out: RawTarget[] = [];
  let i = 0;
  for (let row = rows[0]; row; row = rows[i]) {
    const origin = rowOrigin.get(row);
    const group = origin?.group;
    if (group && groupUntouched(rows.slice(i, i + group.size), group, isDefault)) {
      out.push(group.stored);
      i += group.size;
      continue;
    }
    const st = subTargetFromForm(row, isDefault);
    if (st) {
      out.push(origin ? keepUntouched(st, origin) : { ...st });
    }
    i++;
  }
  return out;
}

function groupUntouched(
  rows: readonly Element[],
  group: VariantGroup,
  isDefault: boolean,
): boolean {
  return (
    rows.length === group.size &&
    rows.every((row) => {
      const origin = rowOrigin.get(row);
      return (
        origin?.group === group && sameValue(subTargetFromForm(row, isDefault), origin.pristine)
      );
    })
  );
}

/** `current` with each field the reader left as built taken from the stored
 *  target instead, fields the row has no control for included. */
function keepUntouched(current: SubtitleTarget, origin: RowOrigin): RawTarget {
  const now: Record<string, unknown> = { ...current };
  const built: Record<string, unknown> = { ...origin.pristine };
  const stored = origin.stored;
  const out: Record<string, unknown> = {};
  for (const k of new Set([...Object.keys(now), ...Object.keys(built), ...Object.keys(stored)])) {
    const v = sameValue(now[k], built[k]) ? stored[k] : now[k];
    if (v !== undefined) {
      out[k] = v;
    }
  }
  if (out["variant"] !== undefined && out["variants"] !== undefined) {
    // The loader refuses a target carrying both, so the picked variant wins.
    delete out["variants"];
  }
  if (out["variant"] === DEFAULT_VARIANT) {
    delete out["variant"];
  }
  return out;
}

function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function subTargetFromForm(row: Element, isDefault: boolean): SubtitleTarget | null {
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
