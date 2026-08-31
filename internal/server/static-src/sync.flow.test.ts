// Behaviour of the sync dialog, driven through its real public API.
//
// Two exports, and both are exercised here: openSyncDialog and
// consumeSyncClosing. Nothing private is reached for and no markup shape is
// asserted — the queries are by accessible role and visible text, so a layout
// change cannot redden this file while a behaviour change can. That is the
// distinction between a behaviour test and the change-detector the earlier
// analysis rejected.
//
// PER-TEST ISOLATION WITHOUT A RESET HOOK. This module holds state at module
// scope, so the usual worry is leakage between cases. openSyncDialog is itself
// the reset: it disposes the prior offset effect and save binding, recreates
// the offset signal from the incoming entry, and fully reassigns syncState. So
// calling it per test IS a clean arrange, and no _resetForTest export is
// needed. The one thing it does not clear is the one-shot syncClosing flag,
// which the close test drains deliberately.
//
// The network edge is mocked, not the dialog's own helpers: sync-actions.js is
// where this module stops being subflux's UI and starts being HTTP.
import { describe, it, expect, vi, beforeAll, afterAll, beforeEach, afterEach } from "vitest";

const dispatchAudio = vi.hoisted(() => vi.fn());
const dispatchOffset = vi.hoisted(() => vi.fn());
const attachMock = vi.hoisted(() => vi.fn());
const watchMock = vi.hoisted(() => vi.fn());
const unwatchMock = vi.hoisted(() => vi.fn());

// Mocked as whole modules because these are the two the dialog dispatches
// through; a partial factory that stopped matching the export set would fail
// collection rather than fail a test.
vi.mock("./sync-actions.js", () => ({
  audioSyncAction: { dispatch: dispatchAudio },
  saveManualOffsetAction: { dispatch: dispatchOffset },
  seasonSyncAction: { dispatch: vi.fn() },
}));

// The settlement registry is the dialog's other network edge: watch/attach
// are captured so tests deliver sync:done results and reload states.
vi.mock("./sync-jobs.js", () => ({
  attachSyncJob: attachMock,
  watchSyncJob: watchMock,
  syncDoneFromEvent: vi.fn(),
  clearSyncCorrelation: vi.fn(),
}));

vi.mock("./notify.js", () => ({
  success: vi.fn(),
  error: vi.fn(),
  warn: vi.fn(),
  info: vi.fn(),
}));

import { openSyncDialog, consumeSyncClosing } from "./sync.js";
import * as notify from "./notify.js";
import type { SubtitleEntry, MediaType } from "./api-types.js";
import type { SyncDoneEvent } from "./wire/types.gen.js";

/** A dispatch handle whose outcome resolves a 202 with the given job id. */
function acceptedHandle(jobId: number): { outcome: Promise<unknown> } {
  return {
    outcome: Promise.resolve({
      status: "success",
      value: { activity_id: `act-${String(jobId)}`, job_id: jobId },
    }),
  };
}

/** A dispatch handle whose outcome resolves a typed error. */
function errorHandle(err: unknown): { outcome: Promise<unknown> } {
  return { outcome: Promise.resolve({ status: "error", error: err }) };
}

/** The sync:done payload for the harness entry's FileRef. */
function doneFor(jobId: number, over: Partial<SyncDoneEvent> = {}): SyncDoneEvent {
  return {
    job_id: jobId,
    outcome: "result",
    file_ref: {
      media_type: "episode",
      media_id: "tvdb-1-s01e05",
      language: "en",
      variant: "standard",
      source: "external",
    },
    offset_ms: -750,
    confidence: 0.93,
    method: "audio",
    applied: true,
    dry_run: true,
    ...over,
  };
}

/** The callback the dialog registered for jobId (fails loudly when absent). */
function watcherFor(jobId: number): (ev: SyncDoneEvent | null) => void {
  const call = watchMock.mock.calls.find((c) => c[0] === jobId);
  if (!call) {
    throw new Error(
      `no watcher registered for job ${String(jobId)}; calls: ${String(watchMock.mock.calls.length)}`,
    );
  }
  return call[1] as (ev: SyncDoneEvent | null) => void;
}

/** A subtitle entry with only the fields the dialog reads. */
function entry(over: Partial<SubtitleEntry> = {}): SubtitleEntry {
  return {
    language: "en",
    variant: "",
    offset_ms: 0,
    media_id: "tvdb-1-s01e05",
    ...over,
  } as SubtitleEntry;
}

