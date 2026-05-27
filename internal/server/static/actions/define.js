// defineAction: the lifecycle runner. Takes an ActionDefinition,
// returns an Action whose dispatch() executes:
//
//   1. record instance as "pending"; run optimistic() if present
//   2. await run(args, signal)  (with auto-retry if def.retry is set)
//   3a. on success: record "success", fire success toast, return result
//   3b. on error:   record "error", call rollback() with the captured
//                   TOp + ActionError, fire error toast, return null
//   3c. on cancel:  record "cancelled", call rollback(), return null
//
// Cancellation: each dispatch creates an AbortController. Calling
// action.cancel() aborts every in-flight instance for that action.
// run() is expected to honour the signal; HTTP/transport adapters do
// this automatically.
//
// Scope serialization: when def.scope is set, dispatches with the same
// scope key serialize through a per-scope FIFO queue. The next entry
// only starts after the previous resolves (success/error/cancelled).
// Without scope, dispatches run in parallel.
//
// Auto-retry: when def.retry.count > 0 and the error is retry-class
// (matches def.retryable's classifier), the action transparently
// retries with exponential backoff before surfacing the error toast.
//
// Toast wiring: integrates with toast.ts. Defaults:
//   - success: NO toast unless `success` is set in the definition.
//   - error:   ALWAYS toast unless `error: false` is explicitly set.
//     Default error message is "<HumanName> failed: <serverMessage>".
//
// Per-dispatch hooks: opts.onSuccess / onError / onSettled fire AFTER
// the toast emission. Useful when a specific callsite needs to react
// without changing the action definition.
// ---------------------------------------------------------------------------
import { error as toastError, success as toastSuccess } from "../notify.js";
import { toActionError } from "./error.js";
import { record } from "./registry.js";
import { _registerAction } from "./cleanup.js";
let instanceCounter = 0;
/** Invoke a callback safely — errors are caught and logged without
 *  disrupting the dispatch lifecycle. Eliminates repetitive try/catch
 *  blocks throughout runOnce (8+ occurrences → 1 helper). */
function safeInvoke(actionName, hookName, fn) {
    try {
        fn();
    }
    catch (e) {
        console.error(`[actions] ${hookName} callback for ${actionName} threw`, e);
    }
}
/** Shared empty options object to avoid allocating {} on every dispatch call.
 *  All DispatchOptions fields are optional, so the empty frozen object is
 *  structurally compatible with every instantiation. The cast uses `unknown`
 *  in both slots — safe because the object has no defined properties, so
 *  the callback types (which are contravariant) are never actually read. */
const NO_OPTS = Object.freeze({});
/** Shared no-op for .catch() handlers on fire-and-forget promises.
 *  Avoids allocating a new `() => {}` closure on every scoped dispatch. */
const NOOP = () => { };
/** Generate a monotonically-increasing instance ID for registry tracking.
 *  Format: `"<actionName>#<counter>"`. Not globally unique across page
 *  reloads — only unique within a single page session. */
function nextInstanceID(name) {
    instanceCounter += 1;
    return `${name}#${String(instanceCounter)}`;
}
/** Header name used by apiAction when an idempotency key is generated.
 *  Servers can dedupe on this header; the value is a per-dispatch
 *  ULID-like string that survives across retries (so a retry of the
 *  same dispatch sends the same key). */
export const IDEMPOTENCY_HEADER = "Idempotency-Key";
/** Generate a unique idempotency key. Doesn't need true ULID
 *  ordering; just needs to be unique across dispatches and stable
 *  enough that a retry sends the same value (generated once per
 *  dispatch, not per retry). */
function generateIdempotencyKey() {
    // Format: base36 timestamp + "-" + 14-char random base36 suffix.
    // Collision-resistant for our scale (up to ~36^14 unique keys per ms).
    const ts = Date.now().toString(36);
    const rnd = Math.random().toString(36).slice(2, 16).padEnd(14, "0");
    return `${ts}-${rnd}`;
}
/** Monotonic counter for symbol identity in dedupe keys. Symbols with
 *  the same description are distinct values but String(sym) is identical,
 *  so we assign each unique symbol a stable numeric ID. */
