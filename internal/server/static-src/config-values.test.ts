// config-values.test.ts — the typed accessors over GET /api/config/structured.
//
// This module replaced raw-YAML text extraction, and it inherited that
// extractor's answers on purpose: a missing key reads "", a nested object reads
// "" rather than JSON, a present-but-empty provider block counts as present,
// and a bool that is neither absent nor `false` counts as true. Those are the
// rules the settings drawer's prefill depends on, and every one of them is a
// decision a refactor could quietly change, so they are pinned here rather
// than left to the renderers' tests to imply.
import { describe, it, expect, beforeEach } from "vitest";
import {
  setCfgSections,
  cfgSectionEntries,
  scalarString,
  cfgValue,
  cfgSubValue,
  cfgBool,
  cfgScalar,
  cfgList,
  cfgProviderBlock,
} from "./config-values.js";

beforeEach(() => {
  setCfgSections({});
});

describe("scalarString", () => {
  const cases: { name: string; input: unknown; expected: string }[] = [
    { name: "passes a string through", input: "info", expected: "info" },
    { name: "renders a number", input: 30, expected: "30" },
    { name: "renders a negative number", input: -1, expected: "-1" },
    { name: "renders true", input: true, expected: "true" },
    { name: "renders false", input: false, expected: "false" },
    { name: "renders undefined as empty", input: undefined, expected: "" },
    { name: "renders null as empty", input: null, expected: "" },
    { name: "renders a nested object as empty", input: { a: 1 }, expected: "" },
    { name: "joins an array of scalars", input: ["a", "b"], expected: "a, b" },
    { name: "joins a mixed array", input: [1, true, "x"], expected: "1, true, x" },
    { name: "renders an empty array as empty", input: [], expected: "" },
    {
      name: "renders object members of an array as empty",
      input: [{ a: 1 }, "b"],
      expected: ", b",
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(scalarString(tc.input)).toBe(tc.expected);
    });
  }
});

describe("cfgValue", () => {
  it("reads a scalar field out of a mapping section", () => {
    setCfgSections({ sonarr: { url: "http://sonarr:8989" } });

    expect(cfgValue("sonarr", "url")).toBe("http://sonarr:8989");
  });

  it("reads empty for a missing key, a missing section and a non-mapping section", () => {
    setCfgSections({ sonarr: { url: "u" }, poll_interval: "30s" });

    expect(cfgValue("sonarr", "api_key")).toBe("");
    expect(cfgValue("radarr", "url")).toBe("");
    expect(cfgValue("poll_interval", "poll_interval")).toBe("");
  });
});

describe("cfgSubValue", () => {
  it("reads the scoring weights nesting", () => {
    setCfgSections({ scoring: { weights: { hash: 50 } } });

    expect(cfgSubValue("scoring", "weights", "hash")).toBe("50");
  });

  it("reads empty when the nested level is missing or not a mapping", () => {
    setCfgSections({ scoring: { weights: "nope" }, other: {} });

    expect(cfgSubValue("scoring", "weights", "hash")).toBe("");
    expect(cfgSubValue("other", "weights", "hash")).toBe("");
    expect(cfgSubValue("absent", "weights", "hash")).toBe("");
  });
});

describe("cfgBool", () => {
  it("returns the default when the key is absent or null", () => {
    setCfgSections({ adaptive: { enabled: null } });

    expect(cfgBool("adaptive", "enabled", true)).toBe(true);
    expect(cfgBool("adaptive", "enabled", false)).toBe(false);
    expect(cfgBool("adaptive", "missing", true)).toBe(true);
    expect(cfgBool("absent", "enabled", true)).toBe(true);
  });

  it("returns a real boolean as-is", () => {
    setCfgSections({ adaptive: { on: true, off: false } });

    expect(cfgBool("adaptive", "on", false)).toBe(true);
    expect(cfgBool("adaptive", "off", true)).toBe(false);
  });

  it("counts anything other than false as true", () => {
    // Inherited from the text extractor's `!== "false"` check: a YAML
    // `enabled: 0` or `enabled: yes` reads as enabled.
    setCfgSections({ adaptive: { a: "false", b: "true", c: 0, d: "no" } });

    expect(cfgBool("adaptive", "a", true)).toBe(false);
    expect(cfgBool("adaptive", "b", false)).toBe(true);
    expect(cfgBool("adaptive", "c", false)).toBe(true);
    expect(cfgBool("adaptive", "d", false)).toBe(true);
  });
});

