// wizard-state.ts — Pure first-boot wizard logic: prefill from the
// structured config, the per-step {prefill, satisfied, validate, touched}
// matrix's satisfied/collapse gating, draft fingerprint + touched-only
// overlay, and the GET-overlay-PUT full-map save assembly. No DOM, no
// network: wizard.ts owns the flow, this module owns the decisions, so the
// vitest suite can pin them without a browser.

import { EXAMPLE_SECTIONS } from "./wizard-example.js";
import { DEFAULT_VARIANT } from "./constants.js";
import type { SchemaField, SchemaSection } from "./api-types.js";

/** Sections is the structured config map: top-level YAML section name to
 *  its JSON value (the wire StructuredConfig.sections after decode). */
export type Sections = Record<string, unknown>;

/** WizardBoot is everything the flow reads at entry: the full structured
 *  map, the secret-presence set, and the validity signal. */
export interface WizardBoot {
  sections: Sections;
  secretsPresent: ReadonlySet<string>;
  configValid: boolean;
}

/** LangRow is the wizard's flattened language-rule row. */
interface LangRow {
  audio: string;
  code: string;
  variant: string;
}

/** LangDefaultRow is one fallback language entry. */
interface LangDefaultRow {
  code: string;
  variant: string;
}

/** WizardModel is the wizard's editable state, prefilled from the
 *  structured config and overlaid by a (fingerprint-valid) draft. */
export interface WizardModel {
  wizardValues: Record<string, Record<string, string>>;
  providerEnabled: Record<string, boolean>;
  langRules: LangRow[];
  langDefault: LangDefaultRow[];
  mediaRoots: string[];
}

// --- Step identity (the design matrix rows) ---

export const STEP_IDS = [
  "arr",
  "media_roots",
  "providers",
  "languages",
  "search",
  "scoring",
  "post_processing",
] as const;

/** StepID identifies one wizard step. */
export type StepID = (typeof STEP_IDS)[number];

/** MANDATORY_STEPS are the steps whose satisfaction gates the R3.5
 *  "everything looks configured — finish" fast path. The remaining steps
 *  are tunable defaults a user may accept untouched. */
export const MANDATORY_STEPS: readonly StepID[] = ["arr", "media_roots", "providers", "languages"];

/** STEP_SECTIONS maps each step to the config sections it owns — the units
 *  of the touched-section overlay on save and of the differs-from-example
 *  comparison. */
const STEP_SECTIONS: Record<StepID, readonly string[]> = {
  arr: ["sonarr", "radarr"],
  media_roots: ["media_roots"],
  providers: ["providers"],
  languages: ["languages"],
  search: ["search", "adaptive"],
  scoring: ["scoring"],
  post_processing: ["post_processing"],
};

// --- Structural helpers ---

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function section(sections: Sections, key: string): Record<string, unknown> {
  const v = sections[key];
  return isRecord(v) ? v : {};
}

function str(v: unknown): string {
  if (typeof v === "string") {
    return v;
  }
  if (typeof v === "number" || typeof v === "boolean") {
    return String(v);
  }
  return "";
}

/** stableStringify renders a value as JSON with sorted object keys, so two
 *  structurally equal values compare equal regardless of key order. */
function stableStringify(v: unknown): string {
  if (v === undefined) {
    // JSON.stringify(undefined) yields undefined (not a string); normalize.
    return "null";
  }
  if (Array.isArray(v)) {
    return "[" + v.map(stableStringify).join(",") + "]";
  }
  if (isRecord(v)) {
    const keys = Object.keys(v).sort();
    return "{" + keys.map((k) => JSON.stringify(k) + ":" + stableStringify(v[k])).join(",") + "}";
  }
  return JSON.stringify(v);
}

function deepEqual(a: unknown, b: unknown): boolean {
  return stableStringify(a) === stableStringify(b);
}

// --- Prefill (R3.2: every non-secret value plus secret-presence state) ---

