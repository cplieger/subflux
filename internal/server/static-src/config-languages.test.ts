// config-languages.test.ts — the settings drawer's language builder.
//
// The pair of exports is a codec: buildLanguagesSection() renders the
// `language_rules` section into a form, serializeLanguagesFromForm() reads that
// form back into the value the structured PUT sends
// (config.ts:480 assigns it straight into the saved section). So the assertion
// that carries the most weight is the round trip — if render and serialise
// disagree, a settings save silently rewrites the operator's language config,
// and nothing else in the app would notice.
//
// Where the round trip is deliberately LOSSY, that is asserted too, because the
// loss is a save-path behaviour and not an implementation detail.
import { describe, it, expect, beforeEach } from "vitest";
import * as store from "./store.js";
import { buildLanguagesSection, serializeLanguagesFromForm } from "./config-languages.js";
import type { LanguageRules, ParsedConfig } from "./wire/types.gen.js";

/** A ParsedConfig carrying only the section this module reads. */
function parsedConfig(lr: LanguageRules): ParsedConfig {
  return {
    adaptive: {},
    search: {},
    providers: {},
    language_rules: lr,
    languages: ["en"],
    scores: {} as ParsedConfig["scores"],
    post_processing: {} as ParsedConfig["post_processing"],
    configured: true,
    sonarr_configured: false,
    radarr_configured: false,
  };
}

/** Render the section from `lr` into the document — serializeLanguagesFromForm
 *  reads by getElementById, so the form has to be attached. */
function mount(lr: LanguageRules | null): HTMLElement {
  store.set("config", lr === null ? null : parsedConfig(lr));
  const host = document.createElement("div");
  host.appendChild(buildLanguagesSection());
  document.body.replaceChildren(host);
  return host;
}

function buttonWithText(host: HTMLElement, text: string): HTMLButtonElement {
  const found = [...host.querySelectorAll("button")].find((b) => b.textContent === text);
  if (!found) {
    throw new Error(`no button labelled ${text}`);
  }
  return found;
}

function byLabel(host: HTMLElement, label: string): HTMLButtonElement[] {
  return [...host.querySelectorAll<HTMLButtonElement>(`button[aria-label="${label}"]`)];
}

beforeEach(() => {
  store.set("config", null);
  document.body.replaceChildren();
});

describe("language builder round trip", () => {
  it("names every language and variant select, so none is announced bare", () => {
    const host = mount({
      rules: [{ audio: "ja", subtitles: [{ code: "en", variant: "forced" }] }],
      default: [{ code: "en" }],
    });

    // These rows caption their selects with a layout element, not a <label>,
    // so the name has to be an aria-label (axe `select-name`, critical).
    const unnamed = [...host.querySelectorAll("select")].filter(
      (s) => !s.getAttribute("aria-label") && (s.labels?.length ?? 0) === 0,
    );
    expect(unnamed).toStrictEqual([]);
    expect(
      host.querySelector(".lang-rule .lang-row .lang-select")?.getAttribute("aria-label"),
    ).toBe("Audio language");
  });

  it("serialises a rendered rules + defaults config back to the same value", () => {
    const lr: LanguageRules = {
      rules: [
        {
          audio: "ja",
          subtitles: [
            { code: "en", variant: "forced", min_score: 80 },
            { code: "fr", providers: ["opensubtitles", "subdl"], exclude: ["gestdown"] },
          ],
        },
        { audio: "en", subtitles: [{ code: "pb" }] },
      ],
      default: [{ code: "en" }, { code: "de" }],
    };

    mount(lr);

    expect(serializeLanguagesFromForm()).toStrictEqual(lr);
  });

  it("omits the standard variant rather than writing it out", () => {
    mount({ rules: [{ audio: "en", subtitles: [{ code: "fr", variant: "standard" }] }] });

    // The select does carry "standard"; the serialiser drops it because it is
    // the default, which is what keeps a saved config free of noise.
    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "en", subtitles: [{ code: "fr" }] },
    ]);
  });

  it("renders a single en default when the config has no language rules", () => {
    mount({});

    expect(serializeLanguagesFromForm()).toStrictEqual({ default: [{ code: "en" }] });
  });

  it("renders a single en default when the parsed config is absent entirely", () => {
    mount(null);

    expect(serializeLanguagesFromForm()).toStrictEqual({ default: [{ code: "en" }] });
  });

  it("keeps a rule that asks for no subtitles as an empty list", () => {
    // rules[].subtitles == [] is a distinct, acted-on state server-side ("skip
    // this media, do not fall through to the defaults"), so it must survive a
    // save rather than collapsing to an absent key.
    mount({ rules: [{ audio: "en", subtitles: [] }] });

    expect(serializeLanguagesFromForm().rules).toStrictEqual([{ audio: "en", subtitles: [] }]);
  });
});

