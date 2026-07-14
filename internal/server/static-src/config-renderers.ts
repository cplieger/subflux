import { el, option, icon } from "./dom.js";
import { extractYAMLValue, extractYAMLBlock, formatDurationCfg } from "./config-yaml.js";
import { prettyLabel } from "./utils.js";
import type { SchemaField, SchemaSection } from "./api-types.js";
import type { ParsedConfig } from "./store.js";

export function fieldId(sectionKey: string, fieldKey: string): string {
  return `cfg-${sectionKey}-${fieldKey}`;
}

function resolveFieldValue(
  sectionKey: string,
  field: SchemaField,
  sections: Record<string, string>,
  pc: ParsedConfig,
  configText: string,
): string {
  // For top-level keys like poll_interval, the section IS the value line.
  if (sectionKey === "poll_interval" && field.key === "poll_interval") {
    return (
      (extractYAMLValue(sections[sectionKey] ?? "", "poll_interval") ||
        extractTopLevelValue(configText, "poll_interval") ||
        field.default) ??
      ""
    );
  }
  // For scoring, values are nested under "weights:".
  if (sectionKey === "scoring") {
    const weightsRaw = extractYAMLBlock(sections[sectionKey] ?? "", "weights");
    if (weightsRaw) {
      const v = extractYAMLValue(weightsRaw, field.key);
      if (v) {
        return v;
      }
    }
    return field.default ?? "";
  }
  // Try parsed config first (accurate after env expansion).
  const parsed = (pc as Record<string, unknown>)[sectionKey] ?? pc;
  const parsedRec = parsed as Record<string, unknown>;
  // Try camelCase and snake_case variants.
  const camel = field.key.replace(/_([a-z])/g, (_: string, c: string) => c.toUpperCase());
  const Camel = camel.charAt(0).toUpperCase() + camel.slice(1);
  const val = parsedRec[Camel] ?? parsedRec[camel] ?? parsedRec[field.key];
  if (val !== undefined && val !== null) {
    if (field.type === "duration" && typeof val === "number") {
      return formatDurationCfg(val);
    }
    if (field.type === "bool") {
      return String(!!val);
    }
    if (Array.isArray(val)) {
      return (val as unknown[]).join(", ");
    }
    if (typeof val === "object") {
      return JSON.stringify(val);
    }
    return `${val as string | number | boolean}`;
  }
  // Fall back to raw YAML extraction.
  const raw = sections[sectionKey] ?? "";
  const yamlVal = extractYAMLValue(raw, field.key);
  if (yamlVal) {
    return yamlVal;
  }
  return field.default ?? "";
}

export function renderFieldsSection(
  schema: SchemaSection,
  sections: Record<string, string>,
  pc: ParsedConfig,
  configText: string,
): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  const hasEnableKey = !!schema.enable_key;

  // Section header with optional toggle.
  if (hasEnableKey) {
    const raw = sections[schema.key] ?? "";
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- guarded by hasEnableKey
    const enableVal = extractYAMLValue(raw, schema.enable_key!);
    // Default to true for backward compat (omitted = enabled).
    const isEnabled = enableVal !== "false";
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- guarded by hasEnableKey
    const toggleId = fieldId(schema.key, schema.enable_key!);
    const toggle = cfgToggle(toggleId, isEnabled);
    const header = el(
      "div",
      {
        className: "cfg-title",
      },
      schema.title,
      toggle,
    );
    sec.appendChild(header);

    // Content container that collapses when disabled.
    const content = el("div", { className: "cfg-body" });
    if (!isEnabled) {
      content.style.display = "none";
    }
    const toggleInput = toggle.querySelector("input");
    if (toggleInput) {
      toggleInput.addEventListener("change", () => {
        content.style.display = toggleInput.checked ? "" : "none";
      });
    }
    renderFieldsInto(content, schema, sections, pc, configText);
    sec.appendChild(content);
  } else {
    sec.appendChild(el("div", { className: "cfg-title" }, schema.title));
    renderFieldsInto(sec, schema, sections, pc, configText);
  }
  return sec;
}

// wireShowWhen sets up show_when visibility toggling for fields.
function wireShowWhen(
  schema: SchemaSection,
  fieldEls: Record<string, HTMLElement>,
  container: HTMLElement,
): void {
  for (const field of schema.fields ?? []) {
    if (!field.show_when) {
      continue;
    }
    const [depKey, depVal] = field.show_when.split("=");
    if (!depKey || depVal === undefined) {
      continue;
    }
    const depId = fieldId(schema.key, depKey);
    const depInput = (container.querySelector(`#${CSS.escape(depId)}`) ??
      container.parentElement?.querySelector(`#${CSS.escape(depId)}`)) as HTMLInputElement | null;
    const target = fieldEls[field.key];
    if (!depInput || !target) {
      continue;
    }
    const update = (): void => {
      const current = depInput.type === "checkbox" ? String(depInput.checked) : depInput.value;
      target.style.display = current === depVal ? "" : "none";
    };
    update();
    depInput.addEventListener("change", update);
  }
}

