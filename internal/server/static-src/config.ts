// config.ts — Settings dialog, schema renderers, language builder, YAML helpers.

import * as store from "./store.js";
import type { ParsedConfig } from "./store.js";
import * as notify from "./notify.js";
import { el, dialog, closeDialog, patch, $ } from "./dom.js";
import { apiGet } from "./api-client.js";
import {
  apiAction,
  defineAction,
  ActionError,
  classifyFetchError,
  retryNetwork,
  RETRY_STANDARD,
} from "./actions/index.js";
import { hasCode, ErrorCode } from "./error_codes.js";
import { pollStatus } from "./status.js";
import { YAML_TIMEOUT_MS } from "./constants.js";
import { parseYAMLSections } from "./config-yaml.js";
import type { SchemaField, ConfigSchema } from "./api-types.js";
import { buildLanguagesSection, serializeLanguagesFromForm } from "./config-languages.js";
import { renderProvidersSection, renderEmbeddedSection, genProviders } from "./config-providers.js";
import {
  fieldId,
  renderFieldsSection,
  renderListSection,
  renderRawSection,
} from "./config-renderers.js";

let config = "";
let configSchema: ConfigSchema[] | null = null;
const cfgDlg: HTMLDialogElement = dialog("configDialog");

// --- Config drawer ---

export function openConfig(skipPush?: boolean): void {
  if (!skipPush) {
    history.pushState(null, "", "/settings");
  }
  void loadConfig();
  cfgDlg.showModal();
}

export function closeConfig(): void {
  // Block closing when unconfigured; user must save a valid config first.
  if (store.get("isUnconfigured")) {
    notify.error("Save a valid configuration before closing settings");
    return;
  }
  const finish = closeDialog(cfgDlg);
  if (finish && location.pathname === "/settings") {
    setTimeout(() => {
      if (location.pathname === "/settings") {
        history.replaceState(null, "", "/");
      }
    }, 260);
  }
}

export async function loadConfig(): Promise<void> {
  try {
    // /api/config returns text/yaml on success; keep raw fetch.
    // The other two return JSON and go through apiGet.
    const cfgSignal = AbortSignal.timeout(YAML_TIMEOUT_MS);
    const [rawRes, parsed, schema] = await Promise.all([
      fetch("/api/config", { signal: cfgSignal }),
      apiGet<ParsedConfig>("/api/config/parsed", cfgSignal),
      apiGet<ConfigSchema[]>("/api/config/schema", cfgSignal),
    ]);
    // Don't throw on raw config failure; render with defaults so the
    // user can fix a broken or unreadable config via the UI.
    config = rawRes.ok ? await rawRes.text() : "";
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

    // Auto-open settings when unconfigured so the user can set up.
    if (store.get("isUnconfigured")) {
      if (!cfgDlg.open) {
        cfgDlg.showModal();
      }
    }
    $.configClose.style.display = store.get("isUnconfigured") ? "none" : "";
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    notify.error(`Failed to load config: ${msg}`);
  }
}

export async function saveConfig(): Promise<void> {
  buildConfigFromForm();
  const wasUnconfigured = store.get("isUnconfigured");
  const r = await saveConfigAction.dispatch(config);
  if (r === null) {
    return;
  }
  void initLanguages();
  store.set("needsRefresh", true);
  await loadConfig();
  if (wasUnconfigured && !store.get("isUnconfigured")) {
    notify.success("Subflux is now configured and running");
    void pollStatus();
  }
  closeConfig();
}

/** Save the config dialog's YAML body. text/yaml content type means the
 *  apiAction adapter (which assumes JSON) doesn't fit; defineAction with
 *  a custom run() does. dedupe protects against rapid Save clicks.
 *  retryNetwork covers transient blips. The error message is computed
 *  via configSaveError() to map server codes to user-friendly text. */
