// config-values.ts — Typed accessors over the structured config sections
// from GET /api/config/structured. Replaces the raw-YAML text extraction
// helpers (the former config-yaml.ts): the server parses the config file and
// serves it as JSON keyed by top-level YAML section name (secrets redacted
// to ""), so reading a current value is object navigation instead of line
// scanning. The section state lives here (not in config.ts) so the renderer
// modules can import the accessors without a circular import.

import type { SubtitleTarget } from "./wire/types.gen.js";

let cfgSections: Record<string, unknown> = {};
let secretsPresent: ReadonlySet<string> = new Set();

/** Replace the structured sections and the dotted paths of the secrets the
 *  file holds a value for. loadConfig() calls this on every fetch, with {}
 *  when the fetch fails, so the form renders schema defaults. */
export function setCfgSections(
  sections: Record<string, unknown>,
  presentSecrets: readonly string[] = [],
): void {
  cfgSections = sections;
  secretsPresent = new Set(presentSecrets);
}

/** cfgSecretPresent reports whether the stored config holds a value for the
 *  secret at a dotted schema path ("sonarr.api_key", "auth.oidc.client_secret").
 *  The value itself is redacted, so this is the only way to tell "saved" from
 *  "never set". */
export function cfgSecretPresent(path: string): boolean {
  return secretsPresent.has(path);
}

/** All sections as entries — for rendering unknown (non-schema) sections and
 *  the "does a config exist at all" first-setup check. */
