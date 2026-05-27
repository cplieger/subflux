// Action registry: in-memory log of all dispatched actions with a
// subscribe API. Fires per state transition. Bounded to a recent
// window so memory usage stays flat over a long session.
//
// Surface for:
//   - Devtools / debug overlay (show recent actions)
//   - Loading-state queries (pendingCount(["chat.delete"]))
//   - Future: action telemetry, replay, time-travel debugging
//
// Performance: eviction uses a head-pointer + tombstones instead of
// splice + O(n) index re-computation. State-transition updates use a
// direct id→instance Map (no index bookkeeping). Listener notification
// iterates the Set directly (no per-call array snapshot allocation).
// Net effect: record() is O(1) amortized in steady state.
// ---------------------------------------------------------------------------

import type { ActionInstance, RegistryListener } from "./types.js";

const MAX_LOG_SIZE = 200;
const MAX_LOG_HARD = 1000;

// Module-level state. The registry is intentionally a singleton — at
// most one log per page; subscribers are tab-scoped.
//
// log:       ordered ring of recent instances (newest at end). Evicted
//            slots are null (tombstones); head tracks the first live slot.
// idMap:     id -> { instance, index } for O(1) state-transition updates
//            AND O(1) log-slot overwrites (no backward scan needed).
// pendingByName: name -> Set<id> for O(1) pendingCount(names).
const log: (ActionInstance | null)[] = [];
interface LogSlot {
  instance: ActionInstance;
  index: number;
}
const idMap = new Map<string, LogSlot>();
const listeners = new Set<RegistryListener>();
// Per-name listeners: subscribers that only care about a single action
// name. Avoids O(n) fan-out in record() for name-filtered consumers
// like bindLoadingState (typically ~40 active bindings).
const namedListeners = new Map<string, Set<RegistryListener>>();
// Per-name pending index: maps action name → Set of instance IDs that
// are currently pending. Enables O(1) isPending() and O(k) pendingCount(names)
// where k = pending count for that name (typically 0–2).
const pendingByName = new Map<string, Set<string>>();
// Incremental count of currently-pending instances. Maintained in
// record() at status transitions; pendingCount() returns this in O(1).
let _pendingTotal = 0;
// Number of live (non-null) entries in the log.
let _liveCount = 0;
// Head pointer: first potentially-live index. Entries before head are
// guaranteed null (compacted away).
let _head = 0;

/** Advance head past leading nulls and compact when prefix is large. */
function compact(): void {
  while (_head < log.length && log[_head] === null) {
    _head++;
  }
  if (_head > 256) {
    log.splice(0, _head);
    // Re-index: all stored indices shifted by _head.
    for (const entry of idMap.values()) {
      entry.index -= _head;
    }
    _head = 0;
  }
}

/** Record a state transition. Called by define.ts at every status
 *  change (pending → success/error/cancelled). The instance is
 *  push-replaced in the log (newest at end).
 *
 *  Eviction strategy (bounded memory):
 *  - Soft cap (MAX_LOG_SIZE=200): evicts the oldest NON-pending entry
 *    so long-running pending actions are never lost.
 *  - Hard cap (MAX_LOG_HARD=1000): force-evicts the oldest entry
 *    regardless of status to bound memory in runaway scenarios.
 *  - Tombstone compaction: leading nulls are spliced when the head
 *    pointer exceeds 256, keeping array iteration fast.
 *
 *  Notifies all subscribers synchronously after the state update.
 *  Listener errors are caught and logged (never propagate). */
