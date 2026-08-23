// Unit tests for config-renderers.ts — the schema-to-form renderers behind the
// settings drawer. The formatDurationCfg table is ported from the former
// config-yaml.test.ts (the function moved here when the YAML text helpers were
// deleted).
//
// These render into a real browser, so the assertions are the contracts a
// reader of the drawer depends on and not markup snapshots: which control a
// schema field type produces, the label→control association that assistive
// tech needs, the redaction rule that keeps a stored secret out of the input,
// the textContent path that keeps a raw section from becoming an XSS sink, and
// the show_when / requires wiring, which is the only place in the drawer where
// one field's value changes another field's state.
import { describe, it, expect, beforeEach } from "vitest";
import {
  fieldId,
  formatDurationCfg,
  renderField,
  renderFieldsSection,
  renderListSection,
  renderRawSection,
  cfgField,
  cfgToggle,
} from "./config-renderers.js";
import { setCfgSections } from "./config-values.js";
import type { SchemaField, SchemaSection } from "./api-types.js";
import type { ParsedConfig } from "./wire/types.gen.js";

function field(over: Partial<SchemaField> & { key: string; type: string }): SchemaField {
  return { label: over.key, ...over };
}

/** Mount a rendered element so :checked, labels and getElementById behave as
 *  they do in the drawer. */
function mount(node: HTMLElement): HTMLElement {
  document.body.replaceChildren(node);
  return node;
}

function inputOf(host: HTMLElement): HTMLInputElement {
  const inp = host.querySelector("input");
  if (!inp) {
    throw new Error("no input rendered");
  }
  return inp;
}

beforeEach(() => {
  setCfgSections({});
  document.body.replaceChildren();
});

describe("formatDurationCfg", () => {
  const cases = [
    { name: "formats seconds", input: 30e9, expected: "30s" },
    { name: "formats minutes", input: 120e9, expected: "2m" },
    { name: "formats hours", input: 7200e9, expected: "2h" },
    { name: "formats days", input: 172800e9, expected: "2D" },
    { name: "returns empty for zero", input: 0, expected: "" },
    { name: "passes through string", input: "5m", expected: "5m" },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(formatDurationCfg(tc.input)).toBe(tc.expected);
    });
  }
});

describe("fieldId", () => {
  it("namespaces a field by its section", () => {
    expect(fieldId("sonarr", "api_key")).toBe("cfg-sonarr-api_key");
  });

  it("distinguishes the same field key in two sections", () => {
    expect(fieldId("sonarr", "url")).not.toBe(fieldId("radarr", "url"));
  });
});

describe("cfgField", () => {
  it("pairs the label with the input by id so clicking the label focuses it", () => {
    const host = mount(cfgField("cfg-x-y", "URL", "text", "http://sonarr:8989", "", undefined));

    const label = host.querySelector("label");
    expect(label?.getAttribute("for")).toBe("cfg-x-y");
    expect(inputOf(host).id).toBe("cfg-x-y");
  });

  it("renders the value and the placeholder", () => {
    const host = mount(cfgField("cfg-x-y", "URL", "text", "kept", "hint", undefined));

    expect(inputOf(host).value).toBe("kept");
    expect(inputOf(host).placeholder).toBe("hint");
  });

  it("carries the tooltip on the label, not the input", () => {
    const host = mount(cfgField("cfg-x-y", "URL", "text", "", "", "what this does"));

    expect(host.querySelector("label")?.getAttribute("data-tip")).toBe("what this does");
    expect(inputOf(host).hasAttribute("data-tip")).toBe(false);
  });

  it("opts the input out of password-manager autofill", () => {
    // These attributes are why the drawer's secrets are type=text with CSS
    // masking; losing them lets a manager overwrite a stored credential.
    const host = mount(cfgField("cfg-x-y", "URL", "text", "", "", undefined));

    expect(inputOf(host).getAttribute("autocomplete")).toBe("off");
    expect(inputOf(host).hasAttribute("data-1p-ignore")).toBe(true);
    expect(inputOf(host).hasAttribute("data-lpignore")).toBe(true);
  });
});

describe("cfgToggle", () => {
  it("renders a checkbox reflecting the checked argument", () => {
    expect(inputOf(mount(cfgToggle("cfg-a-b", true))).checked).toBe(true);
    expect(inputOf(mount(cfgToggle("cfg-a-b", false))).checked).toBe(false);
  });

  it("leaves the checked ATTRIBUTE absent either way", () => {
    // The factory sets the property, so an attribute-based assertion elsewhere
    // would read false for a checked toggle.
    expect(inputOf(mount(cfgToggle("cfg-a-b", true))).hasAttribute("checked")).toBe(false);
  });
});

