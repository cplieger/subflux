import { describe, it, expect, beforeEach, vi, onTestFinished } from "vitest";

// security.ts exports ONE symbol (initSecurity); every section builder, the
// password validator, the passkey/API-key rows and the OIDC probe are
// module-private and reached only through the bus event the app emits when the
// user opens the Security dialog. So the harness captures that handler and
// drives the real dialog, exactly as the header button does in production.
//
// CRITICAL: vitest.config sets clearMocks/mockReset/restoreMocks, which strips
// a vi.fn's implementation before each test. Every factory below is therefore a
// PLAIN function closing over a vi.hoisted record, and per-test wiring is a
// field write on that record.

const client = vi.hoisted(() => ({
  me: null as unknown,
  passkeys: null as unknown[] | null,
  apikeys: null as unknown[] | null,
  changeResult: { ok: true } as { ok: boolean; error?: string },
  changeDefer: false,
  changeRelease: [] as ((v: unknown) => void)[],
  deleteResult: { ok: true } as { ok: boolean; error?: string },
  renameOK: true,
  generateResult: { ok: true, data: { key: "sfx_new_secret" } } as {
    ok: boolean;
    error?: string;
    data?: { key: string };
  },
  revokeOK: true,
  unlinkResult: { ok: true } as { ok: boolean; error?: string },
  begin: null as unknown,
  calls: [] as string[],
  changeBodies: [] as unknown[],
  renameArgs: [] as { id: unknown; body: unknown }[],
  generateBodies: [] as unknown[],
  beginBodies: [] as unknown[],
  deletedIds: [] as unknown[],
  revokedIds: [] as unknown[],
  profileResult: { ok: true } as { ok: boolean; error?: string },
  profileBodies: [] as unknown[],
}));
vi.mock("./wire/client.gen.js", () => ({
  me: () => {
    client.calls.push("me");
    return Promise.resolve(client.me);
  },
  listPasskeys: () => {
    client.calls.push("listPasskeys");
    return Promise.resolve(client.passkeys);
  },
  listAPIKeys: () => {
    client.calls.push("listAPIKeys");
    return Promise.resolve(client.apikeys);
  },
  changePasswordRaw: (body: unknown) => {
    client.calls.push("changePasswordRaw");
    client.changeBodies.push(body);
    if (client.changeDefer) {
      return new Promise((resolve) => {
        client.changeRelease.push(resolve as (v: unknown) => void);
      });
    }
    return Promise.resolve(client.changeResult);
  },
  deletePasskeyRaw: (id: unknown) => {
    client.calls.push("deletePasskeyRaw");
    client.deletedIds.push(id);
    return Promise.resolve(client.deleteResult);
  },
  renamePasskey: (id: unknown, body: unknown) => {
    client.calls.push("renamePasskey");
    client.renameArgs.push({ id, body });
    return Promise.resolve(client.renameOK);
  },
  generateAPIKeyRaw: (body: unknown) => {
    client.calls.push("generateAPIKeyRaw");
    client.generateBodies.push(body);
    return Promise.resolve(client.generateResult);
  },
  revokeAPIKey: (id: unknown) => {
    client.calls.push("revokeAPIKey");
    client.revokedIds.push(id);
    return Promise.resolve(client.revokeOK);
  },
  oidcUnlinkRaw: () => {
    client.calls.push("oidcUnlinkRaw");
    return Promise.resolve(client.unlinkResult);
  },
  updateProfileRaw: (body: unknown) => {
    client.calls.push("updateProfileRaw");
    client.profileBodies.push(body);
    return Promise.resolve(client.profileResult);
  },
  webauthnRegisterBegin: (body: unknown) => {
    client.calls.push("webauthnRegisterBegin");
    client.beginBodies.push(body);
    return Promise.resolve(client.begin);
  },
  // Pulled in by the REAL webauthn-utils.js (kept real so the finish request
  // carries genuine base64url encoding). Chromium DOES provide
  // window.PublicKeyCredential, so sendWebAuthnSignals gets past its feature
  // check and calls this; answering null takes its "nothing to signal" return
  // before any PublicKeyCredential.signal* call, and keeps the stub off the
  // network.
  webauthnSignalData: () => {
    client.calls.push("webauthnSignalData");
    return Promise.resolve(null);
  },
  PATH_OIDC_REDIRECT: "/api/auth/oidc",
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
}));

// ask() backs BOTH dom.ts's confirm() (boolean) and security.ts's
// promptTrimmed() (string | null). Answers are queued in call order; an
// exhausted queue answers null, which reads as "declined" for a confirm and
// "cancelled" for a prompt.
const asks = vi.hoisted(() => ({
  answers: [] as unknown[],
  calls: [] as { message: string; opts: unknown }[],
}));
vi.mock("@cplieger/ui-primitives/ask", () => ({
  ask: (message: string, opts: unknown) => {
    asks.calls.push({ message, opts });
    return Promise.resolve(asks.answers.length > 0 ? asks.answers.shift() : null);
  },
}));

const toasts = vi.hoisted(() => ({ errors: [] as string[], successes: [] as string[] }));
vi.mock("./notify.js", () => ({
  error: (msg: string) => {
    toasts.errors.push(msg);
  },
  success: (msg: string) => {
    toasts.successes.push(msg);
  },
  info: () => undefined,
}));

// initSecurity registers its OpenSecurity handler through the bus; capturing it
// is the only entry point into the dialog.
const busState = vi.hoisted(() => ({ handlers: new Map<string, () => void>() }));
vi.mock("./bus.js", () => ({
  on: (event: string, handler: () => void) => {
    busState.handlers.set(event, handler);
    return () => undefined;
  },
  emit: () => undefined,
  BusEvent: { OpenSecurity: "open:security" },
}));

import { initSecurity } from "./security.js";

// --- Fixtures (hardcoded, DAMP) ---

