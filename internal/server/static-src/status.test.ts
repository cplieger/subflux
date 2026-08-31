import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Type-only: erased at runtime, so the hoisted vi.mock factory may reference it.
import type * as ActionsModule from "@cplieger/actions";

// Capture every action's dispatch mock, run() implementation, and full
// definition by name so tests can drive and assert specific actions
// (activity.cancel, activity.dismiss, status.poll, …). vi.hoisted because
// the vi.mock factories run before module-level initializers. The factories
// use PLAIN functions (not vi.fn wrappers) so the config's mockReset cannot
// strip them between tests — several suites below vi.resetModules() and
// re-import status.js mid-test.
const dispatchers = vi.hoisted(() => new Map<string, ReturnType<typeof vi.fn>>());
const cancellers = vi.hoisted(() => new Map<string, ReturnType<typeof vi.fn>>());
const actionRuns = vi.hoisted(
  () => new Map<string, (args: unknown, signal: AbortSignal) => Promise<unknown>>(),
);
const actionConfigs = vi.hoisted(() => new Map<string, Record<string, unknown>>());
function registerAction(cfg: {
  name: string;
  run?: (args: unknown, signal: AbortSignal) => Promise<unknown>;
}): { dispatch: ReturnType<typeof vi.fn>; cancel: ReturnType<typeof vi.fn> } {
  const dispatch = vi.fn().mockResolvedValue(undefined);
  const cancel = vi.fn();
  dispatchers.set(cfg.name, dispatch);
  cancellers.set(cfg.name, cancel);
  actionConfigs.set(cfg.name, cfg as unknown as Record<string, unknown>);
  if (cfg.run) {
    actionRuns.set(cfg.name, cfg.run);
  }
  return { dispatch, cancel };
}

// Wire client fns used by the status poll; implementations are set per test
// (mockReset strips them before each test).
const wire = vi.hoisted(() => ({
  listActivity: vi.fn(),
  listAlertsRaw: vi.fn(),
  providerTimeouts: vi.fn(),
  stateStats: vi.fn(),
}));

vi.mock("./api-client.js", () => ({
  apiGet: vi.fn().mockResolvedValue(null),
  apiGetTyped: vi.fn().mockResolvedValue(null),
  fillPath: (template: string, params: Record<string, string | number>): string =>
    template.replace(/\{(\w+)\}/g, (_, k: string) => encodeURIComponent(String(params[k]))),
}));
vi.mock("@cplieger/actions", async (importOriginal) => ({
  apiAction: (cfg: { name: string }) => registerAction(cfg),
  defineAction: (cfg: { name: string }) => registerAction(cfg),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
  // The REAL pollAction: setStatusDegraded's 5s floor is pinned against the
  // genuine cadence primitive (fake timers drive it), with only the action's
  // dispatch captured above.
  pollAction: (await importOriginal<typeof ActionsModule>()).pollAction,
}));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./wire/decoders.gen.js", () => ({
  decodeStats: vi.fn((v: unknown) => v),
  decodeProvidersResponse: vi.fn((v: unknown) => v),
}));
vi.mock("./wire/client.gen.js", () => ({
  listActivity: wire.listActivity,
  listAlertsRaw: wire.listAlertsRaw,
  providerTimeouts: wire.providerTimeouts,
  stateStats: wire.stateStats,
  PATH_CANCEL_ACTIVITY: "/api/activity/{id}/cancel",
  PATH_DISMISS_ACTIVITY: "/api/activity",
  PATH_DISMISS_ALERT: "/api/alerts",
}));
// The status popover fake reports open so poll runs paint the popup; plain
// functions keep it reset-proof. onOpen is never fired, so the skeleton
// anti-flicker path stays disarmed and paints are synchronous.
vi.mock("./popover-menu.js", () => ({
  createMenuPopover: () => ({
    toggle: () => undefined,
    hide: () => undefined,
    isOpen: true,
    reposition: () => undefined,
    dispose: () => undefined,
  }),
}));

import * as store from "./store.js";
import { buildActivityItem, updateLiveTimers } from "./status.js";
import { SSE_DOWN_POLL_MS, STATUS_RECONCILE_MS } from "./constants.js";
import type * as StatusModule from "./status.js";
import type * as BusModule from "./bus.js";
import type { ActivityEntry } from "./wire/types.gen.js";
import type {
  Alert,
  ParsedConfig,
  ProviderStatus,
  ProvidersResponse,
  Stats,
} from "./wire/types.gen.js";

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

function alertEntry(partial: Partial<Alert>): Alert {
  return {
    time: "2026-07-19T10:00:00Z",
    level: "warn",
    message: "m",
    source: "scanner",
    kind: "transient",
    id: 1,
    dismissed: false,
    ...partial,
  };
}

function statsOf(partial: Partial<Stats>): Stats {
  return {
    last_scan: "",
    downloads: 0,
    attempts: 0,
    scan_interval_seconds: 0,
    total_subs: 0,
    total_series: 0,
    total_movies: 0,
    missing_subs: 0,
    partial: false,
    ...partial,
  };
}

function providersRes(providers: Record<string, ProviderStatus>): ProvidersResponse {
  return { enabled: true, providers };
}

// buildStatsSummary reads only cfg.providers off the store's config; the rest
// of ParsedConfig is irrelevant here, so one localized cast keeps the fixture
// to the fields under test.
function configWithProviders(providers: Record<string, boolean>): ParsedConfig {
  return { providers } as unknown as ParsedConfig;
}

/** The status button, or a hard failure if the harness DOM is missing. */
function statusBtnEl(): HTMLElement {
  const b = document.getElementById("statusBtn");
  if (!b) {
    throw new Error("statusBtn not mounted");
  }
  return b;
}

function statusLabelText(): string {
  return statusBtnEl().querySelector(".nav-label")?.textContent ?? "";
}

function statusIconEl(): HTMLElement {
  const i = document.getElementById("statusIcon");
  if (!i) {
    throw new Error("statusIcon not mounted");
  }
  return i;
}

/** Flush the microtask queue (dispatch().then chains). */
async function flush(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
}

describe("status: buildActivityItem terminal renders", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    store.set("isAdmin", false);
  });

  it("cancelled entries render the distinct stopped state, not the success check", () => {
    const item = buildActivityItem(
      entry({ id: "c1", done: true, cancelled: true, ended_at: "2026-07-19T10:05:00Z" }),
    );
    expect(item.querySelector(".act-cancelled")).not.toBeNull();
    expect(item.querySelector(".act-done")).toBeNull();
    expect(item.querySelector(".live-timer")?.textContent).toContain("stopped");
  });

  it("failed entries render the distinct failed state, not the success check", () => {
    const item = buildActivityItem(
      entry({ id: "f1", done: true, failed: true, ended_at: "2026-07-19T10:05:00Z" }),
    );
    expect(item.querySelector(".act-failed")).not.toBeNull();
    expect(item.querySelector(".act-done")).toBeNull();
    expect(item.querySelector(".live-timer")?.textContent).toContain("failed");
  });

  it("completed entries keep the success check", () => {
    const item = buildActivityItem(
      entry({ id: "d1", done: true, ended_at: "2026-07-19T10:05:00Z" }),
    );
    expect(item.querySelector(".act-done")).not.toBeNull();
    expect(item.querySelector(".act-cancelled")).toBeNull();
    expect(item.querySelector(".act-failed")).toBeNull();
  });
});

