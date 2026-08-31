// events.test.ts — the SSE connection lifecycle on the E3 contract: the
// backoff ladder, the visibility pause, the epoch gate on frame dispatch,
// and the replay-table handlers. The transaction machinery is pinned by
// events.transaction.test.ts (mocked seams) and events.integration.test.ts
// (real coverage modules); here the legs are stubbed inert.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SSE_RECONNECT_MS, VISIBILITY_DEBOUNCE_MS } from "./constants.js";
// Type-only: erased at runtime, so the hoisted vi.mock factory may reference it.
import type * as BusModule from "./bus.js";
import { FakeEventSource, lastFakeES } from "./events-fakes.js";

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./sync-jobs.js", () => ({
  syncDoneFromEvent: vi.fn(),
  clearSyncCorrelation: vi.fn(),
}));
vi.mock("./coverage-heal.js", () => ({
  healFromCoverageEvent: vi.fn(),
  resetCoverageHeal: vi.fn(),
  subsumeDirtyRoots: vi.fn(),
}));
vi.mock("./status.js", () => ({
  pollStatus: vi.fn(async () => undefined),
  abortPoll: vi.fn(),
  setStatusDegraded: vi.fn(),
  applyActivityEvent: vi.fn(),
  applyAlertEvent: vi.fn(),
  applyProviderEvent: vi.fn(),
}));
vi.mock("@cplieger/actions", () => ({ registerCleanup: vi.fn() }));
vi.mock("./bus.js", async (importOriginal) => ({
  ...(await importOriginal<typeof BusModule>()),
  emit: vi.fn(),
}));
// The transaction seams, inert: the collection leg finds nothing registered
// and no library route, so it settles empty; the page leg applies at once.
vi.mock("./coverage.js", () => ({
  applyCoveragePair: vi.fn(),
  abortInFlightPairFetch: vi.fn(),
  beginCoverageTransaction: vi.fn(),
  beginCoveredPairWrite: vi.fn(() => vi.fn()),
  registeredCollections: vi.fn(() => new Set<string>()),
  setCollectionLegJoin: vi.fn(),
  settleCoverageTransaction: vi.fn(),
}));
vi.mock("./page-leg.js", () => ({
  currentRouteKey: vi.fn(() => "history"),
  dispatchTransactionPageLeg: vi.fn(async () => "applied"),
}));
vi.mock("./wire/client.gen.js", () => ({
  PATH_EVENTS: "/api/events",
  coverageSeriesRaw: vi.fn(async () => ({ ok: true, status: 200, data: [] })),
  coverageMoviesRaw: vi.fn(async () => ({ ok: true, status: 200, data: [] })),
}));

import * as notify from "./notify.js";
import { healFromCoverageEvent } from "./coverage-heal.js";
import { syncDoneFromEvent } from "./sync-jobs.js";
import {
  abortPoll,
  applyActivityEvent,
  applyAlertEvent,
  applyProviderEvent,
  pollStatus,
  setStatusDegraded,
} from "./status.js";
import { emit, BusEvent } from "./bus.js";

const events = await import("./events.js");

function setHidden(hidden: boolean): void {
  Object.defineProperty(document, "hidden", { value: hidden, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
}

/** Open the newest connection and deliver a clean boot epoch, so post-epoch
 *  frame dispatch (the replay table) is reachable. */
async function openWithEpoch(head = 0): Promise<void> {
  lastFakeES().open();
  lastFakeES().epoch("boot-a", false, head);
  await vi.runOnlyPendingTimersAsync();
}

/** A decodable sync:done payload (job 7). */
const syncDonePayload = {
  job_id: 7,
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
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0); // deterministic backoff jitter
});

