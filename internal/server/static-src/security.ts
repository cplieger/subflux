// security.ts — Security popup: password, TOTP, passkeys, API keys.

import * as bus from "./bus.js";
import * as notify from "./notify.js";
import { el, icon, dialog, closeDialog, dialogHead, onBackdropClose, patch } from "./dom.js";
import { reconcile } from "./lib/reactive/reconcile.js";
import {
  apiGet,
  apiPost,
  apiPostRaw,
  apiPut,
  apiPutRaw,
  apiDelete,
  apiDeleteRaw,
} from "./api-client.js";
import { base64urlToBuffer, bufferToBase64url, sendWebAuthnSignals } from "./webauthn-utils.js";
import type { MeResponse } from "./api-types.js";
import { REAUTH_WINDOW_MS } from "./constants.js";

// --- Inline interfaces for API response shapes ---

interface PasskeyItem {
  id: number;
  name: string;
  created_at: string;
}

interface APIKeyItem {
  id: number;
  key_prefix: string;
  key_suffix: string;
  label: string;
  created_at: string;
}

interface TOTPEnableResponse {
  secret: string;
  uri: string;
  recovery_codes?: string[];
}

interface APIKeyCreateResponse {
  key: string;
}

// Client-side reauth tracking (5-minute window).
let lastReauthAt = 0;

/** Wrap an async click handler with disabled + aria-busy lifecycle. The
 *  button is disabled and announced as busy while the handler runs;
 *  state restores in `finally` regardless of resolve/reject. Re-clicks
 *  during pending are no-ops (early return on btn.disabled). Used across
 *  security.ts ops where the framework allowlist applies but the click
 *  surface still needs a11y feedback. */
function busyClick(handler: () => Promise<void>): (ev: Event) => Promise<void> {
  return async (ev: Event) => {
    const btn = ev.currentTarget as HTMLButtonElement | null;
    if (!btn || btn.disabled) {
      return;
    }
    btn.disabled = true;
    btn.setAttribute("aria-busy", "true");
    try {
      await handler();
    } finally {
      btn.disabled = false;
      btn.removeAttribute("aria-busy");
    }
  };
}

const secDlg = (): HTMLDialogElement => dialog("securityDialog");

export function initSecurity(): void {
  bus.on(bus.BusEvent.OpenSecurity, () => openSecurity());
}

async function openSecurity(): Promise<void> {
  const dlg = secDlg();
  const closeFn = (): void => {
    closeDialog(dlg);
  };
  const header = dialogHead("Security", closeFn);

  const body = el("div", { className: "dlg-body" });
  body.appendChild(el("p", { className: "muted" }, "Loading\u2026"));

  dlg.replaceChildren(header, body);
  dlg.showModal();
  onBackdropClose(dlg, closeFn);
  dlg.addEventListener("cancel", closeFn, { once: true });

  await renderSections(body);
}

async function renderSections(body: HTMLElement): Promise<void> {
  const [me, passkeys, apikeys] = await Promise.all([
    apiGet<MeResponse>("/api/auth/me"),
    apiGet<PasskeyItem[]>("/api/auth/passkeys"),
    apiGet<APIKeyItem[]>("/api/auth/apikeys"),
  ]);

  const frag = document.createDocumentFragment();

  frag.appendChild(buildPasswordSection());
  frag.appendChild(buildTOTPSection(me));
  frag.appendChild(buildPasskeysSection(passkeys));
  frag.appendChild(buildAPIKeysSection(apikeys));

  patch(body, frag);
}

// --- Reauthentication ---

function needsReauth(): boolean {
  return Date.now() - lastReauthAt > REAUTH_WINDOW_MS;
}

async function ensureReauth(): Promise<boolean> {
  if (!needsReauth()) {
    return true;
  }
  return showReauthPrompt();
}

