// wizard.test.ts — Pins the wizard FLOW's safety invariants (the pure
// decision logic lives in wizard-state.test.ts):
//  - boot requires BOTH the schema and the structured config; either fetch
//    failing renders a retryable initialization error with NO step content,
//    NO navigation, and NO Finish — a Finish from an empty substituted boot
//    snapshot would PUT an empty section map and delete untouched sections;
//  - the localStorage draft is schema-sanitized: secret values and their
//    keys never reach storage, and a legacy (v1, pre-sanitization) draft is
//    removed at boot.
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { SchemaSection } from "./api-types.js";
import * as wizard from "./wizard.js";

// Hoisted mock state: dispatch mocks by action name (so tests can assert
// "no save dispatch happened") and the wire client fns (impls set per test;
// the vitest config's mockReset strips implementations before each test).
const dispatchers = vi.hoisted(() => new Map<string, ReturnType<typeof vi.fn>>());
const wire = vi.hoisted(() => ({
  configSchema: vi.fn(),
  configStructured: vi.fn(),
  validateConfigPath: vi.fn(),
  webauthnRegisterBegin: vi.fn(),
  webauthnSignalData: vi.fn(),
}));

vi.mock("./wire/client.gen.js", () => ({
  configSchema: wire.configSchema,
  configStructured: wire.configStructured,
  validateConfigPath: wire.validateConfigPath,
  webauthnRegisterBegin: wire.webauthnRegisterBegin,
  webauthnSignalData: wire.webauthnSignalData,
  PATH_SAVE_CONFIG_STRUCTURED: "/api/config/structured",
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
}));

// Plain-function factory (immune to mockReset). wizard.ts is imported ONCE
// here, so this registers the dispatch mocks once, at that import; mockReset
// clears their call history before each test, which is all these assertions
// need (no test dispatches the save action).
vi.mock("@cplieger/actions", () => ({
  apiAction: (cfg: { name: string }) => {
    const dispatch = vi.fn(() => {
      const p = Promise.resolve(undefined) as Promise<unknown> & { outcome: unknown };
      p.outcome = Promise.resolve({ status: "success", value: undefined });
      return p;
    });
    dispatchers.set(cfg.name, dispatch);
    return { dispatch, cancel: vi.fn() };
  },
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
}));

const DRAFT_KEY = "subflux-setup-draft";

/** Minimal schema covering the steps the tests render (arr inputs carry a
 *  type-"secret" api_key — the marker the draft sanitizer keys on). */
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
  ];
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

function hiddenOf(id: string): boolean {
  // The DOM lib types `hidden` as boolean | "until-found"; the fixture only
  // uses the boolean form.
  return (document.getElementById(id) as HTMLElement).hidden === true;
}

// This file imports wizard.ts ONCE, statically, and resets it between tests
// through its own `_resetForTest` export. Neither of the two module-freshness
// routes is available here:
//   - `vi.resetModules()` does not re-evaluate a module in Browser Mode (the
//     module map is URL-keyed), so it hands back the cached instance;
//   - `./wizard.ts?boot=N` -- the fix the other files in this package use --
//     mints a DUPLICATE instance, and wizard.ts is in a circular import with
//     wizard-steps.ts and wizard-providers.ts, which import `schemaByKey`,
//     `wizardValues`, `secretSaved` and `mediaRoots` back from "./wizard.js".
//     The copy sets `fullSchema` on itself while the single shared step
//     modules keep reading the original's, which stays empty. Measured: the
//     busted arr step renders its <h3> and no fields at all.
// The explicit reset is the fleet's pattern for exactly this (see
// `_resetForTest` in @cplieger/ui-primitives, `resetActionFramework` in
// @cplieger/actions). `navWired` is the binding that made the leak visible:
// left true from a previous test, `wireWizardNav()` no-ops and the freshly
// mounted page gets no nav listeners, so clicking Next never saves a draft.

/** Wait past the 150ms step fade-in used when wizardSection already holds
 *  content (the init-error UI) at render time. */
async function waitForFade(): Promise<void> {
  await new Promise((r) => setTimeout(r, 250));
}

beforeEach(() => {
  wizard._resetForTest();
  mountWizardPage();
  localStorage.clear();
});