/** The dialog resolves #syncDialog lazily on first open and caches it, because
 *  the real page declares it once (index.html:54) and nothing ever replaces it.
 *  The harness matches that: ONE host for the file, mounted before the first
 *  open. Recreating it per test would leave the module's cache pointing at a
 *  detached element from the previous case — the harness fighting production
 *  rather than reproducing it. */
function mountDialogHost(): HTMLDialogElement {
  const dlg = document.createElement("dialog");
  dlg.id = "syncDialog";
  dlg.tabIndex = -1;
  document.body.appendChild(dlg);
  return dlg;
}

function open(entries: SubtitleEntry[] = [entry()], mediaId = 42, label = "Show S01E05"): void {
  openSyncDialog(entries, "series" as MediaType, mediaId, label);
}

const dlg = (): HTMLDialogElement => document.getElementById("syncDialog") as HTMLDialogElement;

function button(name: RegExp): HTMLButtonElement {
  const found = Array.from(dlg().querySelectorAll("button")).find((b) =>
    name.test(b.textContent ?? ""),
  );
  if (!found) {
    throw new Error(`no button matching ${String(name)}; have: ${dlg().textContent ?? ""}`);
  }
  return found;
}

let host: HTMLDialogElement;
let startPath: string;

beforeAll(() => {
  startPath = location.pathname;
  host = mountDialogHost();
});

afterAll(() => {
  host.remove();
  history.replaceState(null, "", startPath);
});

beforeEach(() => {
  // A real sync-capable route, not the runner's "/". The dialog derives its
  // history entry as `${pathname}/sync`, so at "/" that would be "//sync",
  // which a browser reads as protocol-relative and refuses. Production cannot
  // reach that (router.ts:209-216 hosts the dialog only under /series/{id} and
  // /movie/{id}, with or without /files), so this is the harness matching
  // reality rather than a bug being papered over.
  history.replaceState(null, "", "/series/42");
  dispatchAudio.mockReset();
  dispatchOffset.mockReset();
  attachMock.mockReset();
  attachMock.mockResolvedValue({ kind: "none" });
  watchMock.mockReset();
  unwatchMock.mockReset();
  watchMock.mockReturnValue(unwatchMock);
});

afterEach(() => {
  // Drain the one-shot flag so a close in one test cannot answer another's
  // consumeSyncClosing. Per-test state isolation otherwise comes from
  // openSyncDialog itself, which reassigns syncState and recreates the offset
  // signal on every call.
  consumeSyncClosing();
});

describe("openSyncDialog", () => {
  it("opens the dialog and names the media", () => {
    open([entry()], 42, "The Wire S01E05");
    expect(dlg().open).toBe(true);
    expect(dlg().textContent).toContain("The Wire S01E05");
  });

  it("offers every subtitle in the dropdown, labelled", () => {
    open([entry({ language: "en" }), entry({ language: "fr" }), entry({ language: "en" })]);
    const sel = dlg().querySelector("select") as HTMLSelectElement;
    expect(Array.from(sel.options, (o) => o.textContent)).toEqual([
      "English #1",
      "French",
      "English #2",
    ]);
  });

  it("seeds the offset from the first entry", () => {
    open([entry({ offset_ms: 2500 })]);
    // The timecode widget renders the seeded value; asserting the rendered text
    // rather than the signal keeps this a behaviour assertion.
    expect(dlg().textContent).toMatch(/2[.,]500|02\.500|2\.5/);
  });

  it("pushes a /sync history entry", () => {
    open();
    expect(location.pathname.endsWith("/sync")).toBe(true);
  });

  it("does not stack a second /sync entry when already there", () => {
    open();
    const afterFirst = location.pathname;
    open();
    expect(location.pathname).toBe(afterFirst);
    expect(location.pathname.endsWith("/sync/sync")).toBe(false);
  });

  it("omits the video preview when there is no arr id to resolve it", () => {
    // Without a media id the server cannot find the video file, so offering a
    // play control would be a dead button.
    open([entry()], 0);
    expect(dlg().querySelector("#sync-preview-container")).toBeNull();
  });

  it("offers the video preview when an arr id is present", () => {
    open([entry()], 42);
    expect(dlg().querySelector("#sync-preview-container")).not.toBeNull();
  });

  it("reopens cleanly, leaving one dialog and the newest media label", () => {
    // A reopen without an intervening close is a real path (the earlier design
    // notes call it out) and must not accumulate state or duplicate the dialog.
    open([entry()], 42, "First");
    open([entry()], 42, "Second");
    expect(document.querySelectorAll("#syncDialog").length).toBe(1);
    expect(dlg().textContent).toContain("Second");
    expect(dlg().textContent).not.toContain("First");
  });

  it("re-opens add no dialog-chrome listeners: the chrome is wired once (F3)", () => {
    open(); // whatever ran before, the chrome is wired by now
    const listeners = vi.spyOn(dlg(), "addEventListener");

    open();
    open();

    // Per-open wiring leaked one close and one cancel listener per open;
    // the chrome (backdrop press pair, close re-arm, Escape override) now
    // lives with the permanent element.
    const chrome = listeners.mock.calls.filter(([type]) =>
      ["mousedown", "mouseup", "close", "cancel"].includes(type),
    );
    expect(chrome).toEqual([]);
    listeners.mockRestore();
  });

  it("a backdrop press closes the dialog, on the first open and on reopens alike", async () => {
    const press = (): void => {
      dlg().dispatchEvent(new MouseEvent("mousedown"));
      dlg().dispatchEvent(new MouseEvent("mouseup"));
    };
    open();
    press();
    expect(consumeSyncClosing()).toBe(true);
    await vi.waitFor(() => {
      expect(location.pathname.endsWith("/sync")).toBe(false);
    });

    open();
    press();

    expect(consumeSyncClosing()).toBe(true);
  });
});