function showReauthPrompt(): Promise<boolean> {
  return new Promise((resolve) => {
    const dlg = dialog("confirmDialog");
    if (dlg.open) {
      dlg.close();
    }
    let resolved = false;
    const settle = (ok: boolean): void => {
      if (resolved) {
        return;
      }
      resolved = true;
      closeDialog(dlg);
      resolve(ok);
    };

    const header = dialogHead("Reauthenticate", () => {
      settle(false);
    });

    const pwInput = el("input", {
      type: "password",
      id: "reauth-pw",
      autocomplete: "current-password",
      placeholder: "Password or TOTP code",
    }) as HTMLInputElement;

    const errEl = el("div", { className: "empty", hidden: true });

    const body = el(
      "div",
      { className: "dlg-body" },
      el("p", null, "Enter your password or TOTP code to continue."),
      pwInput,
      errEl,
    );

    const footer = el(
      "div",
      { className: "dlg-foot" },
      el(
        "button",
        {
          type: "button",
          onclick: busyClick(async () => {
            const val = pwInput.value.trim();
            if (!val) {
              return;
            }
            const r = await apiPostRaw<{ ok: boolean }>("/api/auth/reauth", { credential: val });
            if (r.ok) {
              lastReauthAt = Date.now();
              settle(true);
            } else {
              errEl.textContent = r.error ?? "Reauthentication failed";
              errEl.hidden = false;
            }
          }),
        },
        "Confirm",
      ),
      el(
        "button",
        {
          type: "button",
          className: "ghost",
          onclick: () => {
            settle(false);
          },
        },
        "Cancel",
      ),
    );

    dlg.replaceChildren(header, body, footer);
    dlg.showModal();
    onBackdropClose(dlg, () => {
      settle(false);
    });
    dlg.addEventListener(
      "close",
      () => {
        settle(false);
      },
      { once: true },
    );
    pwInput.focus();
  });
}

// --- Change Password ---

function buildPasswordSection(): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "Change Password"));

  const currentPw = el("input", {
    type: "password",
    id: "sec-current-pw",
    autocomplete: "current-password",
    placeholder: "Current password",
  }) as HTMLInputElement;

  const newPw = el("input", {
    type: "password",
    id: "sec-new-pw",
    autocomplete: "new-password",
    placeholder: "New password",
    minlength: "8",
    maxlength: "128",
    passwordrules: "minlength: 8; maxlength: 128;",
  }) as HTMLInputElement;

  const feedback = el("div", { className: "sec-feedback", hidden: true });

  const submitBtn = el(
    "button",
    {
      type: "button",
      onclick: busyClick(async () => {
        const cur = currentPw.value;
        const nw = newPw.value;
        if (!cur || !nw) {
          showFeedback(feedback, "Both fields are required", true);
          return;
        }
        if (nw.length < 8) {
          showFeedback(feedback, "Password must be at least 8 characters", true);
          return;
        }
        const ok = await ensureReauth();
        if (!ok) {
          return;
        }
        const r = await apiPutRaw<unknown>("/api/auth/password", {
          current_password: cur,
          new_password: nw,
        });
        if (r.ok) {
          showFeedback(feedback, "Password changed", false);
          currentPw.value = "";
          newPw.value = "";
        } else {
          showFeedback(feedback, r.error || "Failed to change password", true);
        }
      }),
    },
    "Change Password",
  );

  sec.appendChild(
    el(
      "div",
      { className: "sec-fields" },
      el("label", null, "Current password"),
      currentPw,
      el("label", null, "New password"),
      newPw,
    ),
  );
  sec.appendChild(feedback);
  sec.appendChild(el("div", { className: "sec-actions" }, submitBtn));
  return sec;
}

// --- TOTP ---

function buildTOTPSection(me: MeResponse | null): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "Two-Factor Authentication"));

  const totpEnabled = me?.totp_enabled ?? false;

  const status = el(
    "div",
    { className: "sec-status" },
    el("span", null, "Status: "),
    el(
      "span",
      {
        className: "badge",
        "data-status": totpEnabled ? "ok" : "",
      },
      totpEnabled ? "Enabled" : "Disabled",
    ),
  );
  sec.appendChild(status);

  const content = el("div", { className: "sec-totp-content" });
  sec.appendChild(content);

  if (totpEnabled) {
    const disableBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: busyClick(async () => {
          const ok = await ensureReauth();
          if (!ok) {
            return;
          }
          const code = await showInputDialog("Enter a TOTP code to disable 2FA:", {
            inputmode: "numeric",
            pattern: "[0-9]*",
            maxlength: "6",
            autocomplete: "one-time-code",
          });
          if (!code) {
            return;
          }
          const r = await apiDeleteRaw<unknown>("/api/auth/totp", { code });
          if (r.ok) {
            notify.success("TOTP disabled");
            await renderSections(secDlg().querySelector(".dlg-body")!);
          } else {
            notify.error(r.error ?? "Failed to disable TOTP");
          }
        }),
      },
      "Disable TOTP",
    );
    content.appendChild(el("div", { className: "sec-actions" }, disableBtn));
  } else {
    const enableBtn = el(
      "button",
      {
        type: "button",
        onclick: busyClick(async () => {
          const ok = await ensureReauth();
          if (!ok) {
            return;
          }
          const data = await apiPost<TOTPEnableResponse>("/api/auth/totp/enable");
          if (!data) {
            notify.error("Failed to start TOTP setup");
            return;
          }
          showTOTPSetup(content, data);
        }),
      },
      "Enable TOTP",
    );
    content.appendChild(el("div", { className: "sec-actions" }, enableBtn));
  }

  return sec;
}

