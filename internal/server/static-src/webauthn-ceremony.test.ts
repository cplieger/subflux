// webauthn-ceremony.test.ts — the one ceremony module: the finish bodies, the
// DOMException mapping, the server-answer map and the availability probe.
import { describe, it, expect, beforeEach, vi } from "vitest";

// The mocked client answers from this hoisted record (vitest.config's
// mockReset strips a vi.fn's implementation, so every factory is a plain
// function closing over it).
const client = vi.hoisted(() => ({
  availability: { ok: true, status: 200, data: { available: true } } as unknown,
  availabilityCalls: 0,
  registerBegin: { ok: false, status: 400 } as unknown,
  registerBeginBodies: [] as unknown[],
  loginBegin: { ok: false, status: 400 } as unknown,
  loginBeginQueries: [] as unknown[],
  signalDataCalls: 0,
}));
vi.mock("./wire/client.gen.js", () => ({
  webauthnAvailabilityRaw: () => {
    client.availabilityCalls++;
    return Promise.resolve(client.availability);
  },
  // Cloned per call: requestOptionsFromJSON decodes the options IN PLACE, so
  // a second ceremony in one test must not see the first one's buffers.
  webauthnRegisterBeginRaw: (body: unknown) => {
    client.registerBeginBodies.push(body);
    return Promise.resolve(structuredClone(client.registerBegin));
  },
  webauthnLoginBeginRaw: (query: unknown) => {
    client.loginBeginQueries.push(query);
    return Promise.resolve(structuredClone(client.loginBegin));
  },
  webauthnSignalData: () => {
    client.signalDataCalls++;
    return Promise.resolve(null);
  },
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
  PATH_WEBAUTHN_LOGIN_FINISH: "/api/auth/webauthn/login/finish",
}));

import {
  authenticateWithPasskey,
  probeAvailability,
  registerPasskey,
  unavailable,
  unavailableSentence,
  type UnavailableReason,
} from "./webauthn-ceremony.js";

// --- Boundaries: the finish fetch and the authenticator ---

const net = vi.hoisted(() => ({
  finishStatus: 200,
  finishBody: {} as unknown,
  finishNotJSON: false,
  finishNetworkFailure: false,
  calls: [] as { url: string; headers: Record<string, string>; body: unknown }[],
}));

const authenticator = vi.hoisted(() => ({
  createResult: null as unknown,
  createThrows: null as unknown,
  getResult: null as unknown,
  getThrows: null as unknown,
  getRequests: [] as unknown[],
}));

function installBoundaries(): void {
  vi.stubGlobal("fetch", (url: unknown, init?: RequestInit) => {
    net.calls.push({
      url: String(url),
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
    });
    if (net.finishNetworkFailure) {
      return Promise.reject(new TypeError("Failed to fetch"));
    }
    if (net.finishNotJSON) {
      return Promise.resolve(new Response("not json", { status: net.finishStatus }));
    }
    return Promise.resolve(
      new Response(JSON.stringify(net.finishBody), {
        status: net.finishStatus,
        headers: { "Content-Type": "application/json" },
      }),
    );
  });
  Object.defineProperty(navigator, "credentials", {
    configurable: true,
    value: {
      create: () =>
        authenticator.createThrows !== null
          ? Promise.reject(authenticator.createThrows)
          : Promise.resolve(authenticator.createResult),
      get: (request: unknown) => {
        authenticator.getRequests.push(request);
        return authenticator.getThrows !== null
          ? Promise.reject(authenticator.getThrows)
          : Promise.resolve(authenticator.getResult);
      },
    },
  });
}

function beginResponse(): Record<string, unknown> {
  return {
    session_token: "sess-token-123",
    publicKey: {
      publicKey: {
        challenge: "Y2hhbGxlbmdl",
        rp: { id: "example.com", name: "subflux" },
        user: { id: "dXNlcjE", name: "root", displayName: "root" },
        pubKeyCredParams: [],
      },
    },
  };
}

function okBegin(): unknown {
  return { ok: true, status: 200, data: beginResponse() };
}