interface Me {
  username: string;
  display_name: string;
  role: string;
  id: number;
  has_passkeys: boolean;
  oidc_linked: boolean;
  has_password: boolean;
  can_link_oidc: boolean;
}

function user(over: Partial<Me> = {}): Me {
  return {
    username: "root",
    display_name: "",
    role: "user",
    id: 1,
    has_passkeys: false,
    oidc_linked: false,
    has_password: true,
    can_link_oidc: false,
    ...over,
  };
}

function passkey(id: number, name: string): Record<string, unknown> {
  return { id, name, created_at: "2026-03-04T10:00:00Z", backup_eligible: false };
}

function apiKey(id: number, label: string): Record<string, unknown> {
  return {
    id,
    label,
    key_prefix: "sfx",
    key_suffix: "9f2c",
    created_at: "2026-03-04T10:00:00Z",
  };
}

// WebAuthn creation options as the wire delivers them: base64url strings that
// the real creationOptionsFromJSON decodes ("Y2hhbGxlbmdl" is "challenge").
function beginResponse(): Record<string, unknown> {
  return {
    session_token: "sess-token-123",
    publicKey: {
      publicKey: {
        challenge: "Y2hhbGxlbmdl",
        rp: { id: "localhost", name: "subflux" },
        user: { id: "dXNlcjE", name: "root", displayName: "root" },
        pubKeyCredParams: [],
      },
    },
  };
}

// bufferToBase64url of these byte arrays: rawId "AQID", attestation "BAU",
// clientData "Bg" — the values the finish body must carry.
function credential(rk: boolean | undefined): Record<string, unknown> {
  return {
    id: "cred-id-1",
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: "public-key",
    response: {
      attestationObject: new Uint8Array([4, 5]).buffer,
      clientDataJSON: new Uint8Array([6]).buffer,
    },
    getClientExtensionResults: () => (rk === undefined ? {} : { credProps: { rk } }),
  };
}

// --- Network + WebAuthn boundary ---

const net = vi.hoisted(() => ({
  oidcStatus: 404,
  oidcThrows: false,
  // When set, the OIDC probe answers a FILTERED response of this type instead
  // of a constructed one. A cross-origin redirect under `redirect: "manual"`
  // reaches the caller as an opaque-redirect response — status 0, type
  // "opaqueredirect" — which the Response constructor cannot express (it
  // rejects status 0), so the probe's two readable fields are supplied directly.
  oidcType: null as string | null,
  finishStatus: 200,
  finishBody: {} as unknown,
  finishNotJSON: false,
  calls: [] as {
    url: string;
    method: string | undefined;
    headers: Record<string, string>;
    body: string | undefined;
  }[],
}));

const creds = vi.hoisted(() => ({
  createResult: null as unknown,
  createThrows: null as Error | null,
  createOptions: [] as unknown[],
}));

const clip = vi.hoisted(() => ({ writes: [] as string[], rejects: false }));

function installBoundaries(): void {
  vi.stubGlobal("fetch", (url: unknown, init?: RequestInit) => {
    const headers = (init?.headers ?? {}) as Record<string, string>;
    net.calls.push({
      url: String(url),
      method: init?.method,
      headers,
      body: typeof init?.body === "string" ? init.body : undefined,
    });
    if (String(url) === "/api/auth/oidc") {
      if (net.oidcThrows) {
        return Promise.reject(new TypeError("probe failed"));
      }
      if (net.oidcType !== null) {
        return Promise.resolve({ status: net.oidcStatus, type: net.oidcType } as Response);
      }
      return Promise.resolve(new Response(null, { status: net.oidcStatus }));
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
      create: (opts: unknown) => {
        creds.createOptions.push(opts);
        if (creds.createThrows) {
          return Promise.reject(creds.createThrows);
        }
        return Promise.resolve(creds.createResult);
      },
    },
  });

  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: (value: string) => {
        clip.writes.push(value);
        return clip.rejects ? Promise.reject(new Error("denied")) : Promise.resolve();
      },
    },
  });
}

// --- Harness ---

/** Let the dialog's promise chain (me + listPasskeys + the OIDC probe, then
 *  patch) settle. Two macrotask hops cover the deepest chain in this module. */
async function settle(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
  await new Promise((r) => setTimeout(r, 0));
}

function req<T extends Element>(selector: string, root: ParentNode = document): T {
  const found = root.querySelector<T>(selector);
  if (found === null) {
    throw new Error(`missing element: ${selector}`);
  }
  return found;
}

/** Find a button by its visible text or its accessible name. */
function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const all = [...root.querySelectorAll("button")];
  const found = all.find(
    (b) => b.textContent?.trim() === label || b.getAttribute("aria-label") === label,
  );
  if (found === undefined) {
    throw new Error(
      `no button labelled ${label}; have: ${all
        .map((b) => b.getAttribute("aria-label") ?? b.textContent?.trim())
        .join(" | ")}`,
    );
  }
  return found;
}

function sectionTitles(): string[] {
  return [...document.querySelectorAll("#securityDialog h3")].map((h) => h.textContent ?? "");
}

/** The `.sec-section` block whose heading is `title`. Every section renders a
 *  `.sec-feedback` and most a `.sec-fields`, so a first-match query would
 *  silently resolve to whichever section happens to come first. */
function section(title: string): HTMLElement {
  const found = [...document.querySelectorAll<HTMLElement>("#securityDialog .sec-section")].find(
    (s) => s.querySelector("h3")?.textContent === title,
  );
  if (found === undefined) {
    throw new Error(`missing section: ${title}; have: ${sectionTitles().join(" | ")}`);
  }
  return found;
}

/** Open the Security dialog the way the app does and return its body. */
async function openSecurity(): Promise<HTMLElement> {
  initSecurity();
  const handler = busState.handlers.get("open:security");
  if (handler === undefined) {
    throw new Error("initSecurity did not register an OpenSecurity handler");
  }
  handler();
  await settle();
  return req<HTMLElement>("#securityDialog .dlg-body");
}

