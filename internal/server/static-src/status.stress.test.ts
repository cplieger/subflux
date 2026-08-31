// status.stress.test.ts — task 19's steady-state status REQUEST-RATE
// measurement over a 5-minute simulated window (fake timers): SSE-UP rides
// the shared 60s reconcile tick (≤1 fetch per 60s), SSE-DOWN rides the 5s
// degraded poll. Same harness shape as status.test.ts's poll-floor suite:
// the actions are captured by name (dispatch count = fetch count) with the
// REAL pollAction driving the down cadence, and each test gets a fresh
// status instance via the ?boot= URL-busting import.
import { describe, it, vi, beforeEach, afterEach, expect } from "vitest";

// Type-only: erased at runtime, so the hoisted vi.mock factory may reference it.
import type * as ActionsModule from "@cplieger/actions";

const dispatchers = vi.hoisted(() => new Map<string, ReturnType<typeof vi.fn>>());
function registerAction(cfg: { name: string }): {
  dispatch: ReturnType<typeof vi.fn>;
  cancel: ReturnType<typeof vi.fn>;
} {
  const dispatch = vi.fn().mockResolvedValue(undefined);
  const cancel = vi.fn();
  dispatchers.set(cfg.name, dispatch);
  return { dispatch, cancel };
}

vi.mock("./api-client.js", () => ({
  fillPath: (template: string, params: Record<string, string | number>): string =>
    template.replace(/\{(\w+)\}/g, (_, k: string) => encodeURIComponent(String(params[k]))),
}));
vi.mock("@cplieger/actions", async (importOriginal) => ({
  apiAction: (cfg: { name: string }) => registerAction(cfg),
  defineAction: (cfg: { name: string }) => registerAction(cfg),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
  // The REAL pollAction: the 5s degraded cadence is measured against the
  // genuine primitive, with only the action's dispatch captured above.
  pollAction: (await importOriginal<typeof ActionsModule>()).pollAction,
}));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./wire/decoders.gen.js", () => ({
  decodeStats: vi.fn((v: unknown) => v),
  decodeProvidersResponse: vi.fn((v: unknown) => v),
}));
vi.mock("./wire/client.gen.js", () => ({
  listActivity: vi.fn().mockResolvedValue([]),
  listAlertsRaw: vi.fn().mockResolvedValue({ ok: true, status: 200, data: [] }),
  providerTimeouts: vi.fn().mockResolvedValue(null),
  stateStats: vi.fn().mockResolvedValue(null),
  PATH_CANCEL_ACTIVITY: "/api/activity/{id}/cancel",
  PATH_DISMISS_ACTIVITY: "/api/activity",
  PATH_DISMISS_ALERT: "/api/alerts",
}));
vi.mock("./popover-menu.js", () => ({
  createMenuPopover: () => ({
    toggle: () => undefined,
    hide: () => undefined,
    isOpen: false,
    reposition: () => undefined,
    dispose: () => undefined,
  }),
}));

import { SSE_DOWN_POLL_MS, STATUS_RECONCILE_MS } from "./constants.js";
import type * as StatusModule from "./status.js";

const WINDOW_MS = 5 * 60_000;

// Fresh status instance per test: the reconcile interval and the degraded
// poller are module state that must be created under THIS test's fake clock.
// The ?boot= query busts Browser Mode's URL-keyed module map; the `.ts`
// extension is load-bearing for v8 coverage attribution (see status.test.ts).
let bootCount = 0;
async function freshStatus(): Promise<typeof StatusModule> {
  vi.resetModules();
  document.body.innerHTML =
    '<button id="statusBtn"><span class="nav-label"></span></button>' +
    '<span id="statusIcon"></span><div id="statusPopup"></div>';
  return (await import(
    /* @vite-ignore */ `./status.ts?stress=${String(++bootCount)}`
  )) as typeof StatusModule;
}

function pollCount(): number {
  const dispatch = dispatchers.get("status.poll");
  if (!dispatch) {
    throw new Error("status.poll not captured");
  }
  return dispatch.mock.calls.length;
}

describe("status stress: steady-state request rates over a 5-minute window", () => {
  let status: typeof StatusModule;

  beforeEach(async () => {
    vi.useFakeTimers();
    status = await freshStatus();
  });

  afterEach(() => {
    status.setStatusDegraded(false);
    vi.useRealTimers();
  });

  it("SSE-UP: the reconcile tick costs exactly one poll per 60s — 5 in 5 minutes", async () => {
    status.initStatusReconcile();
    expect(pollCount()).toBe(0);

    // Minute-by-minute: the count never exceeds 1 fetch per 60s of
    // simulated time (the ≤1/60s bound, asserted at every minute edge).
    for (let minute = 1; minute <= 5; minute++) {
      await vi.advanceTimersByTimeAsync(STATUS_RECONCILE_MS);
      expect(pollCount()).toBe(minute);
    }
    expect(pollCount()).toBe(5);
    console.warn(
      `[stress] status SSE-up: ${String(pollCount())} polls / ${String(WINDOW_MS / 1000)}s window ` +
        `(≤1 per ${String(STATUS_RECONCILE_MS / 1000)}s reconcile tick)`,
    );
  });

  it("SSE-DOWN: the 5s degraded cadence costs 61 polls in 5 minutes and stops on recovery", async () => {
    // Entering the down period costs one immediate catch-up fetch…
    status.setStatusDegraded(true);
    expect(pollCount()).toBe(1);

    // …then the 5s cadence: 12 per simulated minute.
    for (let minute = 1; minute <= 5; minute++) {
      await vi.advanceTimersByTimeAsync(60_000);
      expect(pollCount()).toBe(1 + minute * (60_000 / SSE_DOWN_POLL_MS));
    }
    const downTotal = pollCount();
    expect(downTotal).toBe(61);

    // Recovery: events own status again; the floor poll stops cold.
    status.setStatusDegraded(false);
    await vi.advanceTimersByTimeAsync(WINDOW_MS);
    expect(pollCount()).toBe(downTotal);
    console.warn(
      `[stress] status SSE-down: ${String(downTotal)} polls / ${String(WINDOW_MS / 1000)}s window ` +
        `(1 catch-up + ${String(SSE_DOWN_POLL_MS / 1000)}s cadence), 0 after recovery`,
    );
  });
});
