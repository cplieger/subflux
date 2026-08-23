// @vitest-environment happy-dom
//
// The settings drawer's lifecycle: the dialog controller's dismissal policy,
// open/close, the config load, the save (including the WebAuthn RP ID guard),
// reset-to-defaults, the schema-driven form render, and the language
// initialization.
//
// Everything here lives behind module state config.ts builds at import time
// (`dialog("configDialog")`, the lazily-created DialogController, the
// module-level `configSchema`), so each test re-imports the module against a
// freshly built DOM instead of sharing one instance. The collaborators that
// reach the network or the toast stack are replaced; the dialog primitive, the
// reactive store, the bus and every renderer are the real ones, so what the
// assertions read is the DOM a browser would get.
//
// `isUnconfigured` is a DERIVED key in the app (app.ts computes it from
// `config.configured`), so the harness installs the same derivation and drives
// it through the parsed config the wire answers with — which is also how the
// save's reload flips it in production.
//
// The mock factories use PLAIN functions, not vi.fn: vitest.config sets
// mockReset, which would strip an implementation registered at module load
// (apiAction, bindLoadingState) before the first test ran.
import { describe, it, expect, vi } from "vitest";

import type * as ConfigModule from "./config.js";
import type * as StoreModule from "./store.js";
import type * as BusModule from "./bus.js";
import type { ParsedConfig, StructuredConfig, SchemaSection } from "./wire/types.gen.js";

// --- Collaborator doubles ---------------------------------------------------

// GET /api/config/{structured,parsed,schema}. Each slot holds the value the
// endpoint answers with, or an Error to make it reject.
const wire = vi.hoisted(() => {
  const state = {
    structured: null as unknown,
    parsed: null as unknown,
    schema: null as unknown,
    structuredCalls: [] as unknown[],
    parsedCalls: [] as unknown[],
    schemaCalls: [] as unknown[],
    answer: (v: unknown): Promise<unknown> =>
      v instanceof Error ? Promise.reject(v) : Promise.resolve(v),
    reset(): void {
      state.structured = null;
      state.parsed = null;
      state.schema = null;
      state.structuredCalls.length = 0;
      state.parsedCalls.length = 0;
      state.schemaCalls.length = 0;
    },
  };
  return state;
});

vi.mock("./wire/client.gen.js", () => ({
  configStructured: (opts?: unknown): Promise<unknown> => {
    wire.structuredCalls.push(opts);
    return wire.answer(wire.structured);
  },
  configParsed: (opts?: unknown): Promise<unknown> => {
    wire.parsedCalls.push(opts);
    return wire.answer(wire.parsed);
  },
  configSchema: (opts?: unknown): Promise<unknown> => {
    wire.schemaCalls.push(opts);
    return wire.answer(wire.schema);
  },
  // Reached by renderProvidersSection's health-badge pass.
  providerTimeouts: (): Promise<unknown> => Promise.resolve(null),
  PATH_RESET_CONFIG: "/api/config/reset",
  PATH_SAVE_CONFIG_STRUCTURED: "/api/config/structured",
}));

// The actions framework. `dispatch()` returns the real handle shape: a promise
// of the result with an `outcome` promise hanging off it.
const actions = vi.hoisted(() => {
  const state = {
    /** Registered action definitions, by name. */
    defs: new Map<string, unknown>(),
    calls: [] as { name: string; args: unknown }[],
    /** Per-action `.outcome`; defaults to success. */
    outcomes: new Map<string, unknown>(),
    /** Per-action awaited result; defaults to a non-null success value. */
    results: new Map<string, unknown>(),
    /** bindLoadingState registrations, recorded at module init. */
    loading: [] as { name: string; target: unknown }[],
    names(): string[] {
      return state.calls.map((c) => c.name);
    },
    dispatcher(name: string) {
      return (args: unknown): Promise<unknown> & { outcome: Promise<unknown> } => {
        state.calls.push({ name, args });
        const value = state.results.has(name) ? state.results.get(name) : { status: "ok" };
        const outcome = state.outcomes.get(name) ?? { status: "success", value };
        return Object.assign(Promise.resolve(value), {
          abort: (): void => undefined,
          outcome: Promise.resolve(outcome),
        });
      };
    },
    reset(): void {
      state.defs.clear();
      state.calls.length = 0;
      state.loading.length = 0;
      state.outcomes.clear();
      state.results.clear();
    },
  };
  return state;
});

