//
// status.wiring.test.ts — the popover-driven half of status.ts, which
// status.test.ts cannot reach: its popover double drops the options object, so
// the panel's `onOpen` hook (the first-open skeleton plus the seeding poll)
// never fires there, and with no skeleton pending renderPopup only ever takes
// its direct-paint arm. The double here CAPTURES the options so a test can open
// the panel, and the skeleton primitive is the REAL one driven by fake timers,
// so what the assertions read is the timing a browser would get.
//
// Also here: the arguments each request a poll issues carries (the abort
// signal abortPoll relies on, and the cancel action's path), and the row
// controls' click containment.
import { describe, it, expect, vi, afterEach } from "vitest";

import type * as StatusModule from "./status.js";
import type * as StoreModule from "./store.js";
import type { ActivityEntry, Alert, Stats } from "./wire/types.gen.js";

// --- Doubles ---------------------------------------------------------------
//
// Every factory below uses PLAIN functions, never vi.fn: vitest.config sets
// mockReset, which strips an implementation registered at module load — and
// status.ts registers all four of its actions in module initializers.

const actions = vi.hoisted(() => {
  interface Def {
    name: string;
    run?: (args: unknown, signal: AbortSignal) => Promise<unknown>;
    // The real defs' request fns take the action's own argument type (a string
    // id, an alert's number); `any` keeps this record assignable from all of
    // them and callable from a test.
    request?: (args: any) => { method: string; path: string };
  }
  const state = {
    dispatched: [] as { name: string; args: unknown }[],
    runs: new Map<string, (args: unknown, signal: AbortSignal) => Promise<unknown>>(),
    defs: new Map<string, Def>(),
    /** Per-action dispatch result; `null` is the framework's failure signal. */
    results: new Map<string, unknown>(),
    register(def: Def) {
      state.defs.set(def.name, def);
      if (def.run) {
        state.runs.set(def.name, def.run);
      }
      return {
        dispatch: (args: unknown): Promise<unknown> => {
          state.dispatched.push({ name: def.name, args });
          return Promise.resolve(state.results.has(def.name) ? state.results.get(def.name) : {});
        },
        cancel: (): void => undefined,
      };
    },
    names(): string[] {
      return state.dispatched.map((d) => d.name);
    },
    reset(): void {
      state.dispatched.length = 0;
      state.runs.clear();
      state.defs.clear();
      state.results.clear();
    },
  };
  return state;
});

vi.mock("@cplieger/actions", () => ({
  apiAction: (def: { name: string }) => actions.register(def),
  defineAction: (def: { name: string }) => actions.register(def),
  retryNetwork: (fn: unknown) => fn,
  RETRY_STANDARD: {},
  registerCleanup: () => undefined,
  // Inert: the degraded 5s floor's cadence is status.test.ts's subject.
  pollAction: () => () => undefined,
}));

// The popover primitive, with the options object status.ts hands it kept so a
// test can fire `onOpen`/`onClose` the way a user opening and closing the
// panel does.
const popover = vi.hoisted(() => {
  const state = {
    anchor: null as HTMLElement | null,
    panel: null as HTMLElement | null,
    opts: {} as { onOpen?: () => void; onClose?: () => void },
    isOpen: true,
    toggles: 0,
    repositions: 0,
    reset(): void {
      state.anchor = null;
      state.panel = null;
      state.opts = {};
      state.isOpen = true;
      state.toggles = 0;
      state.repositions = 0;
    },
  };
  return state;
});

vi.mock("./popover-menu.js", () => ({
  createMenuPopover: (
    anchor: HTMLElement,
    panel: HTMLElement,
    opts: { onOpen?: () => void; onClose?: () => void },
  ) => {
    popover.anchor = anchor;
    popover.panel = panel;
    popover.opts = opts;
    return {
      toggle: (): void => {
        popover.toggles += 1;
      },
      hide: (): void => undefined,
      get isOpen(): boolean {
        return popover.isOpen;
      },
      reposition: (): void => {
        popover.repositions += 1;
      },
      dispose: (): void => undefined,
    };
  },
}));