/** Fill the change-password form and click Change Password. */
async function changePassword(current: string, next: string): Promise<HTMLButtonElement> {
  req<HTMLInputElement>("#sec-current-pw").value = current;
  req<HTMLInputElement>("#sec-new-pw").value = next;
  const btn = button("Change Password");
  btn.click();
  await settle();
  return btn;
}

function feedback(): HTMLElement {
  return req<HTMLElement>(".sec-feedback", section("Change Password"));
}

function releaseChange(result: { ok: boolean; error?: string }): void {
  const resolve = client.changeRelease.shift();
  if (resolve === undefined) {
    throw new Error("no deferred change-password request to release");
  }
  resolve(result);
}

/** Record every navigation the page attempts, without letting one carry the test
 *  page away.
 *
 *  `window.location` is `[LegacyUnforgeable]` in a real browser: `location`,
 *  `location.href`, `location.assign` and `window` ITSELF are all
 *  non-configurable, so `vi.stubGlobal("location", ...)` throws "Cannot redefine
 *  property: location", and there is no `Location.prototype` member to spy on.
 *  The DOM emulator this suite used to run under allowed the redefine; a browser
 *  does not. So nothing is faked: security.ts's real `window.location.href`
 *  assignment is observed through the platform's own Navigation API, whose
 *  `navigate` event is cancelable for a scripted assignment and carries the
 *  resolved destination. preventDefault is what keeps it from navigating the
 *  runner away. The assertion is stronger for it -- it pins that the browser
 *  really began the navigation, not that a property was written on a plain
 *  object. */
function watchNavigations(): { targets: string[] } {
  const targets: string[] = [];
  const onNavigate = (ev: NavigateEvent): void => {
    const url = new URL(ev.destination.url);
    targets.push(url.pathname + url.search);
    if (ev.cancelable) {
      ev.preventDefault();
    }
  };
  navigation.addEventListener("navigate", onNavigate);
  onTestFinished(() => {
    navigation.removeEventListener("navigate", onNavigate);
  });
  return { targets };
}

beforeEach(() => {
  document.body.innerHTML = '<dialog id="securityDialog"></dialog>';
  client.me = user({ role: "admin" });
  client.passkeys = [];
  client.apikeys = [];
  client.changeResult = { ok: true };
  client.changeDefer = false;
  client.changeRelease = [];
  client.deleteResult = { ok: true };
  client.renameOK = true;
  client.generateResult = { ok: true, data: { key: "sfx_new_secret" } };
  client.revokeOK = true;
  client.unlinkResult = { ok: true };
  client.begin = null;
  client.calls = [];
  client.changeBodies = [];
  client.renameArgs = [];
  client.generateBodies = [];
  client.beginBodies = [];
  client.deletedIds = [];
  client.revokedIds = [];
  asks.answers = [];
  asks.calls = [];
  toasts.errors = [];
  toasts.successes = [];
  busState.handlers.clear();
  net.oidcStatus = 404;
  net.oidcThrows = false;
  net.oidcType = null;
  net.finishStatus = 200;
  net.finishBody = {};
  net.finishNotJSON = false;
  net.calls = [];
  creds.createResult = null;
  creds.createThrows = null;
  creds.createOptions = [];
  clip.writes = [];
  clip.rejects = false;
  installBoundaries();
});

describe("security dialog: which sections an account gets", () => {
  it("opens on the bus event and renders the dialog head", async () => {
    await openSecurity();

    expect(req("#securityDialog .dlg-head h2").textContent).toBe("Security");
  });

  it("gives an admin with a password all three local sections", async () => {
    client.me = user({ role: "admin", has_password: true });

    await openSecurity();

    expect(sectionTitles()).toEqual(["Display Name", "Change Password", "Passkeys", "API Keys"]);
  });

  it("withholds the API-key section from a non-admin", async () => {
    client.me = user({ role: "user", has_password: true });

    await openSecurity();

    expect(sectionTitles()).toEqual(["Display Name", "Change Password", "Passkeys"]);
  });

  it("never asks the server for API keys as a non-admin", async () => {
    client.me = user({ role: "user", has_password: true });

    await openSecurity();

    expect(client.calls).not.toContain("listAPIKeys");
  });

  it("withholds password and passkey management from a password-less SSO account", async () => {
    client.me = user({ role: "admin", has_password: false });

    await openSecurity();

    expect(sectionTitles()).toEqual(["Display Name", "API Keys"]);
  });

  it("shows the loading placeholder until the sections arrive", async () => {
    client.me = user({ role: "admin" });
    initSecurity();
    const handler = busState.handlers.get("open:security");
    if (handler === undefined) {
      throw new Error("no handler");
    }
    handler();

    expect(req("#securityDialog .dlg-body").textContent).toBe("Loading\u2026");
  });

  it("closes the dialog from the header close button", async () => {
    await openSecurity();

    button("Close").click();

    expect(req("#securityDialog").className).toContain("is-leaving");
  });

  it("renders no sections at all when the identity request fails", async () => {
    client.me = null;

    await openSecurity();

    expect(sectionTitles()).toEqual([]);
  });
});

