// events.test.ts — the stream as this module wires it (the headers it
// presents, the 401 seam, the degraded-poll mapping off the library's
// states) and the replay-table handlers driven by frames on a fake stream.
// The connection lifecycle itself (backoff, visibility, watchdog, cursor) is
// the library's and is tested there; the revalidate body is pinned by
// events.revalidate.test.ts (mocked seams) and events.integration.test.ts
// (real coverage modules); here the legs are stubbed inert.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// Type-only: erased at runtime, so the hoisted vi.mock factory may reference it.
import type * as BusModule from "./bus.js";
import { EPOCH_A, fakeSSE, type FakeSSE } from "./events-fakes.js";

// The fake server behind the injected fetch; replaced per test, reached
// through a hoisted slot because the api-client mock below is hoisted too.
const server = vi.hoisted(() => ({ current: null as FakeSSE | null }));
vi.mock("./api-client.js", () => ({
  authFetch: (input: RequestInfo | URL, init?: RequestInit) => {
    if (!server.current) {
      throw new Error("fake server not installed");
    }
    return server.current.fetch(input, init);
  },
  handleSessionExpiry: vi.fn(),
}));

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./sync-jobs.js", () => ({
  syncDoneFromEvent: vi.fn(),
  clearSyncCorrelation: vi.fn(),
  reattachSyncWatches: vi.fn(async () => undefined),
}));
vi.mock("./coverage-heal.js", () => ({
  healFromCoverageEvent: vi.fn(),
  resetCoverageHeal: vi.fn(),
  subsumeDirtyRoots: vi.fn(),
}));
vi.mock("./history.js", () => ({ noteHistoryMutation: vi.fn() }));
vi.mock("./status.js", () => ({
  pollStatus: vi.fn(async () => undefined),
  abortPoll: vi.fn(),
  setStatusDegraded: vi.fn(),
  setStatusAttached: vi.fn(),
  applyActivityEvent: vi.fn(),
  applyAlertEvent: vi.fn(),
  applyProviderEvent: vi.fn(),
}));
vi.mock("@cplieger/actions", () => ({ registerCleanup: vi.fn() }));
vi.mock("./search.js", () => ({ noteServerRestart: vi.fn() }));
vi.mock("./bus.js", async (importOriginal) => ({
  ...(await importOriginal<typeof BusModule>()),
  emit: vi.fn(),
}));
// The transaction seams, inert: the collection leg finds nothing registered
// and no library route, so it settles empty; the page leg applies at once.
vi.mock("./coverage.js", () => ({
  applyCoveragePair: vi.fn(),
  abortInFlightPairFetch: vi.fn(),
}));
vi.mock("./coverage-store.js", () => ({
  beginCoveredPairWrite: vi.fn(() => vi.fn()),
  registeredCollections: vi.fn(() => new Set<string>()),
  setCollectionLegJoin: vi.fn(),
  releaseCoverageTombstones: vi.fn(),
}));
vi.mock("./page-leg.js", () => ({
  currentRouteKey: vi.fn(() => "history"),
  routeSubject: vi.fn(() => null),
  dispatchTransactionPageLeg: vi.fn(async () => "applied"),
}));
vi.mock("./wire/client.gen.js", () => ({
  PATH_EVENTS: "/api/events",
  PATH_EVENTS_SYNC: "/api/events/sync",
  PATH_EVENTS_ALIVE: "/api/events/alive",
  coverageSeriesRaw: vi.fn(async () => ({ ok: true, status: 200, data: [] })),
  coverageMoviesRaw: vi.fn(async () => ({ ok: true, status: 200, data: [] })),
}));

import * as notify from "./notify.js";
import { healFromCoverageEvent } from "./coverage-heal.js";
import { noteHistoryMutation } from "./history.js";
import { syncDoneFromEvent } from "./sync-jobs.js";
import {
  abortPoll,
  applyActivityEvent,
  applyAlertEvent,
  applyProviderEvent,
  setStatusDegraded,
} from "./status.js";
import { handleSessionExpiry } from "./api-client.js";
import { emit, BusEvent } from "./bus.js";
import { _resetSubjectsForTest } from "./subjects.js";

const events = await import("./events.js");

/** Flush microtasks and due timers so the stream reader sees what was
 *  written. One async tick drains a bounded number of promise hops, and the
 *  reader, the digest POST and the legs chain more than that. */
async function settle(): Promise<void> {
  for (let i = 0; i < 6; i++) {
    await vi.advanceTimersByTimeAsync(0);
  }
}

