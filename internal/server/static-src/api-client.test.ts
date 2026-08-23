import { describe, it, expect, vi, beforeEach, onTestFinished } from "vitest";
import type * as ApiClientModule from "./api-client.js";
import { clientRequest, clientRequestOK, clientRequestRaw, fillPath } from "./api-client.js";

// The client's request core is @cplieger/fetch, which reads a response body
// via res.text() + JSON.parse (not res.json()) and builds request headers as a
// Headers instance. Stub the global fetch with real Response objects so the
// mock matches exactly what the core consumes.
//
// The clientRequest / clientRequestOK / clientRequestRaw trio is the transport
// contract the CODE-GENERATED wire/client.gen.ts functions dispatch through;
// these tests pin the envelope mapping, null-collapse, decode diagnostics, and
// console behavior at that boundary.
function stubFetch(body: BodyInit | null, init?: ResponseInit): void {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, init)));
}

const JSON_HEADERS = { "Content-Type": "application/json" };

/** Identity decoder for tests that don't validate a shape. */
const passthrough = <T>(v: unknown): T => v as T;

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("clientRequestRaw", () => {
  it("200 with JSON body", async () => {
    expect.assertions(3);
    stubFetch(JSON.stringify({ name: "test" }), { status: 200, headers: JSON_HEADERS });
    const r = await clientRequestRaw<{ name: string }>("GET", "/api/test");
    expect(r.ok).toBe(true);
    expect(r.status).toBe(200);
    expect(r.data).toEqual({ name: "test" });
  });

  it("204 No Content", async () => {
    expect.assertions(3);
    stubFetch(null, { status: 204 });
    const r = await clientRequestRaw<{ name: string }>("GET", "/api/test");
    expect(r.ok).toBe(true);
    expect(r.status).toBe(204);
    // Empty-body 2xx collapses to null (subflux's contract), even on the raw
    // flavor — @cplieger/fetch yields undefined, the client maps it to null.
    expect(r.data).toEqual(null);
  });

  it("400 with JSON error", async () => {
    expect.assertions(3);
    stubFetch(JSON.stringify({ error: "invalid input" }), { status: 400, headers: JSON_HEADERS });
    const r = await clientRequestRaw<{ name: string }>("GET", "/api/test");
    expect(r.ok).toBe(false);
    expect(r.status).toBe(400);
    expect(r.error).toBe("invalid input");
  });

  it("500 with non-JSON body falls back to the HTTP status", async () => {
    expect.assertions(3);
    stubFetch("something broke", { status: 500, headers: { "Content-Type": "text/plain" } });
    const r = await clientRequestRaw<{ name: string }>("GET", "/api/test");
    expect(r.ok).toBe(false);
    expect(r.status).toBe(500);
    // @cplieger/fetch does not surface a non-JSON error body; it reports
    // `HTTP <status>`. subflux endpoints always return `{"error": ...}` JSON,
    // so this path only occurs for an infrastructure/proxy response.
    expect(r.error).toBe("HTTP 500");
  });

  it("network failure", async () => {
    expect.assertions(3);
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    const r = await clientRequestRaw("GET", "/api/fail");
    expect(r.ok).toBe(false);
    expect(r.status).toBe(0);
    expect(r.error).toBe("network down");
  });

  it("sends Content-Type header when body provided", async () => {
    expect.assertions(2);
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ id: 1 }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const r = await clientRequestRaw("POST", "/api/create", { name: "x" });
    expect(r.ok).toBe(true);
    // @cplieger/fetch builds request headers as a Headers instance.
    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    expect((init.headers as Headers).get("Content-Type")).toBe("application/json");
  });
});

