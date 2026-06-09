// security.ts — Security popup: password, passkeys, API keys.

import * as bus from "./bus.js";
import * as notify from "./notify.js";
import {
  el,
  icon,
  dialog,
  closeDialog,
  dialogHead,
  onBackdropClose,
  patch,
  confirm,
} from "./dom.js";
import { reconcile } from "@cplieger/reactive";
import {
  apiGet,
  apiGetTyped,
  apiPost,
  apiPostRaw,
  apiPut,
  apiPutRaw,
  apiDelete,
  apiDeleteRaw,
} from "./api-client.js";
import { base64urlToBuffer, bufferToBase64url, sendWebAuthnSignals } from "./webauthn-utils.js";
import type { MeResponse, KeyGenerated } from "./api-types.js";
import { decodeMeResponse } from "./wire/decoders.gen.js";

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
// eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- dialog always has .dlg-body
const secDlgBody = (): HTMLElement => secDlg().querySelector<HTMLElement>(".dlg-body")!;

export function initSecurity(): void {
  bus.on(bus.BusEvent.OpenSecurity, () => {
    void openSecurity();
  });
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
  const [me, passkeys, oidcAvailable] = await Promise.all([
    apiGetTyped("/api/auth/me", decodeMeResponse),
    apiGet<PasskeyItem[]>("/api/auth/passkeys"),
    detectOIDC(),
  ]);

  const frag = document.createDocumentFragment();

  // Local-credential management is only for accounts that have a password.
  // SSO-governed (password-less) accounts are managed at the identity provider.
  if (me?.has_password) {
    frag.appendChild(buildPasswordSection());
    frag.appendChild(buildPasskeysSection(passkeys));
  }
  // API keys are admin-only (bearer credentials carrying the owner's role).
  if (me?.role === "admin") {
    const apikeys = await apiGet<APIKeyItem[]>("/api/auth/apikeys");
    frag.appendChild(buildAPIKeysSection(apikeys));
  }
  const oidcSection = buildOIDCSection(me, oidcAvailable);
  if (oidcSection) {
    frag.appendChild(oidcSection);
  }

  patch(body, frag);
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
        const r = await apiPutRaw<unknown>("/api/auth/password", {
          current_password: cur,
          new_password: nw,
        });
        if (r.ok) {
          showFeedback(feedback, "Password changed", false);
          currentPw.value = "";
          newPw.value = "";
        } else {
          showFeedback(feedback, r.error ?? "Failed to change password", true);
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
        const password = await showInputDialog("Enter your password to add a passkey:", {
          type: "password",
          autocomplete: "current-password",
        });
        if (password === null) {
          return;
        }
        await registerPasskey(password);
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
        if (
          !(await confirm(
            "Delete passkey",
            "This passkey can no longer be used to sign in.",
            "Delete",
          ))
        ) {
          return;
        }
        const r = await apiDeleteRaw<unknown>(`/api/auth/passkeys/${pk.id}`);
        if (r.ok) {
          notify.success("Passkey deleted");
          void sendWebAuthnSignals();
          await renderSections(secDlgBody());
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
  if (typeof pk.user.id === "string") {
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

async function registerPasskey(password: string): Promise<void> {
  try {
    const begin = await apiPost<{
      publicKey: PublicKeyCredentialCreationOptions;
      session_token?: string;
    }>("/api/auth/webauthn/register/begin", { password });
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
      await renderSections(secDlgBody());
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
        const label = await showInputDialog("Label for the new API key:", {
          maxlength: "64",
        });
        if (label === null) {
          return;
        }
        const r = await apiPostRaw<KeyGenerated>("/api/auth/apikeys", { label });
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
        if (
          !(await confirm(
            "Revoke API key",
            "Applications using this key will stop working.",
            "Revoke",
          ))
        ) {
          return;
        }
        const deleted = await apiDelete(`/api/auth/apikeys/${key.id}`);
        if (deleted) {
          notify.success("API key revoked");
          await renderSections(secDlgBody());
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
        void renderSections(secDlgBody());
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

// --- Single Sign-On (OIDC) ---

/** Probe whether an OIDC provider is configured (mirrors the login page). */
async function detectOIDC(): Promise<boolean> {
  try {
    const res = await fetch("/api/auth/oidc", {
      method: "HEAD",
      redirect: "manual",
      signal: AbortSignal.timeout(10_000),
    });
    return res.status === 200 || res.type === "opaqueredirect" || res.status === 302;
  } catch {
    return false;
  }
}

/** Build the SSO section. Returns null when OIDC is neither linked nor available. */
function buildOIDCSection(me: MeResponse | null, available: boolean): HTMLElement | null {
  const linked = me?.oidc_linked ?? false;
  if (!linked && !available) {
    return null;
  }

  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "Single Sign-On"));
  sec.appendChild(
    el(
      "div",
      { className: "sec-status" },
      el("span", null, "Status: "),
      el(
        "span",
        { className: "badge", "data-status": linked ? "ok" : "" },
        linked ? "Connected" : "Not connected",
      ),
    ),
  );

  if (linked) {
    const unlinkBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: busyClick(async () => {
          if (
            !(await confirm(
              "Disconnect single sign-on",
              "You'll no longer be able to sign in through your identity provider.",
              "Disconnect",
            ))
          ) {
            return;
          }
          const r = await apiDeleteRaw<unknown>("/api/auth/oidc/link");
          if (r.ok) {
            notify.success("Single sign-on disconnected");
            await renderSections(secDlgBody());
          } else {
            notify.error(r.error ?? "Failed to disconnect single sign-on");
          }
        }),
      },
      "Disconnect",
    );
    sec.appendChild(el("div", { className: "sec-actions" }, unlinkBtn));
  } else if (me?.can_link_oidc) {
    const connectBtn = el(
      "button",
      {
        type: "button",
        onclick: () => {
          window.location.href = "/api/auth/oidc";
        },
      },
      "Connect",
    );
    sec.appendChild(el("div", { className: "sec-actions" }, connectBtn));
  } else {
    sec.appendChild(
      el(
        "p",
        { className: "muted" },
        "Create another admin before switching this account to single sign-on.",
      ),
    );
  }

  return sec;
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
      "aria-label": message,
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
