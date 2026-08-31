// sync.season.test.ts — the server-owned season sync dialog (D2/D3).
//
// The season UI's whole contract: ONE POST (no client fan-out — the server
// enumerates the files), a view driven from the registry filtered by
// batch_activity_id plus each item's own sync:done, and a reload that
// re-attaches through the jobs read. The network edges are mocked
// (sync-actions.js for the dispatch, wire/client.gen.js for the registry
// read, sync-jobs.js for settlement watches); everything the user sees is
// asserted through the real dialog DOM.
import { describe, it, expect, vi, beforeAll, afterAll, beforeEach } from "vitest";

const dispatchSeason = vi.hoisted(() => vi.fn());
const dispatchAudio = vi.hoisted(() => vi.fn());
const syncJobsMock = vi.hoisted(() => vi.fn());
const cancelActivityMock = vi.hoisted(() => vi.fn());
const watchMock = vi.hoisted(() => vi.fn());
const unwatchMock = vi.hoisted(() => vi.fn());

vi.mock("./sync-actions.js", () => ({
  audioSyncAction: { dispatch: dispatchAudio },
  saveManualOffsetAction: { dispatch: vi.fn() },
  seasonSyncAction: { dispatch: dispatchSeason },
}));

vi.mock("./sync-jobs.js", () => ({
  attachSyncJob: vi.fn(),
  watchSyncJob: watchMock,
  syncDoneFromEvent: vi.fn(),
  clearSyncCorrelation: vi.fn(),
}));

// The activity store (task 11): sync.ts watches the BATCH activity's
// terminal transition for the no-event terminal arms. Captured observers
// are fired by hand with scripted snapshots.
const activityObs = vi.hoisted(() => ({
  fns: new Set<(activities: readonly unknown[]) => void>(),
}));
vi.mock("./status.js", () => ({
  observeActivities: (fn: (activities: readonly unknown[]) => void) => {
    activityObs.fns.add(fn);
    return () => {
      activityObs.fns.delete(fn);
    };
  },
}));

vi.mock("./wire/client.gen.js", () => ({
  previewStart: vi.fn(),
  syncJobs: syncJobsMock,
  cancelActivity: cancelActivityMock,
  PATH_PREVIEW_POSTER: "/api/preview/poster",
  PATH_PREVIEW_SUBTITLE: "/api/preview/subtitle",
  PATH_PREVIEW_VIDEO: "/api/preview/video",
}));

vi.mock("./notify.js", () => ({
  success: vi.fn(),
  error: vi.fn(),
  warn: vi.fn(),
  info: vi.fn(),
}));

import { confirmSeasonSync } from "./sync.js";
import * as notify from "./notify.js";
import type { Job, SyncDoneEvent } from "./wire/types.gen.js";

/** One registry item record for the fixture batch. */
function job(jobId: number, episode: number, state: Job["state"], over: Partial<Job> = {}): Job {
  return {
    accepted_at: "2026-08-31T09:00:00Z",
    activity_id: "act-7",
    batch_activity_id: "act-7",
    state,
    file_ref: {
      media_type: "episode",
      media_id: `tvdb-81189-s01e${String(episode).padStart(2, "0")}`,
      language: "en",
      variant: "standard",
      source: "external",
    },
    job_id: jobId,
    series_id: 42,
    season: 1,
    ordinal: episode,
    ...over,
  };
}

function dlg(): HTMLDialogElement {
  return document.getElementById("seasonSyncConfirm") as HTMLDialogElement;
}

function button(name: RegExp): HTMLButtonElement {
  const found = Array.from(dlg().querySelectorAll("button")).find((b) =>
    name.test(b.textContent ?? ""),
  );
  if (!found) {
    throw new Error(`no button matching ${String(name)}; have: ${dlg().textContent ?? ""}`);
  }
  return found;
}

/** The watcher the dialog registered for jobId (fails loudly when absent). */
function watcherFor(jobId: number): (ev: SyncDoneEvent | null) => void {
  const call = watchMock.mock.calls.find((c) => c[0] === jobId);
  if (!call) {
    throw new Error(
      `no watcher for job ${String(jobId)}; ${String(watchMock.mock.calls.length)} calls`,
    );
  }
  return call[1] as (ev: SyncDoneEvent | null) => void;
}

let host: HTMLDialogElement;

beforeAll(() => {
  host = document.createElement("dialog");
  host.id = "seasonSyncConfirm";
  host.tabIndex = -1;
  document.body.appendChild(host);
});

afterAll(() => {
  host.remove();
});

