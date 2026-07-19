// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

// Capture every action's dispatch mock, run() implementation, and full
// definition by name so tests can drive and assert specific actions
// (activity.cancel, activity.dismiss, status.poll, …). vi.hoisted because
// the vi.mock factories run before module-level initializers. The factories
// use PLAIN functions (not vi.fn wrappers) so the config's mockReset cannot
// strip them between tests — several suites below vi.resetModules() and
// re-import status.js mid-test.
const dispatchers = vi.hoisted(() => new Map<string, ReturnType<typeof vi.fn>>());
const actionRuns = vi.hoisted(
  () => new Map<string, (args: unknown, signal: AbortSignal) => Promise<unknown>>(),
);
const actionConfigs = vi.hoisted(() => new Map<string, Record<string, unknown>>());
function registerAction(cfg: {
  name: string;
  run?: (args: unknown, signal: AbortSignal) => Promise<unknown>;
}): { dispatch: ReturnType<typeof vi.fn>; cancel: ReturnType<typeof vi.fn> } {
  const dispatch = vi.fn().mockResolvedValue(undefined);
  dispatchers.set(cfg.name, dispatch);
  actionConfigs.set(cfg.name, cfg as unknown as Record<string, unknown>);
  if (cfg.run) {
    actionRuns.set(cfg.name, cfg.run);
  }
  return { dispatch, cancel: vi.fn() };
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
vi.mock("@cplieger/actions", () => ({
  apiAction: (cfg: { name: string }) => registerAction(cfg),
  defineAction: (cfg: { name: string }) => registerAction(cfg),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
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
import { buildActivityItem } from "./status.js";
import type * as StatusModule from "./status.js";
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

describe("status: renderPopup", () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <button id="statusBtn"></button>
      <div id="statusPopup" hidden></div>
    `;
    store.set("config", null);
    store.set("configChecked", true);
    store.set("isUnconfigured", false);
  });

  it.todo("renders 'All clear' when no activities, alerts, or provider issues");

  it.todo("renders activity items keyed by act-{id}");

  it.todo("filters out Manual Search and Manual Download activities");

  it.todo("renders provider error items for timed-out providers");

  it.todo("renders embedded detection error when present");

  it.todo("renders alert items with dismiss button");

  it.todo("renders scan timing when not active and last_scan exists");

  it.todo("reconcile preserves existing activity DOM nodes on re-render");
});

describe("status: updateStatusButton", () => {
  it.todo("shows warning icon when alerts exist");

  it.todo("shows dot icon when activities are in progress");

  it.todo("clears icon when no alerts and no activities");
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

interface PollHarness {
  runPoll: (activities: ActivityEntry[]) => Promise<void>;
  notifyM: {
    success: ReturnType<typeof vi.fn>;
    error: ReturnType<typeof vi.fn>;
    info: ReturnType<typeof vi.fn>;
  };
  status: typeof StatusModule;
}

async function freshPollHarness(): Promise<PollHarness> {
  vi.resetModules();
  document.body.innerHTML =
    '<button id="statusBtn"><span class="nav-label"></span></button>' +
    '<span id="statusIcon"></span><div id="statusPopup"></div>';
  const st = await import("./store.js");
  st.set("isUnconfigured", true);
  st.set("isAdmin", false);
  const notifyM = (await import("./notify.js")) as unknown as PollHarness["notifyM"];
  const status = await import("./status.js");
  const run = actionRuns.get("status.poll");
  if (!run) {
    throw new Error("status.poll run not captured");
  }
  const runPoll = async (activities: ActivityEntry[]): Promise<void> => {
    wire.listAlertsRaw.mockResolvedValue({ ok: true, status: 200, data: [] });
    wire.listActivity.mockResolvedValue(activities);
    await run(undefined, new AbortController().signal);
  };
  return { runPoll, notifyM, status };
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