vi.mock("@cplieger/actions", () => ({
  apiAction: (def: { name?: string }) => {
    if (def.name !== undefined) {
      actions.defs.set(def.name, def);
    }
    return {
      dispatch: actions.dispatcher(def.name ?? ""),
      cancel: (): void => undefined,
    };
  },
  bindLoadingState: (name: string, target: unknown): void => {
    actions.loading.push({ name, target });
  },
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
}));

// Toasts, recorded as text instead of rendered.
const toasts = vi.hoisted(() => {
  const state = {
    error: [] as string[],
    success: [] as string[],
    info: [] as string[],
    reset(): void {
      state.error.length = 0;
      state.success.length = 0;
      state.info.length = 0;
    },
  };
  return state;
});

vi.mock("./notify.js", () => ({
  error: (m: string): void => {
    toasts.error.push(m);
  },
  success: (m: string): void => {
    toasts.success.push(m);
  },
  info: (m: string): void => {
    toasts.info.push(m);
  },
}));

// status.ts drags the whole polling stack in; only pollStatus is consumed.
const polls = vi.hoisted(() => {
  const state = {
    count: 0,
    reset(): void {
      state.count = 0;
    },
  };
  return state;
});

vi.mock("./status.js", () => ({
  pollStatus: (): Promise<void> => {
    polls.count += 1;
    return Promise.resolve();
  },
}));

// dom.ts's confirm() delegates to the ask primitive; `answer` is the user.
const asked = vi.hoisted(() => {
  const state = {
    answer: true,
    calls: [] as { message: string; title: unknown }[],
    reset(): void {
      state.answer = true;
      state.calls.length = 0;
    },
  };
  return state;
});

vi.mock("@cplieger/ui-primitives/ask", () => ({
  ask: (message: string, opts?: { title?: string }): Promise<boolean> => {
    asked.calls.push({ message, title: opts?.title });
    return Promise.resolve(asked.answer);
  },
}));

// --- Harness ---------------------------------------------------------------

interface Harness {
  config: typeof ConfigModule;
  store: typeof StoreModule;
  bus: typeof BusModule;
  dlg: HTMLDialogElement;
  body: HTMLElement;
  saveBtn: HTMLButtonElement;
}

interface BootOpts {
  /** Answer the parsed-config fetch with `configured: false`. */
  unconfigured?: boolean;
  /** Shorthand for a structured response carrying these sections. */
  sections?: Record<string, unknown>;
  structured?: StructuredConfig | null;
  /** `null` = the parsed fetch answered nothing. */
  parsed?: ParsedConfig | null;
  schema?: SchemaSection[] | null;
  /** Make the structured fetch reject with this error. */
  failWith?: Error;
  /** Starting URL path. */
  path?: string;
}

/** A ParsedConfig with only the fields these tests read filled in. */
function parsedConfig(opts: { configured?: boolean; ignoredCodecs?: string[] } = {}): ParsedConfig {
  const pc: ParsedConfig = {
    adaptive: {},
    search: {},
    providers: {},
    language_rules: {},
    languages: ["en"],
    scores: {} as ParsedConfig["scores"],
    post_processing: {} as ParsedConfig["post_processing"],
    configured: opts.configured ?? true,
    sonarr_configured: false,
    radarr_configured: false,
  };
  if (opts.ignoredCodecs !== undefined) {
    pc.ignored_codecs = opts.ignoredCodecs;
  }
  return pc;
}

