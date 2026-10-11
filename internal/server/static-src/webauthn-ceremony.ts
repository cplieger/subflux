// webauthn-ceremony.ts — the one owner of the passkey ceremonies; the three
// surfaces (Security dialog, wizard, login) render an outcome and nothing else.
// create and get stay two functions — different endpoints, option types and
// outcomes — sharing the plumbing beneath them.
import {
  webauthnAvailabilityRaw,
  webauthnLoginBeginRaw,
  webauthnRegisterBeginRaw,
  PATH_WEBAUTHN_LOGIN_FINISH,
  PATH_WEBAUTHN_REGISTER_FINISH,
} from "./wire/client.gen.js";
import type { WebAuthnUnavailableReason } from "./wire/types.gen.js";
import {
  bufferToBase64url,
  creationOptionsFromJSON,
  requestOptionsFromJSON,
  sendWebAuthnSignals,
} from "./webauthn-utils.js";
import { hasCode, ErrorCode } from "./error_codes.js";

// --- Availability ---

export type UnavailableReason =
  WebAuthnUnavailableReason | "browser_unsupported" | "no_password" | "probe_failed";

export type Availability =
  | { readonly available: true }
  | {
      readonly available: false;
      readonly reason: UnavailableReason;
      readonly rpID: string;
      readonly suggestedRPID: string;
    };

type Unavailable = Extract<Availability, { available: false }>;

export function unavailable(reason: UnavailableReason): Unavailable {
  return { available: false, reason, rpID: "", suggestedRPID: "" };
}

let probeInFlight: Promise<Availability> | null = null;

/** Ask the server whether a passkey ceremony could be conducted from this
 *  page's own origin. Advisory: the authoritative refusal is at Begin, with
 *  the server's message. Concurrent callers share one request; nothing is
 *  memoised, because the relying-party ID hot-reloads. */
export function probeAvailability(): Promise<Availability> {
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime feature detection
  if (!window.PublicKeyCredential) {
    return Promise.resolve(unavailable("browser_unsupported"));
  }
  probeInFlight ??= probe().finally(() => {
    probeInFlight = null;
  });
  return probeInFlight;
}

async function probe(): Promise<Availability> {
  const r = await webauthnAvailabilityRaw({ origin: window.location.origin });
  if (!r.ok || r.data === undefined) {
    return unavailable("probe_failed");
  }
  if (r.data.available) {
    return { available: true };
  }
  return {
    available: false,
    reason: r.data.reason ?? "address_unusable",
    rpID: r.data.rp_id ?? "",
    suggestedRPID: r.data.suggested_rp_id ?? "",
  };
}

const SENTENCES: Record<UnavailableReason, (a: Unavailable) => string> = {
  unconfigured: (a) =>
    "Passkeys are not set up yet because no relying-party ID is configured. " +
    (a.suggestedRPID === ""
      ? "Save your settings once from this address and subflux fills it in, "
      : `Save your settings once from this address and subflux fills it in as "${a.suggestedRPID}", `) +
    "or set auth.webauthn_rp_id in the Authentication section of Settings.",
  unconfigurable: () =>
    "Passkeys cannot be used at this address. A passkey is scoped to a domain name, so an IP address, a single-label hostname or a bare public suffix cannot have one. Use a domain name over HTTPS, or http://localhost.",
  init_failed: (a) =>
    `Passkeys are unavailable because the configured relying-party ID "${a.rpID}" was rejected at startup. Fix auth.webauthn_rp_id in the Authentication section of Settings.`,
  insecure_scheme: () =>
    "Passkeys require HTTPS. This page is served over plain HTTP, and the only exception the standard makes is http://localhost.",
  address_unusable: () =>
    "Passkeys cannot be used at this address. The browser reports it in a form a passkey cannot be scoped to. A trailing dot is the usual cause, so reach subflux at the same host without the trailing dot.",
  origin_not_accepted: (a) =>
    `Passkeys here are configured for "${a.rpID}", which does not cover this address. Reach subflux at a host inside "${a.rpID}"` +
    (a.suggestedRPID === ""
      ? "."
      : `, or change auth.webauthn_rp_id to "${a.suggestedRPID}". That change strands any passkey already registered.`),
  origin_not_allowlisted: () =>
    "Passkeys here are restricted to specific addresses, and this is not one of them. Reach subflux at an address on its allowed list.",
  browser_unsupported: () =>
    "This browser does not support passkeys. You can add one later from another device.",
  no_password: () =>
    "Passkey enrollment needs your password to confirm. You can add one any time from the Security dialog.",
  probe_failed: () =>
    "Could not check whether passkeys are available. Reload the page to try again.",
};

export function unavailableSentence(a: Unavailable): string {
  return SENTENCES[a.reason](a);
}