let _symbolCounter = 0;
const _symbolMap = new Map();
function symbolId(sym) {
    let id = _symbolMap.get(sym);
    if (id === undefined) {
        id = ++_symbolCounter;
        _symbolMap.set(sym, id);
    }
    return id;
}
/** Defensive JSON.stringify — falls back to String(args) on cycles
 *  or non-serializable values (DOM elements, functions). Used by
 *  the default dedupe key computation. Primitive shortcut avoids
 *  JSON.stringify overhead for the common single-value case.
 *  Uses a custom replacer to distinguish undefined from null in
 *  arrays/objects (JSON.stringify converts both to "null"). */
function safeStringify(args) {
    if (args === undefined)
        return "undefined";
    if (args === null || typeof args === "number" || typeof args === "boolean")
        return String(args);
    if (typeof args === "string")
        return JSON.stringify(args);
    if (typeof args === "bigint")
        return `${String(args)}n`;
    if (typeof args === "symbol")
        return `@@sym${String(symbolId(args))}`;
    try {
        return JSON.stringify(args, (_key, value) => value === undefined ? "__undef__" : value) ?? "undefined";
    }
    catch {
        return String(args);
    }
}
/** Resolve a ToastSpec to its message string. Returns null when
 *  the spec is `false` (suppressed) or undefined and no fallback. */
function resolveToast(spec, args, payload, fallback) {
    if (spec === false)
        return null;
    if (spec === undefined)
        return fallback ?? null;
    if (typeof spec === "string")
        return spec;
    return spec(args, payload);
}
/** Build a default error toast prefix from the action name. Converts
 *  "chat.delete" -> "Delete failed", "mcp.add_server" -> "Add server
 *  failed", "files.create_file" -> "Create file failed". Callers
 *  usually override via the `error` field. */
function defaultErrorPrefix(name) {
    const parts = name.split(".");
    const tail = parts[parts.length - 1] ?? name;
    // Convert underscores/hyphens to spaces for readability, then
    // capitalise the first character only.
    const readable = tail.replace(/[_-]/g, " ");
    return readable.charAt(0).toUpperCase() + readable.slice(1) + " failed";
}
/** Per-scope FIFO chain. Each scope key maps to the tail of its
 *  serial-promise chain. New dispatches in that scope await the tail
 *  before starting their own work. Module-scope so all actions sharing
 *  the same scope key serialize together (e.g. two different settings
 *  actions can both use `scope: "settings"` to serialize against each
 *  other, not just within one action). */
const scopeChains = new Map();
const activeDedupes = new Map();
/** Sleep helper for retry backoff. Cancellable via signal: rejects
 *  with an AbortError if the signal aborts during the wait, so the
 *  retry chain unwinds cleanly when action.cancel() fires mid-backoff. */
function sleep(ms, signal) {
    if (signal.aborted)
        return Promise.reject(new DOMException("aborted", "AbortError"));
    if (ms <= 0)
        return Promise.resolve();
    return new Promise((resolve, reject) => {
        const t = setTimeout(() => {
            signal.removeEventListener("abort", onAbort);
            resolve();
        }, ms);
        const onAbort = () => {
            clearTimeout(t);
            reject(new DOMException("aborted", "AbortError"));
        };
        signal.addEventListener("abort", onAbort, { once: true });
    });
}
/** Wait for the browser to come back online, or for the signal to abort.
 *  Resolves immediately when navigator.onLine is already true. Used by
 *  runWithRetry when the action's networkMode is "online" (default) so
 *  retries pause during connectivity loss instead of burning through the
 *  retry budget against a dead network. */
function waitForOnline(signal) {
    if (typeof navigator === "undefined" || navigator.onLine)
        return Promise.resolve();
    if (signal.aborted)
        return Promise.reject(new DOMException("aborted", "AbortError"));
    return new Promise((resolve, reject) => {
        const onOnline = () => { cleanup(); resolve(); };
        const onAbort = () => { cleanup(); reject(new DOMException("aborted", "AbortError")); };
        function cleanup() {
            if (typeof window !== "undefined") {
                window.removeEventListener("online", onOnline);
            }
            signal.removeEventListener("abort", onAbort);
        }
        if (typeof window !== "undefined") {
            window.addEventListener("online", onOnline, { once: true });
        }
        signal.addEventListener("abort", onAbort, { once: true });
    });
}
/** Attach attempt count to a thrown error so the catch block in runOnce
 *  can record it in the registry. Uses a non-enumerable property to avoid
 *  polluting serialization. Safe against frozen/sealed objects — silently
 *  skips if the property can't be defined (the attempt count is best-effort
 *  metadata, not critical to the error flow). */
