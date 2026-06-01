// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { apiGet, apiGetRaw, apiPostRaw, apiDelete } from "./api-client.js";

function mockFetch(response: Partial<Response>) {
  const r = {
    ok: response.ok ?? true,
    status: response.status ?? 200,
    statusText: response.statusText ?? "OK",
    headers: new Headers(
      (response.headers as HeadersInit) ?? { "Content-Type": "application/json" },
    ),
    json: response.json ?? (() => Promise.resolve(null)),
    text: response.text ?? (() => Promise.resolve("")),
  } as Response;
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(r));
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("apiGetRaw", () => {
  const cases = [
    {
      name: "200 with JSON body",
      mock: {
        ok: true,
        status: 200,
        headers: { "Content-Type": "application/json" } as unknown as Headers,
        json: () => Promise.resolve({ name: "test" }),
      },
      expect: { ok: true, status: 200, data: { name: "test" } },
    },
    {
      name: "204 No Content",
      mock: {
        ok: true,
        status: 204,
        headers: { "Content-Type": "application/json" } as unknown as Headers,
        json: () => Promise.reject(new Error("no body")),
      },
      expect: { ok: true, status: 204, data: null },
    },
    {
      name: "400 with JSON error",
      mock: {
        ok: false,
        status: 400,
        statusText: "Bad Request",
        headers: { "Content-Type": "application/json" } as unknown as Headers,
        json: () => Promise.resolve({ error: "invalid input" }),
      },
      expect: { ok: false, status: 400, error: "invalid input" },
    },
    {
      name: "500 with non-JSON body",
      mock: {
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
        headers: { "Content-Type": "text/plain" } as unknown as Headers,
        text: () => Promise.resolve("something broke"),
      },
      expect: { ok: false, status: 500, error: "something broke" },
    },
  ] as const;

  for (const tc of cases) {
    it(tc.name, async () => {
      expect.assertions(3);
      mockFetch(tc.mock as unknown as Partial<Response>);
      const r = await apiGetRaw<{ name: string }>("/api/test");
      expect(r.ok).toBe(tc.expect.ok);
      expect(r.status).toBe(tc.expect.status);
      if ("data" in tc.expect) {
        expect(r.data).toEqual(tc.expect.data);
      } else {
        expect(r.error).toBe(tc.expect.error);
      }
    });
  }

  it("network failure", async () => {
    expect.assertions(3);
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    const r = await apiGetRaw("/api/fail");
    expect(r.ok).toBe(false);
    expect(r.status).toBe(0);
    expect(r.error).toBe("network down");
  });
});

describe("apiPostRaw", () => {
  it("sends Content-Type header when body provided", async () => {
    expect.assertions(2);
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "Content-Type": "application/json" }),
      json: () => Promise.resolve({ id: 1 }),
    } as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);
    const r = await apiPostRaw("/api/create", { name: "x" });
    expect(r.ok).toBe(true);
    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });
});

describe("apiGet", () => {
  it("returns null on non-2xx", async () => {
    expect.assertions(1);
    mockFetch({
      ok: false,
      status: 404,
      statusText: "Not Found",
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve({ error: "not found" }),
    });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const r = await apiGet("/api/missing");
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
    const r = await apiGet("/api/x", ctrl.signal);
    expect(r).toBeNull();
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });
});

describe("apiDelete", () => {
  it("returns true on success", async () => {
    expect.assertions(1);
    mockFetch({
      ok: true,
      status: 200,
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve(null),
    });
    const r = await apiDelete("/api/item/1");
    expect(r).toBe(true);
  });

  it("returns false on failure", async () => {
    expect.assertions(1);
    mockFetch({
      ok: false,
      status: 403,
      statusText: "Forbidden",
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve({ error: "forbidden" }),
    });
    const spy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const r = await apiDelete("/api/item/1");
    expect(r).toBe(false);
    spy.mockRestore();
  });
});

describe("typed error envelope", () => {
  it("exposes code and requestId on 4xx with full envelope", async () => {
    expect.assertions(4);
    mockFetch({
      ok: false,
      status: 422,
      statusText: "Unprocessable Entity",
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () =>
        Promise.resolve({
          error: "invalid input",
          code: "config_invalid",
          request_id: "req-abc-123",
        }),
    });
    const r = await apiGetRaw("/api/cfg");
    expect(r.ok).toBe(false);
    expect(r.error).toBe("invalid input");
    expect(r.code).toBe("config_invalid");
    expect(r.requestId).toBe("req-abc-123");
  });

  it("leaves code and requestId undefined on legacy error without those fields", async () => {
    expect.assertions(4);
    mockFetch({
      ok: false,
      status: 400,
      statusText: "Bad Request",
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve({ error: "something went wrong" }),
    });
    const r = await apiGetRaw("/api/old");
    expect(r.ok).toBe(false);
    expect(r.error).toBe("something went wrong");
    expect(r.code).toBeUndefined();
    expect(r.requestId).toBeUndefined();
  });
});

describe("apiGetTyped", () => {
  // The decoder runs only on 2xx with a non-null body. On decoder
  // failure, the helper logs once at console.error and returns null
  // (the same null-on-failure contract as the untyped apiGet, so
  // existing call-site error handling does not need to change).
  it("runs the decoder on 2xx and returns the typed value", async () => {
    expect.assertions(1);
    mockFetch({
      ok: true,
      status: 200,
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve({ a: 1 }),
    });
    const decoded = await import("./api-client.js").then((m) =>
      m.apiGetTyped<{ doubled: number }>("/api/x", (v) => {
        const o = v as { a: number };
        return { doubled: o.a * 2 };
      }),
    );
    expect(decoded).toEqual({ doubled: 2 });
  });

  it("returns null and logs when the decoder throws", async () => {
    expect.assertions(2);
    mockFetch({
      ok: true,
      status: 200,
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve({ wrong: "shape" }),
    });
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {
      /* noop */
    });
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {
      /* noop */
    });
    const decoded = await import("./api-client.js").then((m) =>
      m.apiGetTyped("/api/x", () => {
        throw new TypeError("nope");
      }),
    );
    expect(decoded).toBeNull();
    expect(errSpy).toHaveBeenCalled();
    errSpy.mockRestore();
    warnSpy.mockRestore();
  });

  it("apiGetTypedRaw exposes the decode error in the envelope", async () => {
    expect.assertions(2);
    mockFetch({
      ok: true,
      status: 200,
      headers: { "Content-Type": "application/json" } as unknown as Headers,
      json: () => Promise.resolve({ wrong: "shape" }),
    });
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {
      /* noop */
    });
    const r = await import("./api-client.js").then((m) =>
      m.apiGetTypedRaw("/api/x", () => {
        throw new TypeError("field foo: expected string");
      }),
    );
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/response shape mismatch:.*field foo/);
    errSpy.mockRestore();
  });
});