describe("status: stop control", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    store.set("isAdmin", false);
    dispatchers.get("activity.cancel")?.mockClear();
    dispatchers.get("activity.cancel")?.mockResolvedValue(undefined);
  });

  it("running cancellable per-item scans render the stop button for any user", () => {
    const item = buildActivityItem(entry({ id: "r1", cancellable: true, kind: "series" }));
    expect(item.querySelector('button[aria-label="Stop scan"]')).not.toBeNull();
  });

  it("running entries without a live stop registration render no stop button", () => {
    const item = buildActivityItem(entry({ id: "r2" }));
    expect(item.querySelector("button")).toBeNull();
  });

  it("admin-only scans hide the stop button from plain users and show it to admins", () => {
    const full = entry({ id: "r3", cancellable: true, kind: "full", required_role: "admin" });

    store.set("isAdmin", false);
    expect(buildActivityItem(full).querySelector('button[aria-label="Stop scan"]')).toBeNull();

    store.set("isAdmin", true);
    expect(buildActivityItem(full).querySelector('button[aria-label="Stop scan"]')).not.toBeNull();
  });

  it("clicking stop dispatches the cancel action and enters the optimistic stopping state", async () => {
    const e = entry({ id: "s1", cancellable: true, kind: "movie", media_id: 7 });
    const item = buildActivityItem(e);
    document.body.appendChild(item);

    const btn = item.querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]');
    expect(btn).not.toBeNull();
    btn?.click();

    expect(dispatchers.get("activity.cancel")).toHaveBeenCalledWith("s1");
    expect(btn?.disabled).toBe(true);
    expect(item.querySelector(".live-timer")?.textContent).toContain("stopping");
    await flush();

    // A repaint while the scan is still running preserves the optimistic
    // overlay (poll cadence rebuilds rows from scratch).
    const rebuilt = buildActivityItem(e);
    expect(rebuilt.querySelector(".live-timer")?.textContent).toContain("stopping");
    const rebuiltBtn = rebuilt.querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]');
    expect(rebuiltBtn?.disabled).toBe(true);
  });

  it("a failed stop dispatch reverts the optimistic overlay", async () => {
    dispatchers.get("activity.cancel")?.mockResolvedValue(null);
    const e = entry({ id: "s2", cancellable: true, kind: "series", media_id: 9 });
    const item = buildActivityItem(e);
    document.body.appendChild(item);

    item.querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]')?.click();
    await flush();

    const rebuilt = buildActivityItem(e);
    expect(rebuilt.querySelector(".live-timer")?.textContent).not.toContain("stopping");
    const rebuiltBtn = rebuilt.querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]');
    expect(rebuiltBtn?.disabled).toBe(false);
  });

  it("queued entries keep the dismiss-cancel control, not the stop control", () => {
    const item = buildActivityItem(
      entry({ id: "q1", queued: true, cancellable: true, kind: "series" }),
    );
    expect(item.querySelector('button[aria-label="Stop scan"]')).toBeNull();
    expect(item.querySelector('button[aria-label="Cancel"]')).not.toBeNull();
  });
});

describe("status: buildActivityItem elapsed-time rendering", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    store.set("isAdmin", false);
  });

  function timerFor(startedAt: string, endedAt: string): string {
    const item = buildActivityItem(
      entry({ id: `dur-${endedAt}`, done: true, started_at: startedAt, ended_at: endedAt }),
    );
    return item.querySelector(".live-timer")?.textContent ?? "";
  }

  it("renders a sub-minute run in whole seconds", () => {
    expect(timerFor("2026-07-19T10:00:00Z", "2026-07-19T10:00:45Z")).toBe(" \u00B7 45s");
  });

  it("switches to minutes and seconds at exactly one minute", () => {
    expect(timerFor("2026-07-19T10:00:00Z", "2026-07-19T10:01:00Z")).toBe(" \u00B7 1m 0s");
  });

  it("renders a sub-hour run as minutes plus remainder seconds", () => {
    expect(timerFor("2026-07-19T10:00:00Z", "2026-07-19T10:03:05Z")).toBe(" \u00B7 3m 5s");
  });

  it("switches to hours and minutes at exactly one hour", () => {
    expect(timerFor("2026-07-19T10:00:00Z", "2026-07-19T11:00:00Z")).toBe(" \u00B7 1h 0m");
  });

  it("renders a multi-hour run as hours plus remainder minutes", () => {
    expect(timerFor("2026-07-19T10:00:00Z", "2026-07-19T12:05:30Z")).toBe(" \u00B7 2h 5m");
  });

  it("clamps a backwards span to zero instead of reporting negative time", () => {
    expect(timerFor("2026-07-19T10:05:00Z", "2026-07-19T10:00:00Z")).toBe(" \u00B7 0s");
  });
});

describe("status: buildActivityItem running and queued renders", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    store.set("isAdmin", false);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("queued entries render the hourglass and a queued timer", () => {
    const item = buildActivityItem(entry({ id: "q9", queued: true }));
    expect(item.querySelector(".act-queued .icon-hourglass")).not.toBeNull();
    expect(item.querySelector(".live-timer")?.textContent).toBe(" \u00B7 queued");
  });

  it("running entries render the spinner and a live elapsed timer", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T10:00:20Z"));
    const item = buildActivityItem(entry({ id: "a9", started_at: "2026-07-19T10:00:00Z" }));
    expect(item.querySelector(".act-active .spinner")).not.toBeNull();
    expect(item.querySelector(".live-timer")?.getAttribute("data-started")).toBe(
      "2026-07-19T10:00:00Z",
    );
    expect(item.querySelector(".live-timer")?.textContent).toBe(" \u00B7 20s");
  });

  it("scheduled runs are titled apart from manual ones", () => {
    expect(
      buildActivityItem(entry({ id: "s9", source: "scheduled" })).querySelector(".act-title")
        ?.textContent,
    ).toContain("Scheduled search");
    expect(
      buildActivityItem(entry({ id: "m9", source: "manual" })).querySelector(".act-title")
        ?.textContent,
    ).toContain("Manual search");
  });

  it("a completed entry the server gave no end time renders no timer at all", () => {
    const item = buildActivityItem(entry({ id: "d9", done: true }));
    expect(item.querySelector(".act-done")).not.toBeNull();
    expect(item.querySelector(".live-timer")).toBeNull();
  });

  it("terminal and queued rows carry the done row class, running rows do not", () => {
    expect(
      buildActivityItem(entry({ id: "t9", done: true, ended_at: "2026-07-19T10:01:00Z" }))
        .className,
    ).toBe("pop-item pop-act pop-done");
    expect(buildActivityItem(entry({ id: "u9", queued: true })).className).toBe(
      "pop-item pop-act pop-done",
    );
    expect(buildActivityItem(entry({ id: "v9" })).className).toBe("pop-item pop-act");
  });

  it("renders the server-supplied detail in its own cell", () => {
    const item = buildActivityItem(entry({ id: "x9", detail: "Searched 12 items" }));
    expect(item.querySelector(".act-detail")?.textContent).toBe("Searched 12 items");
  });
});

