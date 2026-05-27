// Property-based tests for config-yaml.ts parsing functions.
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { parseYAMLSections, extractYAMLValue, formatDurationCfg } from "./config-yaml.js";

describe("config-yaml property tests", () => {
  it("parseYAMLSections: output keys are exactly the section headers", () => {
    fc.assert(
      fc.property(
        fc.array(fc.constantFrom("server", "providers", "languages", "adaptive", "sync"), {
          minLength: 1,
          maxLength: 5,
        }),
        fc.array(fc.string({ minLength: 0, maxLength: 30 }), { minLength: 0, maxLength: 5 }),
        (keys, values) => {
          const uniqueKeys = [...new Set(keys)];
          const lines = uniqueKeys.flatMap((k, i) => {
            const val = values[i] ?? "value";
            return [`${k}:`, `  field: ${val}`];
          });
          const result = parseYAMLSections(lines);
          const resultKeys = Object.keys(result).sort();
          expect(resultKeys).toEqual(uniqueKeys.sort());
        },
      ),
    );
  });

  it("extractYAMLValue: round-trips simple scalars", () => {
    fc.assert(
      fc.property(
        fc.string({ minLength: 1, maxLength: 20 }).filter((k) => /^[a-z_]+$/.test(k)),
        fc
          .string({ minLength: 1, maxLength: 30 })
          .filter(
            (v) =>
              !v.includes("#") &&
              !v.includes("\n") &&
              !v.includes(":") &&
              !v.includes('"') &&
              !v.includes("'") &&
              !v.startsWith(" ") &&
              !v.endsWith(" "),
          ),
        (key, value) => {
          const yaml = `${key}: ${value}`;
          const result = extractYAMLValue(yaml, key);
          expect(result).toBe(value);
        },
      ),
    );
  });

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
