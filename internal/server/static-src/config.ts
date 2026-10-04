// config.ts — Settings dialog, schema renderers, language builder,
// structured-config form building.

import * as store from "./store.js";
import type { ParsedConfig } from "./store.js";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { el, dialog, confirm, $ } from "./dom.js";
import { FOCUS_KEY, trackFocus, captureReaderState } from "./focus-restore.js";
import { createDialog, type DialogController } from "@cplieger/ui-primitives/dialog";
import { patch } from "@cplieger/reactive";
import {
  configParsed,
  configStructured,
  configSchema as fetchConfigSchema,
  PATH_RESET_CONFIG,
  PATH_SAVE_CONFIG_STRUCTURED,
} from "./wire/client.gen.js";
import type { StructuredConfig } from "./wire/types.gen.js";
import { apiAction, bindLoadingState, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import { hasCode, ErrorCode } from "./error_codes.js";
import { pollStatus } from "./status.js";
import { YAML_TIMEOUT_MS } from "./constants.js";
import {
  setCfgSections,
  cfgSectionEntries,
  cfgUnrendered,
  cfgValue,
  cfgSecretPresent,
} from "./config-values.js";
import { configBanner, configBannerHost } from "./config-banner.js";
import type { SchemaField, SchemaSection } from "./api-types.js";
import { buildLanguagesSection, serializeLanguagesFromForm } from "./config-languages.js";
import { renderProvidersSection, genProviders } from "./config-providers.js";
import {
  fieldId,
  renderFieldsSection,
  renderListSection,
  renderRawSection,
} from "./config-renderers.js";

let configSchema: SchemaSection[] | null = null;
const cfgDlg: HTMLDialogElement = dialog("configDialog");

// --- Config drawer ---

// Dismissal (backdrop + Escape) is the dialog primitive's: createDialog wires
// drag-safe backdrop dismissal and Escape through ONE canDismiss guard —
// unconfigured mode refuses dismissal (toast + stay open, wiring stays
// armed). Created lazily on first open/close so importing this module in a
// DOM without #configDialog (unit tests) stays side-effect-free.
let cfgCtlCache: DialogController | null = null;
function cfgCtl(): DialogController {
  cfgCtlCache ??= createDialog(cfgDlg, {
    canDismiss: (): boolean => {
      if (store.get("isUnconfigured")) {
        notify.error("Save a valid configuration before closing settings");
        return false;
      }
      return true;
    },
    onClose: (): void => {
      // The close (backdrop, Escape, the X button, or programmatic) has
      // finished its fade — restore the URL if we're still parked on
      // /settings.
      if (location.pathname === "/settings") {
        history.replaceState(null, "", "/");
      }
    },
  });
  return cfgCtlCache;
}

export function openConfig(skipPush?: boolean): void {
  if (!skipPush) {
    history.pushState(null, "", "/settings");
  }
  void loadConfig();
  cfgCtl().open();
}

export function closeConfig(): void {
  // Block closing when unconfigured; user must save a valid config first.
  // (Programmatic close bypasses the canDismiss guard, so it lives here too.)
  if (store.get("isUnconfigured")) {
    notify.error("Save a valid configuration before closing settings");
    return;
  }
  cfgCtl().close();
}

async function loadConfig(): Promise<void> {
  try {
    const cfgSignal = AbortSignal.timeout(YAML_TIMEOUT_MS);
    const [structured, parsed, schema] = await Promise.all([
      configStructured({ signal: cfgSignal }),
      configParsed({ signal: cfgSignal }),
      fetchConfigSchema({ signal: cfgSignal }),
    ]);
    // Don't throw on structured-config failure; render with schema defaults
    // so the user can fix a broken or unreadable config via the UI.
    setCfgSections(structured?.sections ?? {}, structured?.secrets_present ?? []);
    if (parsed) {
      store.batch(() => {
        store.set("config", parsed);
        store.set("ignoredCodecs", new Set(parsed.ignored_codecs ?? []));
      });
    }
    if (schema) {
      configSchema = schema;
    }
    renderConfigForm();

    // Auto-open settings when unconfigured, through the controller so the
    // open path is uniform with openConfig().
    if (store.get("isUnconfigured") && !cfgDlg.open) {
      cfgCtl().open();
    }
    $.configClose.style.display = store.get("isUnconfigured") ? "none" : "";
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    notify.error(`Failed to load config: ${msg}`);
  }
}

// Save can legitimately take seconds (the server pings changed arrs before
// applying): disable + aria-busy the button for the in-flight window so the
// wait is visible and double-clicks have nothing to land on. Static button,
// so bind once at module init.
{
  const saveBtn = document.getElementById("saveConfigBtn");
  if (saveBtn instanceof HTMLButtonElement) {
    bindLoadingState("config.save", saveBtn);
  }
}

export async function saveConfig(): Promise<void> {
  const sections = buildSectionsFromForm(configSchema ?? []);
  if (!(await confirmRPIDChange(sections))) {
    return;
  }
  const wasUnconfigured = store.get("isUnconfigured");
  const o = await saveConfigAction.dispatch(sections).outcome;
  if (o.status !== "success") {
    return;
  }
  void initLanguages();
  emit(BusEvent.DataInvalidate);
  await loadConfig();
  if (wasUnconfigured && !store.get("isUnconfigured")) {
    notify.success("Subflux is now configured and running");
    void pollStatus();
  }
  closeConfig();
}

/** confirmRPIDChange guards a WebAuthn RP ID edit: passkeys are scoped to
 *  their RP ID, so changing it locks out passkey-only sign-ins until users
 *  re-register. Returns false when the user backs out. Setting an RP ID
 *  for the first time needs no warning — there are no credentials to
 *  strand. Clearing it is not a change either: a save carrying no value
 *  keeps the stored one, so there is nothing to warn about. */
async function confirmRPIDChange(sections: Record<string, unknown>): Promise<boolean> {
  const oldRPID = cfgValue("auth", "webauthn_rp_id").trim();
  if (oldRPID === "") {
    return true;
  }
  const auth = sections["auth"];
  const raw =
    typeof auth === "object" && auth !== null && !Array.isArray(auth)
      ? (auth as Record<string, unknown>)["webauthn_rp_id"]
      : undefined;
  const newRPID = typeof raw === "string" ? raw.trim() : "";
  if (newRPID === "" || newRPID === oldRPID) {
    return true;
  }
  return confirm(
    "Change WebAuthn RP ID?",
    `Existing passkeys are bound to "${oldRPID}" and will STOP working after this change. ` +
      "Affected users must sign in with their password and re-register their passkeys.",
    "Change RP ID",
  );
}

/** Save the form's structured sections as JSON to the structured endpoint
 *  (the server merges empty secrets, serializes canonical YAML, validates,
 *  and hot-reloads). dedupe protects against rapid Save clicks; retryNetwork
 *  covers transient blips. decodeError maps the server's JSON error
 *  envelope through configSaveError() to user-friendly text. */
const saveConfigAction = apiAction<Record<string, unknown>>({
  name: "config.save",
  dedupe: true,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  timeout: YAML_TIMEOUT_MS,
  request: (sections) => ({
    method: "PUT",
    path: PATH_SAVE_CONFIG_STRUCTURED,
    body: { sections } satisfies StructuredConfig,
  }),
  decodeError: (info) => {
    const body = (info.body ?? {}) as { error?: string; code?: string };
    const errArg: { error?: string; code?: string } = {};
    if (body.error !== undefined) {
      errArg.error = body.error;
    }
    if (body.code !== undefined) {
      errArg.code = body.code;
    }
    return {
      kind: "error",
      error: {
        message: configSaveError(errArg),
        status: info.status,
        ...(body.code !== undefined && { code: body.code }),
      },
    };
  },
  success: "Configuration saved",
  // The error message produced by decodeError is already user-friendly (via
  // configSaveError); pass it through verbatim instead of prepending the
  // action's name.
  error: (_args, err) => err.message,
});

// friendlyConfigError cleans up YAML validation errors for display.
const YAML_ERROR_PATTERNS: readonly { match: string; message: string }[] = [
  { match: "mapping values are not allowed", message: "check indentation and colons" },
  { match: "did not find expected key", message: "unexpected value, check indentation" },
  { match: "could not find expected", message: "missing closing quote or bracket" },
  {
    match: "found character that cannot start",
    message: "invalid character, try quoting the value",
  },
];

function friendlyConfigError(raw: string): string {
  let msg = raw.replace(/^invalid configuration:\s*/i, "");
  msg = msg.replace(/^parse YAML:\s*/i, "");
  const yamlLine = /^yaml:\s*line\s*(\d+)/i.exec(msg);
  if (yamlLine) {
    const line = yamlLine[1] ?? "";
    const rule = YAML_ERROR_PATTERNS.find((r) => msg.includes(r.match));
    if (rule) {
      return `Syntax error on line ${line}: ${rule.message}`;
    }
    return `Syntax error on line ${line}`;
  }
  if (msg.length > 0) {
    msg = msg.charAt(0).toUpperCase() + msg.slice(1);
  }
  return msg;
}

/** Data-driven error-code-to-message mapping for config save errors. */
const CONFIG_SAVE_ERRORS: readonly { code: ErrorCode; msg: string }[] = [
  {
    code: ErrorCode.ConfigUnreachableArr,
    msg: "Sonarr/Radarr unreachable. Check the URL and API key.",
  },
  {
    code: ErrorCode.ConfigTooLarge,
    msg: "Configuration is too large. Remove unused entries and try again.",
  },
  {
    code: ErrorCode.ConfigReloadFailed,
    msg: "Configuration could not be applied and was not saved. Check server logs for details.",
  },
];

function configSaveError(err: { error?: string; code?: string }): string {
  const matched = CONFIG_SAVE_ERRORS.find((e) => hasCode(err, e.code));
  if (matched) {
    return matched.msg;
  }
  return friendlyConfigError(err.error ?? "Unknown error");
}

/** Reset config to defaults. Action def with optimistic+rollback would
 *  be premature here (the reset is destructive and its outcome reshapes
 *  the entire form); keep it as a non-optimistic action with toast. */
const resetConfigAction = apiAction<undefined>({
  name: "config.reset",
  request: () => ({ method: "POST", path: PATH_RESET_CONFIG }),
  dedupe: true, // double-click protection
  success: "Config reset to defaults",
  error: "Reset failed",
});

async function resetConfig(): Promise<void> {
  const ok = await resetConfigAction.dispatch(undefined);
  if (ok !== null) {
    await loadConfig();
  }
}

function renderConfigForm(): void {
  const body = document.getElementById("configBody");
  if (!body) {
    return;
  }
  if (!configSchema) {
    patch(body, el("p", null, "Loading schema\u2026"));
    return;
  }
  const pc: ParsedConfig | null = store.get("config");
  const isUnconfigured = store.get("isUnconfigured");
  // Distinguish truly empty config (first-time setup) from a config
  // that exists but failed validation (badly configured).
  const hasConfig = cfgSectionEntries().length > 0;
  const isFirstSetup = isUnconfigured && !hasConfig;
  const frag = document.createDocumentFragment();
  const rendered = new Set<string>();

  // The form's one error slot: config-banner.ts's permanent live region, whose
  // message child carries .cfg-banner. Every error on this surface reports at
  // the top of the form, so the credential-check controls scattered through the
  // sections have somewhere to put a failure without inventing a second visual
  // language; .cfg-banner is already err-tinted with an err border, which is
  // what the notices below borrow it for. The host is module-held and re-seated
  // into every render, so a verdict from the previous open is cleared first.
  configBanner.hide();
  frag.appendChild(configBannerHost);

  // Show setup banner only for first-time setup (no config file).
  if (isFirstSetup) {
    const bannerText = el(
      "span",
      null,
      "Configure at least one of Sonarr or Radarr, " +
        "one language rule, and one subtitle provider to get started.",
    );
    const resetBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: resetConfig,
      },
      "Reset to defaults",
    );
    const banner = el("div", { className: "cfg-banner" }, bannerText, resetBtn);
    frag.appendChild(banner);
  } else if (isUnconfigured && hasConfig) {
    // Config exists but failed validation; show a warning banner.
    const banner = el(
      "div",
      { className: "cfg-banner" },
      "Configuration has errors. Fix the issues below and save, " + "or reset to defaults.",
    );
    frag.appendChild(banner);
  }

  for (const schema of configSchema) {
    rendered.add(schema.key);
    if (schema.type === "providers") {
      frag.appendChild(renderProvidersSection(schema));
    } else if (schema.type === "languages") {
      frag.appendChild(buildLanguagesSection());
    } else if (schema.type === "list") {
      frag.appendChild(renderListSection(schema));
    } else {
      frag.appendChild(renderFieldsSection(schema, pc));
    }
  }

  for (const [name, value] of cfgSectionEntries()) {
    if (!rendered.has(name)) {
      frag.appendChild(rawSection(name, value));
    }
  }
  // Must not be `patch`: patch REUSES the nodes already in this parent and copies
  // the fresh tree's on* handlers onto them, so every Add, Remove and reveal
  // handler would run against nodes that were never inserted — silently, since
  // the click still fires and the write lands on a detached subtree.
  trackReaderFocus(body);
  keyFocusables(frag);
  const restoreReader = captureReaderState(body, { scrollEl: body });
  body.replaceChildren(frag);
  restoreReader();

  // Mark required fields only for first-time setup.
  if (isFirstSetup) {
    markRequiredFields(configSchema, body);
  }
}

