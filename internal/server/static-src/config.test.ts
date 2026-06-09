// @vitest-environment happy-dom
import { describe, it, expect, beforeEach } from "vitest";
import { markRequiredFields } from "./config.js";
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
