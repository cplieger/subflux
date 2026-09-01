// sync.ts — Subtitle sync dialog: audio sync, manual offset, video preview, timecode controls.

import * as notify from "./notify.js";
import {
  el,
  text,
  option,
  icon,
  dialog,
  closeDialog,
  onBackdropClose,
  dialogHead,
  pad,
} from "./dom.js";
import { openDialog, createDialog } from "@cplieger/ui-primitives/dialog";
import { signal, effect, patch } from "@cplieger/reactive";
import { audioSyncAction, saveManualOffsetAction, seasonSyncAction } from "./sync-actions.js";
import { attachSyncJob, watchSyncJob } from "./sync-jobs.js";
import { refKey, subtitleRef, type FileRefArgs } from "./file-ref.js";
import type { Job, JobOutcome, SyncDoneEvent } from "./wire/types.gen.js";
import {
  previewStart,
  syncJobs,
  cancelActivity,
  PATH_PREVIEW_POSTER,
  PATH_PREVIEW_SUBTITLE,
  PATH_PREVIEW_VIDEO,
} from "./wire/client.gen.js";
import { bindLoadingState, registerCleanup } from "@cplieger/actions";
import { observeActivities } from "./status.js";
import { langName } from "./utils.js";
import { buildTimecodeInput, formatOffsetMs, updateTimecodeDisplay } from "./sync-timecode.js";
import type { TimecodeInput } from "./sync-timecode.js";
import { buildSyncSubLabels, parseSeasonEpisode } from "./sync-entries.js";
import type { SubtitleEntry, MediaType } from "./api-types.js";

// --- Subtitle Sync Dialog ---

// Resolved on first use, not at import: `dialog()` is a getElementById,
// so reading it at module scope would require a document already
// containing #syncDialog at import time.
let syncDlgCache: HTMLDialogElement | null = null;

function syncDialogEl(): HTMLDialogElement {
  syncDlgCache ??= dialog("syncDialog");
  return syncDlgCache;
}

// Subtitles are addressed by FileRef and the video by MediaRef (arr id +
// season/episode) — the client never handles paths.
interface SyncStateBase {
  /** Index of the selected subtitle in `entries`. */
  selIdx: number;
  /** Poster proxy type ("series"/"movie") — the arr the poster comes from. */
  mediaType: MediaType | "";
  /** Store media type for FileRef/MediaRef ("episode" for series). */
  fileMediaType: MediaType;
  /** Arr internal ID (Sonarr series / Radarr movie) for the video MediaRef. */
  mediaId: number;
  /** Aired season/episode for the video MediaRef (episodes only). */
  season: number;
  episode: number;
  entries: SubtitleEntry[];
  blobUrl: string;
}

// Discriminated union: invalid state combinations are unrepresentable.
type SyncState =
  | (SyncStateBase & { status: "idle"; previewStart: number; previewBuffered: boolean })
  | (SyncStateBase & {
      status: "preview";
      ffmpegAbort: AbortController | null;
      previewStart: number;
      previewBuffered: boolean;
    })
  | (SyncStateBase & { status: "syncing"; previewStart: number; previewBuffered: boolean });

let syncState: SyncState = {
  status: "idle",
  selIdx: 0,
  mediaType: "",
  fileMediaType: "movie",
  mediaId: 0,
  season: 0,
  episode: 0,
  entries: [],
  previewStart: 0,
  previewBuffered: false,
  blobUrl: "",
};

/** The currently selected subtitle entry, or null when entries are empty. */
function selectedEntry(): SubtitleEntry | null {
  return syncState.entries[syncState.selIdx] ?? null;
}

/** FileRef for the currently selected subtitle. */
function currentRef(): FileRefArgs | null {
  const sub = selectedEntry();
  if (!sub) {
    return null;
  }
  return subtitleRef(syncState.fileMediaType, sub);
}

// Recreated per openSyncDialog so each dialog instance gets a fresh signal;
// an effect toggles Reset-button visibility on zero-crossing.
let offset = signal(0);
let stopOffsetEffect: (() => void) | null = null;
let stopSaveBinding: (() => void) | null = null;

// Closing the dialog drops the WATCH only — the analysis is server-owned
// and continues; a reopen re-attaches via the jobs read.
let syncUnwatch: (() => void) | null = null;
let audioResultEl: HTMLElement | null = null;

function stopWatchingSyncJob(): void {
  if (syncUnwatch) {
    syncUnwatch();
    syncUnwatch = null;
  }
}

// Drain any in-flight ffmpeg stream + revoke blob URL on page unload.
registerCleanup(() => {
  if (syncState.status === "preview" && syncState.ffmpegAbort) {
    syncState.ffmpegAbort.abort();
  }
  if (syncState.blobUrl) {
    try {
      URL.revokeObjectURL(syncState.blobUrl);
    } catch {
      /* ignore */
    }
  }
});

// Whether openSyncDialog pushed a history entry that closeSyncDialog
// needs to pop (vs. direct URL navigation where no entry was pushed).
let syncPushedHistory = false;

// Set by closeSyncDialog before history.back() so the popstate handler
// in app.ts can skip the redundant applyRoute() call.
let syncClosing = false;

/** Check and reset the syncClosing flag (one-shot). */
export function consumeSyncClosing(): boolean {
  if (!syncClosing) {
    return false;
  }
  syncClosing = false;
  return true;
}

