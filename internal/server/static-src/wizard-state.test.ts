// wizard-state.test.ts — Pins the first-boot wizard's decision logic:
// prefill from structured configs (with presence flags), the
// satisfied/collapse gating incl. the fresh-volume example-config FULL-walk
// fixture, stale-draft invalidation, the preserve-untouched-sections
// round-trip proof, and the non-admin entry routing.

import { describe, it, expect } from "vitest";
import {
  MANDATORY_STEPS,
  STEP_IDS,
  buildDraftJSON,
  buildSaveSections,
  differsFromExample,
  fastPathAvailable,
  fingerprintBoot,
  overlayDraft,
  parseDraft,
  postLoginDestination,
  prefillModel,
  providerEnabledInSections,
  satisfiedSteps,
  stepSatisfied,
  type Sections,
  type StepID,
  type WizardBoot,
  type WizardDraft,
  type WizardModel,
} from "./wizard-state.js";
import { EXAMPLE_SECTIONS } from "./wizard-example.js";
import type { SchemaSection } from "./api-types.js";

// --- Fixtures ---

/** freshVolumeSections mirrors GET /api/config/structured on a fresh volume:
 *  runServer auto-writes config.example.yaml, so the sections ARE the
 *  example (plus the non-wizard sections the file also carries). */
function freshVolumeSections(): Sections {
  return {
    ...structuredClone(EXAMPLE_SECTIONS),
    trusted_proxies: [],
    poll_interval: "30s",
    embedded_subtitles: { ignore_pgs: true, ignore_vobsub: true, ignore_ass: false },
    logging: { level: "info", format: "json" },
  };
}

/** freshVolumeBoot: the example config does not validate (empty arr keys),
 *  so a fresh volume boots unconfigured — config_valid=false. */
function freshVolumeBoot(): WizardBoot {
  return {
    sections: freshVolumeSections(),
    secretsPresent: new Set<string>(),
    configValid: false,
  };
}

/** configFileBoot mirrors a pre-authored VALID config: real arr endpoints
 *  (keys present-but-redacted), custom media roots and languages, one
 *  credentialed provider — and untouched search/scoring/post_processing
 *  kept at example defaults. */
function configFileBoot(): WizardBoot {
  const sections: Sections = {
    ...structuredClone(EXAMPLE_SECTIONS),
    sonarr: { enabled: true, url: "http://10.0.0.5:8989", api_key: "", public_url: "" },
    radarr: { enabled: true, url: "http://10.0.0.5:7878", api_key: "", public_url: "" },
    media_roots: ["/tv", "/movies"],
    languages: {
      rules: [{ audio: "de", subtitles: [{ code: "de" }] }],
      default: [{ code: "de" }],
    },
    providers: {
      opensubtitles: {
        enabled: true,
        priority: 2,
        settings: { username: "me", password: "", api_key: "", use_hash: true },
      },
      gestdown: { enabled: true, priority: 4 },
    },
    logging: { level: "debug", format: "text" },
    backup: { enabled: true, frequency: "12h", retention: 5 },
    auth: { webauthn_rp_id: "subflux.example.com" },
  };
  return {
    sections,
    secretsPresent: new Set<string>([
      "sonarr.api_key",
      "radarr.api_key",
      "providers.opensubtitles.settings.password",
      "providers.opensubtitles.settings.api_key",
    ]),
    configValid: true,
  };
}

// --- Prefill (R3.2) ---