/** Rebuild the settings DOM, arm the doubles and import config.ts fresh. */
async function boot(opts: BootOpts = {}): Promise<Harness> {
  vi.resetModules();
  wire.reset();
  actions.reset();
  toasts.reset();
  polls.reset();
  asked.reset();

  if (opts.failWith !== undefined) {
    wire.structured = opts.failWith;
  } else if (opts.structured !== undefined) {
    wire.structured = opts.structured;
  } else {
    wire.structured = { sections: opts.sections ?? {} };
  }
  wire.parsed =
    opts.parsed === undefined
      ? parsedConfig({ configured: !(opts.unconfigured ?? false) })
      : opts.parsed;
  wire.schema = opts.schema ?? null;

  document.body.replaceChildren();
  const dlg = document.createElement("dialog");
  dlg.id = "configDialog";
  const body = document.createElement("div");
  body.id = "configBody";
  const closeBtn = document.createElement("button");
  closeBtn.id = "configClose";
  dlg.append(body, closeBtn);
  const saveBtn = document.createElement("button");
  saveBtn.id = "saveConfigBtn";
  document.body.append(dlg, saveBtn);

  history.replaceState(null, "", opts.path ?? "/");

  const store = await import("./store.js");
  // The same seeding and the same derived isUnconfigured app.ts installs at
  // boot, so "unconfigured" here means exactly what it means in the app.
  store.batch(() => {
    store.set("config", null);
    store.set("ignoredCodecs", new Set<string>());
  });
  store.computed("isUnconfigured", () => store.get("config")?.configured === false);

  const bus = await import("./bus.js");
  const config = await import("./config.js");
  return { config, store, bus, dlg, body, saveBtn };
}

/** Let the fire-and-forget load/save chains settle. */
async function flush(): Promise<void> {
  await new Promise<void>((resolve) => {
    setTimeout(resolve, 0);
  });
}

/** Open the drawer and let its config load finish. */
async function openDrawer(h: Harness): Promise<void> {
  h.config.openConfig(true);
  await flush();
}

/** A press that both starts and ends on the dialog element: the backdrop. */
function backdropPress(dlg: HTMLDialogElement): void {
  dlg.dispatchEvent(new Event("mousedown"));
  dlg.dispatchEvent(new Event("mouseup"));
}

/** Finish the primitive's leave fade the way a real CSS transition does. */
function finishFade(dlg: HTMLDialogElement): void {
  dlg.dispatchEvent(new Event("transitionend"));
}

const UNCONFIGURED_TOAST = "Save a valid configuration before closing settings";

// One plain fields section, enough for the form to render something addressable.
const SEARCH_SCHEMA: SchemaSection[] = [
  {
    key: "search",
    title: "Search",
    type: "fields",
    fields: [{ key: "min_score", label: "Min Score", type: "number" }],
  },
];

// A required field, for the first-setup marking pass.
const SONARR_SCHEMA: SchemaSection[] = [
  {
    key: "sonarr",
    title: "Sonarr",
    type: "fields",
    fields: [{ key: "url", label: "URL", type: "text", required: true }],
  },
];

const AUTH_SCHEMA: SchemaSection[] = [
  {
    key: "auth",
    title: "Authentication",
    type: "fields",
    fields: [{ key: "webauthn_rp_id", label: "WebAuthn RP ID", type: "text" }],
  },
];

/** The rendered input for one schema field. */
function fieldInput(h: Harness, section: string, field: string): HTMLInputElement {
  const inp = h.body.querySelector<HTMLInputElement>(`#cfg-${section}-${field}`);
  if (!inp) {
    throw new Error(`missing rendered input #cfg-${section}-${field}`);
  }
  return inp;
}

// --- Dismissal policy ------------------------------------------------------

describe("config: settings drawer dismissal", () => {
  it("closes on a backdrop press once the config is valid", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });
    await openDrawer(h);
    expect(h.dlg.open).toBe(true);

    backdropPress(h.dlg);
    finishFade(h.dlg);

    expect(h.dlg.open).toBe(false);
  });

  it("refuses a backdrop press while unconfigured and says why", async () => {
    const h = await boot({ unconfigured: true, schema: SEARCH_SCHEMA });
    await openDrawer(h);

    backdropPress(h.dlg);
    finishFade(h.dlg);

    // The drawer is the only way out of unconfigured mode, so a dismissal
    // must not strand the user on an unusable app.
    expect(h.dlg.open).toBe(true);
    expect(toasts.error).toContain(UNCONFIGURED_TOAST);
  });

  it("refuses Escape while unconfigured too", async () => {
    const h = await boot({ unconfigured: true, schema: SEARCH_SCHEMA });
    await openDrawer(h);

    // The platform fires `cancel` on Escape; the primitive intercepts it.
    h.dlg.dispatchEvent(new Event("cancel", { cancelable: true }));
    finishFade(h.dlg);

    expect(h.dlg.open).toBe(true);
    expect(toasts.error).toContain(UNCONFIGURED_TOAST);
  });

  it("restores the URL after closing while parked on /settings", async () => {
    const h = await boot({ path: "/settings", schema: SEARCH_SCHEMA });
    await openDrawer(h);

    h.config.closeConfig();
    finishFade(h.dlg);

    expect(location.pathname).toBe("/");
  });

  it("leaves a non-settings URL alone after closing", async () => {
    const h = await boot({ path: "/library", schema: SEARCH_SCHEMA });
    await openDrawer(h);

    h.config.closeConfig();
    finishFade(h.dlg);

    expect(location.pathname).toBe("/library");
  });
});