export function cfgSectionEntries(): [string, unknown][] {
  return Object.entries(cfgSections);
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** scalarString renders a JSON value the way the old YAML line extraction
 *  did: string/number/boolean become their string form, missing/null become
 *  "". Arrays of scalars join with ", " (the same display the parsed-config
 *  path uses); nested objects render "" (the old text extractor found no
 *  inline value on the key's line). */
export function scalarString(v: unknown): string {
  if (Array.isArray(v)) {
    return v.map(scalarString).join(", ");
  }
  if (typeof v === "string") {
    return v;
  }
  if (typeof v === "number" || typeof v === "boolean") {
    return String(v);
  }
  return "";
}

/** cfgSection returns a section's mapping form, or undefined when the
 *  section is missing or not a mapping (scalar or list sections). Internal:
 *  the typed per-field accessors below are the consumed surface. */
function cfgSection(section: string): Record<string, unknown> | undefined {
  const v = cfgSections[section];
  return isRecord(v) ? v : undefined;
}

/** cfgUnrendered returns the stored entries of the mapping at `path` (a
 *  section name, then nested keys) whose keys are not in `rendered`, or {} when
 *  that mapping is absent. A save merges them under what the form produced, so
 *  a key the form has no control for is written back rather than deleted. */
export function cfgUnrendered(
  path: readonly string[],
  rendered: ReadonlySet<string>,
): Record<string, unknown> {
  let v: unknown = cfgSections;
  for (const key of path) {
    v = isRecord(v) ? v[key] : undefined;
  }
  if (!isRecord(v)) {
    return {};
  }
  return Object.fromEntries(Object.entries(v).filter(([k]) => !rendered.has(k)));
}

/** cfgValue reads one scalar field from a mapping section ("" when the
 *  section or key is missing). */
export function cfgValue(section: string, key: string): string {
  return scalarString(cfgSection(section)?.[key]);
}

/** cfgSubValue reads section.sub.key — the scoring weights nesting. */
export function cfgSubValue(section: string, sub: string, key: string): string {
  const subObj = cfgSection(section)?.[sub];
  return isRecord(subObj) ? scalarString(subObj[key]) : "";
}

/** cfgBool reads a boolean field. Missing/null yields def; any present
 *  value other than false / "false" counts as true (mirroring the old
 *  `extractYAMLValue(...) !== "false"` checks). */
export function cfgBool(section: string, key: string, def: boolean): boolean {
  const v = cfgSection(section)?.[key];
  if (v === undefined || v === null) {
    return def;
  }
  if (typeof v === "boolean") {
    return v;
  }
  return scalarString(v) !== "false";
}

/** cfgScalar reads a section whose entire value is one scalar — the
 *  top-level `poll_interval: 30s` line. */
export function cfgScalar(section: string): string {
  const v = cfgSections[section];
  return isRecord(v) ? "" : scalarString(v);
}

/** cfgList reads a list section (media_roots, trusted_proxies, ...) as
 *  strings, dropping null/empty entries like the old `- item` line scan. */
export function cfgList(section: string): string[] {
  const v = cfgSections[section];
  if (!Array.isArray(v)) {
    return [];
  }
  return v.map(scalarString).filter((s) => s !== "");
}

/** CfgProviderBlock is one provider's entry under the providers section. */
export interface CfgProviderBlock {
  enabled?: boolean;
  priority?: number;
  settings?: Record<string, unknown>;
}

/** cfgProviderBlock returns one provider's block, or undefined when the
 *  provider has no entry at all. A bare `name:` entry (YAML null) is
 *  present-but-empty — {} — matching the old text parser, for which any
 *  present block without `enabled: false` counted as enabled. */
export function cfgProviderBlock(name: string): CfgProviderBlock | undefined {
  const providers = cfgSection("providers");
  if (providers === undefined || !(name in providers)) {
    return undefined;
  }
  const block = providers[name];
  const out: CfgProviderBlock = {};
  if (!isRecord(block)) {
    return out;
  }
  if (typeof block["enabled"] === "boolean") {
    out.enabled = block["enabled"];
  }
  if (typeof block["priority"] === "number") {
    out.priority = block["priority"];
  }
  const settings = block["settings"];
  if (isRecord(settings)) {
    out.settings = settings;
  }
  return out;
}

function stringList(v: unknown): string[] | undefined {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : undefined;
}

/** A languages target as the file holds it. A typed field may carry a
 *  `${VAR}` string the loader expands, so this is what a save writes back. */
export type RawTarget = Readonly<Record<string, unknown>>;

/** One stored subtitle target: `raw` losslessly, `typed` its reading for the
 *  form, which drops a value whose stored type differs from the effective one. */
export interface StoredTarget {
  readonly raw: RawTarget;
  readonly typed: SubtitleTarget;
}

export interface StoredRule {
  readonly audio: string;
  readonly subtitles: readonly StoredTarget[];
}

/** The stored languages section; a missing list stays undefined. */
export interface StoredLanguages {
  readonly rules?: readonly StoredRule[];
  readonly default?: readonly StoredTarget[];
}

function storedTarget(v: unknown): StoredTarget | null {
  if (!isRecord(v) || typeof v["code"] !== "string") {
    return null;
  }
  const t: SubtitleTarget = { code: v["code"] };
  if (typeof v["variant"] === "string") {
    t.variant = v["variant"];
  }
  if (typeof v["min_score"] === "number") {
    t.min_score = v["min_score"];
  }
  const variants = stringList(v["variants"]);
  if (variants !== undefined) {
    t.variants = variants;
  }
  const providers = stringList(v["providers"]);
  if (providers !== undefined) {
    t.providers = providers;
  }
  const exclude = stringList(v["exclude"]);
  if (exclude !== undefined) {
    t.exclude = exclude;
  }
  return { raw: { ...v }, typed: t };
}

function storedTargets(v: unknown): StoredTarget[] {
  return Array.isArray(v) ? v.map(storedTarget).filter((t) => t !== null) : [];
}

/** cfgLanguageRules reads the stored languages section, the value a settings
 *  save replaces. A malformed entry is dropped. */
export function cfgLanguageRules(): StoredLanguages {
  const sect = cfgSection("languages");
  if (sect === undefined) {
    return {};
  }
  const out: { rules?: StoredRule[]; default?: StoredTarget[] } = {};
  if (Array.isArray(sect["rules"])) {
    const rules: StoredRule[] = [];
    for (const r of sect["rules"] as unknown[]) {
      if (isRecord(r) && typeof r["audio"] === "string") {
        rules.push({ audio: r["audio"], subtitles: storedTargets(r["subtitles"]) });
      }
    }
    out.rules = rules;
  }
  if (Array.isArray(sect["default"])) {
    out.default = storedTargets(sect["default"]);
  }
  return out;
}
