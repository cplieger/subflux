// wizard.offer.test.ts — the passkey offer, the wizard's TERMINAL screen.
//
// wizard.flow.test.ts walks the steps and never clicks Finish, because Finish
// ends in a navigation and neither `window` nor `location` can be substituted
// in a real browser. This file can click it: the navigation lives in
// nav-app.ts, so replacing that one import is the seam the non-configurable
// globals otherwise deny.
//
// The offer is not a step. Finish PUTs the config, then showPasskeyOffer takes
// over #wizardSection and dismantles the wizard chrome — Back, Next, Finish
// and the progress dots — because the screen owns its own action row. What is
// pinned here is that the chrome cannot come BACK: withWizardBusy's finally
// re-ran the nav updater after the takeover, and the step index still pointed
// at Review, so a Back and a Finish button were resurrected beside the offer's
// own action row. That Finish re-entered the save with every step's fields
// gone from the DOM, which wizard.test.ts documents as the shape that PUTs an
// empty section map and deletes untouched sections.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as wizard from "./wizard.js";
import { EXAMPLE_SECTIONS } from "./wizard-example.js";
import type { SchemaSection } from "./api-types.js";
import type { WebAuthnAvailability } from "./wire/types.gen.js";

const wire = vi.hoisted(() => ({
  schema: [] as unknown,
  structured: null as unknown,
  availability: null as WebAuthnAvailability | null,
  availabilityCalls: 0,
}));
const nav = vi.hoisted(() => ({ navigateToApp: vi.fn() }));

vi.mock("./wire/client.gen.js", () => ({
  configSchema: () => Promise.resolve(wire.schema),
  configStructured: () => Promise.resolve(wire.structured),
  validateConfigPath: () => Promise.resolve({ valid: true }),
  webauthnAvailabilityRaw: () => {
    wire.availabilityCalls++;
    return Promise.resolve(
      wire.availability === null
        ? { ok: true, status: 200, data: { available: false, reason: "unconfigured" } }
        : { ok: true, status: 200, data: wire.availability },
    );
  },
  // Reached only by the offer's Add-passkey button, which these tests do not
  // click; the ceremony itself is security.test.ts's subject. The rest is the
  // ceremony module's import list, which the mock must link in full.
  webauthnRegisterBeginRaw: () => Promise.resolve({ ok: false, status: 400 }),
  webauthnLoginBeginRaw: () => Promise.resolve({ ok: false, status: 400 }),
  webauthnSignalData: () => Promise.resolve(null),
  PATH_WEBAUTHN_LOGIN_FINISH: "/api/auth/webauthn/login/finish",
  testConnectionRaw: () => Promise.resolve({ ok: true, status: 200, data: { valid: true } }),
  PATH_SAVE_CONFIG_STRUCTURED: "/api/config/structured",
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
}));

vi.mock("./nav-app.js", () => ({ navigateToApp: nav.navigateToApp }));

// Plain-function factory, immune to the config's mockReset. The save must
// report success or Finish stops at the error banner and never reaches the
// offer; the PUT itself is wizard.test.ts's subject.
vi.mock("@cplieger/actions", () => ({
  apiAction: () => ({
    dispatch: () => ({ outcome: Promise.resolve({ status: "success", value: undefined }) }),
    cancel: () => undefined,
  }),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
}));

function schemaFixture(): SchemaSection[] {
  return [
    {
      key: "sonarr",
      title: "Sonarr",
      type: "object",
      fields: [
        { key: "url", label: "URL", type: "text" },
        { key: "api_key", label: "API Key", type: "secret" },
      ],
    },
    {
      key: "radarr",
      title: "Radarr",
      type: "object",
      fields: [
        { key: "url", label: "URL", type: "text" },
        { key: "api_key", label: "API Key", type: "secret" },
      ],
    },
    {
      key: "providers",
      title: "Providers",
      type: "providers",
      providers: [{ name: "subdl", label: "SubDL", settings: [] }],
    },
    { key: "search", title: "Search", type: "object", fields: [] },
    { key: "scoring", title: "Scoring", type: "object", fields: [] },
    { key: "post_processing", title: "Post-Processing", type: "object", fields: [] },
  ] as SchemaSection[];
}

/** Sections satisfying every mandatory step and differing from the example, so
 *  the walk collapses and the wizard opens on Review — one Finish away from
 *  the offer. The sonarr key is redacted to "" the way the server serves it,
 *  so its presence rides the secrets list. */
function configuredSections(): Record<string, unknown> {
  return {
    sonarr: { enabled: true, url: "http://sonarr.local:8989", api_key: "" },
    radarr: EXAMPLE_SECTIONS["radarr"],
    media_roots: ["/data/media"],
    providers: { subdl: { enabled: true } },
    languages: { default: [{ code: "de" }] },
  };
}

const CONFIGURED_SECRETS = ["sonarr.api_key"];

/** An available probe: the server answers this only once a WebAuthn RP ID is
 *  configured and covers this origin, which is what the offer branches on. */
function availabilityFixture(): WebAuthnAvailability {
  return { available: true };
}

function mountWizardPage(): void {
  document.body.innerHTML = `
    <main id="configWizardPage" class="auth-page" hidden>
      <div id="wizardError" class="auth-error" role="alert" hidden></div>
      <div id="wizardSection" class="wizard-section"></div>
      <div class="wizard-nav">
        <button type="button" id="wizardBack" aria-disabled="true">Back</button>
        <div id="wizardProgress"></div>
        <button type="button" id="wizardNext">Next</button>
        <button type="button" id="wizardFinish" hidden>Finish</button>
      </div>
    </main>`;
}

function btn(id: string): HTMLButtonElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLButtonElement)) {
    throw new Error(`no button #${id}`);
  }
  return found;
}