/** fieldMapFromSection flattens a mapping section into the wizard's string
 *  form map: bools become "true"/"false", numbers their decimal form,
 *  scalar arrays comma-joined (exclude_arr_tags), nested objects skipped. */
function fieldMapFromSection(sect: Record<string, unknown>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(sect)) {
    if (Array.isArray(v)) {
      out[k] = v.map(str).filter(Boolean).join(", ");
    } else if (!isRecord(v) && v !== null && v !== undefined) {
      out[k] = str(v);
    }
  }
  return out;
}

/** prefillModel builds the wizard model from the structured config: the
 *  "prefill" column of the step matrix. Secret values arrive redacted-empty
 *  by design; presence is carried separately (WizardBoot.secretsPresent). */
export function prefillModel(sections: Sections): WizardModel {
  const model: WizardModel = {
    wizardValues: {},
    providerEnabled: {},
    langRules: [],
    langDefault: [],
    mediaRoots: [],
  };

  for (const key of ["sonarr", "radarr", "search", "adaptive", "post_processing"]) {
    if (isRecord(sections[key])) {
      model.wizardValues[key] = fieldMapFromSection(section(sections, key));
    }
  }

  // Scoring nests its numbers under "weights"; the wizard edits them flat.
  const weights = section(section(sections, "scoring"), "weights");
  if (Object.keys(weights).length > 0) {
    model.wizardValues["scoring"] = fieldMapFromSection(weights);
  }

  const roots = sections["media_roots"];
  if (Array.isArray(roots)) {
    model.mediaRoots = roots.map(str).filter(Boolean);
  }

  // Providers: enabled flags + settings string maps ("prov_<name>").
  const provs = section(sections, "providers");
  for (const [name, block] of Object.entries(provs)) {
    if (!isRecord(block)) {
      // A bare `name:` entry counts as present-and-enabled (settings-dialog
      // semantics: any present block without enabled:false is enabled).
      model.providerEnabled[name] = true;
      continue;
    }
    model.providerEnabled[name] = block["enabled"] !== false;
    const settings = block["settings"];
    if (isRecord(settings)) {
      model.wizardValues["prov_" + name] = fieldMapFromSection(settings);
    }
  }

  // Languages: flatten rules to one row per (audio, subtitle target).
  const langs = section(sections, "languages");
  const rules = langs["rules"];
  if (Array.isArray(rules)) {
    for (const rule of rules) {
      if (!isRecord(rule)) {
        continue;
      }
      const audio = str(rule["audio"]);
      const subs = rule["subtitles"];
      if (!Array.isArray(subs)) {
        continue;
      }
      for (const sub of subs) {
        if (!isRecord(sub)) {
          continue;
        }
        model.langRules.push({
          audio,
          code: str(sub["code"]),
          variant: subtitleVariant(sub),
        });
      }
    }
  }
  const defs = langs["default"];
  if (Array.isArray(defs)) {
    for (const d of defs) {
      if (!isRecord(d)) {
        continue;
      }
      model.langDefault.push({ code: str(d["code"]), variant: subtitleVariant(d) });
    }
  }

  return model;
}

/** subtitleVariant resolves a subtitle target's variant: singular
 *  `variant`, else the first of `variants`, else the standard default. */
function subtitleVariant(sub: Record<string, unknown>): string {
  const v = str(sub["variant"]);
  if (v !== "") {
    return v;
  }
  const vs = sub["variants"];
  if (Array.isArray(vs) && vs.length > 0) {
    return str(vs[0]) || DEFAULT_VARIANT;
  }
  return DEFAULT_VARIANT;
}

// --- Satisfied / collapse gating (R3.3) ---

/** arrComplete reports whether one arr section is fully configured: URL set
 *  and API key either entered or already saved (presence flag). */
function arrComplete(sections: Sections, present: ReadonlySet<string>, key: string): boolean {
  const sect = section(sections, key);
  if (str(sect["url"]).trim() === "" || sect["enabled"] === false) {
    return false;
  }
  return str(sect["api_key"]).trim() !== "" || present.has(key + ".api_key");
}

