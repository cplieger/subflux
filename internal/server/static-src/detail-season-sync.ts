// detail-season-sync.ts — Season audio sync dialog extracted from detail.ts.

import { el, dialog, closeDialog, dialogHead, pad } from "./dom.js";
import { createDialog } from "@cplieger/ui-primitives/dialog";
import { patch } from "@cplieger/reactive";
import { audioSyncAction } from "./sync-actions.js";
import { SEASON_SYNC_CONCURRENCY } from "./constants.js";

export interface SeasonSyncEpisode {
  subPath: string;
  videoPath: string;
  label: string;
}

export function confirmSeasonSync(
  seriesTitle: string,
  seasonNum: number,
  episodes: SeasonSyncEpisode[],
): void {
  const dlg = dialog("seasonSyncConfirm");
  const label = `${seriesTitle} S${pad(seasonNum)}`;
  const syncAbort = new AbortController();

  // createDialog bundles open + drag-safe backdrop dismiss + Escape → fade-out
  // close. The in-flight sync abort is wired through onClose so it fires on
  // every dismissal path: the old hand-wiring aborted on backdrop click and the
  // Cancel button but let Escape close the dialog WITHOUT stopping the running
  // workers. dispose() on the `close` event keeps the controller's listeners
  // from accumulating across reopens of the reused #seasonSyncConfirm element.
  const ctrl = createDialog(dlg, {
    onClose: () => {
      syncAbort.abort();
    },
  });
  dlg.addEventListener(
    "close",
    () => {
      ctrl.dispose();
    },
    { once: true },
  );
  const closeFn = (): void => {
    ctrl.close();
  };
  const header = dialogHead(`Sync ${label}`, closeFn);

  const body = el(
    "div",
    { className: "dlg-body" },
    el(
      "p",
      null,
      `This will run audio sync on ${episodes.length} subtitle file${
        episodes.length === 1 ? "" : "s"
      } in ${label}.`,
    ),
    el(
      "p",
      null,
      "Audio sync analyzes the video audio track to align subtitle " +
        "timing automatically. Results depend on audio quality and " +
        "are not guaranteed to be accurate for every file.",
    ),
  );

  const progress = el("div", {
    id: "season-sync-status",
    hidden: true,
  });
  body.appendChild(progress);

  const startBtn = el(
    "button",
    {
      type: "button",
      id: "season-sync-start",
      onclick: () => runSeasonAudioSync(episodes, dlg, syncAbort.signal),
    },
    "Start Sync",
  );

  const footer = el(
    "div",
    { className: "dlg-foot" },
    startBtn,
    el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: closeFn,
      },
      "Cancel",
    ),
  );

  patch(dlg, header, body, footer);
  ctrl.open();
}

async function runSeasonAudioSync(
  episodes: SeasonSyncEpisode[],
  dlg: HTMLDialogElement,
  signal: AbortSignal,
): Promise<void> {
  const startBtn = document.getElementById("season-sync-start") as HTMLButtonElement | null;
  const status = document.getElementById("season-sync-status");
  if (startBtn) {
    startBtn.disabled = true;
  }
  if (status) {
    status.hidden = false;
  }

  let done = 0;
  let applied = 0;
  let failed = 0;

  // Bounded-concurrency runner with abort.
  const queue = [...episodes];
  async function worker(): Promise<void> {
    while (queue.length > 0) {
      if (signal.aborted) {
        return;
      }
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- length checked in while condition
      const ep = queue.shift()!;
      done++;
      if (status) {
        status.textContent = `Syncing ${done}/${episodes.length}: ${ep.label}\u2026`;
      }
      const r = await audioSyncAction.dispatch(
        {
          subtitle_path: ep.subPath,
          video_path: ep.videoPath,
        },
        { silent: true },
      );
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- may change during await
      if (signal.aborted) {
        return;
      }
      if (r === null) {
        failed++;
        continue;
      }
      if (r.applied) {
        applied++;
      }
    }
  }

  const workers = Array.from({ length: Math.min(SEASON_SYNC_CONCURRENCY, episodes.length) }, () =>
    worker(),
  );
  await Promise.all(workers);

  if (signal.aborted) {
    return;
  }
  const skipped = episodes.length - applied - failed;
  if (status) {
    status.textContent = `Done: ${applied} synced${
      failed > 0 ? `, ${failed} failed` : ""
    }${skipped > 0 ? `, ${skipped} low confidence` : ""}`;
  }
  if (startBtn) {
    startBtn.textContent = "Close";
    startBtn.disabled = false;
    startBtn.onclick = () => closeDialog(dlg);
  }
}
