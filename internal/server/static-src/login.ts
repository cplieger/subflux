// login.ts — Standalone login page logic. Only imports leaf modules.
import type { ApiResult } from "./api-client.js";
import {
  authSetupCreateRaw,
  authSetupStatus,
  loginRaw,
  me,
  oidcLinkRaw,
  PATH_OIDC_REDIRECT,
} from "./wire/client.gen.js";
import type { LoginSuccess, SetupStatus } from "./wire/types.gen.js";
import { registerCleanup } from "@cplieger/actions";
import { initTooltips } from "@cplieger/ui-primitives/tooltip";
import { $, show, showPage, showError, hideError } from "./dom-core.js";
import { storePasswordCredential } from "./password-credential.js";
import type { WizardEntry } from "./wizard.js";
import { postLoginDestination } from "./post-login.js";
import { SETUP_PATH } from "./constants.js";
import { hasCode, ErrorCode } from "./error_codes.js";
import {
  authenticateWithPasskey,
  probeAvailability,
  type Availability,
  type LoginOutcome,
} from "./webauthn-ceremony.js";

// --- Inline interfaces for API response shapes ---

/** Login-success envelope shared by the WebAuthn login-finish and OIDC-link
 *  responses; only `redirect` is consumed. */
interface LoginRedirect {
  redirect?: string;
}

// --- State ---

let conditionalAbort: AbortController | null = null;
let conditionalRetryTimer: ReturnType<typeof setTimeout> | null = null;
let conditionalUIAttempts = 0;
// The ONE offer decision: the server's "a passkey login could succeed here"
// AND this origin's availability. Both the button and the autofill ceremony
// read it, so they cannot disagree. Module-scope because the retry ladder
// re-enters startConditionalUI with no arguments.
let passkeyOffered = false;

// Drain in-flight conditional WebAuthn ceremony + clear any pending retry
// timer on page unload; otherwise a retry fires into a torn-down DOM.
registerCleanup(() => {
  conditionalAbort?.abort();
  conditionalAbort = null;
  if (conditionalRetryTimer !== null) {
    clearTimeout(conditionalRetryTimer);
    conditionalRetryTimer = null;
  }
});

// --- The setup wizard's chunk ---

const WIZARD_LOAD_FAILED = "The setup wizard could not be loaded. Reload the page to continue.";

/** loadWizard fetches the setup wizard's chunk. It is a separate chunk because only
 *  /setup ever needs it and every sign-in would otherwise download it; null on a
 *  failed fetch, the same shape the generated client answers with. */
async function loadWizard(): Promise<{
  startConfigWizard: (entry: WizardEntry) => Promise<void>;
} | null> {
  try {
    return await import("./wizard.js");
  } catch {
    return null;
  }
}

// --- Initialization ---

async function init(): Promise<void> {
  initTooltips({ attribute: "data-tip", delayCold: 300, delayWarm: 300 });

  // Link-on-login: an OIDC login that collided with an existing local account
  // redirects here with ?oidc_link=<token>; prove the password to link it.
  const linkToken = new URLSearchParams(window.location.search).get("oidc_link");
  if (linkToken) {
    // Wire before reveal: showPage focuses the first visible field.
    wireOIDCLinkForm(linkToken);
    showPage("loginPage");
    return;
  }

  const data = await authSetupStatus();
  if (!data) {
    showError("loginError", "Failed to initialize");
    showPage("loginPage");
    return;
  }

  if (data.setup_required) {
    showPage("setupPage");
    wireSetupForm(data.config_valid);
    return;
  }

  if (window.location.pathname === SETUP_PATH) {
    if (data.config_valid) {
      window.location.replace("/");
      return;
    }
    // Creating the admin issued a session, so this is usually still
    // authenticated. Without one (cookie cleared, restart, or a later
    // return) this 401s and the session-expiry redirect lands on
    // /login?next=/setup, whose resume-setup form funds the wizard again.
    const who = await me();
    if (who) {
      if (postLoginDestination(who.role, false) === "wizard") {
        const wiz = await loadWizard();
        if (!wiz) {
          showPage("loginPage");
          showError("loginError", WIZARD_LOAD_FAILED);
          return;
        }
        await wiz.startConfigWizard({ configValid: false });
      } else {
        showPage("setupNoticePage");
      }
      return;
    }
    // No 401: a transport failure. Fall through to resume-setup login below.
  }

  if (!data.config_valid) {
    showPage("loginPage");
    wireLoginForm(true);
    return;
  }

  showPage("loginPage");
  wireLoginForm(false);
  await detectAuthMethods(data);
  void startConditionalUI();
}

// --- Auth method detection ---

// The origin half fails OPEN: a probe that could not answer must not strand a
// user whose only credential is a passkey, and Begin gives the authoritative
// refusal with a real message.
function positivelyUnavailable(a: Availability): boolean {
  return !a.available && a.reason !== "probe_failed";
}

