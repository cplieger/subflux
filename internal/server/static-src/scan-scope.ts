// scan-scope.ts — canonical scan scope keys + the runningScansByScope store
// shape (background scans S12).
//
// A scan's scope is the STRUCTURED identity the server stores on its activity
// entry (kind / media_type / media_id / season / episode) — never parsed from
// the human action/detail strings. The status poll derives a map of running
// scans keyed by scope from GET /api/activity and publishes it to the store;
// scan buttons carry the same key in data-scan-scope and reflect the map
// (disabled/spinner while a running scan covers their scope). This is the
// load-bearing restoration path: on reload the first poll repopulates the
// map with zero SSE events seen.

import type { ActivityEntry, Role } from "./wire/types.gen.js";

/** One running (or queued) scan derived from the activity feed. */
export interface RunningScan {
  /** Activity id — the cancel endpoint's addressing. */
  activityId: string;
  /** True while the server holds a live stop registration for it. */
  cancellable: boolean;
  /** Role required to cancel ("admin" for full scans; unset = any user). */
  requiredRole?: Role;
}

/** Store value published by the status poll. */
export type RunningScansByScope = ReadonlyMap<string, RunningScan>;

/** Build the canonical scope key from structured fields. Zero/absent values
 *  normalize to "" / 0 so client keys match across render sites and polls. */
export function scanScopeKey(
  kind: string,
  mediaType?: string,
  mediaId?: number,
  season?: number,
  episode?: number,
): string {
  return `${kind}:${mediaType ?? ""}:${mediaId ?? 0}:${season ?? 0}:${episode ?? 0}`;
}

/** Scope key of a series scan button (POST /api/scan/series/{id}). */
export function seriesScopeKey(seriesId: number): string {
  return scanScopeKey("series", undefined, seriesId);
}

/** Scope key of a season scan button (POST /api/scan/season/{id}/{season}). */
export function seasonScopeKey(seriesId: number, season: number): string {
  return scanScopeKey("season", undefined, seriesId, season);
}

/** Scope key of a movie scan button (POST /api/scan/movie/{id}). */
export function movieScopeKey(movieId: number): string {
  return scanScopeKey("movie", undefined, movieId);
}

/** Scope key of an activity entry, or null when the entry is not a scan
 *  (no structured kind field). */
export function entryScopeKey(a: ActivityEntry): string | null {
  if (!a.kind) {
    return null;
  }
  return scanScopeKey(a.kind, a.media_type, a.media_id, a.season, a.episode);
}

/** Derive the running-scans-by-scope map from an activity snapshot: every
 *  scan entry that is neither terminal (done) nor doomed (queued-cancelled)
 *  counts as running/queued for its scope. */
export function runningScans(activities: readonly ActivityEntry[]): Map<string, RunningScan> {
  const m = new Map<string, RunningScan>();
  for (const a of activities) {
    if (a.done || a.cancelled) {
      continue;
    }
    const key = entryScopeKey(a);
    if (key === null) {
      continue;
    }
    const scan: RunningScan = { activityId: a.id, cancellable: a.cancellable === true };
    if (a.required_role !== undefined) {
      scan.requiredRole = a.required_role;
    }
    m.set(key, scan);
  }
  return m;
}
