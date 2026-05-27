// Property-based tests for formatOffsetMs.
//
// Invariants tested:
// 1. Sign preservation: positive → '+', negative → '-', zero → no sign.
// 2. Format invariant: output matches /^[+-]?\d+\.\d{3}s$/.
// 3. Magnitude preservation: parsing the numeric portion gives abs(ms)/1000.

import { describe, it, expect } from "vitest";
import fc from "fast-check";

import { formatOffsetMs } from "./sync-timecode.js";

describe("formatOffsetMs properties", () => {
  it("sign preservation", () => {
    fc.assert(
      fc.property(fc.integer({ min: -999_999_999, max: 999_999_999 }), (ms) => {
        const result = formatOffsetMs(ms);
        if (ms > 0) expect(result.startsWith("+")).toBe(true);
        else if (ms < 0) expect(result.startsWith("-")).toBe(true);
        else expect(result).toBe("0.000s");
      }),
    );
  });

  it("format invariant", () => {
    fc.assert(
      fc.property(fc.integer({ min: -999_999_999, max: 999_999_999 }), (ms) => {
        const result = formatOffsetMs(ms);
        expect(result).toMatch(/^[+-]?\d+\.\d{3}s$/);
      }),
    );
  });

  it("magnitude preservation", () => {
    fc.assert(
      fc.property(fc.integer({ min: -999_999_999, max: 999_999_999 }), (ms) => {
        const result = formatOffsetMs(ms);
        // Strip sign and trailing 's', parse as float.
        const numStr = result.replace(/^[+-]/, "").replace(/s$/, "");
        const parsed = parseFloat(numStr);
        const expected = Math.abs(ms) / 1000;
        // Compare with tolerance for floating point.
        expect(Math.abs(parsed - expected)).toBeLessThan(0.001);
      }),
    );
  });
});
