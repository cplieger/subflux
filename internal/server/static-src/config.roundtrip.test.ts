// config.roundtrip.test.ts — the settings form against the real schema.
//
// testdata/settings-schema.json and settings-structured.json are written by
// settings_schema_test.go from the real registry and handler; only the
// transport is replaced. The captured save is the committed
// settings-save-payload.golden.json that confighandlers replays: regenerate it
// with `npx vitest --run -u config.roundtrip.test.ts` and review the diff.
import { describe, it, expect, vi } from "vitest";

import type * as ConfigModule from "./config.js";
import schemaRaw from "./testdata/settings-schema.json?raw";
import structuredRaw from "./testdata/settings-structured.json?raw";

const net = vi.hoisted(() => {
  const state = {
    answers: new Map<string, unknown>(),
    posts: [] as { path: string; body: unknown }[],
  };
  return state;
});

function answer(method: string, path: string, body: unknown, decode?: (v: unknown) => unknown) {
  if (method !== "GET") {
    net.posts.push({ path, body });
    return { valid: true };
  }
  const v = net.answers.get(path) ?? null;
  return v === null || decode === undefined ? v : decode(v);
}

vi.mock("./api-client.js", () => ({
  clientRequest: (
    method: string,
    path: string,
    body: unknown,
    decode?: (v: unknown) => unknown,
  ): Promise<unknown> => Promise.resolve(answer(method, path, body, decode)),
  clientRequestOK: (method: string, path: string, body: unknown): Promise<boolean> =>
    Promise.resolve(answer(method, path, body) !== null),
  clientRequestRaw: (
    method: string,
    path: string,
    body: unknown,
    decode?: (v: unknown) => unknown,
  ): Promise<unknown> =>
    Promise.resolve({ ok: true, status: 200, data: answer(method, path, body, decode) }),
  fillPath: (template: string): string => template,
  handleSessionExpiry: (): void => undefined,
  authFetch: (): Promise<Response> => Promise.reject(new Error("no network in this test")),
}));

// A failed outcome keeps the form as the reader left it: a successful save
// re-renders from the stored config, which would undo every toggle so far.
const saves = vi.hoisted(() => ({ payloads: [] as Record<string, unknown>[] }));
vi.mock("@cplieger/actions", () => ({
  apiAction: (def: { name?: string }) => ({
    dispatch: (args: unknown) => {
      if (def.name === "config.save") {
        saves.payloads.push(structuredClone(args) as Record<string, unknown>);
      }
      return Object.assign(Promise.resolve(null), {
        abort: (): void => undefined,
        outcome: Promise.resolve({ status: "error" }),
      });
    },
    cancel: (): void => undefined,
  }),
  bindLoadingState: (): void => undefined,
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
}));

const toasts = vi.hoisted(() => ({ errors: [] as string[] }));
vi.mock("./notify.js", () => ({
  error: (m: string): void => {
    toasts.errors.push(m);
  },
  success: (): void => undefined,
  info: (): void => undefined,
}));

vi.mock("./status.js", () => ({
  pollStatus: (): Promise<void> => Promise.resolve(),
}));

interface SchemaFixture {
  key: string;
  conn_test?: boolean;
  providers?: { name: string; conn_test?: boolean }[];
}

const SCHEMA = JSON.parse(schemaRaw) as SchemaFixture[];
const PROVIDERS = SCHEMA.find((s) => s.key === "providers")?.providers ?? [];

const PARSED_CONFIG = {
  adaptive: {},
  search: {},
  providers: {},
  language_rules: {},
  languages: ["en", "fr"],
  scores: {
    hash: 100,
    source: 27,
    release_group: 22,
    streaming_service: 13,
    video_codec: 11,
    hdr: 9,
    edition: 16,
    season_pack: 14,
  },
  post_processing: {
    strip_hi: true,
    strip_tags: false,
    normalize_utf8: true,
    clean_whitespace: true,
    normalize_endings: false,
    remove_empty: false,
  },
  configured: true,
  sonarr_configured: true,
  radarr_configured: true,
};

let booted: Promise<typeof ConfigModule> | null = null;

/** Build the settings DOM, answer its three reads from the fixtures and open the
 *  drawer once per file: every test reads the same rendered form. */
function bootForm(): Promise<typeof ConfigModule> {
  booted ??= (async () => {
    net.answers.set("/api/config/schema", JSON.parse(schemaRaw));
    net.answers.set("/api/config/structured", JSON.parse(structuredRaw));
    net.answers.set("/api/config/parsed", PARSED_CONFIG);

    const dlg = document.createElement("dialog");
    dlg.id = "configDialog";
    const body = document.createElement("div");
    body.id = "configBody";
    const close = document.createElement("button");
    close.id = "configClose";
    dlg.append(body, close);
    const saveBtn = document.createElement("button");
    saveBtn.id = "saveConfigBtn";
    document.body.replaceChildren(dlg, saveBtn);

    const store = await import("./store.js");
    store.batch(() => {
      store.set("config", null);
      store.set("ignoredCodecs", new Set<string>());
    });
    store.computed("isUnconfigured", () => store.get("config")?.configured === false);

    const config = await import("./config.js");
    config.openConfig(true);
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
    if (toasts.errors.length > 0) {
      throw new Error(`Setup: the settings form did not load: ${toasts.errors.join("; ")}`);
    }
    return config;
  })();
  return booted;
}

function byId<T extends HTMLElement>(id: string): T {
  const found = document.getElementById(id);
  if (!found) {
    throw new Error(`settings form has no #${id}`);
  }
  return found as T;
}