export function openSyncDialog(
  entries: SubtitleEntry[],
  mediaType: MediaType,
  mediaId: number,
  mediaLabel: string,
): void {
  const dlg = syncDialogEl();

  const currentPath = location.pathname.replace(/\/sync$/, "");
  const syncPath = `${currentPath}/sync`;
  if (location.pathname !== syncPath) {
    history.pushState(null, "", syncPath);
    syncPushedHistory = true;
  } else {
    syncPushedHistory = false;
  }

  const initialOffset = entries[0]?.offset_ms ?? 0;
  // Dispose any effect left over from a prior open that wasn't closed.
  if (stopOffsetEffect) {
    stopOffsetEffect();
    stopOffsetEffect = null;
  }
  if (stopSaveBinding) {
    stopSaveBinding();
    stopSaveBinding = null;
  }
  offset = signal(initialOffset);
  // season/episode parse from the entry's media_id ("tvdb-...-s01e05");
  // movies parse to 0/0, which the server ignores.
  const se = parseSeasonEpisode(entries[0]?.media_id ?? "");
  syncState = {
    status: "idle",
    selIdx: 0,
    mediaType: mediaType,
    fileMediaType: mediaType === "movie" ? "movie" : "episode",
    mediaId: mediaId,
    season: se.season,
    episode: se.episode,
    entries: entries,
    previewStart: 0,
    previewBuffered: false,
    blobUrl: "",
  };

  const labeled = buildSyncSubLabels(entries);

  const subSel = el("select", {
    id: "sync-sub-sel",
    "aria-label": "Subtitle",
  }) as HTMLSelectElement;
  for (let i = 0; i < labeled.length; i++) {
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- bounds checked
    subSel.appendChild(option(String(i), labeled[i]!.label));
  }
  subSel.value = "0";
  subSel.addEventListener("change", () => {
    const idx = parseInt(subSel.value, 10);
    const entry = labeled[idx];
    if (!entry) {
      return;
    }
    syncState.selIdx = idx;
    offset.value = entry.sub.offset_ms ?? 0;
    updateTimecodeDisplay(offset.peek());
    if (syncState.status === "preview") {
      updatePreviewTrack();
    }
    // Drop the old watch and re-attach for the newly selected file.
    stopWatchingSyncJob();
    if (audioResultEl) {
      audioResultEl.hidden = true;
      void reattachSyncJob(audioResultEl, false);
    }
  });

  const titleParts: (string | Node)[] = [text("Subtitle Sync")];
  if (mediaLabel) {
    titleParts.push(el("span", { className: "sync-media" }, " \u00B7 ", mediaLabel));
  }

  const header = el(
    "div",
    { className: "dlg-head" },
    el("div", { className: "dlg-title" }, ...titleParts),
    el(
      "div",
      { className: "dlg-controls" },
      subSel,
      el(
        "button",
        {
          type: "button",
          className: "close-btn ghost",
          "aria-label": "Close sync",
          onclick: closeSyncDialog,
        },
        icon("close"),
      ),
    ),
  );

  const body = el("div", { className: "dlg-body" });

  body.appendChild(
    el(
      "div",
      { className: "sync-help" },
      "Use ",
      el("strong", null, "Sync to Audio"),
      " for automatic alignment, then fine-tune with the manual " +
        "controls. Play the video to verify timing.",
    ),
  );

  // Audio sync button and result.
  const audioResultDiv = el("div", {    className: "sync-audio-result",
    hidden: true,
  });
  audioResultEl = audioResultDiv;
  const audioBtn = el(
    "button",
    {
      type: "button",
      className: "sync-audio-btn",
      onclick: () => runAudioSync(audioBtn as HTMLButtonElement, audioResultDiv),
    },
    syncIcon(),
    " Sync to Audio",
  );
  body.appendChild(el("div", { className: "sync-audio" }, audioBtn, audioResultDiv));

  // A job dispatched before a reload (or from a closed dialog) is still
  // the server's; the jobs read answers its current state.
  stopWatchingSyncJob();
  void reattachSyncJob(audioResultDiv, false);

  // Video preview: backdrop/poster background with play button.
  const preview = buildVideoPreview();
  if (preview) {
    body.appendChild(preview);
  }

  const timecode = buildTimecodeInput(initialOffset, (newMs: number) => {
    // Widget self-edited its own display; only sync the signal.
    offset.value = newMs;
    if (syncState.status === "preview") {
      updatePreviewTrack();
    }
  });

  const offsetSection = el("div", { className: "sync-offset" }, timecode);
  body.appendChild(offsetSection);

  // Footer with Save + Reset (matches config dialog footer).
  const footer = el("div", { className: "dlg-foot" });
  const saveBtn = el(
    "button",
    {
      type: "button",
      onclick: () => applyManualOffset(),
    },
    "Save Offset",
  ) as HTMLButtonElement;
  footer.appendChild(saveBtn);
  stopSaveBinding = bindLoadingState("sync.save_offset", saveBtn);
  const resetBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      onclick: resetSync,
    },
    "Reset",
  );
  footer.appendChild(resetBtn);

  // Single source of truth for Reset-button visibility.
  stopOffsetEffect = effect(() => {
    resetBtn.hidden = offset.value === 0;
  });

  dlg.replaceChildren(header, body, footer);
  if (dlg.open) {
    dlg.close();
  }
  // openDialog cancels a pending is-leaving fade before showing, so a
  // reopen within the fade window renders visible and the stale close no-ops.
  openDialog(dlg);
  // Keep keyboard focus inside the modal (dialog has tabindex="-1").
  dlg.focus();

  // Arrow keys adjust the active timecode segment, except inside form
  // controls (select/input/textarea need their own arrow keys).
  dlg.onkeydown = (e: KeyboardEvent) => {
    const t = e.target as HTMLElement | null;
    if (t?.closest("select, input, textarea")) {
      return;
    }
    (timecode as TimecodeInput).handleKey(e);
  };

  wireSyncDialogChrome(dlg);
}

