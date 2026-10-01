// login.passkey.test.ts — the sign-in page's passkey offer: ONE composed value
// (the server's passkey_login_available AND this origin's availability) gates
// both the button and the autofill ceremony, the origin half fails OPEN, and
// every login outcome renders the sentence login.ts has always rendered.
//
// login.ts runs init() at import, so each case imports it through a busted
// specifier: vi.resetModules() does not re-evaluate a module in the browser's
// URL-keyed module map, and a fresh instance is what gives each case its own
// module state (passkeyOffered, the abort controller, the retry ladder).
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type * as ClientGen from "./wire/client.gen.js";

const wire = vi.hoisted(() => ({
  setup: {
    setup_required: false,
    config_valid: true,
    passkey_login_available: true,
  } as Record<string, unknown>,
  availability: { ok: true, status: 200, data: { available: true } } as unknown,
  availabilityCalls: 0,
  loginBegin: { ok: false, status: 400 } as unknown,
  loginBeginCalls: [] as unknown[],
}));
vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<typeof ClientGen>()),
  authSetupStatus: () => Promise.resolve(wire.setup),
  webauthnAvailabilityRaw: () => {
    wire.availabilityCalls++;
    return Promise.resolve(wire.availability);
  },
  // Cloned per call: requestOptionsFromJSON decodes the options in place.
  webauthnLoginBeginRaw: (query: unknown) => {
    wire.loginBeginCalls.push(query);
    return Promise.resolve(structuredClone(wire.loginBegin));
  },
  webauthnSignalData: () => Promise.resolve(null),
}));

const net = vi.hoisted(() => ({
  oidcStatus: 404,
  finishStatus: 200,
  finishBody: {} as unknown,
  finishNotJSON: false,
  finishCalls: 0,
}));

// getResults is a QUEUE: the autofill ceremony and a click both call get(),
// and a restarted ladder calls it again, so each call consumes one answer and
// an exhausted queue answers null (a cancellation).
const authenticator = vi.hoisted(() => ({
  getResults: [] as unknown[],
  getThrows: null as unknown,
  conditionalAvailable: true,
}));

function beginResponse(): Record<string, unknown> {
  return {
    session_token: "sess-token-123",
    publicKey: {
      publicKey: {
        challenge: "Y2hhbGxlbmdl",
        rp: { id: "example.com", name: "subflux" },
        user: { id: "dXNlcjE", name: "root", displayName: "root" },
        pubKeyCredParams: [],
      },
    },
  };
}

function assertion(): Record<string, unknown> {
  return {
    id: "cred-id-1",
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: "public-key",
    response: {
      authenticatorData: new Uint8Array([7]).buffer,
      clientDataJSON: new Uint8Array([6]).buffer,
      signature: new Uint8Array([8]).buffer,
      userHandle: null,
    },
    getClientExtensionResults: () => ({}),
  };
}

function mountLoginPage(): void {
  const main = document.createElement("main");
  main.id = "loginPage";
  main.className = "auth-page";
  main.hidden = true;
  const error = document.createElement("div");
  error.id = "loginError";
  error.hidden = true;
  const form = document.createElement("form");
  form.id = "loginForm";
  const divider = document.createElement("div");
  divider.id = "authDivider";
  divider.hidden = true;
  const passkeyBtn = document.createElement("button");
  passkeyBtn.id = "passkeyBtn";
  passkeyBtn.type = "button";
  passkeyBtn.hidden = true;
  passkeyBtn.textContent = "Sign in with passkey";
  const oidcBtn = document.createElement("button");
  oidcBtn.id = "oidcBtn";
  oidcBtn.type = "button";
  oidcBtn.hidden = true;
  main.append(error, form, divider, passkeyBtn, oidcBtn);
  document.body.replaceChildren(main);
}

