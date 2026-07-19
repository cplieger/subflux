// @vitest-environment happy-dom
import { describe, it, expect, beforeEach } from "vitest";
import { markRequiredFields, buildSectionsFromForm } from "./config.js";
import { fieldId } from "./config-renderers.js";
import type { SchemaSection } from "./api-types.js";

// Two sonarr/radarr sections sharing a single required_group ("arr").
// Each has two required fields; api_key mirrors the real secret field.
const ARR_SECTIONS: SchemaSection[] = [
  {
    key: "sonarr",
    title: "Sonarr",
    type: "fields",
    required_group: "arr",
    fields: [
      { key: "url", label: "URL", type: "text", required: true },
      { key: "api_key", label: "API Key", type: "secret", required: true, secret: true },
    ],
  },
  {
    key: "radarr",
    title: "Radarr",
    type: "fields",
    required_group: "arr",
    fields: [
      { key: "url", label: "URL", type: "text", required: true },
      { key: "api_key", label: "API Key", type: "secret", required: true, secret: true },
    ],
  },
];

// Builds an empty config form (#configBody) matching ARR_SECTIONS and
// returns the four field inputs keyed by section.field.
function buildArrForm(): {
  body: HTMLElement;
  sonarrUrl: HTMLInputElement;
  sonarrKey: HTMLInputElement;
  radarrUrl: HTMLInputElement;
  radarrKey: HTMLInputElement;
} {
  const body = document.createElement("div");
  body.id = "configBody";
  document.body.appendChild(body);

  const make = (section: string, field: string): HTMLInputElement => {
    const wrap = document.createElement("div");
    wrap.className = "cfg-field";
    const inp = document.createElement("input");
    inp.type = "text";
    inp.id = fieldId(section, field);
    wrap.appendChild(inp);
    body.appendChild(wrap);
    return inp;
  };

  return {
    body,
    sonarrUrl: make("sonarr", "url"),
    sonarrKey: make("sonarr", "api_key"),
    radarrUrl: make("radarr", "url"),
    radarrKey: make("radarr", "api_key"),
  };
}

function hasError(inp: HTMLInputElement): boolean {
  return inp.closest(".cfg-field")?.querySelector(".cfg-error") != null;
}

describe("config: markRequiredFields required_group", () => {
  beforeEach(() => {
    document.body.replaceChildren();
  });

  it("flags every member when the group is unsatisfied", () => {
    const f = buildArrForm();

    markRequiredFields(ARR_SECTIONS, f.body);

    // No member is filled, so all required fields in the group are flagged.
    expect(f.sonarrUrl.classList.contains("cfg-required")).toBe(true);
    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(true);
    expect(hasError(f.sonarrUrl)).toBe(true);
    expect(hasError(f.radarrUrl)).toBe(true);
  });

  it("clears all members once one member satisfies the group", () => {
    const f = buildArrForm();
    f.sonarrUrl.value = "http://sonarr:8989";
    f.sonarrKey.value = "abc123";

    markRequiredFields(ARR_SECTIONS, f.body);

    // Group satisfied by sonarr: neither the filled member nor the empty
    // sibling carries the required styling (border or "Required" message).
    expect(f.sonarrUrl.classList.contains("cfg-required")).toBe(false);
    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(false);
    expect(f.radarrKey.classList.contains("cfg-required")).toBe(false);
    expect(hasError(f.radarrUrl)).toBe(false);
    expect(hasError(f.radarrKey)).toBe(false);
  });

  it("a satisfied required_group does not re-flag an empty member on input", () => {
    const f = buildArrForm();

    // First pass with everything empty wires the input listeners on every
    // member (the group is unsatisfied, so no member is short-circuited).
    markRequiredFields(ARR_SECTIONS, f.body);
    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(true);

    // Satisfy the group via sonarr, then type in the still-empty radarr URL.
    f.sonarrUrl.value = "http://sonarr:8989";
    f.sonarrKey.value = "abc123";
    f.radarrUrl.dispatchEvent(new Event("input", { bubbles: true }));

    // The input listener must recompute the group state and leave the empty
    // sibling unflagged — the previous behavior re-added the red border via
    // updateFieldValidation on every keystroke.
    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(false);
    expect(hasError(f.radarrUrl)).toBe(false);
    // The filled member stays clear too.
    expect(f.sonarrUrl.classList.contains("cfg-required")).toBe(false);
  });
});

// --- buildSectionsFromForm: form -> structured sections payload ---
// Ports the form->payload guarantees that the deleted YAML emitters carried:
// typed JSON values per schema section instead of YAML text.

function addInput(id: string, value: string): HTMLInputElement {
  const inp = document.createElement("input");
  inp.type = "text";
  inp.id = id;
  inp.value = value;
  document.body.appendChild(inp);
  return inp;
}

function addCheckbox(id: string, checked: boolean): HTMLInputElement {
  const inp = document.createElement("input");
  inp.type = "checkbox";
  inp.id = id;
  inp.checked = checked;
  document.body.appendChild(inp);
  return inp;
}