beforeEach(async () => {
  if (dlg().open) {
    // Chromium QUEUES the close event as a task instead of dispatching it
    // synchronously, on a different task queue than timers — so wait for
    // the event itself. An undrained stale close lands mid-test and
    // consumes the next dialog's once-close teardown listener.
    const closed = new Promise((r) => {
      dlg().addEventListener("close", r, { once: true });
    });
    dlg().close();
    await closed;
  }
  activityObs.fns.clear();
  dispatchSeason.mockReset();
  dispatchAudio.mockReset();
  syncJobsMock.mockReset();
  syncJobsMock.mockResolvedValue([]); // default: no live batch to re-attach
  cancelActivityMock.mockReset();
  cancelActivityMock.mockResolvedValue(true);
  watchMock.mockReset();
  watchMock.mockReturnValue(unwatchMock);
});

describe("confirmSeasonSync: one POST, no client fan-out", () => {
  it("dispatches exactly one season request and never a per-file sync", async () => {
    dispatchSeason.mockReturnValue({
      outcome: Promise.resolve({ status: "success", value: { activity_id: "act-7" } }),
    });
    confirmSeasonSync("Breaking Bad", 1, 42, 3);
    expect(dlg().open).toBe(true);
    expect(dlg().textContent).toContain("3 subtitle files in Breaking Bad S01");

    button(/Start Sync/).click();
    await vi.waitFor(() => {
      expect(dispatchSeason).toHaveBeenCalledTimes(1);
    });
    expect(dispatchSeason).toHaveBeenCalledWith({ series_id: 42, season: 1 });
    // The server enumerates; the client never fans out per file.
    expect(dispatchAudio).not.toHaveBeenCalled();

    // The batch view loads from the registry filtered by the 202's id.
    await vi.waitFor(() => {
      expect(syncJobsMock).toHaveBeenCalledWith({ batch_activity_id: "act-7" });
    });
  });

  it("renders the typed cap refusal inline — one surface, no toast", async () => {
    dispatchSeason.mockReturnValue({
      outcome: Promise.resolve({ status: "error", error: { status: 429 } }),
    });
    confirmSeasonSync("Breaking Bad", 1, 42, 2);
    button(/Start Sync/).click();

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Sync queue is full");
    });
    expect(notify.error).not.toHaveBeenCalled();
  });
});

describe("the batch view drives from the registry", () => {
  it("renders per-item results from the registry rows", async () => {
    dispatchSeason.mockReturnValue({
      outcome: Promise.resolve({ status: "success", value: { activity_id: "act-7" } }),
    });
    syncJobsMock.mockImplementation((query?: Record<string, unknown>) => {
      if (query && query["batch_activity_id"] === "act-7") {
        return Promise.resolve([
          job(11, 1, "done", {
            outcome: "result",
            applied: true,
            offset_ms: -750,
            confidence: 0.93,
          }),
          job(12, 2, "running"),
          job(13, 3, "queued"),
        ]);
      }
      return Promise.resolve([]);
    });

    confirmSeasonSync("Breaking Bad", 1, 42, 3);
    button(/Start Sync/).click();

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("S01E01");
    });
    const text = dlg().textContent ?? "";
    expect(text).toContain("-0.750s (93%)"); // the applied item's outcome
    expect(text).toContain("S01E02");
    expect(text).toContain("analyzing");
    expect(text).toContain("S01E03");
    expect(text).toContain("queued");
    expect(text).toContain("Syncing 1/3");
    // Item rows carry NO cancel affordance: the only buttons are the
    // batch-level Stop and Close.
    const labels = Array.from(dlg().querySelectorAll("button")).map((b) =>
      (b.textContent ?? "").trim(),
    );
    expect(labels.filter((l) => l !== "")).toEqual(["Stop", "Close"]);
  });

  it("a sync:done settles only its own row, and the aggregate follows", async () => {
    dispatchSeason.mockReturnValue({
      outcome: Promise.resolve({ status: "success", value: { activity_id: "act-7" } }),
    });
    syncJobsMock.mockImplementation((query?: Record<string, unknown>) =>
      Promise.resolve(
        query && query["batch_activity_id"] === "act-7"
          ? [job(11, 1, "queued"), job(12, 2, "queued")]
          : [],
      ),
    );

    confirmSeasonSync("Breaking Bad", 1, 42, 2);
    button(/Start Sync/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalledTimes(2);
    });

    watcherFor(11)({
      job_id: 11,
      batch_activity_id: "act-7",
      file_ref: {
        media_type: "episode",
        media_id: "tvdb-81189-s01e01",
        language: "en",
        variant: "standard",
        source: "external",
      },
      offset_ms: 420,
      confidence: 0.9,
      method: "audio",
      applied: true,
      dry_run: false,
    });

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("+0.420s (90%)");
    });
    const text = dlg().textContent ?? "";
    // Its sibling is untouched by job 11's settlement.
    expect(text).toContain("S01E02 · English — queued");
    expect(text).toContain("Syncing 1/2");
  });
});

