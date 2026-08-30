import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SSE_RECONNECT_MS, VISIBILITY_DEBOUNCE_MS } from "./constants.js";
// Type-only: erased at runtime, so the hoisted vi.mock factory may reference it.
import type * as BusModule from "./bus.js";

vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./coverage-heal.js", () => ({ healFromCoverageEvent: vi.fn() }));
vi.mock("./status.js", () => ({ pollStatus: vi.fn(), abortPoll: vi.fn() }));
vi.mock("@cplieger/actions", () => ({ registerCleanup: vi.fn() }));
vi.mock("./bus.js", async (importOriginal) => ({
  ...(await importOriginal<typeof BusModule>()),
  emit: vi.fn(),
}));

import * as notify from "./notify.js";
import { healFromCoverageEvent } from "./coverage-heal.js";
import { pollStatus, abortPoll } from "./status.js";
import { registerCleanup } from "@cplieger/actions";
import { emit, BusEvent } from "./bus.js";

// --- Fake EventSource ---
//
// The real module reads EventSource.CLOSED and constructs instances with the
// endpoint path; the fake records instances, exposes emit helpers, and mimics
// the readyState lifecycle the reconnect logic depends on.
class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  url: string;
  readyState = FakeEventSource.CONNECTING;
  closed = false;
  private listeners = new Map<string, Set<(e: MessageEvent) => void>>();

  constructor(url: string) {
    this.url = url;
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

  /** Dispatch a named SSE frame whose data is the JSON envelope
   *  {data: payload} the server publishes (events.ts decodes env.data). */
  frame(type: string, payload: unknown): void {
    this.dispatch(type, JSON.stringify({ data: payload }));
  }

  open(): void {
    this.readyState = FakeEventSource.OPEN;
    this.dispatch("open", "");
  }

  /** Server-side connection loss: browser EventSource flips to CLOSED
   *  before the error event when it will not retry on its own. */
  fail(): void {
    this.readyState = FakeEventSource.CLOSED;
    this.dispatch("error", "");
  }

  /** A transient error the browser retries on its own: it stays OPEN (or
   *  CONNECTING) and the module must not schedule a reconnect of its own. */
  errorWhileOpen(): void {
    this.readyState = FakeEventSource.OPEN;
    this.dispatch("error", "");
  }

  private dispatch(type: string, data: string): void {
    const e = new MessageEvent(type, { data });
    for (const fn of this.listeners.get(type) ?? new Set<(e: MessageEvent) => void>()) {
      fn(e);
    }
  }
}

// events.js is imported ONCE (its visibilitychange listener registers on the
// document at import time); tests share the module state and
// reset the connection between tests via the registerCleanup-captured
// disconnect. EventSource is stubbed per test (vitest.config has
// unstubGlobals: true, which clears module-scope stubs).
const events = await import("./events.js");

/** The disconnect function events.ts registered on its last connect(). */
function capturedDisconnect(): (() => void) | undefined {
  return vi.mocked(registerCleanup).mock.calls.at(-1)?.[0] as (() => void) | undefined;
}

function lastES(): FakeEventSource {
  const es = FakeEventSource.instances.at(-1);
  if (!es) {
    throw new Error("no EventSource instance");
  }
  return es;
}

function setHidden(hidden: boolean): void {
  Object.defineProperty(document, "hidden", { value: hidden, configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0); // deterministic backoff jitter
});