describe("security dialog: display name", () => {
  /** Fill the display-name field and click Save. */
  async function saveDisplayName(value: string): Promise<void> {
    req<HTMLInputElement>("#sec-display-name").value = value;
    button("Save").click();
    await settle();
  }

  function nameFeedback(): HTMLElement {
    return req<HTMLElement>(".sec-feedback", section("Display Name"));
  }

  it("prefills the field with the stored display name", async () => {
    client.me = user({ display_name: "Ada Lovelace" });

    await openSecurity();

    expect(req<HTMLInputElement>("#sec-display-name").value).toBe("Ada Lovelace");
  });

  it("offers the username as the placeholder when no display name is set", async () => {
    client.me = user({ username: "ada", display_name: "" });

    await openSecurity();

    const input = req<HTMLInputElement>("#sec-display-name");
    expect([input.value, input.placeholder]).toEqual(["", "ada"]);
  });

  it("sends the typed value under its wire name", async () => {
    await openSecurity();

    await saveDisplayName("Ada Lovelace");

    expect(client.profileBodies).toEqual([{ display_name: "Ada Lovelace" }]);
  });

  it("signals the new account label to the credential manager after a save", async () => {
    await openSecurity();
    client.calls = [];

    await saveDisplayName("Ada Lovelace");

    // Without this the stored passkey keeps offering the name it was
    // registered under, which is the whole reason the section exists.
    expect(client.calls).toContain("webauthnSignalData");
  });

  it("confirms a successful save", async () => {
    await openSecurity();

    await saveDisplayName("Ada Lovelace");

    expect(nameFeedback().textContent).toBe("Display name saved");
  });

  it("says the name was cleared when the field is emptied", async () => {
    client.me = user({ display_name: "Ada Lovelace" });
    await openSecurity();

    await saveDisplayName("");

    expect(nameFeedback().textContent).toBe("Display name cleared");
  });

  it("shows the server's rejection message", async () => {
    client.profileResult = { ok: false, error: "display name contains a disallowed character" };
    await openSecurity();

    await saveDisplayName("Ada\u202eevoL");

    expect(nameFeedback().textContent).toBe("display name contains a disallowed character");
  });

  it("sends no signal when the save is rejected", async () => {
    client.profileResult = { ok: false, error: "display name too long" };
    await openSecurity();
    client.calls = [];

    await saveDisplayName("a".repeat(200));

    expect(client.calls).not.toContain("webauthnSignalData");
  });

  it("falls back to a generic message when the rejection carries no text", async () => {
    client.profileResult = { ok: false };
    await openSecurity();

    await saveDisplayName("Ada Lovelace");

    expect(nameFeedback().textContent).toBe("Failed to save display name");
  });

  it("offers the section to a password-less SSO account too", async () => {
    client.me = user({ role: "user", has_password: false });

    await openSecurity();

    // A display name is not a credential, so it is not managed at the IdP.
    expect(sectionTitles()).toContain("Display Name");
  });
});

describe("security dialog: change password", () => {
  it("starts with an empty feedback area kept out of the accessibility tree", async () => {
    await openSecurity();

    expect([feedback().hidden, feedback().className]).toEqual([true, "sec-feedback"]);
  });

  it("refuses an empty current password", async () => {
    await openSecurity();

    await changePassword("", "brand-new-secret");

    expect(feedback().textContent).toBe("Both fields are required");
  });

  it("sends no request when the current password is empty", async () => {
    await openSecurity();

    await changePassword("", "brand-new-secret");

    expect(client.changeBodies).toEqual([]);
  });

  it("announces a missing field assertively", async () => {
    await openSecurity();

    await changePassword("", "brand-new-secret");

    expect(feedback().getAttribute("role")).toBe("alert");
  });

  it("refuses an empty new password", async () => {
    await openSecurity();

    await changePassword("old-secret", "");

    expect(feedback().textContent).toBe("Both fields are required");
  });

  it("refuses a new password shorter than eight characters", async () => {
    await openSecurity();

    await changePassword("old-secret", "1234567");

    expect(feedback().textContent).toBe("Password must be at least 8 characters");
  });

  it("announces a too-short password assertively", async () => {
    await openSecurity();

    await changePassword("old-secret", "1234567");

    expect(feedback().getAttribute("role")).toBe("alert");
  });

  it("sends no request for a too-short new password", async () => {
    await openSecurity();

    await changePassword("old-secret", "1234567");

    expect(client.changeBodies).toEqual([]);
  });

  it("accepts a new password of exactly eight characters", async () => {
    await openSecurity();

    await changePassword("old-secret", "12345678");

    expect(feedback().textContent).toBe("Password changed");
  });

  it("sends both passwords under their wire names", async () => {
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect(client.changeBodies).toEqual([
      { current_password: "old-secret", new_password: "brand-new-secret" },
    ]);
  });

  it("announces success politely rather than as an alert", async () => {
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect(feedback().getAttribute("role")).toBe("status");
  });

  it("marks the success feedback with the ok class and unhides it", async () => {
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect([feedback().className, feedback().hidden]).toEqual([
      "sec-feedback sec-feedback-ok",
      false,
    ]);
  });

  it("clears both inputs after a successful change", async () => {
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect([
      req<HTMLInputElement>("#sec-current-pw").value,
      req<HTMLInputElement>("#sec-new-pw").value,
    ]).toEqual(["", ""]);
  });

  it("shows the server's rejection message", async () => {
    client.changeResult = { ok: false, error: "Current password is wrong" };
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect(feedback().textContent).toBe("Current password is wrong");
  });

  it("announces a rejection assertively as an alert", async () => {
    client.changeResult = { ok: false, error: "Current password is wrong" };
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect([feedback().getAttribute("role"), feedback().getAttribute("aria-live")]).toEqual([
      "alert",
      "assertive",
    ]);
  });

  it("falls back to a generic message when the rejection carries no text", async () => {
    client.changeResult = { ok: false };
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect(feedback().textContent).toBe("Failed to change password");
  });

  it("keeps the inputs after a rejected change", async () => {
    client.changeResult = { ok: false, error: "nope" };
    await openSecurity();

    await changePassword("old-secret", "brand-new-secret");

    expect(req<HTMLInputElement>("#sec-new-pw").value).toBe("brand-new-secret");
  });
});

