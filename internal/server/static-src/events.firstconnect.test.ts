// What the FIRST SSE connection of a page load must not do.
//
// `everConnected` is module state that is never reset, so the first-open case
// exists exactly once per module instance. events.test.ts imports events.js
// once and shares that instance across its whole file, where earlier tests
// have already opened a connection — so this one behaviour needs its own file
// to be observed at all.
import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import type * as BusModule from "./bus.js";

vi.mock("./store.js", () => ({ get: vi.fn(), set: vi.fn() }));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./coverage.js", () => ({
  patchCoverageBadge: vi.fn(),
  fetchAndMergeCoverage: vi.fn(),
}));
vi.mock("./status.js", () => ({ pollStatus: vi.fn(), abortPoll: vi.fn() }));
vi.mock("@cplieger/actions", () => ({ registerCleanup: vi.fn() }));
vi.mock("./bus.js", async (importOriginal) => ({
  ...(await importOriginal<typeof BusModule>()),
  emit: vi.fn(),
}));

import { pollStatus } from "./status.js";
import { registerCleanup } from "@cplieger/actions";
import { emit, BusEvent } from "./bus.js";

/** Enough EventSource for connect(): it records instances and can dispatch the
 *  `open` frame the module listens for. */
class FakeEventSource {
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readyState = 0;
  closed = false;
  private readonly listeners = new Map<string, Set<(e: MessageEvent) => void>>();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: (e: MessageEvent) => void): void {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(fn);
  }

  close(): void {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }

  open(): void {
    this.readyState = 1;
    for (const fn of this.listeners.get("open") ?? []) {
      fn(new MessageEvent("open", { data: "" }));
    }
  }
}

const events = await import("./events.js");

function lastES(): FakeEventSource {
  const es = FakeEventSource.instances.at(-1);
  if (!es) {
    throw new Error("no EventSource instance");
  }
  return es;
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.useFakeTimers();
});

afterEach(() => {
  (vi.mocked(registerCleanup).mock.calls.at(-1)?.[0] as (() => void) | undefined)?.();
  FakeEventSource.instances = [];
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("events: the first connection of a page load", () => {
  it("does not refetch the pull state", () => {
    events.connect();

    lastES().open();

    // The first open has no gap behind it: the page just loaded its data
    // through the normal render path, so a refetch here is pure duplication.
    // (One test, two assertions, deliberately: `everConnected` latches on the
    // first open and is never reset, so this module instance can only be asked
    // the question once.)
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
    expect(pollStatus).not.toHaveBeenCalled();
  });
});