/** Connect and answer the connection with a fresh hello, so application
 *  frames (the replay table) are reachable. */
async function openStream(head = 0): Promise<void> {
  events.connect();
  await settle();
  server.current?.last().hello(EPOCH_A, { head });
  await settle();
}

function id(offset: number): string {
  return `${EPOCH_A}:${String(offset)}`;
}

/** A decodable sync:done payload (job 7). */
const syncDonePayload = {
  job_id: 7,
  outcome: "result",
  file_ref: {
    media_type: "movie",
    media_id: "tmdb-1",
    language: "en",
    variant: "standard",
    source: "external",
  },
  offset_ms: 250,
  confidence: 0.9,
  method: "audio",
  applied: true,
  dry_run: true,
};

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0); // deterministic backoff jitter
  // The browser project runs in real Chromium, where SharedWorker exists;
  // these suites pin the per-tab stream, so the worker branch is closed.
  vi.stubGlobal("SharedWorker", undefined);
  server.current = fakeSSE();
  localStorage.removeItem("subflux.sse_client");
});

afterEach(() => {
  events._resetEventsForTest();
  _resetSubjectsForTest();
  server.current = null;
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("events: the connection as this module presents it", () => {
  it("connects to /api/events with the wire revision and a persisted client tag", async () => {
    events.connect();
    await settle();

    const conn = server.current!.last();
    expect(conn.url).toBe("/api/events");
    expect(conn.headers.get("SSE-Wire")).toBe("1");
    expect(conn.cursor).toBeNull(); // a fresh page presents no cursor
    const tag = conn.headers.get("SSE-Client");
    expect(tag).toMatch(/^[0-9a-f]{16}$/);
    expect(localStorage.getItem("subflux.sse_client")).toBe(tag);

    // Idempotent while a stream exists.
    events.connect();
    await settle();
    expect(server.current!.connections).toHaveLength(1);
  });

  it("a second page load of the same profile presents the same tag", async () => {
    localStorage.setItem("subflux.sse_client", "0123456789abcdef");
    events.connect();
    await settle();

    expect(server.current!.last().headers.get("SSE-Client")).toBe("0123456789abcdef");
  });

  it("the digest carries the same client tag as the stream", async () => {
    await openStream();

    expect(server.current!.digestCalls).toHaveLength(1);
    expect(server.current!.digestCalls[0]?.headers.get("SSE-Client")).toBe(
      server.current!.last().headers.get("SSE-Client"),
    );
  });

  it("a 401 on the stream routes through the session-expiry chokepoint", async () => {
    server.current!.refuseStream = 401;
    events.connect();
    await settle();

    expect(handleSessionExpiry).toHaveBeenCalledWith(401);
  });

  it("a 503 on the stream does not touch the session", async () => {
    server.current!.refuseStream = 503;
    events.connect();
    await settle();

    expect(handleSessionExpiry).not.toHaveBeenCalled();
  });

  it("the teardown stops the stream and aborts the status poll", async () => {
    await openStream();
    const conn = server.current!.last();

    events._resetEventsForTest();

    expect(conn.ended).toBe(true); // the aborted fetch tore the body down
    expect(events._stateForTest().stream).toBeNull();
    expect(abortPoll).toHaveBeenCalled();
  });

  it("open dispatches NO unconditional refetch (the deleted live bug)", async () => {
    await openStream();

    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
    // The boot digest over an empty map named nothing, so no leg ran.
    expect(server.current!.digestCalls[0]?.subjects).toStrictEqual([]);
  });
});

describe("events: the degraded poll follows the stream's state changes", () => {
  it("a refused connect enters degraded polling; the next open leaves it", async () => {
    server.current!.refuseStream = 503;
    events.connect();
    await settle();
    expect(setStatusDegraded).toHaveBeenLastCalledWith(expect.objectContaining({ to: "backoff" }));

    // The ladder's next attempt is answered with a stream and a hello.
    server.current!.refuseStream = null;
    await vi.advanceTimersByTimeAsync(2_000);
    server.current!.last().hello(EPOCH_A);
    await settle();

    expect(setStatusDegraded).toHaveBeenLastCalledWith(expect.objectContaining({ to: "open" }));
  });

  it("the first attempt is not a down period", async () => {
    events.connect();
    await settle();

    expect(setStatusDegraded).toHaveBeenLastCalledWith(
      expect.objectContaining({ from: "stopped", to: "connecting" }),
    );
  });

  it("a stream the server closes re-enters the ladder", async () => {
    await openStream();
    vi.mocked(setStatusDegraded).mockClear();

    server.current!.last().end();
    await settle();

    expect(setStatusDegraded).toHaveBeenLastCalledWith(
      expect.objectContaining({ from: "open", to: "backoff" }),
    );
  });
});

describe("events: SSE handlers (the replay table)", () => {
  it("coverage dispatches its decoded payload to the heal coalescer", async () => {
    await openStream();

    const payload = {
      media_type: "episode",
      media_id: "tvdb-81189-s01e01",
      language: "en",
      variant: "standard",
      source: "opensubtitles",
    };
    server.current!.last().frame("coverage", payload, id(1));
    await settle();

    // The coalescer owns parse/gate/coalesce; the handler owns only decode +
    // dispatch, and no longer touches the full-collection refresh path.
    expect(healFromCoverageEvent).toHaveBeenCalledExactlyOnceWith(payload);
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("an undecodable coverage frame is dropped without a heal dispatch", async () => {
    await openStream();

    server.current!.last().frame("coverage", { media_type: 42 }, id(1));
    await settle();

    expect(healFromCoverageEvent).not.toHaveBeenCalled();
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("a frame's id advances the cursor the next connect presents", async () => {
    await openStream();
    server.current!.last().frame("notify", { level: "info", text: "x" }, id(4));
    await settle();

    server.current!.last().end();
    await vi.advanceTimersByTimeAsync(2_000);

    expect(server.current!.connections).toHaveLength(2);
    expect(server.current!.last().cursor).toBe(id(4));
  });

  it("notify dispatches by level", async () => {
    await openStream();

    const conn = server.current!.last();
    conn.frame("notify", { level: "error", text: "provider down" }, id(1));
    conn.frame("notify", { level: "info", text: "fyi" }, id(2));
    conn.frame("notify", { level: "success", text: "subtitle saved" }, id(3));
    await settle();

    expect(notify.error).toHaveBeenCalledWith("provider down");
    expect(notify.info).toHaveBeenCalledWith("fyi");
    expect(notify.success).toHaveBeenCalledWith("subtitle saved");
  });

  it("scan:start shows its toast", async () => {
    await openStream();

    const conn = server.current!.last();
    conn.frame(
      "scan:start",
      { action: "scan", detail: "Breaking Bad", source: "scheduled" },
      id(4),
    );
    await settle();

    expect(notify.info).toHaveBeenCalledExactlyOnceWith("Scan started: Breaking Bad");
  });

  it("scan:done applies nothing: no status poll, no page refresh, zero coverage fetches", async () => {
    // The terminal activity upsert owns the status flip and the history
    // trigger; per-root coverage events own the row heals. Scan completion
    // must not trigger a blanket refresh or any coverage fetch.
    await openStream();
    vi.mocked(emit).mockClear();

    const conn = server.current!.last();
    conn.frame("scan:done", { action: "scan", detail: "", source: "scheduled" }, id(5));
    await settle();

    expect(emit).not.toHaveBeenCalled();
    expect(healFromCoverageEvent).not.toHaveBeenCalled();
    expect(noteHistoryMutation).not.toHaveBeenCalled();
  });

  it("a coverage frame notes the history trigger OUTSIDE the heal gate", async () => {
    await openStream();

    server.current!.last().frame(
      "coverage",
      {
        media_type: "episode",
        media_id: "tvdb-42-s01e01",
        language: "en",
        variant: "standard",
        source: "opensubtitles",
      },
      id(5),
    );
    await settle();

    // Both observers run: the gated heal (its own gate lives inside the
    // mocked module) and the history trigger beside it.
    expect(noteHistoryMutation).toHaveBeenCalledTimes(1);
    expect(healFromCoverageEvent).toHaveBeenCalledTimes(1);
  });

  it("a TERMINAL activity upsert notes the history trigger; a running one does not", async () => {
    await openStream();

    const entry = {
      started_at: "2026-08-30T10:00:00Z",
      id: "a1",
      action: "Manual Download",
      detail: "d",
      source: "manual",
      done: false,
    };
    const conn = server.current!.last();
    conn.frame("activity", { op: "upsert", entry }, id(8));
    await settle();
    expect(noteHistoryMutation).not.toHaveBeenCalled();

    conn.frame("activity", { op: "upsert", entry: { ...entry, done: true } }, id(9));
    await settle();
    expect(noteHistoryMutation).toHaveBeenCalledTimes(1);
  });

  it("an activity REMOVE never notes the history trigger, done or not", async () => {
    await openStream();

    const entry = {
      started_at: "2026-08-30T10:00:00Z",
      id: "a1",
      action: "Manual Download",
      detail: "d",
      source: "manual",
      done: true,
    };
    server.current!.last().frame("activity", { op: "remove", entry }, id(8));
    await settle();

    expect(noteHistoryMutation).not.toHaveBeenCalled();
  });

  it("sync:done routes its decoded payload to the settlement registry", async () => {
    await openStream();

    server.current!.last().frame("sync:done", syncDonePayload, id(6));
    await settle();

    expect(syncDoneFromEvent).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ job_id: 7, applied: true, offset_ms: 250 }),
    );
  });

  it("an undecodable sync:done frame is dropped without a settlement", async () => {
    await openStream();

    server.current!.last().frame("sync:done", { job_id: "not-a-number" }, id(6));
    await settle();

    expect(syncDoneFromEvent).not.toHaveBeenCalled();
  });

  it("activity deltas dispatch their decoded payload to the status store", async () => {
    await openStream();

    const payload = {
      op: "upsert",
      entry: {
        started_at: "2026-08-30T10:00:00Z",
        id: "a1",
        action: "Series Search",
        detail: "d",
        source: "manual",
        done: false,
      },
    };
    server.current!.last().frame("activity", payload, id(8));
    await settle();

    expect(applyActivityEvent).toHaveBeenCalledExactlyOnceWith(payload);
  });

  it("alert deltas dispatch their decoded payload to the status store", async () => {
    await openStream();

    const payload = {
      op: "raise",
      alert: {
        time: "2026-08-30T10:00:00Z",
        level: "warn",
        message: "m",
        source: "scanner",
        kind: "transient",
        id: 3,
        dismissed: false,
      },
    };
    server.current!.last().frame("alert", payload, id(9));
    await settle();

    expect(applyAlertEvent).toHaveBeenCalledExactlyOnceWith(payload);
  });

  it("provider deltas dispatch their decoded payload to the status store", async () => {
    await openStream();

    const payload = {
      op: "raise",
      entry: {
        provider: "opensubtitles",
        status: { recent_failures: 3, threshold: 5, timed_out: true },
      },
    };
    server.current!.last().frame("provider", payload, id(10));
    await settle();

    expect(applyProviderEvent).toHaveBeenCalledExactlyOnceWith(payload);
  });

  it("undecodable status deltas are dropped without an application", async () => {
    await openStream();

    const conn = server.current!.last();
    conn.frame("activity", { op: "explode" }, id(8));
    conn.frame("alert", { op: 42 }, id(9));
    conn.frame("provider", { entry: "nope" }, id(10));
    await settle();

    expect(applyActivityEvent).not.toHaveBeenCalled();
    expect(applyAlertEvent).not.toHaveBeenCalled();
    expect(applyProviderEvent).not.toHaveBeenCalled();
  });

  it("a REPLAYED activity delta re-applies — idempotence lives in the store, not here", async () => {
    // The status rows carry no dedupe: the replay table re-applies what the
    // server's ring re-sends and the store's keyed appliers make that a no-op.
    await openStream();
    const payload = {
      op: "upsert",
      entry: {
        started_at: "2026-08-30T10:00:00Z",
        id: "a1",
        action: "Series Search",
        detail: "d",
        source: "manual",
        done: true,
      },
    };
    server.current!.last().frame("activity", payload, id(11));
    await settle();
    expect(applyActivityEvent).toHaveBeenCalledTimes(1);

    // The connection drops; the resumed reconnect replays the same frame.
    server.current!.last().end();
    await vi.advanceTimersByTimeAsync(2_000);
    server.current!.last().hello(EPOCH_A, { head: 11, resumed: true });
    server.current!.last().frame("activity", payload, id(11));
    await settle();

    expect(applyActivityEvent).toHaveBeenCalledTimes(2);
  });

  it("the legacy epoch frame decodes to nothing", async () => {
    await openStream();
    vi.mocked(emit).mockClear();

    server.current!.last().frame("epoch", { boot_id: EPOCH_A, gap: true, head: 3 });
    await settle();

    expect(emit).not.toHaveBeenCalled();
    expect(events._stateForTest().stream?.kind).toBe("open");
  });

  it("malformed frames are dropped without side effects", async () => {
    await openStream();

    server.current!.last().frame("notify", { level: "nonsense-level", text: 42 }, id(1));
    await settle();

    expect(notify.error).not.toHaveBeenCalled();
    expect(notify.info).not.toHaveBeenCalled();
    expect(notify.success).not.toHaveBeenCalled();
  });
});
