// config-values.ts — Typed accessors over the structured config sections
// from GET /api/config/structured. Replaces the raw-YAML text extraction
// helpers (the former config-yaml.ts): the server parses the config file and
// serves it as JSON keyed by top-level YAML section name (secrets redacted
// to ""), so reading a current value is object navigation instead of line
// scanning. The section state lives here (not in config.ts) so the renderer
// modules can import the accessors without a circular import.

let cfgSections: Record<string, unknown> = {};

/** Replace the structured sections. loadConfig() calls this on every fetch;
 *  {} on failure makes the form render schema defaults, the same degraded
 *  behavior as the old empty raw-YAML string. */
export function setCfgSections(sections: Record<string, unknown>): void {
  cfgSections = sections;
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