function addSelect(parent: HTMLElement, className: string, options: string[], value: string): void {
  const sel = document.createElement("select");
  sel.className = className;
  for (const o of options) {
    const opt = document.createElement("option");
    opt.value = o;
    opt.textContent = o;
    sel.appendChild(opt);
  }
  sel.value = value;
  parent.appendChild(sel);
}

describe("config: buildSectionsFromForm", () => {
  beforeEach(() => {
    document.body.replaceChildren();
  });

  it("maps a fields section with typed values and omits empty optional scalars", () => {
    const schema: SchemaSection[] = [
      {
        key: "search",
        title: "Search",
        type: "fields",
        fields: [
          { key: "scan_interval", label: "Scan Interval", type: "duration" },
          { key: "min_score", label: "Min Score", type: "number" },
          { key: "exclude_arr_tags", label: "Exclude Arr Tags", type: "text" },
          { key: "upgrade_enabled", label: "Upgrades", type: "bool" },
          { key: "upgrade_window_days", label: "Upgrade window", type: "number" },
        ],
      },
    ];
    addInput(fieldId("search", "scan_interval"), "24h");
    addInput(fieldId("search", "min_score"), "75");
    addInput(fieldId("search", "exclude_arr_tags"), "no-subs, , 4k ");
    addCheckbox(fieldId("search", "upgrade_enabled"), true);
    addInput(fieldId("search", "upgrade_window_days"), "");

    const sections = buildSectionsFromForm(schema);

    expect(sections["search"]).toEqual({
      scan_interval: "24h", // durations stay strings
      min_score: 75, // number inputs become numbers
      exclude_arr_tags: ["no-subs", "4k"], // comma-split string list
      upgrade_enabled: true, // checkboxes become booleans
      // upgrade_window_days omitted: empty optional scalar
    });
  });

  it("maps an enable_key section header toggle to a boolean field", () => {
    const schema: SchemaSection[] = [
      {
        key: "sonarr",
        title: "Sonarr",
        type: "fields",
        enable_key: "enabled",
        fields: [
          { key: "enabled", label: "Enabled", type: "bool" },
          { key: "url", label: "URL", type: "text" },
        ],
      },
    ];
    addCheckbox(fieldId("sonarr", "enabled"), false);
    addInput(fieldId("sonarr", "url"), "http://sonarr:8989");

    expect(buildSectionsFromForm(schema)["sonarr"]).toEqual({
      enabled: false,
      url: "http://sonarr:8989",
    });
  });

  it("keeps empty secrets as empty strings so the server-side merge kicks in", () => {
    // The focused round-trip guarantee: an untouched (redacted) secret must
    // ride back as "" — both in a fields section and in provider settings —
    // so PUT /api/config/structured merges the stored value.
    const schema: SchemaSection[] = [
      {
        key: "sonarr",
        title: "Sonarr",
        type: "fields",
        fields: [
          { key: "url", label: "URL", type: "text" },
          { key: "api_key", label: "API Key", type: "secret", secret: true },
        ],
      },
      {
        key: "providers",
        title: "Providers",
        type: "providers",
        providers: [
          {
            name: "opensubtitles",
            label: "OpenSubtitles",
            settings: [{ key: "password", label: "Password", type: "secret", secret: true }],
          },
        ],
      },
    ];
    addInput(fieldId("sonarr", "url"), "http://sonarr:8989");
    addInput(fieldId("sonarr", "api_key"), "");
    addCheckbox("cfg-prov-opensubtitles-enabled", true);
    addInput("cfg-prov-opensubtitles-priority", "");
    addInput("cfg-prov-opensubtitles-s-password", "");

    const sections = buildSectionsFromForm(schema);

    const sonarr = sections["sonarr"] as Record<string, unknown>;
    expect(sonarr["api_key"]).toBe("");
    const providers = sections["providers"] as Record<string, Record<string, unknown>>;
    expect(providers["opensubtitles"]).toEqual({
      enabled: true,
      settings: { password: "" },
    });
  });

  it("maps scoring to a weights map of ints, defaults for missing inputs", () => {
    const schema: SchemaSection[] = [
      {
        key: "scoring",
        title: "Scoring",
        type: "fields",
        fields: [
          { key: "hash", label: "Hash", type: "number", default: "40" },
          { key: "source", label: "Source", type: "number", default: "7" },
          { key: "release_group", label: "Release Group", type: "number", default: "5" },
        ],
      },
    ];
    addInput(fieldId("scoring", "hash"), "10");
    addInput(fieldId("scoring", "source"), ""); // cleared -> omitted, server default
    // release_group has no input element -> schema default is used.

    expect(buildSectionsFromForm(schema)["scoring"]).toEqual({
      weights: { hash: 10, release_group: 5 },
    });
  });

  it("maps list sections to trimmed string arrays", () => {
    const schema: SchemaSection[] = [
      { key: "media_roots", title: "Media Roots", type: "list" },
      { key: "trusted_proxies", title: "Trusted Proxies", type: "list" },
    ];
    const list = document.createElement("div");
    list.id = "media_roots-list";
    document.body.appendChild(list);
    for (const v of ["/media", "   ", " /tv "]) {
      const inp = document.createElement("input");
      inp.type = "text";
      inp.value = v;
      list.appendChild(inp);
    }
    // trusted_proxies has no container at all -> empty list.

    const sections = buildSectionsFromForm(schema);
    expect(sections["media_roots"]).toEqual(["/media", "/tv"]);
    expect(sections["trusted_proxies"]).toEqual([]);
  });

  it("maps providers with enabled/priority and YAML-like scalar inference for settings", () => {
    const schema: SchemaSection[] = [
      {
        key: "providers",
        title: "Providers",
        type: "providers",
        providers: [
          {
            name: "opensubtitles",
            label: "OpenSubtitles",
            settings: [
              { key: "username", label: "Username", type: "text" },
              { key: "use_hash", label: "Use Hash", type: "bool" },
              { key: "delay_ms", label: "Delay", type: "text" },
            ],
          },
          {
            name: "gestdown",
            label: "Gestdown",
          },
        ],
      },
    ];
    addCheckbox("cfg-prov-opensubtitles-enabled", true);
    addInput("cfg-prov-opensubtitles-priority", "5");
    addInput("cfg-prov-opensubtitles-s-username", "user1");
    addCheckbox("cfg-prov-opensubtitles-s-use_hash", true);
    // Numeric text settings stayed numbers through the old YAML round-trip
    // (flexint-style settings); the JSON payload preserves that.
    addInput("cfg-prov-opensubtitles-s-delay_ms", "500");
    addCheckbox("cfg-prov-gestdown-enabled", false);

    expect(buildSectionsFromForm(schema)["providers"]).toEqual({
      opensubtitles: {
        enabled: true,
        priority: 5,
        settings: { username: "user1", use_hash: true, delay_ms: 500 },
      },
      // Settings-less provider: enabled flag only.
      gestdown: { enabled: false },
    });
  });

  it("mirrors the languages form as rules/default JSON", () => {
    const schema: SchemaSection[] = [{ key: "languages", title: "Languages", type: "languages" }];

    // Rule 1: audio en -> fr forced with min_score + providers.
    const rules = document.createElement("div");
    rules.id = "lang-rules";
    document.body.appendChild(rules);

    const block = document.createElement("div");
    rules.appendChild(block);
    const audioRow = document.createElement("div");
    audioRow.className = "lang-row";
    addSelect(audioRow, "lang-select", ["en", "ja"], "en");
    block.appendChild(audioRow);
    const subs = document.createElement("div");
    subs.className = "lang-subs";
    block.appendChild(subs);
    const sub = document.createElement("div");
    sub.className = "lang-sub";
    subs.appendChild(sub);
    addSelect(sub, "lang-select", ["fr", "en"], "fr");
    addSelect(sub, "variant-select", ["standard", "forced", "hi"], "forced");
    const ms = document.createElement("input");
    ms.className = "lang-min-score";
    ms.value = "80";
    sub.appendChild(ms);
    const prov = document.createElement("input");
    prov.className = "lang-providers";
    prov.value = "opensubtitles, subdl";
    sub.appendChild(prov);
    const excl = document.createElement("input");
    excl.className = "lang-exclude";
    excl.value = "";
    sub.appendChild(excl);

    // Rule 2: audio ja with no subtitle targets -> subtitles: [].
    const block2 = document.createElement("div");
    rules.appendChild(block2);
    const audioRow2 = document.createElement("div");
    audioRow2.className = "lang-row";
    addSelect(audioRow2, "lang-select", ["en", "ja"], "ja");
    block2.appendChild(audioRow2);
    const subs2 = document.createElement("div");
    subs2.className = "lang-subs";
    block2.appendChild(subs2);

    // Defaults: one english target, standard variant fields absent.
    const defaults = document.createElement("div");
    defaults.id = "lang-defaults";
    document.body.appendChild(defaults);
    const def = document.createElement("div");
    def.className = "lang-sub";
    defaults.appendChild(def);
    addSelect(def, "lang-select", ["en"], "en");

    expect(buildSectionsFromForm(schema)["languages"]).toEqual({
      rules: [
        {
          audio: "en",
          subtitles: [
            {
              code: "fr",
              variant: "forced",
              min_score: 80,
              providers: ["opensubtitles", "subdl"],
            },
          ],
        },
        { audio: "ja", subtitles: [] },
      ],
      default: [{ code: "en" }],
    });
  });

  it("keeps poll_interval as a top-level scalar string and omits it when cleared", () => {
    const schema: SchemaSection[] = [
      {
        key: "poll_interval",
        title: "Polling",
        type: "fields",
        fields: [{ key: "poll_interval", label: "Poll interval", type: "duration" }],
      },
    ];
    const inp = addInput(fieldId("poll_interval", "poll_interval"), "30s");
    expect(buildSectionsFromForm(schema)["poll_interval"]).toBe("30s");

    inp.value = "";
    expect(buildSectionsFromForm(schema)).not.toHaveProperty("poll_interval");
  });
});