// bufferToBase64url of these byte arrays: rawId "AQID", attestation "BAU",
// clientData "Bg", authenticatorData "Bw", signature "CA".
function attestation(
  extensions: unknown,
  opts: { transports?: string[] | "absent" } = {},
): Record<string, unknown> {
  const response: Record<string, unknown> = {
    attestationObject: new Uint8Array([4, 5]).buffer,
    clientDataJSON: new Uint8Array([6]).buffer,
  };
  if (opts.transports !== "absent") {
    response["getTransports"] = () => opts.transports ?? ["internal", "hybrid"];
  }
  return {
    id: "cred-id-1",
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: "public-key",
    response,
    getClientExtensionResults: () => extensions,
  };
}

function assertion(extensions: unknown): Record<string, unknown> {
  return {
    id: "cred-id-1",
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: "public-key",
    response: {
      authenticatorData: new Uint8Array([7]).buffer,
      clientDataJSON: new Uint8Array([6]).buffer,
      signature: new Uint8Array([8]).buffer,
      userHandle: null,
    },
    getClientExtensionResults: () => extensions,
  };
}

function finishCall(): { url: string; headers: Record<string, string>; body: unknown } {
  const call = net.calls[0];
  if (call === undefined) {
    throw new Error("no finish request was made");
  }
  return call;
}

beforeEach(() => {
  client.availability = { ok: true, status: 200, data: { available: true } };
  client.availabilityCalls = 0;
  client.registerBegin = okBegin();
  client.registerBeginBodies = [];
  client.loginBegin = okBegin();
  client.loginBeginQueries = [];
  client.signalDataCalls = 0;
  net.finishStatus = 200;
  net.finishBody = {};
  net.finishNotJSON = false;
  net.finishNetworkFailure = false;
  net.calls = [];
  authenticator.createResult = attestation({ credProps: { rk: true } });
  authenticator.createThrows = null;
  authenticator.getResult = assertion({});
  authenticator.getThrows = null;
  authenticator.getRequests = [];
  installBoundaries();
});

// --- The registration finish body (defect 4a) ---

describe("registerPasskey: the finish body", () => {
  it("carries the credProps output and the transports", async () => {
    authenticator.createResult = attestation({ credProps: { rk: true } });

    await registerPasskey("pw");

    expect(finishCall().body).toEqual({
      id: "cred-id-1",
      rawId: "AQID",
      type: "public-key",
      response: {
        attestationObject: "BAU",
        clientDataJSON: "Bg",
        transports: ["internal", "hybrid"],
      },
      clientExtensionResults: { credProps: { rk: true } },
    });
  });

  it("forwards ONLY the solicited output: an unsolicited extension is dropped", async () => {
    authenticator.createResult = attestation({ credProps: { rk: true }, prf: { enabled: true } });

    await registerPasskey("pw");

    expect(finishCall().body).toHaveProperty("clientExtensionResults", { credProps: { rk: true } });
  });

  it("omits clientExtensionResults entirely when the browser reported no credProps", async () => {
    authenticator.createResult = attestation({});

    await registerPasskey("pw");

    expect(finishCall().body).not.toHaveProperty("clientExtensionResults");
  });

  it("sends an empty transports list when the engine has no getTransports", async () => {
    authenticator.createResult = attestation({ credProps: { rk: true } }, { transports: "absent" });

    const outcome = await registerPasskey("pw");

    expect(outcome).toEqual({ kind: "registered" });
    expect(finishCall().body).toHaveProperty("response.transports", []);
  });

  it("carries the session token in the WebAuthn session header on a raw POST", async () => {
    await registerPasskey("pw");

    expect(finishCall().url).toBe("/api/auth/webauthn/register/finish");
    expect(finishCall().headers).toEqual({
      "Content-Type": "application/json",
      "X-WebAuthn-Session": "sess-token-123",
    });
  });
});

// --- The DOMException mapping (defect 4b) ---