describe("reload re-attach", () => {
  it("shows a live batch's progress instead of a second confirm", async () => {
    // The unfiltered registry read finds a live batch for this scope (a
    // reload ago); the filtered read then feeds the batch view.
    syncJobsMock.mockImplementation((query?: Record<string, unknown>) =>
      Promise.resolve(
        query && query["batch_activity_id"] === "act-7"
          ? [
              job(11, 1, "done", {
                outcome: "result",
                applied: true,
                offset_ms: 420,
                confidence: 0.9,
              }),
              job(12, 2, "running"),
            ]
          : [job(12, 2, "running")],
      ),
    );

    confirmSeasonSync("Breaking Bad", 1, 42, 2);

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Syncing 1/2");
    });
    // Re-attached, never re-dispatched: the batch is the server's.
    expect(dispatchSeason).not.toHaveBeenCalled();
    expect(dlg().textContent).toContain("S01E02");
  });

  it("a batch of another scope does not capture the dialog", async () => {
    syncJobsMock.mockResolvedValue([job(12, 2, "running", { series_id: 99, season: 4 })]);

    confirmSeasonSync("Breaking Bad", 1, 42, 2);
    // The confirm view stays: the live batch belongs to another season.
    await vi.waitFor(() => {
      expect(syncJobsMock).toHaveBeenCalled();
    });
    expect(dlg().textContent).toContain("Start Sync");
    expect(dispatchSeason).not.toHaveBeenCalled();
  });
});

describe("stopping the batch", () => {
  it("Stop cancels the BATCH activity and re-reads the registry", async () => {
    dispatchSeason.mockReturnValue({
      outcome: Promise.resolve({ status: "success", value: { activity_id: "act-7" } }),
    });
    let stopped = false;
    syncJobsMock.mockImplementation((query?: Record<string, unknown>) => {
      if (!query || query["batch_activity_id"] !== "act-7") {
        return Promise.resolve([]);
      }
      return Promise.resolve(
        stopped
          ? [
              job(11, 1, "done", { outcome: "cancelled", error: "context canceled" }),
              job(12, 2, "done", { outcome: "cancelled", error: "context canceled" }),
            ]
          : [job(11, 1, "running"), job(12, 2, "queued")],
      );
    });

    confirmSeasonSync("Breaking Bad", 1, 42, 2);
    button(/Start Sync/).click();
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Syncing 0/2");
    });

    stopped = true;
    button(/^Stop$/).click();
    await vi.waitFor(() => {
      expect(cancelActivityMock).toHaveBeenCalledWith("act-7");
    });
    // Queued items settle server-side WITHOUT events; the re-read shows it.
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Done: 0 synced, 2 failed");
    });
  });

  it("a popup stop landing between items reconciles the OPEN dialog via the batch activity terminal", async () => {
    dispatchSeason.mockReturnValue({
      outcome: Promise.resolve({ status: "success", value: { activity_id: "act-7" } }),
    });
    let stopped = false;
    syncJobsMock.mockImplementation((query?: Record<string, unknown>) => {
      if (!query || query["batch_activity_id"] !== "act-7") {
        return Promise.resolve([]);
      }
      return Promise.resolve(
        stopped
          ? [
              job(11, 1, "done", {
                outcome: "result",
                applied: true,
                offset_ms: 420,
                confidence: 0.9,
              }),
              job(12, 2, "done", { outcome: "cancelled", error: "context canceled" }),
            ]
          : [
              job(11, 1, "done", {
                outcome: "result",
                applied: true,
                offset_ms: 420,
                confidence: 0.9,
              }),
              job(12, 2, "queued"),
            ],
      );
    });

    confirmSeasonSync("Breaking Bad", 1, 42, 2);
    button(/Start Sync/).click();
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Syncing 1/2");
    });

    // The stop was dispatched from the ACTIVITY POPUP and landed BETWEEN
    // items: the queued sibling settles cancelled server-side with no
    // sync:done of its own. The batch activity's terminal upsert is the
    // only delivery the open dialog gets.
    stopped = true;
    const terminal = {
      started_at: "2026-08-31T09:00:00Z",
      id: "act-7",
      action: "Season Sync",
      detail: "Breaking Bad S01",
      source: "manual",
      done: true,
      cancelled: true,
    };
    for (const fn of [...activityObs.fns]) {
      fn([terminal]);
    }

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("stopped");
    });
    expect(dlg().textContent).toContain("Done: 1 synced, 1 failed");
    // The dialog's own Stop button was never the trigger.
    expect(cancelActivityMock).not.toHaveBeenCalled();
  });
});
