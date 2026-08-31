// events.transaction.test.ts — THE ORDERED TRANSACTION (E3) against mocked
// legs: the boot gate, the trigger truth table, the two buffers and their
// boot-change arms, the commit-only watermark and synthetic cursor, the
// latched backoff ladder, and the deterministic step order. The coverage
// modules are real in events.integration.test.ts; here every seam is
// scripted so each fixture controls one variable.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EPOCH_TIMEOUT_MS, SSE_RECONNECT_MS, SSE_MAX_RECONNECT_MS } from "./constants.js";
import type * as BusModule from "./bus.js";
import { FakeEventSource, lastFakeES } from "./events-fakes.js";

// One shared sequence log: the deterministic-order fixtures read it.
const seq = vi.hoisted(() => ({ log: [] as string[] }));

const toasts = vi.hoisted(() => ({ infos: [] as string[] }));
vi.mock("./notify.js", () => ({
  error: vi.fn(),
  success: vi.fn(),
  info: (text: string) => {
    toasts.infos.push(text);
    seq.log.push(`toast:${text}`);
  },
}));

// The sync settlement registry: settlements and the boot-change clear land
// in the shared sequence log, so their ORDER against each other is a plain
// assertion (the held settlement must land before the correlation clears).
const syncReg = vi.hoisted(() => ({ settled: [] as number[], clears: 0 }));
vi.mock("./sync-jobs.js", () => ({
  syncDoneFromEvent: (ev: { job_id: number }) => {
    syncReg.settled.push(ev.job_id);
    seq.log.push(`syncDone:${String(ev.job_id)}`);
  },
  clearSyncCorrelation: () => {
    syncReg.clears += 1;
    seq.log.push("syncClear");
  },
}));

vi.mock("./coverage-heal.js", () => ({
  healFromCoverageEvent: (p: { media_id: string }) => {
    seq.log.push(`heal:${p.media_id}`);
  },
  resetCoverageHeal: vi.fn(),
  subsumeDirtyRoots: vi.fn(),
}));