describe("status: updateLiveTimers", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    store.set("isAdmin", false);
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("re-renders every running row's elapsed time and leaves terminal rows frozen", () => {
    vi.setSystemTime(new Date("2026-07-19T10:00:30Z"));
    const running = buildActivityItem(entry({ id: "lt1", started_at: "2026-07-19T10:00:00Z" }));
    const finished = buildActivityItem(
      entry({
        id: "lt2",
        done: true,
        started_at: "2026-07-19T09:00:00Z",
        ended_at: "2026-07-19T09:00:10Z",
      }),
    );
    document.body.append(running, finished);
    expect(running.querySelector(".live-timer")?.textContent).toBe(" \u00B7 30s");

    vi.setSystemTime(new Date("2026-07-19T10:02:10Z"));
    updateLiveTimers();

    expect(running.querySelector(".live-timer")?.textContent).toBe(" \u00B7 2m 10s");
    // Terminal rows carry no data-started, so their frozen duration stays put.
    expect(finished.querySelector(".live-timer")?.textContent).toBe(" \u00B7 10s");
  });
});

// --- Poll-driven suites -----------------------------------------------------
//
// These drive the REAL status.poll run() (captured from the defineAction
// mock) against mocked wire responses. Module state (toastedActivities,
// activitiesInitialized, dismissedActivities) must start fresh per test, so
// each beforeEach resets the module registry and re-imports status.js —
// including its store/notify instances, which therefore must also be
// re-imported dynamically (the file-top static bindings would point at the
// previous instances).

interface PollWire {
  activities?: ActivityEntry[];
  alerts?: { ok: boolean; status: number; data?: Alert[] | null };
  providers?: ProvidersResponse | null;
  stats?: Stats | null;
  signal?: AbortSignal;
}

interface PollHarness {
  runPoll: (activities: ActivityEntry[]) => Promise<void>;
  /** Drive one poll with an explicit wire snapshot (alerts flavour, provider
   *  health, stats and the abort signal all under the test's control). */
  runPollWith: (w: PollWire) => Promise<void>;
  notifyM: {
    success: ReturnType<typeof vi.fn>;
    error: ReturnType<typeof vi.fn>;
    info: ReturnType<typeof vi.fn>;
  };
  status: typeof StatusModule;
  store: typeof store;
  bus: typeof BusModule;
}

// Only the module under test carries the `?boot=` query, and it is what makes
// this harness fresh. Browser Mode resolves a dynamic import through the
// browser's own URL-keyed module map, which vi.resetModules() cannot evict, so a
// bare import("./status.js") hands back the instance an earlier test evaluated --
// its toasted-activity sets and paint state included, which is what makes the
// first-poll-seeding and closed-popup assertions below observe a previous test's
// work. A distinct query is a distinct URL and therefore a fresh evaluation, and
// its top-level apiAction() calls re-register status.poll over the stale one.
// `@vite-ignore` opts out of Vite's variable-dynamic-import rewrite.
//
// The `.ts` extension is load-bearing: this specifier is built at runtime, so the
// URL the browser requests is the one written here, and that URL is what v8
// coverage attributes the evaluation to. Written `./status.js` it names a file
// that does not exist and status.ts reports 0% coverage while this suite stays
// green.
//
// ./store.js, ./notify.js and ./bus.js are imported plainly on purpose: a busted
// specifier mints a DUPLICATE instance, so busting those would hand this harness
// different objects than the status instance reads, and both the seeded store
// values and the notify spies would be invisible to it.
let bootCount = 0;
async function freshPollHarness(): Promise<PollHarness> {
  vi.resetModules();
  document.body.innerHTML =
    '<button id="statusBtn"><span class="nav-label"></span></button>' +
    '<span id="statusIcon"></span><div id="statusPopup"></div>';
  const st = await import("./store.js");
  st.set("isUnconfigured", true);
  st.set("isAdmin", false);
  const notifyM = (await import("./notify.js")) as unknown as PollHarness["notifyM"];
  const bus = await import("./bus.js");
  const status = (await import(
    /* @vite-ignore */ `./status.ts?boot=${++bootCount}`
  )) as typeof StatusModule;
  const run = actionRuns.get("status.poll");
  if (!run) {
    throw new Error("status.poll run not captured");
  }
  const runPollWith = async (w: PollWire): Promise<void> => {
    wire.listAlertsRaw.mockResolvedValue(w.alerts ?? { ok: true, status: 200, data: [] });
    wire.listActivity.mockResolvedValue(w.activities ?? []);
    wire.providerTimeouts.mockResolvedValue(w.providers ?? null);
    wire.stateStats.mockResolvedValue(w.stats ?? null);
    await run(undefined, w.signal ?? new AbortController().signal);
  };
  const runPoll = async (activities: ActivityEntry[]): Promise<void> => {
    await runPollWith({ activities });
  };
  return { runPoll, runPollWith, notifyM, status, store: st, bus };
}

describe("status: toast seeding across polls", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("toasts a completion that follows an EMPTY first poll", async () => {
    await h.runPoll([]);
    expect(h.notifyM.success).not.toHaveBeenCalled();

    await h.runPoll([entry({ id: "e1", done: true, detail: "Found 3 subtitles" })]);
    expect(h.notifyM.success).toHaveBeenCalledWith("Found 3 subtitles");
  });

  it("toasts a completion first seen RUNNING on the first poll", async () => {
    await h.runPoll([entry({ id: "r1", detail: "Scanning" })]);
    expect(h.notifyM.success).not.toHaveBeenCalled();

    await h.runPoll([entry({ id: "r1", done: true, detail: "Scan finished" })]);
    expect(h.notifyM.success).toHaveBeenCalledWith("Scan finished");
  });

  it("seeds first-poll done entries as historical and toasts only later completions", async () => {
    await h.runPoll([entry({ id: "h1", done: true, detail: "old news" })]);
    expect(h.notifyM.success).not.toHaveBeenCalled();

    await h.runPoll([
      entry({ id: "h1", done: true, detail: "old news" }),
      entry({ id: "n1", done: true, detail: "fresh completion" }),
    ]);
    expect(h.notifyM.success).toHaveBeenCalledTimes(1);
    expect(h.notifyM.success).toHaveBeenCalledWith("fresh completion");
  });

  it("toasts distinct terminal states distinctly after a running first poll", async () => {
    await h.runPoll([entry({ id: "c1", detail: "x" }), entry({ id: "f1", detail: "y" })]);
    await h.runPoll([
      entry({ id: "c1", done: true, cancelled: true, detail: "series scan" }),
      entry({ id: "f1", done: true, failed: true, detail: "movie scan" }),
    ]);
    expect(h.notifyM.info).toHaveBeenCalledWith("Stopped: series scan");
    expect(h.notifyM.error).toHaveBeenCalledWith("Failed: movie scan");
    expect(h.notifyM.success).not.toHaveBeenCalled();
  });
});

