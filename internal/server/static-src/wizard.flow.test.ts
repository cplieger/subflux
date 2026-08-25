// wizard.flow.test.ts — the wizard's WALK: the review screen, editing a
// collapsed step from it, Back/Next, and the async validation gate.
//
// wizard.test.ts pins the boot's safety invariants (either fetch failing must
// not produce a partial boot, and the draft must never hold a secret); the pure
// decision matrix is wizard-state.test.ts's. What was left untested is the part
// between them — what the user actually operates:
//
//  - the review screen is the fast path's landing page and the only route back
//    into a step the collapse gate hid, so a broken Edit strands a user on a
//    summary of a config they cannot change;
//  - Back must collect before it moves, or stepping back and forward silently
//    reverts what was just typed;
//  - a step's async validation gates Next, and while it is in flight the nav
//    must be busy — a second Next through an un-disabled button would advance
//    on a stale result.
//
// Finish is deliberately never clicked: it ends in navigateToApp, which assigns
// window.location.href, and neither window nor location can be substituted in a
// real browser — the assignment would reload the runner's own iframe and fail
// the file. The passkey offer sits behind the same call, so both stay for a
// harness that can own a page.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as wizard from "./wizard.js";
import { EXAMPLE_SECTIONS } from "./wizard-example.js";
import { fingerprintBoot } from "./wizard-state.js";
import type { SchemaSection } from "./api-types.js";
import type { PathValidationResponse } from "./wire/types.gen.js";

const wire = vi.hoisted(() => ({
  schema: [] as unknown,
  structured: null as unknown,
  pathResults: [] as (PathValidationResponse | null)[],
  pathDefer: false,
  pathReleases: [] as (() => void)[],
}));
vi.mock("./wire/client.gen.js", () => ({
  configSchema: () => Promise.resolve(wire.schema),
  configStructured: () => Promise.resolve(wire.structured),
  validateConfigPath: () => {
    const next = (): PathValidationResponse | null =>
      wire.pathResults.length > 0 ? (wire.pathResults.shift() ?? null) : { valid: true };
    if (wire.pathDefer) {
      return new Promise<PathValidationResponse | null>((resolve) => {
        wire.pathReleases.push(() => {
          resolve(next());
        });
      });
    }
    return Promise.resolve(next());
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

/** Sections that satisfy every mandatory step AND differ from the example, so
 *  the collapse gate hides the whole walk. The sonarr key is redacted to ""
 *  the way the server serves it, so its presence rides the secrets list. */
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

async function boot(opts: {
  configValid: boolean;
  sections?: Record<string, unknown>;
  secretsPresent?: string[];
}): Promise<void> {
  wire.schema = schemaFixture();
  wire.structured = {
    sections: opts.sections ?? {},
    secrets_present: opts.secretsPresent ?? [],
  };
  await wizard.startConfigWizard({ configValid: opts.configValid });
}

function btn(id: string): HTMLButtonElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLButtonElement)) {
    throw new Error(`no button #${id}`);
  }
  return found;
}

function input(id: string): HTMLInputElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLInputElement)) {
    throw new Error(`no input #${id}`);
  }
  return found;
}

function stepTitle(): string {
  return document.querySelector(".wizard-section-title")?.textContent ?? "";
}

function errorText(): string {
  const e = document.getElementById("wizardError");
  return e?.hidden === false ? (e.textContent ?? "") : "";
}

/** Past the 150ms step fade the wizard runs when the section already holds
 *  content, plus a tick for the awaited validation. */
async function settleStep(): Promise<void> {
  await new Promise((r) => setTimeout(r, 250));
}

function reviewRows(): { title: string; status: string }[] {
  return [...document.querySelectorAll(".wiz-review-row")].map((row) => ({
    title: row.querySelector(".wiz-review-title")?.textContent ?? "",
    status: row.querySelector(".wiz-review-status")?.textContent ?? "",
  }));
}

beforeEach(() => {
  wizard._resetForTest();
  wire.pathResults.length = 0;
  wire.pathReleases.length = 0;
  wire.pathDefer = false;
  mountWizardPage();
  localStorage.clear();
});