// Once per dialog: sync elements resolve lazily on first open (the module
// must import without a DOM), so "at boot" is the first open here.
let dialogChromeWired = false;

function wireSyncDialogChrome(dlg: HTMLDialogElement): void {
  if (dialogChromeWired) {
    return;
  }
  dialogChromeWired = true;
  onBackdropClose(dlg, closeSyncDialog);
  // Prevent the browser's default close so closeSyncDialog can run the
  // animated close, video cleanup, and URL fixup.
  dlg.addEventListener("cancel", (e: Event) => {
    e.preventDefault();
    closeSyncDialog();
  });
}

/** Video preview container with poster background and play overlay.
 *  Requires the arr MediaRef — without it the server cannot resolve the
 *  video file, so the section is omitted. */
function buildVideoPreview(): HTMLElement | null {
  if (!syncState.mediaId) {
    return null;
  }
  const previewContainer = el("div", {
    className: "sync-preview",
    id: "sync-preview-container",
  });
  if (syncState.mediaId) {
    const fanartUrl = `${PATH_PREVIEW_POSTER}?${new URLSearchParams({
      type: syncState.mediaType,
      id: String(syncState.mediaId),
      style: "fanart",
    }).toString()}`;
    const posterUrl = `${PATH_PREVIEW_POSTER}?${new URLSearchParams({
      type: syncState.mediaType,
      id: String(syncState.mediaId),
    }).toString()}`;
    previewContainer.style.backgroundImage = `url(${fanartUrl}), url(${posterUrl})`;
    previewContainer.style.backgroundSize = "cover";
    previewContainer.style.backgroundPosition = "center";
  }
  const playOverlay = el(
    "button",
    {
      type: "button",
      className: "sync-play",
      "aria-label": "Play video preview",
      onclick: () => {
        playOverlay.remove();
        previewContainer.style.backgroundImage = "none";
        void toggleVideoPreview(previewContainer);
      },
    },
    previewPlayIcon(),
  );
  previewContainer.appendChild(playOverlay);
  return previewContainer;
}

function closeSyncDialog(): void {
  if (stopOffsetEffect) {
    stopOffsetEffect();
    stopOffsetEffect = null;
  }
  if (stopSaveBinding) {
    stopSaveBinding();
    stopSaveBinding = null;
  }
  // Drop the job WATCH, never the job: the analysis is server-owned and
  // continues after the dialog closes; reopening re-attaches via the jobs read.
  stopWatchingSyncJob();
  audioResultEl = null;
  if (syncState.status === "preview" && syncState.ffmpegAbort) {
    syncState.ffmpegAbort.abort();
  }
  syncState = { ...syncState, status: "idle" };
  if (syncState.blobUrl) {
    URL.revokeObjectURL(syncState.blobUrl);
    syncState.blobUrl = "";
  }
  // Stop hold-repeat timers so a pending tick can't fire on the detached
  // element after the dialog closes.
  const tc = document.getElementById("sync-offset-val") as TimecodeInput | null;
  tc?.dispose();
  const video = syncDialogEl().querySelector("video");
  if (video) {
    video.pause();
    video.removeAttribute("src");
    video.load();
  }
  closeDialog(syncDialogEl());
  // history.back() fires popstate -> applyRoute(); syncClosing lets the
  // popstate handler skip the redundant re-render.
  if (syncPushedHistory && location.pathname.endsWith("/sync")) {
    syncPushedHistory = false;
    syncClosing = true;
    history.back();
  } else if (location.pathname.endsWith("/sync")) {
    const parent = location.pathname.replace(/\/sync$/, "");
    history.replaceState(null, "", parent || "/");
  }
}

async function applyManualOffset(): Promise<void> {
  const ref = currentRef();
  const sub = selectedEntry();
  if (!ref || !sub) {
    notify.error("No subtitle selected");
    return;
  }
  const currentOffset = offset.peek();
  const r = await saveManualOffsetAction.dispatch(
    {
      ...ref,
      offset_ms: currentOffset,
    },
    { silent: true },
  );
  if (r === null) {
    return;
  }
  sub.offset_ms = currentOffset;
  notify.success(`Offset saved: ${formatOffsetMs(currentOffset)}`);
  closeSyncDialog();
}

// The fields the inline result panel renders, shared by the live sync:done
// event and the reload path's job record. Both carry the registry's typed
// outcome; the event's is always set, a queued/running record's is not.
interface SyncOutcomeView {
  outcome?: JobOutcome;
  applied?: boolean;
  confidence?: number;
  offset_ms?: number;
  error?: string;
}

/** The line for a job that produced no offset. Read from the TYPED outcome:
 *  a stopped job, a timed-out one and a crashed one all carry an error
 *  string, so `error` alone cannot tell them apart. */