/** providerEnabledInSections reports whether a provider's config block is
 *  present and enabled (a bare `name:` entry counts as enabled, matching the
 *  settings-dialog semantics). Used both by the satisfied matrix and by the
 *  providers step's credential guard: a provider the SERVER-validated config
 *  already enables must not be second-guessed by the wizard (the example
 *  config enables credential-optional providers like animetosho). */
export function providerEnabledInSections(sections: Sections, name: string): boolean {
  const provs = sections["providers"];
  if (!isRecord(provs) || !(name in provs)) {
    return false;
  }
  const block = provs[name];
  return !isRecord(block) || block["enabled"] !== false;
}

/** stepComplete is the per-step structural-completeness column of the
 *  matrix, evaluated over the CONFIG (not form state): does the section
 *  actually answer the step's question? */
function stepComplete(step: StepID, boot: WizardBoot): boolean {
  const { sections, secretsPresent } = boot;
  switch (step) {
    case "arr":
      return (
        arrComplete(sections, secretsPresent, "sonarr") ||
        arrComplete(sections, secretsPresent, "radarr")
      );
    case "media_roots": {
      const roots = sections["media_roots"];
      return Array.isArray(roots) && roots.some((r) => str(r).trim() !== "");
    }
    case "providers": {
      const provs = section(sections, "providers");
      return Object.keys(provs).some((name) => providerEnabledInSections(sections, name));
    }
    case "languages": {
      const langs = section(sections, "languages");
      const rules = langs["rules"];
      const defs = langs["default"];
      return (Array.isArray(rules) && rules.length > 0) || (Array.isArray(defs) && defs.length > 0);
    }
    // Tunable-defaults steps carry no completeness requirement.
    case "search":
    case "scoring":
    case "post_processing":
      return true;
  }
}

/** differsFromExample reports whether ANY of the step's sections differs
 *  from the embedded example default. Fresh boots auto-write
 *  config.example.yaml, so equality means placeholder data. */
export function differsFromExample(step: StepID, sections: Sections): boolean {
  return STEP_SECTIONS[step].some(
    (key) => !deepEqual(sections[key] ?? null, EXAMPLE_SECTIONS[key] ?? null),
  );
}

/** stepSatisfied is the collapse gate (R3.3, settled): a step may
 *  auto-collapse ONLY when the config is valid AND its section differs from
 *  the embedded example default AND the section structurally answers the
 *  step. A fresh volume (example config, config_valid=false) satisfies
 *  nothing — the FULL walk. */
export function stepSatisfied(step: StepID, boot: WizardBoot): boolean {
  return boot.configValid && differsFromExample(step, boot.sections) && stepComplete(step, boot);
}

/** satisfiedSteps evaluates the whole matrix once. */
export function satisfiedSteps(boot: WizardBoot): Set<StepID> {
  const out = new Set<StepID>();
  for (const id of STEP_IDS) {
    if (stepSatisfied(id, boot)) {
      out.add(id);
    }
  }
  return out;
}

/** fastPathAvailable is the R3.5 gate: config valid and every mandatory
 *  step satisfied — the flow may open directly on the review screen. */
export function fastPathAvailable(boot: WizardBoot): boolean {
  return boot.configValid && MANDATORY_STEPS.every((id) => stepSatisfied(id, boot));
}

// --- Draft (R3.7) ---

/** WizardDraft is the localStorage draft: the SANITIZED model (v2 excludes
 *  every schema secret field — see buildDraftJSON) plus the fingerprint of
 *  the redacted config it was started against and the set of steps the user
 *  actually touched. Only touched steps overlay the fresh prefill. */
export interface WizardDraft {
  /** Draft format version. v2 = secret-sanitized model; v1 predates the
   *  sanitization (it persisted plaintext credentials) and is dropped. */
  v: 2;
  fingerprint: string;
  stepId: string;
  touched: StepID[];
  model: WizardModel;
}

/** fingerprintBoot hashes the redacted structured config + presence set.
 *  Any server-side config change (or secret appearing/vanishing) changes the
 *  fingerprint and invalidates outstanding drafts. */
