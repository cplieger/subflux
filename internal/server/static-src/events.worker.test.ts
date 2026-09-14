// events.worker.test.ts — the connection's two topologies as this module
// wires them: the branch on SharedWorker's presence, the fixed constructor
// options, what a tab does with the messages a worker host sends it (frames,
// lifecycle records, revalidate runs), and the logout seam in both modes.
// The port protocol itself (heartbeats, expiry, the fold) is the library's
// and is tested there; here the worker is a stub port the test speaks for.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TabToWorker, WorkerToTab } from "@cplieger/sse";
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
import { clearSyncCorrelation } from "./sync-jobs.js";
import { setStatusAttached, setStatusDegraded } from "./status.js";
import { noteServerRestart } from "./search.js";
import { handleSessionExpiry } from "./api-client.js";
import { _resetSubjectsForTest, versionMap } from "./subjects.js";

const events = await import("./events.js");

/** What the page constructed: the URL and options, and the worker's end of
 *  the port so the test can speak as the host. */
interface Spawned {
  readonly url: string;
  readonly options: WorkerOptions | undefined;
  readonly host: MessagePort;
  readonly received: TabToWorker[];
}

const spawned: Spawned[] = [];

/** A SharedWorker stand-in: a MessageChannel whose far end the test holds. */
class StubSharedWorker {
  readonly port: MessagePort;
  constructor(url: string | URL, options?: WorkerOptions) {
    const channel = new MessageChannel();
    this.port = channel.port1;
    const received: TabToWorker[] = [];
    channel.port2.onmessage = (ev: MessageEvent) => {
      received.push(ev.data as TabToWorker);
    };
    spawned.push({ url: String(url), options, host: channel.port2, received });
  }
  addEventListener(): void {
    /* the stub never errors */
  }
}

/** Lets every message already posted on a MessageChannel land: a probe
 *  message on a fresh channel is ordered after them. */
async function messageTick(): Promise<void> {
  for (let i = 0; i < 4; i++) {
    await new Promise<void>((resolve) => {
      const probe = new MessageChannel();
      probe.port1.onmessage = () => {
        probe.port1.close();
        resolve();
      };
      probe.port2.postMessage(null);
    });
  }
}

function host(): Spawned {
  const s = spawned.at(-1);
  if (!s) {
    throw new Error("no worker spawned");
  }
  return s;
}

function fromHost(message: WorkerToTab): void {
  host().host.postMessage(message);
}

/** Connect in worker mode and answer the tab's attach with one heartbeat. */
async function attach(): Promise<void> {
  events.connect();
  await messageTick();
  fromHost({ type: "heartbeat", seq: 1 });
  await messageTick();
}

/** The host forwarding its stream's hello under `epoch`. */
function hello(epoch: string): WorkerToTab {
  return {
    type: "lifecycle",
    event: {
      kind: "hello",
      verdict: "fresh",
      wire: 1,
      resumed: false,
      epoch,
      floor: "0",
      head: "0",
    },
  };
}

beforeEach(() => {
  server.current = fakeSSE();
  spawned.length = 0;
  vi.stubGlobal("SharedWorker", StubSharedWorker);
  localStorage.removeItem("subflux.sse_client");
});

afterEach(() => {
  events._resetEventsForTest();
  _resetSubjectsForTest();
  for (const s of spawned) {
    s.host.close();
  }
  server.current = null;
});

describe("events: which topology connect() picks", () => {
  it("without SharedWorker the tab streams on its own: one fetch to /api/events", async () => {
    vi.stubGlobal("SharedWorker", undefined);
    events.connect();
    await messageTick();

    expect(spawned).toHaveLength(0);
    expect(server.current!.connections).toHaveLength(1);
    expect(events._stateForTest().mode).toBe("fallback");
  });

  it("with SharedWorker the tab attaches to the profile's worker and fetches no stream", async () => {
    await attach();

    expect(events._stateForTest().mode).toBe("worker");
    expect(server.current!.connections).toHaveLength(0);
    expect(host().received[0]).toMatchObject({ type: "attach", tag: expect.any(String) });
  });

  it("constructs the worker by the bundle's hashed URL, named after it, classic, same-origin", async () => {
    await attach();

    expect(host().url).toBe("/chunks/sse-worker-test.js");
    expect(host().options).toStrictEqual({
      name: "sse-worker-test.js",
      type: "classic",
      credentials: "same-origin",
    });
  });

  it("presents the persisted client tag to the worker, the same one the digest sends", async () => {
    await attach();
    const attachMsg = host().received[0] as Extract<TabToWorker, { type: "attach" }>;
    expect(attachMsg.tag).toMatch(/^[0-9a-f]{16}$/);
    expect(localStorage.getItem("subflux.sse_client")).toBe(attachMsg.tag);

    fromHost({
      type: "revalidate_run",
      runId: 1,
      ctx: { cause: "visible", epoch: EPOCH_A, generation: 1, full: false },
    });
    await messageTick();
    await messageTick();
    expect(server.current!.digestCalls[0]?.headers.get("SSE-Client")).toBe(attachMsg.tag);
  });
});

