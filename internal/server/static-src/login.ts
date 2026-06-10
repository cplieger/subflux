// login.ts — Standalone login page logic. Only imports leaf modules.

import { apiGetTyped, apiPost, apiPostRaw } from "./api-client.js";
import type { ApiResult } from "./api-client.js";
import { decodeSetupStatus } from "./wire/decoders.gen.js";
import { registerCleanup } from "@cplieger/actions";
import { $, show, showPage, showError, hideError } from "./dom-core.js";
import { startConfigWizard } from "./wizard.js";
import { base64urlToBuffer, bufferToBase64url, sendWebAuthnSignals } from "./webauthn-utils.js";
import { hasCode, ErrorCode } from "./error_codes.js";

// --- Inline interfaces for API response shapes ---

interface LoginResponse {
  redirect?: string;
  error?: string;
}

interface WebAuthnOptions {
  publicKey: PublicKeyCredentialRequestOptions;
  session_token?: string;
}

// --- State ---

let conditionalAbort: AbortController | null = null;
let conditionalRetryTimer: ReturnType<typeof setTimeout> | null = null;
let webauthnSessionToken = "";
let conditionalUIAttempts = 0;

// Drain in-flight conditional WebAuthn ceremony + clear any pending retry
// timer on page unload. Without this, a retry fires into a torn-down DOM
// after a failed conditional UI attempt + slow navigation.
registerCleanup(() => {
  conditionalAbort?.abort();
  conditionalAbort = null;
  if (conditionalRetryTimer !== null) {
    clearTimeout(conditionalRetryTimer);
    conditionalRetryTimer = null;
  }
});

// --- Initialization ---

async function init(): Promise<void> {
  // Link-on-login: an OIDC login that collided with an existing local account
  // redirects here with ?oidc_link=<token>; prove the password to link it.
  const linkToken = new URLSearchParams(window.location.search).get("oidc_link");
  if (linkToken) {
    showPage("loginPage");
    wireOIDCLinkForm(linkToken);
    return;
  }

  const data = await apiGetTyped("/api/auth/setup", decodeSetupStatus);
  if (!data) {
    showError("loginError", "Failed to initialize");
    showPage("loginPage");
    return;
  }

  if (data.setup_required) {
    showPage("setupPage");
    wireSetupForm();
    return;
  }

  if (!data.config_valid) {
    showPage("loginPage");
    wireLoginForm(true);
    return;
  }

  showPage("loginPage");
  wireLoginForm(false);
  await detectAuthMethods();
  void startConditionalUI();
}

// --- Auth method detection ---

async function detectAuthMethods(): Promise<void> {
  try {
    const res = await fetch("/api/auth/oidc", {
      method: "HEAD",
      redirect: "manual",
      signal: AbortSignal.timeout(10_000),
    });
    if (res.status === 200 || res.type === "opaqueredirect" || res.status === 302) {
      show($("oidcBtn"));
      show($("authDivider"));
    }
  } catch {
    /* OIDC not available */
  }

  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime feature detection
  if (window.PublicKeyCredential) {
    show($("passkeyBtn"));
    show($("authDivider"));
  }

  $("oidcBtn")?.addEventListener("click", () => {
    window.location.href = "/api/auth/oidc";
  });
  $("passkeyBtn")?.addEventListener("click", () => {
    void passkeyLogin();
  });
}

// --- Conditional UI (passkey autofill) ---

