// wizard.ts — Setup wizard extracted from login.ts. Invoked via startConfigWizard().

import { apiGet } from './api-client.js';
import { defineAction, ActionError, classifyFetchError, retryNetwork, RETRY_STANDARD, registerCleanup } from './actions/index.js';
import { LANGUAGES } from './languages.js';
import { $, showPage, showError, hideError } from './dom-core.js';
import { el, option } from './dom.js';
import { SUBTITLE_VARIANTS, YAML_TIMEOUT_MS, DEFAULT_VARIANT } from './constants.js';
import type { ProviderSchema, ConfigSchema } from './api-types.js';
import { buildProvidersStep } from './wizard-providers.js';
import { buildLanguagesStep } from './wizard-languages.js';
import { buildArrStep, buildMediaRootsStep, buildSearchStep, buildPostProcessStep } from './wizard-steps.js';

export interface WizardStep {
  id: string;
  title: string;
  render: (container: HTMLElement) => void;
  collect: () => void;
  validate: () => string;
  validateAsync?: (signal: AbortSignal) => Promise<string>;
}

interface SetupDraft {
  wizardIndex: number;
  wizardValues: Record<string, Record<string, string>>;
  providerEnabled: Record<string, boolean>;
  langRules: Array<{ audio: string; code: string; variant: string }>;
  langDefault: Array<{ code: string; variant: string }>;
  mediaRoots: string[];
}

let fullSchema: ConfigSchema[] = [];
let wizardSteps: WizardStep[] = [];
let wizardIndex = 0;
export let wizardValues: Record<string, Record<string, string>> = {};
export let providerEnabled: Record<string, boolean> = {};
export let langRules: Array<{ audio: string; code: string; variant: string }> = [];
export let langDefault: Array<{ code: string; variant: string }> = [];
export let mediaRoots: string[] = [];

export function schemaByKey(key: string): ConfigSchema | undefined {
  return fullSchema.find((s: ConfigSchema) => s['key'] === key);
}

const DRAFT_KEY = 'subflux-setup-draft';

function saveDraft(): void {
  try {
    const draft: SetupDraft = {
      wizardIndex, wizardValues, providerEnabled,
      langRules, langDefault, mediaRoots,
    };
    localStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
  } catch { /* localStorage full or unavailable */ }
}

function loadDraft(): boolean {
  try {
    const raw = localStorage.getItem(DRAFT_KEY);
    if (!raw) return false;
    const draft = JSON.parse(raw) as SetupDraft;
    wizardIndex = draft.wizardIndex ?? 0;
    wizardValues = draft.wizardValues ?? {};
    providerEnabled = draft.providerEnabled ?? {};
    langRules = draft.langRules ?? [];
    langDefault = draft.langDefault ?? [];
    mediaRoots = draft.mediaRoots ?? [];
    return true;
  } catch { return false; }
}

function clearDraft(): void {
  try { localStorage.removeItem(DRAFT_KEY); } catch { /* ignore */ }
}

export function infoIcon(tip: string): HTMLElement {
  return el('span', {
    className: 'wiz-info', textContent: 'i',
    'data-tip': tip, tabindex: '0', role: 'note'
  });
}

export function wizField(
  id: string, label: string, type: string,
  value: string, placeholder: string, tip: string | undefined
): HTMLElement {
  const lbl = el('label', { for: id }, label);
  if (tip) lbl.appendChild(infoIcon(tip));
  const inp = el('input', {
    type: type === 'number' ? 'number' : 'text',
    id, placeholder, value, autocomplete: 'off',
    'data-1p-ignore': '', 'data-lpignore': 'true',
    'data-bwignore': '', 'data-form-type': 'other',
    className: type === 'secret' ? 'wiz-masked' : ''
  });
  return el('div', { className: 'wiz-field' }, lbl, inp);
}

export function wizToggle(id: string, label: string, checked: boolean, tip: string | undefined): HTMLElement {
  const lbl = el('label', null, label);
  if (tip) lbl.appendChild(infoIcon(tip));
  const cb = el('input', { type: 'checkbox', id }) as HTMLInputElement;
  cb.checked = checked;
  const toggle = el('label', { className: 'wiz-toggle' }, cb, el('span'));
  return el('div', { className: 'wiz-field' }, lbl, toggle);
}

export function langSelect(id: string, value: string, placeholder: string): HTMLElement {
  const sel = el('select', { id, className: 'wiz-lang-select' }) as HTMLSelectElement;
  sel.appendChild(option('', placeholder));
  for (const [code, name] of LANGUAGES) {
    sel.appendChild(option(code, name + ' (' + code + ')'));
  }
  sel.value = value;
  return sel;
}

export function variantSelect(id: string, value: string): HTMLElement {
  const sel = el('select', { id, className: 'wiz-lang-variant' }) as HTMLSelectElement;
  for (const v of SUBTITLE_VARIANTS) {
    sel.appendChild(option(v.value, v.label));
  }
  sel.value = value || DEFAULT_VARIANT;
  return sel;
}

