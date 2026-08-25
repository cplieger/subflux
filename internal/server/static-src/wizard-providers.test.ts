// wizard-providers.test.ts — the first-boot wizard's providers step.
//
// The interesting rule is the one its own comment defends: a provider the
// config file ALREADY enables is config-blessed, so the step must not demand
// credentials for it. The shipped example config enables credential-free
// providers, so a validate() that second-guessed the boot config would refuse
// to let a fresh install past the step it just prefilled — a dead end with no
// way forward but disabling a provider that works.
//
// The step reads wizard.ts's schema and boot snapshot, so these tests boot the
// wizard for real (both fetches doubled) and then drive the step object
// directly, which is what wizard.ts does with it.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as wizard from "./wizard.js";
import { buildProvidersStep } from "./wizard-providers.js";
import type { SchemaSection } from "./api-types.js";

const wire = vi.hoisted(() => ({
  schema: [] as unknown,
  structured: null as unknown,
}));
vi.mock("./wire/client.gen.js", () => ({
  configSchema: () => Promise.resolve(wire.schema),
  configStructured: () => Promise.resolve(wire.structured),
  validateConfigPath: () => Promise.resolve(null),
  webauthnRegisterBegin: () => Promise.resolve(null),
  webauthnSignalData: () => Promise.resolve(null),
  // Reached only by the arr sections' Test-connection button, which these
  // tests do not click; shaped like a real answer so a future one can.
  testArrConnectionRaw: () => Promise.resolve({ ok: true, status: 200, data: { valid: true } }),
  PATH_SAVE_CONFIG_STRUCTURED: "/api/config/structured",
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
}));
vi.mock("@cplieger/actions", () => ({
  apiAction: () => ({ dispatch: () => Promise.resolve(undefined), cancel: () => undefined }),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
}));

/** A providers schema section: one credential-bearing provider, one with only
 *  a bool setting, one with no settings at all. */
function schemaFixture(): SchemaSection[] {
  return [
    {
      key: "providers",
      title: "Providers",
      type: "providers",
      providers: [
        {
          name: "subdl",
          label: "SubDL",
          settings: [
            { key: "api_key", label: "API Key", type: "text", secret: true },
            { key: "prefer_hi", label: "Prefer HI", type: "bool", default: "true" },
          ],
        },
        {
          name: "animetosho",
          label: "AnimeTosho",
          settings: [{ key: "only_batch", label: "Only batch", type: "bool" }],
        },
        { name: "gestdown", label: "Gestdown" },
      ],
    },
  ] as SchemaSection[];
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

/** Boot the wizard from a structured-config snapshot, then hand back a
 *  freshly rendered providers step in its own container. */
async function boot(opts: {
  sections?: Record<string, unknown>;
  secretsPresent?: string[];
}): Promise<HTMLElement> {
  wire.schema = schemaFixture();
  wire.structured = {
    sections: opts.sections ?? {},
    secrets_present: opts.secretsPresent ?? [],
  };
  await wizard.startConfigWizard({ configValid: false });
  const host = document.createElement("div");
  document.body.appendChild(host);
  buildProvidersStep().render(host);
  return host;
}

function checkbox(id: string): HTMLInputElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLInputElement)) {
    throw new Error(`no checkbox #${id}`);
  }
  return found;
}

function textInput(id: string): HTMLInputElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLInputElement)) {
    throw new Error(`no input #${id}`);
  }
  return found;
}

const SAVED_PLACEHOLDER = "\u2022\u2022\u2022\u2022 saved \u2014 leave blank to keep";

beforeEach(() => {
  wizard._resetForTest();
  mountWizardPage();
  localStorage.clear();
});

describe("providers step: rendering", () => {
  it("renders one card per schema provider, labelled", async () => {
    const host = await boot({});

    expect([...host.querySelectorAll(".wiz-prov-name")].map((e) => e.textContent)).toStrictEqual([
      "SubDL",
      "AnimeTosho",
      "Gestdown",
    ]);
  });

  it("comes up unchecked and collapsed for a provider the config does not mention", async () => {
    const host = await boot({});

    expect(checkbox("wiz-prov-subdl").checked).toBe(false);
    expect(host.querySelector(".wiz-prov-card")?.classList.contains("open")).toBe(false);
  });

  it("comes up checked and open for a provider the config enables", async () => {
    const host = await boot({ sections: { providers: { subdl: { enabled: true } } } });

    expect(checkbox("wiz-prov-subdl").checked).toBe(true);
    expect(host.querySelector(".wiz-prov-card")?.classList.contains("open")).toBe(true);
  });

  it("treats a bare provider entry as enabled", async () => {
    // Settings-dialog semantics: a present block without enabled:false counts.
    await boot({ sections: { providers: { subdl: null } } });

    expect(checkbox("wiz-prov-subdl").checked).toBe(true);
  });

  it("prefills a saved non-secret setting", async () => {
    await boot({
      sections: { providers: { subdl: { enabled: true, settings: { api_key: "typed-in-yaml" } } } },
    });

    expect(textInput("wiz-prov-subdl-api_key").value).toBe("typed-in-yaml");
  });

  it("renders a stored secret as a keep-placeholder, never as a value", async () => {
    // The server redacts secrets, so a value here would save the redaction
    // back over the real key. The presence flag is what the step gets.
    await boot({
      sections: { providers: { subdl: { enabled: true } } },
      secretsPresent: ["providers.subdl.settings.api_key"],
    });

    expect(textInput("wiz-prov-subdl-api_key").value).toBe("");
    expect(textInput("wiz-prov-subdl-api_key").placeholder).toBe(SAVED_PLACEHOLDER);
  });

  it("takes a bool setting's default when the config has no value", async () => {
    await boot({});

    expect(checkbox("wiz-prov-subdl-prefer_hi").checked).toBe(true);
    expect(checkbox("wiz-prov-animetosho-only_batch").checked).toBe(false);
  });

  it("prefers the config's bool value over the schema default", async () => {
    await boot({
      sections: { providers: { subdl: { enabled: true, settings: { prefer_hi: false } } } },
    });

    expect(checkbox("wiz-prov-subdl-prefer_hi").checked).toBe(false);
  });

  it("gives a settings-less provider a toggle and no body", async () => {
    const host = await boot({});

    expect(checkbox("wiz-prov-gestdown")).not.toBeNull();
    const cards = [...host.querySelectorAll(".wiz-prov-card")];
    expect(cards[2]?.querySelector(".wiz-prov-body")).toBeNull();
  });

  it("renders nothing when the schema has no providers section", async () => {
    wire.schema = [];
    wire.structured = { sections: {}, secrets_present: [] };
    await wizard.startConfigWizard({ configValid: false });
    const host = document.createElement("div");

    buildProvidersStep().render(host);

    expect(host.children).toHaveLength(0);
  });
});

