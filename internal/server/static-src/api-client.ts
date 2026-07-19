// ---------------------------------------------------------------------------
// Thin API client: subflux's transport layer over @cplieger/fetch.
//
// The request/response core — JSON body encoding, the non-throwing ApiResult
// envelope, timeout composition, credentials, and decoder validation — is
// @cplieger/fetch. This module keeps the pieces fetch deliberately leaves to
// each consumer:
//
//   - subflux's ApiResult shape: a loose interface (`ok` is a plain boolean
//     with optional data/error), NOT fetch's discriminated ApiOk|ApiErr union,
//     so existing call sites that read r.data / r.error without narrowing keep
//     compiling. It also carries `headers` on the error envelope — sourced
//     from fetch's ApiErr.headers (present whenever a real HTTP response was
//     received); subflux's login rate-limit UI reads `Retry-After` off a 429
//     (see login.ts).
//   - the null-collapsing (T|null) + OK-flag sugar and the console diagnostics
//     (silent on caller abort; one warn per failed call; one error at a decode
//     boundary).
//   - the session-expiry redirect chokepoint every call routes through.
//
// Modules do not call this transport directly: they use the CODE-GENERATED
// typed functions in wire/client.gen.ts (one per JSON endpoint, decoder-bound,
// in T|null / OK-flag / ApiResult<T> Raw flavors), which dispatch through the
// clientRequest* trio below. Declarative apiAction sites keep the actions
// framework's own transport and source only PATH_ constants (+ fillPath).
//
// Raw platform fetch() stays reserved for the documented non-JSON / streaming /
// custom-header flows (config YAML PUT, WebAuthn finish, OIDC HEAD, video
// stream) — those never routed through this client and still don't.
// ---------------------------------------------------------------------------

import { createFetch } from "@cplieger/fetch";
import type { ApiResult as FetchApiResult, HttpMethod, RequestOptions } from "@cplieger/fetch";

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

// subflux's single fetch instance. Config is frozen at construction (fetch v2
// is instances-only / immutable); credentials mirror actions-boot's
// configureApi("same-origin"), which also matches the browser default the
// former hand-rolled core relied on.
const client = createFetch({ credentials: "same-origin" });

// The shared non-throwing core. Delegates the request lifecycle to
// @cplieger/fetch and maps its envelope onto subflux's ApiResult. Error
// response headers ride fetch's ApiErr.headers — no header-capturing fetchFn
// or per-call instance needed.
async function requestRaw<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  decoder?: Decoder<T>,
): Promise<ApiResult<T>> {
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

  // Drive @cplieger/fetch's per-verb raw helpers (apiGetRaw / apiPostRaw / …)
  // rather than the generic requestRaw, so the library's verb helpers are what
  // subflux actually consumes. Each is a thin wrapper over requestRaw with the
  // method baked in (GET/DELETE thread the body through opts, POST/PUT/PATCH via
  // the body param), so the envelope mapping, decode/warn diagnostics, and
  // empty-body contract below are all unchanged. The raw (not null-collapsing)
  // family is the right fit: subflux layers its own null-collapse + loose
  // envelope on top, which the T|null helpers would hide. The switch is
  // exhaustive over HttpMethod — no default arm, no string cast.
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
  if (r.headers !== undefined) {
    result.headers = r.headers;
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
  method: HttpMethod,
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

// --- Generated-client transport contract ---
//
// wire/client.gen.ts (emitted by cmd/wire-codegen from the wirespec endpoint
// table) calls exactly these three functions. They are thin aliases over the
// same request/requestRaw core as the hand helpers below, so the envelope
// mapping, session-expiry redirect, and console diagnostics stay uniform.

/** Typed transport for generated functions with a bound decoder. */
export function clientRequest<T>(
  method: HttpMethod,
  path: string,
  body: unknown,
  decoder: Decoder<T>,
  signal?: AbortSignal,
): Promise<T | null> {
  return request<T>(method, path, body, signal, decoder);
}

/** OK-flag transport for generated functions without a decoded response. */
export async function clientRequestOK(
  method: HttpMethod,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<boolean> {
  const r = await requestRaw<unknown>(method, path, body, signal);
  if (!r.ok && !(r.status === 0 && signal?.aborted)) {
    console.warn("api:", method, path, r.status, r.error);
  }
  return r.ok;
}

/** Raw-envelope transport for generated *Raw functions. */
export function clientRequestRaw<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  decoder?: Decoder<T>,
  signal?: AbortSignal,
): Promise<ApiResult<T>> {
  return requestRaw<T>(method, path, body, signal, decoder);
}

/** Fill a generated PATH_ template's `{name}` placeholders with encoded
 *  values. For declarative apiAction sites that can't call the generated
 *  functions directly (the action framework owns the request lifecycle) but
 *  should still source their paths from the generated constants. Unknown
 *  placeholders are left verbatim. */
export function fillPath(template: string, params: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, name: string) => {
    const v = params[name];
    return v === undefined ? match : encodeURIComponent(String(v));
  });
}

// (The former hand-rolled apiGet/apiPost/… helper families were removed once
// every JSON call site moved onto the generated wire/client.gen.ts functions;
// the clientRequest* trio above is the only surface the generated code needs.
// requestRaw is fully typed; add a typed wrapper here if layered code ever
// needs extra headers again.)