describe("security dialog: the busy-click lifecycle", () => {
  it("disables the button while the request is in flight", async () => {
    client.changeDefer = true;
    await openSecurity();

    const btn = await changePassword("old-secret", "brand-new-secret");

    expect(btn.disabled).toBe(true);
  });

  it("announces the button as busy while the request is in flight", async () => {
    client.changeDefer = true;
    await openSecurity();

    const btn = await changePassword("old-secret", "brand-new-secret");

    expect(btn.getAttribute("aria-busy")).toBe("true");
  });

  it("ignores a second click while the first request is still running", async () => {
    client.changeDefer = true;
    await openSecurity();
    const btn = await changePassword("old-secret", "brand-new-secret");

    btn.click();
    await settle();

    expect(client.changeBodies).toHaveLength(1);
  });

  it("re-enables the button once the request settles", async () => {
    client.changeDefer = true;
    await openSecurity();
    const btn = await changePassword("old-secret", "brand-new-secret");

    releaseChange({ ok: true });
    await settle();

    expect(btn.disabled).toBe(false);
  });

  it("drops the busy announcement once the request settles", async () => {
    client.changeDefer = true;
    await openSecurity();
    const btn = await changePassword("old-secret", "brand-new-secret");

    releaseChange({ ok: true });
    await settle();

    expect(btn.hasAttribute("aria-busy")).toBe(false);
  });
});

describe("security dialog: passkeys", () => {
  it("says so when no passkeys are registered", async () => {
    client.passkeys = [];

    await openSecurity();

    expect(req(".sec-section p.muted").textContent).toBe("No passkeys registered.");
  });

  it("renders one row per registered passkey", async () => {
    client.passkeys = [passkey(1, "Yubikey"), passkey(2, "Phone")];

    await openSecurity();

    expect([...document.querySelectorAll(".sec-pk-name")].map((e) => e.textContent)).toEqual([
      "Yubikey",
      "Phone",
    ]);
  });

  it("labels an unnamed passkey as Passkey", async () => {
    client.passkeys = [passkey(1, "")];

    await openSecurity();

    expect(req(".sec-pk-name").textContent).toBe("Passkey");
  });

  it("names the rename control for screen readers", async () => {
    client.passkeys = [passkey(1, "Yubikey")];

    await openSecurity();

    expect(req(".sec-pk-name").getAttribute("aria-label")).toBe("Rename passkey Yubikey");
  });

  it("keeps the passkey when the delete confirmation is declined", async () => {
    client.passkeys = [passkey(1, "Yubikey")];
    asks.answers = [false];
    await openSecurity();

    button("Delete passkey").click();
    await settle();

    expect(client.deletedIds).toEqual([]);
  });

  it("asks before deleting a passkey", async () => {
    client.passkeys = [passkey(1, "Yubikey")];
    asks.answers = [false];
    await openSecurity();

    button("Delete passkey").click();
    await settle();

    expect(asks.calls).toEqual([
      {
        message: "This passkey can no longer be used to sign in.",
        opts: { title: "Delete passkey", confirmLabel: "Delete" },
      },
    ]);
  });

  it("deletes the confirmed passkey by id", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = [true];
    await openSecurity();

    button("Delete passkey").click();
    await settle();

    expect(client.deletedIds).toEqual([7]);
  });

  it("re-renders the list after a successful delete", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = [true];
    await openSecurity();
    client.passkeys = [];

    button("Delete passkey").click();
    await settle();

    expect(req(".sec-section p.muted").textContent).toBe("No passkeys registered.");
  });

  it("confirms a successful delete", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = [true];
    await openSecurity();

    button("Delete passkey").click();
    await settle();

    expect(toasts.successes).toEqual(["Passkey deleted"]);
  });

  it("reports the server's message when a delete fails", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    client.deleteResult = { ok: false, error: "Passkey is your last credential" };
    asks.answers = [true];
    await openSecurity();

    button("Delete passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Passkey is your last credential"]);
  });

  it("falls back to a generic message when a failed delete carries no text", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    client.deleteResult = { ok: false };
    asks.answers = [true];
    await openSecurity();

    button("Delete passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to delete passkey"]);
  });

  it("renames a passkey to the submitted name", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = ["Work key"];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(client.renameArgs).toEqual([{ id: 7, body: { name: "Work key" } }]);
  });

  it("updates the row label after a rename", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = ["Work key"];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(req(".sec-pk-name").textContent).toBe("Work key");
  });

  it("trims the submitted name before renaming", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = ["  Work key  "];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(client.renameArgs).toEqual([{ id: 7, body: { name: "Work key" } }]);
  });

  it("sends no rename for a whitespace-only name", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = ["   "];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(client.renameArgs).toEqual([]);
  });

  it("sends no rename when the name is unchanged", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = ["Yubikey"];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(client.renameArgs).toEqual([]);
  });

  it("prefills the rename prompt with the current name", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    asks.answers = ["Work key"];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(asks.calls).toEqual([
      {
        message: "Rename passkey:",
        opts: { title: "Input", input: { initialValue: "Yubikey", maxLength: 64 } },
      },
    ]);
  });

  it("reports a failed rename", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    client.renameOK = false;
    asks.answers = ["Work key"];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to rename passkey"]);
  });

  it("keeps the old label when a rename fails", async () => {
    client.passkeys = [passkey(7, "Yubikey")];
    client.renameOK = false;
    asks.answers = ["Work key"];
    await openSecurity();

    req<HTMLElement>(".sec-pk-name").click();
    await settle();

    expect(req(".sec-pk-name").textContent).toBe("Yubikey");
  });
});