export async function startConfigWizard(): Promise<void> {
  const schema = await apiGet<ConfigSchema[]>('/api/config/schema');
  if (!schema) { window.location.href = '/'; return; }
  fullSchema = schema;
  wizardSteps = [
    buildArrStep(), buildMediaRootsStep(), buildProvidersStep(),
    buildLanguagesStep(), buildSearchStep(), buildPostProcessStep(),
  ];

  // Try restoring a saved draft; otherwise start fresh.
  if (!loadDraft()) {
    wizardIndex = 0;
    wizardValues = {};
    providerEnabled = {};
    langRules = [];
    langDefault = [];
    mediaRoots = [];
  }

  // Ensure embedded provider is always enabled.
  const provSection = schemaByKey('providers');
  if (provSection?.['providers']) {
    for (const p of provSection['providers']) {
      if (p['always_enabled']) providerEnabled[p['name']] = true;
    }
  }

  // Clamp index in case steps changed since draft was saved.
  if (wizardIndex >= wizardSteps.length) wizardIndex = 0;

  showPage('configWizardPage');
  renderCurrentStep();
  wireWizardNav();
}

function renderCurrentStep(): void {
  const container = $('wizardSection');
  if (!container) return;
  hideError('wizardError');
  const step = wizardSteps[wizardIndex];
  if (!step) return;

  const isEmpty = container.children.length === 0;

  if (isEmpty) {
    populateStep(container, step);
    container.classList.add('fade-in');
    return;
  }

  container.classList.add('fade-out');
  container.classList.remove('fade-in');
  setTimeout(() => {
    populateStep(container, step);
    container.classList.remove('fade-out');
    container.classList.add('fade-in');
  }, 150);
}

function populateStep(container: HTMLElement, step: WizardStep): void {
  container.replaceChildren();
  const title = el('h3', { className: 'wizard-section-title' }, step.title);
  container.appendChild(title);
  step.render(container);
  renderWizardProgress();
  updateWizardNav();
}

function renderWizardProgress(): void {
  const container = $('wizardProgress');
  if (!container) return;
  container.replaceChildren();
  for (let i = 0; i < wizardSteps.length; i++) {
    const cls = i === wizardIndex ? 'wizard-dot active'
      : i < wizardIndex ? 'wizard-dot done' : 'wizard-dot';
    container.appendChild(el('div', { className: cls }));
  }
}

function updateWizardNav(): void {
  const isFirst = wizardIndex === 0;
  const isLast = wizardIndex === wizardSteps.length - 1;
  const back = $('wizardBack');
  if (back) back.setAttribute('aria-disabled', isFirst ? 'true' : 'false');
  if ($('wizardNext')) $('wizardNext')!.hidden = isLast;
  if ($('wizardFinish')) $('wizardFinish')!.hidden = !isLast;
}

let navWired = false;
let validationAbort: AbortController | null = null;

function abortValidation(): void {
  if (validationAbort) { validationAbort.abort(); validationAbort = null; }
}

function wireWizardNav(): void {
  if (navWired) return;
  navWired = true;
  // Drain any in-flight validation on page unload so the timeout signal
  // doesn't fire into a torn-down DOM.
  registerCleanup(() => { abortValidation(); });
  $('wizardBack')?.addEventListener('click', () => {
    if ($('wizardBack')?.getAttribute('aria-disabled') === 'true') return;
    abortValidation();
    wizardSteps[wizardIndex]!.collect();
    saveDraft();
    if (wizardIndex > 0) { wizardIndex--; renderCurrentStep(); }
  });
  $('wizardNext')?.addEventListener('click', async () => {
    abortValidation();
    const step = wizardSteps[wizardIndex]!;
    step.collect();
    saveDraft();
    const err = step.validate();
    if (err) { showError('wizardError', err); return; }
    if (step.validateAsync) {
      validationAbort = new AbortController();
      const asyncErr = await step.validateAsync(validationAbort.signal);
      if (validationAbort?.signal.aborted) return;
      validationAbort = null;
      if (asyncErr) { showError('wizardError', asyncErr); return; }
    }
    hideError('wizardError');
    if (wizardIndex < wizardSteps.length - 1) { wizardIndex++; renderCurrentStep(); }
  });
  $('wizardFinish')?.addEventListener('click', finishWizard);
}

// --- Finish wizard ---