function failedOutcomeText(outcome: Exclude<JobOutcome, "result">, error?: string): string {
  switch (outcome) {
    case "cancelled":
      return "Audio sync was stopped.";
    case "timeout":
      return "Audio sync ran out of time before it finished.";
    case "crash":
      return `Audio sync failed: ${error ?? "unknown error"}`;
  }
}

/** Render one job's terminal outcome into the inline result panel — the
 *  dialog's ONE surface for analysis results and refusals alike. */
function renderSyncOutcome(view: SyncOutcomeView, resultDiv: HTMLElement): void {
  resultDiv.hidden = false;
  resultDiv.className = "sync-audio-result";
  if (view.outcome !== undefined && view.outcome !== "result") {
    resultDiv.textContent = failedOutcomeText(view.outcome, view.error);
    return;
  }
  if (view.error) {
    // A record with no outcome cannot arrive from this server; keep the
    // generic line rather than claiming a verdict nothing supplied.
    resultDiv.textContent = `Audio sync did not complete: ${view.error}`;
    return;
  }
  const confidence = ((view.confidence ?? 0) * 100).toFixed(0);
  if (view.applied) {
    const offsetMs = view.offset_ms ?? 0;
    resultDiv.textContent = `${formatOffsetMs(offsetMs)} (${confidence}% confidence)`;
    offset.value = offsetMs;
    updateTimecodeDisplay(offsetMs);
    if (syncState.status === "preview") {
      updatePreviewTrack();
    }
    notify.success("Audio sync offset applied to preview");
  } else {
    resultDiv.textContent = `Low confidence (${confidence}%). No changes.`;
  }
}

/** Watch one job's settlement for the open dialog, showing `label` until the
 *  sync:done event (matched on the 202's job_id) settles it. */
function watchDialogSyncJob(jobId: number, resultDiv: HTMLElement, label: string): void {
  stopWatchingSyncJob();
  resultDiv.hidden = false;
  resultDiv.className = "sync-audio-result";
  resultDiv.textContent = label;
  syncUnwatch = watchSyncJob(jobId, (ev: SyncDoneEvent | null) => {
    syncUnwatch = null;
    if (audioResultEl !== resultDiv) {
      return; // dialog closed or rebuilt while the job ran
    }
    if (ev === null) {
      // Boot changed with no held settlement for this job: re-attach.
      void reattachSyncJob(resultDiv, true);
      return;
    }
    const cur = currentRef();
    if (cur && refKey(ev.file_ref) === refKey(cur)) {
      renderSyncOutcome(ev, resultDiv);
    }
  });
}

/** Re-attach the result panel through the jobs read: prefer this file's
 *  queued/running job, else its newest terminal outcome. `lost` marks a
 *  correlation lost to a server restart, reported when nothing is found. */
async function reattachSyncJob(resultDiv: HTMLElement, lost: boolean): Promise<void> {
  const ref = currentRef();
  if (!ref) {
    return;
  }
  const key = refKey(ref);
  const state = await attachSyncJob(ref);
  const cur = currentRef();
  if (audioResultEl !== resultDiv || !cur || refKey(cur) !== key) {
    return; // closed, rebuilt, or re-selected while the read was in flight
  }
  switch (state.kind) {
    case "live":
      watchDialogSyncJob(
        state.job.job_id,
        resultDiv,
        state.job.state === "queued" ? "Queued for analysis\u2026" : "Analyzing audio\u2026",
      );
      break;
    case "done":
      renderSyncOutcome(state.job, resultDiv);
      break;
    case "none":
      if (lost) {
        resultDiv.hidden = false;
        resultDiv.className = "sync-audio-result";
        resultDiv.textContent = "Sync result was lost to a server restart. Run it again.";
      }
      break;
  }
}

/** The typed capacity refusal: the admission lease is full (HTTP 429), read
 *  through the dispatch handle's outcome (ActionErrorLike.status). */
function isCapacityRefusal(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    "status" in err &&
    (err as { status?: unknown }).status === 429
  );
}

async function runAudioSync(btn: HTMLButtonElement, resultDiv: HTMLElement): Promise<void> {
  const ref = currentRef();
  if (!ref) {
    notify.error("No subtitle selected");
    return;
  }
  btn.disabled = true;
  const origNodes = Array.from(btn.childNodes, (n: ChildNode) => n.cloneNode(true));
  btn.textContent = "Requesting\u2026";
  resultDiv.hidden = true;
  try {
    // Instant 202 hands over {activity_id, job_id}; the result arrives via
    // sync:done matched on job_id.
    const outcome = await audioSyncAction.dispatch({ ...ref, dry_run: true }).outcome;
    if (outcome.status === "cancelled") {
      return;
    }
    if (outcome.status === "error") {
      if (isCapacityRefusal(outcome.error)) {
        // The typed cap refusal renders through the dialog's inline
        // result path — the only visible surface (error: false keeps the
        // framework toast off).
        resultDiv.hidden = false;
        resultDiv.className = "sync-audio-result";
        resultDiv.textContent =
          "Sync queue is full \u2014 wait for a running sync to finish, then try again.";
      } else {
        notify.error("Audio sync failed");
      }
      return;
    }
    watchDialogSyncJob(outcome.value.job_id, resultDiv, "Analyzing audio\u2026");
  } finally {
    btn.replaceChildren(...origNodes);
    btn.disabled = false;
  }
}