function attachAttempts(e, attempts) {
    if (typeof e === "object" && e !== null) {
        try {
            Object.defineProperty(e, "_attempts", { value: attempts, configurable: true });
        }
        catch { /* frozen/sealed object — skip */ }
    }
}
/** Read the attempt count attached by runWithRetry, or undefined. */
function readAttempts(e) {
    try {
        if (typeof e === "object" && e !== null && "_attempts" in e) {
            // After `in` narrowing, TS knows `e` has `_attempts` but types it as
            // `object & Record<"_attempts", unknown>`. Direct property access is safe.
            const val = e._attempts;
            return typeof val === "number" ? val : undefined;
        }
    }
    catch { /* Proxy or getter threw — skip */ }
    return undefined;
}
/**
 * Create an action from a declarative definition. The returned action
 * manages the full lifecycle: optimistic UI → run (with optional retry)
 * → success/error/cancel, including toast emission, registry recording,
 * scope serialization, and dedupe collapsing.
 *
 * @param def - Declarative action definition (name + run are required;
 *   all other fields opt into framework features).
 * @returns An {@link Action} whose `dispatch()` executes the lifecycle
 *   and `cancel()` aborts all in-flight instances.
 *
 * @example
 * ```ts
 * const deleteChat = defineAction<string, void>({
 *   name: "chat.delete",
 *   run: async (id, signal) => {
 *     await fetch(`/api/chats/${id}`, { method: "DELETE", signal });
 *   },
 *   error: "Couldn't delete chat",
 *   retryable: retryNetwork,
 * });
 * await deleteChat.dispatch(chatId);
 * ```
 */
