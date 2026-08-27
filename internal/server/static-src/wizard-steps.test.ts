// wizard-steps.test.ts — the five schema-driven wizard steps (arr, media
// roots, search, scoring, post-processing).
//
// Every one of them is a render/collect/validate triple over wizard.ts's
// shared model, and the risks are the same shape in each: a prefill that
// renders a REDACTED secret back into a field would save the redaction over a
// working credential; a collect that replaces instead of merging drops the
// config keys the wizard deliberately does not render; and a validate that
// asks for the wrong thing dead-ends a first boot. Those three are what these
// tests aim at.
//
// The steps read wizard.ts's schema and boot snapshot, so the wizard is booted
// for real with both fetches doubled, and each step object is then driven
// directly — which is exactly how wizard.ts uses it. The wizard's OWN rendered
// step is cleared after boot, because collect() resolves ids with
// getElementById and would otherwise read the wizard's copy of a field
// instead of the one under test.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as wizard from "./wizard.js";
import {
  buildArrStep,
  buildMediaRootsStep,
  buildSearchStep,
  buildScoringStep,
  buildPostProcessStep,
  SECRET_SAVED_PLACEHOLDER,
} from "./wizard-steps.js";
import type { SchemaSection } from "./api-types.js";
import type { WizardStep } from "./wizard.js";
import type { PathValidationResponse } from "./wire/types.gen.js";