async function startConditionalUI(): Promise<void> {
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- runtime feature detection
  if (!window.PublicKeyCredential) {
    return;
  }
  try {
    const available = await PublicKeyCredential.isConditionalMediationAvailable();
    if (!available) {
      return;
    }
  } catch {
    return;
  }

  try {
    const options = await apiPost<WebAuthnOptions>(
      "/api/auth/webauthn/login/begin?mediation=conditional",
    );
    if (!options) {
      return;
    }
    webauthnSessionToken = options.session_token ?? "";
    const pk = options.publicKey;
    pk.challenge = base64urlToBuffer(pk.challenge as unknown as string);
    if (pk.allowCredentials) {
      for (const cred of pk.allowCredentials) {
        cred.id = base64urlToBuffer(cred.id as unknown as string);
      }
    }
    conditionalAbort = new AbortController();
    const credential = (await navigator.credentials.get({
      publicKey: pk,
      mediation: "conditional" as CredentialMediationRequirement,
      signal: conditionalAbort.signal,
    })) as PublicKeyCredential | null;
    if (!credential) {
      return;
    }
    conditionalUIAttempts = 0;
    await finishWebAuthnLogin(credential, webauthnSessionToken);
  } catch (e: unknown) {
    if (e instanceof DOMException && e.name === "AbortError") {
      return;
    }
    conditionalUIAttempts++;
    if (conditionalUIAttempts >= 3) {
      return;
    }
    const delay = Math.min(1000 * 2 ** conditionalUIAttempts, 30_000);
    conditionalRetryTimer = setTimeout(startConditionalUI, delay);
  }
}

// --- Passkey login ---

async function passkeyLogin(): Promise<void> {
  if (conditionalAbort) {
    conditionalAbort.abort();
    conditionalAbort = null;
  }
  try {
    const options = await apiPost<WebAuthnOptions>("/api/auth/webauthn/login/begin");
    if (!options) {
      showError("loginError", "Failed to start passkey login");
      return;
    }
    webauthnSessionToken = options.session_token ?? "";
    const pk = options.publicKey;
    pk.challenge = base64urlToBuffer(pk.challenge as unknown as string);
    if (pk.allowCredentials) {
      for (const cred of pk.allowCredentials) {
        cred.id = base64urlToBuffer(cred.id as unknown as string);
      }
    }
    const credential = (await navigator.credentials.get({
      publicKey: pk,
    })) as PublicKeyCredential | null;
    if (!credential) {
      return;
    }
    await finishWebAuthnLogin(credential, webauthnSessionToken);
  } catch (e: unknown) {
    if (e instanceof DOMException && e.name === "AbortError") {
      return;
    }
    const msg = e instanceof Error ? e.message : String(e);
    showError("loginError", "Passkey error: " + msg);
  }
}

async function finishWebAuthnLogin(
  credential: PublicKeyCredential,
  sessionToken: string,
): Promise<void> {
  const response = credential.response as AuthenticatorAssertionResponse;
  const body = {
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
  const res = await fetch("/api/auth/webauthn/login/finish", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-WebAuthn-Session": sessionToken },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(10_000),
  });
  if (!res.ok) {
    const data = (await res.json()) as { error?: string; signal?: string; code?: string };
    if (data.signal === "unknown_credential") {
      showError(
        "loginError",
        "This passkey is not recognized. Please delete it from your authenticator and try again.",
      );
      return;
    }
    if (
      data.code === ErrorCode.WebAuthnSessionInvalid ||
      data.error === "invalid or expired session"
    ) {
      void startConditionalUI();
      return;
    }
    if (data.code === ErrorCode.WebAuthnAssertionFailed) {
      showError("loginError", "Passkey verification failed. Please try again.");
      return;
    }
    showError("loginError", data.error ?? "Passkey authentication failed");
    return;
  }
  const data = (await res.json()) as LoginResponse;
  void sendWebAuthnSignals();
  window.location.href = data.redirect ?? "/";
}

// --- Login error message helper ---

const LOGIN_ERROR_MAP: readonly {
  code: ErrorCode;
  msg: string | ((res: ApiResult<LoginResponse>) => string);
}[] = [
  {
    code: ErrorCode.RateLimited,
    msg: (res) => {
      const retry = res.headers?.get("Retry-After");
      if (retry) {
        return `Too many login attempts; try again in ${retry} seconds.`;
      }
      return "Too many login attempts. Please wait and try again.";
    },
  },
  { code: ErrorCode.AuthAccountDisabled, msg: "This account has been disabled." },
  {
    code: ErrorCode.AuthAccountNotSetup,
    msg: "Account setup is incomplete. Contact your administrator.",
  },
];