describe("renderField dispatch", () => {
  it("renders a bool as a checkbox that is checked for the string 'true'", () => {
    const host = mount(renderField("cfg-s-flag", field({ key: "flag", type: "bool" }), "true"));

    expect(inputOf(host).type).toBe("checkbox");
    expect(inputOf(host).checked).toBe(true);
  });

  it("renders a bool as unchecked for any other value", () => {
    const host = mount(renderField("cfg-s-flag", field({ key: "flag", type: "bool" }), "1"));

    expect(inputOf(host).checked).toBe(false);
  });

  it("renders a select over the schema options and selects the current value", () => {
    const host = mount(
      renderField(
        "cfg-s-level",
        field({
          key: "level",
          type: "select",
          options: [
            { value: "info", label: "Info" },
            { value: "debug", label: "Debug" },
          ],
        }),
        "debug",
      ),
    );

    const sel = host.querySelector("select");
    expect([...(sel?.options ?? [])].map((o) => o.value)).toStrictEqual(["info", "debug"]);
    expect(sel?.value).toBe("debug");
  });

  it("falls back to the schema default when a select has no current value", () => {
    const host = mount(
      renderField(
        "cfg-s-level",
        field({
          key: "level",
          type: "select",
          default: "info",
          options: [
            { value: "info", label: "Info" },
            { value: "debug", label: "Debug" },
          ],
        }),
        "",
      ),
    );

    expect(host.querySelector("select")?.value).toBe("info");
  });

  it("renders a number field as a number input", () => {
    const host = mount(renderField("cfg-s-port", field({ key: "port", type: "number" }), "8989"));

    expect(inputOf(host).type).toBe("number");
    expect(inputOf(host).value).toBe("8989");
  });

  it("renders an unknown field type as a text input", () => {
    const host = mount(renderField("cfg-s-x", field({ key: "x", type: "wat" }), "v"));

    expect(inputOf(host).type).toBe("text");
  });
});

describe("renderField: secret fields", () => {
  const secret = field({ key: "api_key", type: "secret", label: "API Key" });

  it("renders a redacted value as an empty masked input with a placeholder", () => {
    // GET /api/config redacts secrets to a run of asterisks. Rendering that
    // into the input would save the asterisks back over the real credential.
    const host = mount(renderField("cfg-sonarr-api_key", secret, "*****"));

    expect(inputOf(host).value).toBe("");
    expect(inputOf(host).placeholder).toBe("****");
    expect(inputOf(host).classList.contains("cfg-masked")).toBe(true);
  });

  it("treats the [REDACTED] marker the same way", () => {
    const host = mount(renderField("cfg-sonarr-api_key", secret, "[REDACTED]"));

    expect(inputOf(host).value).toBe("");
    expect(inputOf(host).placeholder).toBe("****");
  });

  it("renders a real value when the server sent one", () => {
    const host = mount(renderField("cfg-sonarr-api_key", secret, "abc123"));

    expect(inputOf(host).value).toBe("abc123");
    expect(inputOf(host).placeholder).toBe("");
  });

  it("uses type=text so no password manager claims the field", () => {
    const host = mount(renderField("cfg-sonarr-api_key", secret, ""));

    expect(inputOf(host).type).toBe("text");
    expect(inputOf(host).getAttribute("data-form-type")).toBe("other");
  });

  it("unmasks and re-masks on the reveal button", () => {
    const host = mount(renderField("cfg-sonarr-api_key", secret, "abc123"));
    const reveal = host.querySelector<HTMLButtonElement>(".cfg-reveal");

    reveal?.click();
    expect(inputOf(host).classList.contains("cfg-masked")).toBe(false);

    reveal?.click();
    expect(inputOf(host).classList.contains("cfg-masked")).toBe(true);
  });
});