function resetSync(): void {
  offset.value = 0;
  updateTimecodeDisplay(0);
  if (syncState.status === "preview") {
    updatePreviewTrack();
  }
}

async function toggleVideoPreview(container: HTMLElement): Promise<void> {
  if (syncState.status === "preview") {
    if (syncState.ffmpegAbort) {
      syncState.ffmpegAbort.abort();
    }
    syncState = { ...syncState, status: "idle" };
    const playOverlay = el(
      "button",
      {
        type: "button",
        className: "sync-play",
        "aria-label": "Play video preview",
        onclick: () => {
          playOverlay.remove();
          void toggleVideoPreview(container);
        },
      },
      previewPlayIcon(),
    );
    patch(container, playOverlay);
    return;
  }

  patch(
    container,
    el(
      "div",
      { className: "sync-loading" },
      el("span", { className: "spinner" }),
      " Finding best scene\u2026",
    ),
  );

  // Find dialogue-dense start point; falls back to startSec=0 silently.
  let startSec = 0;
  const ref = currentRef();
  const r = ref ? await previewStart({ ...ref }) : null;
  if (r) {
    startSec = r.start_seconds || 0;
  }

  syncState.previewStart = startSec;
  syncState = { ...syncState, status: "preview", ffmpegAbort: null, previewStart: startSec };

  startPreviewStream(container, startSec);
}

/** Preview-video stream URL (raw media flow; the generated client only
 *  supplies the path constant). The video is addressed by MediaRef (arr id
 *  + season/episode); buffered=true selects the non-MSE fallback. */
function previewVideoUrl(startSec: number, buffered?: boolean): string {
  const params = new URLSearchParams({
    media_type: syncState.fileMediaType,
    media_id: String(syncState.mediaId),
    start: String(startSec),
  });
  if (syncState.fileMediaType === "episode") {
    params.set("season", String(syncState.season));
    params.set("episode", String(syncState.episode));
  }
  if (buffered) {
    params.set("buffered", "true");
  }
  return `${PATH_PREVIEW_VIDEO}?${params.toString()}`;
}

function startPreviewStream(container: HTMLElement, startSec: number): void {
  const mimeType = 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"';
  const canMSE = typeof MediaSource !== "undefined" && MediaSource.isTypeSupported(mimeType);

  syncState.previewBuffered = !canMSE;
  syncState.previewStart = startSec;

  const video = el("video", {
    autoplay: "",
    preload: "none",
    disablepictureinpicture: "",
    playsinline: "",
  }) as HTMLVideoElement;

  if (canMSE) {
    startMSEStream(video, startSec);
  } else {
    video.src = previewVideoUrl(startSec, true);
  }

  reloadSubtitleTrack(video);
  video.addEventListener(
    "loadedmetadata",
    () => {
      const t = video.querySelector("track");
      if (t?.track) {
        t.track.mode = "showing";
      }
    },
    { once: true },
  );

  video.addEventListener(
    "error",
    () => {
      if (syncState.status !== "preview") {
        return;
      }
      patch(
        container,
        el("div", { className: "sync-unavailable" }, "Preview unavailable for this file."),
      );
      syncState = { ...syncState, status: "idle" };
    },
    { once: true },
  );

  const seekRow = buildSeekControls(video);

  const videoWrap = el("div", { className: "sync-video-wrap" }, video);
  patch(container, videoWrap, seekRow);
}

// Build seek controls row: -30s, -10s, play/pause, +10s, +30s buttons
// with a loading spinner that disappears on first frame.
function buildSeekControls(video: HTMLVideoElement): HTMLElement {
  const seekRow = el("div", { className: "sync-seek" });
  const seekSpinner = el("span", { className: "spinner sync-seek-spinner" });
  seekRow.appendChild(seekSpinner);

  video.addEventListener(
    "loadeddata",
    () => {
      seekSpinner.remove();
    },
    { once: true },
  );

  for (const delta of [-30, -10]) {
    seekRow.appendChild(
      el(
        "button",
        {
          type: "button",
          className: "sync-offset-btn",
          onclick: () => {
            seekPreview(video, delta);
          },
        },
        `${delta}s`,
      ),
    );
  }

  const playPauseBtn = el(
    "button",
    {
      type: "button",
      className: "sync-offset-btn",
      "aria-label": "Pause",
      onclick: () => {
        if (video.paused) {
          video.play().catch(() => {
            /* ignore */
          });
        } else {
          video.pause();
        }
      },
    },
    icon("pause"),
  );
  seekRow.appendChild(playPauseBtn);

  // Keep the accessible name in sync with the button's next action.
  video.addEventListener("play", () => {
    playPauseBtn.replaceChildren(icon("pause"));
    playPauseBtn.setAttribute("aria-label", "Pause");
  });
  video.addEventListener("pause", () => {
    playPauseBtn.replaceChildren(icon("play"));
    playPauseBtn.setAttribute("aria-label", "Play");
  });

  for (const delta of [10, 30]) {
    seekRow.appendChild(
      el(
        "button",
        {
          type: "button",
          className: "sync-offset-btn",
          onclick: () => {
            seekPreview(video, delta);
          },
        },
        `+${delta}s`,
      ),
    );
  }

  return seekRow;
}

