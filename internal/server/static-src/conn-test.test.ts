// conn-test.test.ts — the shared credential-check control.
//
// The control is the same object in the settings dialog and the setup wizard, so
// its risks are behavioral rather than per-host: a verdict that outlives the
// values it was about (a green beside a key edited since), a transport failure
// rendered as a verdict about the service, a stale answer from an abandoned
// click painting over a newer one, and a success clearing a banner message
// somebody else wrote. Those are what these tests aim at; where the control is
// APPENDED is covered by the four hosts' own suites.
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { connTestControl } from "./conn-test.js";
import { configBanner, configBannerHost } from "./config-banner.js";
import type { ApiResult } from "./api-client.js";
import type { ConnTestResponse } from "./wire/types.gen.js";

const wire = vi.hoisted(() => ({
  calls: [] as unknown[],
  answers: [] as ApiResult<ConnTestResponse>[],
  defer: false,
  releases: [] as (() => void)[],
}));

vi.mock("./wire/client.gen.js", () => ({
  testConnectionRaw: (body: unknown): Promise<ApiResult<ConnTestResponse>> => {
    wire.calls.push(body);
    const next = (): ApiResult<ConnTestResponse> =>
      wire.answers.shift() ?? { ok: true, status: 200, data: { valid: true } };
    if (wire.defer) {
      return new Promise<ApiResult<ConnTestResponse>>((resolve) => {
        wire.releases.push(() => {
          resolve(next());
        });
      });
    }
    return Promise.resolve(next());
  },
}));

interface Mounted {
  btn: HTMLButtonElement;
  banner: HTMLElement;
  inputs: Record<string, HTMLInputElement>;
}

/** mount renders one control over freshly created inputs and the settings
 *  form's REAL banner, so the writer under test is the shipped one. */
function mount(kind = "sonarr", keys: string[] = ["url", "api_key"]): Mounted {
  const inputs: Record<string, HTMLInputElement> = {};
  for (const key of keys) {
    inputs[key] = document.createElement("input");
  }
  const btn = connTestControl(kind, { inputs, banner: configBanner });
  document.body.replaceChildren(configBannerHost, ...Object.values(inputs), btn);
  return { btn, banner: configBannerHost, inputs };
}

/** messages names what the banner is showing: the host is permanent, so the
 *  observable is which `.cfg-banner` messages it holds, in order. */
function messages(host: HTMLElement): string[] {
  return [...host.querySelectorAll(".cfg-banner")].map((m) => m.textContent ?? "");
}

/** settle lets the click's awaited chain run to completion under fake timers,
 *  which also flush the microtasks between them. */
async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
}

/** state names what the control is showing, so a case asserts one thing rather
 *  than three attributes. */
function state(btn: HTMLButtonElement): string {
  if (btn.querySelector(".spinner")) {
    return "busy";
  }
  if (btn.querySelector(".icon-check")) {
    return "ok";
  }
  if (btn.querySelector(".icon-close")) {
    return "err";
  }
  if (btn.querySelector(".icon-flask")) {
    return "idle";
  }
  return "unknown";
}

beforeEach(() => {
  wire.calls = [];
  wire.answers = [];
  wire.defer = false;
  wire.releases = [];
  vi.useFakeTimers();
  document.body.replaceChildren();
});

afterEach(() => {
  vi.useRealTimers();
  // Browser Mode isolates per FILE, not per test, and nothing clears the body
  // for us. The banner host is module-held, so its message outlives the body.
  configBanner.hide();
  document.body.replaceChildren();
});

describe("conn-test: what it sends", () => {
  it("sends the section kind with every watched field, as typed", async () => {
    const m = mount("radarr");
    m.inputs["url"]!.value = "http://radarr:7878";
    m.inputs["api_key"]!.value = "k1";
    m.btn.click();
    await settle();

    expect(wire.calls).toEqual([
      { kind: "radarr", settings: { url: "http://radarr:7878", api_key: "k1" } },
    ]);
  });

  it("sends an empty secret rather than refusing, so the server can use the stored one", async () => {
    // A saved secret renders as an empty field (the redacting GET never ships
    // the value), so a control that demanded one would fail the test on exactly
    // the configs that already work.
    const m = mount("opensubtitles", ["username", "password", "api_key"]);
    m.inputs["username"]!.value = "u";
    m.btn.click();
    await settle();

    expect(wire.calls).toEqual([
      { kind: "opensubtitles", settings: { username: "u", password: "", api_key: "" } },
    ]);
  });

  it("sends a provider's own setting keys, not a url/api_key pair", async () => {
    // The body is keyed by the section's schema, which for hdbits is a username
    // and a passkey; a control hard-coded to the arr pair would send neither.
    const m = mount("hdbits", ["username", "passkey"]);
    m.inputs["passkey"]!.value = "pk";
    m.btn.click();
    await settle();

    expect(wire.calls).toEqual([{ kind: "hdbits", settings: { username: "", passkey: "pk" } }]);
  });

  it("sends a checkbox as its checked state, matching the boolean a save writes", async () => {
    // A checkbox's `value` is "on" whether checked or not, and no saved boolean
    // equals it, so a passing check sending it would leave the disable in place.
    const m = mount("opensubtitles", ["username", "use_hash", "include_ai_translated"]);
    m.inputs["use_hash"]!.type = "checkbox";
    m.inputs["use_hash"]!.checked = true;
    m.inputs["include_ai_translated"]!.type = "checkbox";
    m.btn.click();
    await settle();

    expect(wire.calls).toEqual([
      {
        kind: "opensubtitles",
        settings: { username: "", use_hash: "true", include_ai_translated: "false" },
      },
    ]);
  });
});