describe("status: dismissActivity success and rollback", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
    // The fake popover reports open, so poll runs paint activity rows into
    // #statusPopup and the dismiss buttons are clickable.
    h.status.initStatusPopover();
  });

  it("keeps the row hidden when the server dismissal succeeds", async () => {
    const done = entry({ id: "x1", done: true, ended_at: "2026-07-19T10:05:00Z" });
    await h.runPoll([done]);
    const row = document.querySelector('[data-act-id="x1"]');
    expect(row).not.toBeNull();

    dispatchers.get("activity.dismiss")?.mockResolvedValue(undefined);
    row?.querySelector<HTMLButtonElement>('button[aria-label="Dismiss"]')?.click();
    expect(dispatchers.get("activity.dismiss")).toHaveBeenCalledWith("x1");
    await flush();

    // Success path: no rollback refresh, and the optimistic hide persists
    // even while the server still reports the entry.
    expect(dispatchers.get("status.poll")).not.toHaveBeenCalled();
    await h.runPoll([done]);
    expect(document.querySelector('[data-act-id="x1"]')).toBeNull();
  });

  it("rolls back the optimistic hide and repolls when the dismissal fails", async () => {
    const done = entry({ id: "y1", done: true, ended_at: "2026-07-19T10:05:00Z" });
    await h.runPoll([done]);

    // Terminal failure (retries exhausted): the action resolves null.
    dispatchers.get("activity.dismiss")?.mockResolvedValue(null);
    document
      .querySelector('[data-act-id="y1"]')
      ?.querySelector<HTMLButtonElement>('button[aria-label="Dismiss"]')
      ?.click();
    await flush();

    // The rollback requests a refresh poll…
    expect(dispatchers.get("status.poll")).toHaveBeenCalled();
    // …and the id left the dismissed set: the row renders again.
    await h.runPoll([done]);
    expect(document.querySelector('[data-act-id="y1"]')).not.toBeNull();
  });

  it("surfaces the framework error notification for failed dismissals", () => {
    // The action definition carries the error spec (the framework renders
    // it); silent-`false` would swallow terminal failures.
    expect(actionConfigs.get("activity.dismiss")?.["error"]).toBe("Dismiss failed");
  });
});

describe("status: unreachable server", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("a network-level alerts failure shows the offline state, not a healthy one", async () => {
    await h.runPollWith({ alerts: { ok: false, status: 0 } });
    expect(statusBtnEl().dataset["status"]).toBe("offline");
    expect(statusLabelText()).toBe("Offline");
    expect(statusIconEl().querySelector(".icon-warning")).not.toBeNull();
  });

  it("an HTTP error carrying a real status code polls on and reports health", async () => {
    await h.runPollWith({ alerts: { ok: false, status: 500 } });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
    expect(statusLabelText()).toBe("Healthy");
  });

  it("offline gates result handling, not issuance: legs fire, results are discarded", async () => {
    // The legs go out in one concurrent burst, so the activity fetch is
    // ISSUED alongside the alerts probe…
    const published = vi.fn();
    const off = store.subscribe("runningScansByScope", published);
    await h.runPollWith({
      alerts: { ok: false, status: 0 },
      activities: [entry({ id: "off1", kind: "series", media_id: 1, cancellable: true })],
    });
    off();

    expect(wire.listActivity).toHaveBeenCalled();
    // …but the offline verdict discards its RESULT: nothing publishes, and
    // the button shows offline rather than the fetched running scan.
    expect(published).not.toHaveBeenCalled();
    expect(statusBtnEl().dataset["status"]).toBe("offline");
  });

  it("a poll aborted mid-flight does not paint the offline state", async () => {
    const ac = new AbortController();
    ac.abort();
    await h.runPollWith({ alerts: { ok: false, status: 0 }, signal: ac.signal });
    expect(statusBtnEl().dataset["status"]).toBeUndefined();
  });

  it("leaves a closed popup untouched when the server is unreachable", async () => {
    const popup = document.getElementById("statusPopup");
    popup?.appendChild(document.createElement("hr"));
    await h.runPollWith({ alerts: { ok: false, status: 0 } });
    expect(popup?.querySelector("hr")).not.toBeNull();
  });

  it("replaces an open popup's content with the unreachable-server row", async () => {
    h.status.initStatusPopover();
    await h.runPollWith({ alerts: { ok: false, status: 0 } });
    const row = document.querySelector("#statusPopup .pop-item.muted");
    expect(row?.textContent).toContain("Server unreachable");
  });
});

describe("status: status button severity", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
    h.store.set("isUnconfigured", false);
  });

  it("reports healthy with the dot icon when nothing is wrong", async () => {
    await h.runPollWith({});
    expect(statusBtnEl().dataset["status"]).toBe("idle");
    expect(statusLabelText()).toBe("Healthy");
    expect(statusIconEl().querySelector(".icon-dot")).not.toBeNull();
  });

  it("an error-level alert outranks a running scan", async () => {
    await h.runPollWith({
      alerts: {
        ok: true,
        status: 200,
        data: [alertEntry({ id: 1 }), alertEntry({ id: 2, level: "error" })],
      },
      activities: [entry({ id: "run1" })],
    });
    expect(statusBtnEl().dataset["status"]).toBe("error");
    expect(statusLabelText()).toBe("Error");
    expect(statusIconEl().querySelector(".icon-warning")).not.toBeNull();
  });

  it("a persistent alert reports error even when no alert is error-level", async () => {
    await h.runPollWith({
      alerts: {
        ok: true,
        status: 200,
        data: [alertEntry({ id: 1 }), alertEntry({ id: 2, kind: "persistent" })],
      },
    });
    expect(statusBtnEl().dataset["status"]).toBe("error");
    expect(statusLabelText()).toBe("Error");
  });

  it("a transient warn alert reports warning, not error", async () => {
    await h.runPollWith({ alerts: { ok: true, status: 200, data: [alertEntry({ id: 1 })] } });
    expect(statusBtnEl().dataset["status"]).toBe("warn");
    expect(statusLabelText()).toBe("Warning");
  });

  it("a timed-out provider reports warning with no alerts at all", async () => {
    await h.runPollWith({
      providers: providersRes({
        opensubtitles: { timed_out: true, recent_failures: 3, threshold: 5 },
      }),
    });
    expect(statusBtnEl().dataset["status"]).toBe("warn");
    expect(statusLabelText()).toBe("Warning");
  });

  it("a running activity with nothing wrong reports scanning", async () => {
    await h.runPollWith({ activities: [entry({ id: "r1" })] });
    expect(statusBtnEl().dataset["status"]).toBe("scanning");
    expect(statusLabelText()).toBe("Searching");
  });

  it("a finished activity beside a running one still reports scanning", async () => {
    await h.runPollWith({
      activities: [
        entry({ id: "d1", done: true, ended_at: "2026-07-19T10:01:00Z" }),
        entry({ id: "r1" }),
      ],
    });
    expect(statusBtnEl().dataset["status"]).toBe("scanning");
  });

  it("only-finished activities report healthy", async () => {
    await h.runPollWith({
      activities: [entry({ id: "d1", done: true, ended_at: "2026-07-19T10:01:00Z" })],
    });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
  });

  it("the scanning state clears the warning icon an earlier poll left behind", async () => {
    await h.runPollWith({ alerts: { ok: true, status: 200, data: [alertEntry({ id: 1 })] } });
    expect(statusIconEl().querySelector(".icon-warning")).not.toBeNull();

    await h.runPollWith({ activities: [entry({ id: "r1" })] });

    expect(statusIconEl().childElementCount).toBe(0);
  });
});