// The generated client, recording the options object each call receives — the
// abort signal lives there, and abortPoll is only real if it is threaded.
const wire = vi.hoisted(() => {
  const state = {
    alerts: null as unknown,
    activities: null as unknown,
    providers: null as unknown,
    stats: null as unknown,
    calls: [] as { name: string; opts: { signal?: AbortSignal } | undefined }[],
    record(
      name: string,
      opts: { signal?: AbortSignal } | undefined,
      value: unknown,
    ): Promise<unknown> {
      state.calls.push({ name, opts });
      return Promise.resolve(value);
    },
    optsFor(name: string): { signal?: AbortSignal } | undefined {
      return state.calls.find((c) => c.name === name)?.opts;
    },
    called(name: string): boolean {
      return state.calls.some((c) => c.name === name);
    },
    reset(): void {
      state.alerts = { ok: true, status: 200, data: [] };
      state.activities = [];
      state.providers = null;
      state.stats = null;
      state.calls.length = 0;
    },
  };
  return state;
});

vi.mock("./wire/client.gen.js", () => ({
  listAlertsRaw: (opts?: { signal?: AbortSignal }): Promise<unknown> =>
    wire.record("listAlertsRaw", opts, wire.alerts),
  listActivity: (opts?: { signal?: AbortSignal }): Promise<unknown> =>
    wire.record("listActivity", opts, wire.activities),
  providerTimeouts: (opts?: { signal?: AbortSignal }): Promise<unknown> =>
    wire.record("providerTimeouts", opts, wire.providers),
  stateStats: (opts?: { signal?: AbortSignal }): Promise<unknown> =>
    wire.record("stateStats", opts, wire.stats),
  PATH_CANCEL_ACTIVITY: "/api/activity/{id}/cancel",
  PATH_DISMISS_ACTIVITY: "/api/activity",
  PATH_DISMISS_ALERT: "/api/alerts",
}));

// Only fillPath is consumed from api-client; the real module drags the whole
// fetch transport in.
vi.mock("./api-client.js", () => ({
  fillPath: (template: string, params: Record<string, string | number>): string =>
    template.replace(/\{(\w+)\}/g, (_m, k: string) => encodeURIComponent(String(params[k]))),
}));

const toasts = vi.hoisted(() => {
  const state = {
    success: [] as string[],
    error: [] as string[],
    info: [] as string[],
    reset(): void {
      state.success.length = 0;
      state.error.length = 0;
      state.info.length = 0;
    },
  };
  return state;
});

vi.mock("./notify.js", () => ({
  success: (m: string): void => {
    toasts.success.push(m);
  },
  error: (m: string): void => {
    toasts.error.push(m);
  },
  info: (m: string): void => {
    toasts.info.push(m);
  },
}));

// --- Fixtures --------------------------------------------------------------

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

const HARNESS_HTML =
  '<button id="statusBtn"><span class="nav-label"></span></button>' +
  '<span id="statusIcon"></span><div id="statusPopup"></div>';

interface PollWire {
  activities?: ActivityEntry[];
  alerts?: Alert[];
  stats?: Stats | null;
  signal?: AbortSignal;
}

interface Harness {
  status: typeof StatusModule;
  store: typeof StoreModule;
  /** Fire the popover's open hook, the way the primitive does on open. */
  openPanel: () => void;
  /** Fire the popover's close hook, the way the primitive does on close. */
  closePanel: () => void;
  /** Run one real status poll with the wire under the test's control. */
  poll: (w?: PollWire) => Promise<void>;
}