describe("conn-test: every state is reachable", () => {
  it("starts idle, showing the flask and an accessible name", () => {
    const m = mount();
    expect(state(m.btn)).toBe("idle");
    expect(m.btn.disabled).toBe(false);
    expect(m.btn.hasAttribute("aria-busy")).toBe(false);
    expect(m.btn.getAttribute("aria-label")).toBe("Test credentials");
  });

  it("shows a spinner and reports busy while the request is in flight", () => {
    wire.defer = true;
    const m = mount();
    m.btn.click();

    expect(state(m.btn)).toBe("busy");
    expect(m.btn.disabled).toBe(true);
    expect(m.btn.getAttribute("aria-busy")).toBe("true");
  });

  it("shows a checkmark for accepted credentials", async () => {
    const m = mount();
    m.btn.click();
    await settle();

    expect(state(m.btn)).toBe("ok");
    expect(m.btn.dataset["status"]).toBe("ok");
    expect(m.btn.disabled).toBe(false);
    expect(m.btn.hasAttribute("aria-busy")).toBe(false);
  });

  it("shows a cross for refused credentials", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    const m = mount();
    m.btn.click();
    await settle();

    expect(state(m.btn)).toBe("err");
    expect(m.btn.dataset["status"]).toBe("err");
  });
});

describe("conn-test: a pass that re-enabled a provider says so", () => {
  it("names what the pass did on the control until it fades", async () => {
    wire.answers = [
      {
        ok: true,
        status: 200,
        data: { valid: true, message: "credentials accepted; provider re-enabled" },
      },
    ];
    const m = mount("hdbits", ["username", "passkey"]);
    m.btn.click();
    await settle();
    expect([m.btn.getAttribute("aria-label"), m.btn.getAttribute("data-tip")]).toEqual([
      "credentials accepted; provider re-enabled",
      "credentials accepted; provider re-enabled",
    ]);

    await vi.advanceTimersByTimeAsync(3000);

    expect([m.btn.getAttribute("aria-label"), m.btn.getAttribute("data-tip")]).toEqual([
      "Test credentials",
      "Test credentials",
    ]);
  });
});

describe("conn-test: a success fades and a failure does not", () => {
  it("returns to idle a few seconds after a success", async () => {
    const m = mount();
    m.btn.click();
    await settle();
    expect(state(m.btn)).toBe("ok");

    await vi.advanceTimersByTimeAsync(3000);

    expect(state(m.btn)).toBe("idle");
    expect(m.btn.dataset["status"]).toBeUndefined();
  });

  it("keeps the failure state, because its reason is somewhere to go and read", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    const m = mount();
    m.btn.click();
    await settle();

    await vi.advanceTimersByTimeAsync(60_000);

    expect(state(m.btn)).toBe("err");
    expect(messages(m.banner)).toEqual(["HTTP 401"]);
  });

  it("does not fade a success that an edit has already retired", async () => {
    // The fade belongs to one verdict. Left running, it would fire after the
    // edit and paint idle over whatever the operator did next.
    const m = mount();
    m.btn.click();
    await settle();
    m.inputs["url"]!.dispatchEvent(new Event("input"));
    expect(state(m.btn)).toBe("idle");

    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    m.btn.click();
    await settle();
    expect(state(m.btn)).toBe("err");

    await vi.advanceTimersByTimeAsync(3000);

    expect(state(m.btn)).toBe("err");
  });

  it("lets a second press's verdict outlive the first press's fade", async () => {
    // The button is live in the green state, so a second press lands while the
    // first fade is still armed. Left running, that fade would paint idle over
    // the answer the operator just asked for.
    const m = mount();
    m.btn.click();
    await settle();
    await vi.advanceTimersByTimeAsync(1000);

    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    m.btn.click();
    await settle();
    expect(state(m.btn)).toBe("err");

    await vi.advanceTimersByTimeAsync(2100);

    expect(state(m.btn)).toBe("err");
  });
});