function heading(): string {
  return document.querySelector(".wizard-section-title")?.textContent ?? "";
}

function reasonText(): string {
  return document.querySelector(".wiz-offer-reason")?.textContent ?? "";
}

function offerActionLabels(): string[] {
  return [...document.querySelectorAll(".wiz-offer-actions button")].map(
    (b) => b.textContent ?? "",
  );
}

/** The offer's action buttons are keyed by their label, not their position or
 *  class, so a test about one button's behaviour cannot fail for the other's. */
function offerAction(label: string): HTMLButtonElement | undefined {
  return [...document.querySelectorAll<HTMLButtonElement>(".wiz-offer-actions button")].find(
    (b) => b.textContent === label,
  );
}

function progressDots(): number {
  return document.getElementById("wizardProgress")?.childElementCount ?? -1;
}

/** One macrotask clears the whole Finish chain: the save outcome and the
 *  availability probe are both already-resolved promises. */
async function settle(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
}

/** Past the 150ms fade a step repaint runs when the section already holds
 *  content. */
async function settleStep(): Promise<void> {
  await new Promise((r) => setTimeout(r, 250));
}

/** Boots onto Review and clicks Finish, which lands on the offer screen.
 *  `sections` overrides let a caller keep a step in the walk; `password` is
 *  the memory-only one the offer needs to enroll a passkey at all. */
async function finishInto(opts: {
  password?: string;
  extraSections?: Record<string, unknown>;
}): Promise<void> {
  wire.schema = schemaFixture();
  wire.structured = {
    sections: { ...configuredSections(), ...opts.extraSections },
    secrets_present: CONFIGURED_SECRETS,
  };
  await wizard.startConfigWizard(
    opts.password === undefined
      ? { configValid: true }
      : { configValid: true, password: opts.password },
  );
  expect(heading()).toBe("Review & Finish");

  btn("wizardFinish").click();
  await settle();
}

beforeEach(() => {
  wizard._resetForTest();
  wire.availability = null;
  wire.availabilityCalls = 0;
  mountWizardPage();
  localStorage.clear();
});

describe("passkeys unavailable", () => {
  it("clears the wizard chrome and keeps it cleared once the offer is up", async () => {
    await finishInto({ password: "pw" });

    // The resurrection: withWizardBusy's finally re-runs the nav updater after
    // showPasskeyOffer has already replaced the chrome, and the step index
    // still points at Review, so Back reappears and Finish is un-hidden.
    expect(heading()).toBe("Add a passkey?");
    expect(btn("wizardBack").hidden).toBe(true);
    expect(btn("wizardNext").hidden).toBe(true);
    expect(btn("wizardFinish").hidden).toBe(true);
    expect(progressDots()).toBe(0);
  });

  it("keeps the chrome cleared when a later repaint re-derives the nav state", async () => {
    // Scoring stays in the walk, so the walk has a step to go back TO. Back is
    // hidden from the user by the takeover; the programmatic click is how this
    // test reaches the one remaining caller of the nav updater, and it is what
    // separates a terminal mode from re-hiding the buttons after the fact.
    await finishInto({ password: "pw", extraSections: { scoring: EXAMPLE_SECTIONS["scoring"] } });
    expect(heading()).toBe("Add a passkey?");

    btn("wizardBack").click();
    await settleStep();

    expect(btn("wizardBack").hidden).toBe(true);
    expect(btn("wizardNext").hidden).toBe(true);
    expect(btn("wizardFinish").hidden).toBe(true);
  });

  it("offers exactly one action, labelled Finish", async () => {
    await finishInto({ password: "pw" });

    expect(offerActionLabels()).toStrictEqual(["Finish"]);
  });

  it("carries no Continue-to-Subflux button", async () => {
    await finishInto({ password: "pw" });

    expect(document.getElementById("wizardOfferContinue")).toBeNull();
  });

  it("navigates into the app when Finish is clicked", async () => {
    await finishInto({ password: "pw" });

    offerAction("Finish")?.click();

    expect(nav.navigateToApp).toHaveBeenCalledTimes(1);
  });

  it("says why passkeys are unavailable and where to configure them", async () => {
    await finishInto({ password: "pw" });

    expect(reasonText()).toBe(
      "Passkeys are not set up yet because no relying-party ID is configured. " +
        "Save your settings once from this address and subflux fills it in, " +
        "or set auth.webauthn_rp_id in the Authentication section of Settings.",
    );
  });
});

describe("passkeys available", () => {
  // The offer's other two conditions are the platform's own: this file is
  // served from a secure context, so window.PublicKeyCredential is a real
  // constructor and no stand-in is needed. Were that ever false, every test
  // below would render the unavailable branch and fail on the action labels.
  beforeEach(() => {
    wire.availability = availabilityFixture();
  });

  it("offers Add passkey beside a ghost Skip for now", async () => {
    await finishInto({ password: "pw" });

    expect(offerActionLabels()).toStrictEqual(["Add passkey", "Skip for now"]);
    expect(offerAction("Skip for now")?.className).toBe("ghost");
  });

  it("navigates into the app when Skip for now is clicked", async () => {
    await finishInto({ password: "pw" });

    offerAction("Skip for now")?.click();

    expect(nav.navigateToApp).toHaveBeenCalledTimes(1);
  });

  it("declines the offer without a password, which register/begin requires", async () => {
    await finishInto({});

    expect(offerActionLabels()).toStrictEqual(["Finish"]);
    expect(reasonText()).toBe(
      "Passkey enrollment needs your password to confirm. You can add one any time from the Security dialog.",
    );
  });

  it("spends no availability request when the password check already declines", async () => {
    await finishInto({});

    expect(wire.availabilityCalls).toBe(0);
  });
});