describe("registerPasskey: authenticator refusals", () => {
  const rows: { name: string; want: unknown }[] = [
    { name: "TimeoutError", want: { kind: "timeout" } },
    { name: "NotAllowedError", want: { kind: "cancelled" } },
    { name: "AbortError", want: { kind: "cancelled" } },
    { name: "InvalidStateError", want: { kind: "duplicate" } },
    {
      name: "SecurityError",
      want: { kind: "failed", message: "Passkey registration failed (SecurityError)." },
    },
  ];
  for (const row of rows) {
    it(`maps ${row.name}`, async () => {
      authenticator.createThrows = new DOMException("boom", row.name);

      expect(await registerPasskey("pw")).toEqual(row.want);
    });
  }

  it("never renders a bare DOMException name as the whole message", async () => {
    authenticator.createThrows = new DOMException("boom", "SecurityError");

    const outcome = await registerPasskey("pw");

    expect(outcome.kind).toBe("failed");
    if (outcome.kind === "failed") {
      expect(outcome.message).not.toBe("SecurityError");
    }
  });

  it("maps a non-DOMException throw to a plain failure", async () => {
    authenticator.createThrows = new Error("weird");

    expect(await registerPasskey("pw")).toEqual({
      kind: "failed",
      message: "Passkey registration failed.",
    });
  });

  it("treats a null credential as a cancellation", async () => {
    authenticator.createResult = null;

    expect(await registerPasskey("pw")).toEqual({ kind: "cancelled" });
  });
});

// --- The server-answer map (defect 4c) ---

describe("registerPasskey: server answers", () => {
  it("surfaces the server's own begin refusal, message and all", async () => {
    client.registerBegin = {
      ok: false,
      status: 400,
      error: "passkeys are not configured on this instance",
      code: "webauthn_unconfigured",
    };

    expect(await registerPasskey("pw")).toEqual({
      kind: "failed",
      message: "passkeys are not configured on this instance",
    });
    expect(net.calls).toEqual([]);
  });

  it("falls back to the begin sentence when the refusal carries no message", async () => {
    client.registerBegin = { ok: false, status: 500 };

    expect(await registerPasskey("pw")).toEqual({
      kind: "failed",
      message: "Failed to start passkey registration",
    });
  });

  it("refuses an empty password before contacting the server", async () => {
    expect(await registerPasskey("")).toEqual({
      kind: "failed",
      message:
        "Passkey enrollment needs your password to confirm. You can add one any time from the Security dialog.",
    });
    expect(client.registerBeginBodies).toEqual([]);
  });

  it("reports a non-discoverable refusal on its own arm with the server's sentence", async () => {
    net.finishStatus = 400;
    net.finishBody = {
      error:
        "this authenticator cannot store a passkey; try a different device or a password manager",
      code: "webauthn_not_discoverable",
    };

    expect(await registerPasskey("pw")).toEqual({
      kind: "not-discoverable",
      message:
        "this authenticator cannot store a passkey; try a different device or a password manager",
    });
  });

  it("surfaces any other finish refusal's message", async () => {
    net.finishStatus = 401;
    net.finishBody = { error: "invalid or expired session", code: "webauthn_session_invalid" };

    expect(await registerPasskey("pw")).toEqual({
      kind: "failed",
      message: "invalid or expired session",
    });
  });

  it("falls back to the finish sentence when a rejected finish carries no JSON", async () => {
    net.finishStatus = 500;
    net.finishNotJSON = true;

    expect(await registerPasskey("pw")).toEqual({
      kind: "failed",
      message: "Failed to register passkey",
    });
  });

  it("falls back to the finish sentence when the finish request never reaches the server", async () => {
    net.finishNetworkFailure = true;

    expect(await registerPasskey("pw")).toEqual({
      kind: "failed",
      message: "Failed to register passkey",
    });
  });

  it("signals the credential manager before reporting success", async () => {
    expect(await registerPasskey("pw")).toEqual({ kind: "registered" });
    expect(client.signalDataCalls).toBe(1);
  });
});

// --- The login leg ---