describe("conn-test: the banner carries the reason", () => {
  it("puts the server's reason in the surface's banner", async () => {
    wire.answers = [
      {
        ok: true,
        status: 200,
        data: { valid: false, error: "the credentials were rejected: HTTP 401" },
      },
    ];
    const m = mount();
    m.btn.click();
    await settle();

    expect(messages(m.banner)).toEqual(["the credentials were rejected: HTTP 401"]);
  });

  it("distinguishes a transport failure from a verdict about the service", async () => {
    // An expired session or a 500 must not read as "your key is wrong".
    wire.answers = [{ ok: false, status: 401, error: "unauthorized" }];
    const m = mount();
    m.btn.click();
    await settle();

    expect(messages(m.banner)).toEqual(["unauthorized"]);
  });

  it("falls back to its own wording when a failure carries no message", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false } }];
    const m = mount();
    m.btn.click();
    await settle();

    expect(messages(m.banner)).toEqual(["The credentials were not accepted"]);
  });

  it("leaves the banner empty on a success that had nothing to clear", async () => {
    const m = mount();
    m.btn.click();
    await settle();

    expect(messages(m.banner)).toEqual([]);
  });

  it("clears its own message when a later press succeeds", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    const m = mount();
    m.btn.click();
    await settle();
    expect(messages(m.banner)).toEqual(["HTTP 401"]);

    m.inputs["api_key"]!.dispatchEvent(new Event("input"));
    m.btn.click();
    await settle();

    expect(state(m.btn)).toBe("ok");
    expect(messages(m.banner)).toEqual([]);
  });

  it("does not clear a message somebody else wrote over its own", async () => {
    // The banner is shared with the surface's other errors, so a success that
    // cleared it unconditionally would hide a failure nobody has dealt with.
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    const m = mount();
    m.btn.click();
    await settle();

    configBanner.show("Save failed: something else");
    m.btn.click();
    await settle();

    expect(state(m.btn)).toBe("ok");
    expect(messages(m.banner)).toEqual(["Save failed: something else"]);
  });
});

describe("conn-test: a verdict does not outlive its values", () => {
  it("retires the verdict and the banner when any watched field is edited", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    const m = mount("opensubtitles", ["username", "password", "api_key"]);
    m.btn.click();
    await settle();
    expect(state(m.btn)).toBe("err");

    m.inputs["password"]!.dispatchEvent(new Event("input"));

    expect(state(m.btn)).toBe("idle");
    expect(m.btn.dataset["status"]).toBeUndefined();
    expect(messages(m.banner)).toEqual([]);
  });

  it("drops an abandoned answer instead of painting it over the current state", async () => {
    wire.defer = true;
    const m = mount();
    m.btn.click();

    // Edit mid-flight: the answer in flight is about the old value.
    m.inputs["url"]!.dispatchEvent(new Event("input"));
    wire.releases.shift()?.();
    await settle();

    expect(state(m.btn)).toBe("idle");
    expect(m.btn.disabled).toBe(false);
  });

  it("cannot be clicked into a second request while one is in flight", async () => {
    // The double-submit guard is the disabled attribute, not a queue: a
    // disabled button fires no click, so a second request is unreachable
    // rather than merely discarded.
    wire.defer = true;
    const m = mount();
    m.btn.click();
    m.btn.click();
    m.btn.click();

    expect(wire.calls).toHaveLength(1);

    wire.releases.shift()?.();
    await settle();
    expect(state(m.btn)).toBe("ok");
  });

  it("answers the newest values after an edit mid-flight", async () => {
    // The reachable re-entry: an edit aborts and re-enables, so the next click
    // is a fresh request whose answer must be the one that reports.
    wire.defer = true;
    wire.answers = [
      { ok: true, status: 200, data: { valid: false, error: "stale" } },
      { ok: true, status: 200, data: { valid: true } },
    ];
    const m = mount();
    m.btn.click();
    m.inputs["url"]!.dispatchEvent(new Event("input"));
    m.btn.click();

    for (const release of wire.releases.splice(0)) {
      release();
    }
    await settle();

    expect(wire.calls).toHaveLength(2);
    expect(state(m.btn)).toBe("ok");
    expect(messages(m.banner)).toEqual([]);
  });
});

describe("conn-test: missing fields", () => {
  it("still sends, so the server answers what is required", async () => {
    // Every host resolves its inputs by id and may find none; the control must
    // not throw, and the server owns the "URL is required" wording so the hosts
    // cannot disagree about it.
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "URL is required" } }];
    const btn = connTestControl("sonarr", {
      inputs: { url: null, api_key: null },
      banner: configBanner,
    });
    document.body.replaceChildren(configBannerHost, btn);

    btn.click();
    await settle();

    expect(wire.calls).toEqual([{ kind: "sonarr", settings: { url: "", api_key: "" } }]);
    expect(messages(configBannerHost)).toEqual(["URL is required"]);
  });
});