export function fingerprintBoot(sections: Sections, secretsPresent: readonly string[]): string {
  const s = stableStringify({ sections, secrets: [...secretsPresent].sort() });
  // djb2-xor over the stable form; length suffix guards short collisions.
  let h = 5381;
  for (let i = 0; i < s.length; i++) {
    h = ((h * 33) ^ s.charCodeAt(i)) >>> 0;
  }
  return h.toString(36) + ":" + s.length.toString(36);
}

/** parseDraft validates a stored draft against the current fingerprint.
 *  Unparseable, wrong-shape, stale-fingerprint, or wrong-VERSION drafts are
 *  dropped — a v1 draft predates secret sanitization and may hold plaintext
 *  credentials, so it must never overlay (the caller clears storage on
 *  null). The embedded model is normalized field by field (localStorage is
 *  untrusted). */
export function parseDraft(raw: string | null, fingerprint: string): WizardDraft | null {
  if (!raw) {
    return null;
  }
  let d: unknown;
  try {
    d = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!isRecord(d) || d["v"] !== 2 || d["fingerprint"] !== fingerprint) {
    return null;
  }
  const model = d["model"];
  const touched = d["touched"];
  if (!isRecord(model) || !Array.isArray(touched)) {
    return null;
  }
  const ids = touched.filter((t): t is StepID => (STEP_IDS as readonly string[]).includes(str(t)));
  return {
    v: 2,
    fingerprint,
    stepId: str(d["stepId"]),
    touched: ids,
    model: normalizeModel(model),
  };
}

// --- Draft secret sanitization (drafts persist across browser restarts) ---

/** isSecretField reports whether a schema field is a secret. The schema
 *  marks secrets two ways: app sections type the field "secret" (the arr
 *  api_key), provider settings additionally set the Secret flag — either
 *  marker counts. */
function isSecretField(f: SchemaField): boolean {
  return f.secret === true || f.type === "secret";
}

/** collectSecretKeys adds a field list's secret keys to out, recursing into
 *  nested field groups (defense in depth: the wizard collects only top-level
 *  fields today, but a nested secret must never slip through a future
 *  collector either). */
function collectSecretKeys(fields: readonly SchemaField[] | undefined, out: Set<string>): void {
  for (const f of fields ?? []) {
    if (isSecretField(f)) {
      out.add(f.key);
    }
    collectSecretKeys(f.fields, out);
  }
}

/** secretFieldsBySection derives, from the config schema, which wizardValues
 *  entries hold secrets: form-map key (section key, or "prov_<name>" for a
 *  provider's settings) to the set of secret field keys under it. */
function secretFieldsBySection(schema: readonly SchemaSection[]): Map<string, Set<string>> {
  const out = new Map<string, Set<string>>();
  for (const sect of schema) {
    const keys = new Set<string>();
    collectSecretKeys(sect.fields, keys);
    if (keys.size > 0) {
      out.set(sect.key, keys);
    }
    for (const prov of sect.providers ?? []) {
      const provKeys = new Set<string>();
      collectSecretKeys(prov.settings, provKeys);
      if (provKeys.size > 0) {
        out.set("prov_" + prov.name, provKeys);
      }
    }
  }
  return out;
}

/** sanitizeModelForDraft deep-copies the model WITHOUT any schema secret
 *  field — neither the values nor the secret-bearing keys reach storage. */
function sanitizeModelForDraft(
  model: WizardModel,
  secrets: ReadonlyMap<string, ReadonlySet<string>>,
): WizardModel {
  const wizardValues: Record<string, Record<string, string>> = {};
  for (const [sectionKey, vals] of Object.entries(model.wizardValues)) {
    const secretKeys = secrets.get(sectionKey);
    const clean: Record<string, string> = {};
    for (const [k, v] of Object.entries(vals)) {
      if (!secretKeys?.has(k)) {
        clean[k] = v;
      }
    }
    wizardValues[sectionKey] = clean;
  }
  return {
    wizardValues,
    providerEnabled: { ...model.providerEnabled },
    langRules: model.langRules.map((r) => ({ ...r })),
    langDefault: model.langDefault.map((d) => ({ ...d })),
    mediaRoots: [...model.mediaRoots],
  };
}

