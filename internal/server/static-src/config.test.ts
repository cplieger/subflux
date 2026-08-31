import { describe, it, expect, beforeEach, vi } from "vitest";

// config.ts registers its save/reset apiActions at module load. Capturing the
// definitions is the only way in to decodeError -> configSaveError ->
// friendlyConfigError: they are module-private and reached in production only
// when the actions framework decodes a failed save.
//
// CRITICAL: vitest.config sets clearMocks/mockReset/restoreMocks, so the mock
// factory must be a PLAIN function — a vi.fn would lose its implementation
// before the first test and the module-load registration would vanish.
interface MockActionDef {
  name?: string;
  decodeError?: (info: { status: number; body?: unknown }) => {
    kind: string;
    error: { message: string; status: number; code?: string };
  };
}
const actionDefs = vi.hoisted(() => ({ map: new Map<string, MockActionDef>() }));
vi.mock("@cplieger/actions", () => ({
  apiAction: (def: MockActionDef) => {
    if (def.name !== undefined) {
      actionDefs.map.set(def.name, def);
    }
    return { dispatch: () => ({ outcome: Promise.resolve({ status: "success" }) }) };
  },
  defineAction: () => ({ dispatch: () => Promise.resolve(null) }),
  bindLoadingState: () => undefined,
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
  pollAction: () => () => undefined,
}));

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

  it("ignores an optional field when deciding whether a member is filled", () => {
    // Only `required: true` fields gate the group. Counting an optional field
    // would leave both arrs flagged red for a blank tag filter.
    const sections: SchemaSection[] = [
      {
        key: "sonarr",
        title: "Sonarr",
        type: "fields",
        required_group: "arr",
        fields: [
          { key: "url", label: "URL", type: "text", required: true },
          { key: "exclude_arr_tags", label: "Exclude Arr Tags", type: "text" },
        ],
      },
      {
        key: "radarr",
        title: "Radarr",
        type: "fields",
        required_group: "arr",
        fields: [{ key: "url", label: "URL", type: "text", required: true }],
      },
    ];
    const body = document.createElement("div");
    body.id = "configBody";
    document.body.appendChild(body);
    fieldInput(body, "sonarr", "url", "http://sonarr:8989");
    fieldInput(body, "sonarr", "exclude_arr_tags", "");
    const radarrUrl = fieldInput(body, "radarr", "url", "");

    markRequiredFields(sections, body);

    expect(radarrUrl.classList.contains("cfg-required")).toBe(false);
    expect(hasError(radarrUrl)).toBe(false);
  });

  it("wires each input's listener once however many marking passes run", () => {
    // Re-rendering the form (or typing) runs the pass again; without the
    // data-required-wired guard every pass would stack another listener, and
    // each of those re-runs the pass, so the count compounds per keystroke.
    const f = buildArrForm();
    const listeners = vi.spyOn(f.sonarrUrl, "addEventListener");

    markRequiredFields(ARR_SECTIONS, f.body);
    markRequiredFields(ARR_SECTIONS, f.body);
    f.sonarrUrl.dispatchEvent(new Event("input", { bubbles: true }));

    expect(listeners.mock.calls.filter(([type]) => type === "input")).toHaveLength(1);
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

  it("defaults a section with no rendered toggle to enabled", () => {
    // A section whose header toggle is missing from the DOM (collapsed, or a
    // schema/DOM mismatch) must not silently disable the section on save.
    const schema: SchemaSection[] = [
      {
        key: "sonarr",
        title: "Sonarr",
        type: "fields",
        enable_key: "enabled",
        fields: [{ key: "url", label: "URL", type: "text" }],
      },
    ];
    addInput(fieldId("sonarr", "url"), "http://sonarr:8989");

    expect(buildSectionsFromForm(schema)["sonarr"]).toEqual({
      enabled: true,
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

// --- Required-field marking: the cases the required_group tests don't reach ---

// One standalone section (no required_group), so updateFieldValidation runs
// instead of the group's force-clear path.
const SEARCH_SECTION: SchemaSection[] = [
  {
    key: "search",
    title: "Search",
    type: "fields",
    fields: [
      { key: "min_score", label: "Min Score", type: "number", required: true },
      { key: "note", label: "Note", type: "text" },
      { key: "token", label: "Token", type: "secret", required: true, secret: true },
    ],
  },
];

describe("config: markRequiredFields field state", () => {
  beforeEach(() => {
    document.body.replaceChildren();
  });

  it("flags an empty required field", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const inp = fieldInput(body, "search", "min_score", "");

    markRequiredFields(SEARCH_SECTION, body);

    expect(inp.classList.contains("cfg-required")).toBe(true);
    expect(hasError(inp)).toBe(true);
  });

  it("leaves a filled required field alone", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const inp = fieldInput(body, "search", "min_score", "75");

    markRequiredFields(SEARCH_SECTION, body);

    expect(inp.classList.contains("cfg-required")).toBe(false);
    expect(hasError(inp)).toBe(false);
  });

  it("treats a whitespace-only value as empty", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const inp = fieldInput(body, "search", "min_score", "   ");

    markRequiredFields(SEARCH_SECTION, body);

    expect(inp.classList.contains("cfg-required")).toBe(true);
  });

  it("clears the Required message once the field is filled", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const inp = fieldInput(body, "search", "min_score", "");
    markRequiredFields(SEARCH_SECTION, body);
    expect(hasError(inp)).toBe(true);

    inp.value = "75";
    markRequiredFields(SEARCH_SECTION, body);

    expect(hasError(inp)).toBe(false);
    expect(inp.classList.contains("cfg-required")).toBe(false);
  });

  it("adds one Required message however many passes run", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const inp = fieldInput(body, "search", "min_score", "");

    markRequiredFields(SEARCH_SECTION, body);
    markRequiredFields(SEARCH_SECTION, body);

    expect(inp.closest(".cfg-field")?.querySelectorAll(".cfg-error").length).toBe(1);
  });

  it("never flags an optional field", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const optional = fieldInput(body, "search", "note", "");

    markRequiredFields(SEARCH_SECTION, body);

    expect(optional.classList.contains("cfg-required")).toBe(false);
    expect(hasError(optional)).toBe(false);
  });

  it("accepts a redacted secret as filled outside a group", () => {
    const body = document.createElement("div");
    document.body.appendChild(body);
    const secret = fieldInput(body, "search", "token", "");
    secret.placeholder = "****";

    markRequiredFields(SEARCH_SECTION, body);

    expect(secret.classList.contains("cfg-required")).toBe(false);
    expect(hasError(secret)).toBe(false);
  });

  it("still flags an empty secret with no placeholder", () => {
    // No placeholder means the server holds no value: this one really is
    // missing, redaction is not what makes a secret optional.
    const body = document.createElement("div");
    document.body.appendChild(body);
    const secret = fieldInput(body, "search", "token", "");

    markRequiredFields(SEARCH_SECTION, body);

    expect(secret.classList.contains("cfg-required")).toBe(true);
  });

  it("accepts a redacted secret as filled", () => {
    // A stored secret comes back as the "****" placeholder with an empty value;
    // flagging it would tell the user to retype a key the server already has.
    // The proof is the SIBLING: only a satisfied group clears radarr too.
    const f = buildArrForm();
    f.sonarrUrl.value = "http://sonarr:8989";
    f.sonarrKey.placeholder = "****";

    markRequiredFields(ARR_SECTIONS, f.body);

    expect(f.sonarrKey.classList.contains("cfg-required")).toBe(false);
    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(false);
    expect(hasError(f.radarrUrl)).toBe(false);
  });

  it("needs EVERY required field of a member, not just one", () => {
    // Sonarr's URL alone does not make the arr group satisfied: without the key
    // the connection cannot be made, so both members stay flagged.
    const f = buildArrForm();
    f.sonarrUrl.value = "http://sonarr:8989";

    markRequiredFields(ARR_SECTIONS, f.body);

    expect(f.sonarrKey.classList.contains("cfg-required")).toBe(true);
    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(true);
  });

  it("does not count a whitespace-only value towards a group", () => {
    const f = buildArrForm();
    f.sonarrUrl.value = "   ";
    f.sonarrKey.value = "   ";

    markRequiredFields(ARR_SECTIONS, f.body);

    expect(f.radarrUrl.classList.contains("cfg-required")).toBe(true);
  });

  it("does not count a member whose inputs are absent from the form", () => {
    // A section rendered without its inputs (collapsed, or a schema/DOM
    // mismatch) must not satisfy the group on behalf of the others.
    const body = document.createElement("div");
    body.id = "configBody";
    document.body.appendChild(body);
    const radarrUrl = fieldInput(body, "radarr", "url", "");
    fieldInput(body, "radarr", "api_key", "");

    markRequiredFields(ARR_SECTIONS, body);

    expect(radarrUrl.classList.contains("cfg-required")).toBe(true);
  });
});