const RAW_SECTION_NOTICE =
  "Subflux does not recognise this section, so the settings form cannot edit it. " +
  "A save from this dialog keeps it exactly as it is on disk. " +
  "Edit it in the config file.";

/** buildSectionsFromForm round-trips the STORED value for this section, so a save
 *  keeps it; nothing here parses what a reader would type, so the control is
 *  read-only rather than an edit the save would accept and then discard. */
function rawSection(name: string, value: unknown): HTMLElement {
  const sec = renderRawSection(name, value);
  const ta = sec.querySelector("textarea");
  if (ta === null) {
    return sec;
  }
  ta.readOnly = true;
  sec.insertBefore(el("p", { className: "muted" }, RAW_SECTION_NOTICE), ta);
  return sec;
}

let readerFocusTracked = false;

/** The form can re-render while `confirm`'s dialog is still closing, so the
 *  control the reader left is tracked rather than read at swap time. */
function trackReaderFocus(body: HTMLElement): void {
  if (readerFocusTracked) {
    return;
  }
  trackFocus(body);
  readerFocusTracked = true;
}

/** Key every field focus-restore must find again after the swap. Both trees go
 *  through this one function or the restore lands nowhere: an id where the schema
 *  gives one, else the nearest id-bearing ancestor plus the child-index path,
 *  because a bare position does not survive the setup banner coming or going. */