function installBoundaries(): void {
  vi.stubGlobal("fetch", (url: unknown, init?: RequestInit) => {
    if (String(url) === "/api/auth/oidc" && init?.method === "HEAD") {
      return Promise.resolve(new Response(null, { status: net.oidcStatus }));
    }
    net.finishCalls++;
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
      get: () =>
        authenticator.getThrows !== null
          ? Promise.reject(authenticator.getThrows)
          : Promise.resolve(authenticator.getResults.shift() ?? null),
    },
  });
  vi.spyOn(PublicKeyCredential, "isConditionalMediationAvailable").mockImplementation(() =>
    Promise.resolve(authenticator.conditionalAvailable),
  );
}

let bootCount = 0;

/** Evaluate a fresh login.ts and let its init() chain settle. */
async function bootLoginPage(): Promise<void> {
  bootCount++;
  await import(/* @vite-ignore */ `./login.ts?boot=${bootCount}`);
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
  }
}

function el(id: string): HTMLElement {
  const found = document.getElementById(id);
  if (found === null) {
    throw new Error(`missing #${id}`);
  }
  return found;
}

/** Record every navigation the page attempts without letting one carry the
 *  runner away; `window.location` cannot be substituted in a browser. */
function watchNavigations(): { targets: string[] } {
  const targets: string[] = [];
  const onNavigate = (ev: NavigateEvent): void => {
    targets.push(new URL(ev.destination.url).pathname);
    if (ev.cancelable) {
      ev.preventDefault();
    }
  };
  navigation.addEventListener("navigate", onNavigate);
  afterEach(() => {
    navigation.removeEventListener("navigate", onNavigate);
  });
  return { targets };
}

beforeEach(() => {
  wire.setup = { setup_required: false, config_valid: true, passkey_login_available: true };
  wire.availability = { ok: true, status: 200, data: { available: true } };
  wire.availabilityCalls = 0;
  wire.loginBegin = { ok: true, status: 200, data: beginResponse() };
  wire.loginBeginCalls = [];
  net.oidcStatus = 404;
  net.finishStatus = 200;
  net.finishBody = {};
  net.finishNotJSON = false;
  net.finishCalls = 0;
  authenticator.getResults = [];
  authenticator.getThrows = null;
  authenticator.conditionalAvailable = true;
  mountLoginPage();
  installBoundaries();
});

describe("login page: the passkey offer", () => {
  it("shows the button when the server can complete a passkey login and the origin is accepted", async () => {
    await bootLoginPage();

    expect(el("passkeyBtn").hidden).toBe(false);
    expect(el("authDivider").hidden).toBe(false);
  });

  it("hides the button when no passkey login is possible on this server, whatever the origin says", async () => {
    wire.setup = { setup_required: false, config_valid: true, passkey_login_available: false };

    await bootLoginPage();

    expect(el("passkeyBtn").hidden).toBe(true);
  });

  it("issues no availability request when the server already says no", async () => {
    wire.setup = { setup_required: false, config_valid: true, passkey_login_available: false };

    await bootLoginPage();

    expect(wire.availabilityCalls).toBe(0);
  });

  it("issues exactly one availability request per page load", async () => {
    await bootLoginPage();

    expect(wire.availabilityCalls).toBe(1);
  });

  it("hides the button when this origin is positively refused", async () => {
    wire.availability = {
      ok: true,
      status: 200,
      data: { available: false, reason: "origin_not_accepted", rp_id: "example.com" },
    };

    await bootLoginPage();

    expect(el("passkeyBtn").hidden).toBe(true);
  });

  it("still shows the button when the probe itself fails: it fails open", async () => {
    wire.availability = { ok: false, status: 500, error: "internal error" };

    await bootLoginPage();

    expect(el("passkeyBtn").hidden).toBe(false);
  });

  it("runs the autofill ceremony only when the button is offered", async () => {
    await bootLoginPage();

    expect(wire.loginBeginCalls).toEqual([{ mediation: "conditional" }]);
  });

  it("runs no autofill ceremony while the button is hidden", async () => {
    wire.setup = { setup_required: false, config_valid: true, passkey_login_available: false };

    await bootLoginPage();

    expect(wire.loginBeginCalls).toEqual([]);
  });

  it("shows the divider for SSO alone", async () => {
    wire.setup = { setup_required: false, config_valid: true, passkey_login_available: false };
    net.oidcStatus = 302;

    await bootLoginPage();

    expect(el("oidcBtn").hidden).toBe(false);
    expect(el("passkeyBtn").hidden).toBe(true);
    expect(el("authDivider").hidden).toBe(false);
  });

  it("never shows the divider when neither alternative is offered", async () => {
    wire.setup = { setup_required: false, config_valid: true, passkey_login_available: false };

    await bootLoginPage();

    expect(el("oidcBtn").hidden).toBe(true);
    expect(el("passkeyBtn").hidden).toBe(true);
    expect(el("authDivider").hidden).toBe(true);
  });
});

