// security.ts — Security popup: password, passkeys, API keys.

import * as bus from "./bus.js";
import * as notify from "./notify.js";
import { el, icon, dialog, dialogHead, confirm, errDiv } from "./dom.js";
import { createDialog } from "@cplieger/ui-primitives/dialog";
import { ask, type AskInput } from "@cplieger/ui-primitives/ask";
import { reconcile } from "@cplieger/reactive";
import { FOCUS_KEY, trackFocus, captureReaderState } from "./focus-restore.js";
import {
  changePasswordRaw,
  deletePasskeyRaw,
  generateAPIKeyRaw,
  listAPIKeys,
  listPasskeys,
  me,
  oidcUnlinkRaw,
  renamePasskey as renamePasskeyRequest,
  revokeAPIKey,
  updateProfileRaw,
  PATH_OIDC_REDIRECT,
} from "./wire/client.gen.js";
import type { APIKeyInfo, PasskeyInfo } from "./wire/types.gen.js";
import { sendWebAuthnSignals } from "./webauthn-utils.js";
import {
  probeAvailability,
  registerPasskey,
  unavailableSentence,
  type Availability,
} from "./webauthn-ceremony.js";
import type { MeResponse } from "./api-types.js";

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
  // createDialog bundles the three things this site used to hand-wire:
  // showModal() (open), drag-safe backdrop dismiss, and Escape handling — all
  // routed through the shared fade-out close (dialog.is-leaving), so the header
  // close button, a backdrop click, and Escape now all animate identically.
  // The controller is disposed on the dialog's `close` event so its listeners
  // don't accumulate across reopens of the reused #securityDialog element.
  const ctrl = createDialog(dlg);
  dlg.addEventListener(
    "close",
    () => {
      ctrl.dispose();
    },
    { once: true },
  );
  const closeFn = (): void => {
    ctrl.close();
  };
  const header = dialogHead("Security", closeFn);

  const body = el("div", { className: "dlg-body" });
  body.appendChild(el("p", { className: "muted" }, "Loading\u2026"));
  trackFocus(body);

  dlg.replaceChildren(header, body);
  ctrl.open();

  await renderSections(body);
}

async function renderSections(body: HTMLElement): Promise<void> {
  const [user, passkeys, oidcProbe, availability] = await Promise.all([
    me(),
    listPasskeys(),
    detectOIDC(),
    probeAvailability(),
  ]);

  if (!user) {
    body.replaceChildren(errDiv("Could not load your account. Close the dialog and try again."));
    return;
  }

  const frag = document.createDocumentFragment();

  // Identity first: the display name is not a credential, so an SSO-governed
  // account gets it too.
  frag.appendChild(buildProfileSection(user));
  // Local-credential management is only for accounts that have a password.
  // SSO-governed (password-less) accounts are managed at the identity provider.
  if (user.has_password) {
    frag.appendChild(buildPasswordSection());
    frag.appendChild(buildPasskeysSection(passkeys, availability));
  }
  // API keys are admin-only (bearer credentials carrying the owner's role).
  if (user.role === "admin") {
    const apikeys = await listAPIKeys();
    frag.appendChild(buildAPIKeysSection(apikeys));
  }
  const oidcSection = buildOIDCSection(user, oidcProbe);
  if (oidcSection) {
    frag.appendChild(oidcSection);
  }

  // The installed tree must BE the tree these handlers closed over: a reusing
  // reconciler (`patch`) copies a fresh node's handler properties into the
  // element already in the document, leaving every handler holding nodes that
  // were never inserted.
  const restore = captureReaderState(body);
  body.replaceChildren(frag);
  restore();
}

// --- Display name ---