const status = vi.hoisted(() => ({ polls: 0 }));
vi.mock("./status.js", () => ({
  pollStatus: async () => {
    status.polls += 1;
    seq.log.push("pollStatus");
  },
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

// The coverage seams: registration is scriptable tab state; applications are
// recorded. beginCoveredPairWrite/settle are inert (the tombstone lifecycle
// is integration-tested against the real module).
const cov = vi.hoisted(() => ({
  registered: new Set<string>(),
  applied: [] as { series: unknown; movies: unknown }[],
  aborts: 0,
}));
vi.mock("./coverage.js", () => ({
  applyCoveragePair: (series: unknown, movies: unknown) => {
    cov.applied.push({ series, movies });
    seq.log.push("applyPair");
  },
  abortInFlightPairFetch: () => {
    cov.aborts += 1;
  },
  beginCoverageTransaction: vi.fn(),
  beginCoveredPairWrite: vi.fn(() => vi.fn()),
  registeredCollections: () => cov.registered,
  setCollectionLegJoin: vi.fn(),
  settleCoverageTransaction: vi.fn(),
}));

// The page leg: scripted outcomes; records each dispatch's recovery flag.
const leg = vi.hoisted(() => ({
  route: "library",
  results: [] as (string | Error | Promise<string>)[],
  calls: [] as boolean[],
}));
vi.mock("./page-leg.js", () => ({
  currentRouteKey: () => leg.route,
  dispatchTransactionPageLeg: (recovery: boolean) => {
    leg.calls.push(recovery);
    seq.log.push("pageLeg");
    const next = leg.results.shift() ?? "applied";
    return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
  },
}));

// The collection pair at the network edge: per-call scripted results,
// deferrable; queries recorded for the ?recovery=1 pins.
interface RawResult {
  ok: boolean;
  status: number;
  data?: unknown;
  error?: string;
}
const wire = vi.hoisted(() => ({
  seriesQueries: [] as unknown[],
  moviesQueries: [] as unknown[],
  result: { ok: true, status: 200, data: [] as unknown } as RawResult,
  defer: false,
  pending: [] as ((r: RawResult) => void)[],
}));
function pairCall(kind: "series" | "movies", q: unknown): Promise<RawResult> {
  (kind === "series" ? wire.seriesQueries : wire.moviesQueries).push(q);
  seq.log.push(`fetch:${kind}`);
  if (wire.defer) {
    return new Promise((resolve) => {
      wire.pending.push(resolve);
    });
  }
  return Promise.resolve(wire.result);
}
vi.mock("./wire/client.gen.js", () => ({
  PATH_EVENTS: "/api/events",
  coverageSeriesRaw: (q?: unknown) => pairCall("series", q),
  coverageMoviesRaw: (q?: unknown) => pairCall("movies", q),
}));

import { emit, BusEvent } from "./bus.js";
import { subsumeDirtyRoots } from "./coverage-heal.js";

const events = await import("./events.js");

/** Flush microtasks + due timers so an epoch's transaction fully settles. */
async function settle(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
}

/** Boot to a committed steady state: connect, open, clean epoch at `head`,
 *  transaction commits. Returns after the commit. */
async function bootCommitted(head: number, bootId = "boot-a"): Promise<void> {
  events.connect();
  lastFakeES().open();
  lastFakeES().epoch(bootId, false, head);
  await settle();
  expect(events._stateForTest().watermark).toBe(head);
}

/** Drop the live connection (no transaction running) and let the ladder
 *  reconnect; returns the fresh connection's URL. */
function reconnect(): string {
  lastFakeES().fail();
  vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
  return lastFakeES().url;
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0);
  seq.log = [];
  toasts.infos = [];
  syncReg.settled = [];
  syncReg.clears = 0;
  status.polls = 0;
  cov.registered = new Set<string>();
  cov.applied = [];
  cov.aborts = 0;
  leg.route = "library";
  leg.results = [];
  leg.calls = [];
  wire.seriesQueries = [];
  wire.moviesQueries = [];
  wire.result = { ok: true, status: 200, data: [] };
  wire.defer = false;
  wire.pending = [];
});

afterEach(() => {
  events._resetEventsForTest();
  vi.runOnlyPendingTimers();
  events._resetEventsForTest();
  FakeEventSource.instances = [];
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("the boot gate", () => {
  it("resolves on the first valid epoch, and the boot transaction reads PLAIN", async () => {
    let gateOpen = false;
    void events.bootGate().then(() => {
      gateOpen = true;
    });
    events.connect();
    lastFakeES().open();
    await settle();
    expect(gateOpen).toBe(false); // no epoch yet

    lastFakeES().epoch("boot-a", false, 3);
    await settle();

    expect(gateOpen).toBe(true);
    // Boot transaction: collection pair fetched plain, page leg plain,
    // status fetched once; commit advanced the watermark.
    expect(wire.seriesQueries).toStrictEqual([undefined]);
    expect(wire.moviesQueries).toStrictEqual([undefined]);
    expect(leg.calls).toStrictEqual([false]);
    expect(status.polls).toBe(1);
    expect(events._stateForTest().watermark).toBe(3);
  });

  it("a refusal fails FAST: the gate degrades without waiting the deadline", async () => {
    let gateOpen = false;
    void events.bootGate().then(() => {
      gateOpen = true;
    });
    events.connect();

    lastFakeES().fail(); // the 401/refused connect: error fires immediately
    await Promise.resolve();

    expect(gateOpen).toBe(true); // no EPOCH_TIMEOUT_MS wait
  });

  it("a SILENT open stream degrades at the epoch deadline", async () => {
    let gateOpen = false;
    void events.bootGate().then(() => {
      gateOpen = true;
    });
    events.connect();
    lastFakeES().open();
    const es = lastFakeES();

    await vi.advanceTimersByTimeAsync(EPOCH_TIMEOUT_MS - 1);
    expect(gateOpen).toBe(false);
    await vi.advanceTimersByTimeAsync(1);

    expect(gateOpen).toBe(true);
    expect(es.closed).toBe(true); // timeout = undecodable handling: teardown + ladder
  });

  it("an undecodable epoch degrades and condemns the connection", async () => {
    let gateOpen = false;
    void events.bootGate().then(() => {
      gateOpen = true;
    });
    events.connect();
    lastFakeES().open();
    const es = lastFakeES();

    lastFakeES().frame("epoch", { boot_id: 42 }); // fails the generated decoder
    await settle();

    expect(gateOpen).toBe(true);
    expect(es.closed).toBe(true);
  });

  it("the transaction FOLLOWING a degraded boot uses RECOVERY semantics", async () => {
    events.connect();
    lastFakeES().fail(); // degrade
    await settle();

    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 3);
    await settle();

    // ?recovery=1 on the honoring endpoints; the page leg told the same.
    expect(wire.seriesQueries).toStrictEqual([{ recovery: 1 }]);
    expect(wire.moviesQueries).toStrictEqual([{ recovery: 1 }]);
    expect(leg.calls).toStrictEqual([true]);
  });
});

describe("buffers and the deterministic order", () => {
  it("a post-epoch frame > head is HELD and drained ASCENDING after commit", async () => {
    wire.defer = true;
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    // Mid-transaction: beyond-snapshot frames hold, out of order.
    lastFakeES().frame("notify", { level: "info", text: "seven" }, 7);
    lastFakeES().frame("notify", { level: "info", text: "six" }, 6);
    expect(toasts.infos).toStrictEqual([]);
    expect(events._stateForTest().holdQueueLength).toBe(2);

    for (const r of wire.pending.splice(0)) {
      r({ ok: true, status: 200, data: [] });
    }
    await settle();

    expect(events._stateForTest().watermark).toBe(5);
    expect(toasts.infos).toStrictEqual(["six", "seven"]); // ascending drain
    expect(events._stateForTest().appliedHigh).toBe(7);
  });

  it("frames ≤ head apply IMMEDIATELY mid-transaction", async () => {
    wire.defer = true;
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    lastFakeES().frame("notify", { level: "info", text: "within" }, 4);

    expect(toasts.infos).toStrictEqual(["within"]);
    for (const r of wire.pending.splice(0)) {
      r({ ok: true, status: 200, data: [] });
    }
  });

  it("classification precedes the legs, commit precedes the drain", async () => {
    // Replay before the epoch (buffered), a hold during the legs, and the
    // scripted sequence log to pin: classify → legs → commit(apply) → drain.
    wire.defer = true;
    events.connect();
    lastFakeES().open();
    lastFakeES().frame(
      "coverage",
      {
        media_type: "episode",
        media_id: "tvdb-1-s01e01",
        language: "en",
        variant: "standard",
        source: "auto",
      },
      2,
    );
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    lastFakeES().frame("notify", { level: "info", text: "held" }, 9);

    for (const r of wire.pending.splice(0)) {
      r({ ok: true, status: 200, data: [] });
    }
    await settle();

    const iHeal = seq.log.indexOf("heal:tvdb-1-s01e01");
    const iFetch = seq.log.indexOf("fetch:series");
    const iApply = seq.log.indexOf("applyPair");
    const iDrain = seq.log.indexOf("toast:held");
    expect(iHeal).toBeGreaterThanOrEqual(0);
    expect(iHeal).toBeLessThan(iFetch); // classified before the legs ran
    expect(iApply).toBeLessThan(iDrain); // snapshot applied before the drain
    expect(events._stateForTest().appliedHigh).toBe(9);
  });

  it("verdictBuffer overflow degrades into a latched recovery", async () => {
    events.connect();
    lastFakeES().open();
    const es = lastFakeES();
    for (let i = 0; i < 2_049; i++) {
      es.frame("notify", { level: "info", text: "x" }, i + 1);
    }

    expect(es.closed).toBe(true);
    expect(events._stateForTest().forceLatch).toBe(true);
    expect(toasts.infos).toStrictEqual([]); // discarded, never applied

    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    expect(lastFakeES().url).toBe("/api/events"); // latched recreate: no cursor
  });
});

describe("the trigger truth table and the synthetic cursor", () => {
  it("W == head: the resumed connection triggers nothing", async () => {
    await bootCommitted(5);
    expect(reconnect()).toBe("/api/events?last_id=5");

    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    expect(wire.seriesQueries).toHaveLength(1); // the boot's only
    expect(status.polls).toBe(1);
  });

  it("in-ring within budget: replay classifies, appliedHigh reaches head, no transaction", async () => {
    await bootCommitted(5);
    expect(reconnect()).toBe("/api/events?last_id=5");

    lastFakeES().open();
    lastFakeES().frame("notify", { level: "info", text: "r6" }, 6);
    lastFakeES().frame("notify", { level: "info", text: "r7" }, 7);
    lastFakeES().epoch("boot-a", false, 7);
    await settle();

    expect(toasts.infos).toStrictEqual(["r6", "r7"]);
    expect(events._stateForTest().appliedHigh).toBe(7);
    expect(wire.seriesQueries).toHaveLength(1); // zero refetches
  });

  it("the NATIVE-RETRY split: replay in budget = no transaction; a gap verdict = one", async () => {
    await bootCommitted(100);

    // The browser's own retry: same EventSource, header-carried cursor the
    // client never sees. Its replay classifies and covers the head.
    lastFakeES().errorWhileOpen();
    for (let id = 101; id <= 110; id++) {
      lastFakeES().frame("notify", { level: "info", text: `r${String(id)}` }, id);
    }
    lastFakeES().epoch("boot-a", false, 110);
    await settle();
    expect(events._stateForTest().appliedHigh).toBe(110);
    expect(wire.seriesQueries).toHaveLength(1); // ZERO refetches, NO transaction

    // The same retry too far back: the server strips and answers gap.
    lastFakeES().errorWhileOpen();
    lastFakeES().epoch("boot-a", true, 500);
    await settle();
    expect(wire.seriesQueries).toHaveLength(2); // exactly ONE transaction
    expect(events._stateForTest().watermark).toBe(500);
  });

  it("R5.2 max rows: W=100/appliedHigh=400 cursor-less; head 400 = nothing, head 401 = one transaction", async () => {
    await bootCommitted(100);
    // Live frames advance appliedHigh to 400 (never the watermark).
    for (const id of [250, 400]) {
      lastFakeES().frame("notify", { level: "info", text: `live${String(id)}` }, id);
    }
    expect(events._stateForTest().appliedHigh).toBe(400);
    expect(events._stateForTest().watermark).toBe(100);

    // Pre-filter: 400 - 100 = 300 > 256 → the recreate presents NO cursor.
    expect(reconnect()).toBe("/api/events");
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 400);
    await settle();
    expect(wire.seriesQueries).toHaveLength(1); // ZERO refetches (head == appliedHigh)

    expect(reconnect()).toBe("/api/events");
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 401);
    await settle();
    expect(wire.seriesQueries).toHaveLength(2); // exactly one transaction
  });

  it("an unevaluable pre-filter PASSES: one unknown counter still presents the cursor", async () => {
    await bootCommitted(5); // appliedHigh stays null: nothing was classified or applied
    expect(events._stateForTest().appliedHigh).toBeNull();

    expect(reconnect()).toBe("/api/events?last_id=5");
  });

  it("a cursor-less non-boot connect with BOTH counters unknown transacts", async () => {
    await bootCommitted(5, "boot-a");

    // A boot-change epoch resets both counters, its forced transaction
    // FAILS (leg 502) — abort clears nothing back in, so both stay unknown.
    wire.result = { ok: false, status: 502, error: "upstream down" };
    lastFakeES().fail();
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 0);
    await settle();
    expect(events._stateForTest().forceLatch).toBe(true);
    expect(events._stateForTest().watermark).toBeNull();

    // The latched retry commits; the fixture's point is that the epoch with
    // both counters unknown transacted rather than idling.
    wire.result = { ok: true, status: 200, data: [] };
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 0);
    await settle();
    expect(events._stateForTest().forceLatch).toBe(false);
    expect(wire.seriesQueries.length).toBeGreaterThanOrEqual(3);
  });
});