function loginErrorMessage(res: ApiResult<LoginResponse>): string {
  const entry = LOGIN_ERROR_MAP.find((e) => hasCode(res, e.code));
  if (entry) {
    return typeof entry.msg === "function" ? entry.msg(res) : entry.msg;
  }
  return res.error ?? "Invalid credentials";
}

// --- Login form ---

function wireLoginForm(resumeSetup: boolean): void {
  const form = $("loginForm") as HTMLFormElement | null;
  if (!form) {
    return;
  }
  // eslint-disable-next-line @typescript-eslint/no-misused-promises -- event handler
  form.addEventListener("submit", async (e: Event) => {
    e.preventDefault();
    hideError("loginError");
    if (conditionalAbort) {
      conditionalAbort.abort();
      conditionalAbort = null;
    }
    const formData = new FormData(form);
    const username = (formData.get("username") as string) || "";
    const password = (formData.get("password") as string) || "";
    const btn = $("loginBtn") as HTMLButtonElement | null;
    if (btn) {
      btn.disabled = true;
      btn.setAttribute("aria-busy", "true");
    }
    try {
      const res = await apiPostRaw<LoginResponse>("/api/auth/login", { username, password });
      if (!res.ok) {
        const errEl = $("loginError");
        if (errEl && hasCode(res, ErrorCode.RateLimited)) {
          errEl.dataset["level"] = "warn";
        } else if (errEl) {
          delete errEl.dataset["level"];
        }
        showError("loginError", loginErrorMessage(res));
        return;
      }
      const data = res.data ?? {};
      if (resumeSetup) {
        await startConfigWizard();
        return;
      }
      window.location.href = data.redirect ?? "/";
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.removeAttribute("aria-busy");
      }
    }
  });
}

// --- OIDC link-on-login ---

function wireOIDCLinkForm(linkToken: string): void {
  const form = $("loginForm") as HTMLFormElement | null;
  if (!form) {
    return;
  }
  // The token identifies the account; the username field is irrelevant here.
  const userInput = form.querySelector<HTMLInputElement>('input[name="username"]');
  if (userInput) {
    userInput.removeAttribute("required");
    ((userInput.closest("label") as HTMLElement | null) ?? userInput).hidden = true;
  }
  showError(
    "loginError",
    "Enter your password to switch this account to single sign-on. You'll no longer be able to sign in with a password or passkey.",
  );
  const hint = $("loginError");
  if (hint) {
    hint.dataset["level"] = "info";
  }
  // eslint-disable-next-line @typescript-eslint/no-misused-promises -- event handler
  form.addEventListener("submit", async (e: Event) => {
    e.preventDefault();
    hideError("loginError");
    const password = (new FormData(form).get("password") as string) || "";
    const res = await apiPostRaw<LoginResponse>("/api/auth/oidc/link", {
      link_token: linkToken,
      password,
    });
    if (!res.ok) {
      showError("loginError", res.data?.error ?? res.error ?? "Failed to link account");
      return;
    }
    window.location.href = res.data?.redirect ?? "/";
  });
}

// --- Setup form ---

function wireSetupForm(): void {
  const form = $("setupForm") as HTMLFormElement | null;
  if (!form) {
    return;
  }
  // eslint-disable-next-line @typescript-eslint/no-misused-promises -- event handler
  form.addEventListener("submit", async (e: Event) => {
    e.preventDefault();

    hideError("setupError");
    const formData = new FormData(form);
    const username = (formData.get("username") as string) || "";
    const password = (formData.get("password") as string) || "";
    const res = await apiPostRaw<{ error?: string }>("/api/auth/setup", { username, password });
    if (!res.ok) {
      showError("setupError", res.data?.error ?? res.error ?? "Setup failed");
      return;
    }
    await startConfigWizard();
  });
}

// --- Entry point ---

void init();
