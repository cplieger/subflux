// wizard-languages.test.ts — the first-boot wizard's languages step.
//
// This step decides what subflux downloads for the rest of the install, and
// its state lives in wizard.ts's shared `langDefault` / `langRules` arrays
// rather than in the DOM: render paints from the arrays, collect reads the
// selects back into them, and the add/remove buttons collect BEFORE mutating.
// That ordering is the whole correctness story — a remove that splices before
// collecting loses whatever the user typed into the other rows, and it looks
// fine on screen until the wizard finishes and writes the wrong config.
//
// The step is driven directly (buildLanguagesStep().render/collect/validate),
// which is how wizard.ts drives it, with wizard's module state reset between
// tests through its own _resetForTest — Browser Mode cannot re-evaluate the
// module and a cache-busted specifier is closed here, because wizard.ts sits
// in an import cycle with the step modules.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as wizard from "./wizard.js";
import { buildLanguagesStep } from "./wizard-languages.js";

// wizard.ts reaches the network at module scope only through these two, and
// builds its action definitions from @cplieger/actions at import; both are
// doubled the way wizard.test.ts does it (plain functions, immune to
// mockReset).
vi.mock("./wire/client.gen.js", () => ({
  configSchema: () => Promise.resolve(null),
  configStructured: () => Promise.resolve(null),
  validateConfigPath: () => Promise.resolve(null),
  webauthnRegisterBegin: () => Promise.resolve(null),
  webauthnSignalData: () => Promise.resolve(null),
  PATH_SAVE_CONFIG_STRUCTURED: "/api/config/structured",
  PATH_WEBAUTHN_REGISTER_FINISH: "/api/auth/webauthn/register/finish",
}));
vi.mock("@cplieger/actions", () => ({
  apiAction: () => ({ dispatch: () => Promise.resolve(undefined), cancel: () => undefined }),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
}));

const step = (): ReturnType<typeof buildLanguagesStep> => buildLanguagesStep();

/** Render the step into an attached container — collect reads the selects
 *  back by id, so the form has to be in the document. */
function render(): HTMLElement {
  const host = document.createElement("div");
  document.body.replaceChildren(host);
  step().render(host);
  return host;
}

