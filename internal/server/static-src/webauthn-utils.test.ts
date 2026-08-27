// webauthn-utils.test.ts — the wire/buffer boundary and the Signal API.
//
// Two things are worth pinning here. First, the JSON→buffer converters are
// where a base64url string becomes the ArrayBuffer the credential API needs:
// get the alphabet or the padding wrong and login fails with an opaque
// browser error, so the tests assert the decoded BYTES, not just the type.
// Second, sendWebAuthnSignals is all feature detection — three independent
// capability checks whose absent arms have to be exercised, because a browser
// missing one must still run the rest and must never let a failed signal
// break the login it rides along with.
//
// Every test states the PublicKeyCredential capability it assumes AT THE
// SITE, with vi.stubGlobal, rather than relying on what this Chromium build
// happens to ship: the signal methods arrived in Chromium 132 and the
// production code feature-detects them precisely because that varies.
// `unstubGlobals` in vitest.config undoes each stub after the test.
import { describe, it, expect, vi, onTestFinished } from "vitest";
import type { Signals } from "./wire/types.gen.js";
import {
  bufferToBase64url,
  requestOptionsFromJSON,
  creationOptionsFromJSON,
  sendWebAuthnSignals,
} from "./webauthn-utils.js";

// mockReset strips a vi.fn's implementation before each test, so the factory
// is a plain function over a hoisted record (the pattern security.test.ts
// uses for the same module).
const wire = vi.hoisted(() => ({
  data: null as Signals | null,
  reads: 0,
}));
vi.mock("./wire/client.gen.js", () => ({
  webauthnSignalData: () => {
    wire.reads++;
    return Promise.resolve(wire.data);
  },
}));

function bytesOf(buf: ArrayBuffer | BufferSource): number[] {
  return [...new Uint8Array(buf as ArrayBuffer)];
}

/** base64url of the bytes 0xFB 0xFF: standard base64 is "+/8=", so this one
 *  value exercises both alphabet substitutions and the padding strip. */
const ALPHABET_BYTES = [0xfb, 0xff];
const ALPHABET_B64URL = "-_8";

/** The server derives these payloads in the browser API's own shape, so the
 *  fixture is what the wire actually carries. */
function signalData(): Signals {
  return {
    allAcceptedCredentials: {
      rpId: "subflux.example.com",
      userId: "dXNlci0x",
      allAcceptedCredentialIds: ["Y3JlZC1h", "Y3JlZC1i"],
    },
    currentUserDetails: {
      rpId: "subflux.example.com",
      userId: "dXNlci0x",
      name: "admin",
      displayName: "Administrator",
    },
  };
}

describe("bufferToBase64url", () => {
  it("encodes to the URL alphabet with the padding stripped", () => {
    expect(bufferToBase64url(new Uint8Array(ALPHABET_BYTES).buffer)).toBe(ALPHABET_B64URL);
  });

  it("encodes an empty buffer as an empty string", () => {
    expect(bufferToBase64url(new ArrayBuffer(0))).toBe("");
  });
});

describe("requestOptionsFromJSON", () => {
  it("decodes the challenge to the bytes the base64url encoded", () => {
    const pk = requestOptionsFromJSON({ challenge: ALPHABET_B64URL });

    expect(pk.challenge).toBeInstanceOf(ArrayBuffer);
    expect(bytesOf(pk.challenge)).toStrictEqual(ALPHABET_BYTES);
  });

  it("decodes every allowCredentials id, not only the first", () => {
    const pk = requestOptionsFromJSON({
      challenge: "AAAA",
      allowCredentials: [
        { id: "AQID", type: "public-key" },
        { id: ALPHABET_B64URL, type: "public-key" },
      ],
    });

    const ids = (pk.allowCredentials ?? []).map((c) => bytesOf(c.id));
    expect(ids).toStrictEqual([[1, 2, 3], ALPHABET_BYTES]);
  });

  it("returns options with no allowCredentials key when the wire omitted it", () => {
    const pk = requestOptionsFromJSON({ challenge: "AQID", rpId: "subflux.example.com" });

    expect(pk.allowCredentials).toBeUndefined();
    expect(bytesOf(pk.challenge)).toStrictEqual([1, 2, 3]);
  });
});

describe("creationOptionsFromJSON", () => {
  function creationJSON(
    over: Partial<PublicKeyCredentialCreationOptionsJSON> = {},
  ): PublicKeyCredentialCreationOptionsJSON {
    return {
      challenge: "AQID",
      rp: { name: "subflux", id: "subflux.example.com" },
      user: { id: ALPHABET_B64URL, name: "admin", displayName: "Administrator" },
      pubKeyCredParams: [{ alg: -7, type: "public-key" }],
      ...over,
    };
  }

  it("decodes both the challenge and the user id", () => {
    const pk = creationOptionsFromJSON(creationJSON());

    expect(bytesOf(pk.challenge)).toStrictEqual([1, 2, 3]);
    expect(bytesOf(pk.user.id)).toStrictEqual(ALPHABET_BYTES);
  });

  it("decodes every excludeCredentials id", () => {
    const pk = creationOptionsFromJSON(
      creationJSON({
        excludeCredentials: [
          { id: "AQID", type: "public-key" },
          { id: ALPHABET_B64URL, type: "public-key" },
        ],
      }),
    );

    const ids = (pk.excludeCredentials ?? []).map((c) => bytesOf(c.id));
    expect(ids).toStrictEqual([[1, 2, 3], ALPHABET_BYTES]);
  });

  it("returns options with no excludeCredentials key when the wire omitted it", () => {
    const pk = creationOptionsFromJSON(creationJSON());

    expect(pk.excludeCredentials).toBeUndefined();
    expect(pk.user.name).toBe("admin");
  });
});