describe("authenticateWithPasskey", () => {
  it("forwards no extension results on the assertion leg", async () => {
    authenticator.getResult = assertion({ prf: { results: {} } });

    await authenticateWithPasskey();

    expect(finishCall().url).toBe("/api/auth/webauthn/login/finish");
    expect(finishCall().body).toEqual({
      id: "cred-id-1",
      rawId: "AQID",
      type: "public-key",
      response: { authenticatorData: "Bw", clientDataJSON: "Bg", signature: "CA", userHandle: "" },
    });
  });

  it("passes conditional mediation and the caller's signal to the authenticator", async () => {
    const controller = new AbortController();

    await authenticateWithPasskey({ mediation: "conditional", signal: controller.signal });

    expect(client.loginBeginQueries).toEqual([{ mediation: "conditional" }]);
    expect(authenticator.getRequests[0]).toMatchObject({
      mediation: "conditional",
      signal: controller.signal,
    });
  });

  it("asks for no mediation when the caller names none", async () => {
    await authenticateWithPasskey();

    expect(client.loginBeginQueries).toEqual([undefined]);
    expect(authenticator.getRequests[0]).not.toHaveProperty("mediation");
  });

  it("returns the redirect on success", async () => {
    net.finishBody = { redirect: "/somewhere" };

    expect(await authenticateWithPasskey()).toEqual({
      kind: "authenticated",
      redirect: "/somewhere",
    });
  });

  const finishRows: { name: string; status: number; body: unknown; want: unknown }[] = [
    {
      name: "unknown credential is keyed on the signal field",
      status: 401,
      body: { error: "unknown credential", signal: "unknown_credential" },
      want: { kind: "unknown-credential" },
    },
    {
      name: "an invalid ceremony session restarts",
      status: 401,
      body: { error: "invalid or expired session", code: "webauthn_session_invalid" },
      want: { kind: "session-expired" },
    },
    {
      name: "a missing session token restarts",
      status: 400,
      body: { error: "missing session token", code: "bad_request" },
      want: { kind: "session-expired" },
    },
    {
      name: "a failed assertion has its own arm",
      status: 401,
      body: { error: "authentication failed", code: "webauthn_assertion_failed" },
      want: { kind: "verification-failed" },
    },
    {
      name: "a disabled account renders the server's sentence",
      status: 403,
      body: { error: "account disabled", code: "auth_account_disabled" },
      want: { kind: "failed", message: "account disabled" },
    },
    {
      name: "an unconfigured relying party renders the server's sentence",
      status: 400,
      body: {
        error: "passkeys are not configured on this instance",
        code: "webauthn_unconfigured",
      },
      want: { kind: "failed", message: "passkeys are not configured on this instance" },
    },
  ];
  for (const row of finishRows) {
    it(`finish: ${row.name}`, async () => {
      net.finishStatus = row.status;
      net.finishBody = row.body;

      expect(await authenticateWithPasskey()).toEqual(row.want);
    });
  }

  it("falls back to the login-finish sentence when a rejected finish carries no JSON", async () => {
    net.finishStatus = 500;
    net.finishNotJSON = true;

    expect(await authenticateWithPasskey()).toEqual({
      kind: "failed",
      message: "Passkey authentication failed",
    });
  });

  it("surfaces the server's begin refusal, and the begin fallback without one", async () => {
    client.loginBegin = { ok: false, status: 400, error: "boom", code: "webauthn_unconfigured" };
    expect(await authenticateWithPasskey()).toEqual({ kind: "failed", message: "boom" });

    client.loginBegin = { ok: false, status: 503 };
    expect(await authenticateWithPasskey()).toEqual({
      kind: "failed",
      message: "Failed to start passkey login",
    });
  });

  it("folds InvalidStateError into a named failure: there is no exclusion list on get", async () => {
    authenticator.getThrows = new DOMException("boom", "InvalidStateError");

    expect(await authenticateWithPasskey()).toEqual({
      kind: "failed",
      message: "Passkey login failed (InvalidStateError).",
    });
  });

  it("maps AbortError, TimeoutError and a plain throw", async () => {
    authenticator.getThrows = new DOMException("aborted", "AbortError");
    expect(await authenticateWithPasskey()).toEqual({ kind: "cancelled" });

    authenticator.getThrows = new DOMException("timed out", "TimeoutError");
    expect(await authenticateWithPasskey()).toEqual({ kind: "timeout" });

    authenticator.getThrows = new Error("weird");
    expect(await authenticateWithPasskey()).toEqual({
      kind: "failed",
      message: "Passkey login failed.",
    });
  });
});

// --- The availability probe ---