/** Rebuild the status-bar DOM, arm the doubles and import status.ts fresh:
 *  initStatusPopover, the skeleton controller and the toasted-activity sets are
 *  all module state, so a shared instance would leak across tests.
 *
 *  The `?boot=` query on the status specifier is what makes "fresh" true, and it
 *  is not decoration. Browser Mode resolves a dynamic import through the
 *  browser's own module map, which is keyed by URL and holds evaluated modules
 *  for the life of the page: `vi.resetModules()` clears the runner's registry but
 *  cannot evict an entry from that map. A bare `import("./status.js")` therefore
 *  returns the instance the FIRST boot evaluated, its top-level apiAction() calls
 *  never run again, and since `actions.reset()` just cleared the registry the
 *  lookup below throws "status.poll run not captured" for every test after the
 *  first. A distinct query is a distinct URL and therefore a fresh evaluation.
 *  `@vite-ignore` opts out of Vite's variable-dynamic-import rewrite.
 *
 *  The `.ts` extension is load-bearing: this specifier is built at runtime, so
 *  the URL the browser requests is the one written here, and that URL is what v8
 *  coverage attributes the evaluation to. Written `./status.js` it names a file
 *  that does not exist and status.ts reports 0% coverage while this suite stays
 *  green.
 *
 *  Only the module under test is busted. `./store.js` is imported plainly on
 *  purpose: a busted specifier mints a DUPLICATE instance, so busting the store
 *  too would hand the test a different store object than the one the status
 *  instance reads, and every seeded value would be invisible to it. The mocked
 *  modules are unaffected either way -- they resolve through the mock registry
 *  whatever query the importer carries. */
let bootCount = 0;
async function boot(opts: { unconfigured?: boolean } = {}): Promise<Harness> {
  vi.resetModules();
  actions.reset();
  popover.reset();
  wire.reset();
  toasts.reset();
  document.body.innerHTML = HARNESS_HTML;

  const store = await import("./store.js");
  store.set("isUnconfigured", opts.unconfigured ?? true);
  store.set("isAdmin", false);
  store.set("config", null);

  const status = (await import(
    /* @vite-ignore */ `./status.ts?boot=${++bootCount}`
  )) as typeof StatusModule;
  status.initStatusPopover();

  const run = actions.runs.get("status.poll");
  if (!run) {
    throw new Error("status.poll run not captured");
  }
  const openPanel = (): void => {
    popover.isOpen = true;
    popover.opts.onOpen?.();
  };
  const closePanel = (): void => {
    popover.isOpen = false;
    popover.opts.onClose?.();
  };
  const poll = async (w: PollWire = {}): Promise<void> => {
    wire.alerts = { ok: true, status: 200, data: w.alerts ?? [] };
    wire.activities = w.activities ?? [];
    wire.stats = w.stats ?? null;
    await run(undefined, w.signal ?? new AbortController().signal);
  };
  return { status, store, openPanel, closePanel, poll };
}

function skeletonRows(): NodeListOf<Element> {
  return document.querySelectorAll("#statusPopup > div.skeleton-row");
}

function mutedRow(): Element | null {
  return document.querySelector("#statusPopup .pop-item.muted");
}

describe("status: popover wiring", () => {
  it("the header button toggles the panel", async () => {
    await boot();
    expect(popover.anchor).toBe(document.getElementById("statusBtn"));

    document.getElementById("statusBtn")?.click();

    expect(popover.toggles).toBe(1);
  });
});

describe("status: first-open skeleton", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("an empty panel opening paints two skeleton rows with no show delay, and polls", async () => {
    const h = await boot();
    vi.useFakeTimers();

    h.openPanel();

    // The open hook is what seeds the panel; without its poll the panel would
    // sit on the skeleton until the next background poll came round.
    expect(actions.names()).toContain("status.poll");
    // showDelayMs 0: a panel that has never held content must not sit blank
    // for the primitive's default 150ms delay.
    vi.advanceTimersByTime(1);
    const rows = skeletonRows();
    expect(rows).toHaveLength(2);
    expect(rows[0]?.querySelector("div.skeleton")).not.toBeNull();
    expect(rows[1]?.querySelector("div.skeleton")).not.toBeNull();
  });

  it("the first data paint removes the skeleton rows it replaces", async () => {
    const h = await boot();
    vi.useFakeTimers();
    h.openPanel();
    vi.advanceTimersByTime(1);
    expect(skeletonRows()).toHaveLength(2);

    await h.poll({ activities: [entry({ id: "a1", detail: "Scanning A" })] });
    vi.advanceTimersByTime(300);

    // The rows arrive through reconcile(), which owns only children carrying
    // its key attribute — an unkeyed placeholder survives every later paint, so
    // the two bars would sit above the live rows for the lifetime of the page.
    expect(skeletonRows()).toHaveLength(0);
    expect(document.querySelector('[data-act-id="a1"]')).not.toBeNull();
  });

  it("re-opening a panel that already holds rows never wipes them with a skeleton", async () => {
    const h = await boot();
    // Paint real content first (nothing armed, so this paints straight).
    await h.poll({ activities: [entry({ id: "a1", detail: "Scanning A" })] });
    expect(document.querySelector('[data-act-id="a1"]')).not.toBeNull();
    vi.useFakeTimers();

    h.openPanel();
    vi.advanceTimersByTime(400);

    expect(skeletonRows()).toHaveLength(0);
    expect(document.querySelector('[data-act-id="a1"]')).not.toBeNull();
  });
});