function keyFocusables(root: ParentNode): void {
  for (const ctl of root.querySelectorAll<HTMLElement>("input, select, textarea")) {
    const key = focusKey(ctl);
    if (key !== null) {
      ctl.setAttribute(FOCUS_KEY, key);
    }
  }
}

function focusKey(ctl: HTMLElement): string | null {
  if (ctl.id !== "") {
    return ctl.id;
  }
  const path: number[] = [];
  let node: Element = ctl;
  for (let parent = node.parentElement; parent !== null; parent = node.parentElement) {
    path.push([...parent.children].indexOf(node));
    if (parent.id !== "") {
      return `${parent.id}/${path.reverse().join("/")}`;
    }
    node = parent;
  }
  return null;
}

// Shared validation display for required fields.
function updateFieldValidation(inp: HTMLInputElement, field: SchemaField, path: string): void {
  const empty = (inp.value || "").trim() === "" && !(field.secret && cfgSecretPresent(path));
  inp.classList.toggle("cfg-required", empty);
  const fieldRow = inp.closest(".cfg-field");
  if (!fieldRow) {
    return;
  }
  const msg = fieldRow.querySelector(".cfg-error");
  if (empty && !msg) {
    fieldRow.appendChild(el("span", { className: "cfg-error" }, "Required"));
  } else if (!empty && msg) {
    msg.remove();
  }
}

