// Property-based tests for the base64url codec in webauthn-utils.ts.
//
// The two directions are not equally reachable: bufferToBase64url is exported,
// base64urlToBuffer is module-private and reached only through the option
// converters, so the decode side is driven through requestOptionsFromJSON's
// challenge — which is also how production reaches it.
//
// This codec sits between the server's WebAuthn JSON and
// navigator.credentials, so a single byte lost at either end is a passkey that
// silently stops working. Padding lengths (0, 1 or 2 `=` in standard base64)
// and the two substituted characters are exactly what an arbitrary byte array
// explores.
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { bufferToBase64url, requestOptionsFromJSON } from "./webauthn-utils.js";

/** decode drives the module-private base64urlToBuffer through the only
 *  exported path that reaches it. */
function decode(b64url: string): Uint8Array {
  return new Uint8Array(requestOptionsFromJSON({ challenge: b64url }).challenge as ArrayBuffer);
}

describe("base64url codec properties", () => {
  it("decode(encode(bytes)) returns the same bytes", () => {
    fc.assert(
      fc.property(fc.uint8Array({ maxLength: 256 }), (bytes) => {
        expect([...decode(bufferToBase64url(bytes.buffer as ArrayBuffer))]).toStrictEqual([
          ...bytes,
        ]);
      }),
    );
  });

  it("encode(decode(s)) returns s for any string encode can produce", () => {
    fc.assert(
      fc.property(fc.uint8Array({ maxLength: 256 }), (bytes) => {
        const s = bufferToBase64url(bytes.buffer as ArrayBuffer);
        expect(bufferToBase64url(decode(s).buffer as ArrayBuffer)).toBe(s);
      }),
    );
  });

  it("encoded output is URL-safe and unpadded", () => {
    fc.assert(
      fc.property(fc.uint8Array({ maxLength: 256 }), (bytes) => {
        expect(bufferToBase64url(bytes.buffer as ArrayBuffer)).toMatch(/^[A-Za-z0-9_-]*$/);
      }),
    );
  });

  it("encoded length is the unpadded base64 length of the input", () => {
    fc.assert(
      fc.property(fc.uint8Array({ maxLength: 256 }), (bytes) => {
        expect(bufferToBase64url(bytes.buffer as ArrayBuffer)).toHaveLength(
          Math.ceil(bytes.length / 3) * 4 - ((3 - (bytes.length % 3)) % 3),
        );
      }),
    );
  });
});