describe("saving a manual offset", () => {
  it("dispatches the offset the user has, and reports it", async () => {
    dispatchOffset.mockResolvedValue({});
    open([entry({ offset_ms: 1500 })]);

    button(/Save Offset/).click();
    await vi.waitFor(() => {
      expect(dispatchOffset).toHaveBeenCalledTimes(1);
    });

    expect(dispatchOffset.mock.calls[0]?.[0]).toMatchObject({ offset_ms: 1500 });
    expect(notify.success).toHaveBeenCalled();
  });

  it("closes the dialog once the save lands", async () => {
    dispatchOffset.mockResolvedValue({});
    open([entry({ offset_ms: 1500 })]);

    button(/Save Offset/).click();
    await vi.waitFor(() => {
      expect(dispatchOffset).toHaveBeenCalled();
    });
    await vi.waitFor(() => {
      expect(dlg().open).toBe(false);
    });
  });

  it("keeps the dialog open when the save fails", async () => {
    // The action reports null on failure. Closing would discard the user's
    // offset with nowhere to retry it from.
    //
    // Asserted through consumeSyncClosing rather than dlg().open, because the
    // close is ANIMATED: closeDialog defers the actual close behind a fade, so
    // `open` is still true for a beat afterwards and a naive assertion here
    // passes whether or not the dialog closed. closeSyncDialog sets the closing
    // flag synchronously before it touches history, so the flag is the
    // deterministic public observable. Verified: with a `closeSyncDialog()`
    // planted on the failure path, this fails and the `open` check does not.
    dispatchOffset.mockResolvedValue(null);
    open([entry({ offset_ms: 1500 })]);

    button(/Save Offset/).click();
    await vi.waitFor(() => {
      expect(dispatchOffset).toHaveBeenCalled();
    });

    expect(consumeSyncClosing()).toBe(false);
    expect(location.pathname.endsWith("/sync")).toBe(true);
    expect(notify.success).not.toHaveBeenCalled();
  });

  it("refuses to save with no subtitle selected", async () => {
    open([]);
    button(/Save Offset/).click();
    await vi.waitFor(() => {
      expect(notify.error).toHaveBeenCalled();
    });
    expect(dispatchOffset).not.toHaveBeenCalled();
  });
});