function showTOTPSetup(container: HTMLElement, data: TOTPEnableResponse): void {
  const uriDisplay = el(
    "div",
    { className: "sec-totp-uri" },
    el("p", null, "Scan this URI with your authenticator app:"),
    el("code", null, data.uri),
  );

  const codeInput = el("input", {
    type: "text",
    inputmode: "numeric",
    pattern: "[0-9]*",
    autocomplete: "one-time-code",
    placeholder: "Enter 6-digit code",
    maxlength: "6",
  }) as HTMLInputElement;

  const feedback = el("div", { className: "sec-feedback", hidden: true });

  const confirmBtn = el(
    "button",
    {
      type: "button",
      onclick: busyClick(async () => {
        const code = codeInput.value.trim();
        if (!code) {
          return;
        }
        const r = await apiPostRaw<{ recovery_codes?: string[] }>("/api/auth/totp/confirm", {
          code,
        });
        if (r.ok && r.data) {
          notify.success("TOTP enabled");
          if (r.data.recovery_codes && r.data.recovery_codes.length > 0) {
            showRecoveryCodes(container, r.data.recovery_codes);
          } else {
            void renderSections(secDlg().querySelector(".dlg-body")!);
          }
        } else {
          showFeedback(feedback, r.error || "Invalid code", true);
        }
      }),
    },
    "Confirm",
  );

  container.replaceChildren(
    uriDisplay,
    el("div", { className: "sec-fields" }, el("label", null, "Verification code"), codeInput),
    feedback,
    el("div", { className: "sec-actions" }, confirmBtn),
  );
}

function showRecoveryCodes(container: HTMLElement, codes: string[]): void {
  const list = el("div", { className: "sec-recovery-codes" });
  for (const code of codes) {
    list.appendChild(el("code", null, code));
  }
  container.replaceChildren(
    el("p", null, "Save these recovery codes. They will not be shown again."),
    list,
    el(
      "div",
      { className: "sec-actions" },
      el(
        "button",
        {
          type: "button",
          onclick: () => {
            void renderSections(secDlg().querySelector(".dlg-body")!);
          },
        },
        "Done",
      ),
    ),
  );
}

// --- Passkeys ---

function buildPasskeysSection(passkeys: PasskeyItem[] | null): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "Passkeys"));

  const items = passkeys ?? [];

  const list = el("div", { className: "sec-list" });
  reconcile(list, items, {
    key: (pk) => String(pk.id),
    mount: (pk) => passkeyRow(pk),
  });
  if (items.length === 0) {
    sec.appendChild(el("p", { className: "muted" }, "No passkeys registered."));
  } else {
    sec.appendChild(list);
  }

  const addBtn = el(
    "button",
    {
      type: "button",
      onclick: busyClick(async () => {
        const ok = await ensureReauth();
        if (!ok) {
          return;
        }
        await registerPasskey();
      }),
    },
    "Add passkey",
  );
  sec.appendChild(el("div", { className: "sec-actions" }, addBtn));

  return sec;
}

function passkeyRow(pk: PasskeyItem): HTMLElement {
  const date = new Date(pk.created_at).toLocaleDateString();
  const nameEl = el(
    "span",
    {
      className: "sec-pk-name",
      onclick: () => renamePasskey(pk, nameEl),
    },
    pk.name || "Passkey",
  );

  const deleteBtn = el(
    "button",
    {
      type: "button",
      className: "close-btn ghost",
      "aria-label": "Delete passkey",
      onclick: busyClick(async () => {
        const ok = await ensureReauth();
        if (!ok) {
          return;
        }
        const r = await apiDeleteRaw<unknown>(`/api/auth/passkeys/${pk.id}`);
        if (r.ok) {
          notify.success("Passkey deleted");
          void sendWebAuthnSignals();
          await renderSections(secDlg().querySelector(".dlg-body")!);
        } else {
          notify.error(r.error ?? "Failed to delete passkey");
        }
      }),
    },
    icon("trash"),
  );

  return el(
    "div",
    { className: "sec-row" },
    nameEl,
    el("span", { className: "muted" }, date),
    deleteBtn,
  );
}