const wire = vi.hoisted(() => ({
  schema: [] as unknown,
  structured: null as unknown,
  pathResults: [] as (PathValidationResponse | null)[],
  pathChecked: [] as string[],
}));
vi.mock("./wire/client.gen.js", () => ({
  configSchema: () => Promise.resolve(wire.schema),
  configStructured: () => Promise.resolve(wire.structured),
  validateConfigPath: (body: { path: string }) => {
    wire.pathChecked.push(body.path);
    return Promise.resolve(
      wire.pathResults.length > 0 ? (wire.pathResults.shift() ?? null) : { valid: true },
    );
  },
  webauthnRegisterBegin: () => Promise.resolve(null),
  webauthnSignalData: () => Promise.resolve(null),
  // Reached only by a section's Test-connection button, which these
  // tests do not click; shaped like a real answer so a future one can.
  testConnectionRaw: () => Promise.resolve({ ok: true, status: 200, data: { valid: true } }),
  PATH_SAVE_CONFIG_STRUCTURED: "/api/config/structured",
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
}));
vi.mock("@cplieger/actions", () => ({
  apiAction: () => ({ dispatch: () => Promise.resolve(undefined), cancel: () => undefined }),
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
        { key: "url", label: "URL", type: "text", help: "Internal hostname or IP:port" },
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
      key: "search",
      title: "Search",
      type: "object",
      fields: [
        { key: "scan_interval", label: "Scan interval", type: "text", default: "24h" },
        { key: "exclude_arr_tags", label: "Exclude tags", type: "text" },
        { key: "upgrade_enabled", label: "Upgrade", type: "bool", default: "false" },
        { key: "upgrade_window_days", label: "Upgrade window", type: "number", default: "14" },
        // Deliberately outside the wizard's allow-list.
        { key: "provider_timeout", label: "Provider timeout", type: "text", default: "30s" },
      ],
    },
    {
      key: "adaptive",
      title: "Adaptive",
      type: "object",
      fields: [
        { key: "enabled", label: "Enabled", type: "bool", default: "true" },
        { key: "max_attempts", label: "Max attempts", type: "number", default: "5" },
      ],
    },
    {
      key: "scoring",
      title: "Scoring",
      type: "object",
      fields: [
        { key: "hash", label: "Hash", type: "number", default: "100" },
        { key: "release", label: "Release", type: "number", default: "50" },
      ],
    },
    {
      key: "post_processing",
      title: "Post-Processing",
      type: "object",
      fields: [
        { key: "sync_subtitles", label: "Sync", type: "bool", default: "true" },
        { key: "audio_sync_fallback", label: "Audio fallback", type: "bool", default: "false" },
        { key: "encoding", label: "Encoding", type: "text", default: "utf-8" },
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

/** Boot the wizard from a structured snapshot and hand back a container the
 *  step under test can render into. The wizard's own step render is cleared:
 *  ids are document-wide, and its copy of a field would win. */
async function boot(
  opts: { sections?: Record<string, unknown>; secretsPresent?: string[] } = {},
): Promise<HTMLElement> {
  wire.schema = schemaFixture();
  wire.structured = {
    sections: opts.sections ?? {},
    secrets_present: opts.secretsPresent ?? [],
  };
  await wizard.startConfigWizard({ configValid: false });
  document.getElementById("wizardSection")?.replaceChildren();
  const host = document.createElement("div");
  document.body.appendChild(host);
  return host;
}

/** Render a step into a fresh container — the Back/Next revisit path. */
function rerender(step: WizardStep): HTMLElement {
  const host = document.createElement("div");
  document.body.appendChild(host);
  step.render(host);
  return host;
}

function input(id: string): HTMLInputElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLInputElement)) {
    throw new Error(`no input #${id}`);
  }
  return found;
}

function buttonWithText(host: HTMLElement, text: string): HTMLButtonElement {
  const found = [...host.querySelectorAll("button")].find((b) => b.textContent === text);
  if (!found) {
    throw new Error(`no button labelled ${text}`);
  }
  return found;
}

beforeEach(() => {
  wizard._resetForTest();
  wire.pathResults.length = 0;
  wire.pathChecked.length = 0;
  mountWizardPage();
  localStorage.clear();
});

describe("arr step", () => {
  it("renders a URL and API key per arr, prefilled from the config", async () => {
    const host = await boot({
      sections: { sonarr: { url: "http://sonarr:8989", api_key: "k" } },
    });

    buildArrStep().render(host);

    expect(input("wiz-sonarr-url").value).toBe("http://sonarr:8989");
    expect(input("wiz-sonarr-api_key").value).toBe("k");
    expect(input("wiz-radarr-url").value).toBe("");
  });

  it("hangs a field's help text on the label, with nothing else to hover", async () => {
    const host = await boot({ sections: { sonarr: { url: "http://sonarr:8989" } } });

    buildArrStep().render(host);

    const label = host.querySelector<HTMLLabelElement>('label[for="wiz-sonarr-url"]');
    expect(label?.getAttribute("data-tip")).toBe("Internal hostname or IP:port");
    expect(label?.textContent).toBe("URL");
    expect(host.querySelectorAll("[data-tip] *").length).toBe(0);
    expect(input("wiz-sonarr-url").hasAttribute("data-tip")).toBe(false);
  });

  it("renders a stored secret as a keep-placeholder, never as a value", async () => {
    const host = await boot({
      sections: { sonarr: { url: "http://sonarr:8989" } },
      secretsPresent: ["sonarr.api_key"],
    });

    buildArrStep().render(host);

    expect(input("wiz-sonarr-api_key").value).toBe("");
    expect(input("wiz-sonarr-api_key").placeholder).toBe(SECRET_SAVED_PLACEHOLDER);
  });

  it("refuses a step with neither arr filled in", async () => {
    const host = await boot();
    const step = buildArrStep();
    step.render(host);

    expect(step.validate()).toBe("At least one of Sonarr or Radarr must be configured");
  });

  it("names the missing key when only a URL was given", async () => {
    const host = await boot();
    const step = buildArrStep();
    step.render(host);
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    step.collect();

    expect(step.validate()).toBe(
      "Sonarr API Key is required (or clear the Sonarr URL if you don't use Sonarr)",
    );
  });

  it("names the missing URL when only a key was given", async () => {
    const host = await boot();
    const step = buildArrStep();
    step.render(host);
    input("wiz-radarr-api_key").value = "k";
    step.collect();

    expect(step.validate()).toBe("Radarr URL is required");
  });

  it("accepts one fully configured arr and ignores the other", async () => {
    const host = await boot();
    const step = buildArrStep();
    step.render(host);
    input("wiz-radarr-url").value = "http://radarr:7878";
    input("wiz-radarr-api_key").value = "k";
    step.collect();

    expect(step.validate()).toBe("");
  });

  it("counts a stored secret as the key, so a blank field is not a failure", async () => {
    // The redacted-empty field means keep the stored value; demanding a
    // re-entry would make every wizard revisit ask for credentials again.
    const host = await boot({
      sections: { sonarr: { url: "http://sonarr:8989" } },
      secretsPresent: ["sonarr.api_key"],
    });
    const step = buildArrStep();
    step.render(host);
    step.collect();

    expect(step.validate()).toBe("");
  });

  it("carries typed values through a revisit", async () => {
    const host = await boot();
    const step = buildArrStep();
    step.render(host);
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    step.collect();
    host.remove();

    rerender(step);

    expect(input("wiz-sonarr-url").value).toBe("http://sonarr:8989");
  });
});

describe("media roots step", () => {
  it("opens with one empty row", async () => {
    const host = await boot();

    buildMediaRootsStep().render(host);

    expect(input("wiz-media-root-0").value).toBe("");
    expect(host.querySelectorAll(".wiz-lang-remove")).toHaveLength(0);
  });

  it("prefills the configured roots", async () => {
    const host = await boot({ sections: { media_roots: ["/media/tv", "/media/movies"] } });

    buildMediaRootsStep().render(host);

    expect(input("wiz-media-root-0").value).toBe("/media/tv");
    expect(input("wiz-media-root-1").value).toBe("/media/movies");
  });

  it("adds a row and keeps what was typed", async () => {
    const host = await boot();
    buildMediaRootsStep().render(host);
    input("wiz-media-root-0").value = "/media/tv";

    buttonWithText(host, "+ Add path").click();

    expect(input("wiz-media-root-0").value).toBe("/media/tv");
    expect(input("wiz-media-root-1").value).toBe("");
  });

  it("removes the pressed row and preserves the survivors", async () => {
    const host = await boot({ sections: { media_roots: ["/a", "/b", "/c"] } });
    buildMediaRootsStep().render(host);

    host.querySelectorAll<HTMLButtonElement>(".wiz-lang-remove")[1]?.click();

    expect(input("wiz-media-root-0").value).toBe("/a");
    expect(input("wiz-media-root-1").value).toBe("/c");
  });

  it("requires at least one path", async () => {
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);

    expect(step.validate()).toBe("At least one media root path is required");
  });

  it("requires container-absolute paths and names the offender", async () => {
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "media/tv";

    expect(step.validate()).toBe("Media root paths must be absolute (start with /): media/tv");
  });

  it("accepts an absolute path", async () => {
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "/media/tv";

    expect(step.validate()).toBe("");
  });

  it("checks each path against the server and reports the server's reason", async () => {
    wire.pathResults.push({ valid: false, error: "no such directory" });
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "/media/typo";

    const err = await step.validateAsync?.(new AbortController().signal);

    expect(wire.pathChecked).toStrictEqual(["/media/typo"]);
    expect(err).toBe("/media/typo: no such directory");
  });

  it("reports a request failure distinctly from an invalid path", async () => {
    // A failed request is not proof the path is wrong; saying so would send
    // the user hunting a typo that is not there.
    wire.pathResults.push(null);
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "/media/tv";

    expect(await step.validateAsync?.(new AbortController().signal)).toBe(
      "/media/tv: request failed",
    );
  });

  it("passes when the server validates every path", async () => {
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "/media/tv";
    buttonWithText(host, "+ Add path").click();
    input("wiz-media-root-1").value = "/media/movies";

    expect(await step.validateAsync?.(new AbortController().signal)).toBe("");
    expect(wire.pathChecked).toStrictEqual(["/media/tv", "/media/movies"]);
  });

  it("checks nothing once the validation is aborted", async () => {
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "/media/tv";
    const ctrl = new AbortController();
    ctrl.abort();

    expect(await step.validateAsync?.(ctrl.signal)).toBe("");
    expect(wire.pathChecked).toStrictEqual([]);
  });

  it("ignores blank rows when validating", async () => {
    const host = await boot();
    const step = buildMediaRootsStep();
    step.render(host);
    input("wiz-media-root-0").value = "/media/tv";
    buttonWithText(host, "+ Add path").click();

    expect(step.validate()).toBe("");
    expect(await step.validateAsync?.(new AbortController().signal)).toBe("");
    expect(wire.pathChecked).toStrictEqual(["/media/tv"]);
  });
});