describe("status: what the poll fetches", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("an unconfigured server is asked for neither provider health nor stats", async () => {
    h.status.initStatusPopover();
    await h.runPollWith({});
    expect(wire.providerTimeouts).not.toHaveBeenCalled();
    expect(wire.stateStats).not.toHaveBeenCalled();
  });

  it("a closed popup fetches provider health but not the popup-only stats", async () => {
    h.store.set("isUnconfigured", false);
    await h.runPollWith({});
    expect(wire.providerTimeouts).toHaveBeenCalled();
    expect(wire.stateStats).not.toHaveBeenCalled();
  });

  it("an open popup fetches both", async () => {
    h.store.set("isUnconfigured", false);
    h.status.initStatusPopover();
    await h.runPollWith({});
    expect(wire.providerTimeouts).toHaveBeenCalled();
    expect(wire.stateStats).toHaveBeenCalled();
  });

  it("a closed popup is never painted", async () => {
    h.store.set("isUnconfigured", false);
    await h.runPollWith({ activities: [entry({ id: "r1" })] });
    expect(document.getElementById("statusPopup")?.childElementCount).toBe(0);
  });
});

describe("status: poll side effects", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("publishes the running scans by scope from every poll", async () => {
    await h.runPollWith({
      activities: [
        entry({ id: "sc1", kind: "series", media_id: 42, cancellable: true }),
        entry({ id: "done1", kind: "movie", media_id: 7, done: true }),
      ],
    });
    const scans = h.store.get("runningScansByScope");
    expect([...scans.values()]).toEqual([{ activityId: "sc1", cancellable: true }]);
  });

  it("invalidates cached data only when the last running scan finishes", async () => {
    const invalidated = vi.fn();
    h.bus.on(h.bus.BusEvent.DataInvalidate, invalidated);

    await h.runPollWith({ activities: [] });
    expect(invalidated).not.toHaveBeenCalled();

    await h.runPollWith({ activities: [entry({ id: "r1" })] });
    expect(invalidated).not.toHaveBeenCalled();

    await h.runPollWith({
      activities: [entry({ id: "r1", done: true, ended_at: "2026-07-19T10:01:00Z" })],
    });
    expect(invalidated).toHaveBeenCalledTimes(1);
  });

  it("keeps the optimistic stopping overlay while the scan is still running", async () => {
    h.status.initStatusPopover();
    dispatchers.get("activity.cancel")?.mockResolvedValue(undefined);
    const running = entry({ id: "st1", cancellable: true, kind: "series", media_id: 3 });
    await h.runPollWith({ activities: [running] });
    document
      .querySelector('[data-act-id="st1"]')
      ?.querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]')
      ?.click();
    await flush();

    await h.runPollWith({ activities: [running] });

    expect(h.status.buildActivityItem(running).querySelector(".live-timer")?.textContent).toContain(
      "stopping",
    );
  });

  it("drops the stopping overlay once the entry stops running", async () => {
    h.status.initStatusPopover();
    dispatchers.get("activity.cancel")?.mockResolvedValue(undefined);
    const running = entry({ id: "st2", cancellable: true, kind: "series", media_id: 4 });
    await h.runPollWith({ activities: [running] });
    document
      .querySelector('[data-act-id="st2"]')
      ?.querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]')
      ?.click();
    await flush();

    await h.runPollWith({
      activities: [
        entry({ id: "st2", done: true, cancelled: true, ended_at: "2026-07-19T10:01:00Z" }),
      ],
    });

    expect(
      h.status.buildActivityItem(running).querySelector(".live-timer")?.textContent,
    ).not.toContain("stopping");
  });
});