async function renamePasskey(pk: PasskeyItem, nameEl: HTMLElement): Promise<void> {
  const newName = await showInputDialog("Rename passkey:", {
    value: pk.name,
    maxlength: "64",
  });
  if (!newName || newName === pk.name) {
    return;
  }
  const ok = await apiPut<unknown>(`/api/auth/passkeys/${pk.id}`, { name: newName });
  if (ok !== null) {
    nameEl.textContent = newName;
    pk.name = newName;
  } else {
    notify.error("Failed to rename passkey");
  }
}

/** Decode base64url fields in WebAuthn creation options for the browser API. */
function prepareCreationOptions(pk: PublicKeyCredentialCreationOptions): void {
  if (typeof pk.challenge === "string") {
    pk.challenge = base64urlToBuffer(pk.challenge);
  }
  if (pk.user && typeof pk.user.id === "string") {
    pk.user.id = base64urlToBuffer(pk.user.id);
  }
  if (pk.excludeCredentials) {
    for (const cred of pk.excludeCredentials) {
      if (typeof cred.id === "string") {
        cred.id = base64urlToBuffer(cred.id);
      }
    }
  }
}

async function registerPasskey(): Promise<void> {
  try {
    const begin = await apiPost<{
      publicKey: PublicKeyCredentialCreationOptions;
      session_token?: string;
    }>("/api/auth/webauthn/register/begin");
    if (!begin) {
      notify.error("Failed to start passkey registration");
      return;
    }

    const sessionToken = begin.session_token ?? "";

    prepareCreationOptions(begin.publicKey);

    const credential = await navigator.credentials.create({ publicKey: begin.publicKey });
    if (!credential) {
      notify.error("Passkey creation cancelled");
      return;
    }

    const attestation = credential as PublicKeyCredential;
    const response = attestation.response as AuthenticatorAttestationResponse;

    // Check credProps extension: warn if the credential is not discoverable.
    const extensions = attestation.getClientExtensionResults() as { credProps?: { rk?: boolean } };
    if (extensions.credProps?.rk === false) {
      notify.error(
        "Your authenticator created a non-discoverable credential. Passwordless login may not work with this passkey.",
      );
    }

    // Custom header required (X-WebAuthn-Session); can't go through apiPost.
    const finishRes = await fetch("/api/auth/webauthn/register/finish", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-WebAuthn-Session": sessionToken,
      },
      body: JSON.stringify({
        id: attestation.id,
        rawId: bufferToBase64url(attestation.rawId),
        type: attestation.type,
        response: {
          attestationObject: bufferToBase64url(response.attestationObject),
          clientDataJSON: bufferToBase64url(response.clientDataJSON),
        },
      }),
      signal: AbortSignal.timeout(10_000),
    });

    if (finishRes.ok) {
      notify.success("Passkey registered");
      void sendWebAuthnSignals();
      await renderSections(secDlg().querySelector(".dlg-body")!);
    } else {
      const data = (await finishRes.json().catch(() => ({}))) as { error?: string };
      notify.error(data.error ?? "Failed to register passkey");
    }
  } catch (e: unknown) {
    if (e instanceof DOMException && e.name === "AbortError") {
      return;
    }
    notify.error("Passkey registration failed");
  }
}

// --- API Keys ---

function buildAPIKeysSection(apikeys: APIKeyItem[] | null): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "API Keys"));

  const keys = apikeys ?? [];

  const list = el("div", { className: "sec-list" });
  reconcile(list, keys, {
    key: (k) => String(k.id),
    mount: (k) => apiKeyRow(k),
  });
  if (keys.length === 0) {
    sec.appendChild(el("p", { className: "muted" }, "No API keys."));
  } else {
    sec.appendChild(list);
  }

  const genBtn = el(
    "button",
    {
      type: "button",
      onclick: busyClick(async () => {
        const ok = await ensureReauth();
        if (!ok) {
          return;
        }
        const label = await showInputDialog("Label for the new API key:", {
          maxlength: "64",
        });
        if (label === null) {
          return;
        }
        const r = await apiPostRaw<APIKeyCreateResponse>("/api/auth/apikeys", { label });
        if (r.ok && r.data) {
          showNewAPIKey(sec, r.data.key);
        } else {
          notify.error(r.error ?? "Failed to generate API key");
        }
      }),
    },
    "Generate API key",
  );
  sec.appendChild(el("div", { className: "sec-actions" }, genBtn));

  return sec;
}