describe("search step", () => {
  it("renders only the fields the wizard curates", async () => {
    const host = await boot();

    buildSearchStep().render(host);

    expect(document.getElementById("wiz-search-scan_interval")).not.toBeNull();
    expect(document.getElementById("wiz-search-exclude_arr_tags")).not.toBeNull();
    // provider_timeout is real config the wizard deliberately leaves alone.
    expect(document.getElementById("wiz-search-provider_timeout")).toBeNull();
  });

  it("prefills from the config and falls back to the schema default", async () => {
    const host = await boot({ sections: { search: { scan_interval: "6h" } } });

    buildSearchStep().render(host);

    expect(input("wiz-search-scan_interval").value).toBe("6h");
    expect(input("wiz-search-upgrade_window_days").value).toBe("14");
  });

  it("hides the upgrade window until upgrades are enabled", async () => {
    const host = await boot();
    buildSearchStep().render(host);
    const row = document.getElementById("wiz-search-upgrade-window-row");
    expect(row?.hidden).toBe(true);

    const cb = input("wiz-search-upgrade_enabled");
    cb.checked = true;
    cb.dispatchEvent(new Event("change", { bubbles: true }));

    expect(row?.hidden).toBe(false);
  });

  it("shows the upgrade window immediately when the config enables upgrades", async () => {
    const host = await boot({ sections: { search: { upgrade_enabled: true } } });

    buildSearchStep().render(host);

    expect(document.getElementById("wiz-search-upgrade-window-row")?.hidden).toBe(false);
  });

  it("treats adaptive backoff as on unless the config turns it off", async () => {
    const host = await boot();
    buildSearchStep().render(host);
    expect(input("wiz-adaptive-enabled").checked).toBe(true);
    expect(document.getElementById("wiz-adaptive-fields")?.getAttribute("aria-hidden")).toBe(
      "false",
    );

    host.replaceChildren();
    await boot({ sections: { adaptive: { enabled: false } } });
    buildSearchStep().render(host);

    expect(input("wiz-adaptive-enabled").checked).toBe(false);
  });

  it("collapses the adaptive fields when the toggle goes off", async () => {
    const host = await boot();
    buildSearchStep().render(host);
    const fields = document.getElementById("wiz-adaptive-fields");

    const cb = input("wiz-adaptive-enabled");
    cb.checked = false;
    cb.dispatchEvent(new Event("change", { bubbles: true }));

    expect(fields?.getAttribute("aria-hidden")).toBe("true");
  });

  it("keeps prefilled config the step never renders", async () => {
    // The step curates four of the search section's fields; a collect that
    // REPLACED the map would drop provider_timeout out of the model.
    const host = await boot({
      sections: { search: { provider_timeout: "45s", scan_interval: "6h" } },
    });
    const step = buildSearchStep();
    step.render(host);
    input("wiz-search-scan_interval").value = "12h";

    step.collect();
    host.remove();
    rerender(step);

    expect(input("wiz-search-scan_interval").value).toBe("12h");
    expect(wizard.wizardValues["search"]?.["provider_timeout"]).toBe("45s");
  });

  it("records the adaptive toggle and fields", async () => {
    const host = await boot();
    const step = buildSearchStep();
    step.render(host);
    input("wiz-adaptive-enabled").checked = false;
    input("wiz-adaptive-max_attempts").value = "9";

    step.collect();

    expect(wizard.wizardValues["adaptive"]).toStrictEqual({
      enabled: "false",
      max_attempts: "9",
    });
  });

  it("never blocks the walk", async () => {
    const host = await boot();
    const step = buildSearchStep();
    step.render(host);

    expect(step.validate()).toBe("");
  });
});

