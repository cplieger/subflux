// scan-scope.test.ts — scope key derivation + runningScansByScope
// restoration model (background scans S12) + ScanAccepted decode.

import { describe, it, expect } from "vitest";
import {
  scanScopeKey,
  seriesScopeKey,
  seasonScopeKey,
  movieScopeKey,
  entryScopeKey,
  runningScans,
} from "./scan-scope.js";
import { decodeScanAccepted } from "./wire/decoders.gen.js";
import type { ActivityEntry } from "./wire/types.gen.js";

function entry(partial: Partial<ActivityEntry>): ActivityEntry {
  return {
    started_at: "2026-07-19T10:00:00Z",
    id: "1",
    action: "Series Search",
    detail: "d",
    source: "manual",
    done: false,
    ...partial,
  };
}

describe("scan-scope: keys", () => {
  it("normalizes absent fields so render-site and entry-derived keys match", () => {
    expect(seriesScopeKey(42)).toBe(scanScopeKey("series", undefined, 42, undefined, undefined));
    expect(seasonScopeKey(42, 0)).toBe(scanScopeKey("season", undefined, 42, 0));
    expect(movieScopeKey(7)).toBe(scanScopeKey("movie", undefined, 7));
  });

  it("derives an entry's key from its structured fields", () => {
    const e = entry({ kind: "season", media_id: 42, season: 3 });
    expect(entryScopeKey(e)).toBe(seasonScopeKey(42, 3));
  });

  it("season 0 (specials) keys distinctly from the series scope", () => {
    expect(seasonScopeKey(42, 0)).not.toBe(seriesScopeKey(42));
  });

  it("item scans key on media_type + season + episode", () => {
    const a = entry({ kind: "item", media_type: "episode", media_id: 5, season: 1, episode: 1 });
    const b = entry({ kind: "item", media_type: "episode", media_id: 5, season: 1, episode: 2 });
    expect(entryScopeKey(a)).not.toBe(entryScopeKey(b));
  });

  it("returns null for non-scan entries (no structured kind)", () => {
    expect(entryScopeKey(entry({ action: "Manual Download" }))).toBeNull();
  });
});

describe("scan-scope: runningScans derivation (restoration)", () => {
  it("maps running and queued scan entries by scope with cancellable + role", () => {
    const m = runningScans([
      entry({ id: "10", kind: "series", media_id: 42, cancellable: true }),
      entry({ id: "11", kind: "full", queued: true, cancellable: true, required_role: "admin" }),
      entry({ id: "12", action: "Manual Download" }), // not a scan
    ]);

    expect(m.size).toBe(2);
    expect(m.get(seriesScopeKey(42))).toEqual({ activityId: "10", cancellable: true });
    expect(m.get(scanScopeKey("full"))).toEqual({
      activityId: "11",
      cancellable: true,
      requiredRole: "admin",
    });
  });

  it("excludes terminal and doomed entries", () => {
    const m = runningScans([
      entry({ id: "1", kind: "series", media_id: 1, done: true }),
      entry({ id: "2", kind: "series", media_id: 2, done: true, cancelled: true }),
      // queued-dismiss-cancelled: cancelled without done yet — doomed.
      entry({ id: "3", kind: "series", media_id: 3, queued: true, cancelled: true }),
    ]);
    expect(m.size).toBe(0);
  });

  it("restores from a fresh activity snapshot with zero SSE events seen", () => {
    // The load-bearing reconnect path: derive purely from the GET payload.
    const snapshot = [
      entry({ id: "20", kind: "movie", media_id: 7, cancellable: true }),
      entry({ id: "21", kind: "series", media_id: 9, done: true }),
    ];
    const m = runningScans(snapshot);
    expect(m.has(movieScopeKey(7))).toBe(true);
    expect(m.has(seriesScopeKey(9))).toBe(false);
  });
});

describe("scan-scope: ScanAccepted decode", () => {
  it("decodes the 202 accept body", () => {
    const accepted = decodeScanAccepted({ activity_id: "37", status: "scan started" });
    expect(accepted.activity_id).toBe("37");
    expect(accepted.status).toBe("scan started");
  });

  it("rejects a body without activity_id", () => {
    expect(() => decodeScanAccepted({ status: "scan started" })).toThrow();
  });
});
