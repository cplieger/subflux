// wizard-store.ts — The wizard's module state, below both wizard.ts and the
// step modules.
//
// It exists because the step modules read this state and wizard.ts renders
// them: every step module imported `wizardValues` and friends back from
// "./wizard.js", which closed a cycle per step module (three of them, all
// reported by knip). State that two layers share belongs under both, not in
// the parent — the same shape the coverage rows took when `coverage-store.ts`
// was carved out from under `coverage.ts` and `coverage-heal.ts`.
//
// The five model bindings stay LIVE ES module bindings, reassigned wholesale by
// `adoptModel`. That is why `adoptModel` lives here rather than in wizard.ts: a
// module may only assign its own bindings, so an importer cannot reassign them
// (TS2632) and the writer has to sit beside the declarations.

import type { SchemaSection } from "./api-types.js";
import type { Sections, WizardBoot, WizardModel } from "./wizard-state.js";

// --- Boot snapshot and schema ---

let fullSchema: SchemaSection[] = [];
let boot: WizardBoot = { sections: {}, secretsPresent: new Set(), configValid: false };

export function setSchema(s: SchemaSection[]): void {
  fullSchema = s;
}

export function setBoot(b: WizardBoot): void {
  boot = b;
}

/** The full schema, for the save path's draft sanitizer. */
export function fullSchemaSnapshot(): SchemaSection[] {
  return fullSchema;
}

/** The boot snapshot, for the step-satisfaction and fast-path decisions and for
 *  the save overlay. Read-only by convention, like `bootSections`. */
export function bootSnapshot(): WizardBoot {
  return boot;
}

export function schemaByKey(key: string): SchemaSection | undefined {
  return fullSchema.find((s: SchemaSection) => s.key === key);
}

/** Reports whether the config file already holds a value for a schema
 *  secret (dotted path): the steps render a saved placeholder and count
 *  the credential as present. */
export function secretSaved(path: string): boolean {
  return boot.secretsPresent.has(path);
}

/** Read-only by convention: steps consult it for config-blessed state. */
export function bootSections(): Sections {
  return boot.sections;
}

// --- Step-module-facing model bindings ---
//
// Live ES module bindings, reassigned at boot from the prefilled model. A step
// module MUTATES their contents (push, splice, indexed write), which is legal
// on an imported binding; only this module may reassign them.

export let wizardValues: Record<string, Record<string, string>> = {};
export let providerEnabled: Record<string, boolean> = {};
export let langRules: { audio: string; code: string; variant: string }[] = [];
export let langDefault: { code: string; variant: string }[] = [];
export let mediaRoots: string[] = [];

export function adoptModel(m: WizardModel): void {
  wizardValues = m.wizardValues;
  providerEnabled = m.providerEnabled;
  langRules = m.langRules;
  langDefault = m.langDefault;
  mediaRoots = m.mediaRoots;
}

export function currentModel(): WizardModel {
  return { wizardValues, providerEnabled, langRules, langDefault, mediaRoots };
}

/** Returns every module-scope binding to its initial value.
 *
 *  Called from wizard.ts's `_resetForTest`, and every `let` in this file must be
 *  listed here: the wizard cannot be re-evaluated to get a fresh graph, because
 *  Browser Mode keys its module map by URL so `vi.resetModules()` hands back the
 *  cached instance. A missed binding is cross-test pollution rather than a
 *  compile error, and the completeness invariant now spans two files. */
export function resetWizardStore(): void {
  fullSchema = [];
  boot = { sections: {}, secretsPresent: new Set(), configValid: false };
  wizardValues = {};
  providerEnabled = {};
  langRules = [];
  langDefault = [];
  mediaRoots = [];
}