describe("sendWebAuthnSignals", () => {
  /** Installs a PublicKeyCredential carrying exactly the named signal
   *  methods, so a test's assumption about the browser is visible at the
   *  site instead of inherited from it. */
  function stubCredential(methods: {
    signalAllAcceptedCredentials?: ReturnType<typeof vi.fn>;
    signalCurrentUserDetails?: ReturnType<typeof vi.fn>;
  }): void {
    vi.stubGlobal("PublicKeyCredential", { ...methods });
  }

  it("does not reach the network when the browser has no PublicKeyCredential", async () => {
    vi.stubGlobal("PublicKeyCredential", undefined);
    wire.reads = 0;

    await sendWebAuthnSignals();

    expect(wire.reads).toBe(0);
  });

  it("signals nothing when the server answers no data", async () => {
    const all = vi.fn(() => Promise.resolve());
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all, signalCurrentUserDetails: current });
    wire.reads = 0;
    wire.data = null;

    await sendWebAuthnSignals();

    expect(wire.reads).toBe(1);
    expect(all).not.toHaveBeenCalled();
    expect(current).not.toHaveBeenCalled();
  });

  it("hands each payload to its own signal call, not the other's", async () => {
    const all = vi.fn(() => Promise.resolve());
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all, signalCurrentUserDetails: current });
    wire.data = signalData();

    await sendWebAuthnSignals();

    expect(all).toHaveBeenCalledWith({
      rpId: "subflux.example.com",
      userId: "dXNlci0x",
      allAcceptedCredentialIds: ["Y3JlZC1h", "Y3JlZC1i"],
    });
    expect(current).toHaveBeenCalledWith({
      rpId: "subflux.example.com",
      userId: "dXNlci0x",
      name: "admin",
      displayName: "Administrator",
    });
  });

  it("still signals the current user when signalAllAcceptedCredentials is absent", async () => {
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalCurrentUserDetails: current });
    wire.data = signalData();

    await sendWebAuthnSignals();

    expect(current).toHaveBeenCalledTimes(1);
  });

  it("still signals accepted credentials when signalCurrentUserDetails is absent", async () => {
    const all = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all });
    wire.data = signalData();

    await sendWebAuthnSignals();

    expect(all).toHaveBeenCalledTimes(1);
  });

  it("resolves rather than throwing when a signal call rejects", async () => {
    const all = vi.fn(() => Promise.reject(new Error("signal refused")));
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all, signalCurrentUserDetails: current });
    wire.data = signalData();

    // A refused signal is non-critical and must never surface to the caller.
    await expect(sendWebAuthnSignals()).resolves.toBeUndefined();
  });

  it("still signals the current user when the accepted-credentials call rejects", async () => {
    const all = vi.fn(() => Promise.reject(new Error("signal refused")));
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all, signalCurrentUserDetails: current });
    wire.data = signalData();

    await sendWebAuthnSignals();

    // The two calls carry different facts, so one failing must not suppress
    // the other. Awaiting them in series used to skip this one.
    expect(current).toHaveBeenCalledTimes(1);
  });

  it("resolves rather than throwing when a signal call throws synchronously", async () => {
    const all = vi.fn(() => {
      throw new TypeError("bad argument");
    }) as unknown as ReturnType<typeof vi.fn>;
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all, signalCurrentUserDetails: current });
    wire.data = signalData();

    await expect(sendWebAuthnSignals()).resolves.toBeUndefined();
    expect(current).toHaveBeenCalledTimes(1);
  });

  it("abandons a signal that never settles instead of hanging the caller", async () => {
    vi.useFakeTimers();
    onTestFinished(() => {
      vi.useRealTimers();
    });
    // Safari 26 can leave this promise pending forever:
    // https://bugs.webkit.org/show_bug.cgi?id=278339
    const all = vi.fn(() => new Promise<void>(() => undefined));
    const current = vi.fn(() => Promise.resolve());
    stubCredential({ signalAllAcceptedCredentials: all, signalCurrentUserDetails: current });
    wire.data = signalData();

    const pending = sendWebAuthnSignals();
    await vi.advanceTimersByTimeAsync(2000);

    await expect(pending).resolves.toBeUndefined();
    expect(current).toHaveBeenCalledTimes(1);
  });
});