describe("abort, the latch, and the ladder", () => {
  it("a failed collection leg LATCHES: no cursor, the first epoch transacts unconditionally", async () => {
    wire.result = { ok: false, status: 502, error: "upstream down" };
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    expect(events._stateForTest().forceLatch).toBe(true);
    expect(events._stateForTest().watermark).toBeNull(); // no commit

    wire.result = { ok: true, status: 200, data: [] };
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    expect(lastFakeES().url).toBe("/api/events"); // no cursor on a latched client
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    // head == appliedHigh? irrelevant — the latch transacts unconditionally.
    expect(wire.seriesQueries).toHaveLength(2);
    expect(events._stateForTest().watermark).toBe(5);
    expect(events._stateForTest().forceLatch).toBe(false); // cleared on commit
  });

  it("a 429-refused leg latches after exactly ONE request", async () => {
    wire.result = { ok: false, status: 429, error: "recovery refused" };
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    expect(wire.seriesQueries).toHaveLength(1);
    expect(wire.moviesQueries).toHaveLength(1);
    expect(events._stateForTest().forceLatch).toBe(true);
  });

  it("a failed page leg aborts the transaction too", async () => {
    leg.results = [new Error("page leg failed (502)")];
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    expect(events._stateForTest().forceLatch).toBe(true);
    expect(events._stateForTest().watermark).toBeNull();
  });

  it("the persistent-502 ladder climbs to the 60s ceiling (attempts reset only on commit or a non-latched open)", async () => {
    wire.result = { ok: false, status: 502, error: "down" };
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    // Each retry opens LATCHED (no attempt reset), transacts, fails, aborts:
    // the attempt counter climbs monotonically.
    for (let i = 0; i < 6; i++) {
      vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
      lastFakeES().open();
      lastFakeES().epoch("boot-a", false, 5);
      await settle();
    }
    const attempts = events._stateForTest().reconnectAttempt;
    expect(attempts).toBeGreaterThanOrEqual(6);

    // The next rung is capped at the ceiling: nothing fires before it…
    const count = FakeEventSource.instances.length;
    vi.advanceTimersByTime(SSE_MAX_RECONNECT_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(count);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(count + 1);
  });

  it("latches COALESCE into one transaction", async () => {
    await bootCommitted(5);

    // An epoch-less down period (down latch)…
    lastFakeES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    lastFakeES().fail();
    await settle();
    expect(events._stateForTest().downPeriodLatch).toBe(true);

    // …then a healthy epoch whose own triggers are silent: ONE transaction.
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    expect(wire.seriesQueries).toHaveLength(2);
    expect(events._stateForTest().downPeriodLatch).toBe(false);

    // The 10/11 residual recovers ONCE: the next clean reconnect is silent.
    lastFakeES().fail();
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    expect(wire.seriesQueries).toHaveLength(2);
  });

  it("the collection leg is fetched without an abort signal (navigation never aborts it)", async () => {
    await bootCommitted(5);
    // The mock records only the query argument; the generated Raw signature
    // takes (query, opts) and the leg passes no opts — this pin is the
    // structural half; the JOIN fixtures pin the behavioral half.
    expect(wire.seriesQueries).toStrictEqual([undefined]);
  });
});

describe("boot-change application", () => {
  it("restart mid-transaction: held notify applies once from the old namespace, counters restart", async () => {
    wire.defer = true;
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    lastFakeES().frame("notify", { level: "info", text: "held toast" }, 9);
    expect(events._stateForTest().holdQueueLength).toBe(1);

    // The server restarts: the anchor dies mid-transaction → abort keeps the
    // queue; the recreate carries no cursor.
    lastFakeES().fail();
    await settle();
    expect(events._stateForTest().forceLatch).toBe(true);
    expect(events._stateForTest().holdQueueLength).toBe(1);
    wire.defer = false;

    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    expect(lastFakeES().url).toBe("/api/events");
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 2);
    await settle();

    // The toast showed exactly once; old-boot ids never touched the new
    // boot's counters; the forced transaction committed at the new head.
    expect(toasts.infos).toStrictEqual(["held toast"]);
    expect(events._stateForTest().watermark).toBe(2);
    expect(events._stateForTest().appliedHigh).toBeNull();
    expect(events._stateForTest().bootID).toBe("boot-b");

    // The next reconnect presents a cursor for the NEW boot.
    expect(reconnect()).toBe("/api/events?last_id=2");
  });

  it("restart mid-transaction: a held sync:done settles in the old namespace, THEN correlation clears", async () => {
    // The held settlement is non-reconstructible: a restart drops the
    // server's job registry, so this frame is the dialog's only delivery.
    wire.defer = true;
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    lastFakeES().frame(
      "sync:done",
      {
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
      },
      9,
    );
    expect(events._stateForTest().holdQueueLength).toBe(1);

    lastFakeES().fail(); // restart mid-transaction: abort keeps the queue
    await settle();
    wire.defer = false;

    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 2);
    await settle();

    // The dialog settled its job exactly once, and the correlation cleared
    // WITH the namespace — strictly AFTER the held settlement landed.
    expect(syncReg.settled).toStrictEqual([7]);
    expect(syncReg.clears).toBe(1);
    const iSettle = seq.log.indexOf("syncDone:7");
    const iClear = seq.log.indexOf("syncClear");
    expect(iSettle).toBeGreaterThanOrEqual(0);
    expect(iClear).toBeGreaterThan(iSettle);
    // Old-boot ids never touch the new boot's counters.
    expect(events._stateForTest().appliedHigh).toBeNull();
    expect(events._stateForTest().watermark).toBe(2);
  });

  it("the SAME numeric frame id across two boots applies both payloads once each", async () => {
    await bootCommitted(5, "boot-a");
    lastFakeES().frame("notify", { level: "info", text: "boot A payload" }, 7);
    expect(toasts.infos).toStrictEqual(["boot A payload"]);

    // Restart (no transaction running): the dedupe namespace resets with the
    // boot, so id 7 from boot B is a different key.
    lastFakeES().fail();
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 5);
    await settle();
    lastFakeES().frame("notify", { level: "info", text: "boot B payload" }, 7);

    expect(toasts.infos).toStrictEqual(["boot A payload", "boot B payload"]);
  });

  it("a held old-boot scan:done is SKIPPED (state half) while a new-boot scan:start applies", async () => {
    wire.defer = true;
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();
    const pollsMidTransaction = status.polls;
    lastFakeES().frame("scan:done", { action: "scan", detail: "", source: "scheduled" }, 9);
    expect(events._stateForTest().holdQueueLength).toBe(1);

    lastFakeES().fail(); // restart mid-transaction
    await settle();
    wire.defer = false;
    vi.mocked(emit).mockClear();

    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 1);
    await settle();
    lastFakeES().frame("scan:start", { action: "scan", detail: "Fresh", source: "scheduled" }, 2);

    // The old scan:done's state half never ran: the only pollStatus calls
    // are the two transactions' own status legs.
    expect(status.polls).toBe(pollsMidTransaction + 1);
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
    // The new boot's frames apply normally.
    expect(toasts.infos).toStrictEqual(["Scan started: Fresh"]);
    expect(events._stateForTest().appliedHigh).toBe(2);
  });

  it("registeredCollections SURVIVES a boot change: the recovery leg includes the committed pair", async () => {
    leg.route = "history"; // no route requirement — registration alone decides
    cov.registered = new Set(["series", "movies"]);
    await bootCommitted(5, "boot-a");
    expect(wire.seriesQueries).toHaveLength(1);

    lastFakeES().fail();
    vi.advanceTimersByTime(10 * SSE_MAX_RECONNECT_MS);
    lastFakeES().open();
    lastFakeES().epoch("boot-b", false, 1);
    await settle();

    // Counters reset with the boot; the tab-state registration did not.
    expect(wire.seriesQueries).toHaveLength(2);
    expect(wire.seriesQueries[1]).toStrictEqual({ recovery: 1 });
  });

  it("an EMPTY collection leg (no registration, non-library route) fetches nothing", async () => {
    leg.route = "history";
    await bootCommitted(5);

    expect(wire.seriesQueries).toHaveLength(0);
    expect(wire.moviesQueries).toHaveLength(0);
    expect(subsumeDirtyRoots).not.toHaveBeenCalled(); // nothing landed fresh
  });

  it("a committing covered transaction subsumes the dirty set", async () => {
    await bootCommitted(5); // library route: the leg covered the pair
    expect(subsumeDirtyRoots).toHaveBeenCalledTimes(1);
  });

  it("the leg supersedes the in-flight plain pair fetch before applying", async () => {
    await bootCommitted(5);
    expect(cov.aborts).toBe(1);
    expect(cov.applied).toHaveLength(1);
  });
});

describe("abort revocation", () => {
  it("an aborted transaction's still-in-flight collection leg lands as a NO-OP", async () => {
    wire.defer = true;
    leg.results = [new Error("page leg failed (502)")];
    events.connect();
    lastFakeES().open();
    lastFakeES().epoch("boot-a", false, 5);
    await settle();

    // The page leg already rejected → abort → latch; the pair is still in
    // flight. Its landing must apply nothing (license revoked).
    expect(events._stateForTest().forceLatch).toBe(true);
    for (const r of wire.pending.splice(0)) {
      r({ ok: true, status: 200, data: [{ stale: true }] });
    }
    await settle();

    expect(cov.applied).toHaveLength(0);
    expect(events._stateForTest().watermark).toBeNull();
  });
});