// Force-clear the required styling (red border + "Required" message) on a
// field regardless of whether it is empty. Used when a required_group is
// satisfied by another member, so still-empty members must not be flagged.
function clearFieldValidation(inp: HTMLInputElement): void {
  inp.classList.remove("cfg-required");
  const msg = inp.closest(".cfg-field")?.querySelector(".cfg-error");
  if (msg) {
    msg.remove();
  }
}

// markRequiredFields adds a red border to empty required fields. For
// "required_group" sections (sonarr/radarr), at least one group member
// must have its required fields filled — if neither does, both get red
// borders. Dependencies are injected so the pass is a pure function of
// (schema, DOM) and directly testable.
export function markRequiredFields(sections: SchemaSection[], body: HTMLElement): void {
  // Collect required_group state: which groups have at least one
  // member with all required fields filled?
  const groupFilled: Record<string, boolean> = {};
  for (const schema of sections) {
    if (!schema.required_group) {
      continue;
    }
    const grp = schema.required_group;
    if (!(grp in groupFilled)) {
      groupFilled[grp] = false;
    }
    const allFilled = (schema.fields ?? [])
      .filter((f: SchemaField) => f.required)
      .every((f: SchemaField) => {
        const inp = body.querySelector<HTMLInputElement>(
          `#${CSS.escape(fieldId(schema.key, f.key))}`,
        );
        if (!inp) {
          return false;
        }
        // A stored secret renders empty: the structured GET redacts it.
        if (f.secret && cfgSecretPresent(`${schema.key}.${f.key}`)) {
          return true;
        }
        return inp.value.trim() !== "";
      });
    if (allFilled) {
      groupFilled[grp] = true;
    }
  }

  for (const schema of sections) {
    for (const field of schema.fields ?? []) {
      if (!field.required) {
        continue;
      }
      const inp = body.querySelector<HTMLInputElement>(
        `#${CSS.escape(fieldId(schema.key, field.key))}`,
      );
      if (!inp) {
        continue;
      }

      // When the group IS satisfied (by any member), force-clear the
      // required styling on this member even if it is still empty.
      if (schema.required_group && groupFilled[schema.required_group]) {
        clearFieldValidation(inp);
        continue;
      }

      updateFieldValidation(inp, field, `${schema.key}.${field.key}`);

      // Re-run the full marking pass on input so satisfying a
      // required_group via one member clears the styling on its sibling
      // members instead of re-flagging an empty one on every keystroke.
      if (!inp.dataset["requiredWired"]) {
        inp.dataset["requiredWired"] = "true";
        inp.addEventListener("input", () => {
          markRequiredFields(sections, body);
        });
      }
    }
  }
}