// --- Outcomes ---

type RegisterOutcome =
  | { readonly kind: "registered" }
  | { readonly kind: "cancelled" }
  | { readonly kind: "duplicate" }
  | { readonly kind: "timeout" }
  | { readonly kind: "not-discoverable"; readonly message: string }
  | { readonly kind: "failed"; readonly message: string };

export type LoginOutcome =
  | { readonly kind: "authenticated"; readonly redirect: string }
  | { readonly kind: "cancelled" }
  | { readonly kind: "timeout" }
  | { readonly kind: "session-expired" }
  | { readonly kind: "unknown-credential" }
  | { readonly kind: "verification-failed" }
  | { readonly kind: "failed"; readonly message: string };

const REGISTER_BEGIN_FALLBACK = "Failed to start passkey registration";
const REGISTER_FINISH_FALLBACK = "Failed to register passkey";
const LOGIN_BEGIN_FALLBACK = "Failed to start passkey login";
const LOGIN_FINISH_FALLBACK = "Passkey authentication failed";

// --- The create ceremony ---

/** Register a passkey for the signed-in account. Every server answer and every
 *  authenticator refusal maps onto one arm; the caller renders it. */
export async function registerPasskey(password: string): Promise<RegisterOutcome> {
  if (password === "") {
    return { kind: "failed", message: unavailableSentence(unavailable("no_password")) };
  }
  try {
    const begin = await webauthnRegisterBeginRaw({ password });
    if (!begin.ok || begin.data?.publicKey === undefined) {
      return { kind: "failed", message: begin.error ?? REGISTER_BEGIN_FALLBACK };
    }
    const publicKey = creationOptionsFromJSON(begin.data.publicKey.publicKey);
    const credential = await navigator.credentials.create({ publicKey });
    if (!credential) {
      return { kind: "cancelled" };
    }
    const res = await finishCeremony(
      PATH_WEBAUTHN_REGISTER_FINISH,
      begin.data.session_token,
      creationFinishBody(credential as PublicKeyCredential),
    );
    if (res.ok) {
      await sendWebAuthnSignals();
      return { kind: "registered" };
    }
    if (hasCode(res, ErrorCode.WebAuthnNotDiscoverable)) {
      return { kind: "not-discoverable", message: res.error ?? REGISTER_FINISH_FALLBACK };
    }
    return { kind: "failed", message: res.error ?? REGISTER_FINISH_FALLBACK };
  } catch (e: unknown) {
    const verdict = classifyDOMException(e);
    switch (verdict.kind) {
      case "timeout":
        return { kind: "timeout" };
      case "cancelled":
        return { kind: "cancelled" };
      case "duplicate":
        return { kind: "duplicate" };
      case "failed":
        return {
          kind: "failed",
          message:
            verdict.name === ""
              ? "Passkey registration failed."
              : `Passkey registration failed (${verdict.name}).`,
        };
    }
  }
}

// --- The get ceremony ---

/** Sign in with a passkey. `mediation: "conditional"` runs the autofill
 *  ceremony; `signal` lets the caller abort it. */
export async function authenticateWithPasskey(opts?: {
  readonly mediation?: "conditional";
  readonly signal?: AbortSignal;
}): Promise<LoginOutcome> {
  try {
    const begin = await webauthnLoginBeginRaw(
      opts?.mediation === "conditional" ? { mediation: "conditional" } : undefined,
    );
    if (!begin.ok || begin.data?.publicKey === undefined) {
      return { kind: "failed", message: begin.error ?? LOGIN_BEGIN_FALLBACK };
    }
    const request: CredentialRequestOptions = {
      publicKey: requestOptionsFromJSON(begin.data.publicKey.publicKey),
    };
    if (opts?.mediation === "conditional") {
      request.mediation = "conditional";
    }
    if (opts?.signal !== undefined) {
      request.signal = opts.signal;
    }
    const credential = await navigator.credentials.get(request);
    if (!credential) {
      return { kind: "cancelled" };
    }
    const res = await finishCeremony(
      PATH_WEBAUTHN_LOGIN_FINISH,
      begin.data.session_token,
      assertionFinishBody(credential as PublicKeyCredential),
    );
    if (res.ok) {
      return { kind: "authenticated", redirect: res.redirect ?? "/" };
    }
    // The unknown-credential answer is keyed on `signal`: its envelope carries
    // no code.
    if (res.signal === "unknown_credential") {
      return { kind: "unknown-credential" };
    }
    if (hasCode(res, ErrorCode.WebAuthnSessionInvalid, ErrorCode.BadRequest)) {
      return { kind: "session-expired" };
    }
    if (hasCode(res, ErrorCode.WebAuthnAssertionFailed)) {
      return { kind: "verification-failed" };
    }
    return { kind: "failed", message: res.error ?? LOGIN_FINISH_FALLBACK };
  } catch (e: unknown) {
    const verdict = classifyDOMException(e);
    switch (verdict.kind) {
      case "timeout":
        return { kind: "timeout" };
      case "cancelled":
        return { kind: "cancelled" };
      case "duplicate":
        // An exclusion list exists only on the create leg, so here the name
        // carries no duplicate-credential meaning.
        return { kind: "failed", message: "Passkey login failed (InvalidStateError)." };
      case "failed":
        return {
          kind: "failed",
          message:
            verdict.name === ""
              ? "Passkey login failed."
              : `Passkey login failed (${verdict.name}).`,
        };
    }
  }
}