describe("renderFieldsSection", () => {
  it("renders each schema field with its namespaced id", () => {
    const schema: SchemaSection = {
      key: "sonarr",
      title: "Sonarr",
      type: "fields",
      fields: [field({ key: "url", type: "text" }), field({ key: "api_key", type: "secret" })],
    };

    const host = mount(renderFieldsSection(schema, null));

    expect(host.querySelector("#cfg-sonarr-url")).not.toBeNull();
    expect(host.querySelector("#cfg-sonarr-api_key")).not.toBeNull();
  });

  it("prefills from the parsed config, snake_case or camelCase", () => {
    const schema: SchemaSection = {
      key: "sonarr",
      title: "Sonarr",
      type: "fields",
      fields: [field({ key: "url", type: "text" }), field({ key: "api_key", type: "text" })],
    };
    const parsed = { sonarr: { url: "http://sonarr:8989", apiKey: "k" } };

    const host = mount(renderFieldsSection(schema, parsed as unknown as ParsedConfig));

    expect(host.querySelector<HTMLInputElement>("#cfg-sonarr-url")?.value).toBe(
      "http://sonarr:8989",
    );
    expect(host.querySelector<HTMLInputElement>("#cfg-sonarr-api_key")?.value).toBe("k");
  });

  it("falls back to the structured section when the parsed config lacks the key", () => {
    setCfgSections({ backup: { dir: "/config/backups" } });
    const schema: SchemaSection = {
      key: "backup",
      title: "Backup",
      type: "fields",
      fields: [field({ key: "dir", type: "text" })],
    };

    const host = mount(renderFieldsSection(schema, null));

    expect(host.querySelector<HTMLInputElement>("#cfg-backup-dir")?.value).toBe("/config/backups");
  });

  it("renders the enable_key as a header toggle, not as a field", () => {
    setCfgSections({ adaptive: { enabled: false } });
    const schema: SchemaSection = {
      key: "adaptive",
      title: "Adaptive",
      type: "fields",
      enable_key: "enabled",
      fields: [
        field({ key: "enabled", type: "bool" }),
        field({ key: "max_attempts", type: "number" }),
      ],
    };

    const host = mount(renderFieldsSection(schema, null));

    // One control for the enable key (in the title), and it is not repeated in
    // the body.
    expect(host.querySelectorAll("#cfg-adaptive-enabled")).toHaveLength(1);
    expect(host.querySelector(".cfg-title #cfg-adaptive-enabled")).not.toBeNull();
    expect(host.querySelector<HTMLInputElement>("#cfg-adaptive-enabled")?.checked).toBe(false);
  });

  it("treats an omitted enable_key value as enabled", () => {
    const schema: SchemaSection = {
      key: "adaptive",
      title: "Adaptive",
      type: "fields",
      enable_key: "enabled",
      fields: [field({ key: "enabled", type: "bool" })],
    };

    const host = mount(renderFieldsSection(schema, null));

    expect(host.querySelector<HTMLInputElement>("#cfg-adaptive-enabled")?.checked).toBe(true);
  });

  it("hides the section body when the enable toggle is switched off", () => {
    const schema: SchemaSection = {
      key: "adaptive",
      title: "Adaptive",
      type: "fields",
      enable_key: "enabled",
      fields: [
        field({ key: "enabled", type: "bool" }),
        field({ key: "max_attempts", type: "number" }),
      ],
    };
    const host = mount(renderFieldsSection(schema, null));
    const body = host.querySelector<HTMLElement>(".cfg-body");
    expect(body?.getAttribute("aria-hidden")).toBe("false");

    const toggle = host.querySelector<HTMLInputElement>("#cfg-adaptive-enabled");
    if (!toggle) {
      throw new Error("enable toggle not rendered");
    }
    toggle.checked = false;
    toggle.dispatchEvent(new Event("change", { bubbles: true }));

    expect(body?.getAttribute("aria-hidden")).toBe("true");
  });

  it("hides a show_when field until its dependency holds", () => {
    const schema: SchemaSection = {
      key: "auth",
      title: "Auth",
      type: "fields",
      fields: [
        field({
          key: "mode",
          type: "select",
          options: [
            { value: "off", label: "Off" },
            { value: "oidc", label: "OIDC" },
          ],
        }),
        field({ key: "issuer", type: "text", show_when: "mode=oidc" }),
      ],
    };

    const host = mount(renderFieldsSection(schema, null));
    const issuer = host
      .querySelector<HTMLElement>("#cfg-auth-issuer")
      ?.closest<HTMLElement>(".cfg-field");
    const mode = host.querySelector<HTMLSelectElement>("#cfg-auth-mode");
    if (!issuer || !mode) {
      throw new Error("show_when fixture did not render");
    }
    expect(issuer.style.display).toBe("none");

    mode.value = "oidc";
    mode.dispatchEvent(new Event("change", { bubbles: true }));

    expect(issuer.style.display).toBe("");
  });

  it("disables a requires field and forces it off while its dependency is unmet", () => {
    const schema: SchemaSection = {
      key: "post_processing",
      title: "Post Processing",
      type: "fields",
      fields: [
        field({ key: "sync", type: "bool" }),
        field({ key: "sync_verbose", type: "bool", requires: "sync=true" }),
      ],
    };

    const host = mount(renderFieldsSection(schema, null));
    const sync = host.querySelector<HTMLInputElement>("#cfg-post_processing-sync");
    const dependent = host.querySelector<HTMLInputElement>("#cfg-post_processing-sync_verbose");
    if (!sync || !dependent) {
      throw new Error("requires fixture did not render");
    }
    expect(dependent.disabled).toBe(true);

    sync.checked = true;
    sync.dispatchEvent(new Event("change", { bubbles: true }));
    expect(dependent.disabled).toBe(false);

    // Turning the dependency back off must also clear the dependent, or a save
    // writes an option the server rejects as inconsistent.
    dependent.checked = true;
    sync.checked = false;
    sync.dispatchEvent(new Event("change", { bubbles: true }));
    expect(dependent.disabled).toBe(true);
    expect(dependent.checked).toBe(false);
  });

  it("collects grouped fields under the group's own toggle", () => {
    const schema: SchemaSection = {
      key: "post_processing",
      title: "Post Processing",
      type: "fields",
      fields: [
        field({ key: "ocr", type: "bool", group: "ocr", label: "OCR" }),
        field({ key: "ocr_lang", type: "text", group: "ocr" }),
      ],
    };

    const host = mount(renderFieldsSection(schema, null));
    const group = host.querySelector<HTMLElement>("#cfg-group-ocr");

    expect(group?.querySelector("#cfg-post_processing-ocr_lang")).not.toBeNull();
    // The group's bool field is the header toggle, so it sits outside the region.
    expect(group?.querySelector("#cfg-post_processing-ocr")).toBeNull();
  });

  it("collapses a group's region when its header toggle is switched off", () => {
    setCfgSections({ post_processing: { ocr: true } });
    const schema: SchemaSection = {
      key: "post_processing",
      title: "Post Processing",
      type: "fields",
      fields: [
        field({ key: "ocr", type: "bool", group: "ocr", label: "OCR" }),
        field({ key: "ocr_lang", type: "text", group: "ocr" }),
      ],
    };
    const host = mount(renderFieldsSection(schema, null));
    const group = host.querySelector<HTMLElement>("#cfg-group-ocr");
    expect(group?.getAttribute("aria-hidden")).toBe("false");

    const toggle = host.querySelector<HTMLInputElement>("#cfg-post_processing-ocr");
    if (!toggle) {
      throw new Error("group toggle not rendered");
    }
    toggle.checked = false;
    toggle.dispatchEvent(new Event("change", { bubbles: true }));

    expect(group?.getAttribute("aria-hidden")).toBe("true");
  });
});

