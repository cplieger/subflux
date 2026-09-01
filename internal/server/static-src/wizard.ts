// wizard.ts — The single first-boot flow (admin creation hands off here).
// It boots from the FULL structured config (+ secret presence flags) and
// config_valid, prefills every step, collapses steps the config already
// answers, walks the rest, and finishes with a GET-overlay-PUT of the FULL
// section map followed by the passkey offer and one navigation into the
// app. Decision logic lives in wizard-state.ts; this module owns DOM and flow.

import { patch } from "@cplieger/reactive";
import {
  configSchema,
  configStructured,
  webauthnRegisterBegin,
  webauthnSignalData,
  PATH_SAVE_CONFIG_STRUCTURED,
  PATH_WEBAUTHN_REGISTER_FINISH,
} from "./wire/client.gen.js";
import { apiAction, retryNetwork, RETRY_STANDARD, registerCleanup } from "@cplieger/actions";
import { LANGUAGES } from "./languages.js";
import { $, showPage, showError, hideError } from "./dom-core.js";
import { el, option, withHelp } from "./dom.js";
import { SUBTITLE_VARIANTS, YAML_TIMEOUT_MS, DEFAULT_VARIANT, SETUP_PATH } from "./constants.js";
import type { SchemaSection } from "./api-types.js";
import type { StructuredConfig } from "./wire/types.gen.js";
import {
  type Sections,
  type StepID,
  type WizardBoot,
  type WizardDraft,
  type WizardModel,
  buildDraftJSON,
  buildSaveSections,
  fastPathAvailable,
  fingerprintBoot,
  overlayDraft,
  parseDraft,
  prefillModel,
  satisfiedSteps,
} from "./wizard-state.js";
import { bufferToBase64url, creationOptionsFromJSON } from "./webauthn-utils.js";
import { buildProvidersStep } from "./wizard-providers.js";
import { buildLanguagesStep } from "./wizard-languages.js";
import {
  buildArrStep,
  buildMediaRootsStep,
  buildSearchStep,
  buildScoringStep,
  buildPostProcessStep,
} from "./wizard-steps.js";

export interface WizardStep {
  /** Matrix step id, or "review" for the closing summary screen. */
  stepId: StepID | "review";
  title: string;
  render: (container: HTMLElement) => void;
  collect: () => void;
  validate: () => string;
  validateAsync?: (signal: AbortSignal) => Promise<string>;
}

/** WizardEntry carries what the login page knows at handoff: the validity
 *  signal (prefill source is the structured GET) and — memory-only, never
 *  persisted — the password just used, for the post-activation passkey
 *  offer (register/begin requires password proof). */
export interface WizardEntry {
  configValid: boolean;
  password?: string;
}

// --- Module state ---

let fullSchema: SchemaSection[] = [];
let boot: WizardBoot = { sections: {}, secretsPresent: new Set(), configValid: false };
let bootFingerprint = "";
let allSteps: WizardStep[] = [];
let activeSteps: WizardStep[] = [];
let wizardIndex = 0;
let touched = new Set<StepID>();
let setupPassword = "";

// Step-module-facing model bindings (live ES module bindings; reassigned at
// boot from the prefilled model).
export let wizardValues: Record<string, Record<string, string>> = {};
export let providerEnabled: Record<string, boolean> = {};
export let langRules: { audio: string; code: string; variant: string }[] = [];
export let langDefault: { code: string; variant: string }[] = [];
export let mediaRoots: string[] = [];

/** Returns every module-scope binding to its initial value.
 *
 *  This module cannot be re-evaluated to get a fresh graph: Browser Mode
 *  keys its module map by URL, so `vi.resetModules()` hands back the
 *  cached instance, and a `?boot=N` specifier is closed here because
 *  wizard-steps.ts and wizard-providers.ts import the shared bindings back
 *  from "./wizard.js" — a busted specifier would mint a duplicate instance
 *  whose sibling step modules keep reading the original's, which stays empty.
 *
 *  Every module-scope `let` in this file must be listed here. `navWired`
 *  left true makes `wireWizardNav()` no-op on a freshly mounted page.
 *  `stepFadeTimer` left running renders a pending step into the next
 *  test's page. */