describe("sync to audio (async job)", () => {
  it("dispatches once, resolves fast, and shows the analyzing state", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();

    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(dispatchAudio).toHaveBeenCalledTimes(1);
    });

    // The 202 hands over the job; the dialog watches it and says so.
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalledWith(7, expect.any(Function));
      expect(dlg().textContent).toContain("Analyzing audio");
    });
    // The dispatch is instant, so the button is usable again while the
    // analysis runs server-side.
    expect(button(/Sync to Audio/).disabled).toBe(false);
  });

  it("asks the server for a dry run, never a blind write", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(dispatchAudio).toHaveBeenCalled();
    });
    expect(dispatchAudio.mock.calls[0]?.[0]).toMatchObject({ dry_run: true });
  });

  it("applies a confident result when ITS sync:done arrives (matched on job_id)", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalled();
    });

    watcherFor(7)(doneFor(7));

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("93%");
    });
    expect(notify.success).toHaveBeenCalled();
  });

  it("reports low confidence and changes nothing", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalled();
    });

    watcherFor(7)(doneFor(7, { applied: false, confidence: 0.12, offset_ms: 4000 }));

    await vi.waitFor(() => {
      expect(dlg().textContent).toMatch(/Low confidence/);
    });
    expect(notify.success).not.toHaveBeenCalled();
  });

  it("renders a crashed job's error inline", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalled();
    });

    watcherFor(7)(doneFor(7, { applied: false, outcome: "crash", error: "ffmpeg exploded" }));

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Audio sync failed: ffmpeg exploded");
    });
    expect(notify.success).not.toHaveBeenCalled();
  });

  it("renders a STOPPED job distinctly from a crashed one, with no registry re-read", async () => {
    // Both terminals carry an error string (a stop is context.Canceled), so
    // the typed outcome is the only discriminator — and it rides the event,
    // so the dialog never has to go back to the jobs read to find out.
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    await vi.waitFor(() => {
      expect(attachMock).toHaveBeenCalledTimes(1); // the open's own re-attach
    });
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalled();
    });

    watcherFor(7)(doneFor(7, { applied: false, outcome: "cancelled", error: "context canceled" }));

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Audio sync was stopped.");
    });
    const text = dlg().textContent ?? "";
    expect(text).not.toContain("failed");
    expect(text).not.toContain("did not complete");
    expect(text).not.toContain("context canceled");
    expect(attachMock).toHaveBeenCalledTimes(1);
    expect(notify.success).not.toHaveBeenCalled();
  });

  it("renders a timed-out job distinctly", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalled();
    });

    watcherFor(7)(
      doneFor(7, { applied: false, outcome: "timeout", error: "analysis exceeded 15m0s" }),
    );

    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Audio sync ran out of time");
    });
    expect(dlg().textContent).not.toContain("failed");
  });

  it("renders the capacity 429 inline — exactly ONE visible surface, one dispatch", async () => {
    // The typed cap refusal displaces the failure toast for the 429 arm: the
    // dialog's inline result path is the only place the message appears.
    dispatchAudio.mockReturnValue(errorHandle({ status: 429, message: "sync queue is full" }));
    open();

    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Sync queue is full");
    });
    expect(dispatchAudio).toHaveBeenCalledTimes(1);
    expect(notify.error).not.toHaveBeenCalled();
    expect(notify.success).not.toHaveBeenCalled();
    // Usable again: the user retries when a slot frees, by clicking.
    expect(button(/Sync to Audio/).disabled).toBe(false);
  });

  it("keeps the failure toast for NON-capacity errors", async () => {
    dispatchAudio.mockReturnValue(errorHandle({ status: 500 }));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(notify.error).toHaveBeenCalled();
    });
    await vi.waitFor(() => {
      expect(button(/Sync to Audio/).disabled).toBe(false);
    });
  });

  it("close-dialog drops the WATCH, not the job — the analysis continues", async () => {
    dispatchAudio.mockReturnValue(acceptedHandle(7));
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(watchMock).toHaveBeenCalled();
    });

    dlg().dispatchEvent(new Event("cancel", { cancelable: true }));

    // The dialog unwatches (nothing renders into a closed dialog) and never
    // cancels the dispatch or the job: there is no abort surface at all.
    await vi.waitFor(() => {
      expect(unwatchMock).toHaveBeenCalled();
    });
    consumeSyncClosing();
  });

  it("reload re-attach: a QUEUED job renders the queued state", async () => {
    attachMock.mockResolvedValue({
      kind: "live",
      job: { job_id: 9, state: "queued" },
    });
    open();
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Queued for analysis");
    });
    expect(watchMock).toHaveBeenCalledWith(9, expect.any(Function));
  });

  it("reload re-attach: a RUNNING job renders the analyzing state", async () => {
    attachMock.mockResolvedValue({
      kind: "live",
      job: { job_id: 9, state: "running" },
    });
    open();
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("Analyzing audio");
    });
    expect(watchMock).toHaveBeenCalledWith(9, expect.any(Function));
  });

  it("reload re-attach: a completed-while-away outcome renders from the record", async () => {
    attachMock.mockResolvedValue({
      kind: "done",
      job: { job_id: 9, state: "done", applied: true, offset_ms: -750, confidence: 0.93 },
    });
    open();
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("93%");
    });
  });
});

