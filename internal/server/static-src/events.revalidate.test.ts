// events.revalidate.test.ts — THE REVALIDATE BODY against mocked legs: what
// each digest answer runs, the must_refetch binding order, the full body on a
// cleared map, the restart bookkeeping, the signal every leg carries, and a
// failed leg's effect on the next digest. The coverage modules are real in
// events.integration.test.ts; here every seam is scripted so each fixture
// controls one variable.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type * as BusModule from "./bus.js";
import { EPOCH_A, EPOCH_B, fakeSSE, type FakeSSE } from "./events-fakes.js";

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

// One shared sequence log: the deterministic-order fixtures read it.
const seq = vi.hoisted(() => ({ log: [] as string[] }));

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));

// The sync settlement registry: the restart clear and the jobs leg land in
// the shared sequence log.
const syncReg = vi.hoisted(() => ({
  clears: 0,
  reattaches: [] as (AbortSignal | undefined)[],
}));
vi.mock("./sync-jobs.js", () => ({
  syncDoneFromEvent: vi.fn(),
  clearSyncCorrelation: () => {
    syncReg.clears += 1;
    seq.log.push("syncClear");
  },
  reattachSyncWatches: (signal?: AbortSignal) => {
    syncReg.reattaches.push(signal);
    seq.log.push("jobsLeg");
    return Promise.resolve();
  },
}));

vi.mock("./coverage-heal.js", () => ({
  healFromCoverageEvent: vi.fn(),
  resetCoverageHeal: vi.fn(),
  subsumeDirtyRoots: vi.fn(),
}));
vi.mock("./history.js", () => ({ noteHistoryMutation: vi.fn() }));

const status = vi.hoisted(() => ({ signals: [] as (AbortSignal | undefined)[] }));
vi.mock("./status.js", () => ({
  pollStatus: (signal?: AbortSignal) => {
    status.signals.push(signal);
    seq.log.push("pollStatus");
    return Promise.resolve();
  },
  abortPoll: vi.fn(),
  setStatusDegraded: vi.fn(),
  setStatusAttached: vi.fn(),
  applyActivityEvent: vi.fn(),
  applyAlertEvent: vi.fn(),
  applyProviderEvent: vi.fn(),
}));

vi.mock("@cplieger/actions", () => ({ registerCleanup: vi.fn() }));

// The download-tracking seam: search.ts's tracked in-flight downloads outlive
// the process that owned their activity ids, so a restart has to tell it.
const dl = vi.hoisted(() => ({ arms: 0 }));
vi.mock("./search.js", () => ({
  noteServerRestart: () => {
    dl.arms += 1;
    seq.log.push("armDownloadSweep");
  },
}));

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
}));
vi.mock("./coverage-store.js", () => ({
  beginCoveredPairWrite: vi.fn(() => vi.fn()),
  registeredCollections: () => cov.registered,
  setCollectionLegJoin: vi.fn(),
  releaseCoverageTombstones: vi.fn(),
}));

// The page leg: scripted outcomes; records each dispatch's recovery flag and
// signal. routeSubject mirrors the real dispatcher's route→subject table.
const leg = vi.hoisted(() => ({
  route: "library",
  results: [] as (string | Error | Promise<string>)[],
  calls: [] as { recovery: boolean; signal: AbortSignal | undefined }[],
}));
vi.mock("./page-leg.js", () => ({
  currentRouteKey: () => leg.route,
  routeSubject: (key: string) => {
    if (key === "history") {
      return { kind: "history", ref: "" };
    }
    const m = /^(series|movie):(\d+)$/.exec(key);
    if (!m) {
      return null;
    }
    return { kind: "detail", ref: `${m[1] === "series" ? "tvdb" : "tmdb"}-${m[2] ?? ""}` };
  },
  dispatchTransactionPageLeg: (recovery: boolean, signal?: AbortSignal) => {
    leg.calls.push({ recovery, signal });
    seq.log.push("pageLeg");
    const next = leg.results.shift() ?? "applied";
    return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
  },
}));