describe("providers step: toggling", () => {
  it("opens the settings body when the provider is switched on", async () => {
    const host = await boot({});
    const body = host.querySelector(".wiz-prov-body");
    expect(body?.getAttribute("aria-hidden")).toBe("true");

    const cb = checkbox("wiz-prov-subdl");
    cb.checked = true;
    cb.dispatchEvent(new Event("change", { bubbles: true }));

    expect(body?.getAttribute("aria-hidden")).toBe("false");
    expect(host.querySelector(".wiz-prov-card")?.classList.contains("open")).toBe(true);
  });

  it("closes it again when switched off", async () => {
    const host = await boot({ sections: { providers: { subdl: { enabled: true } } } });
    const body = host.querySelector(".wiz-prov-body");

    const cb = checkbox("wiz-prov-subdl");
    cb.checked = false;
    cb.dispatchEvent(new Event("change", { bubbles: true }));

    expect(body?.getAttribute("aria-hidden")).toBe("true");
    expect(host.querySelector(".wiz-prov-card")?.classList.contains("open")).toBe(false);
  });
});

describe("providers step: collect", () => {
  it("carries the toggle and the typed settings through a re-render", async () => {
    await boot({});
    checkbox("wiz-prov-subdl").checked = true;
    textInput("wiz-prov-subdl-api_key").value = "k-123";
    checkbox("wiz-prov-subdl-prefer_hi").checked = false;

    buildProvidersStep().collect();
    const second = document.createElement("div");
    document.body.appendChild(second);
    buildProvidersStep().render(second);

    // Revisiting the step (Back, then Next) must not lose the entry.
    expect(checkbox("wiz-prov-subdl").checked).toBe(true);
    expect(textInput("wiz-prov-subdl-api_key").value).toBe("k-123");
    expect(checkbox("wiz-prov-subdl-prefer_hi").checked).toBe(false);
  });
});

describe("providers step: validate", () => {
  it("passes when nothing is enabled", async () => {
    await boot({});

    expect(buildProvidersStep().validate()).toBe("");
  });

  it("refuses a provider enabled in the FORM with no credentials", async () => {
    await boot({});
    checkbox("wiz-prov-subdl").checked = true;

    expect(buildProvidersStep().validate()).toBe(
      "SubDL is enabled but has no credentials configured. " +
        "Disable it or fill in the required fields.",
    );
  });

  it("passes once a credential is typed", async () => {
    await boot({});
    checkbox("wiz-prov-subdl").checked = true;
    textInput("wiz-prov-subdl-api_key").value = "k-123";

    expect(buildProvidersStep().validate()).toBe("");
  });

  it("passes on a stored secret with the field left blank", async () => {
    await boot({ secretsPresent: ["providers.subdl.settings.api_key"] });
    checkbox("wiz-prov-subdl").checked = true;

    expect(buildProvidersStep().validate()).toBe("");
  });

  it("exempts a provider the config file already enables", async () => {
    // Config-blessed: the server validated this config at boot, so its
    // credentials are optional by proof. Without the exemption a fresh install
    // cannot get past a step prefilled from the shipped example.
    await boot({ sections: { providers: { subdl: { enabled: true } } } });

    expect(buildProvidersStep().validate()).toBe("");
  });

  it("still refuses a provider the config file explicitly disables", async () => {
    await boot({ sections: { providers: { subdl: { enabled: false } } } });
    checkbox("wiz-prov-subdl").checked = true;

    expect(buildProvidersStep().validate()).toContain("SubDL is enabled but has no credentials");
  });

  it("never demands credentials from a provider whose settings are all bools", async () => {
    await boot({});
    checkbox("wiz-prov-animetosho").checked = true;

    expect(buildProvidersStep().validate()).toBe("");
  });

  it("never demands credentials from a provider with no settings", async () => {
    await boot({});
    checkbox("wiz-prov-gestdown").checked = true;

    expect(buildProvidersStep().validate()).toBe("");
  });

  it("passes when the schema has no providers section", async () => {
    wire.schema = [];
    wire.structured = { sections: {}, secrets_present: [] };
    await wizard.startConfigWizard({ configValid: false });

    expect(buildProvidersStep().validate()).toBe("");
  });
});