describe("consumeSyncClosing", () => {
  it("answers false when nothing closed", () => {
    expect(consumeSyncClosing()).toBe(false);
  });

  it("is a one-shot: the second read is false", () => {
    open();
    // Close through the same affordance a user has.
    dlg().dispatchEvent(new Event("cancel", { cancelable: true }));
    const first = consumeSyncClosing();
    const second = consumeSyncClosing();
    expect([first, second]).toEqual([true, false]);
  });
});

describe("the offset controls", () => {
  const resetBtn = (): HTMLButtonElement => button(/^Reset$/);

  it("hides Reset while the offset is zero", () => {
    // Nothing to reset TO, so the control would be a no-op invitation.
    open([entry({ offset_ms: 0 })]);
    expect(resetBtn().hidden).toBe(true);
  });

  it("shows Reset when the dialog opens on a non-zero offset", () => {
    // The visibility effect runs synchronously on creation, so it must seed
    // from the incoming value rather than waiting for a change.
    open([entry({ offset_ms: -1200 })]);
    expect(resetBtn().hidden).toBe(false);
  });

  it("zeroes the offset and hides itself when Reset is clicked", async () => {
    dispatchOffset.mockResolvedValue({});
    open([entry({ offset_ms: 3000 })]);

    resetBtn().click();
    await vi.waitFor(() => {
      expect(resetBtn().hidden).toBe(true);
    });

    // The saved value is the observable that matters: Reset must have moved the
    // offset the save reads, not merely repainted the control.
    button(/Save Offset/).click();
    await vi.waitFor(() => {
      expect(dispatchOffset).toHaveBeenCalled();
    });
    expect(dispatchOffset.mock.calls[0]?.[0]).toMatchObject({ offset_ms: 0 });
  });

  it("adopts the selected subtitle's own offset when the dropdown changes", async () => {
    dispatchOffset.mockResolvedValue({});
    open([entry({ offset_ms: 500 }), entry({ language: "fr", offset_ms: -2500 })]);

    const sel = dlg().querySelector("select") as HTMLSelectElement;
    sel.value = "1";
    sel.dispatchEvent(new Event("change"));

    button(/Save Offset/).click();
    await vi.waitFor(() => {
      expect(dispatchOffset).toHaveBeenCalled();
    });
    // Saving must target the newly selected entry, with ITS offset.
    expect(dispatchOffset.mock.calls[0]?.[0]).toMatchObject({ offset_ms: -2500 });
  });

  it("leaves arrow keys alone inside the subtitle dropdown", () => {
    // The dialog-level arrow handler adjusts the timecode from anywhere, which
    // would otherwise swallow the <select>'s own keyboard navigation.
    open([entry({ offset_ms: 0 }), entry({ language: "fr", offset_ms: 0 })]);
    const sel = dlg().querySelector("select") as HTMLSelectElement;

    const e = new KeyboardEvent("keydown", {
      key: "ArrowUp",
      bubbles: true,
      cancelable: true,
    });
    sel.dispatchEvent(e);

    // Not consumed by the timecode handler, so the select still owns it.
    expect(e.defaultPrevented).toBe(false);
  });
});

describe("the video preview", () => {
  const playBtn = (): HTMLButtonElement => dlg().querySelector(".sync-play") as HTMLButtonElement;

  it("offers a labelled play control rather than an unmarked hotspot", () => {
    open([entry()], 42);
    expect(playBtn()).not.toBeNull();
    expect(playBtn().getAttribute("aria-label")).toBe("Play video preview");
  });

  // WHERE THIS FILE STOPS, and why it is a boundary rather than an omission.
  //
  // Pressing play runs previewStart() against the server for a dialogue-dense
  // start point, then startPreviewStream(), which drives MediaSource with a
  // fetch-streamed body. Asserting past this point needs a live media endpoint
  // serving real container bytes: mocking previewStart only moves the failure
  // into MSE, whose buffer transitions a stub cannot honestly reproduce. A test
  // that faked it all the way down would assert the fake, not the player.
  //
  // So the ~380 uncovered lines below toggleVideoPreview are deliberate. They
  // are covered instead by scripts/verify-chip-geometry.mjs's sibling in
  // web-terminal-ui's sense — a human driving a real instance — and by the
  // functional suite's `sync` section against a live server. The coverage
  // number for sync.ts therefore stays under its per-file threshold, and that
  // is reported rather than excluded: this package's vitest.config.ts admits an
  // exclusion only for "a capability headless Chromium lacks", and a media
  // server is not a browser capability.
});
