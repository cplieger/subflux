// config-languages.test.ts — the settings drawer's language builder.
//
// buildLanguagesSection() renders the language rules and
// serializeLanguagesFromForm() reads them back into the structured PUT, so a
// disagreement between the two silently rewrites the operator's config on save.
import { describe, it, expect, beforeEach } from "vitest";
import * as store from "./store.js";
import { setCfgSections } from "./config-values.js";
import { buildLanguagesSection, serializeLanguagesFromForm } from "./config-languages.js";
import type { LanguageRules, ParsedConfig } from "./wire/types.gen.js";

/** Render the section from `lr`, stored as the structured `languages` section,
 *  into the document — serializeLanguagesFromForm reads by getElementById, so
 *  the form has to be attached. `null` stores no languages section at all. */
function mount(lr: LanguageRules | null): HTMLElement {
  setCfgSections(lr === null ? {} : { languages: lr });
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
  setCfgSections({});
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

  it("renders a single en default when the stored config has no languages section", () => {
    mount(null);

    expect(serializeLanguagesFromForm()).toStrictEqual({ default: [{ code: "en" }] });
  });

  it("renders the stored section when the parsed config does not describe it", () => {
    store.set("config", {
      adaptive: {},
      search: {},
      providers: {},
      language_rules: { default: [{ code: "de" }] },
      languages: ["de"],
      scores: {} as ParsedConfig["scores"],
      post_processing: {} as ParsedConfig["post_processing"],
      configured: true,
      sonarr_configured: false,
      radarr_configured: false,
    });
    mount({ default: [{ code: "en" }, { code: "fr" }] });

    expect(serializeLanguagesFromForm()).toStrictEqual({
      default: [{ code: "en" }, { code: "fr" }],
    });
  });

  it("keeps a rule that asks for no subtitles as an empty list", () => {
    // rules[].subtitles == [] is a distinct, acted-on state server-side ("skip
    // this media, do not fall through to the defaults"), so it must survive a
    // save rather than collapsing to an absent key.
    mount({ rules: [{ audio: "en", subtitles: [] }] });

    expect(serializeLanguagesFromForm().rules).toStrictEqual([{ audio: "en", subtitles: [] }]);
  });
});