// --- Schema-driven section renderers (extracted to config-renderers.ts) ---

// --- Form -> structured sections ---

// buildSectionsFromForm reads the rendered form back into the structured
// sections payload for PUT /api/config/structured: one entry per schema
// section, typed JSON values instead of YAML text. Schema is injected so
// the builder is a pure function of (schema, DOM) and directly testable.
export function buildSectionsFromForm(schemaSections: SchemaSection[]): Record<string, unknown> {
  const sections: Record<string, unknown> = {};
  const known = new Set(schemaSections.map((s) => s.key));
  for (const schema of schemaSections) {
    if (schema.type === "providers") {
      sections[schema.key] = genProviders(schema);
    } else if (schema.type === "languages") {
      sections[schema.key] = serializeLanguagesFromForm();
    } else if (schema.type === "list") {
      sections[schema.key] = genList(schema);
    } else if (schema.key === "poll_interval") {
      // Top-level scalar section. An empty input is omitted (the old
      // emitter's bare `poll_interval:` decoded to the server default).
      const f = document.getElementById(
        fieldId(schema.key, "poll_interval"),
      ) as HTMLInputElement | null;
      const v = f ? f.value : "30s";
      if (v) {
        sections[schema.key] = v;
      }
    } else if (schema.key === "scoring") {
      sections[schema.key] = genScoring(schema);
    } else {
      sections[schema.key] = genFields(schema);
    }
  }
  // A section the schema does not describe is round-tripped from the server's own
  // value: assembleSections builds config.yaml from the submitted sections alone,
  // so omitting one DELETES it, and the loader's strict unknown-key check means
  // every section in that file is one the config struct accepts. Keyed on the
  // schema (renderConfigForm's own `rendered` set), not on what the loop assigned
  // — that would also resurrect a deliberately omitted empty `poll_interval`.
  for (const [name, value] of cfgSectionEntries()) {
    if (!known.has(name)) {
      sections[name] = value;
    }
  }
  return sections;
}