describe("probeAvailability", () => {
  it("answers browser_unsupported without a request when the API is absent", async () => {
    vi.stubGlobal("PublicKeyCredential", undefined);

    expect(await probeAvailability()).toEqual(unavailable("browser_unsupported"));
    expect(client.availabilityCalls).toBe(0);
  });

  it("answers available from the server's answer", async () => {
    expect(await probeAvailability()).toEqual({ available: true });
  });

  it("carries the reason, the configured and the suggested RP ID", async () => {
    client.availability = {
      ok: true,
      status: 200,
      data: {
        available: false,
        reason: "origin_not_accepted",
        rp_id: "example.com",
        suggested_rp_id: "example.net",
      },
    };

    expect(await probeAvailability()).toEqual({
      available: false,
      reason: "origin_not_accepted",
      rpID: "example.com",
      suggestedRPID: "example.net",
    });
  });

  it("answers probe_failed on a non-2xx", async () => {
    client.availability = { ok: false, status: 500, error: "internal error" };

    expect(await probeAvailability()).toEqual(unavailable("probe_failed"));
  });

  it("coalesces concurrent callers onto one request", async () => {
    const [a, b, c] = await Promise.all([
      probeAvailability(),
      probeAvailability(),
      probeAvailability(),
    ]);

    expect([a, b, c]).toEqual([{ available: true }, { available: true }, { available: true }]);
    expect(client.availabilityCalls).toBe(1);
  });

  it("issues a fresh request once the previous one has settled", async () => {
    await probeAvailability();
    await probeAvailability();

    expect(client.availabilityCalls).toBe(2);
  });
});

// --- The sentences, one per reason ---

describe("unavailableSentence", () => {
  const rows: { reason: UnavailableReason; rpID: string; suggested: string; want: string }[] = [
    {
      reason: "unconfigured",
      rpID: "",
      suggested: "example.com",
      want: 'Passkeys are not set up yet because no relying-party ID is configured. Save your settings once from this address and subflux fills it in as "example.com", or set auth.webauthn_rp_id in the Authentication section of Settings.',
    },
    {
      reason: "unconfigurable",
      rpID: "",
      suggested: "",
      want: "Passkeys cannot be used at this address. A passkey is scoped to a domain name, so an IP address, a single-label hostname or a bare public suffix cannot have one. Use a domain name over HTTPS, or http://localhost.",
    },
    {
      reason: "init_failed",
      rpID: "Example.COM",
      suggested: "",
      want: 'Passkeys are unavailable because the configured relying-party ID "Example.COM" was rejected at startup. Fix auth.webauthn_rp_id in the Authentication section of Settings.',
    },
    {
      reason: "insecure_scheme",
      rpID: "example.com",
      suggested: "",
      want: "Passkeys require HTTPS. This page is served over plain HTTP, and the only exception the standard makes is http://localhost.",
    },
    {
      reason: "address_unusable",
      rpID: "example.com",
      suggested: "",
      want: "Passkeys cannot be used at this address. The browser reports it in a form a passkey cannot be scoped to. A trailing dot is the usual cause, so reach subflux at the same host without the trailing dot.",
    },
    {
      reason: "origin_not_accepted",
      rpID: "example.com",
      suggested: "example.net",
      want: 'Passkeys here are configured for "example.com", which does not cover this address. Reach subflux at a host inside "example.com", or change auth.webauthn_rp_id to "example.net". That change strands any passkey already registered.',
    },
    {
      reason: "origin_not_allowlisted",
      rpID: "example.com",
      suggested: "",
      want: "Passkeys here are restricted to specific addresses, and this is not one of them. Reach subflux at an address on its allowed list.",
    },
    {
      reason: "browser_unsupported",
      rpID: "",
      suggested: "",
      want: "This browser does not support passkeys. You can add one later from another device.",
    },
    {
      reason: "no_password",
      rpID: "",
      suggested: "",
      want: "Passkey enrollment needs your password to confirm. You can add one any time from the Security dialog.",
    },
    {
      reason: "probe_failed",
      rpID: "",
      suggested: "",
      want: "Could not check whether passkeys are available. Reload the page to try again.",
    },
  ];
  for (const row of rows) {
    it(`renders ${row.reason}`, () => {
      expect(
        unavailableSentence({
          available: false,
          reason: row.reason,
          rpID: row.rpID,
          suggestedRPID: row.suggested,
        }),
      ).toBe(row.want);
    });
  }

  it("gives every reason a distinct sentence", () => {
    const sentences = rows.map((row) =>
      unavailableSentence({
        available: false,
        reason: row.reason,
        rpID: row.rpID,
        suggestedRPID: row.suggested,
      }),
    );

    expect(new Set(sentences).size).toBe(10);
  });

  it("omits the suggestion clause when no relying-party ID can be derived", () => {
    expect(
      unavailableSentence({
        available: false,
        reason: "origin_not_accepted",
        rpID: "example.com",
        suggestedRPID: "",
      }),
    ).toBe(
      'Passkeys here are configured for "example.com", which does not cover this address. Reach subflux at a host inside "example.com".',
    );
  });
});