describe("review screen (the fast path's landing)", () => {
  it("opens directly on Review when the config already answers every mandatory step", async () => {
    await boot({
      configValid: true,
      sections: configuredSections(),
      secretsPresent: CONFIGURED_SECRETS,
    });

    expect(stepTitle()).toBe("Review & Finish");
    expect(document.querySelector(".wiz-review-intro")?.textContent).toBe(
      "Everything looks configured. Review any step below, or finish now.",
    );
  });

  it("offers Finish instead of Next on the last screen", async () => {
    await boot({
      configValid: true,
      sections: configuredSections(),
      secretsPresent: CONFIGURED_SECRETS,
    });

    expect(btn("wizardFinish").hidden).toBe(false);
    expect(btn("wizardNext").hidden).toBe(true);
  });

  it("lists every step, collapsed ones included, with where its values came from", async () => {
    await boot({
      configValid: true,
      // The scoring section is left EQUAL to the example, which is placeholder
      // data rather than a decision, so that step is not satisfied and stays
      // in the walk; every other section differs (or is absent, which also
      // differs) and reads as config-loaded.
      sections: { ...configuredSections(), scoring: EXAMPLE_SECTIONS["scoring"] },
      secretsPresent: CONFIGURED_SECRETS,
    });

    expect(reviewRows()).toStrictEqual([
      { title: "Sonarr & Radarr", status: "Loaded from your config" },
      { title: "Media Roots", status: "Loaded from your config" },
      { title: "Providers", status: "Loaded from your config" },
      { title: "Languages", status: "Loaded from your config" },
      { title: "Search Settings", status: "Loaded from your config" },
      { title: "Scoring", status: "Using defaults" },
      { title: "Post-Processing", status: "Loaded from your config" },
    ]);
  });

  it("still opens on Review when only a tunable step is unsatisfied", async () => {
    // Scoring is not one of the mandatory steps, so it joins the walk without
    // costing the fast path.
    await boot({
      configValid: true,
      sections: { ...configuredSections(), scoring: EXAMPLE_SECTIONS["scoring"] },
      secretsPresent: CONFIGURED_SECRETS,
    });

    expect(stepTitle()).toBe("Review & Finish");
    expect(btn("wizardBack").getAttribute("aria-disabled")).toBe("false");
  });

  it("walks every step and still shows Review last on a fresh boot", async () => {
    await boot({ configValid: false });

    expect(stepTitle()).toBe("Sonarr & Radarr");
    expect(btn("wizardNext").hidden).toBe(false);
    expect(btn("wizardFinish").hidden).toBe(true);
  });

  it("reads a step the user edited as updated rather than loaded", async () => {
    await boot({
      configValid: true,
      sections: configuredSections(),
      secretsPresent: CONFIGURED_SECRETS,
    });

    // Edit splices the collapsed step into the walk; Next brings the review
    // screen back with that step marked.
    const rows = [...document.querySelectorAll(".wiz-review-row")];
    rows[1]?.querySelector<HTMLButtonElement>(".wiz-review-edit")?.click();
    await settleStep();
    expect(stepTitle()).toBe("Media Roots");

    btn("wizardNext").click();
    await settleStep();

    expect(reviewRows()[1]).toStrictEqual({
      title: "Media Roots",
      status: "Updated in this setup",
    });
  });

  it("names each Edit button for the step it opens", async () => {
    await boot({
      configValid: true,
      sections: configuredSections(),
      secretsPresent: CONFIGURED_SECRETS,
    });

    expect(
      [...document.querySelectorAll(".wiz-review-edit")].map((b) => b.getAttribute("aria-label")),
    ).toStrictEqual([
      "Edit Sonarr & Radarr",
      "Edit Media Roots",
      "Edit Providers",
      "Edit Languages",
      "Edit Search Settings",
      "Edit Scoring",
      "Edit Post-Processing",
    ]);
  });

  it("splices an edited step in once, however often it is edited", async () => {
    await boot({
      configValid: true,
      sections: configuredSections(),
      secretsPresent: CONFIGURED_SECRETS,
    });

    async function editMediaRootsAndReturn(): Promise<void> {
      const rows = [...document.querySelectorAll(".wiz-review-row")];
      rows[1]?.querySelector<HTMLButtonElement>(".wiz-review-edit")?.click();
      await settleStep();
      expect(stepTitle()).toBe("Media Roots");
      btn("wizardNext").click();
      await settleStep();
    }

    await editMediaRootsAndReturn();
    // The second Edit finds the step already in the walk: a jump, not another
    // splice. A duplicate would strand the user on a second copy of the step
    // instead of returning to Review.
    await editMediaRootsAndReturn();

    expect(stepTitle()).toBe("Review & Finish");
    expect(reviewRows()).toHaveLength(7);
  });
});

describe("draft resume", () => {
  it("reopens on the step the draft was left on", async () => {
    // A reload mid-setup must not restart the walk; the draft carries the step
    // and the fingerprint of the config it was started against.
    const sections = { sonarr: { enabled: true, url: "http://sonarr:8989", api_key: "" } };
    // Dotted PATHS marking which secrets the server holds, never their values —
    // the shape `secrets_present` carries. They reach storage only through
    // fingerprintBoot, which returns a djb2 hash plus a length suffix, so no
    // secret and no path name is recoverable from the draft.
    //
    // CodeQL flags this line anyway (js/clear-text-storage-of-sensitive-data,
    // dismissed as a false positive): the taint starts at fingerprintBoot's
    // `secretsPresent` parameter and rides its return value into setItem, and a
    // hand-rolled hash is not a sanitizer the query models. Renaming the local
    // does not clear it — the source is the callee's parameter, not this name.
    const secretPaths = ["sonarr.api_key"];
    localStorage.setItem(
      "subflux-setup-draft",
      JSON.stringify({
        v: 2,
        fingerprint: fingerprintBoot(sections, secretPaths),
        stepId: "providers",
        touched: ["arr"],
        model: {
          wizardValues: { sonarr: { url: "http://sonarr:8989" } },
          providerEnabled: {},
          langRules: [],
          langDefault: [],
          mediaRoots: [],
        },
      }),
    );

    await boot({ configValid: false, sections, secretsPresent: secretPaths });

    expect(stepTitle()).toBe("Providers");
  });

  it("ignores a draft written against a different config", async () => {
    localStorage.setItem(
      "subflux-setup-draft",
      JSON.stringify({
        v: 2,
        fingerprint: "stale:0",
        stepId: "providers",
        touched: [],
        model: {
          wizardValues: {},
          providerEnabled: {},
          langRules: [],
          langDefault: [],
          mediaRoots: [],
        },
      }),
    );

    await boot({ configValid: false });

    // The config changed under the draft, so the walk restarts rather than
    // overlaying values onto a config they were never entered against.
    expect(stepTitle()).toBe("Sonarr & Radarr");
  });
});