describe("prefillModel", () => {
  it("prefills every non-secret value from the structured config", () => {
    const boot = configFileBoot();
    const m = prefillModel(boot.sections);

    expect(m.wizardValues["sonarr"]?.["url"]).toBe("http://10.0.0.5:8989");
    expect(m.wizardValues["radarr"]?.["url"]).toBe("http://10.0.0.5:7878");
    // Secrets arrive redacted-empty; presence rides separately.
    expect(m.wizardValues["sonarr"]?.["api_key"]).toBe("");
    expect(boot.secretsPresent.has("sonarr.api_key")).toBe(true);

    expect(m.mediaRoots).toEqual(["/tv", "/movies"]);
    expect(m.langRules).toEqual([{ audio: "de", code: "de", variant: "standard" }]);
    expect(m.langDefault).toEqual([{ code: "de", variant: "standard" }]);

    expect(m.providerEnabled["opensubtitles"]).toBe(true);
    expect(m.providerEnabled["gestdown"]).toBe(true);
    expect(m.wizardValues["prov_opensubtitles"]?.["username"]).toBe("me");
    expect(m.wizardValues["prov_opensubtitles"]?.["use_hash"]).toBe("true");
  });

  it("prefills search, adaptive, scoring and post_processing string forms", () => {
    const m = prefillModel(freshVolumeSections());
    expect(m.wizardValues["search"]?.["scan_interval"]).toBe("24h");
    expect(m.wizardValues["search"]?.["upgrade_enabled"]).toBe("true");
    expect(m.wizardValues["search"]?.["upgrade_window_days"]).toBe("7");
    expect(m.wizardValues["search"]?.["exclude_arr_tags"]).toBe("no-subflux");
    expect(m.wizardValues["adaptive"]?.["initial_delay"]).toBe("7D");
    expect(m.wizardValues["scoring"]?.["hash"]).toBe("100");
    expect(m.wizardValues["post_processing"]?.["strip_tags"]).toBe("true");
  });

  it("flattens multi-subtitle language rules to one row per target", () => {
    const m = prefillModel(freshVolumeSections());
    // Example rules: en->fr, fr->en (variants standard+forced -> first wins),
    // ja->en; the es empty-subtitles rule contributes no row.
    expect(m.langRules).toEqual([
      { audio: "en", code: "fr", variant: "standard" },
      { audio: "fr", code: "en", variant: "standard" },
      { audio: "ja", code: "en", variant: "standard" },
    ]);
  });
});

// --- Satisfied / collapse gating (R3.3) ---

describe("stepSatisfied", () => {
  it("fresh volume (example config, invalid): NOTHING satisfied — the FULL walk", () => {
    const boot = freshVolumeBoot();
    for (const id of STEP_IDS) {
      expect(stepSatisfied(id, boot), id).toBe(false);
    }
    expect(satisfiedSteps(boot).size).toBe(0);
    expect(fastPathAvailable(boot)).toBe(false);
  });

  it("example-valued sections stay unsatisfied even when config_valid=true (the example-config trap)", () => {
    const boot = freshVolumeBoot();
    boot.configValid = true;
    // The arr/search/scoring/pp sections EQUAL the embedded example: a
    // naive config_valid gate would fast-forward a genuinely new user past
    // real steps on placeholder data.
    for (const id of STEP_IDS) {
      expect(differsFromExample(id, boot.sections), id).toBe(false);
      expect(stepSatisfied(id, boot), id).toBe(false);
    }
  });

  it("config-file boot: differing, complete sections collapse; example-valued ones walk", () => {
    const boot = configFileBoot();
    const satisfied = satisfiedSteps(boot);
    expect(satisfied.has("arr")).toBe(true);
    expect(satisfied.has("media_roots")).toBe(true);
    expect(satisfied.has("languages")).toBe(true);
    expect(satisfied.has("providers")).toBe(true);
    // search/scoring/post_processing were left at example defaults.
    expect(satisfied.has("search")).toBe(false);
    expect(satisfied.has("scoring")).toBe(false);
    expect(satisfied.has("post_processing")).toBe(false);
  });

  it("offers the finish fast path when all mandatory steps are satisfied (R3.5)", () => {
    const boot = configFileBoot();
    expect(MANDATORY_STEPS.every((id) => stepSatisfied(id, boot))).toBe(true);
    expect(fastPathAvailable(boot)).toBe(true);
  });

  it("an arr without a saved or entered key is not satisfied even when it differs", () => {
    const boot = configFileBoot();
    boot.secretsPresent = new Set<string>(); // keys vanished
    expect(stepSatisfied("arr", boot)).toBe(false);
    expect(fastPathAvailable(boot)).toBe(false);
  });
});