export function _resetForTest(): void {
  abortValidation(); // aborts and nulls validationAbort
  clearTimeout(stepFadeTimer ?? undefined);
  stepFadeTimer = null;
  fullSchema = [];
  boot = { sections: {}, secretsPresent: new Set(), configValid: false };
  bootFingerprint = "";
  allSteps = [];
  activeSteps = [];
  wizardIndex = 0;
  touched = new Set();
  setupPassword = "";
  wizardValues = {};
  providerEnabled = {};
  langRules = [];
  langDefault = [];
  mediaRoots = [];
  navWired = false;
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

export function schemaByKey(key: string): SchemaSection | undefined {
  return fullSchema.find((s: SchemaSection) => s.key === key);
}

const DRAFT_KEY = "subflux-setup-draft";

function adoptModel(m: WizardModel): void {
  wizardValues = m.wizardValues;
  providerEnabled = m.providerEnabled;
  langRules = m.langRules;
  langDefault = m.langDefault;
  mediaRoots = m.mediaRoots;
}

function currentModel(): WizardModel {
  return { wizardValues, providerEnabled, langRules, langDefault, mediaRoots };
}

/** Persists the wizard's progress. The stored model is schema-sanitized:
 *  secret fields never reach localStorage. Newly typed secrets are
 *  re-entered after a reload; saved-secret presence rides the boot
 *  presence flags instead. */
function saveDraft(): void {
  try {
    localStorage.setItem(
      DRAFT_KEY,
      buildDraftJSON(
        fullSchema,
        bootFingerprint,
        activeSteps[wizardIndex]?.stepId ?? "",
        [...touched],
        currentModel(),
      ),
    );
  } catch {
    /* localStorage full or unavailable */
  }
}

function clearDraft(): void {
  try {
    localStorage.removeItem(DRAFT_KEY);
  } catch {
    /* ignore */
  }
}

// --- Shared field builders (consumed by the step modules) ---
//
// A field's schema `help` text goes through dom.ts's withHelp, the one
// carrier for both this wizard and the settings dialog.

export function wizField(
  id: string,
  label: string,
  type: string,
  value: string,
  placeholder: string,
  tip: string | undefined,
): HTMLElement {
  const lbl = withHelp(el("label", { for: id }, label), tip);
  const inp = el("input", {
    type: type === "number" ? "number" : "text",
    id,
    placeholder,
    value,
    autocomplete: "off",
    "data-1p-ignore": "",
    "data-lpignore": "true",
    "data-bwignore": "",
    "data-form-type": "other",
    className: type === "secret" ? "wiz-masked" : "",
  });
  return el("div", { className: "wiz-field" }, lbl, inp);
}

export function wizToggle(
  id: string,
  label: string,
  checked: boolean,
  tip: string | undefined,
): HTMLElement {
  // `for` gives the checkbox its accessible name: the .wiz-toggle wrapper
  // holds only the slider span, so without it the control announces
  // unlabelled.
  const lbl = withHelp(el("label", { for: id }, label), tip);
  const cb = el("input", { type: "checkbox", id }) as HTMLInputElement;
  cb.checked = checked;
  const toggle = el("label", { className: "wiz-toggle" }, cb, el("span"));
  return el("div", { className: "wiz-field" }, lbl, toggle);
}

export function langSelect(id: string, value: string, placeholder: string): HTMLElement {
  // The placeholder doubles as the accessible name: these rows caption
  // their selects with a layout element, not a label.
  const sel = el("select", {
    id,
    className: "wiz-lang-select",
    "aria-label": placeholder,
  }) as HTMLSelectElement;
  sel.appendChild(option("", placeholder));
  for (const [code, name] of LANGUAGES) {
    sel.appendChild(option(code, name + " (" + code + ")"));
  }
  sel.value = value;
  return sel;
}

export function variantSelect(id: string, value: string): HTMLElement {
  const sel = el("select", {
    id,
    className: "wiz-lang-variant",
    "aria-label": "Subtitle variant",
  }) as HTMLSelectElement;
  for (const v of SUBTITLE_VARIANTS) {
    sel.appendChild(option(v.value, v.label));
  }
  sel.value = value || DEFAULT_VARIANT;
  return sel;
}

// --- Flow ---

export async function startConfigWizard(entry: WizardEntry): Promise<void> {
  setupPassword = entry.password ?? "";

  // Give the wizard an address before anything can fail, so a reload comes
  // back here rather than resolving to the app shell.
  if (window.location.pathname !== SETUP_PATH) {
    history.replaceState(null, "", SETUP_PATH);
  }

  // Both fetches must succeed before any wizard state initializes. A
  // failed structured fetch must never be substituted with {}: Finish PUTs
  // the full section map, so an empty baseline would delete every
  // untouched section and its secrets.
  const [schema, structured] = await Promise.all([configSchema(), configStructured()]);
  if (!schema || !structured) {
    renderWizardInitError(entry);
    return;
  }
  fullSchema = schema;

  const sections: Sections = { ...structured.sections };
  const present = new Set<string>(structured.secrets_present ?? []);
  boot = { sections, secretsPresent: present, configValid: entry.configValid };
  bootFingerprint = fingerprintBoot(sections, [...present]);

  // Fresh prefill from the server snapshot; a fingerprint-valid draft
  // overlays only its touched fields.
  let model = prefillModel(sections);
  touched = new Set();
  let draft: WizardDraft | null = null;
  try {
    draft = parseDraft(localStorage.getItem(DRAFT_KEY), bootFingerprint);
  } catch {
    draft = null;
  }
  if (draft) {
    model = overlayDraft(model, draft);
    touched = new Set(draft.touched);
  } else {
    clearDraft();
  }
  adoptModel(model);

  allSteps = [
    buildArrStep(),
    buildMediaRootsStep(),
    buildProvidersStep(),
    buildLanguagesStep(),
    buildSearchStep(),
    buildScoringStep(),
    buildPostProcessStep(),
  ];

  // Active walk: steps not auto-collapsed, plus any step the draft
  // already touched, then the review screen. A fresh visit walks everything.
  const satisfied = satisfiedSteps(boot);
  const walk = allSteps.filter((s) => {
    const id = s.stepId as StepID;
    return !satisfied.has(id) || touched.has(id);
  });
  activeSteps = [...walk, buildReviewStep()];

  wizardIndex = 0;
  if (draft) {
    const idx = activeSteps.findIndex((s) => s.stepId === draft.stepId);
    if (idx >= 0) {
      wizardIndex = idx;
    }
  } else if (fastPathAvailable(boot)) {
    // Everything mandatory is satisfied — open on the finish summary.
    wizardIndex = activeSteps.length - 1;
  }

  showPage("configWizardPage");
  renderCurrentStep();
  wireWizardNav();
}

/** Renders the retryable init-failure state: the wizard page with no
 *  step content, navigation, or Finish, plus a Retry button. No draft is
 *  read or written — nothing may overlay a boot that never happened. */
function renderWizardInitError(entry: WizardEntry): void {
  showPage("configWizardPage");
  for (const id of ["wizardBack", "wizardNext", "wizardFinish"]) {
    const b = $(id);
    if (b) {
      b.hidden = true;
    }
  }
  $("wizardProgress")?.replaceChildren();
  showError("wizardError", "Loading the current configuration failed. Nothing has been changed.");
  const container = $("wizardSection");
  if (!container) {
    return;
  }
  container.replaceChildren(
    el("h3", { className: "wizard-section-title" }, "Setup could not start"),
    el(
      "p",
      { className: "wiz-offer-reason" },
      "The configuration could not be loaded from the server, so the setup steps cannot " +
        "be shown yet. Check that the server is reachable, then retry.",
    ),
    el(
      "div",
      { className: "wiz-offer-actions" },
      el(
        "button",
        {
          type: "button",
          id: "wizardInitRetry",
          onclick: (ev: Event) => {
            const btn = ev.currentTarget as HTMLButtonElement;
            btn.disabled = true;
            btn.setAttribute("aria-busy", "true");
            void startConfigWizard(entry);
          },
        },
        "Retry",
      ),
    ),
  );
}

function renderCurrentStep(): void {
  const container = $("wizardSection");
  if (!container) {
    return;
  }
  hideError("wizardError");
  const step = activeSteps[wizardIndex];
  if (!step) {
    return;
  }

  // Persist against the step now on screen: the nav handlers also save
  // before validating, but they run before the index moves, which
  // recorded the step being LEFT. Saving here covers the first step too.
  saveDraft();

  const isEmpty = container.children.length === 0;

  if (isEmpty) {
    populateStep(container, step);
    container.classList.add("fade-in");
    return;
  }

  container.classList.add("fade-out");
  container.classList.remove("fade-in");
  clearTimeout(stepFadeTimer ?? undefined);
  stepFadeTimer = setTimeout(() => {
    stepFadeTimer = null;
    populateStep(container, step);
    container.classList.remove("fade-out");
    container.classList.add("fade-in");
  }, 150);
}

function populateStep(container: HTMLElement, step: WizardStep): void {
  container.replaceChildren();
  const title = el("h3", { className: "wizard-section-title" }, step.title);
  container.appendChild(title);
  step.render(container);
  renderWizardProgress();
  updateWizardNav();
}

/** Recomputes the accessible progress over the active walk (collapsed
 *  steps excluded). */
function renderWizardProgress(): void {
  const container = $("wizardProgress");
  if (!container) {
    return;
  }
  const step = activeSteps[wizardIndex];
  // role="img" over role="progressbar": html-validate's prefer-native-element
  // rule reads progressbar as a <progress> that should have been written
  // natively, and a native <progress> cannot be this dot strip.
  container.setAttribute(
    "aria-label",
    `Step ${String(wizardIndex + 1)} of ${String(activeSteps.length)}: ${step?.title ?? ""}`,
  );
  // Keyed by step index so a step change only flips the changed dots.
  const dots = activeSteps.map((_, i) => {
    const cls =
      i === wizardIndex ? "wizard-dot active" : i < wizardIndex ? "wizard-dot done" : "wizard-dot";
    return el("div", { className: cls, "data-col": String(i) });
  });
  patch(container, ...dots);
}

function updateWizardNav(): void {
  const isFirst = wizardIndex === 0;
  const isLast = wizardIndex === activeSteps.length - 1;
  const back = $("wizardBack");
  if (back) {
    back.hidden = false;
    back.setAttribute("aria-disabled", isFirst ? "true" : "false");
  }
  const next = $("wizardNext");
  if (next) {
    next.hidden = isLast;
  }
  const finish = $("wizardFinish");
  if (finish) {
    finish.hidden = !isLast;
  }
}

/** Records a real step visit/edit for the draft overlay and the save-time
 *  section overlay (the review screen is never "touched"). */
function markTouched(step: WizardStep): void {
  if (step.stepId !== "review") {
    touched.add(step.stepId);
  }
}

// All three are module-scope state: keep them listed in `_resetForTest` above.
let navWired = false;
let validationAbort: AbortController | null = null;
// Held so a second advance cancels the first, and so `_resetForTest` can
// cancel one that would otherwise fire into a torn-down page.
let stepFadeTimer: ReturnType<typeof setTimeout> | null = null;

function abortValidation(): void {
  if (validationAbort) {
    validationAbort.abort();
    validationAbort = null;
  }
}

/** Run an awaited wizard operation with visible busy feedback. */
async function withWizardBusy<T>(
  btn: HTMLElement,
  label: string,
  fn: () => Promise<T>,
): Promise<T> {
  const oldLabel = btn.textContent;
  const navButtons = [$("wizardBack"), $("wizardNext"), $("wizardFinish")].filter(
    (b): b is HTMLButtonElement => b instanceof HTMLButtonElement,
  );
  for (const b of navButtons) {
    b.disabled = true;
  }
  btn.setAttribute("aria-busy", "true");
  btn.textContent = label;
  try {
    return await fn();
  } finally {
    btn.textContent = oldLabel;
    btn.removeAttribute("aria-busy");
    for (const b of navButtons) {
      b.disabled = false;
    }
    updateWizardNav();
  }
}

function wireWizardNav(): void {
  if (navWired) {
    return;
  }
  navWired = true;
  registerCleanup(() => {
    abortValidation();
  });
  $("wizardBack")?.addEventListener("click", () => {
    if ($("wizardBack")?.getAttribute("aria-disabled") === "true") {
      return;
    }
    abortValidation();
    const step = activeSteps[wizardIndex];
    if (step) {
      step.collect();
      markTouched(step);
    }
    saveDraft();
    if (wizardIndex > 0) {
      wizardIndex--;
      renderCurrentStep();
    }
  });
  // eslint-disable-next-line @typescript-eslint/no-misused-promises -- event handler
  $("wizardNext")?.addEventListener("click", async () => {
    abortValidation();
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- index validated
    const step = activeSteps[wizardIndex]!;
    step.collect();
    markTouched(step);
    saveDraft();
    const err = step.validate();
    if (err) {
      showError("wizardError", err);
      return;
    }
    if (step.validateAsync) {
      const validateAsync = step.validateAsync;
      const nextBtn = $("wizardNext");
      validationAbort = new AbortController();
      const signal = validationAbort.signal;
      const validate = (): Promise<string> => validateAsync(signal);
      const asyncErr = nextBtn
        ? await withWizardBusy(nextBtn, "Checking\u2026", validate)
        : await validate();
      if (signal.aborted) {
        return;
      }
      validationAbort = null;
      if (asyncErr) {
        showError("wizardError", asyncErr);
        return;
      }
    }
    hideError("wizardError");
    if (wizardIndex < activeSteps.length - 1) {
      wizardIndex++;
      renderCurrentStep();
    }
  });
  // eslint-disable-next-line @typescript-eslint/no-misused-promises -- event handler
  $("wizardFinish")?.addEventListener("click", finishWizard);
}

// --- Review / summary step ---

function buildReviewStep(): WizardStep {
  return {
    stepId: "review",
    title: "Review & Finish",
    render(container: HTMLElement): void {
      const satisfied = satisfiedSteps(boot);
      const intro =
        fastPathAvailable(boot) && touched.size === 0
          ? "Everything looks configured. Review any step below, or finish now."
          : "Review your setup, then finish to activate it.";
      container.appendChild(el("p", { className: "wiz-review-intro" }, intro));

      const list = el("div", { className: "wiz-review-list", role: "list" });
      for (const step of allSteps) {
        const id = step.stepId as StepID;
        let status: string;
        if (touched.has(id)) {
          status = "Updated in this setup";
        } else if (satisfied.has(id)) {
          status = "Loaded from your config";
        } else {
          status = "Using defaults";
        }
        const editBtn = el(
          "button",
          {
            type: "button",
            className: "ghost wiz-review-edit",
            "aria-label": `Edit ${step.title}`,
            onclick: () => {
              editStep(id);
            },
          },
          "Edit",
        );
        list.appendChild(
          el(
            "div",
            { className: "wiz-review-row", role: "listitem" },
            el("span", { className: "wiz-review-title" }, step.title),
            el("span", { className: "wiz-review-status" }, status),
            editBtn,
          ),
        );
      }
      container.appendChild(list);
    },
    collect(): void {
      /* the review screen edits nothing */
    },
    validate(): string {
      return "";
    },
  };
}

/** Jumps to a step from the review screen, splicing collapsed steps into
 *  the active walk on demand. */
function editStep(id: StepID): void {
  let idx = activeSteps.findIndex((s) => s.stepId === id);
  if (idx < 0) {
    const step = allSteps.find((s) => s.stepId === id);
    if (!step) {
      return;
    }
    activeSteps.splice(activeSteps.length - 1, 0, step);
    idx = activeSteps.length - 2;
  }
  wizardIndex = idx;
  renderCurrentStep();
}

// --- Finish: GET-overlay-PUT the FULL map, then the passkey offer ---

async function finishWizard(): Promise<void> {
  const finishBtn = $("wizardFinish");
  if (finishBtn) {
    await withWizardBusy(finishBtn, "Saving\u2026", finishWizardInner);
  } else {
    await finishWizardInner();
  }
}

async function finishWizardInner(): Promise<void> {
  abortValidation();
  hideError("wizardError");

  // The wizard holds the full boot snapshot and overlays only touched
  // sections; untouched sections survive by round-trip. Completion is
  // idempotent: retries and a duplicate PUT re-run activation harmlessly.
  const sections = buildSaveSections(boot.sections, currentModel(), touched);
  const o = await saveWizardConfigAction.dispatch(sections).outcome;
  if (o.status === "error") {
    showError("wizardError", "Save failed: " + o.error.message);
    return;
  }
  if (o.status !== "success") {
    return;
  }
  clearDraft();
  await showPasskeyOffer();
}

/** Save the wizard's full structured section map. */
const saveWizardConfigAction = apiAction<Sections>({
  name: "wizard.save_config",
  dedupe: true,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  timeout: YAML_TIMEOUT_MS,
  request: (sections) => ({
    method: "PUT",
    path: PATH_SAVE_CONFIG_STRUCTURED,
    body: { sections } satisfies StructuredConfig,
  }),
  error: false,
});

// --- Passkey offer (after activation; skip carries the reason) ---

/** The flow's single navigation (the wizard lives on login.html). */
function navigateToApp(): void {
  window.location.href = "/";
}

async function showPasskeyOffer(): Promise<void> {
  const container = $("wizardSection");
  if (!container) {
    navigateToApp();
    return;
  }
  // The offer replaces the wizard chrome: no back/next/finish, no dots.
  for (const id of ["wizardBack", "wizardNext", "wizardFinish"]) {
    const b = $(id);
    if (b) {
      b.hidden = true;
    }
  }
  $("wizardProgress")?.replaceChildren();
  hideError("wizardError");

  container.replaceChildren();
  container.appendChild(el("h3", { className: "wizard-section-title" }, "Add a passkey?"));

  // Availability probe: signal-data 400s while WebAuthn is unconfigured.
  const signal = await webauthnSignalData();
  // Runtime feature detection; Boolean() widens away the always-defined DOM typing.
  const browserSupport = Boolean(window.PublicKeyCredential);

  const continueBtn = el(
    "button",
    { type: "button", id: "wizardOfferContinue", onclick: navigateToApp },
    "Continue to Subflux",
  );

  if (!signal || !browserSupport || !setupPassword) {
    let reason: string;
    if (!signal) {
      reason =
        "Passkeys are unavailable: no WebAuthn Relying Party ID is configured. " +
        "You can set one later under Settings \u2192 Authentication, then add a passkey from the Security dialog.";
    } else if (!browserSupport) {
      reason = "This browser does not support passkeys. You can add one later from another device.";
    } else {
      reason =
        "Passkey enrollment needs your password to confirm. You can add one any time from the Security dialog.";
    }
    container.appendChild(el("p", { className: "wiz-offer-reason" }, reason));
    container.appendChild(el("div", { className: "wiz-offer-actions" }, continueBtn));
    return;
  }

  container.appendChild(
    el(
      "p",
      { className: "wiz-offer-reason" },
      "Sign in faster next time with a passkey on this device. You can also add one later from the Security dialog.",
    ),
  );
  const addBtn = el(
    "button",
    {
      type: "button",
      id: "wizardOfferAdd",
      onclick: async (ev: Event) => {
        const btn = ev.currentTarget as HTMLButtonElement;
        btn.disabled = true;
        btn.setAttribute("aria-busy", "true");
        try {
          if (await registerOfferPasskey()) {
            navigateToApp();
          }
        } finally {
          btn.disabled = false;
          btn.removeAttribute("aria-busy");
        }
      },
    },
    "Add passkey",
  );
  const skipBtn = el(
    "button",
    { type: "button", className: "ghost", onclick: navigateToApp },
    "Skip for now",
  );
  container.appendChild(el("div", { className: "wiz-offer-actions" }, addBtn, skipBtn));
}

/** Runs the full registration ceremony with the remembered password.
 *  A failure leaves the offer on screen with the error inline. */
async function registerOfferPasskey(): Promise<boolean> {
  try {
    const begin = await webauthnRegisterBegin({ password: setupPassword });
    if (!begin?.publicKey) {
      showError("wizardError", "Failed to start passkey registration.");
      return false;
    }
    const publicKey = creationOptionsFromJSON(begin.publicKey.publicKey);
    const credential = await navigator.credentials.create({ publicKey });
    if (!credential) {
      showError("wizardError", "Passkey creation was cancelled.");
      return false;
    }
    const attestation = credential as PublicKeyCredential;
    const response = attestation.response as AuthenticatorAttestationResponse;
    // Custom header required (X-WebAuthn-Session): a documented raw-fetch
    // flow sourcing only the path constant (same as the Security dialog).
    const finishRes = await fetch(PATH_WEBAUTHN_REGISTER_FINISH, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-WebAuthn-Session": begin.session_token,
      },
      body: JSON.stringify({
        id: attestation.id,
        rawId: bufferToBase64url(attestation.rawId),
        type: attestation.type,
        response: {
          attestationObject: bufferToBase64url(response.attestationObject),
          clientDataJSON: bufferToBase64url(response.clientDataJSON),
        },
      }),
      signal: AbortSignal.timeout(10_000),
    });
    if (!finishRes.ok) {
      const data = (await finishRes.json().catch(() => ({}))) as { error?: string };
      showError("wizardError", data.error ?? "Failed to register the passkey.");
      return false;
    }
    return true;
  } catch (e: unknown) {
    if (e instanceof DOMException && e.name === "AbortError") {
      return false;
    }
    showError("wizardError", "Passkey registration failed.");
    return false;
  }
}