function sel(id: string): HTMLSelectElement {
  const found = document.getElementById(id);
  if (!(found instanceof HTMLSelectElement)) {
    throw new Error(`no select #${id}`);
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

function removeButtons(containerId: string): HTMLButtonElement[] {
  const container = document.getElementById(containerId);
  return [...(container?.querySelectorAll<HTMLButtonElement>(".wiz-lang-remove") ?? [])];
}

beforeEach(() => {
  wizard._resetForTest();
  document.body.replaceChildren();
});

describe("languages step: defaults", () => {
  it("opens with one empty default row so the step is never blank", () => {
    render();

    expect(sel("wiz-lang-def-code-0").value).toBe("");
    expect(sel("wiz-lang-def-variant-0").value).toBe("standard");
  });

  it("offers no remove button while one default is left", () => {
    // The step's own rule: the last default cannot be removed, because a
    // wizard with no defaults and no rules cannot be finished.
    render();

    expect(removeButtons("wiz-lang-defaults")).toHaveLength(0);
  });

  it("adds a default row and keeps what was already chosen", () => {
    const host = render();
    sel("wiz-lang-def-code-0").value = "en";

    buttonWithText(host, "+ Add default").click();

    expect(sel("wiz-lang-def-code-0").value).toBe("en");
    expect(sel("wiz-lang-def-code-1").value).toBe("");
    expect(removeButtons("wiz-lang-defaults")).toHaveLength(2);
  });

  it("removes the pressed row and preserves the survivors' values", () => {
    const host = render();
    sel("wiz-lang-def-code-0").value = "en";
    buttonWithText(host, "+ Add default").click();
    sel("wiz-lang-def-code-1").value = "fr";
    buttonWithText(host, "+ Add default").click();
    sel("wiz-lang-def-code-2").value = "de";

    removeButtons("wiz-lang-defaults")[1]?.click();

    // The remove collects first; skipping that step would rebuild the rows
    // from stale array entries and lose "de".
    expect(sel("wiz-lang-def-code-0").value).toBe("en");
    expect(sel("wiz-lang-def-code-1").value).toBe("de");
  });

  it("carries the chosen variant through a re-render", () => {
    const host = render();
    sel("wiz-lang-def-variant-0").value = "forced";

    buttonWithText(host, "+ Add default").click();

    expect(sel("wiz-lang-def-variant-0").value).toBe("forced");
  });
});

describe("languages step: audio rules", () => {
  it("starts with no rules, since they are optional", () => {
    render();

    expect(document.getElementById("wiz-lang-rule-audio-0")).toBeNull();
  });

  it("adds a rule row with an audio language, a subtitle language and a variant", () => {
    const host = render();

    buttonWithText(host, "+ Add rule").click();

    expect(sel("wiz-lang-rule-audio-0").value).toBe("");
    expect(sel("wiz-lang-rule-code-0").value).toBe("");
    expect(sel("wiz-lang-rule-variant-0").value).toBe("standard");
  });

  it("offers a remove button on the only rule, unlike the defaults", () => {
    const host = render();

    buttonWithText(host, "+ Add rule").click();

    expect(removeButtons("wiz-lang-rules")).toHaveLength(1);
  });

  it("removes the pressed rule and preserves the survivors", () => {
    const host = render();
    buttonWithText(host, "+ Add rule").click();
    sel("wiz-lang-rule-audio-0").value = "ja";
    sel("wiz-lang-rule-code-0").value = "en";
    buttonWithText(host, "+ Add rule").click();
    sel("wiz-lang-rule-audio-1").value = "en";
    sel("wiz-lang-rule-code-1").value = "fr";

    removeButtons("wiz-lang-rules")[0]?.click();

    expect(sel("wiz-lang-rule-audio-0").value).toBe("en");
    expect(sel("wiz-lang-rule-code-0").value).toBe("fr");
    expect(document.getElementById("wiz-lang-rule-audio-1")).toBeNull();
  });
});

describe("languages step: collect", () => {
  it("survives a step revisit, which is what makes Back non-destructive", () => {
    const first = render();
    sel("wiz-lang-def-code-0").value = "pb";
    buttonWithText(first, "+ Add rule").click();
    sel("wiz-lang-rule-audio-0").value = "ja";
    sel("wiz-lang-rule-code-0").value = "en";
    sel("wiz-lang-rule-variant-0").value = "hi";
    step().collect();

    // Re-rendering is what Back/Next does; the values come from the shared
    // arrays, not from the discarded DOM.
    render();

    expect(sel("wiz-lang-def-code-0").value).toBe("pb");
    expect(sel("wiz-lang-rule-audio-0").value).toBe("ja");
    expect(sel("wiz-lang-rule-code-0").value).toBe("en");
    expect(sel("wiz-lang-rule-variant-0").value).toBe("hi");
  });
});

describe("languages step: validate", () => {
  it("refuses a step with neither a default nor a rule", () => {
    render();

    expect(step().validate()).toBe("At least one language default or rule must be configured");
  });

  it("accepts one default language", () => {
    render();
    sel("wiz-lang-def-code-0").value = "en";

    expect(step().validate()).toBe("");
  });

  it("accepts a rule with no defaults at all", () => {
    const host = render();
    buttonWithText(host, "+ Add rule").click();
    sel("wiz-lang-rule-audio-0").value = "ja";
    sel("wiz-lang-rule-code-0").value = "en";

    expect(step().validate()).toBe("");
  });

  it("refuses a half-filled rule, which would match nothing", () => {
    const host = render();
    buttonWithText(host, "+ Add rule").click();
    sel("wiz-lang-rule-audio-0").value = "ja";

    expect(step().validate()).toBe("At least one language default or rule must be configured");
  });

  it("collects before judging, so the current selects decide", () => {
    // validate() runs its own collect: a value typed since the last collect
    // must count, or Next refuses a step the user has just filled in.
    render();
    expect(step().validate()).not.toBe("");

    sel("wiz-lang-def-code-0").value = "de";

    expect(step().validate()).toBe("");
  });
});