const saveConfigAction = defineAction<string, unknown>({
  name: "config.save",
  dedupe: true,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  run: async (yamlBody, signal) => {
    let r: Response;
    try {
      r = await fetch("/api/config", {
        method: "PUT",
        body: yamlBody,
        headers: { "Content-Type": "text/yaml" },
        signal: AbortSignal.any([signal, AbortSignal.timeout(YAML_TIMEOUT_MS)]),
      });
    } catch (e) {
      throw classifyFetchError(e, signal);
    }
    if (!r.ok) {
      const body = (await r.json().catch(() => ({}))) as { error?: string; code?: string };
      const errArg: { error?: string; code?: string } = {};
      if (body.error !== undefined) {
        errArg.error = body.error;
      }
      if (body.code !== undefined) {
        errArg.code = body.code;
      }
      const msg = configSaveError(errArg);
      const opts: { status: number; code?: string } = { status: r.status };
      if (body.code !== undefined) {
        opts.code = body.code;
      }
      throw new ActionError(msg, opts);
    }
    return undefined;
  },
  success: "Configuration saved",
  // The error message produced in run() is already user-friendly (via
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
    msg: "Configuration saved but not applied. Check server logs for details.",
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
  request: () => ({ method: "POST", path: "/api/config/reset" }),
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
  const sections = parseYAMLSections(config.split("\n"));
  const pc: ParsedConfig = store.get("config") ?? {};
  const isUnconfigured = store.get("isUnconfigured");
  // Distinguish truly empty config (first-time setup) from a config
  // that exists but failed validation (badly configured).
  const hasRawConfig = config.trim().length > 0;
  const isFirstSetup = isUnconfigured && !hasRawConfig;
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
  } else if (isUnconfigured && hasRawConfig) {
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
      frag.appendChild(renderEmbeddedSection(schema, sections));
      frag.appendChild(renderProvidersSection(schema, sections));
    } else if (schema.type === "languages") {
      frag.appendChild(buildLanguagesSection());
    } else if (schema.type === "list") {
      frag.appendChild(renderListSection(schema, sections));
    } else {
      frag.appendChild(renderFieldsSection(schema, sections, pc, config));
    }
  }

  for (const [name, content] of Object.entries(sections)) {
    if (!rendered.has(name)) {
      frag.appendChild(renderRawSection(name, content));
    }
  }
  patch(body, frag);

  // Mark required fields only for first-time setup.
  if (isFirstSetup) {
    markRequiredFields();
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

// markRequiredFields adds a red border to empty required fields.
// For "required_group" sections (sonarr/radarr), at least one group
// member must have its required fields filled. If neither does, both
// get red borders.
function markRequiredFields(): void {
  if (!configSchema) {
    return;
  }
  const body = document.getElementById("configBody");
  if (!body) {
    return;
  }

  // Collect required_group state: which groups have at least one
  // member with all required fields filled?
  const groupFilled: Record<string, boolean> = {};
  for (const schema of configSchema) {
    if (!schema.required_group) {
      continue;
    }
    const grp = schema.required_group;
    if (!(grp in groupFilled)) {
      groupFilled[grp] = false;
    }
    const allFilled = schema.fields
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

  for (const schema of configSchema) {
    for (const field of schema.fields) {
      if (!field.required) {
        continue;
      }
      const inp = body.querySelector<HTMLInputElement>(
        `#${CSS.escape(fieldId(schema.key, field.key))}`,
      );
      if (!inp) {
        continue;
      }

      // For group members, only mark if no member in the group is filled.
      if (schema.required_group) {
        if (groupFilled[schema.required_group]) {
          inp.classList.remove("cfg-required");
          continue;
        }
      }

      updateFieldValidation(inp, field);

      // Clear the red border when the user types.
      if (!inp.dataset["requiredWired"]) {
        inp.dataset["requiredWired"] = "true";
        inp.addEventListener("input", () => {
          updateFieldValidation(inp, field);
        });
      }
    }
  }
}

// --- Schema-driven section renderers (extracted to config-renderers.ts) ---

// --- YAML helpers (imported from config-yaml.ts) ---

function buildConfigFromForm(): void {
  if (!configSchema) {
    return;
  }
  const parts: string[] = [];
  for (const schema of configSchema) {
    if (schema.type === "providers") {
      parts.push(genProviders(schema));
    } else if (schema.type === "languages") {
      parts.push(serializeLanguagesFromForm());
    } else if (schema.type === "list") {
      parts.push(genList(schema));
    } else if (schema.key === "poll_interval") {
      const f = document.getElementById(
        fieldId(schema.key, "poll_interval"),
      ) as HTMLInputElement | null;
      parts.push(`poll_interval: ${f ? f.value : "30s"}`);
    } else if (schema.key === "scoring") {
      parts.push(genScoring(schema));
    } else {
      parts.push(genFields(schema));
    }
  }
  config = `${parts.join("\n\n")}\n`;
}

function genFields(schema: ConfigSchema): string {
  const lines: string[] = [`${schema.key}:`];
  if (schema.enable_key) {
    const t = document.getElementById(
      fieldId(schema.key, schema.enable_key),
    ) as HTMLInputElement | null;
    lines.push(`  ${schema.enable_key}: ${t ? String(t.checked) : "true"}`);
  }
  for (const field of schema.fields) {
    if (field.key === schema.enable_key) {
      continue;
    }
    const id = fieldId(schema.key, field.key);
    const f = document.getElementById(id) as HTMLInputElement | null;
    if (!f) {
      continue;
    }
    if (field.type === "bool") {
      lines.push(`  ${field.key}: ${String(f.checked)}`);
    } else if (field.key === "exclude_arr_tags") {
      const tags = f.value
        .split(",")
        .map((s: string) => s.trim())
        .filter(Boolean);
      if (tags.length === 0) {
        lines.push("  exclude_arr_tags: []");
      } else {
        lines.push("  exclude_arr_tags:");
        for (const tag of tags) {
          lines.push(`    - ${tag}`);
        }
      }
    } else if (field.type === "secret" && !f.value) {
      lines.push(`  ${field.key}: ""`);
    } else {
      lines.push(`  ${field.key}: ${f.value || ""}`);
    }
  }
  return lines.join("\n");
}

function genScoring(schema: ConfigSchema): string {
  const lines: string[] = ["scoring:", "  weights:"];
  for (const field of schema.fields) {
    const f = document.getElementById(fieldId(schema.key, field.key)) as HTMLInputElement | null;
    lines.push(`    ${field.key}: ${f ? f.value : (field.default ?? "0")}`);
  }
  return lines.join("\n");
}

function genList(schema: ConfigSchema): string {
  const listEl = document.getElementById(`${schema.key}-list`);
  const lines: string[] = [`${schema.key}:`];
  if (listEl) {
    for (const inp of Array.from(listEl.querySelectorAll<HTMLInputElement>('input[type="text"]'))) {
      const v = inp.value.trim();
      if (v) {
        lines.push(`  - ${v}`);
      }
    }
  }
  return lines.join("\n");
}

// --- Language/provider initialization ---

export async function initLanguages(): Promise<void> {
  const cfg = await apiGet<ParsedConfig>("/api/config/parsed");
  if (cfg) {
    store.batch(() => {
      store.set("config", cfg);
      store.set("ignoredCodecs", new Set(cfg.ignored_codecs ?? []));
    });
  }
  // On failure, filters will be populated from history data instead.
}