describe("renderListSection", () => {
  const schema: SchemaSection = {
    key: "media_roots",
    title: "Media Roots",
    type: "list",
    fields: [field({ key: "path", type: "text", placeholder: "/media" })],
  };

  it("renders one row per configured item", () => {
    setCfgSections({ media_roots: ["/media/tv", "/media/movies"] });

    const host = mount(renderListSection(schema));

    expect([...host.querySelectorAll<HTMLInputElement>("input")].map((i) => i.value)).toStrictEqual(
      ["/media/tv", "/media/movies"],
    );
  });

  it("renders no rows when the section is absent", () => {
    const host = mount(renderListSection(schema));

    expect(host.querySelectorAll("input")).toHaveLength(0);
  });

  it("appends an empty row carrying the schema placeholder on + Add", () => {
    const host = mount(renderListSection(schema));

    host.querySelector<HTMLButtonElement>("button.ghost")?.click();

    const inputs = [...host.querySelectorAll<HTMLInputElement>("input")];
    expect(inputs).toHaveLength(1);
    expect(inputs[0]?.value).toBe("");
    expect(inputs[0]?.placeholder).toBe("/media");
  });

  it("removes only the pressed row", () => {
    setCfgSections({ media_roots: ["/a", "/b", "/c"] });
    const host = mount(renderListSection(schema));

    host.querySelectorAll<HTMLButtonElement>('button[aria-label="Remove item"]')[1]?.click();

    expect([...host.querySelectorAll<HTMLInputElement>("input")].map((i) => i.value)).toStrictEqual(
      ["/a", "/c"],
    );
  });
});

describe("renderRawSection", () => {
  it("shows the value as pretty-printed JSON in a textarea", () => {
    const host = mount(renderRawSection("mystery", { a: 1, b: ["x"] }));

    const ta = host.querySelector("textarea");
    expect(ta?.id).toBe("section-mystery");
    expect(ta?.value).toBe(JSON.stringify({ a: 1, b: ["x"] }, null, 2));
  });

  it("titles the section with the prettified key", () => {
    const host = mount(renderRawSection("poll_interval", "30s"));

    expect(host.querySelector(".cfg-title")?.textContent).toBe("Poll Interval");
  });

  it("sets the value as text, never as markup", () => {
    // A hand-added config section is server data: rendering it as HTML would
    // make an unknown YAML key an XSS sink in the settings drawer.
    const host = mount(renderRawSection("evil", { note: "<img src=x onerror=alert(1)>" }));

    expect(host.querySelector("img")).toBeNull();
    expect(host.querySelector("textarea")?.value).toContain("<img src=x onerror=alert(1)>");
  });
});