function buildProfileSection(user: MeResponse): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "Display Name"));

  const nameInput = el("input", {
    type: "text",
    id: "sec-display-name",
    autocomplete: "nickname",
    maxlength: "128",
    placeholder: user.username,
    value: user.display_name,
  }) as HTMLInputElement;

  const feedback = el("div");

  const submitBtn = el(
    "button",
    {
      type: "button",
      [FOCUS_KEY]: "profile-save",
      onclick: busyClick(async () => {
        const r = await updateProfileRaw({ display_name: nameInput.value });
        if (!r.ok) {
          showFeedback(feedback, r.error ?? "Failed to save display name", true);
          return;
        }
        showFeedback(
          feedback,
          nameInput.value.trim() ? "Display name saved" : "Display name cleared",
          false,
        );
        // Tell the credential manager the account label changed, so a stored
        // passkey stops offering the name it was registered under. Awaited:
        // the signal needs the page alive to reach the provider.
        await sendWebAuthnSignals();
      }),
    },
    "Save",
  );

  sec.appendChild(
    el(
      "div",
      { className: "sec-fields" },
      el("label", { htmlFor: "sec-display-name" }, "Shown instead of your username"),
      nameInput,
    ),
  );
  sec.appendChild(feedback);
  sec.appendChild(el("div", { className: "sec-actions" }, submitBtn));
  return sec;
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

  const feedback = el("div");

  const submitBtn = el(
    "button",
    {
      type: "button",
      [FOCUS_KEY]: "password-submit",
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
        const r = await changePasswordRaw({
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

// Fails CLOSED on every unavailable reason, probe_failed included: the cost of
// optimism here is a password typed for a ceremony that cannot run.
function buildPasskeysSection(
  passkeys: PasskeyInfo[] | null,
  availability: Availability,
): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "Passkeys"));

  if (passkeys === null) {
    sec.appendChild(errDiv("Could not load passkeys."));
  } else if (passkeys.length === 0) {
    sec.appendChild(el("p", { className: "muted" }, "No passkeys registered."));
  } else {
    const list = el("div", { className: "sec-list" });
    reconcile(list, passkeys, {
      key: (pk) => String(pk.id),
      mount: (pk) => passkeyRow(pk),
    });
    sec.appendChild(list);
  }

  if (!availability.available) {
    sec.appendChild(
      el("p", { className: "muted sec-pk-unavailable" }, unavailableSentence(availability)),
    );
  }
  const addBtn = el(
    "button",
    {
      type: "button",
      disabled: !availability.available,
      [FOCUS_KEY]: "pk-add",
      onclick: busyClick(async () => {
        const password = await promptTrimmed("Enter your password to add a passkey:", {
          type: "password",
          autocomplete: "current-password",
        });
        if (password === null) {
          return;
        }
        await addPasskey(password);
      }),
    },
    "Add passkey",
  );
  sec.appendChild(el("div", { className: "sec-actions" }, addBtn));

  return sec;
}

async function addPasskey(password: string): Promise<void> {
  const outcome = await registerPasskey(password);
  switch (outcome.kind) {
    case "registered":
      notify.success("Passkey registered");
      await renderSections(secDlgBody());
      return;
    case "cancelled":
      return;
    case "duplicate":
      notify.error(
        "This device already has a passkey for subflux. Use the one you have, or add a passkey from another device.",
      );
      return;
    case "timeout":
      notify.error(
        "Passkey registration timed out. It may have completed. Reload and check your passkey list before trying again.",
      );
      return;
    case "not-discoverable":
    case "failed":
      notify.error(outcome.message);
  }
}

function passkeyRow(pk: PasskeyInfo): HTMLElement {
  const date = new Date(pk.created_at).toLocaleDateString();
  // A real button (was a click-only <span>): rename must be reachable by
  // keyboard and announced as an action, not as plain text.
  const nameEl = el(
    "button",
    {
      type: "button",
      className: "sec-pk-name sec-pk-rename",
      [FOCUS_KEY]: `pk-rename-${pk.id}`,
      "aria-label": `Rename passkey ${pk.name || "Passkey"}`,
      onclick: () => renamePasskey(pk),
    },
    pk.name || "Passkey",
  );

  const deleteBtn = el(
    "button",
    {
      type: "button",
      className: "close-btn ghost",
      [FOCUS_KEY]: `pk-delete-${pk.id}`,
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
        const r = await deletePasskeyRaw(pk.id);
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

async function renamePasskey(pk: PasskeyInfo): Promise<void> {
  const newName = await promptTrimmed("Rename passkey:", {
    initialValue: pk.name,
    maxLength: 64,
  });
  if (!newName || newName === pk.name) {
    return;
  }
  const ok = await renamePasskeyRequest(pk.id, { name: newName });
  if (ok) {
    await renderSections(secDlgBody());
  } else {
    notify.error("Failed to rename passkey");
  }
}

// --- API Keys ---

function buildAPIKeysSection(apikeys: APIKeyInfo[] | null): HTMLElement {
  const sec = el("div", { className: "sec-section" });
  sec.appendChild(el("h3", null, "API Keys"));

  if (apikeys === null) {
    sec.appendChild(errDiv("Could not load API keys."));
  } else if (apikeys.length === 0) {
    sec.appendChild(el("p", { className: "muted" }, "No API keys."));
  } else {
    const list = el("div", { className: "sec-list" });
    reconcile(list, apikeys, {
      key: (k) => String(k.id),
      mount: (k) => apiKeyRow(k),
    });
    sec.appendChild(list);
  }

  const genBtn = el(
    "button",
    {
      type: "button",
      [FOCUS_KEY]: "apikey-generate",
      onclick: busyClick(async () => {
        const label = await promptTrimmed("Label for the new API key:", {
          maxLength: 64,
        });
        if (label === null) {
          return;
        }
        const r = await generateAPIKeyRaw({ label });
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

function apiKeyRow(key: APIKeyInfo): HTMLElement {
  const display = `${key.key_prefix}\u2026${key.key_suffix}`;
  const date = new Date(key.created_at).toLocaleDateString();

  const revokeBtn = el(
    "button",
    {
      type: "button",
      className: "close-btn ghost",
      [FOCUS_KEY]: `apikey-revoke-${key.id}`,
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
        const deleted = await revokeAPIKey(key.id);
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

  // The key is shown exactly once and is inserted ABOVE the current focus:
  // announce it and move focus onto it so it cannot be missed.
  keyDisplay.setAttribute("role", "status");
  keyDisplay.setAttribute("aria-live", "polite");
  keyDisplay.tabIndex = -1;
  keyDisplay.focus();
}

// --- Single Sign-On (OIDC) ---

type OIDCProbe = "available" | "absent" | "failed";

/** Probe whether an OIDC provider is configured (mirrors the login page). A
 *  probe that could not be answered reports `failed`, never `absent`. */
async function detectOIDC(): Promise<OIDCProbe> {
  try {
    const res = await fetch(PATH_OIDC_REDIRECT, {
      method: "HEAD",
      redirect: "manual",
      signal: AbortSignal.timeout(10_000),
    });
    return res.status === 200 || res.type === "opaqueredirect" || res.status === 302
      ? "available"
      : "absent";
  } catch {
    return "failed";
  }
}

/** Build the SSO section. Returns null only when the probe answered that no
 *  provider is configured and the account is not linked to one. */
function buildOIDCSection(user: MeResponse, probe: OIDCProbe): HTMLElement | null {
  const linked = user.oidc_linked;
  if (!linked && probe === "absent") {
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

  // A linked account renders from `oidc_linked` and disconnects without the
  // probe, so only an unlinked one is left with an unanswered question.
  if (!linked && probe === "failed") {
    sec.appendChild(errDiv("Could not check single sign-on availability."));
  }

  if (linked) {
    const unlinkBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        [FOCUS_KEY]: "oidc-disconnect",
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
          const r = await oidcUnlinkRaw();
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
  } else if (user.can_link_oidc) {
    const connectBtn = el(
      "button",
      {
        type: "button",
        [FOCUS_KEY]: "oidc-connect",
        onclick: () => {
          window.location.href = PATH_OIDC_REDIRECT;
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

/** Styled input prompt over @cplieger/ui-primitives' prompt primitive (its own
 *  reused <dialog class="uip-ask uip-ask--input">, Enter submits,
 *  Escape/backdrop/Cancel settle null), preserving subflux's input semantics:
 *  the value is trimmed and an empty submission resolves null, exactly like
 *  the old hand-rolled showInputDialog. The "Input" title matches the old
 *  dialog head. */
async function promptTrimmed(message: string, input?: AskInput): Promise<string | null> {
  const raw = await ask(message, { title: "Input", input: input ?? {} });
  if (raw === null) {
    return null;
  }
  const val = raw.trim();
  return val === "" ? null : val;
}

function showFeedback(host: HTMLElement, msg: string, isError: boolean): void {
  // The host stays classless and the MESSAGE carries `.sec-feedback`: that class
  // sets padding and margin-block (15-security.css), so a permanently present
  // host wearing it would render dead vertical space on every open.
  //
  // Announce the outcome: inline feedback in this dialog is otherwise invisible
  // to screen readers. role/aria-live vary per call, so they cannot be static,
  // and they go on the host before the swap, so the live region is established
  // before the mutation that is announced.
  host.setAttribute("role", isError ? "alert" : "status");
  host.setAttribute("aria-live", isError ? "assertive" : "polite");
  host.replaceChildren(
    el(
      "div",
      { className: isError ? "sec-feedback sec-feedback-err" : "sec-feedback sec-feedback-ok" },
      msg,
    ),
  );
}