describe("status: popup anti-flicker paint", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("the first data after an open waits out the skeleton's min-visible window", async () => {
    const h = await boot();
    vi.useFakeTimers();
    h.openPanel();
    vi.advanceTimersByTime(1);
    expect(skeletonRows()).toHaveLength(2);

    await h.poll({ activities: [entry({ id: "a1", detail: "Scanning A" })] });

    // Painting now would flash the skeleton away a millisecond after it
    // appeared — the flicker minVisibleMs exists to prevent.
    expect(skeletonRows()).toHaveLength(2);
    expect(document.querySelector('[data-act-id="a1"]')).toBeNull();

    vi.advanceTimersByTime(300);

    expect(document.querySelector('[data-act-id="a1"]')).not.toBeNull();
  });

  it("a newer poll supersedes the queued paint, so the deferred commit shows the freshest rows", async () => {
    const h = await boot();
    vi.useFakeTimers();
    h.openPanel();
    vi.advanceTimersByTime(1);
    await h.poll({ activities: [entry({ id: "a1", detail: "Scanning A" })] });

    await h.poll({ activities: [entry({ id: "b1", detail: "Scanning B" })] });

    // Still inside the min-visible window: neither poll has painted yet.
    expect(skeletonRows()).toHaveLength(2);

    vi.advanceTimersByTime(300);

    // The commit renders the LATEST snapshot, not the one queued first.
    expect(document.querySelector('[data-act-id="b1"]')).not.toBeNull();
    expect(document.querySelector('[data-act-id="a1"]')).toBeNull();
  });
});

describe("status: live timers tick only while the popup is open", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("opening starts the 1s tick, closing stops it", async () => {
    const h = await boot();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T10:00:30Z"));
    await h.poll({ activities: [entry({ id: "lt1", started_at: "2026-07-19T10:00:00Z" })] });
    const timer = (): string =>
      document.querySelector('#statusPopup [data-act-id="lt1"] .live-timer')?.textContent ?? "";
    expect(timer()).toBe(" \u00B7 30s");

    // Panel never opened: the clock advances, the row stays frozen — a
    // closed popup costs zero timer work.
    vi.setSystemTime(new Date("2026-07-19T10:01:30Z"));
    vi.advanceTimersByTime(5_000);
    expect(timer()).toBe(" \u00B7 30s");

    h.openPanel();
    vi.setSystemTime(new Date("2026-07-19T10:02:09Z"));
    vi.advanceTimersByTime(1_000); // the tick fires at 10:02:10
    expect(timer()).toBe(" \u00B7 2m 10s");

    h.closePanel();
    vi.setSystemTime(new Date("2026-07-19T10:05:00Z"));
    vi.advanceTimersByTime(10_000);
    expect(timer()).toBe(" \u00B7 2m 10s");
  });

  it("the tick re-renders only rows inside the popup", async () => {
    const h = await boot();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T10:00:30Z"));
    await h.poll({ activities: [entry({ id: "in1", started_at: "2026-07-19T10:00:00Z" })] });

    // A stray timer row OUTSIDE the popup: nothing ships one, and the
    // popup-rooted scan must not touch it either way.
    const stray = document.createElement("span");
    stray.className = "live-timer";
    stray.setAttribute("data-started", "2026-07-19T10:00:00Z");
    stray.textContent = " \u00B7 30s";
    document.body.appendChild(stray);

    h.openPanel();
    vi.setSystemTime(new Date("2026-07-19T10:02:09Z"));
    vi.advanceTimersByTime(1_000); // the tick fires at 10:02:10

    expect(
      document.querySelector('#statusPopup [data-act-id="in1"] .live-timer')?.textContent,
    ).toBe(" \u00B7 2m 10s");
    expect(stray.textContent).toBe(" \u00B7 30s");
  });
});