// The collection pair at the network edge: per-call scripted results,
// deferrable; queries, signals and the map's epoch at call time recorded.
interface RawResult {
  ok: boolean;
  status: number;
  data?: unknown;
  error?: string;
}
interface PairCall {
  query: unknown;
  signal: AbortSignal | undefined;
  mapEpoch: string | null;
}
const wire = vi.hoisted(() => ({
  series: [] as PairCall[],
  movies: [] as PairCall[],
  result: { ok: true, status: 200, data: [] as unknown } as RawResult,
  defer: false,
  pending: [] as ((r: RawResult) => void)[],
  mapEpoch: (): string | null => null,
}));
function pairCall(kind: "series" | "movies", q: unknown, signal?: AbortSignal): Promise<RawResult> {
  (kind === "series" ? wire.series : wire.movies).push({
    query: q,
    signal,
    mapEpoch: wire.mapEpoch(),
  });
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
  PATH_EVENTS_SYNC: "/api/events/sync",
  PATH_EVENTS_ALIVE: "/api/events/alive",
  coverageSeriesRaw: (q?: unknown, opts?: { signal?: AbortSignal }) =>
    pairCall("series", q, opts?.signal),
  coverageMoviesRaw: (q?: unknown, opts?: { signal?: AbortSignal }) =>
    pairCall("movies", q, opts?.signal),
}));

import { subsumeDirtyRoots } from "./coverage-heal.js";
import { _resetSubjectsForTest, versionMap } from "./subjects.js";

const events = await import("./events.js");
wire.mapEpoch = () => versionMap().epoch();

/** Flush microtasks + due timers so a hello's revalidate fully settles. One
 *  async tick drains a bounded number of promise hops, and the stream reader,
 *  the digest POST and the legs chain more than that. */
async function settle(): Promise<void> {
  for (let i = 0; i < 6; i++) {
    await vi.advanceTimersByTimeAsync(0);
  }
}

/** A loader landed `kind:ref` at `version` under `epoch` before the stream
 *  connected (the transport's observe, seen from this module's side). */
function seed(kind: string, ref: string, version: string, epoch = EPOCH_A): void {
  versionMap().observe({ kind, ref }, version, epoch);
}

/** Connect and answer with a hello, then let the revalidate it schedules run. */
async function connectHello(
  epoch = EPOCH_A,
  opts: { head?: number; resumed?: boolean } = {},
): Promise<void> {
  events.connect();
  await settle();
  server.current?.last().hello(epoch, opts);
  await settle();
}

/** Drop the live stream and let the ladder reconnect; answers the fresh
 *  connection with `hello`. */
async function reconnectWith(
  epoch: string,
  opts: { head?: number; resumed?: boolean },
): Promise<void> {
  server.current?.last().end();
  await vi.advanceTimersByTimeAsync(2_000);
  server.current?.last().hello(epoch, opts);
  await settle();
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0);
  server.current = fakeSSE();
  seq.log = [];
  // The browser project runs in real Chromium, where SharedWorker exists;
  // these suites pin the per-tab stream, so the worker branch is closed.
  vi.stubGlobal("SharedWorker", undefined);
  syncReg.clears = 0;
  syncReg.reattaches = [];
  status.signals = [];
  dl.arms = 0;
  cov.registered = new Set<string>();
  cov.applied = [];
  cov.aborts = 0;
  leg.route = "library";
  leg.results = [];
  leg.calls = [];
  wire.series = [];
  wire.movies = [];
  wire.result = { ok: true, status: 200, data: [] };
  wire.defer = false;
  wire.pending = [];
});