// --- Draft (R3.7) ---

describe("draft fingerprint + overlay", () => {
  function draftFor(
    boot: WizardBoot,
    touched: StepID[],
    model = prefillModel(boot.sections),
  ): WizardDraft {
    return {
      v: 2,
      fingerprint: fingerprintBoot(boot.sections, [...boot.secretsPresent]),
      stepId: touched[0] ?? "arr",
      touched,
      model,
    };
  }

  it("accepts a draft whose fingerprint matches the current snapshot", () => {
    const boot = configFileBoot();
    const fp = fingerprintBoot(boot.sections, [...boot.secretsPresent]);
    const draft = draftFor(boot, ["arr"]);
    expect(parseDraft(JSON.stringify(draft), fp)).not.toBeNull();
  });

  it("drops a stale draft when the server config changed underneath it", () => {
    const boot = configFileBoot();
    const draft = draftFor(boot, ["arr"]);
    const changed = configFileBoot();
    (changed.sections["sonarr"] as Record<string, unknown>)["url"] = "http://elsewhere:8989";
    const fpChanged = fingerprintBoot(changed.sections, [...changed.secretsPresent]);
    expect(parseDraft(JSON.stringify(draft), fpChanged)).toBeNull();
  });

  it("drops a draft when the secret-presence set changed", () => {
    const boot = configFileBoot();
    const draft = draftFor(boot, ["arr"]);
    const fpNoSecrets = fingerprintBoot(boot.sections, []);
    expect(parseDraft(JSON.stringify(draft), fpNoSecrets)).toBeNull();
  });

  it("drops unparseable or wrong-shape drafts", () => {
    expect(parseDraft("{not json", "fp")).toBeNull();
    expect(parseDraft(JSON.stringify({ v: 2, fingerprint: "fp" }), "fp")).toBeNull();
    expect(
      parseDraft(JSON.stringify({ v: 2, fingerprint: "fp", touched: "x", model: {} }), "fp"),
    ).toBeNull();
    expect(parseDraft(null, "fp")).toBeNull();
  });

  it("drops a legacy v1 draft (pre-sanitization format that persisted plaintext secrets)", () => {
    const boot = configFileBoot();
    const fp = fingerprintBoot(boot.sections, [...boot.secretsPresent]);
    const model = prefillModel(boot.sections);
    model.wizardValues["sonarr"] = { url: "http://s:8989", api_key: "leaked-plaintext" };
    const legacy = { v: 1, fingerprint: fp, stepId: "arr", touched: ["arr"], model };
    expect(parseDraft(JSON.stringify(legacy), fp)).toBeNull();
  });

  it("drops drafts from a future format version", () => {
    const boot = configFileBoot();
    const fp = fingerprintBoot(boot.sections, [...boot.secretsPresent]);
    const future = { ...draftFor(boot, ["arr"]), v: 3 };
    expect(parseDraft(JSON.stringify(future), fp)).toBeNull();
  });

  it("overlays ONLY draft-touched fields onto a fresh prefill", () => {
    const boot = configFileBoot();
    const fresh = prefillModel(boot.sections);
    const edited = prefillModel(boot.sections);
    edited.wizardValues["sonarr"] = { url: "http://edited:8989", api_key: "newkey" };
    edited.mediaRoots = ["/edited"];
    const draft = draftFor(boot, ["arr"], edited); // media_roots NOT touched

    const merged = overlayDraft(fresh, draft);
    expect(merged.wizardValues["sonarr"]?.["url"]).toBe("http://edited:8989");
    // Untouched slices keep the fresh server snapshot.
    expect(merged.mediaRoots).toEqual(["/tv", "/movies"]);
  });
});

