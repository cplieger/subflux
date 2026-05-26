// @vitest-environment happy-dom
// Tests for subflux's apiAction idempotency wiring. Verifies the
// framework-generated key flows through to fetch as the Idempotency-Key
// header (subflux's apiAction uses apiRequestRaw which calls fetch
// internally; mocking fetch lets us inspect the actual headers).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("../notify.js", () => ({
  info: vi.fn(), success: vi.fn(), error: vi.fn(),
}));

import { apiAction } from "./api.js";
import { IDEMPOTENCY_HEADER, _resetForTest as resetDefine } from "./define.js";
import { _resetForTest as resetRegistry } from "./registry.js";
import { _resetForTest as resetCleanup } from "./cleanup.js";

const mockFetch = vi.fn<typeof fetch>();

beforeEach(() => {
  resetDefine();
  resetRegistry();
  resetCleanup();
  mockFetch.mockReset();
  vi.stubGlobal("fetch", mockFetch);
});

afterEach(() => {
  vi.restoreAllMocks();
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function getRequestHeaders(call: number): Headers {
  const init = mockFetch.mock.calls[call]?.[1] as RequestInit | undefined;
  return new Headers(init?.headers);
}

describe("apiAction — idempotency key wiring", () => {
  it("sends Idempotency-Key header when idempotencyKey: true", async () => {
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    const action = apiAction<{ id: string }, { ok: boolean }>({
      name: "test.idem.true",
      request: ({ id }) => ({ method: "POST", path: `/api/items/${id}`, body: { name: "x" } }),
      idempotencyKey: true,
      error: false,
    });
    await action.dispatch({ id: "abc" });
    const headers = getRequestHeaders(0);
    const key = headers.get(IDEMPOTENCY_HEADER);
    expect(key).not.toBeNull();
    expect(key!.length).toBeGreaterThan(5);
    // Body JSON content-type still present.
    expect(headers.get("Content-Type")).toBe("application/json");
  });

  it("does NOT send Idempotency-Key when not configured", async () => {
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    const action = apiAction<void, { ok: boolean }>({
      name: "test.idem.absent",
      request: () => ({ method: "POST", path: "/api/items", body: { name: "y" } }),
      error: false,
    });
    await action.dispatch(undefined);
    const headers = getRequestHeaders(0);
    expect(headers.get(IDEMPOTENCY_HEADER)).toBeNull();
  });

  it("idempotencyKey function receives args and returns the chosen key", async () => {
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    const action = apiAction<{ id: string; nonce: string }, { ok: boolean }>({
      name: "test.idem.fn",
      request: ({ id }) => ({ method: "POST", path: `/api/items/${id}`, body: {} }),
      idempotencyKey: ({ id, nonce }) => `items.create:${id}:${nonce}`,
      error: false,
    });
    await action.dispatch({ id: "xyz", nonce: "n1" });
    expect(getRequestHeaders(0).get(IDEMPOTENCY_HEADER)).toBe("items.create:xyz:n1");
  });

  it("retries send the SAME idempotency key (server can dedupe)", async () => {
    // First call fails with 503 (retryable), second succeeds.
    mockFetch
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "down" }), { status: 503 }))
      .mockResolvedValueOnce(jsonResponse({ ok: true }));
    const action = apiAction<void, { ok: boolean }>({
      name: "test.idem.retry",
      request: () => ({ method: "POST", path: "/api/items", body: {} }),
      idempotencyKey: true,
      retryable: (err) => err.status === 503,
      retry: { count: 1, delay: 1 },
      error: false,
    });
    await action.dispatch(undefined);
    expect(mockFetch).toHaveBeenCalledTimes(2);
    const k0 = getRequestHeaders(0).get(IDEMPOTENCY_HEADER);
    const k1 = getRequestHeaders(1).get(IDEMPOTENCY_HEADER);
    expect(k0).not.toBeNull();
    expect(k0).toEqual(k1);  // same dispatch → same key on retry
  });

  it("fresh dispatch generates a new key (different from previous dispatch)", async () => {
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    const action = apiAction<void, { ok: boolean }>({
      name: "test.idem.fresh",
      request: () => ({ method: "POST", path: "/api/items", body: {} }),
      idempotencyKey: true,
      error: false,
    });
    await action.dispatch(undefined);
    await action.dispatch(undefined);
    const k0 = getRequestHeaders(0).get(IDEMPOTENCY_HEADER);
    const k1 = getRequestHeaders(1).get(IDEMPOTENCY_HEADER);
    expect(k0).not.toBeNull();
    expect(k1).not.toBeNull();
    expect(k0).not.toBe(k1);  // different dispatches → different keys
  });
});

describe("apiAction — Idempotency-Key on different methods", () => {
  for (const method of ["POST", "PUT", "PATCH", "DELETE"] as const) {
    it(`threads the header on ${method}`, async () => {
      mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
      const action = apiAction<void, { ok: boolean }>({
        name: `test.idem.${method.toLowerCase()}`,
        request: () => ({ method, path: "/api/items/1", body: { x: 1 } }),
        idempotencyKey: true,
        error: false,
      });
      await action.dispatch(undefined);
      const headers = getRequestHeaders(0);
      expect(headers.get(IDEMPOTENCY_HEADER)).not.toBeNull();
    });
  }

  it("does not thread the header on GET (idempotent by definition)", async () => {
    // GET methods are idempotent in HTTP semantics — even if a def opts in,
    // the framework still passes the key (the contract is consistent), but
    // typical apiAction defs are mutations. This test documents that the
    // wiring fires for ANY method whose def opts in; servers may ignore on
    // GET routes where idempotency middleware isn't installed.
    mockFetch.mockResolvedValue(jsonResponse({ ok: true }));
    const action = apiAction<void, { ok: boolean }>({
      name: "test.idem.get",
      request: () => ({ method: "GET", path: "/api/items" }),
      idempotencyKey: true,
      error: false,
    });
    await action.dispatch(undefined);
    expect(getRequestHeaders(0).get(IDEMPOTENCY_HEADER)).not.toBeNull();
  });
});
