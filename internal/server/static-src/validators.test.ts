// @vitest-environment node
//
// Table-driven tests for the runtime-validation helpers in validators.ts.
//
// The per-shape decoders (decodeStats, decodeSeriesItem, etc.) are
// generated from Go structs by cmd/wire-codegen and live in
// wire/decoders.gen.ts. They aren't tested here individually; they are
// exercised by the integration paths at every apiGetTyped call site.

import { describe, it, expect } from "vitest";

import {
  asObject,
  asArray,
  reqStr,
  reqNum,
  reqBool,
  optStr,
  optNum,
  optBool,
  reqOneOf,
  decodeArray,
  decodeRecord,
} from "./validators.js";

describe("helpers", () => {
  it("asObject rejects non-objects", () => {
    expect(() => asObject(null)).toThrow(/expected object, got null/);
    expect(() => asObject([])).toThrow(/expected object, got array/);
    expect(() => asObject("hi")).toThrow(/expected object, got string/);
    expect(() => asObject(42)).toThrow(/expected object, got number/);
    expect(asObject({ a: 1 })).toEqual({ a: 1 });
  });

  it("asArray rejects non-arrays", () => {
    expect(() => asArray({})).toThrow(/expected array, got object/);
    expect(() => asArray(null)).toThrow(/expected array, got null/);
    expect(asArray([1, 2])).toEqual([1, 2]);
  });

  it("reqStr/reqNum/reqBool include the path in error messages", () => {
    expect(() => reqStr({ x: 1 }, "x", "$.foo")).toThrow(/\$\.foo\.x: expected string, got number/);
    expect(() => reqNum({ x: "a" }, "x", "$.foo")).toThrow(
      /\$\.foo\.x: expected number, got string/,
    );
    expect(() => reqBool({ x: 1 }, "x", "$.foo")).toThrow(
      /\$\.foo\.x: expected boolean, got number/,
    );
  });

  it("reqNum rejects NaN and Infinity", () => {
    expect(() => reqNum({ x: NaN }, "x")).toThrow(/expected number/);
    expect(() => reqNum({ x: Infinity }, "x")).toThrow(/expected number/);
    expect(() => reqNum({ x: -Infinity }, "x")).toThrow(/expected number/);
    expect(reqNum({ x: 0 }, "x")).toBe(0);
    expect(reqNum({ x: -1.5 }, "x")).toBe(-1.5);
  });

  it("optStr / optNum / optBool permit undefined", () => {
    expect(optStr({}, "x")).toBeUndefined();
    expect(optStr({ x: "hi" }, "x")).toBe("hi");
    expect(optNum({}, "x")).toBeUndefined();
    expect(optNum({ x: 5 }, "x")).toBe(5);
    expect(optBool({}, "x")).toBeUndefined();
    expect(optBool({ x: false }, "x")).toBe(false);
  });

  it("optStr / optNum reject non-undefined non-matching types", () => {
    expect(() => optStr({ x: 1 }, "x")).toThrow(/expected string or undefined/);
    expect(() => optNum({ x: "no" }, "x")).toThrow(/expected number or undefined/);
    expect(() => optBool({ x: 0 }, "x")).toThrow(/expected boolean or undefined/);
  });

  it("reqOneOf enforces enum membership", () => {
    expect(reqOneOf({ t: "a" }, "t", ["a", "b"] as const)).toBe("a");
    expect(() => reqOneOf({ t: "c" }, "t", ["a", "b"] as const)).toThrow(
      /expected one of a\|b, got "c"/,
    );
    expect(() => reqOneOf({ t: 1 }, "t", ["a", "b"] as const)).toThrow(
      /expected one of a\|b, got 1/,
    );
  });

  it("decodeArray returns mapped values and reports per-index path", () => {
    const out = decodeArray([1, 2, 3], (v) => {
      if (typeof v !== "number") {throw new TypeError("not number");}
      return v * 2;
    });
    expect(out).toEqual([2, 4, 6]);

    expect(() =>
      decodeArray(
        [1, "bad", 3],
        (v) => {
          if (typeof v !== "number") {throw new TypeError("not number");}
          return v;
        },
        "$.list",
      ),
    ).toThrow(/\$\.list\[1\]: not number/);
  });

  it("decodeRecord iterates entries with path-aware errors", () => {
    const out = decodeRecord({ a: 1, b: 2 }, (v) => {
      if (typeof v !== "number") {throw new TypeError("not number");}
      return v + 1;
    });
    expect(out).toEqual({ a: 2, b: 3 });

    expect(() =>
      decodeRecord(
        { a: 1, b: "x" },
        (v) => {
          if (typeof v !== "number") {throw new TypeError("not number");}
          return v;
        },
        "$.map",
      ),
    ).toThrow(/\$\.map\.b: not number/);
  });
});