async function detectAuthMethods(setup: SetupStatus): Promise<void> {
  try {
    const res = await fetch(PATH_OIDC_REDIRECT, {
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

  // The setup half is free and is tested first, so no availability request is
  // spent on an install where no passkey login is possible.
  passkeyOffered =
    setup.passkey_login_available && !positivelyUnavailable(await probeAvailability());
  if (passkeyOffered) {
    show($("passkeyBtn"));
    show($("authDivider"));
  }

  $("oidcBtn")?.addEventListener("click", () => {
    window.location.href = PATH_OIDC_REDIRECT;
  });
  $("passkeyBtn")?.addEventListener("click", () => {
    void passkeyLogin();
  });
}

// --- Conditional UI (passkey autofill) ---

async function startConditionalUI(): Promise<void> {
  if (!passkeyOffered) {
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

  conditionalAbort = new AbortController();
  const outcome = await authenticateWithPasskey({
    mediation: "conditional",
    signal: conditionalAbort.signal,
  });
  if (outcome.kind === "failed") {
    // The ladder's retry/backoff is this surface's policy.
    conditionalUIAttempts++;
    if (conditionalUIAttempts >= 3) {
      return;
    }
    const delay = Math.min(1000 * 2 ** conditionalUIAttempts, 30_000);
    conditionalRetryTimer = setTimeout(startConditionalUI, delay);
    return;
  }
  if (outcome.kind === "authenticated") {
    conditionalUIAttempts = 0;
  }
  renderLoginOutcome(outcome);
}

// --- Passkey login ---

async function passkeyLogin(): Promise<void> {
  if (conditionalAbort) {
    conditionalAbort.abort();
    conditionalAbort = null;
  }
  renderLoginOutcome(await authenticateWithPasskey());
}

function renderLoginOutcome(outcome: LoginOutcome): void {
  switch (outcome.kind) {
    case "authenticated":
      window.location.href = outcome.redirect;
      return;
    case "cancelled":
      return;
    case "timeout":
      showError("loginError", "Passkey sign-in timed out. Please try again.");
      return;
    case "session-expired":
      void startConditionalUI();
      return;
    case "unknown-credential":
      showError(
        "loginError",
        "This passkey is not recognized. Please delete it from your authenticator and try again.",
      );
      return;
    case "verification-failed":
      showError("loginError", "Passkey verification failed. Please try again.");
      return;
    case "failed":
      showError("loginError", outcome.message);
  }
}

// --- Login error message helper ---

const LOGIN_ERROR_MAP: readonly {
  code: ErrorCode;
  msg: string | ((res: ApiResult<LoginSuccess>) => string);
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

function loginErrorMessage(res: ApiResult<LoginSuccess>): string {
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
      const res = await loginRaw({ username, password });
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
      if (resumeSetup) {
        // The wizard's endpoints are admin-gated: route non-admins to the
        // "an admin needs to finish setup" notice instead of a wizard of 403s.
        const dest = postLoginDestination(res.data?.user.role ?? "", false);
        if (dest === "wizard") {
          const wiz = await loadWizard();
          if (!wiz) {
            showError("loginError", WIZARD_LOAD_FAILED);
            return;
          }
          await wiz.startConfigWizard({ configValid: false, password });
        } else {
          showPage("setupNoticePage");
        }
        return;
      }
      window.location.href = res.data?.redirect ?? "/";
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
    // login.html's labels are siblings (<label for>), never wrappers, so
    // closest("label") finds nothing — hide via `labels` instead.
    userInput.hidden = true;
    for (const label of userInput.labels ?? []) {
      label.hidden = true;
    }
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
    const res = await oidcLinkRaw({
      link_token: linkToken,
      password,
    });
    if (!res.ok) {
      showError("loginError", res.error ?? "Failed to link account");
      return;
    }
    window.location.href = (res.data as LoginRedirect | undefined)?.redirect ?? "/";
  });
}

// --- Setup form ---

function wireSetupForm(configValid: boolean): void {
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
    const res = await authSetupCreateRaw({ username, password });
    if (!res.ok) {
      showError("setupError", res.error ?? "Setup failed");
      return;
    }
    // ONE flow for every first boot: admin creation flows into the config
    // wizard, prefilled from the config file; the memory-only password funds
    // the post-activation passkey offer.
    //
    // Not awaited: the wizard must not wait behind a save prompt, and the
    // call never rejects (see password-credential.ts).
    void storePasswordCredential(username, password);
    const wiz = await loadWizard();
    if (!wiz) {
      showError("setupError", WIZARD_LOAD_FAILED);
      return;
    }
    await wiz.startConfigWizard({ configValid, password });
  });
}

// --- Entry point ---

void init();