describe("status: the requests one poll issues", () => {
  it("an unconfigured server's alerts and activity both carry the poll's abort signal", async () => {
    const h = await boot();
    const signal = new AbortController().signal;

    await h.poll({ signal });

    // abortPoll() cancels the action, which aborts this signal — a request
    // issued without it keeps running after a disconnect.
    expect(wire.optsFor("listAlertsRaw")?.signal).toBe(signal);
    expect(wire.optsFor("listActivity")?.signal).toBe(signal);
  });

  it("an open popup's provider-health and stats requests carry it too", async () => {
    const h = await boot({ unconfigured: false });
    const signal = new AbortController().signal;

    await h.poll({ signal });

    expect(wire.optsFor("providerTimeouts")?.signal).toBe(signal);
    expect(wire.optsFor("stateStats")?.signal).toBe(signal);
  });

  it("a closed popup's provider-health request carries it as well", async () => {
    const h = await boot({ unconfigured: false });
    popover.isOpen = false;
    const signal = new AbortController().signal;

    await h.poll({ signal });

    expect(wire.optsFor("providerTimeouts")?.signal).toBe(signal);
    expect(wire.called("stateStats")).toBe(false);
  });

  it("the stop request addresses the activity by id", async () => {
    await boot();

    const req = actions.defs.get("activity.cancel")?.request?.("s 1");

    expect(req).toEqual({ method: "POST", path: "/api/activity/s%201/cancel" });
  });
});

describe("status: row controls contain their click", () => {
  it("dismissing a completed entry does not let the click reach the row's container", async () => {
    const h = await boot();
    const item = h.status.buildActivityItem(
      entry({ id: "d9", done: true, ended_at: "2026-07-19T10:05:00Z" }),
    );
    const host = document.createElement("div");
    host.appendChild(item);
    document.body.appendChild(host);
    let hostClicks = 0;
    host.addEventListener("click", () => {
      hostClicks += 1;
    });

    item
      .querySelector<HTMLButtonElement>('button[aria-label="Dismiss"]')
      ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(actions.names()).toContain("activity.dismiss");
    // A popup row is a click target in its own right; a dismiss that bubbled
    // would also trigger whatever the row itself does.
    expect(hostClicks).toBe(0);
  });

  it("stopping a running scan does not let the click reach the row's container", async () => {
    const h = await boot();
    const item = h.status.buildActivityItem(
      entry({ id: "r9", cancellable: true, kind: "series", media_id: 3 }),
    );
    const host = document.createElement("div");
    host.appendChild(item);
    document.body.appendChild(host);
    let hostClicks = 0;
    host.addEventListener("click", () => {
      hostClicks += 1;
    });

    item
      .querySelector<HTMLButtonElement>('button[aria-label="Stop scan"]')
      ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(actions.names()).toContain("activity.cancel");
    expect(hostClicks).toBe(0);
  });
});

describe("status: scan scheduling row", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("a disabled scan interval advertises no next scan", async () => {
    const h = await boot({ unconfigured: false });
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00Z"));

    // scan_interval_seconds 0 = scheduling off. The completion timestamp sits
    // AHEAD of the browser clock (a server clock a little fast is the ordinary
    // way that happens), which is the only shape in which a zero interval
    // could still compute a positive time-to-next-scan.
    await h.poll({
      stats: statsOf({ last_scan: "2026-07-19T11:00:00Z", scan_interval_seconds: 0 }),
      activities: [
        entry({ id: "fs1", action: "Full Scan", done: true, ended_at: "2026-07-19T12:30:00Z" }),
      ],
    });

    expect(mutedRow()?.textContent).toBe("Last scan: just now");
  });
});