function apiKeyRow(key: APIKeyItem): HTMLElement {
  const display = `${key.key_prefix}\u2026${key.key_suffix}`;
  const date = new Date(key.created_at).toLocaleDateString();

  const revokeBtn = el(
    "button",
    {
      type: "button",
      className: "close-btn ghost",
      "aria-label": "Revoke API key",
      onclick: busyClick(async () => {
        const ok = await ensureReauth();
        if (!ok) {
          return;
        }
        const deleted = await apiDelete(`/api/auth/apikeys/${key.id}`);
        if (deleted) {
          notify.success("API key revoked");
          await renderSections(secDlg().querySelector(".dlg-body")!);
        } else {
          notify.error("Failed to revoke API key");
        }
      }),
    },
    icon("trash"),
  );

  return el(
    "div",
    { className: "sec-row" },
    el("code", null, display),
    key.label ? el("span", null, key.label) : el("span", { className: "muted" }, "No label"),
    el("span", { className: "muted" }, date),
    revokeBtn,
  );
}

function showNewAPIKey(container: HTMLElement, key: string): void {
  const keyDisplay = el(
    "div",
    { className: "sec-new-key" },
    el("p", null, "Copy this key now. It will not be shown again."),
    el(
      "div",
      { className: "sec-key-value" },
      el("code", null, key),
      el(
        "button",
        {
          type: "button",
          className: "ghost",
          onclick: () => {
            navigator.clipboard.writeText(key).then(
              () => {
                notify.success("Copied to clipboard");
              },
              () => {
                notify.error("Failed to copy");
              },
            );
          },
        },
        "Copy",
      ),
    ),
  );

  const doneBtn = el(
    "button",
    {
      type: "button",
      onclick: () => {
        void renderSections(secDlg().querySelector(".dlg-body")!);
      },
    },
    "Done",
  );

  // Insert after the API Keys heading.
  const heading = container.querySelector("h3");
  const insertPoint = heading ? heading.nextSibling : null;
  if (insertPoint) {
    container.insertBefore(keyDisplay, insertPoint);
    container.insertBefore(
      el("div", { className: "sec-actions" }, doneBtn),
      keyDisplay.nextSibling,
    );
  } else {
    container.appendChild(keyDisplay);
    container.appendChild(el("div", { className: "sec-actions" }, doneBtn));
  }
}

// --- Utilities ---

/** Show a custom input dialog (replaces native prompt() for styled, validated input). */
function showInputDialog(message: string, attrs?: Record<string, string>): Promise<string | null> {
  return new Promise((resolve) => {
    const dlg = dialog("confirmDialog");
    if (dlg.open) {
      dlg.close();
    }
    let resolved = false;
    const settle = (val: string | null): void => {
      if (resolved) {
        return;
      }
      resolved = true;
      closeDialog(dlg);
      resolve(val);
    };

    const header = dialogHead("Input", () => {
      settle(null);
    });

    const inp = el("input", {
      type: "text",
      autocomplete: "off",
      ...attrs,
    }) as HTMLInputElement;

    const body = el("div", { className: "dlg-body" }, el("p", null, message), inp);

    const footer = el(
      "div",
      { className: "dlg-foot" },
      el(
        "button",
        {
          type: "button",
          onclick: () => {
            const val = inp.value.trim();
            settle(val || null);
          },
        },
        "OK",
      ),
      el(
        "button",
        {
          type: "button",
          className: "ghost",
          onclick: () => {
            settle(null);
          },
        },
        "Cancel",
      ),
    );

    dlg.replaceChildren(header, body, footer);
    dlg.showModal();
    onBackdropClose(dlg, () => {
      settle(null);
    });
    dlg.addEventListener(
      "close",
      () => {
        settle(null);
      },
      { once: true },
    );
    inp.focus();
  });
}

function showFeedback(feedbackEl: HTMLElement, msg: string, isError: boolean): void {
  feedbackEl.textContent = msg;
  feedbackEl.hidden = false;
  feedbackEl.className = isError ? "sec-feedback sec-feedback-err" : "sec-feedback sec-feedback-ok";
}
