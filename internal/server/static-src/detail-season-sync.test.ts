// detail-season-sync.test.ts — the season audio-sync dialog.
//
// detail.ts's tests mock this module, so nothing exercised it. Two of its
// properties are the kind that fail quietly and cost the user real work:
//
//  - the abort path. The module's own comment records the bug it was written
//    to fix — Escape closed the dialog while the workers kept running — so
//    every dismissal path must stop the queue, and the assertion has to be
//    "no further dispatch happened", not "abort() was called".
//  - the bounded runner. It fans out SEASON_SYNC_CONCURRENCY workers over one
//    queue; an off-by-one that starts a worker per episode would look
//    identical in the UI and hammer an FFmpeg-bound endpoint with a whole
//    season at once.
import { describe, it, expect, beforeEach, vi } from "vitest";
import { confirmSeasonSync } from "./detail-season-sync.js";
import { SEASON_SYNC_CONCURRENCY } from "./constants.js";
import type { SeasonSyncEpisode } from "./detail-season-sync.js";
import type { SyncAudioResponse } from "./wire/types.gen.js";

// audioSyncAction is a module-scope const in the real module, so the double is
// an object literal (plain functions, immune to mockReset) over a hoisted
// record. `defer` holds every dispatch open so a test can observe how many
// workers are in flight.
const sync = vi.hoisted(() => ({
  args: [] as unknown[],
  silent: [] as (boolean | undefined)[],
  defer: false,
  releases: [] as (() => void)[],
  results: [] as (SyncAudioResponse | null)[],
}));
vi.mock("./sync-actions.js", () => ({
  audioSyncAction: {
    dispatch: (args: unknown, opts?: { silent?: boolean }) => {
      sync.args.push(args);
      sync.silent.push(opts?.silent);
      const next = (): SyncAudioResponse | null =>
        sync.results.length > 0
          ? (sync.results.shift() ?? null)
          : { method: "audio", offset_ms: 120, confidence: 0.9, applied: true };
      if (sync.defer) {
        return new Promise<SyncAudioResponse | null>((resolve) => {
          sync.releases.push(() => {
            resolve(next());
          });
        });
      }
      return Promise.resolve(next());
    },
  },
}));

function applied(): SyncAudioResponse {
  return { method: "audio", offset_ms: 120, confidence: 0.9, applied: true };
}

function lowConfidence(): SyncAudioResponse {
  return { method: "audio", offset_ms: 0, confidence: 0.1, applied: false };
}

function episodes(n: number): SeasonSyncEpisode[] {
  return Array.from({ length: n }, (_, i) => ({
    ref: { media_type: "episode", media_id: `tt1-s01e0${i + 1}`, language: "en" },
    label: `S01E0${i + 1}`,
  }));
}

/** The reused dialog element app.ts ships in the page. */
function mountDialogHost(): HTMLDialogElement {
  const dlg = document.createElement("dialog");
  dlg.id = "seasonSyncConfirm";
  document.body.replaceChildren(dlg);
  return dlg;
}

function startButton(): HTMLButtonElement {
  const btn = document.getElementById("season-sync-start");
  if (!(btn instanceof HTMLButtonElement)) {
    throw new Error("start button not rendered");
  }
  return btn;
}

function statusText(): string {
  return document.getElementById("season-sync-status")?.textContent ?? "";
}

/** Let the queued workers run to their next await. */
async function settle(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0));
}

/** Complete the primitive's fade-out immediately. closeDialog waits for a
 *  `transitionend` on the dialog (or a 400ms fallback) before it actually
 *  closes and runs onClose, so a dismissal's side effects — the sync abort
 *  among them — land only after this. */
async function finishFadeOut(dialog: HTMLDialogElement): Promise<void> {
  dialog.dispatchEvent(new TransitionEvent("transitionend"));
  await settle();
}

function cancelButton(dialog: HTMLDialogElement): HTMLButtonElement | undefined {
  return [...dialog.querySelectorAll("button")].find((b) => b.textContent === "Cancel");
}

let dlg: HTMLDialogElement;

beforeEach(() => {
  sync.args.length = 0;
  sync.silent.length = 0;
  sync.releases.length = 0;
  sync.results.length = 0;
  sync.defer = false;
  dlg = mountDialogHost();
});

describe("confirmSeasonSync: the confirmation", () => {
  it("opens the dialog naming the series and zero-padded season", () => {
    confirmSeasonSync("Show", 3, episodes(2));

    expect(dlg.open).toBe(true);
    expect(dlg.querySelector(".dlg-head h2")?.textContent).toBe("Sync Show S03");
  });

  it("states the file count in the plural", () => {
    confirmSeasonSync("Show", 1, episodes(2));

    expect(dlg.querySelector(".dlg-body p")?.textContent).toBe(
      "This will run audio sync on 2 subtitle files in Show S01.",
    );
  });

  it("states the file count in the singular for one file", () => {
    confirmSeasonSync("Show", 1, episodes(1));

    expect(dlg.querySelector(".dlg-body p")?.textContent).toBe(
      "This will run audio sync on 1 subtitle file in Show S01.",
    );
  });

  it("keeps the progress line hidden until a run starts", () => {
    confirmSeasonSync("Show", 1, episodes(1));

    expect(document.getElementById("season-sync-status")?.hidden).toBe(true);
  });

  it("dispatches nothing until Start is pressed", () => {
    confirmSeasonSync("Show", 1, episodes(3));

    expect(sync.args).toStrictEqual([]);
  });
});