describe("wizard: initialization failure (retryable, no partial boot)", () => {
  it("renders the retry UI when the structured config fetch fails (schema OK)", async () => {
    wire.configSchema.mockResolvedValue(schemaFixture());
    wire.configStructured.mockResolvedValue(null);

    await wizard.startConfigWizard({ configValid: false });

    // Retry UI exposed; the wizard page is shown but holds NO step content.
    expect(document.getElementById("wizardInitRetry")).not.toBeNull();
    expect(hiddenOf("configWizardPage")).toBe(false);
    expect(document.querySelector(".wiz-field")).toBeNull();

    // No navigation affordances: Back/Next hidden and — critically — no
    // Finish, so the full-map save can never run from an empty boot.
    expect(hiddenOf("wizardBack")).toBe(true);
    expect(hiddenOf("wizardNext")).toBe(true);
    expect(hiddenOf("wizardFinish")).toBe(true);
    expect(dispatchers.get("wizard.save_config")).not.toHaveBeenCalled();

    // The failure is announced via the alert region.
    expect(hiddenOf("wizardError")).toBe(false);
  });

  it("renders the same retry UI when the schema fetch fails (structured OK)", async () => {
    wire.configSchema.mockResolvedValue(null);
    wire.configStructured.mockResolvedValue({ sections: {}, secrets_present: [] });

    await wizard.startConfigWizard({ configValid: false });

    expect(document.getElementById("wizardInitRetry")).not.toBeNull();
    expect(hiddenOf("wizardFinish")).toBe(true);
    expect(dispatchers.get("wizard.save_config")).not.toHaveBeenCalled();
  });

  it("does not read or clear a stored draft while initialization fails", async () => {
    const stray = JSON.stringify({
      v: 2,
      fingerprint: "fp",
      stepId: "arr",
      touched: [],
      model: {},
    });
    localStorage.setItem(DRAFT_KEY, stray);
    wire.configSchema.mockResolvedValue(schemaFixture());
    wire.configStructured.mockResolvedValue(null);

    await wizard.startConfigWizard({ configValid: false });

    // A boot that never happened must not judge (or drop) the draft.
    expect(localStorage.getItem(DRAFT_KEY)).toBe(stray);
  });

  it("retry re-runs the boot and enters the wizard once both fetches succeed", async () => {
    wire.configSchema.mockResolvedValue(schemaFixture());
    wire.configStructured.mockResolvedValueOnce(null).mockResolvedValue({
      sections: {},
      secrets_present: [],
    });

    await wizard.startConfigWizard({ configValid: false });
    const retry = document.getElementById("wizardInitRetry") as HTMLButtonElement;
    expect(retry).not.toBeNull();

    retry.click();
    await waitForFade();

    // The full walk opens on the first step; navigation is restored.
    expect(document.querySelector(".wizard-section-title")?.textContent).toBe("Sonarr & Radarr");
    expect(document.getElementById("wizardInitRetry")).toBeNull();
    expect(hiddenOf("wizardNext")).toBe(false);
    expect(hiddenOf("wizardError")).toBe(true);
  });
});

describe("wizard: draft persistence is secret-sanitized", () => {
  it("saveDraft stores neither secret values nor secret-bearing keys", async () => {
    wire.configSchema.mockResolvedValue(schemaFixture());
    wire.configStructured.mockResolvedValue({ sections: {}, secrets_present: [] });
    await wizard.startConfigWizard({ configValid: false });

    // Fill the arr step, secrets included, then advance (Next collects the
    // step and persists the draft before validation).
    (document.getElementById("wiz-sonarr-url") as HTMLInputElement).value = "http://sonarr:8989";
    (document.getElementById("wiz-sonarr-api_key") as HTMLInputElement).value = "HUNTER2-SONARR";
    (document.getElementById("wiz-radarr-url") as HTMLInputElement).value = "http://radarr:7878";
    (document.getElementById("wiz-radarr-api_key") as HTMLInputElement).value = "HUNTER2-RADARR";
    (document.getElementById("wizardNext") as HTMLButtonElement).click();

    const raw = localStorage.getItem(DRAFT_KEY);
    expect(raw).not.toBeNull();
    expect(raw).not.toContain("HUNTER2-SONARR");
    expect(raw).not.toContain("HUNTER2-RADARR");
    expect(raw).not.toContain('"api_key"');
    // Non-secret values persist, and the draft is the sanitized v2 format.
    expect(raw).toContain("http://sonarr:8989");
    expect((JSON.parse(raw ?? "") as { v: number }).v).toBe(2);

    // Then let the click's own work finish inside this test: a valid arr step
    // advances the walk, and that render sits behind the 150ms fade. Left
    // unawaited the timer fired at an arbitrary later point in the file — it
    // rendered the media-roots step into the NEXT test under load, which is
    // what made wizard-steps.ts report 43, 50 or 57 covered statements across
    // runs of one commit.
    await waitForFade();
    expect(document.querySelector(".wizard-section-title")?.textContent).toBe("Media Roots");
  });

  it("removes a legacy v1 draft (pre-sanitization, may hold plaintext secrets) at boot", async () => {
    localStorage.setItem(
      DRAFT_KEY,
      JSON.stringify({
        v: 1,
        fingerprint: "legacy",
        stepId: "arr",
        touched: ["arr"],
        model: { wizardValues: { sonarr: { api_key: "LEAKED-PLAINTEXT" } } },
      }),
    );
    wire.configSchema.mockResolvedValue(schemaFixture());
    wire.configStructured.mockResolvedValue({ sections: {}, secrets_present: [] });

    await wizard.startConfigWizard({ configValid: false });

    expect(localStorage.getItem(DRAFT_KEY)).toBeNull();
  });
});