describe("status: popup content", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
    h.store.set("isUnconfigured", false);
    h.store.set("config", null);
    h.status.initStatusPopover();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function popupRows(): string[] {
    return [...document.querySelectorAll("#statusPopup > *")].map((n) => n.textContent ?? "");
  }

  function header(): Element | null {
    return document.querySelector("#statusPopup .pop-header");
  }

  function mutedRow(): Element | null {
    return document.querySelector("#statusPopup .pop-item.muted");
  }

  it("summarises media, downloads and missing counts in one header row", async () => {
    await h.runPollWith({
      stats: statsOf({ total_series: 3, total_movies: 4, downloads: 9, missing_subs: 2 }),
    });
    expect(header()?.textContent).toBe("Media: 7 \u00B7 Downloads: 9 \u00B7 Missing: 2");
    expect(popupRows()).toEqual(["Media: 7 \u00B7 Downloads: 9 \u00B7 Missing: 2"]);
  });

  it("omits the media, missing and provider parts when there is nothing to say", async () => {
    h.store.set("config", configWithProviders({ opensubtitles: true }));
    await h.runPollWith({ stats: statsOf({}) });
    expect(header()?.textContent).toBe("Downloads: 0");
  });

  it("counts a series-only library", async () => {
    await h.runPollWith({ stats: statsOf({ total_series: 3, total_movies: 0 }) });
    expect(header()?.textContent).toBe("Media: 3 \u00B7 Downloads: 0");
  });

  it("counts a movies-only library", async () => {
    await h.runPollWith({ stats: statsOf({ total_series: 0, total_movies: 4 }) });
    expect(header()?.textContent).toBe("Media: 4 \u00B7 Downloads: 0");
  });

  it("counts enabled providers against the timed-out ones", async () => {
    h.store.set("config", configWithProviders({ a: true, b: true, c: false }));
    await h.runPollWith({
      stats: statsOf({ downloads: 1 }),
      providers: providersRes({
        a: { timed_out: true, recent_failures: 2, threshold: 5 },
        b: { timed_out: false, recent_failures: 0, threshold: 5 },
      }),
    });
    expect(header()?.textContent).toBe("Downloads: 1 \u00B7 1/2 providers");
  });

  it("renders no header and an all-clear row when there is nothing to report", async () => {
    await h.runPollWith({ providers: { enabled: true, providers: {} } });
    expect(header()).toBeNull();
    expect(mutedRow()?.textContent).toBe("All clear");
    expect(popupRows()).toEqual(["All clear"]);
  });

  it("keys activity rows so a repeat poll reuses the mounted node", async () => {
    const running = entry({ id: "a1", detail: "Scanning A" });
    await h.runPollWith({ activities: [running] });
    const first = document.querySelector('[data-act-id="a1"]');
    expect(first).not.toBeNull();

    await h.runPollWith({ activities: [running] });

    expect(document.querySelector('[data-act-id="a1"]')).toBe(first);
  });

  it("hides manual search and manual download activities from the popup", async () => {
    await h.runPollWith({
      activities: [
        entry({ id: "m1", action: "Manual Search" }),
        entry({ id: "m2", action: "Manual Download" }),
        entry({ id: "s1", action: "Series Search" }),
      ],
    });
    expect(document.querySelector('[data-act-id="m1"]')).toBeNull();
    expect(document.querySelector('[data-act-id="m2"]')).toBeNull();
    expect(document.querySelector('[data-act-id="s1"]')).not.toBeNull();
  });

  it("renders one warning row per timed-out provider carrying its last error", async () => {
    await h.runPollWith({
      providers: providersRes({
        subdl: {
          timed_out: true,
          recent_failures: 4,
          threshold: 5,
          last_error: "429 too many requests",
        },
        gestdown: { timed_out: false, recent_failures: 0, threshold: 5 },
      }),
    });
    expect(document.querySelectorAll("#statusPopup .pop-item").length).toBe(1);
    const row = document.querySelector("#statusPopup .pop-item");
    expect(row?.textContent).toBe("subdl: 429 too many requests");
    expect(row?.querySelector(".level-warn")?.textContent).toBe("subdl: ");
  });

  it("reports no timeouts at all when provider health tracking is switched off", async () => {
    await h.runPollWith({
      providers: {
        enabled: false,
        providers: { subdl: { timed_out: true, recent_failures: 4, threshold: 5 } },
      },
    });
    expect(mutedRow()?.textContent).toBe("All clear");
  });

  it("falls back to a failure count when the provider reported no error text", async () => {
    await h.runPollWith({
      providers: providersRes({ subdl: { timed_out: true, recent_failures: 4, threshold: 5 } }),
    });
    expect(document.querySelector("#statusPopup .pop-item")?.textContent).toBe("subdl: 4 failures");
  });

  it("renders a transient alert with its level, message and dismiss control", async () => {
    await h.runPollWith({
      alerts: {
        ok: true,
        status: 200,
        data: [alertEntry({ id: 7, message: "disk almost full", source: "scanner" })],
      },
    });
    const row = document.querySelector("#statusPopup .pop-item");
    expect(row?.querySelector(".level-warn")?.textContent).toBe("[warn]");
    expect(row?.textContent).toContain("disk almost full");
    expect(row?.querySelector('button[aria-label="Dismiss alert"]')).not.toBeNull();
    expect(row?.querySelector(".pop-time")).not.toBeNull();
  });

  it("marks a persistent alert persistent and labels it by source, not level", async () => {
    await h.runPollWith({
      alerts: {
        ok: true,
        status: 200,
        data: [
          alertEntry({
            id: 8,
            kind: "persistent",
            message: "no providers enabled",
            source: "config",
          }),
        ],
      },
    });
    const row = document.querySelector("#statusPopup .pop-item.persistent");
    expect(row?.querySelector(".level-warn")?.textContent).toBe("[config]");
    expect(row?.textContent).toContain("no providers enabled");
    expect(row?.querySelector(".pop-time")).not.toBeNull();
  });

  it("dates the last scan from the newest completed full scan, not any activity", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({
      stats: statsOf({ last_scan: "2026-07-19T09:00:00Z" }),
      activities: [
        entry({
          id: "fs1",
          action: "Full Scan",
          done: true,
          ended_at: "2026-07-19T11:00:00Z",
        }),
        entry({
          id: "ss1",
          action: "Series Search",
          done: true,
          ended_at: "2026-07-19T11:30:00Z",
        }),
      ],
    });
    expect(mutedRow()?.textContent).toBe("Last scan: 1h ago");
  });

  it("falls back to the stats timestamp when no full scan has completed", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({ stats: statsOf({ last_scan: "2026-07-19T09:00:00Z" }) });
    expect(mutedRow()?.textContent).toBe("Last scan: 3h ago");
  });

  it("adds the countdown to the next scheduled scan", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({
      stats: statsOf({ last_scan: "2026-07-19T11:00:00Z", scan_interval_seconds: 7200 }),
      activities: [
        entry({ id: "fs1", action: "Full Scan", done: true, ended_at: "2026-07-19T11:00:00Z" }),
      ],
    });
    expect(mutedRow()?.textContent).toBe("Last scan: 1h ago \u00B7 Next scan: in 1h 0m");
  });

  it("omits the countdown once the next scan is already overdue", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({
      stats: statsOf({ last_scan: "2026-07-19T03:00:00Z", scan_interval_seconds: 7200 }),
      activities: [
        entry({ id: "fs1", action: "Full Scan", done: true, ended_at: "2026-07-19T03:00:00Z" }),
      ],
    });
    expect(mutedRow()?.textContent).toBe("Last scan: 9h ago");
  });

  it("omits the countdown for a scan due exactly now", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({
      stats: statsOf({ last_scan: "2026-07-19T10:00:00Z", scan_interval_seconds: 7200 }),
      activities: [
        entry({ id: "fs1", action: "Full Scan", done: true, ended_at: "2026-07-19T10:00:00Z" }),
      ],
    });
    expect(mutedRow()?.textContent).toBe("Last scan: 2h ago");
  });

  it("shows no scan timing at all while a scan is running", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({
      stats: statsOf({ last_scan: "2026-07-19T09:00:00Z" }),
      activities: [entry({ id: "r1" })],
    });
    expect(mutedRow()).toBeNull();
  });

  it("calls a scan from the last minute just now", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({ stats: statsOf({ last_scan: "2026-07-19T11:59:30Z" }) });
    expect(mutedRow()?.textContent).toBe("Last scan: just now");
  });

  it("switches from just-now to minutes at exactly one minute", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({ stats: statsOf({ last_scan: "2026-07-19T11:59:00Z" }) });
    expect(mutedRow()?.textContent).toBe("Last scan: 1m ago");
  });

  it("reports a sub-hour gap in minutes", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({ stats: statsOf({ last_scan: "2026-07-19T11:30:00Z" }) });
    expect(mutedRow()?.textContent).toBe("Last scan: 30m ago");
  });

  it("switches from hours to days at exactly one day", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({ stats: statsOf({ last_scan: "2026-07-18T12:00:00Z" }) });
    expect(mutedRow()?.textContent).toBe("Last scan: 1d ago");
  });

  it("reports a multi-day gap in whole days", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));
    await h.runPollWith({ stats: statsOf({ last_scan: "2026-07-16T06:00:00Z" }) });
    expect(mutedRow()?.textContent).toBe("Last scan: 3d ago");
  });
});

describe("status: toast suppression and de-duplication", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("never toasts a manual search, manual download or audio sync", async () => {
    await h.runPollWith({
      activities: [
        entry({ id: "m1", action: "Manual Search" }),
        entry({ id: "m2", action: "Manual Download" }),
        entry({ id: "m3", action: "Audio Sync" }),
      ],
    });

    await h.runPollWith({
      activities: [
        entry({ id: "m1", action: "Manual Search", done: true, detail: "a" }),
        entry({ id: "m2", action: "Manual Download", done: true, detail: "b" }),
        entry({ id: "m3", action: "Audio Sync", done: true, detail: "c" }),
      ],
    });

    expect(h.notifyM.success).not.toHaveBeenCalled();
  });

  it("toasts a completion once, not again on every later poll", async () => {
    await h.runPollWith({ activities: [entry({ id: "t1" })] });
    const done = entry({ id: "t1", done: true, detail: "done once" });

    await h.runPollWith({ activities: [done] });
    await h.runPollWith({ activities: [done] });

    expect(h.notifyM.success).toHaveBeenCalledTimes(1);
  });
});