// Start MSE-based fMP4 streaming for instant playback on all browsers.
function startMSEStream(video: HTMLVideoElement, startSec: number): void {
  // Abort any previous stream.
  if (syncState.status === "preview" && syncState.ffmpegAbort) {
    syncState.ffmpegAbort.abort();
  }
  const abort = new AbortController();
  syncState = { ...syncState, status: "preview", ffmpegAbort: abort, previewStart: startSec };

  const ms = new MediaSource();
  const objectUrl = URL.createObjectURL(ms);
  video.src = objectUrl;
  syncState.blobUrl = objectUrl;

  ms.addEventListener(
    "sourceopen",
    // eslint-disable-next-line @typescript-eslint/no-misused-promises -- event handler
    async () => {
      // Free the URL->blob mapping once the MediaSource is connected;
      // the connection persists after revocation.
      URL.revokeObjectURL(objectUrl);
      syncState.blobUrl = "";
      const mime = 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"';
      const sb = ms.addSourceBuffer(mime);

      const url = previewVideoUrl(startSec);

      try {
        const resp = await fetch(url, {
          signal: abort.signal,
        });
        if (!resp.ok || !resp.body) {
          if (ms.readyState === "open") {
            ms.endOfStream("network");
          }
          return;
        }

        const reader = resp.body.getReader();
        for (;;) {
          const { done, value } = await reader.read();
          if (done) {
            break;
          }
          if (sb.updating) {
            await new Promise<void>((resolve) => {
              sb.addEventListener(
                "updateend",
                () => {
                  resolve();
                },
                { once: true },
              );
            });
          }
          try {
            sb.appendBuffer(value);
            await new Promise<void>((resolve) => {
              sb.addEventListener(
                "updateend",
                () => {
                  resolve();
                },
                { once: true },
              );
            });
          } catch {
            // QuotaExceededError or InvalidStateError; stop appending.
            break;
          }
        }
        if (ms.readyState === "open") {
          ms.endOfStream();
        }
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === "AbortError") {
          return;
        }
        if (ms.readyState === "open") {
          ms.endOfStream("network");
        }
      }
    },
    { once: true },
  );
}

// Reload the subtitle track on the video element (initial load, seek,
// manual offset changes).
function reloadSubtitleTrack(video: HTMLVideoElement | null): void {
  if (!video) {
    video = document.querySelector(".sync-preview video");
    if (!video) {
      return;
    }
  }
  for (const t of Array.from(video.querySelectorAll("track"))) {
    t.remove();
  }

  const ref = currentRef();
  if (!ref) {
    return;
  }
  const trackParams = new URLSearchParams({
    media_type: ref.media_type,
    media_id: ref.media_id,
    language: ref.language,
    variant: ref.variant ?? "standard",
    source: ref.source ?? "external",
    ordinal: String(ref.ordinal ?? 0),
    start: String(syncState.previewStart || 0),
    shift: String(offset.peek() || 0),
  });
  const trackUrl = `${PATH_PREVIEW_SUBTITLE}?${trackParams.toString()}`;
  // Declare the track's real language: a hardcoded srclang="en" mislabeled
  // every non-English subtitle for caption menus / SR announcements.
  const entryLang = selectedEntry()?.language ?? "";
  const lang = entryLang === "" ? "en" : entryLang;
  const track = el("track", {
    kind: "subtitles",
    src: trackUrl,
    srclang: lang,
    label: entryLang === "" ? "Subtitles" : langName(entryLang),
    default: true,
  }) as HTMLTrackElement;
  video.appendChild(track);
  // Safari needs the track to be explicitly set to showing after load.
  const show = (): void => {
    track.track.mode = "showing";
  };
  track.addEventListener("load", show, { once: true });
  requestAnimationFrame(show);
}

function seekPreview(video: HTMLVideoElement, deltaSec: number): void {
  if (syncState.status !== "preview") {
    return;
  }

  const absNow = (syncState.previewStart || 0) + (video.currentTime || 0);
  const absTarget = Math.max(0, absNow + deltaSec);

  const bufStart = syncState.previewStart || 0;
  const bufEnd =
    bufStart + (video.buffered.length > 0 ? video.buffered.end(video.buffered.length - 1) : 0);

  if (absTarget >= bufStart && absTarget <= bufEnd) {
    video.currentTime = absTarget - bufStart;
  } else {
    // Outside buffer: restart the stream from the new position.
    syncState.previewStart = absTarget;
    if (!syncState.previewBuffered) {
      startMSEStream(video, absTarget);
    } else {
      video.src = previewVideoUrl(absTarget, true);
    }
    video.play().catch(() => {
      /* ignore */
    });
    video.addEventListener(
      "loadedmetadata",
      () => {
        reloadSubtitleTrack(video);
      },
      { once: true },
    );
  }
}

function updatePreviewTrack(): void {
  reloadSubtitleTrack(null);
}

// Inline waveform SVG: uses a custom path with no mask-image equivalent.
function syncIcon(): SVGSVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", "M2 12h2l3-7 4 14 4-14 3 7h2");
  svg.appendChild(path);
  return svg;
}

