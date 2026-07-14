// ---------------------------------------------------------------------------
// Thin API client: subflux's REST-style surface layered over @cplieger/fetch.
//
// The request/response core — JSON body encoding, the non-throwing ApiResult
// envelope, timeout composition, credentials, and decoder validation — is now
// @cplieger/fetch. This module keeps the pieces fetch deliberately leaves to
// each consumer:
//
//   - subflux's ApiResult shape: a loose interface (`ok` is a plain boolean
//     with optional data/error), NOT fetch's discriminated ApiOk|ApiErr union,
//     so existing call sites that read r.data / r.error without narrowing keep
//     compiling. It also carries `headers` on the error envelope — fetch omits
//     response headers by design (they're on its "unsupported by design"
//     list), but subflux's login rate-limit UI reads `Retry-After` off a 429
//     (see login.ts), so we capture the response headers via an injected
//     fetchFn and attach them here.
//   - the null-collapsing (`apiGet` → T|null) + boolean (`apiDelete`) sugar and
//     the console diagnostics (silent on caller abort; one warn per failed
//     call; one error at a decode boundary).
//   - the exact helper signatures every module already imports.
//
// Two shapes, as before:
//
//   apiX<T>(path, body?)     → Promise<T | null>       (2xx body or null)
//   apiXRaw<T>(path, body?)  → Promise<ApiResult<T>>   (full envelope: status,
//                                                        error, code, headers)
//
// Raw platform fetch() stays reserved for the documented non-JSON / streaming /
// custom-header flows (config YAML PUT, WebAuthn finish, OIDC HEAD, video
// stream) — those never routed through this client and still don't.
// ---------------------------------------------------------------------------

import { createFetch } from "@cplieger/fetch";
import type { ApiResult as FetchApiResult, HttpMethod, RequestOptions } from "@cplieger/fetch";
import { decodeArray } from "./validators.js";

/** Decoder<T> is owned by validators.ts (single source of truth); re-exported
 *  here so callers can import it from api-client as well as validators. */
export type { Decoder } from "./validators.js";
import type { Decoder } from "./validators.js";

// ApiResult keeps subflux's historical envelope shape rather than adopting
// fetch's discriminated union: `ok` is a plain boolean with optional
// data/error (so `r.data` / `r.error` reads at existing call sites need no
// narrowing), plus the lifted `code` / `requestId` fields and `headers`
// (populated on the error path only — see the module header).
export interface ApiResult<T> {
  ok: boolean;
  status: number;
  data?: T;
  error?: string;
  code?: string;
  requestId?: string;
  headers?: Headers;
}

// The shared non-throwing core. Delegates the request lifecycle to
// @cplieger/fetch and maps its envelope onto subflux's ApiResult.
//
// A fresh fetch instance is built per call so the header-capturing fetchFn
// writes into a per-call closure variable — there is no shared slot for
// concurrent requests to clobber. createFetch is cheap (a few closures); the
// per-request cost is negligible for a UI. credentials mirror actions-boot's
// configureApi("same-origin"), which also matches the browser default the
// former hand-rolled core relied on.
async function requestRaw<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  decoder?: Decoder<T>,
  extraHeaders?: Record<string, string>,
): Promise<ApiResult<T>> {
  let responseHeaders: Headers | undefined;
  const client = createFetch({
    credentials: "same-origin",
    fetchFn: (input, init) =>
      fetch(input, init).then((res) => {
        responseHeaders = res.headers;
        return res;
      }),
  });

  const opts: RequestOptions<T> = {};
  if (body !== undefined) {
    opts.body = body;
  }
  if (signal !== undefined) {
    opts.signal = signal;
  }
  if (decoder !== undefined) {
    opts.decoder = decoder;
  }
  if (extraHeaders !== undefined) {
    opts.headers = extraHeaders;
  }

  // Drive @cplieger/fetch's per-verb raw helpers (apiGetRaw / apiPostRaw / …)
  // rather than the generic requestRaw, so the library's verb helpers are what
  // subflux actually consumes. Each is a thin wrapper over requestRaw with the
  // method baked in (GET/DELETE thread the body through opts, POST/PUT/PATCH via
  // the body param), so the envelope mapping, header capture, decode/warn
  // diagnostics, and empty-body contract below are all unchanged. The raw
  // (not null-collapsing) family is the right fit: subflux layers its own
  // null-collapse + loose envelope + response-header capture on top, which the
  // T|null helpers would hide.
  let r: FetchApiResult<T>;
  switch (method) {
    case "GET":
      r = await client.apiGetRaw<T>(path, opts);
      break;
    case "POST":
      r = await client.apiPostRaw<T>(path, body, opts);
      break;
    case "PUT":
      r = await client.apiPutRaw<T>(path, body, opts);
      break;
    case "PATCH":
      r = await client.apiPatchRaw<T>(path, body, opts);
      break;
    case "DELETE":
      r = await client.apiDeleteRaw<T>(path, opts);
      break;
    default:
      r = await client.requestRaw<T>(method as HttpMethod, path, opts);
      break;
  }

  if (r.ok) {
    // Preserve subflux's empty-body contract: a 204 / empty 2xx collapses to
    // `data: null` (fetch yields `undefined`); a real JSON null/0/false/""
    // body is genuine data and passes through unchanged.
    return { ok: true, status: r.status, data: (r.data ?? null) as T };
  }

  // fetch classifies a JSON-parse / decoder failure as code "decode". Keep
  // subflux's single console.error diagnostic at that boundary; the null
  // collapse + warn (below, in request) still applies for the plain helpers.
  if (r.code === "decode") {
    console.error("api decode failed:", method, path, r.error);
  }
  // Raw and null-collapsing helpers alike route through here, so the
  // session-expiry redirect covers every surface.
  handleSessionExpiry(r.status);
  const result: ApiResult<T> = { ok: false, status: r.status, error: r.error };
  if (r.code !== undefined) {
    result.code = r.code;
  }
  if (r.requestId !== undefined) {
    result.requestId = r.requestId;
  }
  if (responseHeaders !== undefined) {
    result.headers = responseHeaders;
  }
  return result;
}

// Session expiry chokepoint: every API call routes through requestRaw, so a
// 401 anywhere in the MAIN app (dialogs included) means the session is gone
// and no surface has a local recovery path — redirect to the login page with
// a return target instead of leaving a blank dialog or a silently-failed
// action. The login shell (/login) is excluded: its own POSTs legitimately
// answer 401 on bad credentials and must not loop.
let redirectingToLogin = false;

function handleSessionExpiry(status: number): void {
  if (status !== 401 || redirectingToLogin) {
    return;
  }
  const { pathname, search } = window.location;
  if (pathname.startsWith("/login")) {
    return;
  }
  redirectingToLogin = true;
  window.location.href = `/login?next=${encodeURIComponent(pathname + search)}`;
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

/** Uniform raw-request helper with optional extra headers. Used by layered
 *  code that already knows the method as a string and needs to thread
 *  per-request headers (e.g. an Idempotency-Key) through the client. The
 *  per-method shorthands above stay clean for direct callers. */
export function apiRequestRaw<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  headers?: Record<string, string>,
): Promise<ApiResult<T>> {
  return requestRaw<T>(method, path, body, signal, undefined, headers);
}