describe("clientRequest", () => {
  it("returns null on non-2xx", async () => {
    expect.assertions(1);
    stubFetch(JSON.stringify({ error: "not found" }), { status: 404, headers: JSON_HEADERS });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const r = await clientRequest("GET", "/api/missing", undefined, passthrough);
    expect(r).toBeNull();
    spy.mockRestore();
  });

  it("returns null on abort without console.warn", async () => {
    expect.assertions(2);
    const ctrl = new AbortController();
    ctrl.abort();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new DOMException("aborted", "AbortError")));
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const r = await clientRequest("GET", "/api/x", undefined, passthrough, ctrl.signal);
    expect(r).toBeNull();
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  // The decoder runs only on a 2xx body. On decoder failure, the transport
  // logs once at console.error and returns null (the same null-on-failure
  // contract as an HTTP error, so call-site handling is uniform).
  it("runs the decoder on 2xx and returns the typed value", async () => {
    expect.assertions(1);
    stubFetch(JSON.stringify({ a: 1 }), { status: 200, headers: JSON_HEADERS });
    const decoded = await clientRequest<{ doubled: number }>("GET", "/api/x", undefined, (v) => {
      const o = v as { a: number };
      return { doubled: o.a * 2 };
    });
    expect(decoded).toEqual({ doubled: 2 });
  });

  it("returns null and logs when the decoder throws", async () => {
    expect.assertions(2);
    stubFetch(JSON.stringify({ wrong: "shape" }), { status: 200, headers: JSON_HEADERS });
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {
      /* noop */
    });
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const decoded = await clientRequest("GET", "/api/x", undefined, () => {
      throw new TypeError("nope");
    });
    expect(decoded).toBeNull();
    expect(errSpy).toHaveBeenCalled();
    errSpy.mockRestore();
    warnSpy.mockRestore();
  });
});

describe("clientRequestOK", () => {
  it("returns true on success", async () => {
    expect.assertions(1);
    stubFetch(JSON.stringify({}), { status: 200, headers: JSON_HEADERS });
    const r = await clientRequestOK("DELETE", "/api/item/1");
    expect(r).toBe(true);
  });

  it("returns false on failure", async () => {
    expect.assertions(1);
    stubFetch(JSON.stringify({ error: "forbidden" }), { status: 403, headers: JSON_HEADERS });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const r = await clientRequestOK("DELETE", "/api/item/1");
    expect(r).toBe(false);
    spy.mockRestore();
  });
});

describe("typed error envelope", () => {
  it("exposes code and requestId on 4xx with full envelope", async () => {
    expect.assertions(4);
    stubFetch(
      JSON.stringify({
        error: "invalid input",
        code: "config_invalid",
        request_id: "req-abc-123",
      }),
      { status: 422, headers: JSON_HEADERS },
    );
    const r = await clientRequestRaw("GET", "/api/cfg");
    expect(r.ok).toBe(false);
    expect(r.error).toBe("invalid input");
    expect(r.code).toBe("config_invalid");
    expect(r.requestId).toBe("req-abc-123");
  });

  it("leaves code and requestId undefined on legacy error without those fields", async () => {
    expect.assertions(4);
    stubFetch(JSON.stringify({ error: "something went wrong" }), {
      status: 400,
      headers: JSON_HEADERS,
    });
    const r = await clientRequestRaw("GET", "/api/old");
    expect(r.ok).toBe(false);
    expect(r.error).toBe("something went wrong");
    expect(r.code).toBeUndefined();
    expect(r.requestId).toBeUndefined();
  });

  it("clientRequestRaw exposes the decode error in the envelope", async () => {
    expect.assertions(2);
    stubFetch(JSON.stringify({ wrong: "shape" }), { status: 200, headers: JSON_HEADERS });
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {
      /* noop */
    });
    const r = await clientRequestRaw("GET", "/api/x", undefined, () => {
      throw new TypeError("field foo: expected string");
    });
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/response shape mismatch:.*field foo/);
    errSpy.mockRestore();
  });
});