describe("confirmSeasonSync: the run", () => {
  it("syncs every episode once, by file ref, without per-file toasts", async () => {
    const eps = episodes(3);
    confirmSeasonSync("Show", 1, eps);

    startButton().click();
    await settle();

    expect(sync.args).toStrictEqual(eps.map((e) => ({ ...e.ref })));
    // The dialog reports its own progress, so per-file toasts would be noise.
    expect(sync.silent).toStrictEqual([true, true, true]);
  });

  it("disables Start and shows the progress line for the duration", async () => {
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(2));

    startButton().click();
    await settle();

    expect(startButton().disabled).toBe(true);
    expect(document.getElementById("season-sync-status")?.hidden).toBe(false);
    expect(statusText()).toContain("Syncing");
  });

  it("names the episode being synced in the progress line", async () => {
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(1));

    startButton().click();
    await settle();

    expect(statusText()).toBe("Syncing 1/1: S01E01\u2026");
  });

  it("runs at most SEASON_SYNC_CONCURRENCY files at a time", async () => {
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(SEASON_SYNC_CONCURRENCY + 2));

    startButton().click();
    await settle();
    expect(sync.args).toHaveLength(SEASON_SYNC_CONCURRENCY);

    // One completion admits exactly one more.
    sync.releases.shift()?.();
    await settle();
    expect(sync.args).toHaveLength(SEASON_SYNC_CONCURRENCY + 1);
  });

  it("never starts more workers than there are episodes", async () => {
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(1));

    startButton().click();
    await settle();

    expect(sync.args).toHaveLength(1);
  });

  it("summarises an all-applied run", async () => {
    confirmSeasonSync("Show", 1, episodes(2));

    startButton().click();
    await settle();

    expect(statusText()).toBe("Done: 2 synced");
  });

  it("counts a null result as a failure and reports it", async () => {
    sync.results.push(applied(), null);
    confirmSeasonSync("Show", 1, episodes(2));

    startButton().click();
    await settle();

    expect(statusText()).toBe("Done: 1 synced, 1 failed");
  });

  it("counts an unapplied result as low confidence, not as a failure", async () => {
    // The server answers 200 with applied:false when the correlation is too
    // weak to trust; calling that a failure would tell the user something
    // broke when nothing did.
    sync.results.push(applied(), lowConfidence());
    confirmSeasonSync("Show", 1, episodes(2));

    startButton().click();
    await settle();

    expect(statusText()).toBe("Done: 1 synced, 1 low confidence");
  });

  it("reports failures and low confidence together", async () => {
    sync.results.push(applied(), null, lowConfidence());
    confirmSeasonSync("Show", 1, episodes(3));

    startButton().click();
    await settle();

    expect(statusText()).toBe("Done: 1 synced, 1 failed, 1 low confidence");
  });

  it("turns Start into a Close button that dismisses the dialog", async () => {
    confirmSeasonSync("Show", 1, episodes(1));

    startButton().click();
    await settle();
    expect(startButton().textContent).toBe("Close");
    expect(startButton().disabled).toBe(false);

    startButton().click();
    // closeDialog fades out first, so the dialog is leaving here and closed
    // once the transition completes.
    expect(dlg.classList.contains("is-leaving")).toBe(true);
    await finishFadeOut(dlg);
    expect(dlg.open).toBe(false);
  });
});

describe("confirmSeasonSync: aborting", () => {
  it("stops the queue when the dialog is dismissed with Escape", async () => {
    // The regression this module's comment records: Escape closed the dialog
    // and left the workers running.
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(SEASON_SYNC_CONCURRENCY + 2));
    startButton().click();
    await settle();
    const startedBeforeEscape = sync.args.length;

    // The primitive intercepts `cancel` (Escape) to run its own fade-out.
    dlg.dispatchEvent(new Event("cancel", { cancelable: true }));
    await finishFadeOut(dlg);
    for (const release of [...sync.releases]) {
      release();
    }
    await settle();

    expect(sync.args).toHaveLength(startedBeforeEscape);
  });

  it("stops the queue when Cancel is pressed", async () => {
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(SEASON_SYNC_CONCURRENCY + 2));
    startButton().click();
    await settle();
    const startedBeforeCancel = sync.args.length;

    cancelButton(dlg)?.click();
    await finishFadeOut(dlg);
    for (const release of [...sync.releases]) {
      release();
    }
    await settle();

    expect(sync.args).toHaveLength(startedBeforeCancel);
  });

  it("leaves the last progress line in place rather than writing a summary", async () => {
    sync.defer = true;
    confirmSeasonSync("Show", 1, episodes(SEASON_SYNC_CONCURRENCY + 2));
    startButton().click();
    await settle();

    cancelButton(dlg)?.click();
    await finishFadeOut(dlg);
    for (const release of [...sync.releases]) {
      release();
    }
    await settle();

    // An aborted run must not claim a result for files it never reached.
    expect(statusText()).not.toContain("Done:");
  });
});