describe("security dialog: passkey registration", () => {
  it("asks for the account password before registering", async () => {
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(asks.calls).toEqual([
      {
        message: "Enter your password to add a passkey:",
        opts: {
          title: "Input",
          input: { type: "password", autocomplete: "current-password" },
        },
      },
    ]);
  });

  it("starts no registration when the password prompt is cancelled", async () => {
    asks.answers = [];
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(client.beginBodies).toEqual([]);
  });

  it("starts no registration when the password prompt is submitted blank", async () => {
    asks.answers = ["   "];
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(client.beginBodies).toEqual([]);
  });

  it("sends the typed password to the begin endpoint", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(client.beginBodies).toEqual([{ password: "old-secret" }]);
  });

  it("reports a begin response with no options", async () => {
    asks.answers = ["old-secret"];
    client.begin = null;
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to start passkey registration"]);
  });

  it("creates no credential when the begin step failed", async () => {
    asks.answers = ["old-secret"];
    client.begin = null;
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(creds.createOptions).toEqual([]);
  });

  it("decodes the challenge into a buffer before calling the authenticator", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    const opts = creds.createOptions[0] as { publicKey: { challenge: ArrayBuffer } };
    expect(new TextDecoder().decode(opts.publicKey.challenge)).toBe("challenge");
  });

  it("reports a cancelled credential creation", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = null;
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Passkey creation cancelled"]);
  });

  it("warns when the authenticator made a non-discoverable credential", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(false);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual([
      "Your authenticator created a non-discoverable credential. Passwordless login may not work with this passkey.",
    ]);
  });

  it("stays quiet when the credential is discoverable", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual([]);
  });

  it("stays quiet when the authenticator reports no credProps at all", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(undefined);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual([]);
  });

  it("carries the session token in the WebAuthn session header", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    const finish = net.calls.filter((c) => c.url.includes("register/finish"));
    expect(finish[0]?.headers).toEqual({
      "Content-Type": "application/json",
      "X-WebAuthn-Session": "sess-token-123",
    });
  });

  it("posts the attestation as base64url", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    await openSecurity();

    button("Add passkey").click();
    await settle();

    const finish = net.calls.filter((c) => c.url.includes("register/finish"));
    expect(JSON.parse(finish[0]?.body ?? "{}")).toEqual({
      id: "cred-id-1",
      rawId: "AQID",
      type: "public-key",
      response: { attestationObject: "BAU", clientDataJSON: "Bg" },
    });
  });

  it("confirms a completed registration", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    net.finishStatus = 200;
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.successes).toEqual(["Passkey registered"]);
  });

  it("reports the server's message when the finish step is rejected", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    net.finishStatus = 400;
    net.finishBody = { error: "Passkey already registered" };
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Passkey already registered"]);
  });

  it("falls back to a generic message when a rejected finish carries no JSON", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createResult = credential(true);
    net.finishStatus = 500;
    net.finishNotJSON = true;
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to register passkey"]);
  });

  it("stays silent when the user aborts the WebAuthn ceremony", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createThrows = new DOMException("aborted", "AbortError");
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual([]);
  });

  it("reports any other registration failure", async () => {
    asks.answers = ["old-secret"];
    client.begin = beginResponse();
    creds.createThrows = new DOMException("not allowed", "NotAllowedError");
    await openSecurity();

    button("Add passkey").click();
    await settle();

    expect(toasts.errors).toEqual(["Passkey registration failed"]);
  });
});

describe("security dialog: API keys", () => {
  it("says so when there are no API keys", async () => {
    client.apikeys = [];

    await openSecurity();

    const muted = [...document.querySelectorAll(".sec-section p.muted")].map((p) => p.textContent);
    expect(muted).toContain("No API keys.");
  });

  it("renders the masked key and its label", async () => {
    client.apikeys = [apiKey(3, "CI runner")];

    await openSecurity();

    expect(req(".sec-list code").textContent).toBe("sfx\u20269f2c");
  });

  it("marks an unlabelled key as having no label", async () => {
    client.apikeys = [apiKey(3, "")];

    await openSecurity();

    expect(req(".sec-list .sec-row span.muted").textContent).toBe("No label");
  });

  it("asks for a label before generating a key", async () => {
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(asks.calls).toEqual([
      {
        message: "Label for the new API key:",
        opts: { title: "Input", input: { maxLength: 64 } },
      },
    ]);
  });

  it("generates no key when the label prompt is cancelled", async () => {
    asks.answers = [];
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(client.generateBodies).toEqual([]);
  });

  it("sends the trimmed label to the generate endpoint", async () => {
    asks.answers = ["  CI runner "];
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(client.generateBodies).toEqual([{ label: "CI runner" }]);
  });

  it("shows the new key exactly once, in full", async () => {
    asks.answers = ["CI runner"];
    client.generateResult = { ok: true, data: { key: "not-a-real-key" } };
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(req(".sec-new-key code").textContent).toBe("not-a-real-key");
  });

  it("announces the new key and takes focus so it cannot be missed", async () => {
    asks.answers = ["CI runner"];
    await openSecurity();

    button("Generate API key").click();
    await settle();

    const shown = req<HTMLElement>(".sec-new-key");
    expect([
      shown.getAttribute("role"),
      shown.getAttribute("aria-live"),
      shown.tabIndex,
      document.activeElement === shown,
    ]).toEqual(["status", "polite", -1, true]);
  });

  it("shows the new key directly under the section heading", async () => {
    asks.answers = ["CI runner"];
    await openSecurity();

    button("Generate API key").click();
    await settle();

    const heading = req("#securityDialog .sec-section:last-of-type h3");
    expect(heading.nextElementSibling?.className).toBe("sec-new-key");
  });

  it("copies the new key to the clipboard", async () => {
    asks.answers = ["CI runner"];
    client.generateResult = { ok: true, data: { key: "not-a-real-key" } };
    await openSecurity();
    button("Generate API key").click();
    await settle();

    button("Copy").click();
    await settle();

    expect(clip.writes).toEqual(["not-a-real-key"]);
  });

  it("confirms a successful copy", async () => {
    asks.answers = ["CI runner"];
    await openSecurity();
    button("Generate API key").click();
    await settle();

    button("Copy").click();
    await settle();

    expect(toasts.successes).toEqual(["Copied to clipboard"]);
  });

  it("reports a refused clipboard write", async () => {
    asks.answers = ["CI runner"];
    clip.rejects = true;
    await openSecurity();
    button("Generate API key").click();
    await settle();

    button("Copy").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to copy"]);
  });

  it("returns to the key list when the user is done", async () => {
    asks.answers = ["CI runner"];
    await openSecurity();
    button("Generate API key").click();
    await settle();

    button("Done").click();
    await settle();

    expect(document.querySelector(".sec-new-key")).toBeNull();
  });

  it("reports a failed generation", async () => {
    asks.answers = ["CI runner"];
    client.generateResult = { ok: false, error: "Key limit reached" };
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(toasts.errors).toEqual(["Key limit reached"]);
  });

  it("falls back to a generic message when a failed generation carries no text", async () => {
    asks.answers = ["CI runner"];
    client.generateResult = { ok: false };
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to generate API key"]);
  });

  it("shows no key when the response carries none", async () => {
    asks.answers = ["CI runner"];
    client.generateResult = { ok: true };
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(document.querySelector(".sec-new-key")).toBeNull();
  });

  it("asks before revoking a key", async () => {
    client.apikeys = [apiKey(3, "CI runner")];
    asks.answers = [false];
    await openSecurity();

    button("Revoke API key").click();
    await settle();

    expect(asks.calls).toEqual([
      {
        message: "Applications using this key will stop working.",
        opts: { title: "Revoke API key", confirmLabel: "Revoke" },
      },
    ]);
  });

  it("keeps the key when the revoke confirmation is declined", async () => {
    client.apikeys = [apiKey(3, "CI runner")];
    asks.answers = [false];
    await openSecurity();

    button("Revoke API key").click();
    await settle();

    expect(client.revokedIds).toEqual([]);
  });

  it("revokes the confirmed key by id", async () => {
    client.apikeys = [apiKey(3, "CI runner")];
    asks.answers = [true];
    await openSecurity();

    button("Revoke API key").click();
    await settle();

    expect(client.revokedIds).toEqual([3]);
  });

  it("confirms a successful revoke", async () => {
    client.apikeys = [apiKey(3, "CI runner")];
    asks.answers = [true];
    await openSecurity();

    button("Revoke API key").click();
    await settle();

    expect(toasts.successes).toEqual(["API key revoked"]);
  });

  it("re-renders the list after a successful revoke", async () => {
    client.apikeys = [apiKey(3, "CI runner")];
    asks.answers = [true];
    await openSecurity();
    client.apikeys = [];

    button("Revoke API key").click();
    await settle();

    expect(document.querySelector(".sec-list code")).toBeNull();
  });

  it("reports a failed revoke", async () => {
    client.apikeys = [apiKey(3, "CI runner")];
    client.revokeOK = false;
    asks.answers = [true];
    await openSecurity();

    button("Revoke API key").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to revoke API key"]);
  });
});