export function defineAction(def) {
    // Track in-flight controllers so action.cancel() can abort them.
    const inFlight = new Map();
    // Track which instances have entered runOnce. Instances NOT in this
    // set are still scope-queued. cancel() eagerly removes scope-queued
    // instances from inFlight so dispatched-instance bookkeeping reflects
    // cancellation immediately rather than waiting for the scope chain to advance.
    const started = new Set();
    // Per-instance scope-skip resolvers. When cancel() fires for a
    // scope-queued instance, it triggers the resolver so the scope chain
    // tail resolves immediately (unblocking subsequent entries).
    const scopeSkipResolvers = new Map();
    // Per-instance prev promise for cross-action race prevention.
    const scopePrevs = new Map();
    // Per-instance early-cancel resolvers. When cancel() fires for a
    // scope-queued instance, this resolves the dispatch promise immediately
    // (returning null) so callers don't block until prev finishes.
    const scopeCancelResolvers = new Map();
    // Track active dedupe keys for this action so cancel() can eagerly
    // clear them from the module-level activeDedupes map. Without this,
    // a cancel() + immediate re-dispatch with the same dedupe key would
    // collapse onto the cancelled promise instead of starting fresh.
    const activeDedupeKeys = new Set();
    function dispatch(args, opts = NO_OPTS) {
        // Dedupe: if a dispatch with a matching dedupe key is already
        // in flight, return its promise instead of starting a new one.
        // Different from scope (which queues sequentially) — dedupe
        // collapses to a single shared dispatch.
        const dedupeKey = dedupeKeyFor(args);
        if (dedupeKey !== null) {
            const entry = activeDedupes.get(dedupeKey);
            if (entry !== undefined) {
                // Wrap so the second caller's per-call callbacks fire with the
                // ACTUAL outcome of the original dispatch (not a synthetic
                // stub). entry.error / entry.cancelled are populated by
                // runOnce when the original dispatch settles.
                const shared = entry.promise;
                if (shared === undefined) {
                    if (opts.onSettled)
                        safeInvoke(def.name, "onSettled", () => opts.onSettled(args));
                    return Promise.resolve(null);
                }
                return shared.then((v) => {
                    if (v !== null) {
                        if (opts.onSuccess)
                            safeInvoke(def.name, "onSuccess", () => opts.onSuccess(v, args));
                    }
                    else if (entry.error !== undefined) {
                        const capturedErr = entry.error;
                        if (opts.onError)
                            safeInvoke(def.name, "onError", () => opts.onError(capturedErr, args));
                    }
                    else if (entry.cancelled !== true) {
                        // Original errored without a captured error AND wasn't cancelled.
                        // Synthesize a generic dedupe error for the caller's onError.
                        if (opts.onError)
                            safeInvoke(def.name, "onError", () => opts.onError({ message: "deduped dispatch did not succeed", code: "dedupe" }, args));
                    }
                    // Cancelled originals don't fire onError on deduped callers.
                    if (opts.onSettled)
                        safeInvoke(def.name, "onSettled", () => opts.onSettled(args));
                    return v;
                }, () => {
                    // Defensive: runOnce never rejects, but guarantee onSettled fires.
                    if (opts.onSettled)
                        safeInvoke(def.name, "onSettled", () => opts.onSettled(args));
                    return null;
                });
            }
        }
        // Compute the scope key (if any) and queue behind the previous
        // entry in that scope. A scope is just a string identifier; two
        // different actions sharing the same string serialize together.
        const scopeKey = typeof def.scope === "function" ? def.scope(args)
            : typeof def.scope === "string" ? def.scope
                : null;
        // Create AbortController at dispatch time so cancel() reaches
        // scope-queued dispatches that haven't started runOnce yet.
        const ac = new AbortController();
        const id = nextInstanceID(def.name);
        inFlight.set(id, ac);
        const dispatchedAt = Date.now();
        // Create the dedupe entry up front so runOnce can populate its
        // error / cancelled fields when the original dispatch settles.
        // Deduped callers read these to fire their own callbacks with the
        // actual outcome of the original. The promise field is assigned
        // immediately below (before the entry is visible in activeDedupes
        // via the set() call after this block).
        const dedupeEntry = dedupeKey !== null ? { promise: undefined } : null;
        let result;
        if (scopeKey === null) {
            result = runOnce(args, opts, ac, id, dedupeEntry, dedupeKey, dispatchedAt);
        }
        else {
            const prev = scopeChains.get(scopeKey) ?? Promise.resolve();
            const next = prev.then(() => runOnce(args, opts, ac, id, dedupeEntry, dedupeKey, dispatchedAt));
            // The tail is what subsequent scope entries wait on. It resolves
            // when either: (a) next settles (normal path), or (b) cancel()
            // triggers the skip resolver (cancelled-while-queued path).
            let tailResolve;
            const tail = new Promise((r) => { tailResolve = r; });
            scopeSkipResolvers.set(id, tailResolve);
            scopePrevs.set(id, prev);
            void next.then(tailResolve, tailResolve);
            scopeChains.set(scopeKey, tail);
            // Cleanup: delete the scope chain entry when this is the last
            // entry. Use next.finally (not tail.then) to preserve the same
            // microtick timing as the original code.
            void next.finally(() => {
                scopeSkipResolvers.delete(id);
                scopeCancelResolvers.delete(id);
                scopePrevs.delete(id);
                if (scopeChains.get(scopeKey) === tail)
                    scopeChains.delete(scopeKey);
            }).catch(NOOP);
            // Race next against an early-cancel resolver so the dispatch
            // promise resolves immediately when cancelled while scope-queued,
            // rather than blocking until prev finishes.
            let earlyCancelResolve;
            const earlyCancel = new Promise((r) => { earlyCancelResolve = r; });
            scopeCancelResolvers.set(id, () => {
                // Fire callbacks eagerly so they complete before the dispatch
                // promise resolves. Registry recording + dedupe cleanup happen
                // here; runOnce will no-op when it eventually runs (signal aborted
                // + id removed from inFlight).
                // Wrapped in try/finally so onSettled + earlyCancelResolve always
                // fire — prevents the dispatch promise from hanging and guarantees
                // the onSettled contract even if record() throws unexpectedly.
                try {
                    const now = Date.now();
                    if (dedupeEntry !== null)
                        dedupeEntry.cancelled = true;
                    evictDedupeSlot(dedupeKey, dedupeEntry);
                    record({
                        id, name: def.name, status: "cancelled", args,
                        dispatchedAt, startedAt: now, completedAt: now,
                    });
                }
                finally {
                    if (opts.onSettled)
                        safeInvoke(def.name, "onSettled", () => opts.onSettled(args));
                    earlyCancelResolve(null);
                }
            });
            result = Promise.race([next, earlyCancel]);
        }
        // Track in dedupe map until the dispatch resolves.
        if (dedupeKey !== null && dedupeEntry !== null) {
            dedupeEntry.promise = result;
            activeDedupes.set(dedupeKey, dedupeEntry);
            activeDedupeKeys.add(dedupeKey);
            void result.finally(() => {
                // Only delete if we're still the in-flight entry (defensive
                // against another dispatch having replaced us mid-flight,
                // though that shouldn't happen since we'd have returned the
                // existing entry first).
                if (activeDedupes.get(dedupeKey) === dedupeEntry) {
                    activeDedupes.delete(dedupeKey);
                    activeDedupeKeys.delete(dedupeKey);
                }
            });
        }
        return result;
    }
    /** Compute the dedupe key for a dispatch, or null if dedupe is off.
     *  When a key is returned and matches an in-flight entry in the
     *  module-level `activeDedupes` map, the framework collapses the
     *  new dispatch onto the existing promise (no second run() call,
     *  no duplicate optimistic mutation). The key is scoped by action
     *  name so different actions with identical args don't collide. */
    function dedupeKeyFor(args) {
        const cfg = def.dedupe;
        if (cfg === undefined || cfg === false)
            return null;
        const argKey = typeof cfg === "function" ? cfg(args) : safeStringify(args);
        return `${def.name}::${argKey}`;
    }
    /** Core single-dispatch lifecycle orchestrator. Executes the full
     *  optimistic → run (with retry) → success/error/cancel pipeline for
     *  one dispatch instance. Handles:
     *  - Early exit if already cancelled while queued in a scope chain
     *  - Idempotency key generation (stable across retries)
     *  - Optimistic mutation with error-path rollback
     *  - Registry recording at each state transition
     *  - Toast emission and per-dispatch callback invocation
     *  - Populating the dedupe entry's error/cancelled fields so
     *    collapsed callers receive the actual outcome
     *
     *  Always resolves (never rejects) — errors are captured internally
     *  and surfaced via callbacks + toasts. Returns TResult on success,
     *  null on error or cancellation. */
    /** Eagerly clear the dedupe entry so dispatches from within
     *  onSuccess/onError callbacks see a clean map and start fresh
     *  rather than collapsing onto this (now-settled) promise. The async
     *  .finally() cleanup in dispatch() remains as a safety net. */
    function evictDedupeSlot(dk, entry) {
        if (dk !== null && entry !== null && activeDedupes.get(dk) === entry) {
            activeDedupes.delete(dk);
            activeDedupeKeys.delete(dk);
        }
    }
    async function runOnce(args, opts, ac, id, dedupeEntry, dedupeKey, dispatchedAt) {
        started.add(id);
        // If the instance was already settled by the early-cancel path
        // (cancel() fired while scope-queued and resolved the dispatch
        // promise eagerly), skip all work — callbacks and registry
        // recording already happened.
        if (!inFlight.has(id) && ac.signal.aborted) {
            started.delete(id);
            return null;
        }
        /** Settle this dispatch: remove from inFlight/started + fire onSettled.
         *  Called from the finally block of the main try/catch so onSettled is
         *  guaranteed to fire regardless of how the dispatch exits. */
        const settle = () => {
            inFlight.delete(id);
            started.delete(id);
            if (opts.onSettled)
                safeInvoke(def.name, "onSettled", () => opts.onSettled(args));
        };
        // If already cancelled while queued in scope chain, short-circuit.
        if (ac.signal.aborted) {
            const now = Date.now();
            // Mark the dedupe entry as cancelled so deduped callers' onError
            // doesn't fire (only onSettled does, per the contract).
            if (dedupeEntry !== null)
                dedupeEntry.cancelled = true;
            evictDedupeSlot(dedupeKey, dedupeEntry);
            record({
                id, name: def.name, status: "cancelled", args,
                dispatchedAt, startedAt: now, completedAt: now,
            });
            settle();
            return null;
        }
        const startedAt = Date.now();
        // Build the per-dispatch context. The idempotency key is generated
        // once here (not per retry) so retries of the same dispatch send
        // the same key and the server can dedupe.
        const idemKey = typeof def.idempotencyKey === "function" ? def.idempotencyKey(args)
            : def.idempotencyKey === true ? generateIdempotencyKey()
                : null;
        const ctx = idemKey !== null
            ? { instanceID: id, idempotencyKey: idemKey }
            : { instanceID: id };
        let optOp;
        if (def.optimistic !== undefined) {
            try {
                optOp = def.optimistic(args);
            }
            catch (e) {
                // Optimistic mutation threw — record + rethrow-as-error path.
                // Skip run() entirely; nothing committed yet so no rollback
                // needed (the optimistic itself failed).
                const raw = toActionError(e);
                // Enrich with a canonical code so downstream can distinguish
                // optimistic failures from run() failures (e.g. for telemetry).
                const err = raw.code !== undefined
                    ? raw
                    : { ...raw, code: "optimistic_failed" };
                if (dedupeEntry !== null)
                    dedupeEntry.error = err;
                evictDedupeSlot(dedupeKey, dedupeEntry);
                record({
                    id, name: def.name, status: "error", args,
                    dispatchedAt, startedAt, completedAt: Date.now(), error: err,
                });
                emitErrorToast(args, err);
                if (opts.onError)
                    safeInvoke(def.name, "onError", () => opts.onError(err, args));
                settle();
                return null;
            }
        }
        // Record as pending after optimistic ran successfully.
        record({
            id, name: def.name, status: "pending", args,
            dispatchedAt, startedAt,
        });
        try {
            const { result, attempts } = await runWithRetry(args, ac.signal, ctx);
            // Cancellation can race success — if the signal aborted,
            // treat as cancelled even if run() resolved. Most adapters
            // throw on abort, but be defensive.
            if (ac.signal.aborted) {
                if (dedupeEntry !== null)
                    dedupeEntry.cancelled = true;
                evictDedupeSlot(dedupeKey, dedupeEntry);
                record({
                    id, name: def.name, status: "cancelled", args,
                    dispatchedAt, startedAt, completedAt: Date.now(), attempts,
                });
                if (def.rollback !== undefined) {
                    try {
                        def.rollback(args, optOp, { message: "cancelled", code: "cancelled" });
                    }
                    catch (e) {
                        console.error(`[actions] rollback (cancellation) for ${def.name} threw`, e);
                    }
                }
                return null;
            }
            record({
                id, name: def.name, status: "success", args,
                dispatchedAt, startedAt, completedAt: Date.now(), result, attempts,
            });
            // Clear dedupe BEFORE callbacks so dispatches from onSuccess
            // with the same key start a fresh run instead of collapsing
            // onto this (now-settled) promise.
            evictDedupeSlot(dedupeKey, dedupeEntry);
            emitSuccessToast(args, result, opts);
            if (opts.onSuccess)
                safeInvoke(def.name, "onSuccess", () => opts.onSuccess(result, args));
            return result;
        }
        catch (e) {
            const err = toActionError(e);
            const attempts = readAttempts(e);
            // If aborted, classify as cancelled rather than error.
            const cancelled = ac.signal.aborted;
            const status = cancelled ? "cancelled" : "error";
            // Populate dedupe entry so deduped callers receive the actual
            // outcome (real error or cancellation flag) rather than a
            // synthetic stub.
            if (dedupeEntry !== null) {
                if (cancelled)
                    dedupeEntry.cancelled = true;
                else
                    dedupeEntry.error = err;
            }
            // Clear dedupe BEFORE callbacks so dispatches from onError
            // with the same key start a fresh run.
            evictDedupeSlot(dedupeKey, dedupeEntry);
            record({
                id, name: def.name, status, args,
                dispatchedAt, startedAt, completedAt: Date.now(),
                ...(!cancelled && { error: err }),
                ...(attempts !== undefined && { attempts }),
            });
            // Rollback the optimistic mutation regardless of cancel/error.
            if (def.rollback !== undefined) {
                try {
                    const rbError = cancelled
                        ? { message: "cancelled", code: "cancelled" }
                        : err;
                    def.rollback(args, optOp, rbError);
                }
                catch (rbCaught) {
                    console.error(`[actions] rollback for ${def.name} threw`, rbCaught);
                }
            }
            if (!cancelled) {
                emitErrorToast(args, err);
                if (opts.onError)
                    safeInvoke(def.name, "onError", () => opts.onError(err, args));
            }
            return null;
        }
        finally {
            settle();
        }
    }
    /** Run with auto-retry on retry-class errors. Each attempt re-runs
     *  def.run() with the same args + signal + ctx. Optimistic does NOT
     *  re-fire — it stays applied across retries (the rollback only
     *  fires once retries are exhausted).
     *
     *  Backoff: when `delay` is a number, `delay × factor^(attempt-1)`,
     *  capped at 5000ms. When `delay` is a function, full control.
     *
     *  When `networkMode === "online"` (the default), the retry loop
     *  awaits `navigator.onLine === true` before each backoff sleep.
     *  Idempotency key in ctx is stable across retries.
     *
     *  Returns { result, attempts } where attempts is total run() calls.
     *  On failure, attaches `_attempts` to the thrown error so the caller
     *  can record the count in the registry. */
    async function runWithRetry(args, signal, ctx) {
        const cfg = def.retry;
        const maxAttempts = (cfg?.count ?? 0) + 1;
        const baseDelay = cfg?.delay;
        const factor = cfg?.factor ?? 2;
        const networkMode = def.networkMode ?? "online";
        let attempt = 0;
        while (true) {
            if (signal.aborted) {
                const abortErr = new DOMException("aborted", "AbortError");
                attachAttempts(abortErr, attempt);
                throw abortErr;
            }
            try {
                attempt++;
                const result = await def.run(args, signal, ctx);
                return { result, attempts: attempt };
            }
            catch (e) {
                if (signal.aborted) {
                    attachAttempts(e, attempt);
                    throw e;
                }
                if (attempt >= maxAttempts) {
                    attachAttempts(e, attempt);
                    throw e;
                }
                const err = toActionError(e);
                if (!shouldRetry(err)) {
                    attachAttempts(e, attempt);
                    throw e;
                }
                // Pause until online when networkMode is "online" (default).
                // Skipped for "always" mode (retry against dead network anyway).
                if (networkMode === "online") {
                    try {
                        await waitForOnline(signal);
                    }
                    catch {
                        const abortErr = new DOMException("aborted", "AbortError");
                        attachAttempts(abortErr, attempt);
                        throw abortErr;
                    }
                }
                // Compute delay: function form gets full control; number form
                // applies exponential backoff capped at 5s.
                let delayMs;
                if (typeof baseDelay === "function") {
                    try {
                        delayMs = baseDelay(attempt, err);
                    }
                    catch {
                        delayMs = 0;
                    } // Defensive: bad delay fn → fire immediately
                }
                else if (typeof baseDelay === "number") {
                    delayMs = Math.min(baseDelay * Math.pow(factor, attempt - 1), 5000);
                }
                else {
                    delayMs = 0;
                }
                try {
                    await sleep(delayMs, signal);
                }
                catch {
                    // Sleep was aborted — throw a proper AbortError rather than
                    // re-throwing the stale run() error `e`. The caller checks
                    // signal.aborted to classify as cancelled; using the original
                    // error would leak stale state (wrong message/code/status).
                    const abortErr = new DOMException("aborted", "AbortError");
                    attachAttempts(abortErr, attempt);
                    throw abortErr;
                }
            }
        }
    }
    /** True if the error matches the action's `retryable` classifier
     *  AND so qualifies for auto-retry. Also gates the manual Retry
     *  button visibility — the same classifier drives both surfaces.
     *  Defensive: a classifier that throws is treated as "no retry" so
     *  a buggy classifier can't keep retrying forever. */
    function shouldRetry(err) {
        if (def.retryable === undefined)
            return false;
        try {
            return def.retryable(err);
        }
        catch {
            return false;
        }
    }
    function emitSuccessToast(args, result, opts) {
        if (opts.silent === true)
            return;
        try {
            const msg = resolveToast(def.success, args, result);
            if (msg !== null)
                toastSuccess(msg);
        }
        catch (e) {
            // Throwing in a success toast spec is silently dropped by design —
            // success toasts are non-critical and must never disrupt the caller.
            console.error(`[actions] emitSuccessToast for ${def.name} threw`, e);
        }
    }
    function emitErrorToast(args, err) {
        // Errors are user-facing by default; only `error: false` in the
        // definition suppresses, never the silent flag.
        // Access def.error from closure scope (not param) so all error paths share the same spec lookup.
        const spec = def.error;
        if (spec === false)
            return;
        const fallbackMsg = `${defaultErrorPrefix(def.name)}: ${err.message}`;
        // Compute retry once so both happy and fallback paths include it.
        const retry = buildRetryButton(args, err);
        try {
            let msg;
            if (typeof spec === "string") {
                msg = `${spec}: ${err.message}`;
            }
            else if (typeof spec === "function") {
                msg = spec(args, err);
            }
            else {
                msg = fallbackMsg;
            }
            toastError(msg, retry);
        }
        catch (e) {
            console.error(`[actions] emitErrorToast for ${def.name} threw`, e);
            toastError(fallbackMsg, retry);
        }
    }
    /** Build the retry button config for error toasts. Returns an
     *  `{ onClick }` object when the error is retry-eligible (per
     *  `def.retryable`), which toast.ts renders as a "Retry" button.
     *  Args are structuredClone'd so post-dispatch mutations don't
     *  corrupt the retry payload. Returns undefined (no button) when
     *  the error doesn't qualify for retry. */
    function buildRetryButton(args, err) {
        if (!shouldRetry(err))
            return undefined;
        // Snapshot args so mutations after dispatch don't corrupt retry.
        // structuredClone handles deep cloning; on failure (DOM refs,
        // functions) fall back to a shallow copy which at least isolates
        // top-level property mutations from the retry payload.
        let frozenArgs;
        try {
            frozenArgs = structuredClone(args);
        }
        catch {
            // Shallow copy only makes sense for objects/arrays. Primitives
            // (string, number, boolean, null, undefined) are immutable and
            // need no cloning — use them directly.
            if (args === null || args === undefined || typeof args !== "object") {
                frozenArgs = args;
            }
            else {
                try {
                    frozenArgs = (Array.isArray(args) ? [...args] : { ...args });
                }
                catch {
                    frozenArgs = args;
                }
            }
        }
        return {
            onClick: () => { void dispatch(frozenArgs); },
        };
    }
    function cancel() {
        if (inFlight.size === 0)
            return;
        // Eagerly clear dedupe entries so a re-dispatch with the same key
        // after cancel() starts a fresh run instead of collapsing onto the
        // (now-cancelled) promise. The async .finally() cleanup remains as
        // a safety net for the normal (non-cancel) path.
        for (const dk of activeDedupeKeys) {
            const entry = activeDedupes.get(dk);
            if (entry !== undefined)
                entry.cancelled = true;
            activeDedupes.delete(dk);
        }
        activeDedupeKeys.clear();
        // Eagerly remove scope-queued instances (not yet started) from
        // inFlight so the bookkeeping reflects cancellation immediately. Started
        // instances are removed by runOnce's settle() helper when they complete.
        for (const [id, controller] of [...inFlight.entries()]) {
            controller.abort();
            if (!started.has(id)) {
                inFlight.delete(id);
                // Trigger the scope-skip resolver so the cancelled entry's
                // tail resolves immediately, unblocking subsequent entries.
                const skip = scopeSkipResolvers.get(id);
                if (skip !== undefined) {
                    const prev = scopePrevs.get(id);
                    scopeSkipResolvers.delete(id);
                    scopePrevs.delete(id);
                    if (prev !== undefined) {
                        void prev.then(skip, skip);
                    }
                    else {
                        skip();
                    }
                }
                // Trigger the early-cancel resolver so the dispatch promise
                // resolves immediately (callers don't wait for prev to finish).
                const earlyCancel = scopeCancelResolvers.get(id);
                if (earlyCancel !== undefined) {
                    scopeCancelResolvers.delete(id);
                    earlyCancel();
                }
            }
        }
    }
    const action = {
        name: def.name,
        dispatch,
        cancel,
    };
    // Register with the global cleanup tracker so beforeunload/teardown
    // can cancel all in-flight instances of this action.
    _registerAction(action);
    return action;
}
/** Test-only: reset the instance counter for deterministic IDs.
 *  Also clears scope chains + dedupe map so a test can dispatch in
 *  a fresh state without serializing behind a previous test's chain. */
export function _resetForTest() {
    instanceCounter = 0;
    _symbolCounter = 0;
    _symbolMap.clear();
    scopeChains.clear();
    activeDedupes.clear();
}
/** Test-only: expose internal map sizes for leak verification. */
export function _internalsForTest() {
    return { scopeChains: scopeChains.size, activeDedupes: activeDedupes.size };
}