// --- open / close ----------------------------------------------------------

describe("config: openConfig", () => {
  it("pushes /settings and opens the drawer", async () => {
    const h = await boot({ path: "/", schema: SEARCH_SCHEMA });

    h.config.openConfig();

    expect(location.pathname).toBe("/settings");
    expect(h.dlg.open).toBe(true);
    await flush();
  });

  it("opens the drawer without touching history when the push is skipped", async () => {
    const h = await boot({ path: "/", schema: SEARCH_SCHEMA });

    // The router already navigated; a push here would duplicate the entry.
    h.config.openConfig(true);

    expect(location.pathname).toBe("/");
    expect(h.dlg.open).toBe(true);
    await flush();
  });
});

describe("config: closeConfig", () => {
  it("closes the drawer once the config is valid", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });
    await openDrawer(h);

    h.config.closeConfig();
    finishFade(h.dlg);

    expect(h.dlg.open).toBe(false);
  });

  it("refuses a programmatic close while unconfigured and says why", async () => {
    const h = await boot({ unconfigured: true, schema: SEARCH_SCHEMA });
    await openDrawer(h);

    h.config.closeConfig();
    finishFade(h.dlg);

    expect(h.dlg.open).toBe(true);
    expect(toasts.error).toContain(UNCONFIGURED_TOAST);
  });
});

// --- The config load -------------------------------------------------------

describe("config: loading the config into the drawer", () => {
  it("requests all three config endpoints under one abort signal", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });

    await openDrawer(h);

    // The signal is the YAML timeout: without it a wedged server leaves the
    // drawer waiting forever.
    expect(wire.structuredCalls).toEqual([{ signal: expect.any(AbortSignal) }]);
    expect(wire.parsedCalls).toEqual([{ signal: expect.any(AbortSignal) }]);
    expect(wire.schemaCalls).toEqual([{ signal: expect.any(AbortSignal) }]);
  });

  it("puts the parsed config and its ignored codecs in the store", async () => {
    const parsed = parsedConfig({ ignoredCodecs: ["pgs", "vobsub"] });
    const h = await boot({ parsed, schema: SEARCH_SCHEMA });

    await openDrawer(h);

    expect(h.store.get("config")).toEqual(parsed);
    expect(h.store.get("ignoredCodecs")).toEqual(new Set(["pgs", "vobsub"]));
  });

  it("leaves the ignored-codec set empty when the config lists none", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });

    await openDrawer(h);

    expect(h.store.get("ignoredCodecs")).toEqual(new Set());
  });

  it("renders the fetched schema into the form", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });

    await openDrawer(h);

    expect(h.body.querySelector("#cfg-search-min_score")).not.toBeNull();
  });

  it("keeps the last known schema when a later fetch answers with nothing", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });
    await openDrawer(h);

    // A schema endpoint blip must not blank the form the user is editing.
    wire.schema = null;
    await openDrawer(h);

    expect(h.body.querySelector("#cfg-search-min_score")).not.toBeNull();
  });

  it("shows the loading placeholder while no schema has arrived", async () => {
    const h = await boot({ schema: null });

    await openDrawer(h);

    expect(h.body.textContent).toContain("Loading schema");
  });

  it("reports the reason when the config cannot be loaded", async () => {
    const h = await boot({ failWith: new Error("boom"), schema: SEARCH_SCHEMA });

    await openDrawer(h);

    expect(toasts.error).toContain("Failed to load config: boom");
  });

  it("re-opens the drawer when a load finds the app unconfigured", async () => {
    // The reload at the end of a save runs with the drawer shut in exactly
    // this case; the app is unusable until the config is valid, so the load
    // must bring the drawer back rather than leave a blank page.
    const h = await boot({ unconfigured: true, schema: SEARCH_SCHEMA });
    expect(h.dlg.open).toBe(false);

    await h.config.saveConfig();
    await flush();

    expect(h.dlg.open).toBe(true);
  });

  it("leaves a shut drawer shut when a load finds the app configured", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });

    await h.config.saveConfig();
    await flush();

    expect(h.dlg.open).toBe(false);
  });
});