describe("scoring step", () => {
  it("renders a number field per weight, prefilled from the config", async () => {
    const host = await boot({ sections: { scoring: { weights: { hash: 90 } } } });

    buildScoringStep().render(host);

    expect(input("wiz-scoring-hash").value).toBe("90");
    expect(input("wiz-scoring-hash").type).toBe("number");
    // No configured value: the schema default rides in the placeholder, so the
    // save omits the field and the server default stands.
    expect(input("wiz-scoring-release").value).toBe("");
    expect(input("wiz-scoring-release").placeholder).toBe("50");
  });

  it("accepts whole numbers and blanks", async () => {
    const host = await boot();
    const step = buildScoringStep();
    step.render(host);
    input("wiz-scoring-hash").value = "80";
    step.collect();

    expect(step.validate()).toBe("");
  });

  it("refuses a non-integer weight and names the field", async () => {
    const host = await boot();
    const step = buildScoringStep();
    step.render(host);
    input("wiz-scoring-hash").value = "80.5";
    step.collect();

    expect(step.validate()).toBe("Scoring weight hash must be a whole number");
  });

  it("carries the weights through a revisit", async () => {
    const host = await boot();
    const step = buildScoringStep();
    step.render(host);
    input("wiz-scoring-release").value = "30";
    step.collect();
    host.remove();

    rerender(step);

    expect(input("wiz-scoring-release").value).toBe("30");
  });
});