describe("login page: passkey outcomes", () => {
  // The explicit button is the subject; without autofill the boot spends no
  // get() on the conditional ceremony.
  beforeEach(() => {
    authenticator.conditionalAvailable = false;
  });

  async function clickPasskey(): Promise<void> {
    el("passkeyBtn").click();
    for (let i = 0; i < 4; i++) {
      await new Promise((r) => setTimeout(r, 0));
    }
  }

  it("navigates to the server's redirect on success", async () => {
    const nav = watchNavigations();
    authenticator.getResults = [assertion()];
    net.finishBody = { redirect: "/" };
    await bootLoginPage();

    await clickPasskey();

    expect(nav.targets).toEqual(["/"]);
  });

  it("renders the verification-failed sentence for a failed assertion", async () => {
    authenticator.getResults = [assertion()];
    net.finishStatus = 401;
    net.finishBody = { error: "authentication failed", code: "webauthn_assertion_failed" };
    await bootLoginPage();

    await clickPasskey();

    expect(el("loginError").textContent).toBe("Passkey verification failed. Please try again.");
  });

  it("renders the unknown-credential sentence", async () => {
    authenticator.getResults = [assertion()];
    net.finishStatus = 401;
    net.finishBody = { error: "unknown credential", signal: "unknown_credential" };
    await bootLoginPage();

    await clickPasskey();

    expect(el("loginError").textContent).toBe(
      "This passkey is not recognized. Please delete it from your authenticator and try again.",
    );
  });

  it("renders the begin fallback when a refused begin carries no message", async () => {
    wire.loginBegin = { ok: false, status: 503 };
    await bootLoginPage();

    await clickPasskey();

    expect(el("loginError").textContent).toBe("Failed to start passkey login");
  });

  it("renders the server's begin refusal when it carries one", async () => {
    wire.loginBegin = {
      ok: false,
      status: 400,
      error: "passkeys are not configured on this instance",
      code: "webauthn_unconfigured",
    };
    await bootLoginPage();

    await clickPasskey();

    expect(el("loginError").textContent).toBe("passkeys are not configured on this instance");
  });

  it("renders the finish fallback when a rejected finish carries no JSON", async () => {
    authenticator.getResults = [assertion()];
    net.finishStatus = 500;
    net.finishNotJSON = true;
    await bootLoginPage();

    await clickPasskey();

    expect(el("loginError").textContent).toBe("Passkey authentication failed");
  });

  it("restarts the autofill ceremony when the ceremony session expired", async () => {
    authenticator.conditionalAvailable = true;
    authenticator.getResults = [null, assertion()];
    net.finishStatus = 401;
    net.finishBody = { error: "invalid or expired session", code: "webauthn_session_invalid" };
    await bootLoginPage();
    const before = wire.loginBeginCalls.length;

    await clickPasskey();

    expect(wire.loginBeginCalls.slice(before)).toEqual([undefined, { mediation: "conditional" }]);
    expect(el("loginError").hidden).toBe(true);
  });

  it("stays quiet when the user dismisses the authenticator prompt", async () => {
    authenticator.getThrows = new DOMException("not allowed", "NotAllowedError");
    await bootLoginPage();

    await clickPasskey();

    expect(el("loginError").hidden).toBe(true);
  });
});
