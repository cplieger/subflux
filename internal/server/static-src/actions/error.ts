// ActionError: thrown by an action's run() function to signal a typed
// failure. Carries optional HTTP status, server-side error code, and
// cause chain for diagnostics. apiAction / transportAction wrappers
// normalise their failure shapes into this.
//
// Public surface:
//   - ActionError       : structured error class
//   - hasErrorString    : narrows parsed JSON bodies with `{ error: "..." }`
//   - classifyFetchError: normalize fetch catch-block errors (used by action
//                         defs that call fetch directly)
//   - retryNetwork      : preset classifier — network/timeout/transient HTTP
//
// Internal:
//   - toActionError: coerce thrown values into ActionErrorLike (used by define.ts)
// ---------------------------------------------------------------------------

import type { ActionErrorLike } from "./types.js";

/**
 * Structured error thrown from an action's `run()` to signal a typed failure.
 * Carries optional HTTP status and server-side error code for downstream
 * classification (retry eligibility, toast formatting, telemetry).
 *
 * @example
 * ```ts
 * throw new ActionError("Server rejected", { status: 409, code: "conflict" });
 * ```
 */
export class ActionError extends Error implements ActionErrorLike {
  readonly status?: number;
  readonly code?: string;
  override readonly cause?: unknown;

  constructor(message: string, opts?: { status?: number; code?: string; cause?: unknown }) {
    super(message);
    this.name = "ActionError";
    if (opts?.status !== undefined) {
      this.status = opts.status;
    }
    if (opts?.code !== undefined) {
      this.code = opts.code;
    }
    if (opts?.cause !== undefined) {
      this.cause = opts.cause;
    }
  }
}

/** Type predicate: true when `v` is a non-null object with a string
 *  `error` property. Replaces unsafe `as { error?: string }` casts on
 *  parsed JSON bodies throughout the action framework and api-client. */
export function hasErrorString(v: unknown): v is { error: string } {
  if (typeof v !== "object" || v === null || !("error" in v)) {
    return false;
  }
  return typeof v.error === "string";
}

/** Coerce any thrown value into an ActionErrorLike snapshot. Used by
 *  the dispatcher when recording an instance to the registry.
 *
 *  Internal — consumed only by define.ts. */
export function toActionError(e: unknown): ActionErrorLike {
  if (e instanceof ActionError) {
    const r: { message: string; status?: number; code?: string; cause?: unknown } = {
      message: e.message,
    };
    if (e.status !== undefined) {
      r.status = e.status;
    }
    if (e.code !== undefined) {
      r.code = e.code;
    }
    if (e.cause !== undefined) {
      r.cause = e.cause;
    }
    return r;
  }
  if (e instanceof DOMException) {
    const code =
      e.name === "TimeoutError"
        ? "timeout"
        : e.name === "AbortError"
          ? "cancelled"
          : e.name === "NetworkError"
            ? "network"
            : e.name.toLowerCase();
    const isNetLayer = code === "network" || code === "timeout";
    return { message: e.message, code, ...(isNetLayer && { status: 0 }), cause: e };
  }
  if (e instanceof AggregateError) {
    const first = e.errors[0];
    const inner = first instanceof Error ? first.message : e.message;
    return { message: inner || e.message, code: "aggregate", cause: e };
  }
  if (e instanceof Error) {
    const rawStatus = "status" in e ? (e as { status: unknown }).status : undefined;
    const status = typeof rawStatus === "number" ? rawStatus : undefined;
    const rawCode = "code" in e ? (e as { code: unknown }).code : undefined;
    const code = typeof rawCode === "string" ? rawCode : undefined;
    const r: { message: string; status?: number; code?: string; cause: unknown } = {
      message: e.message,
      cause: e,
    };
    if (status !== undefined) {
      r.status = status;
    }
    if (code !== undefined) {
      r.code = code;
    }
    return r;
  }
  if (typeof e === "object" && e !== null && "message" in e) {
    const obj = e as { message: unknown; status?: unknown; code?: unknown };
    const message = typeof obj.message === "string" ? obj.message : String(obj.message);
    const status = typeof obj.status === "number" ? obj.status : undefined;
    const code = typeof obj.code === "string" ? obj.code : undefined;
    const r: { message: string; status?: number; code?: string; cause: unknown } = {
      message,
      cause: e,
    };
    if (status !== undefined) {
      r.status = status;
    }
    if (code !== undefined) {
      r.code = code;
    }
    return r;
  }
  if (e === null) {
    return { message: "Unknown error (null thrown)", code: "unknown" };
  }
  if (e === undefined) {
    return { message: "Unknown error (undefined thrown)", code: "unknown" };
  }
  const msg = String(e);
  return {
    message: msg !== "" ? msg : "Unknown error (empty value thrown)",
    code: "unknown",
    cause: e,
  };
}

/**
 * Classify a caught fetch error into an ActionError with a canonical code.
 * Used by transportAction / apiAction wrappers and a few action defs that
 * call fetch directly.
 *
 * Classification priority:
 *  1. Signal already aborted → "cancelled" (user or framework cancelled)
 *  2. DOMException TimeoutError / AbortError with live signal → "timeout"
 *  3. TypeError → "network" (browsers throw TypeError for network failures)
 *  4. Everything else → "network"
 */
export function classifyFetchError(e: unknown, signal: AbortSignal): ActionError {
  if (signal.aborted) {
    return new ActionError("Request cancelled", { code: "cancelled", cause: e });
  }
  if (e instanceof DOMException) {
    if (e.name === "TimeoutError" || e.name === "AbortError") {
      return new ActionError("Request timed out", { status: 0, code: "timeout", cause: e });
    }
  }
  if (e instanceof TypeError) {
    return new ActionError(e.message, { status: 0, code: "network", cause: e });
  }
  const msg = e instanceof Error ? e.message : "network error";
  return new ActionError(msg, { status: 0, code: "network", cause: e });
}

// ---------------------------------------------------------------------------
// Retry classifier preset.
//
// Action defs pass a function (or this preset) as `retryable`.
// The preset only encodes framework concepts — it does NOT know about
// app-specific permanent codes. App-aware filtering is composable:
//
//   import { retryNetwork } from "./actions/index.js";
//   const PERMANENT = new Set(["send_failed", "clipboard"]);
//   defineAction({
//     retryable: (err) => !PERMANENT.has(err.code ?? "") && retryNetwork(err),
//     ...
//   });
//
// For "retry any error except cancellation", inline the lambda directly:
//   retryable: (err) => err.code !== "cancelled"
// ---------------------------------------------------------------------------

/** HTTP statuses that represent transient server-side conditions:
 *  408 Request Timeout, 429 Too Many Requests, 502 Bad Gateway,
 *  503 Service Unavailable, 504 Gateway Timeout. */
const TRANSIENT_STATUSES = new Set([408, 429, 502, 503, 504]);

/**
 * Retry classifier preset: matches network/timeout failures and transient
 * HTTP statuses (408, 429, 502, 503, 504). Always excludes cancellation.
 *
 * Use this for the common case of "retry transient failures, surface
 * everything else immediately".
 */
export function retryNetwork(err: ActionErrorLike): boolean {
  if (err.code === "cancelled") {
    return false;
  }
  if (err.code === "network" || err.code === "timeout") {
    return true;
  }
  if (err.status === 0) {
    return true;
  }
  if (err.status !== undefined && TRANSIENT_STATUSES.has(err.status)) {
    return true;
  }
  return false;
}