describe("status: activity dismissal animation", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
    h.status.initStatusPopover();
    dispatchers.get("activity.dismiss")?.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  async function dismissRow(id: string): Promise<Element> {
    await h.runPollWith({
      activities: [entry({ id, done: true, ended_at: "2026-07-19T10:01:00Z" })],
    });
    const row = document.querySelector(`[data-act-id="${id}"]`);
    if (!row) {
      throw new Error(`row ${id} not rendered`);
    }
    row.querySelector<HTMLButtonElement>('button[aria-label="Dismiss"]')?.click();
    return row;
  }

  it("disables the button, starts the exit animation and removes the row on transition end", async () => {
    const row = await dismissRow("z1");
    expect(row.querySelector<HTMLButtonElement>(".close-btn")?.disabled).toBe(true);
    expect(row.classList.contains("pop-dismissing")).toBe(true);
    expect(row.isConnected).toBe(true);

    row.dispatchEvent(new Event("transitionend"));

    expect(row.isConnected).toBe(false);
  });

  it("removes the row on the fallback timer when no transition ever fires", async () => {
    vi.useFakeTimers();
    const row = await dismissRow("z2");
    expect(row.isConnected).toBe(true);

    vi.advanceTimersByTime(300);

    expect(row.isConnected).toBe(false);
  });
});

describe("status: alert dismissal", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
    h.status.initStatusPopover();
  });

  async function alertRow(): Promise<Element> {
    await h.runPollWith({ alerts: { ok: true, status: 200, data: [alertEntry({ id: 7 })] } });
    const row = document.querySelector("#statusPopup .pop-item");
    if (!row) {
      throw new Error("alert row not rendered");
    }
    return row;
  }

  it("asks the server to delete the alert and animates the row out", async () => {
    dispatchers.get("alerts.dismiss")?.mockResolvedValue(undefined);
    const row = await alertRow();

    row.querySelector<HTMLButtonElement>('button[aria-label="Dismiss alert"]')?.click();

    expect(dispatchers.get("alerts.dismiss")).toHaveBeenCalledWith(7);
    expect(row.classList.contains("pop-dismissing")).toBe(true);
  });

  it("refreshes the status once the alert is gone server-side", async () => {
    dispatchers.get("alerts.dismiss")?.mockResolvedValue(undefined);
    const row = await alertRow();

    row.querySelector<HTMLButtonElement>('button[aria-label="Dismiss alert"]')?.click();
    await flush();

    expect(dispatchers.get("status.poll")).toHaveBeenCalled();
  });

  it("does not refresh when the alert dismissal failed", async () => {
    dispatchers.get("alerts.dismiss")?.mockResolvedValue(null);
    const row = await alertRow();

    row.querySelector<HTMLButtonElement>('button[aria-label="Dismiss alert"]')?.click();
    await flush();

    expect(dispatchers.get("status.poll")).not.toHaveBeenCalled();
  });
});

describe("status: poll action contract", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("aborting cancels the in-flight poll", () => {
    h.status.abortPoll();

    expect(cancellers.get("status.poll")).toHaveBeenCalled();
  });

  it("collapses overlapping polls and stays silent on transient failures", () => {
    // Background polling must neither pile up requests nor toast blips.
    expect(actionConfigs.get("status.poll")?.["dedupe"]).toBe(true);
    expect(actionConfigs.get("status.poll")?.["error"]).toBe(false);
  });
});

describe("status: reconcile tick", () => {
  // Fresh status instance per test: the tick's interval + registration flag
  // are module state, and the interval must be created under THIS test's fake
  // clock (a stale handle from a sibling test would tick nothing).
  let h: PollHarness;

  function setDocumentHidden(hidden: boolean): void {
    Object.defineProperty(document, "hidden", { value: hidden, configurable: true });
  }

  beforeEach(async () => {
    vi.useFakeTimers();
    h = await freshPollHarness();
  });

  afterEach(() => {
    setDocumentHidden(false);
    vi.useRealTimers();
  });

  it("runs registered tasks at each reconcile interval", () => {
    const task = vi.fn();
    h.status.registerReconcileTask(task);

    vi.advanceTimersByTime(STATUS_RECONCILE_MS - 1);
    expect(task).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(task).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(STATUS_RECONCILE_MS);
    expect(task).toHaveBeenCalledTimes(2);
  });

  it("pauses while the tab is hidden and resumes when visible", () => {
    const task = vi.fn();
    h.status.registerReconcileTask(task);

    setDocumentHidden(true);
    vi.advanceTimersByTime(5 * STATUS_RECONCILE_MS);
    expect(task).not.toHaveBeenCalled();

    // No catch-up burst on return: the next scheduled tick runs, once.
    setDocumentHidden(false);
    vi.advanceTimersByTime(STATUS_RECONCILE_MS);
    expect(task).toHaveBeenCalledTimes(1);
  });

  it("an unregistered task no longer runs", () => {
    const task = vi.fn();
    const off = h.status.registerReconcileTask(task);
    off();

    vi.advanceTimersByTime(3 * STATUS_RECONCILE_MS);
    expect(task).not.toHaveBeenCalled();
  });
});

// --- Event-fed store suites (E2) --------------------------------------------
//
// These drive the exported appliers directly (events.ts routes decoded SSE
// payloads into them) and assert the status surfaces repaint with the poll
// SILENT — no wire call is armed or expected. Idempotent re-application is
// pinned through observables a real repaint would move: the status icon is
// rebuilt by replaceChildren on every render, so node identity survives only
// a no-op, and the running-scans store publish is watched via subscribe.