afterEach(() => {
  events._resetEventsForTest();
  vi.runOnlyPendingTimers();
  events._resetEventsForTest();
  FakeEventSource.instances = [];
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("events: SSE connection", () => {
  it("connect creates EventSource to /api/events, cursor-less on boot", () => {
    events.connect();
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(lastFakeES().url).toBe("/api/events");

    // Idempotent while a connection exists.
    events.connect();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("open dispatches NO unconditional refetch (the deleted live bug)", async () => {
    events.connect();
    await openWithEpoch();
    vi.mocked(emit).mockClear();
    vi.mocked(pollStatus).mockClear();

    // A later reconnect's open must not blanket-invalidate either: the epoch
    // verdict owns recovery now.
    lastFakeES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    lastFakeES().open();

    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
    expect(pollStatus).not.toHaveBeenCalled();
  });

  it("disconnect closes EventSource, clears timers, aborts the poll", () => {
    events.connect();
    const es = lastFakeES();

    events._resetEventsForTest(); // teardown path
    expect(es.closed).toBe(true);

    events.connect();
    const es2 = lastFakeES();
    setHidden(true); // the production disconnect path
    expect(es2.closed).toBe(true);
    expect(abortPoll).toHaveBeenCalled();
    setHidden(false);

    // A pending reconnect timer would create a new instance; only the
    // visibility debounce may fire.
    const count = FakeEventSource.instances.length;
    vi.advanceTimersByTime(VISIBILITY_DEBOUNCE_MS);
    expect(FakeEventSource.instances).toHaveLength(count + 1);
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(count + 1);
  });

  it("reconnects with exponential backoff on error", () => {
    events.connect();

    // Attempt 0: base delay = SSE_RECONNECT_MS (jitter pinned to 0).
    lastFakeES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(2);

    // Attempt 1: doubled.
    lastFakeES().fail();
    vi.advanceTimersByTime(2 * SSE_RECONNECT_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(3);
  });

  it("resets the reconnect attempt counter on a NON-LATCHED open", () => {
    events.connect();
    lastFakeES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    lastFakeES().fail();
    vi.advanceTimersByTime(2 * SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(3);

    // A successful non-latched open resets the attempt counter…
    lastFakeES().open();
    expect(events._stateForTest().reconnectAttempt).toBe(0);

    // …so the next failure reconnects after the BASE delay again, not 4x.
    lastFakeES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(4);
  });

  it("disconnects on visibilitychange hidden", () => {
    events.connect();
    const es = lastFakeES();

    setHidden(true);

    expect(es.closed).toBe(true);
    expect(abortPoll).toHaveBeenCalled();
  });

  it("reconnects with debounce on visibilitychange visible", () => {
    events.connect();
    setHidden(true);
    expect(lastFakeES().closed).toBe(true);
    const count = FakeEventSource.instances.length;

    setHidden(false);
    vi.advanceTimersByTime(VISIBILITY_DEBOUNCE_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(count);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(count + 1);
  });

  it("a transient error while the connection is open schedules no reconnect", async () => {
    events.connect();
    await openWithEpoch();

    // readyState stays OPEN: the browser retries this one itself.
    lastFakeES().errorWhileOpen();
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);

    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("disconnect cancels a pending reconnect", () => {
    events.connect();
    lastFakeES().fail(); // schedules the backoff reconnect

    setHidden(true); // disconnect clears the timer
    setHidden(false);
    vi.advanceTimersByTime(VISIBILITY_DEBOUNCE_MS); // the debounce reconnect
    const count = FakeEventSource.instances.length;
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS); // the cancelled ladder slot must not fire

    expect(FakeEventSource.instances).toHaveLength(count);
  });

  it("schedules no second reconnect while one is already pending", () => {
    events.connect();
    lastFakeES().fail(); // schedules reconnect A

    // A connect() while A is still pending, and that connection drops too.
    events.connect();
    lastFakeES().fail();

    // Only ONE timer handle is tracked; the tracked one fires once.
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(3);
    setHidden(true);
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(3);
    setHidden(false);
  });

  it("backoff jitter is additive, so the delay never falls below the base", () => {
    vi.spyOn(Math, "random").mockReturnValue(1); // maximum jitter
    events.connect();

    lastFakeES().fail();
    // base + jitter = 2x SSE_RECONNECT_MS at attempt 0 with jitter pinned high.
    vi.advanceTimersByTime(2 * SSE_RECONNECT_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(2);
  });

  it("a second visibilitychange restarts the reconnect debounce", () => {
    events.connect();
    setHidden(true);
    const count = FakeEventSource.instances.length;

    setHidden(false);
    vi.advanceTimersByTime(VISIBILITY_DEBOUNCE_MS - 1);
    setHidden(false); // flapped back before the first debounce elapsed
    vi.advanceTimersByTime(1);

    // The first debounce was cancelled, so its deadline passes with no connect.
    expect(FakeEventSource.instances).toHaveLength(count);
    vi.advanceTimersByTime(VISIBILITY_DEBOUNCE_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(count + 1);
  });
});

describe("events: SSE handlers (post-epoch, the replay table)", () => {
  it("coverage dispatches its decoded payload to the heal coalescer", async () => {
    events.connect();
    await openWithEpoch();

    const payload = {
      media_type: "episode",
      media_id: "tvdb-81189-s01e01",
      language: "en",
      variant: "standard",
      source: "opensubtitles",
    };
    lastFakeES().frame("coverage", payload, 1);

    // The coalescer owns parse/gate/coalesce; the handler owns only decode +
    // dispatch, and no longer touches the full-collection refresh path.
    expect(healFromCoverageEvent).toHaveBeenCalledExactlyOnceWith(payload);
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("an undecodable coverage frame is dropped without a heal dispatch", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame("coverage", { media_type: 42 }, 1);

    expect(healFromCoverageEvent).not.toHaveBeenCalled();
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("notify dispatches by level", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame("notify", { level: "error", text: "provider down" }, 1);
    lastFakeES().frame("notify", { level: "info", text: "fyi" }, 2);
    lastFakeES().frame("notify", { level: "success", text: "subtitle saved" }, 3);

    expect(notify.error).toHaveBeenCalledWith("provider down");
    expect(notify.info).toHaveBeenCalledWith("fyi");
    expect(notify.success).toHaveBeenCalledWith("subtitle saved");
  });

  it("a REPLAYED notify shows ONE toast (dedupe keyed boot_id + frame_id)", async () => {
    events.connect();
    await openWithEpoch();
    lastFakeES().frame("notify", { level: "info", text: "once" }, 7);
    expect(notify.info).toHaveBeenCalledTimes(1);

    // The connection drops; the native retry replays the same frame.
    lastFakeES().errorWhileOpen();
    lastFakeES().frame("notify", { level: "info", text: "once" }, 7);
    lastFakeES().epoch("boot-a", false, 7);
    await vi.runOnlyPendingTimersAsync();

    expect(notify.info).toHaveBeenCalledTimes(1);
  });

  it("scan:start shows its toast, deduped like notify's", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame(
      "scan:start",
      { action: "scan", detail: "Breaking Bad", source: "scheduled" },
      4,
    );
    lastFakeES().frame(
      "scan:start",
      { action: "scan", detail: "Breaking Bad", source: "scheduled" },
      4,
    );

    expect(notify.info).toHaveBeenCalledExactlyOnceWith("Scan started: Breaking Bad");
  });

  it("scan:done re-applies state: status poll + page refresh", async () => {
    events.connect();
    await openWithEpoch();
    vi.mocked(pollStatus).mockClear();
    vi.mocked(emit).mockClear();

    lastFakeES().frame("scan:done", { action: "scan", detail: "", source: "scheduled" }, 5);

    expect(pollStatus).toHaveBeenCalled();
    expect(emit).toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("sync:done routes its decoded payload to the settlement registry", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame("sync:done", syncDonePayload, 6);

    expect(syncDoneFromEvent).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ job_id: 7, applied: true, offset_ms: 250 }),
    );
  });

  it("an undecodable sync:done frame is dropped without a settlement", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame("sync:done", { job_id: "not-a-number" }, 6);

    expect(syncDoneFromEvent).not.toHaveBeenCalled();
  });

  it("activity deltas dispatch their decoded payload to the status store", async () => {
    events.connect();
    await openWithEpoch();

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
    lastFakeES().frame("activity", payload, 8);

    expect(applyActivityEvent).toHaveBeenCalledExactlyOnceWith(payload);
  });

  it("alert deltas dispatch their decoded payload to the status store", async () => {
    events.connect();
    await openWithEpoch();

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
    lastFakeES().frame("alert", payload, 9);

    expect(applyAlertEvent).toHaveBeenCalledExactlyOnceWith(payload);
  });

  it("provider deltas dispatch their decoded payload to the status store", async () => {
    events.connect();
    await openWithEpoch();

    const payload = {
      op: "raise",
      entry: {
        provider: "opensubtitles",
        status: { recent_failures: 3, threshold: 5, timed_out: true },
      },
    };
    lastFakeES().frame("provider", payload, 10);

    expect(applyProviderEvent).toHaveBeenCalledExactlyOnceWith(payload);
  });

  it("undecodable status deltas are dropped without an application", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame("activity", { op: "explode" }, 8);
    lastFakeES().frame("alert", { op: 42 }, 9);
    lastFakeES().frame("provider", { entry: "nope" }, 10);

    expect(applyActivityEvent).not.toHaveBeenCalled();
    expect(applyAlertEvent).not.toHaveBeenCalled();
    expect(applyProviderEvent).not.toHaveBeenCalled();
  });

  it("a REPLAYED activity delta re-applies — idempotence lives in the store, not here", async () => {
    // Unlike notify, the status rows carry no toast dedupe: the replay table
    // re-applies them and the store's keyed appliers make that a no-op.
    events.connect();
    await openWithEpoch();
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
    lastFakeES().frame("activity", payload, 11);
    expect(applyActivityEvent).toHaveBeenCalledTimes(1);

    // The connection drops; the native retry replays the same frame.
    lastFakeES().errorWhileOpen();
    lastFakeES().frame("activity", payload, 11);
    lastFakeES().epoch("boot-a", false, 11);
    await vi.runOnlyPendingTimersAsync();

    expect(applyActivityEvent).toHaveBeenCalledTimes(2);
  });

  it("malformed frames are dropped without side effects", async () => {
    events.connect();
    await openWithEpoch();

    lastFakeES().frame("notify", { level: "nonsense-level", text: 42 }, 1);

    expect(notify.error).not.toHaveBeenCalled();
    expect(notify.info).not.toHaveBeenCalled();
    expect(notify.success).not.toHaveBeenCalled();
  });
});

describe("events: the epoch gate on dispatch", () => {
  it("pre-epoch frames buffer: nothing dispatches until the verdict", async () => {
    events.connect();
    lastFakeES().open();

    lastFakeES().frame("notify", { level: "info", text: "replayed" }, 3);
    expect(notify.info).not.toHaveBeenCalled();

    lastFakeES().epoch("boot-a", false, 3);
    await vi.runOnlyPendingTimersAsync();
    expect(notify.info).toHaveBeenCalledWith("replayed");
    expect(events._stateForTest().appliedHigh).toBe(3);
  });

  it("verdictBuffer frames of a condemned connection are never applied", async () => {
    events.connect();
    lastFakeES().open();
    lastFakeES().frame("notify", { level: "info", text: "condemned" }, 3);

    lastFakeES().fail(); // dies before its epoch: the buffer is discarded

    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 3);
    await vi.runOnlyPendingTimersAsync();

    expect(notify.info).not.toHaveBeenCalled();
  });

  it("the epoch handler never reads lastEventId", async () => {
    events.connect();
    lastFakeES().open();

    // A hostile epoch frame carrying an id: the handler must not turn it
    // into bookkeeping — no counter moves, and the next recreate carries no
    // cursor derived from it.
    lastFakeES().frame("epoch", { boot_id: "boot-a", gap: false, head: 0 }, 999);
    await vi.runOnlyPendingTimersAsync();

    expect(events._stateForTest().appliedHigh).toBeNull();
    lastFakeES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    expect(lastFakeES().url).toBe("/api/events");
  });
});

describe("events: the status poll floor (E2)", () => {
  it("a CLOSED connection enters degraded polling; the next open leaves it", () => {
    events.connect();
    expect(setStatusDegraded).not.toHaveBeenCalled();

    // The browser gave up (refused connect / server gone): the reconnect
    // ladder is a DOWN period, so status rides the 5s poll.
    lastFakeES().fail();
    expect(setStatusDegraded).toHaveBeenLastCalledWith(true);

    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    lastFakeES().open();
    expect(setStatusDegraded).toHaveBeenLastCalledWith(false);
  });

  it("a CONNECTING blip does not trigger degraded polling", async () => {
    events.connect();
    await openWithEpoch();
    vi.mocked(setStatusDegraded).mockClear();

    // readyState stays OPEN: the browser retries this one itself.
    lastFakeES().errorWhileOpen();
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);

    expect(setStatusDegraded).not.toHaveBeenCalledWith(true);
  });

  it("a deliberate hidden-tab disconnect is not a down period", () => {
    events.connect();
    lastFakeES().fail();
    expect(setStatusDegraded).toHaveBeenLastCalledWith(true);

    // Hiding the tab cancels the ladder: no degraded poll may survive it (a
    // hidden tab issues zero status polls).
    setHidden(true);
    expect(setStatusDegraded).toHaveBeenLastCalledWith(false);
    setHidden(false);
  });
});