/** buildDraftJSON assembles and serializes the persistent draft (v2). The
 *  draft survives browser restarts in localStorage, so every schema-declared
 *  secret field (arr api_key, provider credentials) is EXCLUDED: newly typed
 *  secrets live only in page memory and are re-entered after a reload, while
 *  already-SAVED secrets keep riding the boot snapshot's presence flags,
 *  which were never part of the draft model. */
export function buildDraftJSON(
  schema: readonly SchemaSection[],
  fingerprint: string,
  stepId: string,
  touched: readonly StepID[],
  model: WizardModel,
): string {
  const draft: WizardDraft = {
    v: 2,
    fingerprint,
    stepId,
    touched: [...touched],
    model: sanitizeModelForDraft(model, secretFieldsBySection(schema)),
  };
  return JSON.stringify(draft);
}

/** normalizeModel rebuilds a trusted WizardModel from an untrusted stored
 *  record, coercing each slice to its expected shape. */
function normalizeModel(m: Record<string, unknown>): WizardModel {
  const out: WizardModel = {
    wizardValues: {},
    providerEnabled: {},
    langRules: [],
    langDefault: [],
    mediaRoots: [],
  };
  const wv = m["wizardValues"];
  if (isRecord(wv)) {
    for (const [k, v] of Object.entries(wv)) {
      if (!isRecord(v)) {
        continue;
      }
      const vals: Record<string, string> = {};
      for (const [fk, fv] of Object.entries(v)) {
        vals[fk] = str(fv);
      }
      out.wizardValues[k] = vals;
    }
  }
  const pe = m["providerEnabled"];
  if (isRecord(pe)) {
    for (const [k, v] of Object.entries(pe)) {
      out.providerEnabled[k] = v === true;
    }
  }
  const rules = m["langRules"];
  if (Array.isArray(rules)) {
    for (const r of rules) {
      if (isRecord(r)) {
        out.langRules.push({
          audio: str(r["audio"]),
          code: str(r["code"]),
          variant: str(r["variant"]),
        });
      }
    }
  }
  const defs = m["langDefault"];
  if (Array.isArray(defs)) {
    for (const r of defs) {
      if (isRecord(r)) {
        out.langDefault.push({ code: str(r["code"]), variant: str(r["variant"]) });
      }
    }
  }
  const roots = m["mediaRoots"];
  if (Array.isArray(roots)) {
    out.mediaRoots = roots.map(str);
  }
  return out;
}

/** overlayDraft copies ONLY the draft-touched steps' slices of the model
 *  onto a fresh prefill; untouched steps keep the server snapshot's values. */
export function overlayDraft(fresh: WizardModel, draft: WizardDraft): WizardModel {
  const out: WizardModel = {
    wizardValues: { ...fresh.wizardValues },
    providerEnabled: { ...fresh.providerEnabled },
    langRules: [...fresh.langRules],
    langDefault: [...fresh.langDefault],
    mediaRoots: [...fresh.mediaRoots],
  };
  const dm = draft.model;
  for (const step of draft.touched) {
    switch (step) {
      case "arr":
        copyValues(out, dm, ["sonarr", "radarr"]);
        break;
      case "media_roots":
        out.mediaRoots = [...dm.mediaRoots];
        break;
      case "providers":
        out.providerEnabled = { ...dm.providerEnabled };
        for (const key of Object.keys(dm.wizardValues)) {
          if (key.startsWith("prov_")) {
            copyValues(out, dm, [key]);
          }
        }
        break;
      case "languages":
        out.langRules = [...dm.langRules];
        out.langDefault = [...dm.langDefault];
        break;
      case "search":
        copyValues(out, dm, ["search", "adaptive"]);
        break;
      case "scoring":
        copyValues(out, dm, ["scoring"]);
        break;
      case "post_processing":
        copyValues(out, dm, ["post_processing"]);
        break;
    }
  }
  return out;
}