describe("status: event-fed store", () => {
  let h: PollHarness;

  beforeEach(async () => {
    h = await freshPollHarness();
  });

  it("an activity upsert flips the chip and publishes running scans, poll silent", () => {
    h.status.applyActivityEvent({
      op: "upsert",
      entry: entry({ id: "ev1", kind: "series", media_id: 42, cancellable: true }),
    });

    expect(statusBtnEl().dataset["status"]).toBe("scanning");
    expect(statusLabelText()).toBe("Searching");
    const scans = h.store.get("runningScansByScope");
    expect([...scans.values()]).toEqual([{ activityId: "ev1", cancellable: true }]);
    expect(wire.listActivity).not.toHaveBeenCalled();
    expect(wire.listAlertsRaw).not.toHaveBeenCalled();
  });

  it("an activity remove drops the entry; re-applying the remove is a no-op", () => {
    const e = entry({ id: "ev2", kind: "movie", media_id: 7, cancellable: true });
    h.status.applyActivityEvent({ op: "upsert", entry: e });
    expect(statusBtnEl().dataset["status"]).toBe("scanning");

    h.status.applyActivityEvent({ op: "remove", entry: e });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
    expect(h.store.get("runningScansByScope").size).toBe(0);

    const iconNode = statusIconEl().firstElementChild;
    expect(iconNode).not.toBeNull();
    h.status.applyActivityEvent({ op: "remove", entry: e });
    expect(statusIconEl().firstElementChild).toBe(iconNode);
  });

  it("a replayed activity upsert mutates nothing the second time", () => {
    // A done entry renders the idle dot: icon node identity is the repaint
    // detector (every real render rebuilds it via replaceChildren).
    const e = entry({ id: "ev3", done: true, ended_at: "2026-08-30T10:01:00Z" });
    h.status.applyActivityEvent({ op: "upsert", entry: e });

    const published = vi.fn();
    const off = store.subscribe("runningScansByScope", published);
    const iconNode = statusIconEl().firstElementChild;
    expect(iconNode).not.toBeNull();

    // A fresh but value-identical entry object, as a decoded replay is.
    h.status.applyActivityEvent({ op: "upsert", entry: entry({ ...e }) });
    off();

    expect(published).not.toHaveBeenCalled();
    expect(statusIconEl().firstElementChild).toBe(iconNode);
  });

  it("an activity upsert carrying a CHANGE does repaint", () => {
    const e = entry({ id: "ev4", kind: "series", media_id: 3, cancellable: true });
    h.status.applyActivityEvent({ op: "upsert", entry: e });
    expect(statusBtnEl().dataset["status"]).toBe("scanning");

    h.status.applyActivityEvent({
      op: "upsert",
      entry: entry({ ...e, done: true, ended_at: "2026-08-30T10:01:00Z" }),
    });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
    expect(h.store.get("runningScansByScope").size).toBe(0);
  });

  it("an alert raise flips the chip to warning and a dismiss restores it, event-fresh", () => {
    h.status.applyAlertEvent({ op: "raise", alert: alertEntry({ id: 5 }) });
    expect(statusBtnEl().dataset["status"]).toBe("warn");
    expect(statusLabelText()).toBe("Warning");
    expect(wire.listAlertsRaw).not.toHaveBeenCalled();

    h.status.applyAlertEvent({ op: "dismiss", alert: alertEntry({ id: 5 }) });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
  });

  it("a replayed alert raise mutates nothing the second time", () => {
    h.status.applyAlertEvent({ op: "raise", alert: alertEntry({ id: 6 }) });
    const iconNode = statusIconEl().firstElementChild;
    expect(iconNode).not.toBeNull();

    h.status.applyAlertEvent({ op: "raise", alert: alertEntry({ id: 6 }) });
    expect(statusIconEl().firstElementChild).toBe(iconNode);
  });

  it("a dismiss for an alert the store never held renders nothing", () => {
    h.status.applyAlertEvent({ op: "dismiss", alert: alertEntry({ id: 99 }) });
    expect(statusBtnEl().dataset["status"]).toBeUndefined();
  });

  it("a provider raise turns the chip event-fresh; a clear restores it", () => {
    h.status.applyProviderEvent({
      op: "raise",
      entry: {
        provider: "opensubtitles",
        status: { timed_out: true, recent_failures: 3, threshold: 5 },
      },
    });
    expect(statusBtnEl().dataset["status"]).toBe("warn");
    expect(statusLabelText()).toBe("Warning");
    expect(wire.providerTimeouts).not.toHaveBeenCalled();

    // A clear carries the post-clear snapshot, the same shape the poll
    // serves for a healthy provider.
    h.status.applyProviderEvent({
      op: "clear",
      entry: {
        provider: "opensubtitles",
        status: { timed_out: false, recent_failures: 0, threshold: 5 },
      },
    });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
  });

  it("a replayed provider raise mutates nothing the second time", () => {
    h.status.applyProviderEvent({
      op: "raise",
      entry: { provider: "subdl", status: { timed_out: true, recent_failures: 4, threshold: 5 } },
    });
    const iconNode = statusIconEl().firstElementChild;
    expect(iconNode).not.toBeNull();

    h.status.applyProviderEvent({
      op: "raise",
      entry: { provider: "subdl", status: { timed_out: true, recent_failures: 4, threshold: 5 } },
    });
    expect(statusIconEl().firstElementChild).toBe(iconNode);
  });

  it("events repaint an open popup", () => {
    h.status.initStatusPopover();
    h.status.applyActivityEvent({
      op: "upsert",
      entry: entry({ id: "pv1", detail: "Scanning X" }),
    });
    expect(document.querySelector('[data-act-id="pv1"]')).not.toBeNull();
  });

  it("a terminal activity event toasts once seeded, and a replay does not re-toast", async () => {
    await h.runPoll([entry({ id: "t9" })]);
    h.status.applyActivityEvent({
      op: "upsert",
      entry: entry({ id: "t9", done: true, detail: "event done" }),
    });
    expect(h.notifyM.success).toHaveBeenCalledExactlyOnceWith("event done");

    h.status.applyActivityEvent({
      op: "upsert",
      entry: entry({ id: "t9", done: true, detail: "event done" }),
    });
    expect(h.notifyM.success).toHaveBeenCalledTimes(1);
  });

  it("a terminal event before the first poll seeds does not toast", () => {
    h.status.applyActivityEvent({
      op: "upsert",
      entry: entry({ id: "pre1", done: true, detail: "too early" }),
    });
    expect(h.notifyM.success).not.toHaveBeenCalled();
  });

  it("the reconcile poll replaces event-fed state wholesale", async () => {
    // An expired-but-undismissed alert (TTL expiry emits no event) vanishes
    // at the next full fetch: the poll is the convergence authority.
    h.status.applyAlertEvent({ op: "raise", alert: alertEntry({ id: 7 }) });
    expect(statusBtnEl().dataset["status"]).toBe("warn");

    await h.runPollWith({ alerts: { ok: true, status: 200, data: [] } });
    expect(statusBtnEl().dataset["status"]).toBe("idle");
  });
});

describe("status: the poll floor (E2)", () => {
  let h: PollHarness;

  function setDocumentHidden(hidden: boolean): void {
    Object.defineProperty(document, "hidden", { value: hidden, configurable: true });
  }

  beforeEach(async () => {
    vi.useFakeTimers();
    h = await freshPollHarness();
  });

  afterEach(() => {
    h.status.setStatusDegraded(false);
    setDocumentHidden(false);
    vi.useRealTimers();
  });

  it("steady-state while connected: the reconcile tick costs one poll per interval", () => {
    h.status.initStatusReconcile();
    const dispatch = dispatchers.get("status.poll");

    vi.advanceTimersByTime(STATUS_RECONCILE_MS - 1);
    expect(dispatch).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(dispatch).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(STATUS_RECONCILE_MS);
    expect(dispatch).toHaveBeenCalledTimes(2);
  });

  it("degraded mode polls on the 5s cadence and recovery stops it", async () => {
    const dispatch = dispatchers.get("status.poll");
    h.status.setStatusDegraded(true);
    // Entering the down period costs one immediate catch-up fetch…
    expect(dispatch).toHaveBeenCalledTimes(1);

    // …then the 5s cadence.
    await vi.advanceTimersByTimeAsync(SSE_DOWN_POLL_MS);
    expect(dispatch).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(SSE_DOWN_POLL_MS);
    expect(dispatch).toHaveBeenCalledTimes(3);

    // Reconnect: events own status again, the floor poll stops.
    h.status.setStatusDegraded(false);
    await vi.advanceTimersByTimeAsync(5 * SSE_DOWN_POLL_MS);
    expect(dispatch).toHaveBeenCalledTimes(3);
  });

  it("re-entering the same degraded state does not stack pollers", async () => {
    const dispatch = dispatchers.get("status.poll");
    h.status.setStatusDegraded(true);
    // Every ladder re-entry funnels through here; only the first counts.
    h.status.setStatusDegraded(true);
    expect(dispatch).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(SSE_DOWN_POLL_MS);
    expect(dispatch).toHaveBeenCalledTimes(2);
  });

  it("a hidden tab issues ZERO status polls, degraded mode included", () => {
    setDocumentHidden(true);
    h.status.initStatusReconcile();
    h.status.setStatusDegraded(true);

    vi.advanceTimersByTime(10 * STATUS_RECONCILE_MS);
    expect(dispatchers.get("status.poll")).not.toHaveBeenCalled();
  });
});