describe("config: the save button", () => {
  it("is bound to the config.save loading state at module init", async () => {
    const h = await boot({});

    // Static button, bound once: the in-flight window is what makes a
    // seconds-long save visible and double-click-proof.
    expect(actions.loading).toEqual([{ name: "config.save", target: h.saveBtn }]);
  });
});

// --- Saving ----------------------------------------------------------------

describe("config: saveConfig", () => {
  it("invalidates the cached data, reloads the config and closes the drawer", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });
    await openDrawer(h);
    const invalidated: string[] = [];
    h.bus.on("data:invalidate", () => {
      invalidated.push("x");
    });
    const schemaFetches = wire.schemaCalls.length;

    await h.config.saveConfig();
    await flush();
    finishFade(h.dlg);

    expect(actions.names()).toContain("config.save");
    expect(invalidated).toHaveLength(1);
    expect(wire.schemaCalls.length).toBeGreaterThan(schemaFetches);
    expect(h.dlg.open).toBe(false);
  });

  it("keeps the drawer open and invalidates nothing when the save fails", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });
    await openDrawer(h);
    const invalidated: string[] = [];
    h.bus.on("data:invalidate", () => {
      invalidated.push("x");
    });
    const schemaFetches = wire.schemaCalls.length;
    actions.outcomes.set("config.save", { status: "error", error: { message: "nope" } });

    await h.config.saveConfig();
    await flush();

    // The form still holds the rejected edit, so the user can fix it.
    expect(invalidated).toHaveLength(0);
    expect(wire.schemaCalls.length).toBe(schemaFetches);
    expect(h.dlg.open).toBe(true);
  });

  it("announces the first save out of unconfigured mode and re-polls status", async () => {
    const h = await boot({ unconfigured: true, schema: SEARCH_SCHEMA });
    await openDrawer(h);
    expect(h.store.get("isUnconfigured")).toBe(true);
    // The server accepted the config, so the reload sees a valid one.
    wire.parsed = parsedConfig();

    await h.config.saveConfig();
    await flush();

    expect(toasts.success).toContain("Subflux is now configured and running");
    expect(polls.count).toBe(1);
  });

  it("stays quiet about first-time setup on an ordinary save", async () => {
    const h = await boot({ schema: SEARCH_SCHEMA });
    await openDrawer(h);

    await h.config.saveConfig();
    await flush();

    expect(toasts.success).not.toContain("Subflux is now configured and running");
    expect(polls.count).toBe(0);
  });
});

// --- The WebAuthn RP ID guard ---------------------------------------------