// wireRequires disables fields and forces them off when their
// dependency is unmet.
function wireRequires(
  schema: SchemaSection,
  fieldEls: Record<string, HTMLElement>,
  container: HTMLElement,
): void {
  for (const field of schema.fields ?? []) {
    if (!field.requires) {
      continue;
    }
    const [depKey, depVal] = field.requires.split("=");
    if (!depKey || depVal === undefined) {
      continue;
    }
    const depId = fieldId(schema.key, depKey);
    const depInput = (container.querySelector(`#${CSS.escape(depId)}`) ??
      container.parentElement?.querySelector(`#${CSS.escape(depId)}`)) as HTMLInputElement | null;
    const targetEl = fieldEls[field.key];
    if (!depInput || !targetEl) {
      continue;
    }
    const targetInput = targetEl.querySelector("input");
    if (!targetInput) {
      continue;
    }
    const update = (): void => {
      const current = depInput.type === "checkbox" ? String(depInput.checked) : depInput.value;
      const met = current === depVal;
      targetInput.disabled = !met;
      targetEl.classList.toggle("cfg-disabled", !met);
      if (!met && targetInput.checked) {
        targetInput.checked = false;
        targetInput.dispatchEvent(new Event("change", { bubbles: true }));
      }
    };
    update();
    depInput.addEventListener("change", update);
  }
}

// renderFieldsInto renders fields into a container, handling groups
// and show_when visibility.
function renderFieldsInto(
  container: HTMLElement,
  schema: SchemaSection,
  sections: Record<string, string>,
  pc: ParsedConfig,
  configText: string,
): void {
  const fieldEls: Record<string, HTMLElement> = {};
  // Collect unique groups for sub-headers.
  const seenGroups = new Set<string>();

  for (const field of schema.fields ?? []) {
    // Skip the enable_key field; it's rendered as the header toggle.
    if (field.key === schema.enable_key) {
      continue;
    }

    // Group sub-header with toggle for bool fields that start a group.
    if (field.group && !seenGroups.has(field.group)) {
      seenGroups.add(field.group);
      if (field.type === "bool") {
        // This bool field becomes the group header toggle.
        const groupId = fieldId(schema.key, field.key);
        const value = resolveFieldValue(schema.key, field, sections, pc, configText);
        const isOn = value === "true";
        const toggle = cfgToggle(groupId, isOn);
        const groupHeader = el(
          "div",
          {
            className: "cfg-title",
          },
          field.label,
          toggle,
        );
        container.appendChild(groupHeader);

        // Group content container.
        const groupContent = el("div", {
          className: "cfg-body",
          id: `cfg-group-${field.group}`,
        });
        if (!isOn) {
          groupContent.style.display = "none";
        }
        const grpInput = toggle.querySelector("input");
        if (grpInput) {
          grpInput.addEventListener("change", () => {
            groupContent.style.display = grpInput.checked ? "" : "none";
          });
        }
        container.appendChild(groupContent);
        fieldEls[field.key] = groupHeader;
        continue;
      }
    }

    const id = fieldId(schema.key, field.key);
    const value = resolveFieldValue(schema.key, field, sections, pc, configText);
    const fieldEl = renderField(id, field, value);
    fieldEls[field.key] = fieldEl;

    // Append to group content container if in a group.
    if (field.group) {
      const groupEl = container.querySelector(`#${CSS.escape(`cfg-group-${field.group}`)}`);
      if (groupEl) {
        groupEl.appendChild(fieldEl);
        continue;
      }
    }
    container.appendChild(fieldEl);
  }

  wireShowWhen(schema, fieldEls, container);
  wireRequires(schema, fieldEls, container);
}

export function renderField(id: string, field: SchemaField, value: string): HTMLElement {
  if (field.type === "bool") {
    return cfgCheckbox(id, field.label, value === "true", field.help);
  }
  if (field.type === "secret") {
    return cfgSecretField(id, field.label, value);
  }
  if (field.type === "select" && field.options) {
    const sel = el("select", { id }) as HTMLSelectElement;
    for (const opt of field.options) {
      sel.appendChild(option(opt.value, opt.label));
    }
    sel.value = (value || field.default) ?? "";
    return cfgFieldEl(field.label, sel, field.help);
  }
  const inputType = field.type === "number" ? "number" : "text";
  return cfgField(
    id,
    field.label,
    inputType,
    value,
    field.placeholder ?? field.default ?? "",
    field.help,
  );
}