function copyValues(out: WizardModel, from: WizardModel, keys: readonly string[]): void {
  for (const key of keys) {
    const vals = from.wizardValues[key];
    if (vals) {
      out.wizardValues[key] = { ...vals };
    }
  }
}

// --- Save assembly (R3.4: GET-overlay-PUT the FULL map) ---

/** typedValue mirrors the retired YAML emitter's value typing: bool words
 *  become booleans, all-digit strings numbers, everything else stays a
 *  string (the server rejects genuinely wrong types at validation). */
function typedValue(v: string): unknown {
  if (v === "true") {
    return true;
  }
  if (v === "false") {
    return false;
  }
  if (/^\d+$/.test(v)) {
    return Number(v);
  }
  return v;
}

/** serializeFields merges collected form values over the boot section:
 *  empty values are omitted (an omitted secret leaf merges server-side from
 *  the existing file; an omitted plain field falls back to defaults), and
 *  exclude_arr_tags splits its comma-joined form back to a list. */
function serializeFields(
  boot: Record<string, unknown>,
  vals: Record<string, string> | undefined,
): Record<string, unknown> {
  const collected = vals ?? {};
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(boot)) {
    // A cleared collected field is dropped rather than left at the boot
    // value — clearing must take effect; secrets stay clearing-proof because
    // the server treats an ABSENT secret leaf as "keep existing".
    if (k in collected && collected[k]?.trim() === "") {
      continue;
    }
    out[k] = v;
  }
  for (const [k, v] of Object.entries(collected)) {
    const trimmed = v.trim();
    if (trimmed === "") {
      continue;
    }
    if (k === "exclude_arr_tags") {
      out[k] = trimmed
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      continue;
    }
    out[k] = typedValue(trimmed);
  }
  return out;
}

/** hasFilled reports whether any collected value is non-empty. */
function hasFilled(vals: Record<string, string> | undefined): boolean {
  return Object.values(vals ?? {}).some((v) => v.trim() !== "");
}

/** buildSaveSections assembles the FULL section map for the PUT: a deep
 *  copy of the boot snapshot with ONLY the touched steps' sections
 *  overlaid. Untouched sections (logging, backup, auth, trusted_proxies,
 *  embedded_subtitles, ...) survive by round-trip — the server's
 *  canonicalizer emits exactly what it is given, so omission would DELETE
 *  them. */
export function buildSaveSections(
  boot: Sections,
  model: WizardModel,
  touched: ReadonlySet<StepID>,
): Sections {
  let out: Sections = JSON.parse(JSON.stringify(boot)) as Sections;

  if (touched.has("arr")) {
    out = serializeArr(out, model, "sonarr");
    out = serializeArr(out, model, "radarr");
  }
  if (touched.has("media_roots")) {
    const roots = model.mediaRoots.map((r) => r.trim()).filter(Boolean);
    if (roots.length > 0) {
      out["media_roots"] = roots;
    } else {
      out = omitKey(out, "media_roots");
    }
  }
  if (touched.has("providers")) {
    out["providers"] = serializeProviders(section(boot, "providers"), model);
  }
  if (touched.has("languages")) {
    out["languages"] = serializeLanguages(model);
  }
  if (touched.has("search")) {
    out["search"] = serializeFields(section(boot, "search"), model.wizardValues["search"]);
    out["adaptive"] = serializeFields(section(boot, "adaptive"), model.wizardValues["adaptive"]);
  }
  if (touched.has("scoring")) {
    const bootWeights = section(section(boot, "scoring"), "weights");
    out["scoring"] = { weights: serializeFields(bootWeights, model.wizardValues["scoring"]) };
  }
  if (touched.has("post_processing")) {
    out["post_processing"] = serializeFields(
      section(boot, "post_processing"),
      model.wizardValues["post_processing"],
    );
  }
  return out;
}

