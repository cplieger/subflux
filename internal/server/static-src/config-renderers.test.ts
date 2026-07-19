// @vitest-environment happy-dom
// Unit tests for config-renderers.ts pure helpers. The formatDurationCfg
// table is ported from the former config-yaml.test.ts (the function moved
// here when the YAML text helpers were deleted).
import { describe, it, expect } from "vitest";
import { formatDurationCfg } from "./config-renderers.js";

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
