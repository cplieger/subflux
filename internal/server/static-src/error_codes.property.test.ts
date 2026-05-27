// Property-based tests for error_codes.ts hasCode function.
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { hasCode, ErrorCode } from "./error_codes.js";

const allCodes = Object.values(ErrorCode);

describe("hasCode property tests", () => {
  it("null-safety: hasCode(null/undefined/{}, ...anyCodes) === false", () => {
    fc.assert(
      fc.property(fc.subarray(allCodes, { minLength: 0, maxLength: 5 }), (codes) => {
        expect(hasCode(null, ...codes)).toBe(false);
        expect(hasCode(undefined, ...codes)).toBe(false);
        expect(hasCode({}, ...codes)).toBe(false);
      }),
    );
  });

  it("membership: hasCode({code: C}, C) === true for any code in catalog", () => {
    fc.assert(
      fc.property(fc.constantFrom(...allCodes), (code) => {
        expect(hasCode({ code }, code)).toBe(true);
      }),
    );
  });

  it("non-membership: hasCode({code: C}, ...others) === false when C not in others", () => {
    fc.assert(
      fc.property(
        fc.constantFrom(...allCodes),
        fc.subarray(allCodes, { minLength: 1, maxLength: 5 }),
        (code, subset) => {
          const others = subset.filter((c) => c !== code);
          if (others.length === 0) return; // skip if all match
          expect(hasCode({ code }, ...others)).toBe(false);
        },
      ),
    );
  });

  it("variadic: hasCode({code: C}, A, B, C) === true regardless of position", () => {
    fc.assert(
      fc.property(
        fc.constantFrom(...allCodes),
        fc.subarray(allCodes, { minLength: 0, maxLength: 4 }),
        (code, prefix) => {
          const codes = [...prefix, code];
          expect(hasCode({ code }, ...codes)).toBe(true);
        },
      ),
    );
  });
});