/** Every object key in a payload, at any depth, as a dotted path. */
function keyPaths(v: unknown, prefix = ""): string[] {
  if (typeof v !== "object" || v === null || Array.isArray(v)) {
    return [];
  }
  return Object.entries(v).flatMap(([k, child]) => [
    prefix + k,
    ...keyPaths(child, `${prefix}${k}/`),
  ]);
}

describe("settings save round trip", () => {
  it("saves every provider toggled off without a dotted key and with auth.oidc nested", async () => {
    const config = await bootForm();
    saves.payloads.length = 0;

    for (const prov of PROVIDERS) {
      const toggle = byId<HTMLInputElement>(`cfg-prov-${prov.name}-enabled`);
      expect(toggle.checked, `${prov.name} renders enabled from the fixture`).toBe(true);
      toggle.checked = false;
      toggle.dispatchEvent(new Event("change"));

      await config.saveConfig();

      const payload = saves.payloads.at(-1);
      expect(
        keyPaths(payload).filter((k) => k.includes(".")),
        prov.name,
      ).toStrictEqual([]);
      const auth = payload?.["auth"] as Record<string, unknown> | undefined;
      expect(auth?.["oidc"], prov.name).toStrictEqual({
        issuer_url: "https://auth.example.com/application/o/subflux/",
        client_id: "placeholder-oidc-client-id",
        client_secret: "",
        redirect_uri: "https://subflux.example.com/api/auth/oidc/callback",
      });
      const providers = payload?.["providers"] as Record<string, { enabled: boolean }>;
      expect(providers[prov.name]?.enabled, prov.name).toBe(false);
    }

    expect(saves.payloads).toHaveLength(PROVIDERS.length);
    await expect(`${JSON.stringify(saves.payloads.at(-1), null, 2)}\n`).toMatchFileSnapshot(
      "./testdata/settings-save-payload.golden.json",
    );

    // The form is shared by every test in this file.
    for (const prov of PROVIDERS) {
      const toggle = byId<HTMLInputElement>(`cfg-prov-${prov.name}-enabled`);
      toggle.checked = true;
      toggle.dispatchEvent(new Event("change"));
    }
  });

  it("renders the stored languages, not a default, into the save", async () => {
    const config = await bootForm();

    const sections = config.buildSectionsFromForm(
      SCHEMA as Parameters<typeof config.buildSectionsFromForm>[0],
    );

    expect(sections["languages"]).toStrictEqual(
      (JSON.parse(structuredRaw) as { sections: Record<string, unknown> }).sections["languages"],
    );
  });

  it("writes every stored value back unchanged when nothing in the form was edited", async () => {
    const config = await bootForm();
    const stored = (JSON.parse(structuredRaw) as { sections: Record<string, unknown> }).sections;

    const sections = config.buildSectionsFromForm(
      SCHEMA as Parameters<typeof config.buildSectionsFromForm>[0],
    );

    const lost = leafValues(stored).filter(([path, v]) => !Object.is(leafAt(sections, path), v));
    expect(lost).toStrictEqual([]);
  });
});

/** Every scalar or list in a stored config, as [path, value]; a list is one
 *  leaf, compared by its key-order-insensitive JSON. */
function leafValues(v: unknown, path: string[] = []): [string[], unknown][] {
  if (typeof v !== "object" || v === null) {
    return [[path, v]];
  }
  if (Array.isArray(v)) {
    return [[path, canonicalJSON(v)]];
  }
  return Object.entries(v).flatMap(([k, child]) => leafValues(child, [...path, k]));
}

function leafAt(v: unknown, path: readonly string[]): unknown {
  let cur = v;
  for (const k of path) {
    cur = typeof cur === "object" && cur !== null ? (cur as Record<string, unknown>)[k] : undefined;
  }
  return Array.isArray(cur) ? canonicalJSON(cur) : cur;
}

function canonicalJSON(v: unknown): string {
  return JSON.stringify(v, (_k, val: unknown) =>
    typeof val === "object" && val !== null && !Array.isArray(val)
      ? Object.fromEntries(Object.entries(val).sort(([a], [b]) => a.localeCompare(b)))
      : val,
  );
}

describe("settings credential-check controls", () => {
  it("puts a test control in the header of sonarr, radarr and every checkable provider", async () => {
    await bootForm();
    const want = [
      "sonarr",
      "radarr",
      ...PROVIDERS.filter((p) => p.conn_test === true).map((p) => p.name),
    ].sort();

    const got = [...document.querySelectorAll(".conn-test")].map((btn) => {
      const head = btn.closest(".cfg-title, .provider-head");
      const toggle = head?.querySelector<HTMLInputElement>("input[type=checkbox]");
      return (toggle?.id ?? "")
        .replace(/^cfg-prov-/, "")
        .replace(/^cfg-/, "")
        .replace(/-enabled$/, "");
    });

    expect(got.sort()).toStrictEqual(want);
  });

  it("posts the sonarr section's own settings when its control is pressed", async () => {
    await bootForm();
    net.posts.length = 0;
    const sonarrHead = byId<HTMLInputElement>("cfg-sonarr-enabled").closest(".cfg-title");

    sonarrHead?.querySelector<HTMLButtonElement>(".conn-test")?.click();
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });

    expect(net.posts).toStrictEqual([
      {
        path: "/api/config/test-connection",
        body: {
          kind: "sonarr",
          settings: {
            url: "http://sonarr:8989",
            api_key: "",
            public_url: "https://sonarr.example.com",
          },
        },
      },
    ]);
  });
});
