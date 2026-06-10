// ---------------------------------------------------------------------------
// Thin API client: one shared shape for every REST-style fetch call.
//
// Every handler on the server returns JSON: success payload on 2xx, or
// `{"error": "msg"}` on any 4xx/5xx. This client parses the JSON and
// returns a typed result without forcing each caller to repeat
// Content-Type headers, JSON.stringify, and r.text() fallback handling.
//
// Two shapes:
//
//   apiX<T>(path, body?) → Promise<T | null>
//     Returns parsed JSON on 2xx, null on non-2xx or network failure.
//     Use for fire-and-forget calls where you only need the happy path.
//
//   apiXRaw<T>(path, body?) → Promise<ApiResult<T>>
//     Returns {ok, status, data?, error?}. Use for flows that need the
//     status code (login 401 vs 429) or the server's error message.
//
// Pattern mirrors apps/vibekit/web/static-src/api-client.ts.
// ---------------------------------------------------------------------------

const JSON_HEADERS: Record<string, string> = { "Content-Type": "application/json" };

/** Default timeout for all API requests (30 seconds). Acts as a safety net
 *  for callers that don't pass an explicit AbortSignal. */
const DEFAULT_TIMEOUT_MS = 30_000;

import { decodeArray } from "./validators.js";

// ApiResult is the raw result from a request. One of `data` or `error`
// will be set on a 2xx/non-2xx respectively. Network failures produce
// `{ok: false, status: 0, error: "..."}`.
export interface ApiResult<T> {
  ok: boolean;
  status: number;
  data?: T;
  error?: string;
  code?: string;
  requestId?: string;
  headers?: Headers;
}

interface ErrorEnvelope {
  error?: string;
  code?: string;
  request_id?: string;
}

/** Decoder<T> is owned by validators.ts (single source of truth); re-exported
 *  here so callers can import it from api-client as well as validators. */
export type { Decoder } from "./validators.js";
import type { Decoder } from "./validators.js";

async function parseJSONSafe(r: Response): Promise<unknown> {
  if (r.status === 204) {
    return null;
  }
  const ct = r.headers.get("Content-Type") ?? "";
  if (!ct.includes("application/json")) {
    // Fallback for endpoints that return plain text (e.g. /api/config YAML).
    // Only reached on error paths since every handler routes through api.*.
    const text = await r.text();
    return text ? { error: text } : null;
  }
  try {
    return await r.json();
  } catch {
    return null;
  }
}

