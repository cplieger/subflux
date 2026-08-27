// webauthn-utils.ts — Shared WebAuthn helpers (base64url encoding, Signal API).
// Single source of truth; imported by login.ts and security.ts.

import { webauthnSignalData } from "./wire/client.gen.js";
import type { Signals } from "./wire/types.gen.js";

function base64urlToBuffer(b64: string): ArrayBuffer {
  const padded = b64.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

export function bufferToBase64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let binary = "";
  for (const b of bytes) {
    binary += String.fromCharCode(b);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** Convert WebAuthn REQUEST (login) options from their wire shape (WebAuthn
 *  Level 3 JSON: base64url strings) to the buffer shape
 *  navigator.credentials.get expects. Mutates in place and re-types. */
export function requestOptionsFromJSON(
  json: PublicKeyCredentialRequestOptionsJSON,
): PublicKeyCredentialRequestOptions {
  const pk = json as unknown as PublicKeyCredentialRequestOptions;
  pk.challenge = base64urlToBuffer(json.challenge);
  if (json.allowCredentials) {
    for (const cred of json.allowCredentials) {
      (cred as unknown as PublicKeyCredentialDescriptor).id = base64urlToBuffer(cred.id);
    }
  }
  return pk;
}

/** Convert WebAuthn CREATION (registration) options from their wire shape to
 *  the buffer shape navigator.credentials.create expects. Mutates in place
 *  and re-types. */
export function creationOptionsFromJSON(
  json: PublicKeyCredentialCreationOptionsJSON,
): PublicKeyCredentialCreationOptions {
  const pk = json as unknown as PublicKeyCredentialCreationOptions;
  pk.challenge = base64urlToBuffer(json.challenge);
  pk.user.id = base64urlToBuffer(json.user.id);
  if (json.excludeCredentials) {
    for (const cred of json.excludeCredentials) {
      (cred as unknown as PublicKeyCredentialDescriptor).id = base64urlToBuffer(cred.id);
    }
  }
  return pk;
}

/** How long to wait for one Signal API call before abandoning it.
 *
 *  Safari 26 can leave a Signal API promise pending forever
 *  (https://bugs.webkit.org/show_bug.cgi?id=278339). A signal is a
 *  best-effort hint: losing one costs a stale passkey label until the next
 *  reconciliation, while waiting on one blocks whatever the caller does next. */
const SIGNAL_DEADLINE_MS = 2000;

/** Run one Signal API call, bounded and never throwing. A synchronous throw, a
 *  rejection, and a promise that never settles all resolve here. */
async function sendOne(send: () => Promise<unknown>): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    const quiet = Promise.resolve()
      .then(send)
      .then(
        () => undefined,
        () => undefined,
      );
    const deadline = new Promise<void>((resolve) => {
      timer = setTimeout(resolve, SIGNAL_DEADLINE_MS);
    });
    await Promise.race([quiet, deadline]);
  } finally {
    clearTimeout(timer);
  }
}

/** Fetch the reconciliation payloads, or null when there is nothing to signal. */
async function signalData(): Promise<Signals | null> {
  try {
    return await webauthnSignalData();
  } catch {
    return null;
  }
}

/** Tell the user's passkey provider what this server currently holds for the
 *  account: which credentials it still accepts, and the account's current
 *  label. Both are best-effort hints and neither is observable, so this never
 *  throws and never blocks for longer than SIGNAL_DEADLINE_MS per call.
 *
 *  Call it from an authenticated page, not from a page about to navigate. */
export async function sendWebAuthnSignals(): Promise<void> {
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime feature detection
  if (!window.PublicKeyCredential) {
    return;
  }
  const data = await signalData();
  if (!data) {
    return;
  }

  // Independent rather than sequential. Each call carries a different fact,
  // and Safari can hang or reject one of them; awaiting them in series let
  // either outcome suppress the signal behind it.
  //
  // The server derives these payloads in the browser API's own shape, so they
  // pass straight through — no field remapping to drift out of step with it.
  const sent: Promise<void>[] = [];
  if (typeof PublicKeyCredential.signalAllAcceptedCredentials === "function") {
    sent.push(
      sendOne(() => PublicKeyCredential.signalAllAcceptedCredentials(data.allAcceptedCredentials)),
    );
  }
  if (typeof PublicKeyCredential.signalCurrentUserDetails === "function") {
    sent.push(sendOne(() => PublicKeyCredential.signalCurrentUserDetails(data.currentUserDetails)));
  }
  await Promise.all(sent);
}