describe("config: WebAuthn RP ID change guard", () => {
  it("asks before a change that would strand existing passkeys", async () => {
    const h = await boot({
      schema: AUTH_SCHEMA,
      sections: { auth: { webauthn_rp_id: "example.com" } },
    });
    await openDrawer(h);
    fieldInput(h, "auth", "webauthn_rp_id").value = "other.example";
    asked.answer = false;

    await h.config.saveConfig();

    expect(asked.calls).toHaveLength(1);
    expect(actions.names()).toHaveLength(0);
  });

  it("saves the change once the user accepts it", async () => {
    const h = await boot({
      schema: AUTH_SCHEMA,
      sections: { auth: { webauthn_rp_id: "example.com" } },
    });
    await openDrawer(h);
    fieldInput(h, "auth", "webauthn_rp_id").value = "other.example";
    asked.answer = true;

    await h.config.saveConfig();
    await flush();

    expect(asked.calls).toHaveLength(1);
    expect(actions.names()).toContain("config.save");
  });

  it("asks nothing when the RP ID is unchanged", async () => {
    const h = await boot({
      schema: AUTH_SCHEMA,
      sections: { auth: { webauthn_rp_id: "example.com" } },
    });
    await openDrawer(h);
    // The rendered field already holds the stored value; save it back as-is.
    expect(fieldInput(h, "auth", "webauthn_rp_id").value).toBe("example.com");

    await h.config.saveConfig();
    await flush();

    expect(asked.calls).toHaveLength(0);
    expect(actions.names()).toContain("config.save");
  });

  it("asks nothing when an RP ID is set for the first time", async () => {
    // No stored RP ID means no credentials to strand.
    const h = await boot({ schema: AUTH_SCHEMA, sections: { auth: {} } });
    await openDrawer(h);
    fieldInput(h, "auth", "webauthn_rp_id").value = "example.com";

    await h.config.saveConfig();
    await flush();

    expect(asked.calls).toHaveLength(0);
    expect(actions.names()).toContain("config.save");
  });

  it("does not treat whitespace around the stored RP ID as a change", async () => {
    const h = await boot({
      schema: AUTH_SCHEMA,
      sections: { auth: { webauthn_rp_id: "  example.com  " } },
    });
    await openDrawer(h);
    fieldInput(h, "auth", "webauthn_rp_id").value = "example.com";

    await h.config.saveConfig();
    await flush();

    expect(asked.calls).toHaveLength(0);
  });

  it("does not treat whitespace around the typed RP ID as a change", async () => {
    const h = await boot({
      schema: AUTH_SCHEMA,
      sections: { auth: { webauthn_rp_id: "example.com" } },
    });
    await openDrawer(h);
    fieldInput(h, "auth", "webauthn_rp_id").value = "  example.com  ";

    await h.config.saveConfig();
    await flush();

    expect(asked.calls).toHaveLength(0);
  });

  it("asks when the form carries no auth section at all", async () => {
    // The schema renders no auth section, so the payload has no auth key —
    // dropping a stored RP ID is still a change that strands passkeys.
    const h = await boot({
      schema: SEARCH_SCHEMA,
      sections: { auth: { webauthn_rp_id: "example.com" } },
    });
    await openDrawer(h);
    asked.answer = false;

    await h.config.saveConfig();

    expect(asked.calls).toHaveLength(1);
    expect(actions.names()).toHaveLength(0);
  });
});

// --- Reset to defaults ----------------------------------------------------

describe("config: reset to defaults", () => {
  /** The first-setup banner's reset button. */
  function resetButton(h: Harness): HTMLButtonElement {
    const btn = h.body.querySelector<HTMLButtonElement>(".cfg-banner button");
    if (!btn) {
      throw new Error("no reset button in the setup banner");
    }
    return btn;
  }

  it("asks the server for defaults and reloads the form", async () => {
    const h = await boot({ unconfigured: true, sections: {}, schema: SEARCH_SCHEMA });
    await openDrawer(h);
    const btn = resetButton(h);
    expect(btn.className).toBe("ghost");
    const schemaFetches = wire.schemaCalls.length;

    btn.click();
    await flush();

    expect(actions.names()).toContain("config.reset");
    expect(wire.schemaCalls.length).toBeGreaterThan(schemaFetches);
  });

  it("does not reload the form when the reset fails", async () => {
    const h = await boot({ unconfigured: true, sections: {}, schema: SEARCH_SCHEMA });
    await openDrawer(h);
    const schemaFetches = wire.schemaCalls.length;
    actions.results.set("config.reset", null);

    resetButton(h).click();
    await flush();

    expect(actions.names()).toContain("config.reset");
    expect(wire.schemaCalls.length).toBe(schemaFetches);
  });
});

// --- The rendered form ----------------------------------------------------