async function requestRaw<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  decoder?: Decoder<T>,
  extraHeaders?: Record<string, string>,
): Promise<ApiResult<T>> {
  try {
    const init: RequestInit = { method };
    const headers: Record<string, string> = {};
    if (body !== undefined) {
      Object.assign(headers, JSON_HEADERS);
      init.body = JSON.stringify(body);
    }
    if (extraHeaders !== undefined) {
      Object.assign(headers, extraHeaders);
    }
    if (Object.keys(headers).length > 0) {
      init.headers = headers;
    }
    const timeoutSignal = AbortSignal.timeout(DEFAULT_TIMEOUT_MS);
    init.signal = signal ? AbortSignal.any([signal, timeoutSignal]) : timeoutSignal;
    const r = await fetch(path, init);
    const parsed = await parseJSONSafe(r);
    if (!r.ok) {
      const envelope = (parsed as ErrorEnvelope | null) ?? {};
      const result: ApiResult<T> = {
        ok: false,
        status: r.status,
        error: envelope.error ?? r.statusText,
        headers: r.headers,
      };
      if (envelope.code) {
        result.code = envelope.code;
      }
      if (envelope.request_id) {
        result.requestId = envelope.request_id;
      }
      return result;
    }
    if (decoder && parsed !== null) {
      try {
        return { ok: true, status: r.status, data: decoder(parsed) };
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        console.error("api decode failed:", method, path, msg);
        return { ok: false, status: r.status, error: `response shape mismatch: ${msg}` };
      }
    }
    // 204 No Content responses give parsed == null; callers that expect
    // a specific response shape should use apiX variants, not the raw ones.
    return { ok: true, status: r.status, data: parsed as T };
  } catch (e) {
    return { ok: false, status: 0, error: e instanceof Error ? e.message : String(e) };
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  decoder?: Decoder<T>,
): Promise<T | null> {
  const r = await requestRaw<T>(method, path, body, signal, decoder);
  if (!r.ok) {
    if (r.status === 0 && signal?.aborted) {
      // Caller aborted; don't log.
      return null;
    }
    console.warn("api:", method, path, r.status, r.error);
    return null;
  }
  return r.data ?? null;
}

// --- Typed 2xx-or-null helpers (use these by default) ---

export function apiGet<T>(path: string, signal?: AbortSignal): Promise<T | null> {
  return request<T>("GET", path, undefined, signal);
}

export function apiPost<T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T | null> {
  return request<T>("POST", path, body, signal);
}

export function apiPut<T>(path: string, body?: unknown): Promise<T | null> {
  return request<T>("PUT", path, body);
}

export function apiPatch<T>(path: string, body?: unknown): Promise<T | null> {
  return request<T>("PATCH", path, body);
}

export async function apiDelete(path: string, body?: unknown): Promise<boolean> {
  const r = await requestRaw<unknown>("DELETE", path, body);
  if (!r.ok) {
    console.warn("api: DELETE", path, r.status, r.error);
  }
  return r.ok;
}

// --- Validating helpers (opt-in runtime shape check via decoder) ---
//
// Use these for high-value endpoints that drive UI rendering. If the
// server changes a field name without updating the frontend, the
// decoder throws at the boundary and the caller sees null (apiGetTyped)
// or an error envelope (apiGetTypedRaw) rather than a silent undefined.
// See validators.ts for the available decoders.

export function apiGetTyped<T>(
  path: string,
  decoder: Decoder<T>,
  signal?: AbortSignal,
): Promise<T | null> {
  return request<T>("GET", path, undefined, signal, decoder);
}

export function apiPostTyped<T>(
  path: string,
  body: unknown,
  decoder: Decoder<T>,
  signal?: AbortSignal,
): Promise<T | null> {
  return request<T>("POST", path, body, signal, decoder);
}

export function apiGetTypedRaw<T>(
  path: string,
  decoder: Decoder<T>,
  signal?: AbortSignal,
): Promise<ApiResult<T>> {
  return requestRaw<T>("GET", path, undefined, signal, decoder);
}

// apiGetArray<T>(path, elem) → Promise<T[] | null>
// Like apiGetTyped, but for endpoints that return a JSON array: validates
// the array and every element via the generated element decoder.
export function apiGetArray<T>(
  path: string,
  elem: Decoder<T>,
  signal?: AbortSignal,
): Promise<T[] | null> {
  return request<T[]>("GET", path, undefined, signal, (v) => decodeArray(v, elem, "$"));
}

// --- Raw helpers that expose status + error envelope ---

export function apiGetRaw<T>(path: string, signal?: AbortSignal): Promise<ApiResult<T>> {
  return requestRaw<T>("GET", path, undefined, signal);
}

export function apiPostRaw<T>(
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<ApiResult<T>> {
  return requestRaw<T>("POST", path, body, signal);
}

export function apiPutRaw<T>(path: string, body?: unknown): Promise<ApiResult<T>> {
  return requestRaw<T>("PUT", path, body);
}

export function apiPatchRaw<T>(path: string, body?: unknown): Promise<ApiResult<T>> {
  return requestRaw<T>("PATCH", path, body);
}

export function apiDeleteRaw<T>(path: string, body?: unknown): Promise<ApiResult<T>> {
  return requestRaw<T>("DELETE", path, body);
}

/** Uniform raw-request helper with optional extra headers. Used by the
 *  actions framework's apiAction adapter to thread per-dispatch headers
 *  (notably Idempotency-Key) through fetch. The per-method shorthands
 *  above stay clean for direct callers; this helper is preferred for
 *  layered code that already knows the method as a string. */
export function apiRequestRaw<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  headers?: Record<string, string>,
): Promise<ApiResult<T>> {
  return requestRaw<T>(method, path, body, signal, undefined, headers);
}
