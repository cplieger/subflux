// config.ts — Settings dialog, schema renderers, language builder,
// structured-config form building.

import * as store from "./store.js";
import type { ParsedConfig } from "./store.js";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { el, dialog, confirm, $ } from "./dom.js";
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
import { setCfgSections, cfgSectionEntries, cfgValue } from "./config-values.js";
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
// drag-safe backdrop dismissal (a drag-select ending on the backdrop no longer
// closes settings) and Escape through ONE canDismiss guard — unconfigured mode
// refuses dismissal (toast + stay open, wiring stays armed), replacing the
// hand-rolled click/cancel/keydown listener trio that app.ts used to carry.
// Created lazily on first open/close so importing this module in a DOM
// without #configDialog (unit tests) stays side-effect-free, like the old
// hand-rolled wiring.
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
      // /settings. Replaces the old 260ms post-closeDialog timeout and the
      // cancel-path navigate.
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
    setCfgSections(structured?.sections ?? {});
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

    // Auto-open settings when unconfigured so the user can set up — through
    // the controller, so the open path is uniform with openConfig().
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

/** confirmRPIDChange guards a WebAuthn RP ID edit (a guarded edit, not a
 *  transparent one): passkeys are scoped to their RP ID, so changing it
 *  locks out passkey-only sign-ins until users re-register. Returns false
 *  when the user backs out. Setting an RP ID for the first time needs no
 *  warning — there are no credentials to strand. */
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
  if (newRPID === oldRPID) {
    return true;
  }
  return confirm(
    "Change WebAuthn RP ID?",
    `Existing passkeys are bound to "${oldRPID}" and will STOP working after this change; ` +
      "affected users must sign in with their password and re-register their passkeys.",
    "Change RP ID",
  );
}

/** Save the form's structured sections as JSON to the structured endpoint
 *  (the server merges empty secrets, serializes canonical YAML, validates,
 *  and hot-reloads). dedupe protects against rapid Save clicks. retryNetwork
 *  covers transient blips. decodeError maps the server's JSON error envelope
 *  through configSaveError() to user-friendly text; transport failures
 *  (status 0) keep the framework's default network/timeout codes. */
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

// friendlyConfigError cleans up Go validation errors for display.
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
      frag.appendChild(renderRawSection(name, value));
    }
  }
  patch(body, frag);

  // Mark required fields only for first-time setup.
  if (isFirstSetup) {
    markRequiredFields(configSchema, body);
  }
}

// Shared validation display for required fields.
function updateFieldValidation(inp: HTMLInputElement, field: SchemaField): void {
  const empty = (inp.value || "").trim() === "" && !(field.secret && inp.placeholder === "****");
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

// markRequiredFields adds a red border to empty required fields.
// For "required_group" sections (sonarr/radarr), at least one group
// member must have its required fields filled. If neither does, both
// get red borders. Dependencies are injected so the pass is a pure
// function of (schema, DOM) and directly testable.
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
        // Secret fields with a placeholder have a redacted value on the server.
        if (f.secret && inp.placeholder === "****") {
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

      updateFieldValidation(inp, field);

      // Re-run the full marking pass when the user types so that satisfying
      // a required_group via one member clears the styling on its sibling
      // members instead of re-flagging an empty one on every keystroke. The
      // listener captures the same sections+body it was wired with; wiring is
      // guarded by data-required-wired and the pass only reads values and
      // toggles classes (no input events are dispatched, so no recursion).
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
// sections payload for PUT /api/config/structured. One entry per schema
// section, typed JSON values instead of YAML text. Schema is injected so the
// builder is a pure function of (schema, DOM) and directly testable.
export function buildSectionsFromForm(schemaSections: SchemaSection[]): Record<string, unknown> {
  const sections: Record<string, unknown> = {};
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
  return sections;
}

function genFields(schema: SchemaSection): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (schema.enable_key) {
    const t = document.getElementById(
      fieldId(schema.key, schema.enable_key),
    ) as HTMLInputElement | null;
    out[schema.enable_key] = t ? t.checked : true;
  }
  for (const field of schema.fields ?? []) {
    if (field.key === schema.enable_key) {
      continue;
    }
    const id = fieldId(schema.key, field.key);
    const f = document.getElementById(id) as HTMLInputElement | null;
    if (!f) {
      continue;
    }
    if (field.type === "bool") {
      out[field.key] = f.checked;
    } else if (field.key === "exclude_arr_tags") {
      out[field.key] = f.value
        .split(",")
        .map((s: string) => s.trim())
        .filter(Boolean);
    } else if (field.type === "secret") {
      // Always sent, "" when empty: the server merges the stored secret
      // for empty values (schema-driven).
      out[field.key] = f.value;
    } else if (f.value === "") {
      // Empty optional scalars are omitted — the old emitter's bare
      // `key:` decoded to a YAML null, which the server treats the same.
      continue;
    } else if (field.type === "number") {
      const n = Number(f.value);
      // Non-numeric text rides through as a string so the server rejects
      // it with config_invalid, like the old raw-YAML emit did.
      out[field.key] = Number.isFinite(n) ? n : f.value;
    } else {
      // text/select/duration values stay strings ("30s", "info", ...).
      out[field.key] = f.value;
    }
  }
  return out;
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