/** A `.cfg-field`-wrapped input, addressed the way the renderer addresses it. */
function fieldInput(
  body: HTMLElement,
  section: string,
  field: string,
  value: string,
): HTMLInputElement {
  const wrap = document.createElement("div");
  wrap.className = "cfg-field";
  const inp = document.createElement("input");
  inp.type = "text";
  inp.id = fieldId(section, field);
  inp.value = value;
  wrap.appendChild(inp);
  body.appendChild(wrap);
  return inp;
}

// --- Save-error decoding ---
//
// The chain under test is the one a failed PUT /api/config/structured walks:
// the action's decodeError lifts the server envelope, configSaveError maps a
// known error code to a fixed sentence, and friendlyConfigError rewrites
// anything else out of Go's validator vocabulary into a UI sentence.

function decodeSaveError(
  status: number,
  body: unknown,
): { message: string; status: number; code?: string } {
  const def = actionDefs.map.get("config.save");
  if (!def?.decodeError) {
    throw new Error("config.save action has no decodeError");
  }
  return def.decodeError({ status, body }).error;
}

/** The user-visible message for a server error body. */
function saveMessage(body: unknown): string {
  return decodeSaveError(422, body).message;
}

describe("config: save-error decoding", () => {
  it("lifts the server's error text and status onto the envelope", () => {
    const err = decodeSaveError(422, { error: "bad key", code: "config_invalid" });

    expect(err.status).toBe(422);
    expect(err.code).toBe("config_invalid");
  });

  it("omits the code when the server sent none", () => {
    const err = decodeSaveError(500, { error: "boom" });

    expect("code" in err).toBe(false);
  });

  it("falls back to Unknown error on an empty body", () => {
    expect(saveMessage(undefined)).toBe("Unknown error");
  });

  it("maps an unreachable-arr code to its own sentence", () => {
    expect(
      saveMessage({ error: "dial tcp: connection refused", code: "config_unreachable_arr" }),
    ).toBe("Sonarr/Radarr unreachable. Check the URL and API key.");
  });

  it("maps a too-large config to its own sentence", () => {
    expect(saveMessage({ error: "body too large", code: "config_too_large" })).toBe(
      "Configuration is too large. Remove unused entries and try again.",
    );
  });

  it("maps a failed reload to its own sentence", () => {
    expect(saveMessage({ error: "activate: bad provider", code: "config_reload_failed" })).toBe(
      "Configuration could not be applied and was not saved. Check server logs for details.",
    );
  });

  it("keeps the server's text when the code is unmapped", () => {
    expect(saveMessage({ error: "languages: rule 1 has no targets", code: "config_invalid" })).toBe(
      "Languages: rule 1 has no targets",
    );
  });

  it("strips Go's invalid-configuration prefix", () => {
    expect(saveMessage({ error: "invalid configuration: media_roots is empty" })).toBe(
      "Media_roots is empty",
    );
  });

  it("strips the prefix when no space follows the colon", () => {
    expect(saveMessage({ error: "invalid configuration:media_roots is empty" })).toBe(
      "Media_roots is empty",
    );
  });

  it("keeps an invalid-configuration mention that is not a prefix", () => {
    // The anchor matters: only the leading wrapper is noise, the same words
    // inside the message are the message.
    expect(saveMessage({ error: "provider x: invalid configuration: bad key" })).toBe(
      "Provider x: invalid configuration: bad key",
    );
  });

  it("strips Go's parse-YAML prefix", () => {
    expect(saveMessage({ error: "parse YAML: unexpected end of stream" })).toBe(
      "Unexpected end of stream",
    );
  });

  it("strips the parse-YAML prefix when no space follows the colon", () => {
    expect(saveMessage({ error: "parse YAML:unexpected end of stream" })).toBe(
      "Unexpected end of stream",
    );
  });

  it("keeps a parse-YAML mention that is not a prefix", () => {
    expect(saveMessage({ error: "step 2 could not parse YAML: broken" })).toBe(
      "Step 2 could not parse YAML: broken",
    );
  });

  it("turns a YAML line error into a line-numbered syntax message", () => {
    expect(saveMessage({ error: "yaml: line 7: something odd" })).toBe("Syntax error on line 7");
  });

  it("keeps every digit of a multi-digit line number", () => {
    expect(saveMessage({ error: "yaml: line 142: something odd" })).toBe(
      "Syntax error on line 142",
    );
  });

  it("reads a line error written without spaces", () => {
    expect(saveMessage({ error: "yaml:line3: something odd" })).toBe("Syntax error on line 3");
  });

  it("appends the fix hint for a recognized YAML pattern", () => {
    expect(
      saveMessage({ error: "yaml: line 4: mapping values are not allowed in this context" }),
    ).toBe("Syntax error on line 4: check indentation and colons");
  });

  it("hints about indentation for an unexpected key", () => {
    expect(saveMessage({ error: "yaml: line 9: did not find expected key" })).toBe(
      "Syntax error on line 9: unexpected value, check indentation",
    );
  });

  it("hints about an unclosed quote", () => {
    expect(saveMessage({ error: "yaml: line 2: could not find expected ':'" })).toBe(
      "Syntax error on line 2: missing closing quote or bracket",
    );
  });

  it("hints about quoting for an illegal leading character", () => {
    expect(
      saveMessage({ error: "yaml: line 5: found character that cannot start any token" }),
    ).toBe("Syntax error on line 5: invalid character, try quoting the value");
  });

  it("keeps a yaml-line mention that is not a prefix as prose", () => {
    expect(saveMessage({ error: "config rejected near yaml: line 3" })).toBe(
      "Config rejected near yaml: line 3",
    );
  });

  it("capitalizes the first letter and keeps the rest verbatim", () => {
    expect(saveMessage({ error: "sonarr.url must be an absolute URL" })).toBe(
      "Sonarr.url must be an absolute URL",
    );
  });
});