export function renderListSection(
  schema: SchemaSection,
  sections: Record<string, string>,
): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  sec.appendChild(el("div", { className: "cfg-title" }, schema.title));

  const raw = sections[schema.key] ?? "";
  const items: string[] = [];
  for (const line of raw.split("\n")) {
    const trimmed = line.trim();
    if (trimmed.startsWith("- ")) {
      items.push(
        trimmed
          .substring(2)
          .trim()
          .replace(/^["']|["']$/g, ""),
      );
    }
  }

  const container = el("div", { id: `${schema.key}-list` });

  function addItem(value: string): void {
    const row = el("div", { className: "cfg-list" });
    const fieldSchema: SchemaField | undefined = schema.fields?.[0];
    const inp = el("input", {
      type: "text",
      value: value || "",
      placeholder: fieldSchema?.placeholder ?? "",
    });
    const removeBtn = el(
      "button",
      {
        type: "button",
        className: "close-btn ghost",
        "aria-label": "Remove item",
        onclick: () => {
          row.remove();
        },
      },
      icon("close"),
    );
    row.appendChild(inp);
    row.appendChild(removeBtn);
    container.appendChild(row);
  }

  for (const item of items) {
    addItem(item);
  }

  const addBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      onclick: () => {
        addItem("");
      },
    },
    "+ Add",
  );

  sec.appendChild(container);
  sec.appendChild(addBtn);

  // Hidden textarea for round-trip.
  const ta = el(
    "textarea",
    {
      id: `section-${schema.key}`,
      className: "cfg-textarea",
      hidden: true,
    },
    raw,
  );
  sec.appendChild(ta);
  return sec;
}

export function renderRawSection(name: string, raw: string): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  sec.appendChild(el("div", { className: "cfg-title" }, prettyLabel(name)));
  const ta = el(
    "textarea",
    {
      id: `section-${name}`,
      className: "cfg-textarea",
      spellcheck: "false",
      rows: "6",
    },
    raw,
  );
  sec.appendChild(ta);
  return sec;
}

function extractTopLevelValue(configText: string, key: string): string {
  for (const line of configText.split("\n")) {
    const trimmed = line.trim();
    if (trimmed.startsWith(`${key}:`) && !line.startsWith(" ")) {
      let val = trimmed.substring(key.length + 1).trim();
      // Strip inline comments.
      const commentIdx = val.indexOf(" #");
      if (commentIdx >= 0) {
        val = val.substring(0, commentIdx).trim();
      }
      return val.replace(/^["']|["']$/g, "");
    }
  }
  return "";
}

// --- Config form helpers ---

export function cfgField(
  id: string,
  label: string,
  type: string,
  value: string,
  placeholder: string,
  tip: string | undefined,
): HTMLElement {
  const lbl = el("label", { for: id }, label);
  if (tip) {
    lbl.setAttribute("data-tip", tip);
  }
  return el(
    "div",
    { className: "cfg-field" },
    lbl,
    el("input", {
      id,
      type,
      value: value || "",
      placeholder: placeholder || "",
      autocomplete: "off",
      "data-1p-ignore": "",
      "data-lpignore": "true",
    }),
  );
}

function cfgFieldEl(label: string, element: HTMLElement, tip: string | undefined): HTMLElement {
  const lbl = el("label", null, label);
  if (tip) {
    lbl.setAttribute("data-tip", tip);
  }
  return el("div", { className: "cfg-field" }, lbl, element);
}

function cfgSecretField(id: string, label: string, value: string): HTMLElement {
  const isRedacted = /^\*+$/.test(value) || value === "[REDACTED]";
  const container = el("div", { className: "cfg-secret" });
  // Use type=text with CSS masking instead of type=password to prevent
  // password managers from autofilling these fields.
  const inp = el("input", {
    id,
    type: "text",
    className: "cfg-masked",
    value: isRedacted ? "" : value || "",
    placeholder: isRedacted ? "****" : "",
    autocomplete: "off",
    "data-1p-ignore": "",
    "data-lpignore": "true",
    "data-form-type": "other",
  });
  const toggle = el(
    "button",
    {
      type: "button",
      className: "cfg-reveal",
      "aria-label": "Toggle visibility",
      onclick(this: HTMLElement): void {
        const masked = inp.classList.toggle("cfg-masked");
        this.replaceChildren(icon(masked ? "eye-off" : "eye"));
      },
    },
    icon("eye-off"),
  );
  container.appendChild(inp);
  container.appendChild(toggle);
  return el("div", { className: "cfg-field" }, el("label", { for: id }, label), container);
}

function cfgCheckbox(
  id: string,
  label: string,
  checked: boolean,
  tip: string | undefined,
): HTMLElement {
  const toggle = cfgToggle(id, checked);
  const lbl = el("label", { for: id }, label);
  if (tip) {
    lbl.setAttribute("data-tip", tip);
  }
  return el("div", { className: "cfg-field" }, lbl, toggle);
}

export function cfgToggle(id: string, checked: boolean): HTMLElement {
  const inp = el("input", { id, type: "checkbox" }) as HTMLInputElement;
  if (checked) {
    inp.checked = true;
  }
  const slider = el("span", {});
  return el("label", { className: "toggle" }, inp, slider);
}
