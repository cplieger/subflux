// apiAction: factory for HTTP-backed actions. Wraps subflux's
// apiPostRaw / apiPutRaw / apiDeleteRaw / apiPatchRaw / apiGetRaw so the
// run() implementation is just the request descriptor.
//
// The 90% case for user-initiated mutations:
//
//   const deleteFile = apiAction({
//     name: "files.delete",
//     request: (path: string) => ({ method: "DELETE", path: `/api/files?path=${encodeURIComponent(path)}` }),
//     error: "Couldn't delete",
//   });
//   await deleteFile.dispatch(somePath);
//
// Failures from the underlying api-client (non-2xx, network failure)
// surface to the action lifecycle as ActionError with the server's
// error message + code + status code.
// ---------------------------------------------------------------------------

import { apiRequestRaw } from "../api-client.js";
import { defineAction, IDEMPOTENCY_HEADER } from "./define.js";
import { ActionError } from "./error.js";
import type {
  Action,
  ActionContext,
  ActionDefinition,
  RequestSpec,
} from "./types.js";

/** Caller-facing shape of an apiAction definition. Differs from the
 *  raw ActionDefinition in that `request` replaces `run`. */
interface ApiActionDefinition<TArgs, TResult, TOp = unknown>
  extends Omit<ActionDefinition<TArgs, TResult, TOp>, "run"> {
  /** HTTP request descriptor. Re-evaluated for each dispatch with the
   *  current args (so paths can interpolate args). */
  request: (args: TArgs) => RequestSpec;
}

/**
 * Build an Action from an HTTP request descriptor. The generated `run()`
 * dispatches through subflux's apiX-Raw helpers, which handle timeout,
 * JSON parsing, and the standard `{error, code}` error envelope. When
 * `idempotencyKey` is set on the def, the framework-generated key is
 * threaded as an `Idempotency-Key` header on every retry attempt so the
 * server can dedupe (subflux server endpoints opt in per-route via
 * idempotency middleware; without it the header is harmlessly ignored).
 *
 * @param def - API action definition where `request` replaces `run`.
 * @returns An {@link Action} backed by api-client with full lifecycle support.
 */
export function apiAction<TArgs, TResult = unknown, TOp = unknown>(
  def: ApiActionDefinition<TArgs, TResult, TOp>,
): Action<TArgs, TResult> {
  const { request, ...rest } = def;
  return defineAction<TArgs, TResult, TOp>({
    ...rest,
    run: async (args, signal, ctx) => {
      const spec = request(args);
      return executeRequest<TResult>(spec, signal, ctx);
    },
  });
}

/** Internal: dispatch the request via the matching api-client helper and
 *  translate non-ok ApiResult into a thrown ActionError. The dispatcher
 *  expects exceptions on failure (success path returns the parsed body).
 *  When ctx.idempotencyKey is set, the key is sent as an Idempotency-Key
 *  header — the framework generates it once per dispatch (not per retry),
 *  so a retry of the same dispatch sends the same key. */
async function executeRequest<T>(
  spec: RequestSpec,
  signal: AbortSignal,
  ctx?: ActionContext,
): Promise<T> {
  const headers: Record<string, string> | undefined =
    ctx?.idempotencyKey !== undefined
      ? { [IDEMPOTENCY_HEADER]: ctx.idempotencyKey }
      : undefined;
  const body = spec.method === "GET" ? undefined : spec.body;
  const result = await apiRequestRaw<T>(spec.method, spec.path, body, signal, headers);
  if (!result.ok) {
    // Network failures are surfaced by api-client with status: 0. Map
    // them through ActionError so retryNetwork can match (retryable
    // classifier looks at code === "network" and status === 0).
    const opts: { status: number; code?: string } = { status: result.status };
    if (result.code !== undefined) {
      opts.code = result.code;
    } else if (result.status === 0) {
      // Network/timeout failure with no server-supplied code.
      opts.code = signal.aborted ? "cancelled" : "network";
    }
    throw new ActionError(
      result.error ?? `HTTP ${String(result.status)}`,
      opts,
    );
  }
  return (result.data as T);
}