describe("fillPath", () => {
  it("fills and encodes each placeholder", () => {
    expect.assertions(2);
    expect(fillPath("/api/scan/season/{id}/{season}", { id: 12, season: 3 })).toBe(
      "/api/scan/season/12/3",
    );
    expect(fillPath("/api/auth/passkeys/{id}", { id: "a/b" })).toBe("/api/auth/passkeys/a%2Fb");
  });

  it("leaves unknown placeholders verbatim", () => {
    expect.assertions(1);
    expect(fillPath("/api/scan/series/{id}", {})).toBe("/api/scan/series/{id}");
  });
});

// --- Method dispatch, option threading, and diagnostics ---
//
// The switch over HttpMethod is the whole transport: an emptied case falls
// through to the next verb, so the method that leaves the client is the only
// thing that proves the dispatch. A fresh Response per call because a Response
// body can only be read once.
function captureFetch(body: BodyInit | null = "{}", init: ResponseInit = { status: 200 }) {
  const mock = vi.fn((_url: string, _init?: RequestInit) =>
    Promise.resolve(new Response(body, init)),
  );
  vi.stubGlobal("fetch", mock);
  return mock;
}

function sentInit(mock: ReturnType<typeof captureFetch>, call: number): RequestInit {
  const args = mock.mock.calls[call];
  if (!args?.[1]) {
    throw new Error(`fetch call ${String(call)} carried no init`);
  }
  return args[1];
}

describe("verb dispatch", () => {
  it("sends each verb as its own HTTP method", async () => {
    const mock = captureFetch();

    await clientRequestRaw("GET", "/api/x");
    await clientRequestRaw("POST", "/api/x", { a: 1 });
    await clientRequestRaw("PUT", "/api/x", { a: 1 });
    await clientRequestRaw("PATCH", "/api/x", { a: 1 });
    await clientRequestRaw("DELETE", "/api/x");

    expect(sentInit(mock, 0).method).toBe("GET");
    expect(sentInit(mock, 1).method).toBe("POST");
    expect(sentInit(mock, 2).method).toBe("PUT");
    expect(sentInit(mock, 3).method).toBe("PATCH");
    expect(sentInit(mock, 4).method).toBe("DELETE");
  });

  it("threads a DELETE body through the request options", async () => {
    // DELETE takes its body via opts (only POST/PUT/PATCH get a body param), so
    // this is the one verb where the opts.body assignment is observable.
    const mock = captureFetch();

    await clientRequestRaw("DELETE", "/api/files", { media_id: "tvdb-1" });

    expect(sentInit(mock, 0).body).toBe(JSON.stringify({ media_id: "tvdb-1" }));
  });

  it("sends no body when the caller passes none", async () => {
    const mock = captureFetch();

    await clientRequestRaw("DELETE", "/api/files");

    expect(sentInit(mock, 0).body).toBeUndefined();
  });

  it("threads the caller's abort signal into the request", async () => {
    const mock = captureFetch();
    const ctrl = new AbortController();

    await clientRequestRaw("GET", "/api/x", undefined, undefined, ctrl.signal);
    const sent = sentInit(mock, 0).signal;
    ctrl.abort();

    // The client composes the caller's signal with its timeout, so the signal
    // that reaches fetch is a different object that must still follow ours.
    expect(sent?.aborted).toBe(true);
  });
});