// Large filled play button (48x48), unlike the stroke-based icon system.
function previewPlayIcon(): SVGSVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("width", "48");
  svg.setAttribute("height", "48");
  svg.setAttribute("viewBox", "0 0 48 48");
  svg.setAttribute("fill", "none");
  svg.setAttribute("aria-hidden", "true");
  const circle = document.createElementNS("http://www.w3.org/2000/svg", "circle");
  circle.setAttribute("cx", "24");
  circle.setAttribute("cy", "24");
  circle.setAttribute("r", "22");
  circle.setAttribute("fill", "oklch(0% 0 0deg / 50%)");
  circle.setAttribute("stroke", "white");
  circle.setAttribute("stroke-width", "2");
  svg.appendChild(circle);
  const poly = document.createElementNS("http://www.w3.org/2000/svg", "polygon");
  poly.setAttribute("points", "19,14 35,24 19,34");
  poly.setAttribute("fill", "white");
  svg.appendChild(poly);
  return svg;
}

// --- Season sync: ONE POST, a server-owned batch ---
//
// The dialog dispatches POST /api/sync/season and then only observes: the
// server enumerates the season's files, runs them sequentially, and owns
// every per-item fact in the job registry. Closing the dialog or the tab
// abandons nothing; reopening re-attaches through the jobs read.

// Dropping a WATCH never touches the job; the batch is the server's.
let seasonUnwatchers: (() => void)[] = [];

function stopSeasonWatches(): void {
  for (const unwatch of seasonUnwatchers) {
    unwatch();
  }
  seasonUnwatchers = [];
}

/** A live batch for (series, season) found in the registry, if any. */
async function findLiveSeasonBatch(seriesId: number, seasonNum: number): Promise<string | null> {
  const jobs = await syncJobs();
  if (!jobs) {
    return null;
  }
  for (const j of jobs) {
    if (
      j.series_id === seriesId &&
      j.season === seasonNum &&
      j.batch_activity_id !== undefined &&
      (j.state === "queued" || j.state === "running")
    ) {
      return j.batch_activity_id;
    }
  }
  return null;
}

/** One item row's label: episode marker, language, variant, manual ordinal. */
function seasonItemLabel(job: Job): string {
  const se = parseSeasonEpisode(job.file_ref.media_id);
  let label = `S${pad(se.season)}E${pad(se.episode)} \u00B7 ${langName(job.file_ref.language)}`;
  if (job.file_ref.variant !== "standard") {
    label += ` (${job.file_ref.variant})`;
  }
  const ordinal = job.file_ref.ordinal ?? 0;
  if (ordinal > 0) {
    label += ` #${ordinal}`;
  }
  return label;
}

/** One item row's status text from its registry record. */
function seasonItemStatus(job: Job): string {
  switch (job.state) {
    case "queued":
      return "queued";
    case "running":
      return "analyzing\u2026";
    default:
      break;
  }
  if (job.outcome === "result") {
    const confidence = ((job.confidence ?? 0) * 100).toFixed(0);
    return job.applied
      ? `${formatOffsetMs(job.offset_ms ?? 0)} (${confidence}%)`
      : `low confidence (${confidence}%)`;
  }
  return job.outcome === "cancelled" ? "stopped" : "failed";
}

/** Fold the registry rows into the aggregate line the dialog shows. */
function seasonAggregate(items: Job[]): string {
  let done = 0;
  let applied = 0;
  let failed = 0;
  let low = 0;
  for (const j of items) {
    if (j.state !== "done") {
      continue;
    }
    done++;
    if (j.outcome === "result") {
      if (j.applied) {
        applied++;
      } else {
        low++;
      }
    } else {
      failed++;
    }
  }
  if (done < items.length) {
    return `Syncing ${done}/${items.length}\u2026`;
  }
  return `Done: ${applied} synced${failed > 0 ? `, ${failed} failed` : ""}${
    low > 0 ? `, ${low} low confidence` : ""
  }`;
}

/** Render (or re-render) the batch view from one registry read, then watch
 *  each live item's own sync:done. */
