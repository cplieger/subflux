// Unit tests for config-yaml.ts — table-driven pure function tests.
import { describe, it, expect } from "vitest";
import {
  parseYAMLSections,
  extractYAMLValue,
  extractYAMLBlock,
  parseProviderBlocks,
  formatDurationCfg,
} from "./config-yaml.js";

describe("parseYAMLSections", () => {
  const cases = [
    {
      name: "splits two top-level sections",
      input: ["poll_interval: 30s", "scan_interval: 24h"],
      expected: { poll_interval: "poll_interval: 30s", scan_interval: "scan_interval: 24h" },
    },
    {
      name: "includes indented lines in section",
      input: ["providers:", "  opensubtitles:", "    enabled: true", "logging:", "  level: info"],
      expected: {
        providers: "providers:\n  opensubtitles:\n    enabled: true",
        logging: "logging:\n  level: info",
      },
    },
    {
      name: "returns empty object for empty input",
      input: [],
      expected: {},
    },
    {
      name: "ignores comment lines",
      input: ["# comment", "key: value"],
      expected: { key: "key: value" },
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(parseYAMLSections(tc.input)).toEqual(tc.expected);
    });
  }
});

describe("extractYAMLValue", () => {
  const cases = [
    {
      name: "extracts simple value",
      raw: "enabled: true\npriority: 5",
      key: "enabled",
      expected: "true",
    },
    { name: "extracts numeric value", raw: "priority: 99", key: "priority", expected: "99" },
    {
      name: "strips surrounding quotes",
      raw: 'api_key: "abc123"',
      key: "api_key",
      expected: "abc123",
    },
    { name: "strips single quotes", raw: "name: 'test'", key: "name", expected: "test" },
    {
      name: "strips inline comments",
      raw: "timeout: 30s # seconds",
      key: "timeout",
      expected: "30s",
    },
    { name: "returns empty for missing key", raw: "foo: bar", key: "baz", expected: "" },
    {
      name: "returns empty for comment-only value",
      raw: "key: # nothing",
      key: "key",
      expected: "",
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(extractYAMLValue(tc.raw, tc.key)).toBe(tc.expected);
    });
  }
});

describe("extractYAMLBlock", () => {
  const cases = [
    {
      name: "extracts indented block",
      raw: "settings:\n  api_key: abc\n  user: test\nnext: val",
      key: "settings",
      expected: "  api_key: abc\n  user: test",
    },
    {
      name: "returns empty for missing key",
      raw: "foo: bar",
      key: "settings",
      expected: "",
    },
    {
      name: "returns inline value if on same line",
      raw: "settings: inline_value",
      key: "settings",
      expected: "inline_value",
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(extractYAMLBlock(tc.raw, tc.key)).toBe(tc.expected);
    });
  }
});

describe("parseProviderBlocks", () => {
  it("splits providers into per-name blocks", () => {
    const raw =
      "providers:\n  opensubtitles:\n    enabled: true\n    priority: 1\n  gestdown:\n    enabled: false";
    const result = parseProviderBlocks(raw);
    expect(Object.keys(result)).toEqual(["opensubtitles", "gestdown"]);
    expect(result["opensubtitles"]).toContain("enabled: true");
    expect(result["gestdown"]).toContain("enabled: false");
  });

  it("returns empty object for empty input", () => {
    expect(parseProviderBlocks("")).toEqual({});
  });
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
