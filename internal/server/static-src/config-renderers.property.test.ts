// @vitest-environment happy-dom
// Property-based tests for config-renderers.ts pure helpers, ported from
// the former config-yaml.property.test.ts (formatDurationCfg moved here
// when the YAML text helpers were deleted).
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { formatDurationCfg } from "./config-renderers.js";

describe("config-renderers property tests", () => {
  it("formatDurationCfg: positive integers always produce non-empty string matching /^\\d+[smhD]$/", () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1_000_000_000, max: 365 * 24 * 3600_000_000_000 }), // 1s to 365D in nanoseconds
        (ns) => {
          const result = formatDurationCfg(ns);
          expect(result).not.toBe("");
          expect(result).toMatch(/^\d+[smhD]$/);
        },
      ),
    );
  });

  it("formatDurationCfg: zero and falsy return empty string", () => {
    expect(formatDurationCfg(0)).toBe("");
    expect(formatDurationCfg("")).toBe("");
  });
});