export function record(instance: ActionInstance): void {
  // De-duplicate by id: replace any existing entry with the same id
  // (state transitions on the same instance overwrite, not append).
  const existing = idMap.get(instance.id);
  if (existing !== undefined) {
    const prev = existing.instance;
    // Update incremental counter + per-name index on transitions.
    if (prev.status === "pending" && instance.status !== "pending") {
      _pendingTotal--;
      const s = pendingByName.get(prev.name);
      if (s !== undefined) {
        s.delete(instance.id);
        if (s.size === 0) {
          pendingByName.delete(prev.name);
        }
      }
    } else if (prev.status !== "pending" && instance.status === "pending") {
      _pendingTotal++;
      let s = pendingByName.get(instance.name);
      if (s === undefined) {
        s = new Set();
        pendingByName.set(instance.name, s);
      }
      s.add(instance.id);
    }
    // O(1) overwrite via stored index — no backward scan needed.
    log[existing.index] = instance;
    existing.instance = instance;
    // Evict after pending→terminal transitions: when all entries were
    // pending at insert time, eviction couldn't find non-pending victims.
    // Now that this entry is terminal, check if we're over the cap.
    if (instance.status !== "pending" && _liveCount > MAX_LOG_SIZE) {
      for (let i = _head; i < log.length; i++) {
        const entry = log[i];
        if (
          entry !== null &&
          entry !== undefined &&
          entry.status !== "pending" &&
          entry.id !== instance.id
        ) {
          idMap.delete(entry.id);
          log[i] = null;
          _liveCount--;
          if (_liveCount <= MAX_LOG_SIZE) {
            break;
          }
        }
      }
      compact();
    }
  } else {
    const idx = log.length;
    log.push(instance);
    idMap.set(instance.id, { instance, index: idx });
    _liveCount++;
    if (instance.status === "pending") {
      _pendingTotal++;
      let s = pendingByName.get(instance.name);
      if (s === undefined) {
        s = new Set();
        pendingByName.set(instance.name, s);
      }
      s.add(instance.id);
    }
    if (_liveCount > MAX_LOG_SIZE) {
      // Evict the first NON-pending live entry so pendingCount([name]) never
      // loses track of long-running actions.
      for (let i = _head; i < log.length; i++) {
        const entry = log[i];
        if (entry !== null && entry !== undefined && entry.status !== "pending") {
          idMap.delete(entry.id);
          log[i] = null;
          _liveCount--;
          break;
        }
      }
    }
    // Hard cap: force-evict oldest live entry regardless of pending
    // status to bound memory in extreme runaway scenarios (B5/B6).
    if (_liveCount > MAX_LOG_HARD) {
      for (let i = _head; i < log.length; i++) {
        const entry = log[i];
        if (entry !== null && entry !== undefined) {
          if (entry.status === "pending") {
            _pendingTotal--;
            const s = pendingByName.get(entry.name);
            if (s !== undefined) {
              s.delete(entry.id);
              if (s.size === 0) {
                pendingByName.delete(entry.name);
              }
            }
          }
          idMap.delete(entry.id);
          log[i] = null;
          _liveCount--;
          break;
        }
      }
    }
    compact();
  }
  // Defensive: counter should never go negative; clamp to 0 if it
  // does (would indicate a record() invariant violation).
  if (_pendingTotal < 0) {
    console.warn("[actions] _pendingTotal went negative — invariant violation; clamping to 0");
    _pendingTotal = 0;
  }
  // Notify listeners. Iterate the Set directly — safe because we
  // catch per-listener errors. Set iteration semantics: entries
  // deleted during iteration are not re-visited; entries added during
  // iteration ARE visited.
  for (const fn of listeners) {
    try {
      fn(instance);
    } catch (e) {
      // Don't let a buggy subscriber bring down the registry.
      console.error("[actions] registry listener threw", e);
    }
  }
  // Notify per-name listeners (bindLoadingState).
  const named = namedListeners.get(instance.name);
  if (named !== undefined) {
    for (const fn of named) {
      try {
        fn(instance);
      } catch (e) {
        console.error("[actions] registry listener threw", e);
      }
    }
  }
}

/**
 * Subscribe to all action lifecycle events (pending/success/error/cancelled).
 *
 * @param fn - Listener invoked on each state transition. Errors thrown
 *   by the listener are caught and logged (never propagate to the dispatcher).
 * @returns An unsubscribe function.
 */
export function subscribe(fn: RegistryListener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/**
 * Subscribe to lifecycle events for a single action name. More efficient
 * than `subscribe` + manual name filtering when only one action is of
 * interest (avoids O(n) fan-out across all subscribers per record() call).
 *
 * @param name - Action name to observe (e.g. "git.commit").
 * @param fn - Listener invoked only when instance.name matches.
 * @returns An unsubscribe function.
 */
export function subscribeByName(name: string, fn: RegistryListener): () => void {
  let set = namedListeners.get(name);
  if (set === undefined) {
    set = new Set();
    namedListeners.set(name, set);
  }
  set.add(fn);
  const captured = set;
  return () => {
    captured.delete(fn);
    // Only delete the Map entry if our captured set is still the current
    // one for this name. Prevents double-unsubscribe from nuking a newer
    // Set created after the first unsubscribe emptied and removed ours.
    if (captured.size === 0 && namedListeners.get(name) === captured) {
      namedListeners.delete(name);
    }
  };
}

/** O(1) check: true if at least one instance of the named action is
 *  currently pending. Prefer over `pendingCount([name]) > 0` when
 *  you only need the boolean (avoids allocating the result array). */
export function isPending(name: string): boolean {
  const s = pendingByName.get(name);
  return s !== undefined && s.size > 0;
}

/**
 * Pending count for action(s).
 *
 * - Without arguments: total count of pending instances across all actions.
 *   Drives global progress indicators (app-bar loading bar).
 * - With a name array: sum of pending instances for those names. Useful for
 *   batch-settled detection (e.g. "all settings actions done").
 *
 * O(1) for the no-arg case, O(names.length) for the array case.
 *
 * @example
 * ```ts
 * if (pendingCount() > 0) showSpinner();
 * if (pendingCount(["settings.patch", "settings.save_steering"]) === 0) finalize();
 * ```
 */
export function pendingCount(names?: readonly string[]): number {
  if (names === undefined) {
    return _pendingTotal;
  }
  let total = 0;
  for (const name of names) {
    const s = pendingByName.get(name);
    if (s !== undefined) {
      total += s.size;
    }
  }
  return total;
}

/** Test-only: clear log + listeners. */
export function _resetForTest(): void {
  log.length = 0;
  _head = 0;
  _liveCount = 0;
  idMap.clear();
  pendingByName.clear();
  listeners.clear();
  namedListeners.clear();
  _pendingTotal = 0;
}