// --- Draft secret sanitization (SEC: drafts persist across restarts) ---

describe("buildDraftJSON secret sanitization", () => {
  /** secretSchema exercises every secret-marker style the schema uses:
   *  type "secret" alone (arr api_key), the Secret flag alone (defensive),
   *  both together (provider registry style), and a secret nested inside a
   *  field group. */
  function secretSchema(): SchemaSection[] {
    return [
      {
        key: "sonarr",
        title: "Sonarr",
        type: "object",
        fields: [
          { key: "url", label: "URL", type: "text" },
          { key: "api_key", label: "API Key", type: "secret" },
          { key: "public_url", label: "Public URL", type: "text" },
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
        key: "auth",
        title: "Authentication",
        type: "object",
        fields: [
          {
            key: "oidc",
            label: "OIDC",
            type: "group",
            fields: [{ key: "client_secret", label: "Client Secret", type: "secret" }],
          },
        ],
      },
      {
        key: "providers",
        title: "Providers",
        type: "providers",
        providers: [
          {
            name: "opensubtitles",
            label: "OpenSubtitles",
            settings: [
              { key: "username", label: "Username", type: "text" },
              { key: "password", label: "Password", type: "secret", secret: true },
              { key: "api_key", label: "API Key", type: "secret", secret: true },
              { key: "use_hash", label: "Use hash", type: "bool" },
            ],
          },
          {
            name: "hdbits",
            label: "HDBits",
            settings: [
              { key: "username", label: "Username", type: "text" },
              { key: "passkey", label: "Passkey", type: "secret", secret: true },
            ],
          },
          {
            name: "animetosho",
            label: "AnimeTosho",
            // Secret flag alone (no "secret" type) must also be excluded.
            settings: [{ key: "anidb_client_key", label: "AniDB Key", type: "text", secret: true }],
          },
        ],
      },
    ];
  }

  /** secretModel populates EVERY secret schema field with a sentinel plus
   *  non-secret values that must survive. */
  function secretModel(): WizardModel {
    return {
      wizardValues: {
        sonarr: { url: "http://sonarr:8989", api_key: "SENTINEL-SONARR-KEY", public_url: "" },
        radarr: { url: "http://radarr:7878", api_key: "SENTINEL-RADARR-KEY" },
        auth: { client_secret: "SENTINEL-OIDC-SECRET" },
        prov_opensubtitles: {
          username: "alice",
          password: "SENTINEL-OS-PASSWORD",
          api_key: "SENTINEL-OS-KEY",
          use_hash: "true",
        },
        prov_hdbits: { username: "bob", passkey: "SENTINEL-HDBITS-PASSKEY" },
        prov_animetosho: { anidb_client_key: "SENTINEL-ANIDB-KEY" },
        search: { scan_interval: "24h" },
      },
      providerEnabled: { opensubtitles: true, hdbits: true, animetosho: true },
      langRules: [{ audio: "en", code: "fr", variant: "standard" }],
      langDefault: [{ code: "en", variant: "standard" }],
      mediaRoots: ["/media"],
    };
  }

  const SENTINELS = [
    "SENTINEL-SONARR-KEY",
    "SENTINEL-RADARR-KEY",
    "SENTINEL-OIDC-SECRET",
    "SENTINEL-OS-PASSWORD",
    "SENTINEL-OS-KEY",
    "SENTINEL-HDBITS-PASSKEY",
    "SENTINEL-ANIDB-KEY",
  ];

  it("stores neither secret values nor secret-bearing keys", () => {
    const json = buildDraftJSON(secretSchema(), "fp", "arr", ["arr", "providers"], secretModel());
    for (const sentinel of SENTINELS) {
      expect(json, sentinel).not.toContain(sentinel);
    }
    for (const key of [
      '"api_key"',
      '"password"',
      '"passkey"',
      '"anidb_client_key"',
      '"client_secret"',
    ]) {
      expect(json, key).not.toContain(key);
    }
    // Non-secret keys and values survive verbatim.
    expect(json).toContain('"username":"alice"');
    expect(json).toContain("http://sonarr:8989");
    expect(json).toContain('"use_hash":"true"');
    expect(json).toContain('"scan_interval":"24h"');
  });

  it("round-trips through parseDraft as a v2 draft without the secret keys", () => {
    const json = buildDraftJSON(
      secretSchema(),
      "fp",
      "providers",
      ["arr", "providers"],
      secretModel(),
    );
    const parsed = parseDraft(json, "fp");
    expect(parsed).not.toBeNull();
    expect(parsed?.v).toBe(2);
    expect(parsed?.touched).toEqual(["arr", "providers"]);
    expect(parsed?.model.wizardValues["sonarr"]).toEqual({
      url: "http://sonarr:8989",
      public_url: "",
    });
    expect(parsed?.model.wizardValues["prov_opensubtitles"]).toEqual({
      username: "alice",
      use_hash: "true",
    });
    expect(parsed?.model.wizardValues["prov_animetosho"]).toEqual({});
    // Slices without secrets ride through unchanged.
    expect(parsed?.model.mediaRoots).toEqual(["/media"]);
    expect(parsed?.model.langRules).toEqual([{ audio: "en", code: "fr", variant: "standard" }]);
    expect(parsed?.model.providerEnabled).toEqual({
      opensubtitles: true,
      hdbits: true,
      animetosho: true,
    });
  });

  it("does not mutate the in-memory model (secrets stay in page memory)", () => {
    const model = secretModel();
    buildDraftJSON(secretSchema(), "fp", "arr", ["arr"], model);
    expect(model.wizardValues["sonarr"]?.["api_key"]).toBe("SENTINEL-SONARR-KEY");
    expect(model.wizardValues["prov_opensubtitles"]?.["password"]).toBe("SENTINEL-OS-PASSWORD");
  });
});

// --- Save assembly (R3.4): GET-overlay-PUT preservation proof ---

describe("buildSaveSections", () => {
  it("preserves untouched sections by round-trip (logging, backup, auth, trusted_proxies, post_processing)", () => {
    const boot = configFileBoot();
    const model = prefillModel(boot.sections);
    model.wizardValues["sonarr"] = {
      url: "http://new-sonarr:8989",
      api_key: "fresh-key",
      public_url: "",
    };

    const out = buildSaveSections(boot.sections, model, new Set<StepID>(["arr"]));

    // Touched: sonarr updated (merged over its boot block, enabled forced on).
    expect(out["sonarr"]).toEqual({
      enabled: true,
      url: "http://new-sonarr:8989",
      api_key: "fresh-key",
    });
    // Untouched sections ride through VERBATIM — omission would delete them
    // server-side (canonicalYAML emits only what it is given).
    expect(out["logging"]).toEqual(boot.sections["logging"]);
    expect(out["backup"]).toEqual(boot.sections["backup"]);
    expect(out["auth"]).toEqual(boot.sections["auth"]);
    expect(out["trusted_proxies"]).toEqual(boot.sections["trusted_proxies"]);
    expect(out["post_processing"]).toEqual(boot.sections["post_processing"]);
    expect(out["providers"]).toEqual(boot.sections["providers"]);
  });

  it("merges touched field steps over the boot section, preserving wizard-invisible keys", () => {
    const boot = freshVolumeBoot();
    const model = prefillModel(boot.sections);
    model.wizardValues["search"] = {
      ...model.wizardValues["search"],
      scan_interval: "12h",
      upgrade_enabled: "false",
      exclude_arr_tags: "skip-me, and-me",
    };

    const out = buildSaveSections(boot.sections, model, new Set<StepID>(["search"]));
    const search = out["search"] as Record<string, unknown>;
    expect(search["scan_interval"]).toBe("12h");
    expect(search["upgrade_enabled"]).toBe(false);
    expect(search["exclude_arr_tags"]).toEqual(["skip-me", "and-me"]);
    // provider_timeout / scan_delay are not wizard fields: boot values survive.
    expect(search["provider_timeout"]).toBe("1h");
    expect(search["scan_delay"]).toBe("5s");
  });

  it("keeps disabled providers' blocks (credentials survive a disable) and drops unknown-disabled ones", () => {
    const boot = configFileBoot();
    const model = prefillModel(boot.sections);
    model.providerEnabled = { opensubtitles: false, gestdown: true };

    const out = buildSaveSections(boot.sections, model, new Set<StepID>(["providers"]));
    const provs = out["providers"] as Record<string, unknown>;
    expect((provs["opensubtitles"] as Record<string, unknown>)["enabled"]).toBe(false);
    // The boot block (priority + settings) survives the disable.
    expect((provs["opensubtitles"] as Record<string, unknown>)["priority"]).toBe(2);
    expect((provs["gestdown"] as Record<string, unknown>)["enabled"]).toBe(true);
  });

  it("an empty secret field is omitted so the server keeps the stored value", () => {
    const boot = configFileBoot();
    const model = prefillModel(boot.sections);
    // The user touched the arr step but left the redacted key blank.
    model.wizardValues["sonarr"] = {
      url: "http://10.0.0.5:8989",
      api_key: "",
      public_url: "",
    };
    const out = buildSaveSections(boot.sections, model, new Set<StepID>(["arr"]));
    const sonarr = out["sonarr"] as Record<string, unknown>;
    expect("api_key" in sonarr).toBe(false); // absent leaf = keep existing
    expect(sonarr["url"]).toBe("http://10.0.0.5:8989");
  });

  it("fast path with nothing touched PUTs the boot map unchanged (idempotent completion)", () => {
    const boot = configFileBoot();
    const model = prefillModel(boot.sections);
    const out = buildSaveSections(boot.sections, model, new Set<StepID>());
    expect(out).toEqual(boot.sections);
  });

  it("serializes touched languages back to the config shape", () => {
    const boot = freshVolumeBoot();
    const model = prefillModel(boot.sections);
    model.langRules = [{ audio: "en", code: "fr", variant: "forced" }];
    model.langDefault = [{ code: "en", variant: "standard" }];

    const out = buildSaveSections(boot.sections, model, new Set<StepID>(["languages"]));
    expect(out["languages"]).toEqual({
      rules: [{ audio: "en", subtitles: [{ code: "fr", variant: "forced" }] }],
      default: [{ code: "en" }],
    });
  });
});

// --- Config-blessed providers ---

describe("providerEnabledInSections", () => {
  it("treats present-and-enabled (or bare) blocks as enabled", () => {
    const sections = freshVolumeSections();
    expect(providerEnabledInSections(sections, "gestdown")).toBe(true);
    expect(providerEnabledInSections(sections, "animetosho")).toBe(true);
    expect(providerEnabledInSections(sections, "opensubtitles")).toBe(false);
    expect(providerEnabledInSections(sections, "nonexistent")).toBe(false);
  });
});

// --- Non-admin entry (R3.8) ---

describe("postLoginDestination", () => {
  it("routes an admin into the wizard while the config is invalid", () => {
    expect(postLoginDestination("admin", false)).toBe("wizard");
  });
  it("routes a non-admin to the finish-setup notice, never a wizard of 403s", () => {
    expect(postLoginDestination("user", false)).toBe("admin_needed_notice");
    expect(postLoginDestination("", false)).toBe("admin_needed_notice");
  });
  it("routes everyone to the app when the config is valid", () => {
    expect(postLoginDestination("admin", true)).toBe("app");
    expect(postLoginDestination("user", true)).toBe("app");
  });
});