async function finishWizard(): Promise<void> {
  abortValidation();
  const step = wizardSteps[wizardIndex];
  if (step) {
    step.collect();
    const err = step.validate();
    if (err) { showError('wizardError', err); return; }
    if (step.validateAsync) {
      validationAbort = new AbortController();
      const asyncErr = await step.validateAsync(validationAbort.signal);
      if (validationAbort?.signal.aborted) return;
      validationAbort = null;
      if (asyncErr) { showError('wizardError', asyncErr); return; }
    }
  }
  hideError('wizardError');

  const lines: string[] = [];

  // Sonarr.
  const sv = wizardValues['sonarr'];
  if (sv && hasFilled(sv)) { lines.push('sonarr:'); lines.push('  enabled: true'); appendYAML(lines, sv); }

  // Radarr.
  const rv = wizardValues['radarr'];
  if (rv && hasFilled(rv)) { lines.push('radarr:'); lines.push('  enabled: true'); appendYAML(lines, rv); }

  // Media roots (discard empty entries).
  const validRoots = mediaRoots.filter((p: string) => p.trim() !== '');
  if (validRoots.length > 0) {
    lines.push('media_roots:');
    for (const root of validRoots) lines.push('  - "' + esc(root.trim()) + '"');
  }

  // Search settings.
  const searchVals = wizardValues['search'];
  if (searchVals && hasFilled(searchVals)) {
    lines.push('search:');
    appendYAML(lines, searchVals);
  }

  // Adaptive backoff.
  const adaptiveVals = wizardValues['adaptive'];
  if (adaptiveVals && Object.keys(adaptiveVals).length > 0) {
    lines.push('adaptive:');
    appendYAML(lines, adaptiveVals);
  }

  // Languages (discard incomplete rows).
  const vd = langDefault.filter((d: { code: string }) => d.code.trim() !== '');
  const vr = langRules.filter((r: { audio: string; code: string }) =>
    r.audio.trim() !== '' && r.code.trim() !== '');
  if (vd.length > 0 || vr.length > 0) {
    lines.push('languages:');
    if (vr.length > 0) {
      lines.push('  rules:');
      for (const rule of vr) {
        lines.push('    - audio: "' + esc(rule.audio.trim()) + '"');
        lines.push('      subtitles:');
        lines.push('        - code: "' + esc(rule.code.trim()) + '"');
        if (rule.variant && rule.variant !== DEFAULT_VARIANT) lines.push('          variant: "' + esc(rule.variant) + '"');
      }
    }
    if (vd.length > 0) {
      lines.push('  default:');
      for (const d of vd) {
        lines.push('    - code: "' + esc(d.code.trim()) + '"');
        if (d.variant && d.variant !== DEFAULT_VARIANT) lines.push('      variant: "' + esc(d.variant) + '"');
      }
    }
  }

  // Providers.
  const provSection = schemaByKey('providers');
  if (provSection?.['providers']) {
    const enabled = provSection['providers'].filter((p: ProviderSchema) => providerEnabled[p['name']] === true);
    if (enabled.length > 0) {
      lines.push('providers:');
      for (const prov of enabled) {
        lines.push('  ' + prov['name'] + ':');
        lines.push('    enabled: true');
        const pv = wizardValues['prov_' + prov['name']];
        if (pv) {
          const nonBool: string[] = [];
          const boolVals: string[] = [];
          for (const [k, v] of Object.entries(pv)) {
            if (v.trim() === '') continue;
            if (v === 'true' || v === 'false') boolVals.push('      ' + k + ': ' + v);
            else nonBool.push('      ' + k + ': ' + yv(v));
          }
          if (nonBool.length > 0 || boolVals.length > 0) {
            lines.push('    settings:');
            for (const l of nonBool) lines.push(l);
            for (const l of boolVals) lines.push(l);
          }
        }
      }
    }
  }

  // Post-processing.
  const ppv = wizardValues['post_processing'];
  if (ppv && Object.keys(ppv).length > 0) { lines.push('post_processing:'); appendYAML(lines, ppv); }

  if (lines.length === 0) { clearDraft(); window.location.href = '/'; return; }

  hideError('wizardError');
  const ok = await saveWizardConfigAction.dispatch(lines.join('\n') + '\n', {
    onError: (err) => {
      showError('wizardError', 'Save failed: ' + err.message);
    },
  });
  if (ok !== null) {
    clearDraft();
    window.location.href = '/';
  }
}

/** Save the wizard's assembled YAML config. text/yaml body needs a custom
 *  run() (apiAction assumes JSON), so we use defineAction here. retryNetwork
 *  + RETRY_STANDARD recover from transient blips during the most important
 *  wizard step. dedupe protects against rapid Finish clicks. error: false
 *  because the wizard surfaces failures inline via showError, not toast. */
const saveWizardConfigAction = defineAction<string, unknown>({
  name: "wizard.save_config",
  dedupe: true,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  run: async (yamlBody, signal) => {
    let res: Response;
    try {
      res = await fetch('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'text/yaml' },
        body: yamlBody,
        signal: AbortSignal.any([signal, AbortSignal.timeout(YAML_TIMEOUT_MS)]),
      });
    } catch (e) {
      throw classifyFetchError(e, signal);
    }
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new ActionError(text || `HTTP ${String(res.status)}`, { status: res.status });
    }
    return undefined;
  },
  error: false,
});

function hasFilled(vals: Record<string, string>): boolean {
  return Object.values(vals).some((v: string) => v.trim() !== '');
}

function esc(s: string): string {
  return s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n');
}

function yv(val: string): string {
  if (val === 'true' || val === 'false') return val;
  if (/^\d+$/.test(val)) return val;
  return '"' + esc(val) + '"';
}

function appendYAML(lines: string[], vals: Record<string, string>): void {
  for (const [key, val] of Object.entries(vals)) {
    if (val.trim() === '') continue;
    lines.push('  ' + key + ': ' + yv(val));
  }
}