describe("Back", () => {
  it("returns to the previous step with what was typed intact", async () => {
    await boot({ configValid: false });
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    input("wiz-sonarr-api_key").value = "k";
    btn("wizardNext").click();
    await settleStep();
    expect(stepTitle()).toBe("Media Roots");

    btn("wizardBack").click();
    await settleStep();

    // Back collects before it moves; without that, the values are read back
    // from a stale model and the user's edit is gone.
    expect(stepTitle()).toBe("Sonarr & Radarr");
    expect(input("wiz-sonarr-url").value).toBe("http://sonarr:8989");
  });

  it("does nothing on the first step, where it is marked disabled", async () => {
    await boot({ configValid: false });
    expect(btn("wizardBack").getAttribute("aria-disabled")).toBe("true");

    btn("wizardBack").click();
    await settleStep();

    expect(stepTitle()).toBe("Sonarr & Radarr");
  });

  it("keeps a step's edits when it is left through Back and re-entered", async () => {
    await boot({ configValid: false });
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    input("wiz-sonarr-api_key").value = "k";
    btn("wizardNext").click();
    await settleStep();
    input("wiz-media-root-0").value = "/data/media";

    btn("wizardBack").click();
    await settleStep();
    btn("wizardNext").click();
    await settleStep();

    expect(input("wiz-media-root-0").value).toBe("/data/media");
  });
});

describe("Next and validation", () => {
  it("refuses to advance and shows the step's own message", async () => {
    await boot({ configValid: false });

    btn("wizardNext").click();
    await settleStep();

    expect(errorText()).toBe("At least one of Sonarr or Radarr must be configured");
    expect(stepTitle()).toBe("Sonarr & Radarr");
  });

  it("clears the error once the step is fixed", async () => {
    await boot({ configValid: false });
    btn("wizardNext").click();
    await settleStep();

    input("wiz-sonarr-url").value = "http://sonarr:8989";
    input("wiz-sonarr-api_key").value = "k";
    btn("wizardNext").click();
    await settleStep();

    expect(errorText()).toBe("");
    expect(stepTitle()).toBe("Media Roots");
  });

  it("blocks on a server-rejected media root and reports the server's reason", async () => {
    wire.pathResults.push({ valid: false, error: "no such directory" });
    await boot({ configValid: false });
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    input("wiz-sonarr-api_key").value = "k";
    btn("wizardNext").click();
    await settleStep();
    input("wiz-media-root-0").value = "/data/typo";

    btn("wizardNext").click();
    await settleStep();

    expect(errorText()).toBe("/data/typo: no such directory");
    expect(stepTitle()).toBe("Media Roots");
  });

  it("marks the nav busy while an async validation is in flight", async () => {
    wire.pathDefer = true;
    await boot({ configValid: false });
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    input("wiz-sonarr-api_key").value = "k";
    btn("wizardNext").click();
    await settleStep();
    input("wiz-media-root-0").value = "/data/media";

    btn("wizardNext").click();
    await new Promise((r) => setTimeout(r, 0));

    // A live Next during the check would advance on a stale answer.
    expect(btn("wizardNext").getAttribute("aria-busy")).toBe("true");
    expect(btn("wizardNext").disabled).toBe(true);
    expect(btn("wizardNext").textContent).toContain("Checking");

    wire.pathReleases.shift()?.();
    await settleStep();

    expect(btn("wizardNext").disabled).toBe(false);
    expect(btn("wizardNext").textContent).toBe("Next");
  });

  it("advances once the server accepts every root", async () => {
    await boot({ configValid: false });
    input("wiz-sonarr-url").value = "http://sonarr:8989";
    input("wiz-sonarr-api_key").value = "k";
    btn("wizardNext").click();
    await settleStep();
    input("wiz-media-root-0").value = "/data/media";

    btn("wizardNext").click();
    await settleStep();

    expect(stepTitle()).toBe("Providers");
    expect(errorText()).toBe("");
  });
});