// --- The plumbing beneath both ceremonies ---

interface AbortVerdict {
  readonly kind: "timeout" | "cancelled" | "duplicate" | "failed";
  readonly name: string;
}

// AbortSignal.timeout() aborts with TimeoutError, not AbortError, so the
// register leg's timeout is only reachable through the first arm; AbortError
// is the login page cancelling its own autofill ceremony.
function classifyDOMException(e: unknown): AbortVerdict {
  if (!(e instanceof DOMException)) {
    return { kind: "failed", name: "" };
  }
  switch (e.name) {
    case "TimeoutError":
      return { kind: "timeout", name: e.name };
    case "AbortError":
    case "NotAllowedError":
      return { kind: "cancelled", name: e.name };
    case "InvalidStateError":
      return { kind: "duplicate", name: e.name };
    default:
      return { kind: "failed", name: e.name };
  }
}

interface FinishResult {
  readonly ok: boolean;
  readonly error?: string;
  readonly code?: string;
  readonly signal?: string;
  readonly redirect?: string;
}

// A raw fetch: the generated client's transport carries no per-call header
// seam, and the ceremony needs X-WebAuthn-Session.
async function finishCeremony(
  path: string,
  sessionToken: string,
  body: unknown,
): Promise<FinishResult> {
  let res: Response;
  try {
    res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-WebAuthn-Session": sessionToken },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(10_000),
    });
  } catch (e: unknown) {
    if (e instanceof DOMException) {
      throw e;
    }
    return { ok: false };
  }
  const data: unknown = await res.json().catch(() => ({}));
  const out: { -readonly [K in keyof FinishResult]: FinishResult[K] } = {
    ok: res.ok,
  };
  for (const key of ["error", "code", "signal", "redirect"] as const) {
    const value = readString(data, key);
    if (value !== undefined) {
      out[key] = value;
    }
  }
  return out;
}

function readString(obj: unknown, key: string): string | undefined {
  if (typeof obj !== "object" || obj === null) {
    return undefined;
  }
  const value = (obj as Record<string, unknown>)[key];
  return typeof value === "string" ? value : undefined;
}

/** The registration finish body. It forwards ONLY the solicited extension
 *  output (credProps): go-webauthn refuses any output the relying party did
 *  not request, so forwarding the browser's whole map would fail the ceremony
 *  after the biometric prompt. */
function creationFinishBody(credential: PublicKeyCredential): Record<string, unknown> {
  const response = credential.response as AuthenticatorAttestationResponse;
  const body: Record<string, unknown> = {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      attestationObject: bufferToBase64url(response.attestationObject),
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      transports: transportsOf(response),
    },
  };
  const credProps = readCredProps(credential.getClientExtensionResults());
  if (credProps !== undefined) {
    body["clientExtensionResults"] = { credProps };
  }
  return body;
}

function readCredProps(ext: unknown): Record<string, unknown> | undefined {
  if (typeof ext !== "object" || ext === null) {
    return undefined;
  }
  const credProps = (ext as Record<string, unknown>)["credProps"];
  if (typeof credProps !== "object" || credProps === null) {
    return undefined;
  }
  return credProps as Record<string, unknown>;
}

// getTransports is absent on older engines; an empty list is what the server
// stored before it was forwarded at all.
function transportsOf(response: AuthenticatorAttestationResponse): string[] {
  const getTransports = (response as { getTransports?: () => unknown }).getTransports;
  const list = typeof getTransports === "function" ? getTransports.call(response) : [];
  return Array.isArray(list) ? list.filter((t): t is string => typeof t === "string") : [];
}

// The assertion leg requests no extensions, so it forwards no results: the
// same unsolicited-output check would fail every login.
function assertionFinishBody(credential: PublicKeyCredential): Record<string, unknown> {
  const response = credential.response as AuthenticatorAssertionResponse;
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      authenticatorData: bufferToBase64url(response.authenticatorData),
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      signature: bufferToBase64url(response.signature),
      userHandle: response.userHandle ? bufferToBase64url(response.userHandle) : "",
    },
  };
}