describe("cfgScalar", () => {
  it("reads a section whose whole value is one scalar", () => {
    setCfgSections({ poll_interval: "30s" });

    expect(cfgScalar("poll_interval")).toBe("30s");
  });

  it("reads empty for a mapping section and for an absent one", () => {
    setCfgSections({ sonarr: { url: "u" } });

    expect(cfgScalar("sonarr")).toBe("");
    expect(cfgScalar("poll_interval")).toBe("");
  });
});

describe("cfgList", () => {
  it("reads a list section as strings", () => {
    setCfgSections({ media_roots: ["/media/tv", "/media/movies"] });

    expect(cfgList("media_roots")).toStrictEqual(["/media/tv", "/media/movies"]);
  });

  it("drops null and empty entries", () => {
    // A YAML list with a bare `-` decodes to null; keeping it would render an
    // empty row the operator has to delete before saving.
    setCfgSections({ media_roots: ["/media/tv", null, "", "/media/movies"] });

    expect(cfgList("media_roots")).toStrictEqual(["/media/tv", "/media/movies"]);
  });

  it("reads an empty list for a non-list or absent section", () => {
    setCfgSections({ sonarr: { url: "u" } });

    expect(cfgList("sonarr")).toStrictEqual([]);
    expect(cfgList("media_roots")).toStrictEqual([]);
  });
});

describe("cfgSectionEntries", () => {
  it("returns every section, which is how an unknown section gets rendered at all", () => {
    setCfgSections({ sonarr: { url: "u" }, mystery: { a: 1 } });

    expect(cfgSectionEntries().map(([k]) => k)).toStrictEqual(["sonarr", "mystery"]);
  });

  it("returns nothing after a failed load, which is the first-setup signal", () => {
    setCfgSections({});

    expect(cfgSectionEntries()).toStrictEqual([]);
  });
});

describe("cfgProviderBlock", () => {
  it("returns undefined for a provider with no entry", () => {
    // Distinct from an empty block: absent means "never configured", which the
    // drawer renders as disabled rather than enabled-with-defaults.
    setCfgSections({ providers: { subdl: { enabled: true } } });

    expect(cfgProviderBlock("opensubtitles")).toBeUndefined();
  });

  it("returns undefined when there is no providers section at all", () => {
    expect(cfgProviderBlock("subdl")).toBeUndefined();
  });

  it("returns an empty block for a bare `name:` entry", () => {
    setCfgSections({ providers: { subdl: null } });

    expect(cfgProviderBlock("subdl")).toStrictEqual({});
  });

  it("returns an empty block for a non-mapping entry", () => {
    setCfgSections({ providers: { subdl: "yes" } });

    expect(cfgProviderBlock("subdl")).toStrictEqual({});
  });

  it("carries enabled, priority and settings when the types are right", () => {
    setCfgSections({
      providers: { subdl: { enabled: false, priority: 3, settings: { api_key: "k" } } },
    });

    expect(cfgProviderBlock("subdl")).toStrictEqual({
      enabled: false,
      priority: 3,
      settings: { api_key: "k" },
    });
  });

  it("omits fields whose type is wrong rather than coercing them", () => {
    // The guards are what a config shape change would trip: `enabled: "true"`
    // must not arrive as a boolean, and `priority: "3"` must not arrive as 3.
    setCfgSections({
      providers: { subdl: { enabled: "true", priority: "3", settings: ["api_key"] } },
    });

    expect(cfgProviderBlock("subdl")).toStrictEqual({});
  });
});