describe("error envelope diagnostics", () => {
  it("carries the response headers on the error envelope", async () => {
    // login.ts reads Retry-After off a 429 to time its rate-limit countdown.
    stubFetch(JSON.stringify({ error: "too many attempts" }), {
      status: 429,
      headers: { ...JSON_HEADERS, "Retry-After": "30" },
    });

    const r = await clientRequestRaw("POST", "/api/auth/login", { user: "x" });

    expect(r.headers?.get("Retry-After")).toBe("30");
  });

  it("keeps the decode diagnostic off a plain HTTP error", async () => {
    // console.error is the decode boundary's own signal; a 500 is not a decode
    // failure and must not raise it.
    stubFetch(JSON.stringify({ error: "boom" }), { status: 500, headers: JSON_HEADERS });
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {
      /* noop */
    });
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequestRaw("GET", "/api/x");

    expect(errSpy).not.toHaveBeenCalled();
    errSpy.mockRestore();
    warnSpy.mockRestore();
  });

  it("logs one warning per failed call", async () => {
    stubFetch(JSON.stringify({ error: "not found" }), { status: 404, headers: JSON_HEADERS });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequest("GET", "/api/missing", undefined, passthrough);

    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });

  it("logs no warning for a successful call", async () => {
    stubFetch(JSON.stringify({ a: 1 }), { status: 200, headers: JSON_HEADERS });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequest("GET", "/api/x", undefined, passthrough);

    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it("still warns on an HTTP error when the caller's signal aborted afterwards", async () => {
    // Only a status-0 (transport-level) cancellation is silent; a real HTTP
    // failure keeps its diagnostic even if the caller has since given up.
    stubFetch(JSON.stringify({ error: "gone" }), { status: 410, headers: JSON_HEADERS });
    const ctrl = new AbortController();
    ctrl.abort();
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequest("GET", "/api/x", undefined, passthrough, ctrl.signal);

    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });

  it("clientRequestOK warns on failure", async () => {
    stubFetch(JSON.stringify({ error: "forbidden" }), { status: 403, headers: JSON_HEADERS });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequestOK("DELETE", "/api/item/1");

    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });

  it("clientRequestOK stays quiet on success", async () => {
    stubFetch(JSON.stringify({}), { status: 200, headers: JSON_HEADERS });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequestOK("DELETE", "/api/item/1");

    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it("clientRequestOK stays quiet on a caller-aborted call", async () => {
    const ctrl = new AbortController();
    ctrl.abort();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new DOMException("aborted", "AbortError")));
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    const ok = await clientRequestOK("DELETE", "/api/item/1", undefined, ctrl.signal);

    expect(ok).toBe(false);
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it("clientRequestOK still warns on an HTTP error the caller later abandoned", async () => {
    // Same rule as clientRequest: silence is for a status-0 cancellation only,
    // so BOTH halves of the guard have to hold, not either one.
    stubFetch(JSON.stringify({ error: "gone" }), { status: 410, headers: JSON_HEADERS });
    const ctrl = new AbortController();
    ctrl.abort();
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });

    await clientRequestOK("DELETE", "/api/item/1", undefined, ctrl.signal);

    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });
});

// --- Session-expiry redirect ---
//
// `redirectingToLogin` is a module-level one-shot latch, so each test imports a
// FRESH module; a shared module would let the first redirect disarm the rest.
//
// The `?boot=` query is what makes "fresh" true, and it is not decoration.
// Browser Mode resolves a dynamic import through the browser's own module map,
// which is keyed by URL and holds evaluated modules for the life of the page:
// vi.resetModules() clears the runner's registry but cannot evict an entry from
// that map, so a bare import("./api-client.js") returns the instance whose latch
// an earlier test already tripped. A distinct query is a distinct URL and
// therefore a fresh evaluation. `@vite-ignore` opts out of Vite's
// variable-dynamic-import rewrite.
//
// The `.ts` extension is load-bearing: this specifier is built at runtime, so the
// URL the browser requests is the one written here, and that URL is what v8
// coverage attributes the evaluation to. Written `./api-client.js` it names a file
// that does not exist and api-client.ts reports 0% coverage while this suite stays
// green.
let bootCount = 0;
async function freshClient(): Promise<typeof ApiClientModule> {
  vi.resetModules();
  return (await import(
    /* @vite-ignore */ `./api-client.ts?boot=${++bootCount}`
  )) as typeof ApiClientModule;
}

/** Put the real `window.location` on the view under test and record every
 *  navigation the client attempts, without letting one carry the test page away.
 *
 *  `window.location` is `[LegacyUnforgeable]` in a real browser: `location`,
 *  `location.href`, `location.assign` and `window` ITSELF are all
 *  non-configurable, so `vi.stubGlobal("location", ...)` throws "Cannot redefine
 *  property: location" -- measured, along with the same error for `window`,
 *  `href` and `assign`. The DOM emulator this suite used to run under allowed the
 *  redefine; a browser does not, and there is no `Location.prototype` member to
 *  spy on either.
 *
 *  So neither half is faked. The view api-client.ts reads for its `next` target
 *  is set for REAL with history.replaceState, and the redirect is observed
 *  through the platform's own Navigation API: a scripted `location.href`
 *  assignment fires a cancelable `navigate` event carrying the resolved
 *  destination, and preventDefault is what stops it from navigating the runner
 *  away. That makes these assertions strictly stronger than the stub's -- they
 *  now pin that the browser actually began the navigation production asked for,
 *  rather than that a property was written on a plain object. */
function watchRedirects(pathname: string, search = ""): { targets: string[] } {
  const restore = location.pathname + location.search + location.hash;
  history.replaceState(null, "", pathname + search);
  const targets: string[] = [];
  const onNavigate = (ev: NavigateEvent): void => {
    const url = new URL(ev.destination.url);
    targets.push(url.pathname + url.search);
    if (ev.cancelable) {
      ev.preventDefault();
    }
  };
  navigation.addEventListener("navigate", onNavigate);
  onTestFinished(() => {
    // Remove BEFORE restoring: replaceState fires navigate too, and a restore
    // recorded as a target would corrupt the next test's expectations.
    navigation.removeEventListener("navigate", onNavigate);
    history.replaceState(null, "", restore);
  });
  return { targets };
}

describe("session expiry", () => {
  it("redirects a 401 to the login page with the current view as the return target", async () => {
    const { clientRequestRaw: freshRaw } = await freshClient();
    const nav = watchRedirects("/settings", "?tab=auth");
    stubFetch(JSON.stringify({ error: "unauthorized" }), { status: 401, headers: JSON_HEADERS });

    await freshRaw("GET", "/api/config");

    expect(nav.targets).toEqual(["/login?next=%2Fsettings%3Ftab%3Dauth"]);
  });

  it("leaves a non-401 failure on the current page", async () => {
    const { clientRequestRaw: freshRaw } = await freshClient();
    const nav = watchRedirects("/settings");
    stubFetch(JSON.stringify({ error: "forbidden" }), { status: 403, headers: JSON_HEADERS });

    await freshRaw("GET", "/api/config");

    expect(nav.targets).toEqual([]);
  });

  it("does not redirect the login shell, whose own POSTs answer 401", async () => {
    const { clientRequestRaw: freshRaw } = await freshClient();
    const nav = watchRedirects("/login");
    stubFetch(JSON.stringify({ error: "bad credentials" }), {
      status: 401,
      headers: JSON_HEADERS,
    });

    await freshRaw("POST", "/api/auth/login", { user: "x" });

    expect(nav.targets).toEqual([]);
  });

  it("does not redirect anything under the login shell either", async () => {
    // The guard is a PREFIX test on purpose: a future sub-route of the login
    // shell must not be able to bounce itself back to /login forever.
    const { clientRequestRaw: freshRaw } = await freshClient();
    const nav = watchRedirects("/login/reset");
    stubFetch(JSON.stringify({ error: "unauthorized" }), { status: 401, headers: JSON_HEADERS });

    await freshRaw("POST", "/api/auth/login", { user: "x" });

    expect(nav.targets).toEqual([]);
  });

  it("redirects only once when several calls fail together", async () => {
    const { clientRequestRaw: freshRaw } = await freshClient();
    const nav = watchRedirects("/settings");
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify({ error: "unauthorized" }), {
            status: 401,
            headers: JSON_HEADERS,
          }),
        ),
      ),
    );

    await freshRaw("GET", "/api/config");
    await freshRaw("GET", "/api/status");

    // The latch holds: the second 401 must not restart the navigation, so the
    // recorded list is exactly one entry long.
    expect(nav.targets).toEqual(["/login?next=%2Fsettings"]);
  });
});