describe("post-processing step", () => {
  it("takes each bool's default from the schema", async () => {
    const host = await boot();

    buildPostProcessStep().render(host);

    expect(input("wiz-pp-sync_subtitles").checked).toBe(true);
    expect(input("wiz-pp-encoding").value).toBe("");
    expect(input("wiz-pp-encoding").placeholder).toBe("utf-8");
  });

  it("prefers a configured value over the schema default", async () => {
    const host = await boot({
      sections: { post_processing: { sync_subtitles: false, encoding: "latin-1" } },
    });

    buildPostProcessStep().render(host);

    expect(input("wiz-pp-sync_subtitles").checked).toBe(false);
    expect(input("wiz-pp-encoding").value).toBe("latin-1");
  });

  it("disables the audio fallback while subtitle sync is off, and forces it off", async () => {
    // Audio sync is a fallback WITHIN sync; leaving it settable would save a
    // combination the server treats as inconsistent.
    const host = await boot({
      sections: { post_processing: { sync_subtitles: false, audio_sync_fallback: true } },
    });

    buildPostProcessStep().render(host);

    expect(input("wiz-pp-audio_sync_fallback").disabled).toBe(true);
    expect(input("wiz-pp-audio_sync_fallback").checked).toBe(false);
  });

  it("enables the audio fallback as soon as sync is switched on", async () => {
    const host = await boot({ sections: { post_processing: { sync_subtitles: false } } });
    buildPostProcessStep().render(host);

    const sync = input("wiz-pp-sync_subtitles");
    sync.checked = true;
    sync.dispatchEvent(new Event("change", { bubbles: true }));

    expect(input("wiz-pp-audio_sync_fallback").disabled).toBe(false);
  });

  it("records bools as strings and never blocks the walk", async () => {
    const host = await boot();
    const step = buildPostProcessStep();
    step.render(host);
    input("wiz-pp-encoding").value = "utf-8";

    step.collect();

    expect(wizard.wizardValues["post_processing"]).toStrictEqual({
      sync_subtitles: "true",
      audio_sync_fallback: "false",
      encoding: "utf-8",
    });
    expect(step.validate()).toBe("");
  });
});