describe("config: rendering the config form", () => {
  it("shows the setup banner and marks required fields on a first boot", async () => {
    const h = await boot({ unconfigured: true, sections: {}, schema: SONARR_SCHEMA });

    await openDrawer(h);

    expect(h.body.querySelector(".cfg-banner")).not.toBeNull();
    expect(h.body.textContent).toContain("Configure at least one of Sonarr or Radarr");
    expect(fieldInput(h, "sonarr", "url").classList.contains("cfg-required")).toBe(true);
  });

  it("shows the errors banner when an existing config fails validation", async () => {
    const h = await boot({
      unconfigured: true,
      sections: { search: { min_score: 70 } },
      schema: SEARCH_SCHEMA,
    });

    await openDrawer(h);

    expect(h.body.querySelector(".cfg-banner")).not.toBeNull();
    expect(h.body.textContent).toContain("Configuration has errors");
  });

  it("shows no banner and marks nothing once the config is valid", async () => {
    // A blank required field on an EXISTING config is not decorated: the red
    // borders are the first-run setup aid, not a permanent validator.
    const h = await boot({ sections: { sonarr: {} }, schema: SONARR_SCHEMA });

    await openDrawer(h);

    expect(h.body.querySelector(".cfg-banner")).toBeNull();
    expect(fieldInput(h, "sonarr", "url").value).toBe("");
    expect(fieldInput(h, "sonarr", "url").classList.contains("cfg-required")).toBe(false);
  });

  it("shows no banner for a valid config whose sections could not be read", async () => {
    // The structured fetch degrades to {} so the form can still render from
    // schema defaults; that is not a first-time setup.
    const h = await boot({ sections: {}, schema: SEARCH_SCHEMA });

    await openDrawer(h);

    expect(h.body.querySelector(".cfg-banner")).toBeNull();
  });

  it("routes each section type to its own renderer", async () => {
    const schema: SchemaSection[] = [
      {
        key: "search",
        title: "Search",
        type: "fields",
        fields: [{ key: "min_score", label: "Min Score", type: "number" }],
      },
      { key: "media_roots", title: "Media Roots", type: "list" },
      {
        key: "providers",
        title: "Providers",
        type: "providers",
        providers: [{ name: "opensubtitles", label: "OpenSubtitles" }],
      },
      { key: "languages", title: "Languages", type: "languages" },
    ];
    const h = await boot({ sections: {}, schema });

    await openDrawer(h);

    expect(h.body.querySelector("#cfg-search-min_score")).not.toBeNull();
    expect(h.body.querySelector("#media_roots-list")).not.toBeNull();
    expect(h.body.querySelector("#cfg-prov-opensubtitles-enabled")).not.toBeNull();
    expect(h.body.querySelector("#lang-rules")).not.toBeNull();
  });

  it("renders an unknown config section as raw JSON and a known one only once", async () => {
    const h = await boot({
      sections: { search: { min_score: 70 }, mystery: { a: 1 } },
      schema: SEARCH_SCHEMA,
    });

    await openDrawer(h);

    expect(h.body.querySelector("#section-mystery")).not.toBeNull();
    expect(h.body.querySelector("#section-search")).toBeNull();
  });
});

// --- Save-error message tables --------------------------------------------
//
// friendlyConfigError and configSaveError read two tables built at MODULE
// SCOPE. Both are asserted here, against the module this file imports inside
// the test, because a statically-imported module's initializers run before any
// test claims them — so the tables are only reachable per-test from a
// dynamically-imported instance.

interface SaveActionDef {
  decodeError?: (info: { status: number; body?: unknown }) => {
    error: { message: string; status: number; code?: string };
  };
}

/** The user-visible message config.save's decodeError makes of a server body. */
function saveMessage(body: unknown): string {
  const def = actions.defs.get("config.save") as SaveActionDef | undefined;
  if (!def?.decodeError) {
    throw new Error("config.save registered no decodeError");
  }
  return def.decodeError({ status: 422, body }).error.message;
}

describe("config: save-error message tables", () => {
  it("maps a known error code to its own sentence", async () => {
    await boot({});

    expect(saveMessage({ error: "activate: bad provider", code: "config_reload_failed" })).toBe(
      "Configuration could not be applied and was not saved. Check server logs for details.",
    );
  });

  it("adds the fix hint to a recognized YAML syntax error", async () => {
    await boot({});

    expect(
      saveMessage({ error: "yaml: line 4: mapping values are not allowed in this context" }),
    ).toBe("Syntax error on line 4: check indentation and colons");
  });
});

// --- Language/provider initialization -------------------------------------

describe("config: initLanguages", () => {
  it("puts the parsed config and its ignored codecs in the store", async () => {
    const parsed = parsedConfig({ ignoredCodecs: ["pgs"] });
    const h = await boot({ parsed });

    await h.config.initLanguages();

    expect(h.store.get("config")).toEqual(parsed);
    expect(h.store.get("ignoredCodecs")).toEqual(new Set(["pgs"]));
  });

  it("leaves the ignored-codec set empty when the config lists none", async () => {
    const h = await boot({});

    await h.config.initLanguages();

    expect(h.store.get("ignoredCodecs")).toEqual(new Set());
  });
});