/** omitKey returns sections without one key (omission = deletion on the
 *  server side; the deliberate way to drop a section). */
function omitKey(sections: Sections, key: string): Sections {
  const out: Sections = {};
  for (const [k, v] of Object.entries(sections)) {
    if (k !== key) {
      out[k] = v;
    }
  }
  return out;
}

/** serializeArr overlays one arr section: filled form values merge over the
 *  boot section with enabled:true; an emptied form deletes the section
 *  (omission = removal, the deliberate way to drop an arr). */
function serializeArr(out: Sections, model: WizardModel, key: string): Sections {
  const vals = model.wizardValues[key];
  if (!hasFilled(vals)) {
    return omitKey(out, key);
  }
  const merged = serializeFields(section(out, key), vals);
  merged["enabled"] = true;
  out[key] = merged;
  return out;
}

/** serializeProviders rebuilds the providers section: enabled providers
 *  merge collected settings over their boot block (preserving priority and
 *  untouched settings); disabled providers keep their boot block with
 *  enabled:false (credentials survive a temporary disable); providers absent
 *  from the boot map and left disabled are omitted. */
function serializeProviders(boot: Record<string, unknown>, model: WizardModel): Sections {
  const out: Sections = {};
  const names = new Set<string>([...Object.keys(boot), ...Object.keys(model.providerEnabled)]);
  for (const name of names) {
    const raw = boot[name];
    const bootBlock: Record<string, unknown> = isRecord(raw) ? raw : {};
    const enabled = model.providerEnabled[name] === true;
    if (!enabled) {
      if (name in boot) {
        out[name] = { ...bootBlock, enabled: false };
      }
      continue;
    }
    // Rebuild the block without its boot settings, then attach the merged
    // settings only when non-empty — so clearing every credential clears
    // the stored block instead of resurrecting it via the spread.
    const block: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(bootBlock)) {
      if (k !== "settings") {
        block[k] = v;
      }
    }
    block["enabled"] = true;
    const rawSettings = bootBlock["settings"];
    const bootSettings: Record<string, unknown> = isRecord(rawSettings) ? rawSettings : {};
    const settings = serializeFields(bootSettings, model.wizardValues["prov_" + name]);
    if (Object.keys(settings).length > 0) {
      block["settings"] = settings;
    }
    out[name] = block;
  }
  return out;
}

/** serializeLanguages renders the wizard's flattened rows back to the
 *  config shape (one rule per row, like the retired emitter; a rule's extra
 *  attributes — min_score, providers — are wizard-invisible and replaced
 *  when the step is touched). */
function serializeLanguages(model: WizardModel): Sections {
  const out: Sections = {};
  const rules = model.langRules.filter((r) => r.audio.trim() !== "" && r.code.trim() !== "");
  if (rules.length > 0) {
    out["rules"] = rules.map((r) => ({
      audio: r.audio.trim(),
      subtitles: [
        {
          code: r.code.trim(),
          ...(r.variant && r.variant !== DEFAULT_VARIANT ? { variant: r.variant } : {}),
        },
      ],
    }));
  }
  const defs = model.langDefault.filter((d) => d.code.trim() !== "");
  if (defs.length > 0) {
    out["default"] = defs.map((d) => ({
      code: d.code.trim(),
      ...(d.variant && d.variant !== DEFAULT_VARIANT ? { variant: d.variant } : {}),
    }));
  }
  return out;
}

// --- Post-login routing (R3.8) ---

/** PostLoginDestination is where a successful login lands. */
export type PostLoginDestination = "app" | "wizard" | "admin_needed_notice";

/** postLoginDestination routes a fresh login: a valid config goes to the
 *  app; an invalid one sends admins into the wizard and non-admins to the
 *  "an admin needs to finish setup" notice (every wizard endpoint is
 *  admin-gated — walking a non-admin into 403s is not a flow). */
export function postLoginDestination(role: string, configValid: boolean): PostLoginDestination {
  if (configValid) {
    return "app";
  }
  return role === "admin" ? "wizard" : "admin_needed_notice";
}
