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

// Mocked as whole modules because these are the two the dialog dispatches
// through; a partial factory that stopped matching the export set would fail
// collection rather than fail a test.
vi.mock("./sync-actions.js", () => ({
  audioSyncAction: { dispatch: dispatchAudio },
  saveManualOffsetAction: { dispatch: dispatchOffset },
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

describe("sync to audio", () => {
  it("applies a confident result to the offset", async () => {
    dispatchAudio.mockResolvedValue({ applied: true, offset_ms: -750, confidence: 0.93 });
    open();

    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(dispatchAudio).toHaveBeenCalled();
    });

    // The reported confidence is what tells the user whether to trust it.
    await vi.waitFor(() => {
      expect(dlg().textContent).toContain("93%");
    });
    expect(notify.success).toHaveBeenCalled();
  });

  it("asks the server for a dry run, never a blind write", async () => {
    dispatchAudio.mockResolvedValue({ applied: true, offset_ms: 0, confidence: 1 });
    open();
    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(dispatchAudio).toHaveBeenCalled();
    });
    expect(dispatchAudio.mock.calls[0]?.[0]).toMatchObject({ dry_run: true });
  });

  it("reports low confidence and changes nothing", async () => {
    dispatchAudio.mockResolvedValue({ applied: false, offset_ms: 4000, confidence: 0.12 });
    open();

    button(/Sync to Audio/).click();
    await vi.waitFor(() => {
      expect(dlg().textContent).toMatch(/Low confidence/);
    });
    expect(notify.success).not.toHaveBeenCalled();
  });

  it("restores the button after a failure rather than leaving it spinning", async () => {
    dispatchAudio.mockResolvedValue(null);
    open();
    const btn = button(/Sync to Audio/);

    btn.click();
    await vi.waitFor(() => {
      expect(notify.error).toHaveBeenCalled();
    });
    // The finally block owns this: a permanently disabled button would strand
    // the user with no way to retry.
    await vi.waitFor(() => {
      expect(button(/Sync to Audio/).disabled).toBe(false);
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
