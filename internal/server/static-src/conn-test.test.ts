// conn-test.test.ts — the shared connection-test control.
//
// The control is the same object in the settings dialog and the setup wizard, so
// its risks are behavioral rather than per-host: a verdict that outlives the
// values it was about (a green "Connected" beside a URL edited since), a
// transport failure rendered as a verdict about the service, and a stale answer
// from an abandoned click painting over a newer one. Those three are what these
// tests aim at; where the control is APPENDED is covered by the two hosts' own
// suites.
import { describe, it, expect, beforeEach, vi } from "vitest";
import { connTestControl } from "./conn-test.js";
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
  root: HTMLElement;
  btn: HTMLButtonElement;
  status: HTMLElement;
  url: HTMLInputElement;
  apiKey: HTMLInputElement;
}

function mount(kind = "sonarr"): Mounted {
  const url = document.createElement("input");
  const apiKey = document.createElement("input");
  const root = connTestControl(kind, { url, apiKey });
  document.body.replaceChildren(url, apiKey, root);
  return {
    root,
    btn: root.querySelector("button") as HTMLButtonElement,
    status: root.querySelector(".conn-test-status") as HTMLElement,
    url,
    apiKey,
  };
}

/** settle lets the click's awaited chain run to completion. */
function settle(): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, 0);
  });
}

beforeEach(() => {
  wire.calls = [];
  wire.answers = [];
  wire.defer = false;
  wire.releases = [];
  document.body.replaceChildren();
});

describe("arr-test: what it sends", () => {
  it("sends the section kind with the trimmed URL and the key as typed", async () => {
    const m = mount("radarr");
    m.url.value = "  http://radarr:7878  ";
    m.apiKey.value = "k1";
    m.btn.click();
    await settle();

    expect(wire.calls).toEqual([{ kind: "radarr", url: "http://radarr:7878", api_key: "k1" }]);
  });

  it("sends an empty key rather than refusing, so the server can use the stored one", async () => {
    // A saved secret renders as an empty field (the redacting GET never ships
    // the value), so a control that demanded one would fail the test on exactly
    // the configs that already work.
    const m = mount();
    m.url.value = "http://sonarr:8989";
    m.btn.click();
    await settle();

    expect(wire.calls).toEqual([{ kind: "sonarr", url: "http://sonarr:8989", api_key: "" }]);
  });
});

describe("arr-test: what it reports", () => {
  it("reports a reachable service on both the button and the status line", async () => {
    const m = mount();
    m.btn.click();
    await settle();

    expect(m.btn.dataset["status"]).toBe("ok");
    expect(m.status.dataset["status"]).toBe("ok");
    expect(m.status.textContent).toBe("Connected");
    expect(m.btn.disabled).toBe(false);
    expect(m.btn.hasAttribute("aria-busy")).toBe(false);
  });

  it("carries the server's reason for an unreachable service", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "HTTP 401" } }];
    const m = mount();
    m.btn.click();
    await settle();

    expect(m.btn.dataset["status"]).toBe("err");
    expect(m.status.textContent).toBe("HTTP 401");
  });

  it("distinguishes a transport failure from a verdict about the service", async () => {
    // An expired session or a 500 must not read as "your URL is wrong".
    wire.answers = [{ ok: false, status: 401, error: "unauthorized" }];
    const m = mount();
    m.btn.click();
    await settle();

    expect(m.btn.dataset["status"]).toBe("err");
    expect(m.status.textContent).toBe("unauthorized");
  });

  it("falls back to its own wording when a failure carries no message", async () => {
    wire.answers = [{ ok: true, status: 200, data: { valid: false } }];
    const m = mount();
    m.btn.click();
    await settle();

    expect(m.status.textContent).toBe("Not reachable");
  });

  it("shows a busy label while the request is in flight", async () => {
    wire.defer = true;
    const m = mount();
    m.btn.click();

    expect(m.btn.disabled).toBe(true);
    expect(m.btn.getAttribute("aria-busy")).toBe("true");
    expect(m.btn.textContent).toBe("Testing\u2026");

    wire.releases.shift()?.();
    await settle();
    expect(m.btn.textContent).toBe("Test connection");
  });

  it("announces politely, so a result the operator asked for does not interrupt", () => {
    expect(mount().status.getAttribute("role")).toBe("status");
  });
});

describe("arr-test: a verdict does not outlive its values", () => {
  it("retires the verdict when the URL is edited", async () => {
    const m = mount();
    m.btn.click();
    await settle();
    expect(m.btn.dataset["status"]).toBe("ok");

    m.url.value = "http://elsewhere:8989";
    m.url.dispatchEvent(new Event("input"));

    expect(m.btn.dataset["status"]).toBeUndefined();
    expect(m.status.textContent).toBe("");
  });

  it("retires the verdict when the API key is edited", async () => {
    const m = mount();
    m.btn.click();
    await settle();

    m.apiKey.dispatchEvent(new Event("input"));

    expect(m.btn.dataset["status"]).toBeUndefined();
    expect(m.status.textContent).toBe("");
  });

  it("drops an abandoned answer instead of painting it over the current state", async () => {
    wire.defer = true;
    const m = mount();
    m.btn.click();

    // Edit mid-flight: the answer in flight is about the old value.
    m.url.value = "http://elsewhere:8989";
    m.url.dispatchEvent(new Event("input"));
    wire.releases.shift()?.();
    await settle();

    expect(m.btn.dataset["status"]).toBeUndefined();
    expect(m.status.textContent).toBe("");
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
    expect(m.status.textContent).toBe("Connected");
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
    m.url.dispatchEvent(new Event("input"));
    m.btn.click();

    for (const release of wire.releases.splice(0)) {
      release();
    }
    await settle();

    expect(wire.calls).toHaveLength(2);
    expect(m.status.textContent).toBe("Connected");
    expect(m.btn.dataset["status"]).toBe("ok");
  });
});

describe("arr-test: missing fields", () => {
  it("still sends, so the server answers what is required", async () => {
    // Both hosts resolve the inputs by id and may find neither; the control
    // must not throw, and the server owns the "URL is required" wording so the
    // two hosts cannot disagree about it.
    wire.answers = [{ ok: true, status: 200, data: { valid: false, error: "URL is required" } }];
    const root = connTestControl("sonarr", { url: null, apiKey: null });
    document.body.replaceChildren(root);
    (root.querySelector("button") as HTMLButtonElement).click();
    await settle();

    expect(wire.calls).toEqual([{ kind: "sonarr", url: "", api_key: "" }]);
    expect(root.querySelector(".conn-test-status")?.textContent).toBe("URL is required");
  });
});