describe("language builder: fields with no control", () => {
  it("saves a stored languages section with variants, variant and min_score unchanged", () => {
    const lr: LanguageRules = {
      rules: [
        {
          audio: "ja",
          subtitles: [
            { code: "en", variants: ["standard", "forced"], min_score: 60 },
            { code: "fr", variant: "hi", providers: ["subdl"] },
          ],
        },
      ],
      default: [
        { code: "en", variant: "forced", min_score: 90 },
        { code: "de", variants: ["standard", "hi"], exclude: ["gestdown"] },
      ],
    };

    mount(lr);

    expect(serializeLanguagesFromForm()).toStrictEqual(lr);
  });

  it("keeps a default target's variant and min_score when its language is changed", () => {
    const host = mount({ default: [{ code: "en", variant: "forced", min_score: 90 }] });
    const sel = host.querySelector<HTMLSelectElement>("#lang-defaults .lang-select");
    if (!sel) {
      throw new Error("default language select not rendered");
    }
    sel.value = "fr";

    expect(serializeLanguagesFromForm().default).toStrictEqual([
      { code: "fr", variant: "forced", min_score: 90 },
    ]);
  });

  it("replaces a rule target's variants list with the variant picked in its select", () => {
    // The loader refuses a target carrying both variant and variants.
    const host = mount({
      rules: [{ audio: "en", subtitles: [{ code: "fr", variants: ["standard", "hi"] }] }],
    });
    const sel = host.querySelector<HTMLSelectElement>(".lang-rule .variant-select");
    if (!sel) {
      throw new Error("variant select not rendered");
    }
    sel.value = "forced";

    expect(serializeLanguagesFromForm().rules).toStrictEqual([
      { audio: "en", subtitles: [{ code: "fr", variant: "forced" }] },
    ]);
  });

  it("adds a target with nothing carried from another row", () => {
    const host = mount({ default: [{ code: "en", variant: "forced", min_score: 90 }] });

    buttonWithText(host, "+ Add subtitle").click();

    expect(serializeLanguagesFromForm().default).toStrictEqual([
      { code: "en", variant: "forced", min_score: 90 },
      { code: "en" },
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

describe("language builder: stored shorthand and environment values", () => {
  /** Stores `effective` as the parsed config beside the stored section. */
  function mountParsed(stored: LanguageRules, effective: LanguageRules): HTMLElement {
    store.set("config", {
      adaptive: {},
      search: {},
      providers: {},
      language_rules: effective,
      languages: [],
      scores: {} as ParsedConfig["scores"],
      post_processing: {} as ParsedConfig["post_processing"],
      configured: true,
      sonarr_configured: false,
      radarr_configured: false,
    });
    return mount(stored);
  }

  function shown(host: HTMLElement, selector: string): string[] {
    return [...host.querySelectorAll<HTMLSelectElement>(selector)].map((s) => s.value);
  }

  const shorthand: LanguageRules = {
    rules: [
      { audio: "ja", subtitles: [{ code: "en", variants: ["standard", "forced"], min_score: 60 }] },
    ],
    default: [{ code: "fr", variants: ["standard", "hi"] }],
  };
  const shorthandLoaded: LanguageRules = {
    rules: [
      {
        audio: "ja",
        subtitles: [
          { code: "en", variant: "standard", min_score: 60 },
          { code: "en", variant: "forced", min_score: 60 },
        ],
      },
    ],
    default: [
      { code: "fr", variant: "standard" },
      { code: "fr", variant: "hi" },
    ],
  };

  it("shows one row per variant a stored variants list loads as", () => {
    const host = mountParsed(shorthand, shorthandLoaded);

    expect(shown(host, ".lang-rule .variant-select")).toStrictEqual(["standard", "forced"]);
    expect(shown(host, "#lang-defaults .lang-select")).toStrictEqual(["fr", "fr"]);
  });

  it("saves an untouched variants list as the stored shorthand", () => {
    mountParsed(shorthand, shorthandLoaded);

    expect(serializeLanguagesFromForm()).toStrictEqual(shorthand);
  });

  it("writes each variant out on its own once one of its rows is edited", () => {
    const host = mountParsed(shorthand, shorthandLoaded);
    const variant = host.querySelectorAll<HTMLSelectElement>(".lang-rule .variant-select")[1];
    const defaultCode = host.querySelectorAll<HTMLSelectElement>("#lang-defaults .lang-select")[1];
    if (!variant || !defaultCode) {
      throw new Error("expanded rows not rendered");
    }
    variant.value = "hi";
    defaultCode.value = "de";

    expect(serializeLanguagesFromForm()).toStrictEqual({
      rules: [
        {
          audio: "ja",
          subtitles: [
            { code: "en", min_score: 60 },
            { code: "en", min_score: 60, variant: "hi" },
          ],
        },
      ],
      default: [{ code: "fr" }, { code: "de", variant: "hi" }],
    });
  });

  it("shows the expanded value of an environment reference and saves the reference", () => {
    const stored: LanguageRules = {
      rules: [{ audio: "${SUBFLUX_AUDIO}", subtitles: [{ code: "${SUBFLUX_SUB}" }] }],
      default: [{ code: "${SUBFLUX_SUB}", min_score: 70 }],
    };
    const host = mountParsed(stored, {
      rules: [{ audio: "ja", subtitles: [{ code: "en" }] }],
      default: [{ code: "en", min_score: 70 }],
    });

    expect(shown(host, ".lang-rule .lang-select")).toStrictEqual(["ja", "en"]);
    expect(shown(host, "#lang-defaults .lang-select")).toStrictEqual(["en"]);
    expect(serializeLanguagesFromForm()).toStrictEqual(stored);
  });

  it("saves the picked value in place of an environment reference the reader changed", () => {
    const host = mountParsed(
      { default: [{ code: "${SUBFLUX_SUB}", min_score: 70 }] },
      { default: [{ code: "en", min_score: 70 }] },
    );
    const sel = host.querySelector<HTMLSelectElement>("#lang-defaults .lang-select");
    if (!sel) {
      throw new Error("default language select not rendered");
    }
    sel.value = "fr";

    expect(serializeLanguagesFromForm().default).toStrictEqual([{ code: "fr", min_score: 70 }]);
  });

  describe("a min score given as an environment reference", () => {
    // The stored section holds the raw scalar, so a numeric field carries the
    // reference as a string beside the effective number.
    const ref = "${SUBFLUX_MIN_SCORE}";
    const stored = {
      rules: [{ audio: "ja", subtitles: [{ code: "en", min_score: ref }] }],
      default: [{ code: "fr", min_score: ref }],
    } as unknown as LanguageRules;
    const effective: LanguageRules = {
      rules: [{ audio: "ja", subtitles: [{ code: "en", min_score: 70 }] }],
      default: [{ code: "fr", min_score: 70 }],
    };

    it("shows the expanded score and saves the reference when untouched", () => {
      const host = mountParsed(stored, effective);

      expect(host.querySelector<HTMLInputElement>(".lang-rule .lang-min-score")?.value).toBe("70");
      expect(serializeLanguagesFromForm()).toStrictEqual(stored);
    });

    it("keeps the reference when another field of the same row is edited", () => {
      const host = mountParsed(stored, effective);
      const ruleSub = host.querySelector<HTMLSelectElement>(".lang-rule .lang-sub .lang-select");
      const defaultCode = host.querySelector<HTMLSelectElement>("#lang-defaults .lang-select");
      if (!ruleSub || !defaultCode) {
        throw new Error("target rows not rendered");
      }
      ruleSub.value = "de";
      defaultCode.value = "it";

      expect(serializeLanguagesFromForm()).toStrictEqual({
        rules: [{ audio: "ja", subtitles: [{ code: "de", min_score: ref }] }],
        default: [{ code: "it", min_score: ref }],
      });
    });

    it("saves the typed score once the reader changes it", () => {
      const host = mountParsed(stored, effective);
      const ms = host.querySelector<HTMLInputElement>(".lang-rule .lang-min-score");
      if (!ms) {
        throw new Error("min score input not rendered");
      }
      ms.value = "85";

      expect(serializeLanguagesFromForm().rules).toStrictEqual([
        { audio: "ja", subtitles: [{ code: "en", min_score: 85 }] },
      ]);
    });
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

  /** Every gear in the rule block, in render order. */
  function ruleGears(host: HTMLElement): HTMLButtonElement[] {
    const rule = host.querySelector<HTMLElement>(".lang-rule");
    return rule ? byLabel(rule, "Advanced settings") : [];
  }

  /** Every gear in the defaults list, in render order. */
  function defaultGears(host: HTMLElement): HTMLButtonElement[] {
    const defaults = host.querySelector<HTMLElement>("#lang-defaults");
    return defaults ? byLabel(defaults, "Advanced settings") : [];
  }

  function expanded(gear: HTMLButtonElement | undefined): string | null | undefined {
    return gear?.getAttribute("aria-expanded");
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

  // The settings dialog rebuilds this whole section on every open, every
  // successful save and every reset, and nothing in the config says whether a
  // gear is open — so the tree being replaced is the only record of it. A second
  // mount() models that exactly: buildLanguagesSection runs while the previous
  // mount's tree is still in the document, which is what renderConfigForm's
  // fragment build sees before it swaps.

  it("keeps a gear the reader opened open across a re-render", () => {
    const lr: LanguageRules = { rules: [{ audio: "ja", subtitles: [{ code: "de" }] }] };
    const first = mount(lr);
    expect(expanded(ruleGear(first))).toBe("false");
    ruleGear(first)?.click();
    expect(expanded(ruleGear(first))).toBe("true");

    const second = mount(lr);

    expect(expanded(ruleGear(second))).toBe("true");
  });

  it("keeps a gear the reader closed on an advanced target closed across a re-render", () => {
    const lr: LanguageRules = {
      rules: [{ audio: "ko", subtitles: [{ code: "sv", min_score: 70 }] }],
    };
    const first = mount(lr);
    expect(expanded(ruleGear(first))).toBe("true");
    ruleGear(first)?.click();

    const second = mount(lr);

    expect(expanded(ruleGear(second))).toBe("false");
  });

  it("follows the target rather than the row position when a row is added above it", () => {
    const first = mount({
      rules: [{ audio: "no", subtitles: [{ code: "fi" }, { code: "da" }] }],
    });
    ruleGears(first)[1]?.click();

    const second = mount({
      rules: [{ audio: "no", subtitles: [{ code: "is" }, { code: "fi" }, { code: "da" }] }],
    });

    // Keyed by position this reads false, true, false: the new first row would
    // inherit the gear the reader opened on a target two places below it.
    expect(ruleGears(second).map((g) => g.getAttribute("aria-expanded"))).toStrictEqual([
      "false",
      "false",
      "true",
    ]);
  });

  // Every add button seeds its row from a fixed target ("en", or en/fr for a
  // rule), so the row the reader just created can share an open target's gear
  // key — and the reader expressed nothing about a row that did not exist. All
  // three carry no captured state, so each has its own case.

  it("adds a default whose gear is closed even when an open target shares its key", () => {
    const lr: LanguageRules = { default: [{ code: "en" }] };
    const first = mount(lr);
    defaultGears(first)[0]?.click();

    const second = mount(lr);
    expect(expanded(defaultGears(second)[0])).toBe("true");
    buttonWithText(second, "+ Add subtitle").click();

    expect(expanded(defaultGears(second)[1])).toBe("false");
  });

  it("adds a rule target whose gear is closed even when an open target shares its key", () => {
    const lr: LanguageRules = { rules: [{ audio: "en", subtitles: [{ code: "en" }] }] };
    const first = mount(lr);
    ruleGears(first)[0]?.click();

    const second = mount(lr);
    const rule = second.querySelector<HTMLElement>(".lang-rule");
    expect(expanded(ruleGears(second)[0])).toBe("true");
    // The rule block's own add button; the defaults' button renders first.
    buttonWithText(rule as HTMLElement, "+ Add subtitle").click();

    expect(expanded(ruleGears(second)[1])).toBe("false");
  });

  it("adds a rule whose target gear is closed even when an open target shares its key", () => {
    const lr: LanguageRules = { rules: [{ audio: "en", subtitles: [{ code: "fr" }] }] };
    const first = mount(lr);
    ruleGears(first)[0]?.click();

    const second = mount(lr);
    expect(expanded(ruleGears(second)[0])).toBe("true");
    buttonWithText(second, "+ Add rule").click();

    // The seeded rule is audio "en" with an "fr" target, so it shares the key.
    const blocks = second.querySelectorAll<HTMLElement>(".lang-rule");
    expect(expanded(byLabel(blocks[1] as HTMLElement, "Advanced settings")[0])).toBe("false");
  });
});