describe("events: what a tab does with the worker's messages", () => {
  it("a frame from the worker reaches the replay table", async () => {
    await attach();

    fromHost({
      type: "frame",
      frame: {
        type: "notify",
        data: JSON.stringify({ data: { level: "info", text: "hi" } }),
        id: null,
      },
      generation: 1,
    });
    await messageTick();

    expect(notify.info).toHaveBeenCalledWith("hi");
  });

  it("a state change from the worker drives the degraded poll with the transition", async () => {
    await attach();

    fromHost({
      type: "lifecycle",
      event: { kind: "state", from: "open", to: "backoff", generation: 2 },
    });
    await messageTick();

    expect(setStatusDegraded).toHaveBeenLastCalledWith(
      expect.objectContaining({ from: "open", to: "backoff" }),
    );
  });

  it("the attach record names the stream's state, and a tab joining a degraded stream polls from it", async () => {
    await attach();

    fromHost({
      type: "lifecycle",
      event: { kind: "tab_attached", replaced: false, state: "backoff" },
    });
    await messageTick();

    expect(setStatusAttached).toHaveBeenLastCalledWith("backoff");
    expect(setStatusDegraded).not.toHaveBeenCalled();
  });

  it("the worker's hello binds this tab's map to the epoch (the host binds only its own)", async () => {
    await attach();
    expect(versionMap().epoch()).toBeNull();

    fromHost(hello(EPOCH_A));
    await messageTick();

    expect(versionMap().epoch()).toBe(EPOCH_A);
    expect(clearSyncCorrelation).not.toHaveBeenCalled();
  });

  it("a hello from a new epoch over a held map is a restart, once", async () => {
    await attach();
    fromHost(hello(EPOCH_A));
    await messageTick();
    versionMap().observe({ kind: "activity", ref: "" }, "3", EPOCH_A);

    fromHost(hello(EPOCH_B));
    await messageTick();
    // The full run the host fans out after the hello names the same epoch.
    fromHost({
      type: "revalidate_run",
      runId: 2,
      ctx: { cause: "hello", epoch: EPOCH_B, generation: 2, full: true },
    });
    await messageTick();
    await messageTick();

    expect(versionMap().epoch()).toBe(EPOCH_B);
    expect(versionMap().has({ kind: "activity", ref: "" })).toBe(false);
    expect(clearSyncCorrelation).toHaveBeenCalledTimes(1);
    expect(noteServerRestart).toHaveBeenCalledTimes(1);
  });

  it("a revalidate run from the worker digests what THIS tab holds and reports done", async () => {
    await attach();
    fromHost(hello(EPOCH_A));
    await messageTick();
    versionMap().observe({ kind: "activity", ref: "" }, "3", EPOCH_A);

    fromHost({
      type: "revalidate_run",
      runId: 7,
      ctx: { cause: "visible", epoch: EPOCH_A, generation: 1, full: false },
    });
    await messageTick();
    await messageTick();

    expect(server.current!.digestCalls).toHaveLength(1);
    expect(server.current!.digestCalls[0]?.subjects).toStrictEqual([
      { kind: "activity", ref: "", version: "3" },
    ]);
    expect(host().received).toContainEqual({ type: "revalidate_done", runId: 7 });
  });

  it("auth_lost from the worker redirects like a 401 on any call", async () => {
    await attach();

    fromHost({ type: "lifecycle", event: { kind: "auth_lost", route: "digest", status: 401 } });
    await messageTick();

    expect(handleSessionExpiry).toHaveBeenCalledWith(401);
  });
});

describe("events: the logout seam", () => {
  it("in worker mode the tab leaves with the logout cause, which ends the profile's stream", async () => {
    await attach();

    events.disconnectForLogout();
    await messageTick();

    expect(host().received).toContainEqual({ type: "detach", cause: "logout" });
    expect(events._stateForTest().mode).toBeNull();
  });

  it("in fallback mode the tab's own stream ends", async () => {
    vi.stubGlobal("SharedWorker", undefined);
    events.connect();
    await messageTick();
    const conn = server.current!.last();

    events.disconnectForLogout();
    await messageTick();

    expect(conn.ended).toBe(true);
    expect(events._stateForTest().mode).toBeNull();
  });
});
