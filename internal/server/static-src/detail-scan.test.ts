// detail-scan.test.ts — granular scan starts and the store-driven button state.
//
// Both halves are load-bearing and neither was covered: detail.ts,
// coverage.ts and their tests all MOCK this module, so the assertions that
// exist prove only the handoff.
//
// The button half is the one worth the most: the module's own header states
// that feedback keys off the SHARED runningScansByScope map and never a local
// in-flight flag, because that is what makes a scan started in another tab (or
// before a reload) still show as running. A local flag would satisfy every
// caller-side test and break exactly that, so these tests drive the store.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as store from "./store.js";
import {
  triggerSeriesScan,
  triggerSeasonScan,
  triggerMovieScan,
  registerScanButton,
  initScanButtons,
  _scanButtonCountForTest,
} from "./detail-scan.js";
import { seriesScopeKey, seasonScopeKey, movieScopeKey } from "./scan-scope.js";
import {
  contentView,
  ownedByRoute,
  releaseRouteViews,
  seriesViewId,
  type Scope,
} from "./view-scope.js";
import type { RunningScan } from "./scan-scope.js";
import type { ScanAccepted } from "./wire/types.gen.js";
import type { SeriesItem, MovieDetail } from "./api-types.js";

interface ScanArgs {
  url: string;
  scopeKey: string;
}
interface ActionConfig {
  name: string;
  dedupe?: (a: ScanArgs) => string;
  request: (a: ScanArgs) => { method: string; path: string };
  decode: (data: unknown) => ScanAccepted;
  error?: string;
}
interface DispatchOpts {
  onSuccess?: (v: ScanAccepted) => void;
}

// The action is created at module scope, so the double is registered at import
// and the per-test wiring is a field write on the hoisted record (a vi.fn's
// implementation would be stripped by mockReset).
const action = vi.hoisted(() => ({
  config: null as unknown,
  dispatched: [] as ScanArgs[],
  accepted: { activity_id: "act-1", status: "accepted" } as ScanAccepted | null,
}));
vi.mock("@cplieger/actions", () => ({
  apiAction: (cfg: unknown) => {
    action.config = cfg;
    return {
      dispatch: (args: ScanArgs, opts?: DispatchOpts) => {
        action.dispatched.push(args);
        if (action.accepted !== null) {
          opts?.onSuccess?.(action.accepted);
        }
        return Promise.resolve(action.accepted);
      },
      cancel: () => undefined,
    };
  },
}));

const polls = vi.hoisted(() => ({ count: 0 }));
vi.mock("./status.js", () => ({
  pollStatus: () => {
    polls.count++;
    return Promise.resolve();
  },
}));

function config(): ActionConfig {
  return action.config as ActionConfig;
}

function series(id: number): SeriesItem {
  return { id, title: "Show" } as SeriesItem;
}

function movie(id: number): MovieDetail {
  return { id, title: "Film" } as MovieDetail;
}

// One mounted view per test, re-mounted in beforeEach; buttons that do not
// name a scope get a child of it, mirroring a render's per-row child scopes.
let testView: Scope = contentView.mount("test");
let rowSeq = 0;

/** A scan button as the render paths build it: the scope key in the dataset,
 *  an icon slot for the spinner to replace, mounted and REGISTERED into a
 *  disposal scope the way every render path registers its buttons at creation. */
function scanButton(
  scopeKey: string,
  scope: Scope = testView.child(`row-${String(++rowSeq)}`),
): HTMLButtonElement {
  const btn = document.createElement("button");
  btn.dataset["scanScope"] = scopeKey;
  const slot = document.createElement("span");
  slot.className = "icon icon-search";
  btn.appendChild(slot);
  document.body.appendChild(btn);
  registerScanButton(btn, scope);
  return btn;
}

function running(entries: [string, RunningScan][]): void {
  store.set("runningScansByScope", new Map(entries));
}

beforeEach(() => {
  action.dispatched.length = 0;
  action.accepted = { activity_id: "act-1", status: "accepted" };
  polls.count = 0;
  running([]);
  document.body.replaceChildren();
  // Mounting releases the previous test's view, so the registry starts empty.
  releaseRouteViews();
  testView = contentView.mount("test");
  rowSeq = 0;
});

