// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
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