async function renderSeasonBatch(
  dlg: HTMLDialogElement,
  label: string,
  batchId: string,
  closeFn: () => void,
): Promise<void> {
  stopSeasonWatches();
  const jobs = await syncJobs({ batch_activity_id: batchId });
  if (!dlg.open) {
    return;
  }

  const header = dialogHead(`Sync ${label}`, closeFn);
  const body = el("div", { className: "dlg-body" });
  const aggregate = el("div", { id: "season-sync-status" });
  body.appendChild(aggregate);

  const stopBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      onclick: () => {
        (stopBtn as HTMLButtonElement).disabled = true;
        // The batch is the cancellation unit: one stop request settles
        // queued items server-side with no per-item events, so re-read.
        void cancelActivity(batchId).then(() => renderSeasonBatch(dlg, label, batchId, closeFn));
      },
    },
    "Stop",
  );
  const closeBtn = el("button", { type: "button", className: "ghost", onclick: closeFn }, "Close");
  const footer = el("div", { className: "dlg-foot" }, stopBtn, closeBtn);

  if (!jobs || jobs.length === 0) {
    // The registry dropped the batch (server restart) or the read failed.
    aggregate.textContent =
      jobs === null
        ? "Could not load the batch state. Close and reopen to retry."
        : "This batch is gone (a server restart drops it). Start a new sync.";
    patch(dlg, header, body, el("div", { className: "dlg-foot" }, closeBtn));
    return;
  }

  const items = [...jobs].sort((a, b) => (a.ordinal ?? 0) - (b.ordinal ?? 0));
  const rows = new Map<number, HTMLElement>();
  const list = el("div", { className: "season-sync-items" });
  for (const j of items) {
    const rowEl = el("div", null, `${seasonItemLabel(j)} \u2014 ${seasonItemStatus(j)}`);
    rows.set(j.job_id, rowEl);
    list.appendChild(rowEl);
  }
  body.appendChild(list);
  aggregate.textContent = seasonAggregate(items);
  patch(dlg, header, body, footer);

  const allDone = (): boolean => items.every((j) => j.state === "done");
  const settle = (): void => {
    aggregate.textContent = seasonAggregate(items);
    if (allDone()) {
      stopSeasonWatches();
      stopBtn.hidden = true;
    }
  };
  settle();

  let refreshQueued = false;
  const refresh = (): void => {
    // Coalesced re-read: covers rows that settle without their own event
    // (queued items cancelled by a stop settle server-side only).
    if (refreshQueued) {
      return;
    }
    refreshQueued = true;
    void renderSeasonBatch(dlg, label, batchId, closeFn);
  };

  for (const j of items) {
    if (j.state === "done") {
      continue;
    }
    const unwatch = watchSyncJob(j.job_id, (ev: SyncDoneEvent | null) => {
      if (!dlg.open) {
        return;
      }
      if (ev === null) {
        // Boot change: correlation lost; re-attach.
        refresh();
        return;
      }
      j.state = "done";
      j.outcome = ev.outcome;
      j.applied = ev.applied;
      j.offset_ms = ev.offset_ms;
      j.confidence = ev.confidence;
      const rowEl = rows.get(ev.job_id);
      if (rowEl) {
        rowEl.textContent = `${seasonItemLabel(j)} \u2014 ${seasonItemStatus(j)}`;
      }
      settle();
      if (ev.outcome === "cancelled") {
        // A stopped item means the batch was stopped: siblings settle
        // cancelled with no events of their own. A crash does not stop
        // the batch, so it needs no re-read.
        refresh();
      }
    });
    seasonUnwatchers.push(unwatch);
  }

  if (!allDone()) {
    // Batch terminals that publish no per-item sync:done (a popup stop
    // between items, a queued batch cancelled outright) settle remaining
    // rows server-side in silence; the activity's terminal transition (or
    // removal) is the signal to re-read.
    let seen = false;
    const unobserve = observeActivities((activities) => {
      if (!dlg.open) {
        return;
      }
      const entry = activities.find((a) => a.id === batchId);
      if (entry && !entry.done) {
        seen = true;
        return;
      }
      if (entry?.done || (seen && !entry)) {
        refresh();
      }
    });
    seasonUnwatchers.push(unobserve);
  }
}

/**
 * Season sync entry point: confirm, then ONE POST. A live batch for this
 * season re-attaches instead of re-confirming.
 */
export function confirmSeasonSync(
  seriesTitle: string,
  seasonNum: number,
  seriesId: number,
  fileCount: number,
): void {
  const dlg = dialog("seasonSyncConfirm");
  const label = `${seriesTitle} S${pad(seasonNum)}`;

  const ctrl = createDialog(dlg, {});
  dlg.addEventListener(
    "close",
    () => {
      stopSeasonWatches();
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
      `This will run audio sync on ${fileCount} subtitle file${
        fileCount === 1 ? "" : "s"
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
  const status = el("div", { id: "season-sync-status", hidden: true });
  body.appendChild(status);

  const startBtn = el(
    "button",
    {
      type: "button",
      id: "season-sync-start",
      onclick: () => {
        void startSeasonSync(
          dlg,
          label,
          seriesId,
          seasonNum,
          startBtn as HTMLButtonElement,
          status,
          closeFn,
        );
      },
    },
    "Start Sync",
  );
  const footer = el(
    "div",
    { className: "dlg-foot" },
    startBtn,
    el("button", { type: "button", className: "ghost", onclick: closeFn }, "Cancel"),
  );

  patch(dlg, header, body, footer);
  ctrl.open();

  // A batch dispatched before a reload is still the server's; show its
  // progress instead of offering a second start.
  void findLiveSeasonBatch(seriesId, seasonNum).then((batchId) => {
    if (batchId !== null && dlg.open) {
      void renderSeasonBatch(dlg, label, batchId, closeFn);
    }
  });
}

/** Dispatch the ONE season POST and hand the dialog to the batch view. */
async function startSeasonSync(
  dlg: HTMLDialogElement,
  label: string,
  seriesId: number,
  seasonNum: number,
  startBtn: HTMLButtonElement,
  status: HTMLElement,
  closeFn: () => void,
): Promise<void> {
  startBtn.disabled = true;
  const outcome = await seasonSyncAction.dispatch({ series_id: seriesId, season: seasonNum })
    .outcome;
  if (!dlg.open || outcome.status === "cancelled") {
    return;
  }
  if (outcome.status === "error") {
    startBtn.disabled = false;
    if (isCapacityRefusal(outcome.error)) {
      // The typed cap refusal renders inline — the only visible surface.
      status.hidden = false;
      status.textContent =
        "Sync queue is full \u2014 wait for a running sync to finish, then try again.";
    } else {
      notify.error("Season sync failed");
    }
    return;
  }
  void renderSeasonBatch(dlg, label, outcome.value.activity_id, closeFn);
}