describe("scan start endpoints", () => {
  it("posts the series scan path for a series", async () => {
    await triggerSeriesScan(series(42));

    expect(action.dispatched).toStrictEqual([
      { url: "/api/scan/series/42", scopeKey: seriesScopeKey(42) },
    ]);
  });

  it("posts the season scan path with both ids", async () => {
    await triggerSeasonScan(series(42), 3);

    expect(action.dispatched).toStrictEqual([
      { url: "/api/scan/season/42/3", scopeKey: seasonScopeKey(42, 3) },
    ]);
  });

  it("posts the movie scan path for a movie", async () => {
    await triggerMovieScan(movie(7));

    expect(action.dispatched).toStrictEqual([
      { url: "/api/scan/movie/7", scopeKey: movieScopeKey(7) },
    ]);
  });

  it("builds a POST request from the dispatched url", async () => {
    await triggerSeriesScan(series(42));

    expect(config().request({ url: "/api/scan/series/42", scopeKey: "x" })).toStrictEqual({
      method: "POST",
      path: "/api/scan/series/42",
    });
  });

  it("dedupes rapid clicks by endpoint, so two scopes never share a key", () => {
    const dedupe = config().dedupe;
    if (!dedupe) {
      throw new Error("scan action declares no dedupe key");
    }

    expect(dedupe({ url: "/api/scan/series/42", scopeKey: "a" })).toBe("scan:/api/scan/series/42");
    expect(dedupe({ url: "/api/scan/series/42", scopeKey: "a" })).not.toBe(
      dedupe({ url: "/api/scan/movie/42", scopeKey: "b" }),
    );
  });

  it("decodes the accepted response through the generated wire decoder", () => {
    // The activity id is what the optimistic marking and the cancel endpoint
    // both address, so a wrong decoder here loses the handle on a live scan.
    expect(config().decode({ activity_id: "act-9", status: "accepted" })).toStrictEqual({
      activity_id: "act-9",
      status: "accepted",
    });
    expect(() => config().decode({ status: "accepted" })).toThrow();
  });
});

describe("optimistic scope marking", () => {
  it("marks the scope running with the accepted activity id", async () => {
    await triggerSeriesScan(series(42));

    expect(store.get("runningScansByScope").get(seriesScopeKey(42))).toStrictEqual({
      activityId: "act-1",
      cancellable: true,
    });
  });

  it("keeps the scans other scopes already had", async () => {
    running([["movie::7:0:0", { activityId: "other", cancellable: false }]]);

    await triggerMovieScan(movie(9));

    expect([...store.get("runningScansByScope").keys()]).toStrictEqual([
      "movie::7:0:0",
      movieScopeKey(9),
    ]);
  });

  it("publishes a NEW map rather than mutating the store's own", async () => {
    // The store dedupes by identity, so mutating in place would leave every
    // subscriber (the button effect included) unnotified.
    const before = store.get("runningScansByScope");

    await triggerSeriesScan(series(42));

    expect(store.get("runningScansByScope")).not.toBe(before);
    expect(before.size).toBe(0);
  });

  it("polls the status endpoint so the poll-derived map reconciles", async () => {
    await triggerSeriesScan(series(42));

    expect(polls.count).toBe(1);
  });

  it("marks nothing and does not poll when the start failed", async () => {
    action.accepted = null;

    await triggerSeriesScan(series(42));

    expect(store.get("runningScansByScope").size).toBe(0);
    expect(polls.count).toBe(0);
  });
});

describe("registerScanButton", () => {
  it("leaves a button enabled when no scan covers its scope", () => {
    const btn = scanButton(seriesScopeKey(42));

    expect(btn.disabled).toBe(false);
    expect(btn.hasAttribute("aria-busy")).toBe(false);
    expect(btn.querySelector(".icon")).not.toBeNull();
  });

  it("disables the button and swaps the icon for a spinner while its scope runs", () => {
    running([[seriesScopeKey(42), { activityId: "a", cancellable: true }]]);

    const btn = scanButton(seriesScopeKey(42));

    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("aria-busy")).toBe("true");
    expect(btn.querySelector(".spinner")).not.toBeNull();
    expect(btn.querySelector(".icon")).toBeNull();
  });

  it("ignores a scan on a different scope", () => {
    running([[seasonScopeKey(42, 1), { activityId: "a", cancellable: true }]]);

    const btn = scanButton(seriesScopeKey(42));

    expect(btn.disabled).toBe(false);
  });

  it("never marks a button that carries no scope", () => {
    running([["", { activityId: "a", cancellable: true }]]);

    const btn = scanButton("");

    expect(btn.disabled).toBe(false);
  });
});

describe("initScanButtons", () => {
  it("repaints every annotated button when the running map changes", () => {
    const seriesBtn = scanButton(seriesScopeKey(42));
    const movieBtn = scanButton(movieScopeKey(7));
    initScanButtons();

    running([
      [seriesScopeKey(42), { activityId: "a", cancellable: true }],
      [movieScopeKey(7), { activityId: "b", cancellable: true }],
    ]);

    expect(seriesBtn.disabled).toBe(true);
    expect(movieBtn.disabled).toBe(true);
  });

  it("re-enables a button and restores its icon when its scan completes", () => {
    const btn = scanButton(seriesScopeKey(42));
    initScanButtons();
    running([[seriesScopeKey(42), { activityId: "a", cancellable: true }]]);
    expect(btn.querySelector(".spinner")).not.toBeNull();

    running([]);

    expect(btn.disabled).toBe(false);
    expect(btn.hasAttribute("aria-busy")).toBe(false);
    expect(btn.querySelector(".icon")).not.toBeNull();
    expect(btn.querySelector(".spinner")).toBeNull();
  });

  it("leaves a button on an unaffected scope alone", () => {
    const other = scanButton(movieScopeKey(7));
    initScanButtons();

    running([[seriesScopeKey(42), { activityId: "a", cancellable: true }]]);

    expect(other.disabled).toBe(false);
  });
});

