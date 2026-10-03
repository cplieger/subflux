// Unit tests for providerFieldValue, the settings-dialog value resolver for
// provider setting fields. The rule it encodes is server parity: absent means
// "use the schema default" (provider.NormalizeSettings does the same before a
// factory runs), present means "this is the user's value" — including a bare
// `key:` in YAML, which arrives as null and which the server also counts as
// set. A regression here is silent: the dialog renders the wrong state and the
// next save writes it back as a deliberate choice.
import { describe, it, expect, vi } from "vitest";
import { providerFieldValue, providerHealth, renderProvidersSection } from "./config-providers.js";
import { setCfgSections } from "./config-values.js";
import type { SchemaField, SchemaSection } from "./api-types.js";
import type { ProviderStatus, ProvidersResponse } from "./wire/types.gen.js";

// GET /api/providers/timeout, held open so a render pass can be superseded
// while its health answer is still in flight.
const health = vi.hoisted(() => {
  const state = {
    settle: [] as ((v: unknown) => void)[],
    signals: [] as (AbortSignal | undefined)[],
    reset(): void {
      state.settle.length = 0;
      state.signals.length = 0;
    },
  };
  return state;
});

vi.mock("./wire/client.gen.js", () => ({
  providerTimeouts: (opts?: { signal?: AbortSignal }): Promise<unknown> => {
    health.signals.push(opts?.signal);
    return new Promise((resolve) => {
      health.settle.push(resolve);
    });
  },
  // Reached only by a credential-check click, which these tests do not make.
  testConnectionRaw: () => Promise.resolve({ ok: true, status: 200, data: { valid: true } }),
}));

const boolDefaultTrue: SchemaField = {
  key: "use_hash",
  label: "Use Hash",
  type: "bool",
  default: "true",
};

const boolDefaultFalse: SchemaField = {
  key: "include_machine_translated",
  label: "Include Machine Translated",
  type: "bool",
  default: "false",
};

const secretNoDefault: SchemaField = {
  key: "api_key",
  label: "API Key",
  type: "secret",
  secret: true,
};

describe("providerFieldValue", () => {
  const cases: {
    name: string;
    field: SchemaField;
    settings: Record<string, unknown> | undefined;
    want: string;
  }[] = [
    {
      name: "absent key falls back to a true default",
      field: boolDefaultTrue,
      settings: {},
      want: "true",
    },
    {
      name: "missing settings block falls back to the default",
      field: boolDefaultTrue,
      settings: undefined,
      want: "true",
    },
    {
      name: "absent key falls back to a false default",
      field: boolDefaultFalse,
      settings: {},
      want: "false",
    },
    {
      name: "absent key with no default resolves empty",
      field: secretNoDefault,
      settings: {},
      want: "",
    },
    {
      name: "explicit false beats a true default",
      field: boolDefaultTrue,
      settings: { use_hash: false },
      want: "false",
    },
    {
      name: "explicit true is kept",
      field: boolDefaultFalse,
      settings: { include_machine_translated: true },
      want: "true",
    },
    {
      name: "null counts as set, matching the server",
      field: boolDefaultTrue,
      settings: { use_hash: null },
      want: "",
    },
    {
      name: "empty secret stays empty, never the default",
      field: secretNoDefault,
      settings: { api_key: "" },
      want: "",
    },
    {
      name: "string value passes through",
      field: secretNoDefault,
      settings: { api_key: "abc123" },
      want: "abc123",
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(providerFieldValue(tc.field, tc.settings)).toBe(tc.want);
    });
  }
});

describe("renderProvidersSection", () => {
  it("names each provider's enable toggle with the provider", () => {
    setCfgSections({});
    const schema: SchemaSection = {
      key: "providers",
      title: "Providers",
      type: "providers",
      providers: [{ name: "subdl", label: "SubDL" }],
    };

    const host = renderProvidersSection(schema);
    document.body.replaceChildren(host);

    // The .toggle wrapper holds only the slider, so the card's visible name is
    // this checkbox's only possible accessible name.
    const cb = host.querySelector<HTMLInputElement>("#cfg-prov-subdl-enabled");
    expect([...(cb?.labels ?? [])].map((l) => l.textContent)).toContain("SubDL");
  });
});