afterEach(() => {
  events._resetEventsForTest();
  _resetSubjectsForTest();
  server.current = null;
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("the boot hello", () => {
  it("a fresh page asks an empty digest and runs no leg", async () => {
    await connectHello();

    expect(server.current!.digestCalls).toHaveLength(1);
    expect(server.current!.digestCalls[0]?.subjects).toStrictEqual([]);
    expect(wire.series).toHaveLength(0);
    expect(leg.calls).toHaveLength(0);
    expect(status.signals).toHaveLength(0);
    expect(versionMap().epoch()).toBe(EPOCH_A); // bound by the hello
    expect(events._stateForTest().knownEpoch).toBe(EPOCH_A);
  });

  it("presents the held vector with its epoch", async () => {
    seed("series", "", "3");
    seed("activity", "", "1");
    await connectHello();

    expect(server.current!.digestCalls[0]).toMatchObject({
      epoch: EPOCH_A,
      subjects: [
        { kind: "series", ref: "", version: "3" },
        { kind: "activity", ref: "", version: "1" },
      ],
    });
  });

  it("a resumed hello asks nothing", async () => {
    seed("series", "", "3");
    await connectHello(EPOCH_A, { head: 3, resumed: true });

    expect(server.current!.digestCalls).toHaveLength(0);
  });
});

describe("what a digest's changed subjects run", () => {
  it("series runs exactly one pair fetch with ?recovery=1 and nothing else", async () => {
    seed("series", "", "1");
    server.current!.digestScript.push({ changed: [{ kind: "series", ref: "", version: "2" }] });
    await connectHello();

    expect(wire.series).toHaveLength(1);
    expect(wire.movies).toHaveLength(1);
    expect(wire.series[0]?.query).toStrictEqual({ recovery: 1 });
    expect(cov.applied).toHaveLength(1);
    expect(cov.aborts).toBe(1); // the leg supersedes the plain pair fetch
    expect(leg.calls).toHaveLength(0);
    expect(status.signals).toHaveLength(0);
    expect(syncReg.reattaches).toHaveLength(0);
  });

  it("series and movies together still fetch the pair once", async () => {
    seed("series", "", "1");
    seed("movies", "", "1");
    server.current!.digestScript.push({
      changed: [
        { kind: "series", ref: "", version: "2" },
        { kind: "movies", ref: "", version: "2" },
      ],
    });
    await connectHello();

    expect(wire.series).toHaveLength(1);
    expect(wire.movies).toHaveLength(1);
  });

  it("the pair leg is EMPTY off the library with nothing registered", async () => {
    leg.route = "history";
    seed("series", "", "1");
    server.current!.digestScript.push({ changed: [{ kind: "series", ref: "", version: "2" }] });
    await connectHello();

    expect(wire.series).toHaveLength(0);
    expect(subsumeDirtyRoots).not.toHaveBeenCalled(); // nothing landed fresh
  });

  it("a registered pair is refetched off the library route too", async () => {
    leg.route = "history";
    cov.registered = new Set(["series", "movies"]);
    seed("series", "", "1");
    server.current!.digestScript.push({ changed: [{ kind: "series", ref: "", version: "2" }] });
    await connectHello();

    expect(wire.series).toHaveLength(1);
    expect(subsumeDirtyRoots).toHaveBeenCalledTimes(1);
  });

  it("activity runs one status poll and no pair fetch", async () => {
    seed("activity", "", "1");
    server.current!.digestScript.push({
      changed: [{ kind: "activity", ref: "", version: "2" }],
    });
    await connectHello();

    expect(status.signals).toHaveLength(1);
    expect(wire.series).toHaveLength(0);
    expect(leg.calls).toHaveLength(0);
  });

  it("alerts and providers share the status leg: three subjects, one poll", async () => {
    seed("activity", "", "1");
    seed("alerts", "", "1");
    seed("providers", "", "1");
    server.current!.digestScript.push({
      changed: [
        { kind: "activity", ref: "", version: "2" },
        { kind: "alerts", ref: "", version: "2" },
        { kind: "providers", ref: "", version: "2" },
      ],
    });
    await connectHello();

    expect(status.signals).toHaveLength(1);
  });

  it("the open detail's root runs the page leg with recovery", async () => {
    leg.route = "series:7";
    seed("detail", "tvdb-7", "1");
    server.current!.digestScript.push({
      changed: [{ kind: "detail", ref: "tvdb-7", version: "2" }],
    });
    await connectHello();

    expect(leg.calls).toHaveLength(1);
    expect(leg.calls[0]?.recovery).toBe(true);
    expect(wire.series).toHaveLength(0);
    expect(versionMap().has({ kind: "detail", ref: "tvdb-7" })).toBe(true);
  });

  it("a detail root that is not open runs nothing and is forgotten", async () => {
    leg.route = "library";
    seed("detail", "tvdb-7", "1");
    server.current!.digestScript.push({
      changed: [{ kind: "detail", ref: "tvdb-7", version: "2" }],
    });
    await connectHello();

    expect(leg.calls).toHaveLength(0);
    expect(versionMap().has({ kind: "detail", ref: "tvdb-7" })).toBe(false);
  });

  it("history runs the page leg on /history and is forgotten elsewhere", async () => {
    leg.route = "history";
    seed("history", "", "1");
    server.current!.digestScript.push({ changed: [{ kind: "history", ref: "", version: "2" }] });
    await connectHello();
    expect(leg.calls).toHaveLength(1);

    events._resetEventsForTest();
    _resetSubjectsForTest();
    server.current = fakeSSE();
    leg.calls = [];
    leg.route = "library";
    seed("history", "", "1");
    server.current.digestScript.push({ changed: [{ kind: "history", ref: "", version: "2" }] });
    await connectHello();

    expect(leg.calls).toHaveLength(0);
    expect(versionMap().has({ kind: "history", ref: "" })).toBe(false);
  });

  it("jobs re-attaches the sync watches", async () => {
    seed("jobs", "", "1");
    server.current!.digestScript.push({ changed: [{ kind: "jobs", ref: "", version: "2" }] });
    await connectHello();

    expect(syncReg.reattaches).toHaveLength(1);
    expect(syncReg.reattaches[0]).toBeInstanceOf(AbortSignal);
  });

  it("a removed subject is forgotten", async () => {
    leg.route = "series:7";
    seed("detail", "tvdb-7", "1");
    server.current!.digestScript.push({
      removed: [{ kind: "detail", ref: "tvdb-7", reason: "gone" }],
    });
    await connectHello();

    expect(versionMap().has({ kind: "detail", ref: "tvdb-7" })).toBe(false);
    expect(leg.calls).toHaveLength(0);
  });

  it("an unchanged vector runs nothing", async () => {
    seed("series", "", "1");
    seed("activity", "", "1");
    await connectHello();

    expect(wire.series).toHaveLength(0);
    expect(status.signals).toHaveLength(0);
    expect(leg.calls).toHaveLength(0);
  });
});

describe("must_refetch and the full body", () => {
  it("binds the map to the digest's epoch BEFORE the first loader runs, then runs every leg once", async () => {
    seed("series", "", "1");
    server.current!.digestScript.push({ must_refetch: true, epoch: EPOCH_B });
    await connectHello(EPOCH_A);

    // Every leg ran, once each, and the pair fetch saw the NEW epoch.
    expect(wire.series).toHaveLength(1);
    expect(wire.series[0]?.mapEpoch).toBe(EPOCH_B);
    expect(leg.calls).toHaveLength(1);
    expect(status.signals).toHaveLength(1);
    expect(syncReg.reattaches).toHaveLength(1);
    expect(versionMap().epoch()).toBe(EPOCH_B);
    expect(events._stateForTest().knownEpoch).toBe(EPOCH_B);
    // The old version was dropped by the bind: the loaders refill the map.
    expect(versionMap().has({ kind: "series", ref: "" })).toBe(false);
  });

  it("a must_refetch naming a NEW epoch is a restart: correlation clears and the sweep arms, once", async () => {
    seed("series", "", "1");
    server.current!.digestScript.push({ must_refetch: true, epoch: EPOCH_B });
    await connectHello(EPOCH_A);

    expect(syncReg.clears).toBe(1);
    expect(dl.arms).toBe(1);
    // The bookkeeping precedes every leg.
    const iClear = seq.log.indexOf("syncClear");
    expect(iClear).toBeGreaterThanOrEqual(0);
    expect(seq.log.indexOf("fetch:series")).toBeGreaterThan(iClear);
    expect(seq.log.indexOf("pollStatus")).toBeGreaterThan(iClear);
  });

  it("a must_refetch at the SAME epoch (a subject the server refused) is not a restart", async () => {
    seed("series", "", "1");
    server.current!.digestScript.push({ must_refetch: true, epoch: EPOCH_A });
    await connectHello(EPOCH_A);

    expect(wire.series).toHaveLength(1); // the full body still ran
    expect(syncReg.clears).toBe(0);
    expect(dl.arms).toBe(0);
  });

  it("a hello from a new epoch over a held map runs the full body WITHOUT a digest", async () => {
    seed("series", "", "1");
    seed("activity", "", "1");
    await connectHello(EPOCH_B);

    expect(server.current!.digestCalls).toHaveLength(0);
    expect(wire.series).toHaveLength(1);
    expect(leg.calls).toHaveLength(1);
    expect(status.signals).toHaveLength(1);
    expect(versionMap().epoch()).toBe(EPOCH_B);
    // The map held another process's versions: a restart, once.
    expect(syncReg.clears).toBe(1);
    expect(dl.arms).toBe(1);
  });

  it("the full body runs as ONE transaction: the pair leg supersedes the plain fetch and subsumes the dirty set", async () => {
    seed("series", "", "1");
    await connectHello(EPOCH_B);

    expect(cov.aborts).toBe(1);
    expect(cov.applied).toHaveLength(1);
    expect(subsumeDirtyRoots).toHaveBeenCalledTimes(1);
  });
});

describe("the download restart belt", () => {
  it("a reconnect under a new epoch arms the sweep and clears the sync correlation, once", async () => {
    seed("activity", "", "1");
    await connectHello(EPOCH_A);
    expect(dl.arms).toBe(0);

    // The held entry makes the new hello's bind drop something, so BOTH the
    // hello and the full revalidate it schedules announce the restart.
    await reconnectWith(EPOCH_B, {});

    expect(dl.arms).toBe(1);
    expect(syncReg.clears).toBe(1);
  });

  it("arms the sweep BEFORE the transaction's authoritative status read", async () => {
    // The poll's render pass is what consumes the sweep, so an arm that landed
    // after it would wait for an unrelated status change to fire.
    seed("activity", "", "1");
    await connectHello(EPOCH_A);
    await reconnectWith(EPOCH_B, {});

    const iArm = seq.log.indexOf("armDownloadSweep");
    expect(iArm).toBeGreaterThanOrEqual(0);
    expect(seq.log.lastIndexOf("pollStatus")).toBeGreaterThan(iArm);
  });

  it("boot itself does not arm the sweep", async () => {
    // Nothing can be tracked before the first hello of a page load.
    seed("activity", "", "1");
    await connectHello(EPOCH_A);

    expect(dl.arms).toBe(0);
  });

  it("a SAME-epoch reconnect does not arm the sweep", async () => {
    // The process lived, so every tracked activity id is still valid.
    seed("activity", "", "1");
    await connectHello(EPOCH_A);

    await reconnectWith(EPOCH_A, { head: 9, resumed: true });

    expect(dl.arms).toBe(0);
    expect(syncReg.clears).toBe(0);
  });
});

describe("the run's signal", () => {
  it("every leg carries ctx.signal, and stopping the stream mid-body aborts them", async () => {
    seed("series", "", "1");
    seed("activity", "", "1");
    seed("jobs", "", "1");
    leg.route = "series:7";
    cov.registered = new Set(["series", "movies"]); // the pair leg is non-empty off the library
    seed("detail", "tvdb-7", "1");
    server.current!.digestScript.push({
      changed: [
        { kind: "series", ref: "", version: "2" },
        { kind: "activity", ref: "", version: "2" },
        { kind: "jobs", ref: "", version: "2" },
        { kind: "detail", ref: "tvdb-7", version: "2" },
      ],
    });
    wire.defer = true;
    leg.results = [
      new Promise<string>(() => {
        /* held open */
      }),
    ];
    await connectHello();

    const signals = [
      wire.series[0]?.signal,
      wire.movies[0]?.signal,
      status.signals[0],
      syncReg.reattaches[0],
      leg.calls[0]?.signal,
    ];
    expect(signals.map((s) => (s instanceof AbortSignal ? s.aborted : "missing"))).toStrictEqual([
      false,
      false,
      false,
      false,
      false,
    ]);
    expect(new Set(signals).size).toBe(1); // one run, one signal

    events._resetEventsForTest(); // stop(): aborts the in-flight run

    expect(signals.every((s) => s?.aborted)).toBe(true);
  });
});

describe("a failed leg", () => {
  it("leaves the map at the old version, so the reconnect's digest names the subject again", async () => {
    seed("series", "", "1");
    server.current!.digestScript.push({ changed: [{ kind: "series", ref: "", version: "2" }] });
    wire.result = { ok: false, status: 502, error: "upstream down" };
    await connectHello(EPOCH_A);
    expect(wire.series).toHaveLength(1);
    expect(cov.applied).toHaveLength(0);
    expect(versionMap().has({ kind: "series", ref: "" })).toBe(true);

    // The library ended the connection (revalidate_failed) and reconnects
    // unverified: even a resumed hello runs the digest again.
    wire.result = { ok: true, status: 200, data: [] };
    server.current!.digestScript.push({ changed: [{ kind: "series", ref: "", version: "2" }] });
    await vi.advanceTimersByTimeAsync(2_000);
    server.current!.last().hello(EPOCH_A, { head: 0, resumed: true });
    await settle();

    expect(server.current!.digestCalls).toHaveLength(2);
    expect(server.current!.digestCalls[1]?.subjects).toStrictEqual([
      { kind: "series", ref: "", version: "1" },
    ]);
    expect(wire.series).toHaveLength(2);
    expect(cov.applied).toHaveLength(1);
  });
});