describe("the mounted-button registry (R8.5)", () => {
  it("repaints registered buttons without a document-wide scan", () => {
    const btn = scanButton(seriesScopeKey(42));
    initScanButtons();
    const qsa = vi.spyOn(document, "querySelectorAll");

    running([[seriesScopeKey(42), { activityId: "a", cancellable: true }]]);

    expect(btn.disabled).toBe(true);
    expect(qsa).not.toHaveBeenCalled();
    qsa.mockRestore();
  });

  it("an unregistered annotated button is not managed", () => {
    // The registry is the population: a data-scan-scope button nothing
    // registered (the old document scan would have found it) stays untouched.
    const btn = document.createElement("button");
    btn.dataset["scanScope"] = seriesScopeKey(42);
    const slot = document.createElement("span");
    slot.className = "icon icon-search";
    btn.appendChild(slot);
    document.body.appendChild(btn);
    initScanButtons();

    running([[seriesScopeKey(42), { activityId: "a", cancellable: true }]]);

    expect(btn.disabled).toBe(false);
    expect(btn.querySelector(".spinner")).toBeNull();
  });

  it("a disposed scope releases its buttons, so no publish can reach them", () => {
    // The observable the registry exists for: a button whose subtree is gone
    // must never be written to again. Whether it is still in the document is
    // beside the point — the scope decides, not the DOM.
    const goneRow = testView.child("row-gone");
    const gone = scanButton(seriesScopeKey(1), goneRow);
    const live = scanButton(seriesScopeKey(1), testView.child("row-live"));
    initScanButtons();

    goneRow.dispose();
    running([[seriesScopeKey(1), { activityId: "a", cancellable: true }]]);

    expect(gone.disabled).toBe(false);
    expect(gone.hasAttribute("aria-busy")).toBe(false);
    expect(gone.querySelector(".spinner")).toBeNull();
    // Same scope key, a scope still open: the registry did not lose it.
    expect(live.disabled).toBe(true);
  });

  it("a row scope re-opened by a repaint releases the previous paint's button", () => {
    // Every repaint of a row rebuilds its cells, so each paint registers a NEW
    // button and abandons the last. Re-opening the row's scope is what keeps
    // the registry from growing once per repaint forever.
    scanButton(seriesScopeKey(1), testView.child("row-1"));
    scanButton(seriesScopeKey(1), testView.child("row-1"));
    const last = scanButton(seriesScopeKey(1), testView.child("row-1"));

    expect(_scanButtonCountForTest()).toBe(1);

    initScanButtons();
    running([[seriesScopeKey(1), { activityId: "a", cancellable: true }]]);
    expect(last.disabled).toBe(true);
  });

  it("a released row scope drops that row's button and leaves its siblings", () => {
    const removed = scanButton(seriesScopeKey(1), testView.child("row-1"));
    const kept = scanButton(seriesScopeKey(2), testView.child("row-2"));
    initScanButtons();

    testView.release("row-1");
    running([
      [seriesScopeKey(1), { activityId: "a", cancellable: true }],
      [seriesScopeKey(2), { activityId: "b", cancellable: true }],
    ]);

    expect(removed.disabled).toBe(false);
    expect(kept.disabled).toBe(true);
    expect(_scanButtonCountForTest()).toBe(1);
  });

  it("a route leave releases the buttons the departed view registered", () => {
    // The router's leave path (page-leg's abortPageLeg) releases every view the
    // route mounted, which unregisters their buttons: a detail view's Search
    // buttons must not be repainted after the user has left it.
    const view = contentView.mount(seriesViewId("1"));
    ownedByRoute(view);
    const departed = scanButton(seasonScopeKey(1, 1), view.child("head-1"));
    initScanButtons();

    releaseRouteViews();
    running([[seasonScopeKey(1, 1), { activityId: "a", cancellable: true }]]);

    expect(departed.disabled).toBe(false);
    expect(departed.querySelector(".spinner")).toBeNull();
    expect(_scanButtonCountForTest()).toBe(0);
  });

  it("registers nothing when the scope is already disposed", () => {
    // A late registration would be unreleasable: the subtree it belongs to is
    // already gone, so the disposer runs immediately instead.
    const row = testView.child("row-1");
    row.dispose();

    const btn = scanButton(seriesScopeKey(1), row);
    initScanButtons();
    running([[seriesScopeKey(1), { activityId: "a", cancellable: true }]]);

    expect(btn.disabled).toBe(false);
    expect(_scanButtonCountForTest()).toBe(0);
  });
});