describe("renderProvidersSection: the credential-check control", () => {
  const section: SchemaSection = {
    key: "providers",
    title: "Providers",
    type: "providers",
    providers: [
      {
        name: "opensubtitles",
        label: "OpenSubtitles",
        conn_test: true,
        settings: [secretNoDefault, boolDefaultTrue],
      },
      { name: "gestdown", label: "Gestdown" },
    ],
  };

  it("places the control in the card head of a provider that declares one", () => {
    setCfgSections({});
    const sec = renderProvidersSection(section);

    const cards = sec.querySelectorAll(".provider");
    expect(cards).toHaveLength(2);
    expect(cards[0]!.querySelector(".provider-head .conn-test")).not.toBeNull();
    // Gestdown carries no credentials, so the server offers no check and the
    // schema says so; a control there would only ever answer 400.
    expect(cards[1]!.querySelector(".conn-test")).toBeNull();
  });

  it("puts the control BEFORE the toggle, so both cards' toggles line up", () => {
    setCfgSections({});
    const sec = renderProvidersSection(section);

    const head = sec.querySelector(".provider-head")!;
    const kids = [...head.children];
    expect(kids.indexOf(head.querySelector(".conn-test")!)).toBeLessThan(
      kids.indexOf(head.querySelector(".toggle")!),
    );
  });

  it("watches the provider's own settings, so editing one retires the verdict", () => {
    setCfgSections({});
    document.body.replaceChildren(renderProvidersSection(section));
    const btn = document.body.querySelector<HTMLButtonElement>(".conn-test");
    const key = document.body.querySelector<HTMLInputElement>("#cfg-prov-opensubtitles-s-api_key");
    expect(btn).not.toBeNull();
    expect(key).not.toBeNull();

    btn!.dataset["status"] = "ok";
    key!.dispatchEvent(new Event("input"));

    expect(btn!.dataset["status"]).toBeUndefined();
  });
});

// --- The health-badge pass -------------------------------------------------

// The badge fetch outlives the render pass that started it, which is why this
// one is not fixed by the install idiom: a superseded pass still holds the
// section it built, and that section is off the document by the time the answer
// lands.

const HEALTH_SCHEMA: SchemaSection = {
  key: "providers",
  title: "Providers",
  type: "providers",
  providers: [{ name: "subdl", label: "SubDL" }],
};

function healthAnswer(timedOut: boolean): ProvidersResponse {
  return {
    enabled: true,
    providers: {
      subdl: { recent_failures: 0, threshold: 3, timed_out: timedOut, disabled: false },
    },
  };
}

async function flush(): Promise<void> {
  await new Promise<void>((resolve) => {
    setTimeout(resolve, 0);
  });
}

describe("renderProvidersSection: the health badge", () => {
  it("renders the answer into the section it was asked for", async () => {
    setCfgSections({});
    health.reset();
    const sec = renderProvidersSection(HEALTH_SCHEMA);
    document.body.replaceChildren(sec);

    health.settle[0]!(healthAnswer(false));
    await flush();

    const badge = sec.querySelector(".badge-health");
    expect(badge?.textContent).toBe("healthy");
    // Before the toggle, so every card's toggle lines up whatever its health.
    expect([...sec.querySelectorAll(".provider-head > *")].indexOf(badge!)).toBeLessThan(
      [...sec.querySelectorAll(".provider-head > *")].indexOf(sec.querySelector(".toggle")!),
    );
  });

  it("writes nothing for a pass a later render has superseded", async () => {
    setCfgSections({});
    health.reset();
    const stale = renderProvidersSection(HEALTH_SCHEMA);
    const live = renderProvidersSection(HEALTH_SCHEMA);
    document.body.replaceChildren(live);

    health.settle[1]!(healthAnswer(false));
    await flush();
    health.settle[0]!(healthAnswer(true));
    await flush();

    expect(live.querySelectorAll(".badge-health")).toHaveLength(1);
    expect(stale.querySelectorAll(".badge-health")).toHaveLength(0);
  });
});

describe("renderProvidersSection: the credential gate on the badge", () => {
  async function badgeFor(status: ProviderStatus): Promise<Element | null> {
    setCfgSections({});
    health.reset();
    const sec = renderProvidersSection(HEALTH_SCHEMA);
    document.body.replaceChildren(sec);
    // Timeouts off: the gate's states are reported whatever that flag says.
    health.settle[0]!({ enabled: false, providers: { subdl: status } } satisfies ProvidersResponse);
    await flush();
    return sec.querySelector(".badge-health");
  }

  it("names a credential disable as an error, with the reason in the tooltip", async () => {
    const badge = await badgeFor({
      recent_failures: 0,
      threshold: 3,
      timed_out: false,
      disabled: true,
      disabled_reason: "HTTP 401",
    });
    expect([
      badge?.textContent,
      badge?.getAttribute("data-status"),
      badge?.getAttribute("data-tip"),
    ]).toEqual(["disabled: credentials rejected", "err", "HTTP 401"]);
  });

  it("names a rejected optional setting as a warning", async () => {
    const badge = await badgeFor({
      recent_failures: 0,
      threshold: 3,
      timed_out: false,
      disabled: false,
      rejected_settings: ["anidb_client_key"],
    });
    expect([badge?.textContent, badge?.getAttribute("data-status")]).toEqual([
      "setting rejected: anidb_client_key",
      "warn",
    ]);
  });
});

describe("providerHealth", () => {
  it("says until when a rate-limited provider is paused", () => {
    const now = new Date(2026, 0, 1, 14, 0).getTime();
    const ninetyMinutesNs = 90 * 60 * 1e9;
    expect(
      providerHealth(
        {
          recent_failures: 0,
          threshold: 3,
          timed_out: false,
          disabled: false,
          paused_for: ninetyMinutesNs,
        },
        now,
      ),
    ).toEqual({ status: "warn", text: "paused until 15:30" });
  });
});