function genFields(schema: SchemaSection): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const rendered = new Set((schema.fields ?? []).map((f) => f.key));
  if (schema.enable_key) {
    rendered.add(schema.enable_key);
    const t = document.getElementById(
      fieldId(schema.key, schema.enable_key),
    ) as HTMLInputElement | null;
    out[schema.enable_key] = t ? t.checked : true;
  }
  for (const field of schema.fields ?? []) {
    if (field.key === schema.enable_key) {
      continue;
    }
    if (field.type === "nested") {
      const nested = genNested(schema.key, field);
      if (nested !== null) {
        out[field.key] = nested;
      }
      continue;
    }
    const value = fieldValue(fieldId(schema.key, field.key), field);
    if (value !== undefined) {
      out[field.key] = value;
    }
  }
  return { ...cfgUnrendered([schema.key], rendered), ...out };
}

/** An absent block reads to the server as config the user removed, so a block
 *  whose stored secret is its only value is still sent, or saving would drop
 *  that secret. */
function genNested(sectionKey: string, field: SchemaField): Record<string, unknown> | null {
  const leaves = field.fields ?? [];
  const out = cfgUnrendered([sectionKey, field.key], new Set(leaves.map((l) => l.key)));
  let keep = Object.keys(out).length > 0;
  for (const leaf of leaves) {
    const path = `${field.key}.${leaf.key}`;
    const value = fieldValue(fieldId(sectionKey, path), leaf);
    if (value === undefined) {
      continue;
    }
    out[leaf.key] = value;
    keep ||= value !== "" || cfgSecretPresent(`${sectionKey}.${path}`);
  }
  return keep ? out : null;
}

function fieldValue(id: string, field: SchemaField): unknown {
  const f = document.getElementById(id) as HTMLInputElement | null;
  if (!f) {
    return undefined;
  }
  if (field.type === "bool") {
    return f.checked;
  }
  if (field.key === "exclude_arr_tags") {
    return f.value
      .split(",")
      .map((s: string) => s.trim())
      .filter(Boolean);
  }
  if (field.type === "secret") {
    // Always sent, "" when empty: the server merges the stored secret
    // for empty values (schema-driven).
    return f.value;
  }
  if (f.value === "") {
    // Empty optional scalars are omitted: the server gives an absent key
    // its default, exactly as it does a YAML null.
    return undefined;
  }
  if (field.type === "number") {
    const n = Number(f.value);
    // Non-numeric text rides through as a string so the server rejects
    // it with config_invalid.
    return Number.isFinite(n) ? n : f.value;
  }
  // text/select/duration values stay strings ("30s", "info", ...).
  return f.value;
}

function genScoring(schema: SchemaSection): Record<string, unknown> {
  const weights: Record<string, unknown> = {};
  for (const field of schema.fields ?? []) {
    const f = document.getElementById(fieldId(schema.key, field.key)) as HTMLInputElement | null;
    const raw = f ? f.value : (field.default ?? "0");
    if (raw === "") {
      // Omitted weight keeps the server-side default, matching the old
      // emitter's bare `key:` null.
      continue;
    }
    const n = Number(raw);
    weights[field.key] = Number.isFinite(n) ? n : raw;
  }
  return { weights };
}

function genList(schema: SchemaSection): string[] {
  const listEl = document.getElementById(`${schema.key}-list`);
  const items: string[] = [];
  if (listEl) {
    for (const inp of Array.from(listEl.querySelectorAll<HTMLInputElement>('input[type="text"]'))) {
      const v = inp.value.trim();
      if (v) {
        items.push(v);
      }
    }
  }
  return items;
}

// --- Language/provider initialization ---

export async function initLanguages(): Promise<void> {
  const cfg = await configParsed();
  if (cfg) {
    store.batch(() => {
      store.set("config", cfg);
      store.set("ignoredCodecs", new Set(cfg.ignored_codecs ?? []));
    });
  }
  // On failure, filters will be populated from history data instead.
}