describe("security dialog: single sign-on", () => {
  it("omits the section when SSO is neither linked nor available", async () => {
    client.me = user({ role: "admin", oidc_linked: false });
    net.oidcStatus = 404;

    await openSecurity();

    expect(sectionTitles()).not.toContain("Single Sign-On");
  });

  it("offers the section when the probe answers 200", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;

    await openSecurity();

    expect(sectionTitles()).toContain("Single Sign-On");
  });

  it("offers the section when the probe answers a redirect", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 302;

    await openSecurity();

    expect(sectionTitles()).toContain("Single Sign-On");
  });

  it("offers the section when the probe answers an opaque cross-origin redirect", async () => {
    // What a browser actually hands back for `redirect: "manual"` when the
    // server bounces to an external identity provider: the redirect is opaque,
    // so the status reads 0 and only the response TYPE says what happened.
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 0;
    net.oidcType = "opaqueredirect";

    await openSecurity();

    expect(sectionTitles()).toContain("Single Sign-On");
  });

  it("probes the OIDC entry point without following its redirect", async () => {
    client.me = user({ role: "admin" });
    net.oidcStatus = 302;

    await openSecurity();

    expect(net.calls[0]).toMatchObject({ url: "/api/auth/oidc", method: "HEAD" });
  });

  it("treats a failed probe as SSO unavailable", async () => {
    client.me = user({ role: "admin", oidc_linked: false });
    net.oidcThrows = true;

    await openSecurity();

    expect(sectionTitles()).not.toContain("Single Sign-On");
  });

  it("keeps the section for a linked account even when the probe fails", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    net.oidcThrows = true;

    await openSecurity();

    expect(sectionTitles()).toContain("Single Sign-On");
  });

  it("reports a linked account as connected", async () => {
    client.me = user({ role: "admin", oidc_linked: true });

    await openSecurity();

    expect(req(".sec-status .badge").textContent).toBe("Connected");
  });

  it("marks the connected badge with its ok status", async () => {
    client.me = user({ role: "admin", oidc_linked: true });

    await openSecurity();

    expect(req(".sec-status .badge").getAttribute("data-status")).toBe("ok");
  });

  it("reports an unlinked account as not connected", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;

    await openSecurity();

    expect(req(".sec-status .badge").textContent).toBe("Not connected");
  });

  it("leaves the unconnected badge without an ok status", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;

    await openSecurity();

    expect(req(".sec-status .badge").getAttribute("data-status")).toBe("");
  });

  it("offers a connect control to an account that may link", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;

    await openSecurity();

    expect(button("Connect").tagName).toBe("BUTTON");
  });

  it("sends the browser to the OIDC entry point when Connect is clicked", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;
    await openSecurity();
    const nav = watchNavigations();

    button("Connect").click();

    expect(nav.targets).toEqual(["/api/auth/oidc"]);
  });

  it("explains why an account that may not link has no connect control", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: false });
    net.oidcStatus = 200;

    await openSecurity();

    const muted = [...document.querySelectorAll(".sec-section p.muted")].map((p) => p.textContent);
    expect(muted).toContain(
      "Create another admin before switching this account to single sign-on.",
    );
  });

  it("asks before disconnecting SSO", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    asks.answers = [false];
    await openSecurity();

    button("Disconnect").click();
    await settle();

    expect(asks.calls).toEqual([
      {
        message: "You'll no longer be able to sign in through your identity provider.",
        opts: { title: "Disconnect single sign-on", confirmLabel: "Disconnect" },
      },
    ]);
  });

  it("keeps the link when the disconnect confirmation is declined", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    asks.answers = [false];
    await openSecurity();

    button("Disconnect").click();
    await settle();

    expect(client.calls).not.toContain("oidcUnlinkRaw");
  });

  it("disconnects on confirmation", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    asks.answers = [true];
    await openSecurity();

    button("Disconnect").click();
    await settle();

    expect(toasts.successes).toEqual(["Single sign-on disconnected"]);
  });

  it("re-renders the section after a successful disconnect", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    asks.answers = [true];
    await openSecurity();
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;

    button("Disconnect").click();
    await settle();

    expect(req(".sec-status .badge").textContent).toBe("Not connected");
  });

  it("reports the server's message when a disconnect fails", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    client.unlinkResult = { ok: false, error: "SSO is the only credential" };
    asks.answers = [true];
    await openSecurity();

    button("Disconnect").click();
    await settle();

    expect(toasts.errors).toEqual(["SSO is the only credential"]);
  });

  it("falls back to a generic message when a failed disconnect carries no text", async () => {
    client.me = user({ role: "admin", oidc_linked: true });
    client.unlinkResult = { ok: false };
    asks.answers = [true];
    await openSecurity();

    button("Disconnect").click();
    await settle();

    expect(toasts.errors).toEqual(["Failed to disconnect single sign-on"]);
  });
});