afterEach(() => {
  capturedDisconnect()?.();
  // Ensure a queued visibility reconnect can't leak into the next test.
  vi.runOnlyPendingTimers();
  capturedDisconnect()?.();
  FakeEventSource.instances = [];
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("events: SSE connection", () => {
  it("connect creates EventSource to /api/events", () => {
    events.connect();
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(lastES().url).toBe("/api/events");

    // Idempotent while a connection exists.
    events.connect();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("disconnect closes EventSource and clears timers", () => {
    events.connect();
    const es = lastES();

    capturedDisconnect()?.();

    expect(es.closed).toBe(true);
    expect(abortPoll).toHaveBeenCalled();

    // A pending reconnect timer would create a new instance; none may fire.
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("reconnects with exponential backoff on error", () => {
    events.connect();

    // Attempt 0: base delay = SSE_RECONNECT_MS (jitter pinned to 0).
    lastES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(2);

    // Attempt 1: doubled.
    lastES().fail();
    vi.advanceTimersByTime(2 * SSE_RECONNECT_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(3);
  });

  it("resets reconnect attempt counter on successful open", () => {
    events.connect();
    lastES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    lastES().fail();
    vi.advanceTimersByTime(2 * SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(3);

    // A successful open resets the attempt counter…
    lastES().open();

    // …so the next failure reconnects after the BASE delay again, not 4x.
    lastES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    expect(FakeEventSource.instances).toHaveLength(4);
  });

  it("disconnects on visibilitychange hidden", () => {
    events.connect();
    const es = lastES();

    setHidden(true);

    expect(es.closed).toBe(true);
    expect(abortPoll).toHaveBeenCalled();
  });

  it("reconnects with debounce on visibilitychange visible", () => {
    events.connect();
    setHidden(true);
    expect(lastES().closed).toBe(true);
    const count = FakeEventSource.instances.length;

    setHidden(false);
    vi.advanceTimersByTime(VISIBILITY_DEBOUNCE_MS - 1);
    expect(FakeEventSource.instances).toHaveLength(count);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(count + 1);
  });

  it("a reconnect after a gap refetches the pull state", () => {
    events.connect();
    // First open of this page load; a later open is a reconnect after a gap.
    lastES().open();
    lastES().fail();
    vi.advanceTimersByTime(SSE_RECONNECT_MS);
    // Clear only the assertion targets: clearing registerCleanup too would
    // lose the disconnect the afterEach hook needs.
    vi.mocked(emit).mockClear();
    vi.mocked(pollStatus).mockClear();

    lastES().open();

    // A fresh EventSource never replays the gap (no Last-Event-ID), so the
    // pull state is refetched rather than trusted.
    expect(emit).toHaveBeenCalledWith(BusEvent.DataInvalidate);
    expect(pollStatus).toHaveBeenCalled();
  });

  it("a transient error while the connection is open schedules no reconnect", () => {
    events.connect();
    lastES().open();

    // readyState stays OPEN: the browser retries this one itself.
    lastES().errorWhileOpen();
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);

    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("disconnect cancels a pending reconnect", () => {
    events.connect();
    lastES().fail(); // schedules the backoff reconnect

    capturedDisconnect()?.();
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);

    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("schedules no second reconnect while one is already pending", () => {
    events.connect();
    lastES().fail(); // schedules reconnect A

    // A connect() while A is still pending, and that connection drops too.
    events.connect();
    lastES().fail();

    // Only ONE timer handle is tracked, so a second scheduled reconnect would
    // be untracked — and would survive the disconnect that cancels the
    // tracked one, reconnecting a torn-down module.
    capturedDisconnect()?.();
    vi.advanceTimersByTime(10 * SSE_RECONNECT_MS);

    expect(FakeEventSource.instances).toHaveLength(2);
  });

  it("backoff jitter is additive, so the delay never falls below the base", () => {
    vi.spyOn(Math, "random").mockReturnValue(1); // maximum jitter
    events.connect();

    lastES().fail();
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

describe("events: SSE handlers", () => {
  it("coverage event dispatches its decoded payload to the heal coalescer", () => {
    events.connect();

    const payload = {
      media_type: "episode",
      media_id: "tvdb-81189-s01e01",
      language: "en",
      variant: "standard",
      source: "opensubtitles",
    };
    lastES().frame("coverage", payload);

    // The coalescer owns parse/gate/coalesce; the handler owns only decode +
    // dispatch, and no longer touches the full-collection refresh path.
    expect(healFromCoverageEvent).toHaveBeenCalledExactlyOnceWith(payload);
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("an undecodable coverage frame is dropped without a heal dispatch", () => {
    events.connect();

    lastES().frame("coverage", { media_type: 42 });

    expect(healFromCoverageEvent).not.toHaveBeenCalled();
    expect(emit).not.toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("notify event calls notify.error for error level", () => {
    events.connect();

    lastES().frame("notify", { level: "error", text: "provider down" });

    expect(notify.error).toHaveBeenCalledWith("provider down");
    expect(notify.info).not.toHaveBeenCalled();
  });

  it("notify event calls notify.info for info level", () => {
    events.connect();

    lastES().frame("notify", { level: "info", text: "fyi" });

    expect(notify.info).toHaveBeenCalledWith("fyi");
    expect(notify.error).not.toHaveBeenCalled();
  });

  it("scan:start shows info toast with label", () => {
    events.connect();

    lastES().frame("scan:start", { action: "scan", detail: "Breaking Bad", source: "scheduled" });

    expect(notify.info).toHaveBeenCalledWith("Scan started: Breaking Bad");
  });

  it("notify event calls notify.success for success level", () => {
    events.connect();

    lastES().frame("notify", { level: "success", text: "subtitle saved" });

    expect(notify.success).toHaveBeenCalledWith("subtitle saved");
    expect(notify.error).not.toHaveBeenCalled();
  });

  it("scan:done triggers pollStatus", () => {
    events.connect();

    lastES().frame("scan:done", { action: "scan", detail: "", source: "scheduler" });

    expect(pollStatus).toHaveBeenCalled();
    // Coverage counts reconcile after any terminal outcome.
    expect(emit).toHaveBeenCalledWith(BusEvent.DataInvalidate);
  });

  it("malformed frames are dropped without side effects", () => {
    events.connect();

    // Not JSON at all, and a payload failing the generated decoder.
    lastES().frame("notify", { level: "nonsense-level", text: 42 });

    expect(notify.error).not.toHaveBeenCalled();
    expect(notify.info).not.toHaveBeenCalled();
    expect(notify.success).not.toHaveBeenCalled();
  });
});
