// webauthn-utils.ts — Shared WebAuthn helpers (base64url encoding, Signal API).
// Single source of truth; imported by login.ts and security.ts.

import { apiGetTyped } from "./api-client.js";
import { decodeSignalData } from "./wire/decoders.gen.js";

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

export async function sendWebAuthnSignals(): Promise<void> {
  try {
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime feature detection
    if (!window.PublicKeyCredential) {
      return;
    }
    const data = await apiGetTyped("/api/auth/webauthn/signal-data", decodeSignalData);
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