// The dialog's chrome: the structure css/15-security.css styles. Every class
// name below has a live rule there — `.sec-section` (the bordered block per
// concern), `.sec-fields` (the label/input grid), `.sec-actions` (the trailing
// button row), `.sec-list` + `.sec-row` (the passkey/key table), `.muted` (the
// secondary text) and `.sec-key-value` (the code + copy pairing). Losing one
// leaves the markup readable to a test that only looks at text, and unusable
// on screen.
describe("security dialog: the chrome the stylesheet styles", () => {
  it("renders every concern as its own section", async () => {
    client.me = user({ role: "admin", has_password: true, oidc_linked: true });

    await openSecurity();

    expect(
      [...document.querySelectorAll("#securityDialog .sec-section")].map(
        (s) => s.querySelector("h3")?.textContent,
      ),
    ).toEqual(["Display Name", "Change Password", "Passkeys", "API Keys", "Single Sign-On"]);
  });

  it("announces the loading placeholder as secondary text", async () => {
    client.me = user({ role: "admin" });
    initSecurity();
    const handler = busState.handlers.get("open:security");
    if (handler === undefined) {
      throw new Error("no handler");
    }
    handler();

    expect(req("#securityDialog .dlg-body .muted").textContent).toBe("Loading\u2026");
  });

  it("lays the password inputs out in a fields grid", async () => {
    await openSecurity();

    expect(
      [...req(".sec-fields", section("Change Password")).querySelectorAll("input")].map(
        (i) => i.id,
      ),
    ).toEqual(["sec-current-pw", "sec-new-pw"]);
  });

  it("puts each section's controls in a trailing actions row", async () => {
    client.me = user({ role: "admin", has_password: true, oidc_linked: true });

    await openSecurity();

    expect(
      [...document.querySelectorAll("#securityDialog .sec-actions button")].map((b) =>
        b.textContent?.trim(),
      ),
    ).toEqual(["Save", "Change Password", "Add passkey", "Generate API key", "Disconnect"]);
  });

  it("puts the connect control in an actions row too", async () => {
    client.me = user({ role: "admin", oidc_linked: false, can_link_oidc: true });
    net.oidcStatus = 200;

    await openSecurity();

    expect(
      [...document.querySelectorAll("#securityDialog .sec-actions button")].map((b) =>
        b.textContent?.trim(),
      ),
    ).toEqual(["Save", "Change Password", "Add passkey", "Generate API key", "Connect"]);
  });

  it("renders the passkeys as a list of rows", async () => {
    client.passkeys = [passkey(1, "Phone"), passkey(2, "Laptop")];

    await openSecurity();

    expect([...req(".sec-list").querySelectorAll(".sec-row")]).toHaveLength(2);
  });

  it("shows a passkey's creation date as secondary text beside its name", async () => {
    client.passkeys = [passkey(1, "Phone")];

    await openSecurity();

    expect(req(".sec-row .muted").textContent).toContain("2026");
  });

  it("shows an API key's creation date as secondary text", async () => {
    client.apikeys = [apiKey(1, "CI runner")];

    await openSecurity();

    expect(req(".sec-row .muted").textContent).toContain("2026");
  });

  it("pairs the new key with its copy button", async () => {
    asks.answers = ["CI runner"];
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect([...req(".sec-key-value").children].map((c) => c.tagName)).toEqual(["CODE", "BUTTON"]);
  });

  it("puts the Done control in an actions row under the new key", async () => {
    asks.answers = ["CI runner"];
    await openSecurity();

    button("Generate API key").click();
    await settle();

    expect(req(".sec-new-key + .sec-actions button").textContent).toBe("Done");
  });

  it("drops the dialog skin once the close has finished", async () => {
    await openSecurity();
    const dlg = req<HTMLDialogElement>("#securityDialog");
    // <dialog>.close() QUEUES its close event, so the disposal that event
    // triggers has to be awaited rather than asserted on the next line.
    const closed = new Promise<void>((resolve) => {
      dlg.addEventListener("close", () => resolve(), { once: true });
    });

    button("Close").click();
    // The fade-out finalizes on the dialog's own transitionend (or a 400ms
    // fallback); the close event it then fires is what disposes the controller.
    dlg.dispatchEvent(new Event("transitionend"));
    await closed;
    await settle();

    expect(dlg.classList.contains("uip-dialog")).toBe(false);
  });
});