describe("language builder losses (drawer save path)", () => {
  it("drops variant and min_score from a DEFAULT target", () => {
    // The builder renders no variant or min-score control for defaults
    // (buildSubTarget's isDefault arm), so a config file carrying either on a
    // default target loses it the next time the drawer saves — the config
    // format and GET /api/config/parsed both carry the fields. Asserted so the
    // loss is visible rather than latent.
    mount({ default: [{ code: "en", variant: "forced", min_score: 90 }] });

    expect(serializeLanguagesFromForm().default).toStrictEqual([{ code: "en" }]);
  });

  it("drops the variants list from a rule target", () => {
    // `variants` (plural, a multi-variant target) has no control anywhere in
    // the builder, so it is lost from rules and defaults alike.
    mount({ rules: [{ audio: "en", subtitles: [{ code: "fr", variants: ["standard", "hi"] }] }] });

    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "en", subtitles: [{ code: "fr" }] },
    ]);
  });

  it("drops a non-numeric min score rather than saving NaN", () => {
    const host = mount({ rules: [{ audio: "en", subtitles: [{ code: "fr", min_score: 50 }] }] });
    const ms = host.querySelector<HTMLInputElement>(".lang-min-score");
    if (!ms) {
      throw new Error("min score input not rendered");
    }
    ms.value = "not a number";

    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "en", subtitles: [{ code: "fr" }] },
    ]);
  });
});

describe("language builder editing", () => {
  it("adds an en target to the defaults when + Add subtitle is pressed", () => {
    const host = mount({ default: [{ code: "de" }] });

    buttonWithText(host, "+ Add subtitle").click();

    expect(serializeLanguagesFromForm().default).toStrictEqual([{ code: "de" }, { code: "en" }]);
  });

  it("adds an en→fr rule when + Add rule is pressed", () => {
    const host = mount({});

    buttonWithText(host, "+ Add rule").click();

    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "en", subtitles: [{ code: "fr" }] },
    ]);
  });

  it("adds a target to an existing rule's subtitle list", () => {
    const host = mount({ rules: [{ audio: "ja", subtitles: [{ code: "en" }] }] });

    // The rule block carries its own "+ Add subtitle"; the section-level one
    // (index 0) targets the defaults.
    const adders = [...host.querySelectorAll("button")].filter(
      (b) => b.textContent === "+ Add subtitle",
    );
    expect(adders).toHaveLength(2);
    adders[1]?.click();

    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "ja", subtitles: [{ code: "en" }, { code: "en" }] },
    ]);
  });

  it("removes only the pressed subtitle target", () => {
    const host = mount({ default: [{ code: "en" }, { code: "de" }, { code: "fr" }] });

    byLabel(host, "Remove subtitle")[1]?.click();

    expect(serializeLanguagesFromForm().default).toStrictEqual([{ code: "en" }, { code: "fr" }]);
  });

  it("removes only the pressed rule", () => {
    const host = mount({
      rules: [
        { audio: "ja", subtitles: [{ code: "en" }] },
        { audio: "en", subtitles: [{ code: "fr" }] },
      ],
    });

    byLabel(host, "Remove rule")[0]?.click();

    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "en", subtitles: [{ code: "fr" }] },
    ]);
  });

  it("omits the rules key entirely once the last rule is removed", () => {
    const host = mount({ rules: [{ audio: "ja", subtitles: [{ code: "en" }] }] });

    byLabel(host, "Remove rule")[0]?.click();

    expect(serializeLanguagesFromForm().rules).toBeUndefined();
  });
});

describe("advanced disclosure", () => {
  /** The gear inside the rule block — the defaults render first, so index 0 of
   *  a document-wide query is the default target's gear, not the rule's. */
  function ruleGear(host: HTMLElement): HTMLButtonElement | undefined {
    const rule = host.querySelector<HTMLElement>(".lang-rule");
    return rule ? byLabel(rule, "Advanced settings")[0] : undefined;
  }

  it("opens the advanced region for a target that already has advanced values", () => {
    const host = mount({ rules: [{ audio: "en", subtitles: [{ code: "fr", min_score: 70 }] }] });

    expect(ruleGear(host)?.getAttribute("aria-expanded")).toBe("true");
  });

  it("leaves the advanced region closed for a plain target", () => {
    const host = mount({ rules: [{ audio: "en", subtitles: [{ code: "fr" }] }] });

    expect(ruleGear(host)?.getAttribute("aria-expanded")).toBe("false");
  });

  it("opens the advanced region for a target that only names providers", () => {
    const host = mount({
      rules: [{ audio: "en", subtitles: [{ code: "fr", providers: ["subdl"] }] }],
    });

    expect(ruleGear(host)?.getAttribute("aria-expanded")).toBe("true");
  });

  it("opens the advanced region for a target that only excludes providers", () => {
    const host = mount({
      rules: [{ audio: "en", subtitles: [{ code: "fr", exclude: ["gestdown"] }] }],
    });

    expect(ruleGear(host)?.getAttribute("aria-expanded")).toBe("true");
  });
});
