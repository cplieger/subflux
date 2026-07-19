// webauthn-utils.ts — Shared WebAuthn helpers (base64url encoding, Signal API).
// Single source of truth; imported by login.ts and security.ts.

import { webauthnSignalData } from "./wire/client.gen.js";

export function base64urlToBuffer(b64: string): ArrayBuffer {
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

export async function sendWebAuthnSignals(): Promise<void> {
  try {
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime feature detection
    if (!window.PublicKeyCredential) {
      return;
    }
    const data = await webauthnSignalData();
    if (!data) {
      return;
    }
    if (typeof PublicKeyCredential.signalAllAcceptedCredentials === "function") {
      await PublicKeyCredential.signalAllAcceptedCredentials({
        rpId: data.rp_id,
        userId: data.user_id,
        allAcceptedCredentialIds: [...data.credential_ids],
      });
    }
    if (typeof PublicKeyCredential.signalCurrentUserDetails === "function") {
      await PublicKeyCredential.signalCurrentUserDetails({
        rpId: data.rp_id,
        userId: data.user_id,
        name: data.name,
        displayName: data.display_name,
      });
    }
  } catch {
    /* non-critical */
  }
}
