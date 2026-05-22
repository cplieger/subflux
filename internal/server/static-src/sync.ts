// sync.ts — Subtitle sync dialog: audio sync, manual offset, video preview, timecode controls.

import * as notify from './notify.js';
import { el, text, option, icon, dialog, closeDialog, onBackdropClose, patch } from './dom.js';
import { apiGet, apiPostRaw } from './api-client.js';
import { langName } from './utils.js';
import { DEFAULT_VARIANT } from './constants.js';
import { buildTimecodeInput, formatOffsetMs, updateTimecodeDisplay } from './sync-timecode.js';
import type { TimecodeInput } from './sync-timecode.js';
import type { SubtitleEntry, AudioSyncResponse, MediaType } from './api-types.js';

// --- Subtitle Sync Dialog ---

const syncDlg: HTMLDialogElement = dialog('syncDialog');

// Common fields shared across all sync states.
interface SyncStateBase {
  offsetMs: number;
  subtitlePath: string;
  videoPath: string;
  mediaType: MediaType | '';
  mediaId: number;
  entries: SubtitleEntry[];
  blobUrl: string;
}

// Discriminated union: invalid state combinations are unrepresentable.
type SyncState =
  | SyncStateBase & { status: 'idle'; previewStart: number; previewBuffered: boolean }
  | SyncStateBase & { status: 'preview'; ffmpegAbort: AbortController | null; previewStart: number; previewBuffered: boolean }
  | SyncStateBase & { status: 'syncing'; previewStart: number; previewBuffered: boolean };

interface LabeledEntry {
  sub: SubtitleEntry;
  label: string;
}

let syncState: SyncState = {
  status: 'idle', offsetMs: 0, subtitlePath: '', videoPath: '',
  mediaType: '', mediaId: 0, entries: [],
  previewStart: 0, previewBuffered: false,
  blobUrl: ''
};

// Whether openSyncDialog pushed a history entry that closeSyncDialog
// needs to pop (vs. direct URL navigation where no entry was pushed).
let syncPushedHistory = false;

// Set by closeSyncDialog before history.back() so the popstate handler
// in app.ts can skip the redundant applyRoute() call.
let syncClosing = false;

/** Check and reset the syncClosing flag (one-shot). */
export function consumeSyncClosing(): boolean {
  if (!syncClosing) return false;
  syncClosing = false;
  return true;
}

// buildSyncSubLabel creates display labels for subtitle entries.
// Groups by language+variant, numbers duplicates: "English", "English #1", "English #2",
// "English SDH", "French".
function buildSyncSubLabels(entries: SubtitleEntry[]): LabeledEntry[] {
  // Count how many entries share the same language+variant.
  const counts: Record<string, number> = {};
  for (const sub of entries) {
    const key = `${sub.language || ''  }|${  sub.variant || DEFAULT_VARIANT}`;
    counts[key] = (counts[key] ?? 0) + 1;
  }

  // Assign labels with numbering only when duplicates exist.
  const seen: Record<string, number> = {};
  const labels: LabeledEntry[] = [];
  for (const sub of entries) {
    const lang = langName(sub.language || '??');
    const v = sub.variant || DEFAULT_VARIANT;
    const key = `${sub.language || ''  }|${  v}`;
    const total = counts[key] ?? 0;

    let label = lang;
    if (v === 'hi') label += ' SDH';
    else if (v === 'forced') label += ' Forced';

    if (total > 1) {
      seen[key] = (seen[key] ?? 0) + 1;
      label += ` #${  seen[key]!}`;
    }

    labels.push({ sub, label });
  }
  return labels;
}

export function openSyncDialog(
  entries: SubtitleEntry[],
  videoPath: string,
  mediaType: MediaType,
  mediaId: number,
  mediaLabel: string
): void {
  const dlg = syncDlg;

  // Push sync URL (mirrors search popup pattern).
  const currentPath = location.pathname.replace(/\/sync$/, '');
  const syncPath = `${currentPath}/sync`;
  if (location.pathname !== syncPath) {
    history.pushState(null, '', syncPath);
    syncPushedHistory = true;
  } else {
    syncPushedHistory = false;
  }

  const initialOffset = entries[0]?.offset_ms ?? 0;
  syncState = {
    status: 'idle', offsetMs: initialOffset, subtitlePath: '', videoPath: videoPath || '',
    mediaType: mediaType || '', mediaId: mediaId || 0,
    entries: entries, previewStart: 0, previewBuffered: false,
    blobUrl: ''
  };

  const labeled = buildSyncSubLabels(entries);

  // Header: title + subtitle dropdown + close.
  const subSel = el('select', {
    id: 'sync-sub-sel', 'aria-label': 'Subtitle'
  }) as HTMLSelectElement;
  for (let i = 0; i < labeled.length; i++) {
    subSel.appendChild(option(String(i), labeled[i]!.label));
  }
  subSel.value = '0';
  subSel.addEventListener('change', () => {
    const idx = parseInt(subSel.value, 10);
    const entry = labeled[idx];
    if (!entry) return;
    syncState.subtitlePath = entry.sub.path || '';
    syncState.offsetMs = entry.sub.offset_ms || 0;
    updateTimecodeDisplay(syncState.offsetMs);
    const rb = document.getElementById('sync-reset-btn');
    if (rb) rb.hidden = syncState.offsetMs === 0;
    // Reload subtitle track on the video with the new language.
    if (syncState.status === 'preview') updatePreviewTrack();
  });

  const titleParts: Array<string | Node> = [text('Subtitle Sync')];
  if (mediaLabel) {
    titleParts.push(el('span', { className: 'sync-media' },
      ' \u00B7 ', mediaLabel));
  }

  const header = el('div', { className: 'dlg-head' },
    el('div', { className: 'dlg-title' }, ...titleParts),
    el('div', { className: 'dlg-controls' },
      subSel,
      el('button', {
        type: 'button', className: 'close-btn ghost',
        'aria-label': 'Close sync',
        onclick: closeSyncDialog
      }, icon('close'))
    )
  );

  syncState.subtitlePath = (labeled[0] && labeled[0].sub.path) || '';

  const body = el('div', { className: 'dlg-body' });

  // Help text.
  body.appendChild(el('div', { className: 'sync-help' },
    'Use ', el('strong', null, 'Sync to Audio'),
    ' for automatic alignment, then fine-tune with the manual '
    + 'controls. Play the video to verify timing.'));

  // Audio sync button and result.
  const audioResultDiv = el('div', {
    className: 'sync-audio-result', hidden: true
  });
  const audioBtn = el('button', {
    type: 'button', className: 'sync-audio-btn',
    onclick: () => runAudioSync(audioBtn as HTMLButtonElement, audioResultDiv)
  }, syncIcon(), ' Sync to Audio');
  body.appendChild(el('div', { className: 'sync-audio' },
    audioBtn, audioResultDiv));

  // Video preview: backdrop/poster background with play button.
  const preview = buildVideoPreview();
  if (preview) body.appendChild(preview);

  // Manual offset controls.
  const timecode = buildTimecodeInput(syncState.offsetMs, (newMs: number) => {
    syncState.offsetMs = newMs;
    const rb = document.getElementById('sync-reset-btn');
    if (rb) rb.hidden = newMs === 0;
    if (syncState.status === 'preview') updatePreviewTrack();
  });

  const offsetSection = el('div', { className: 'sync-offset' }, timecode);
  body.appendChild(offsetSection);

  // Footer with Save + Reset (matches config dialog footer).
  const footer = el('div', { className: 'dlg-foot' });
  footer.appendChild(el('button', {
    type: 'button',
    onclick: () => applyManualOffset()
  }, 'Save Offset'));
  const resetBtn = el('button', {
    type: 'button', className: 'ghost', id: 'sync-reset-btn',
    hidden: syncState.offsetMs === 0,
    onclick: resetSync
  }, 'Reset');
  footer.appendChild(resetBtn);

  dlg.replaceChildren(header, body, footer);
  if (dlg.open) dlg.close();
  dlg.style.removeProperty('opacity');
  dlg.style.removeProperty('translate');
  dlg.showModal();
  (document.activeElement as HTMLElement | null)?.blur();

  // Arrow keys adjust the active timecode segment from anywhere in the dialog.
  // Use onkeydown property to avoid accumulating listeners across reopens.
  dlg.onkeydown = (e: KeyboardEvent) => (timecode as TimecodeInput).handleKey(e);

  // Close on backdrop click.
  onBackdropClose(dlg, closeSyncDialog);

  // Close on Escape: prevent the browser's default close so
  // closeSyncDialog can run the animated close, video cleanup, and URL fixup.
  dlg.addEventListener('cancel', (e: Event) => {
    e.preventDefault();
    closeSyncDialog();
  }, { once: true });
}

// Build the video preview container with poster background and play overlay.
function buildVideoPreview(): HTMLElement | null {
  if (!syncState.videoPath) return null;
  const previewContainer = el('div', {
    className: 'sync-preview', id: 'sync-preview-container'
  });
  if (syncState.mediaId) {
    const fanartUrl = `/api/preview/poster?type=${
       encodeURIComponent(syncState.mediaType)
       }&id=${  syncState.mediaId  }&style=fanart`;
    const posterUrl = `/api/preview/poster?type=${
       encodeURIComponent(syncState.mediaType)
       }&id=${  syncState.mediaId}`;
    previewContainer.style.backgroundImage =
      `url(${  fanartUrl  }), url(${  posterUrl  })`;
    previewContainer.style.backgroundSize = 'cover';
    previewContainer.style.backgroundPosition = 'center';
  }
  const playOverlay = el('button', {
    type: 'button',
    className: 'sync-play',
    'aria-label': 'Play video preview',
    onclick: () => {
      playOverlay.remove();
      previewContainer.style.backgroundImage = 'none';
      toggleVideoPreview(previewContainer);
    }
  }, previewPlayIcon());
  previewContainer.appendChild(playOverlay);
  return previewContainer;
}

function closeSyncDialog(): void {
  if (syncState.status === 'preview' && syncState.ffmpegAbort) {
    syncState.ffmpegAbort.abort();
  }
  syncState = { ...syncState, status: 'idle' };
  // Revoke any outstanding blob URL that sourceopen didn't clean up.
  if (syncState.blobUrl) {
    URL.revokeObjectURL(syncState.blobUrl);
    syncState.blobUrl = '';
  }
  // Clean up video before closing.
  const video = syncDlg.querySelector('video');
  if (video) {
    (video as HTMLVideoElement).pause();
    video.removeAttribute('src');
    (video as HTMLVideoElement).load();
  }
  closeDialog(syncDlg);
  // Remove the /sync history entry. history.back() fires popstate
  // which calls applyRoute(); the popstate handler checks
  // syncClosing to skip the redundant re-render.
  if (syncPushedHistory && location.pathname.endsWith('/sync')) {
    syncPushedHistory = false;
    syncClosing = true;
    history.back();
  } else if (location.pathname.endsWith('/sync')) {
    const parent = location.pathname.replace(/\/sync$/, '');
    history.replaceState(null, '', parent || '/');
  }
}

async function applyManualOffset(): Promise<void> {
  if (!syncState.subtitlePath) {
    notify.error('No subtitle selected');
    return;
  }
  const r = await apiPostRaw<unknown>('/api/sync/offset', {
    subtitle_path: syncState.subtitlePath,
    offset_ms: syncState.offsetMs
  });
  if (!r.ok) {
    notify.error(`Save failed: ${r.error || 'Unknown error'}`);
    return;
  }
  // Update cached entries so reopening the dialog shows the new offset.
  for (const e of syncState.entries) {
    if (e.path === syncState.subtitlePath) {
      e.offset_ms = syncState.offsetMs;
    }
  }
  notify.success(`Offset saved: ${formatOffsetMs(syncState.offsetMs)}`);
  closeSyncDialog();
}

async function runAudioSync(btn: HTMLButtonElement, resultDiv: HTMLElement): Promise<void> {
  if (!syncState.subtitlePath || !syncState.videoPath) {
    notify.error('Subtitle and video paths required');
    return;
  }
  btn.disabled = true;
  const origNodes = Array.from(btn.childNodes, (n: ChildNode) => n.cloneNode(true));
  btn.textContent = 'Syncing\u2026';
  resultDiv.hidden = true;
  try {
    const r = await apiPostRaw<AudioSyncResponse>('/api/sync/audio', {
      subtitle_path: syncState.subtitlePath,
      video_path: syncState.videoPath,
      dry_run: true
    });
    if (!r.ok || !r.data) {
      notify.error(`Audio sync failed: ${r.error || 'Unknown error'}`);
      return;
    }
    const data = r.data;
    resultDiv.hidden = false;
    resultDiv.className = 'sync-audio-result';
    if (data.applied) {
      resultDiv.textContent = `${formatOffsetMs(data.offset_ms)
         } (${  (data.confidence * 100).toFixed(0)  }% confidence)`;
      syncState.offsetMs = data.offset_ms;
      updateTimecodeDisplay(data.offset_ms);
      const rb2 = document.getElementById('sync-reset-btn');
      if (rb2) rb2.hidden = data.offset_ms === 0;
      if (syncState.status === 'preview') updatePreviewTrack();
      notify.success('Audio sync offset applied to preview');
    } else {
      resultDiv.textContent = `Low confidence (${
         (data.confidence * 100).toFixed(0)  }%). No changes.`;
    }
  } finally {
    btn.replaceChildren(...origNodes);
    btn.disabled = false;
  }
}

function resetSync(): void {
  syncState.offsetMs = 0;
  updateTimecodeDisplay(0);
  const rb = document.getElementById('sync-reset-btn');
  if (rb) rb.hidden = true;
  if (syncState.status === 'preview') updatePreviewTrack();
}

interface PreviewStartResponse {
  start_seconds: number;
}

async function toggleVideoPreview(container: HTMLElement): Promise<void> {
  if (syncState.status === 'preview') {
    if (syncState.ffmpegAbort) {
      syncState.ffmpegAbort.abort();
    }
    syncState = { ...syncState, status: 'idle' };
    const playOverlay = el('button', {
      type: 'button',
      className: 'sync-play',
      'aria-label': 'Play video preview',
      onclick: () => {
        playOverlay.remove();
        toggleVideoPreview(container);
      }
    }, previewPlayIcon());
    patch(container, playOverlay);
    return;
  }

  patch(container,
    el('div', { className: 'sync-loading' },
      el('span', { className: 'spinner' }),
      ' Finding best scene\u2026'));

  // Find dialogue-dense start point.
  let startSec = 0;
  const r = await apiGet<PreviewStartResponse>(`/api/preview/start?subtitle=${encodeURIComponent(syncState.subtitlePath)}`);
  if (r) startSec = r.start_seconds || 0;

  syncState.previewStart = startSec;
  syncState = { ...syncState, status: 'preview', ffmpegAbort: null, previewStart: startSec };

  startPreviewStream(container, startSec);
}

function startPreviewStream(container: HTMLElement, startSec: number): void {
  const mimeType = 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"';
  const canMSE = typeof MediaSource !== 'undefined'
    && MediaSource.isTypeSupported(mimeType);

  syncState.previewBuffered = !canMSE;
  syncState.previewStart = startSec;

  const video = el('video', {
    autoplay: '', preload: 'none',
    disablepictureinpicture: '',
    playsinline: ''
  }) as HTMLVideoElement;

  if (canMSE) {
    startMSEStream(video, startSec);
  } else {
    video.src = `/api/preview/video?path=${
       encodeURIComponent(syncState.videoPath)
       }&start=${  startSec  }&buffered=true`;
  }

  reloadSubtitleTrack(video);
  video.addEventListener('loadedmetadata', () => {
    const t = video.querySelector('track');
    if (t && t.track) t.track.mode = 'showing';
  }, { once: true });

  video.addEventListener('error', () => {
    if (syncState.status !== 'preview') return;
    patch(container,
      el('div', { className: 'sync-unavailable' },
        'Preview unavailable for this file.'));
    syncState = { ...syncState, status: 'idle' };
  }, { once: true });

  const seekRow = buildSeekControls(video);

  const videoWrap = el('div', { className: 'sync-video-wrap' }, video);
  patch(container, videoWrap, seekRow);
}

// Build seek controls row: -30s, -10s, play/pause, +10s, +30s buttons
// with a loading spinner that disappears on first frame.
function buildSeekControls(
  video: HTMLVideoElement
): HTMLElement {
  const seekRow = el('div', { className: 'sync-seek' });
  const seekSpinner = el('span', { className: 'spinner sync-seek-spinner' });
  seekRow.appendChild(seekSpinner);

  video.addEventListener('loadeddata', () => seekSpinner.remove(), { once: true });

  for (const delta of [-30, -10]) {
    seekRow.appendChild(el('button', {
      type: 'button', className: 'sync-offset-btn',
      onclick: () => seekPreview(video, delta)
    }, `${delta}s`));
  }

  const playPauseBtn = el('button', {
    type: 'button', className: 'sync-offset-btn',
    'aria-label': 'Play/Pause',
    onclick: () => {
      if (video.paused) {
        video.play().catch(() => {});
      } else {
        video.pause();
      }
    }
  }, icon('pause'));
  seekRow.appendChild(playPauseBtn);

  video.addEventListener('play', () =>
    playPauseBtn.replaceChildren(icon('pause')));
  video.addEventListener('pause', () =>
    playPauseBtn.replaceChildren(icon('play')));

  for (const delta of [10, 30]) {
    seekRow.appendChild(el('button', {
      type: 'button', className: 'sync-offset-btn',
      onclick: () => seekPreview(video, delta)
    }, `+${delta}s`));
  }

  return seekRow;
}

// Start MSE-based fMP4 streaming. Fetches the chunked fMP4 from the
// server and feeds it into a SourceBuffer for instant playback on all
// browsers including Safari.
function startMSEStream(video: HTMLVideoElement, startSec: number): void {
  // Abort any previous stream.
  if (syncState.status === 'preview' && syncState.ffmpegAbort) {
    syncState.ffmpegAbort.abort();
  }
  const abort = new AbortController();
  syncState = { ...syncState, status: 'preview', ffmpegAbort: abort, previewStart: startSec };

  const ms = new MediaSource();
  const objectUrl = URL.createObjectURL(ms);
  video.src = objectUrl;
  syncState.blobUrl = objectUrl;

  ms.addEventListener('sourceopen', async () => {
    // Revoke the object URL once the MediaSource is connected to the
    // video element. The connection persists after revocation; this
    // just frees the URL→blob mapping to prevent memory leaks.
    URL.revokeObjectURL(objectUrl);
    syncState.blobUrl = '';
    const mime = 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"';
    const sb = ms.addSourceBuffer(mime);

    const url = `/api/preview/video?path=${
       encodeURIComponent(syncState.videoPath)
       }&start=${  startSec}`;

    try {
      const resp = await fetch(url, {
        signal: abort.signal
      });
      if (!resp.ok || !resp.body) {
        if (ms.readyState === 'open') ms.endOfStream('network');
        return;
      }

      const reader = resp.body.getReader();
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        // Wait for the SourceBuffer to finish updating before appending.
        if (sb.updating) {
          await new Promise<void>((resolve) => {
            sb.addEventListener('updateend', () => resolve(), { once: true });
          });
        }
        try {
          sb.appendBuffer(value);
          await new Promise<void>((resolve) => {
            sb.addEventListener('updateend', () => resolve(), { once: true });
          });
        } catch {
          // QuotaExceededError or InvalidStateError; stop appending.
          break;
        }
      }
      if (ms.readyState === 'open') ms.endOfStream();
    } catch (e: unknown) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      if (ms.readyState === 'open') ms.endOfStream('network');
    }
  }, { once: true });
}

// Reload the subtitle track on the video element. Called on initial load,
// seek, and manual offset changes. Removes old track and creates a new one
// with the correct start position and user offset.
function reloadSubtitleTrack(video: HTMLVideoElement | null): void {
  if (!video) {
    video = document.querySelector('.sync-preview video') as HTMLVideoElement | null;
    if (!video) return;
  }
  for (const t of Array.from(video.querySelectorAll('track'))) t.remove();

  const trackUrl = `/api/preview/subtitle?path=${
     encodeURIComponent(syncState.subtitlePath)
     }&start=${  syncState.previewStart || 0
     }&shift=${  syncState.offsetMs || 0}`;
  const track = el('track', {
    kind: 'subtitles', src: trackUrl,
    srclang: 'en', label: 'Subtitles', default: ''
  }) as HTMLTrackElement;
  video.appendChild(track);
  // Safari needs the track to be explicitly set to showing after load.
  const show = (): void => { if (track.track) track.track.mode = 'showing'; };
  track.addEventListener('load', show, { once: true });
  // Also try immediately for browsers that load synchronously.
  requestAnimationFrame(show);
}

function seekPreview(video: HTMLVideoElement, deltaSec: number): void {
  if (syncState.status !== 'preview') return;

  const absNow = (syncState.previewStart || 0)
    + (video.currentTime || 0);
  const absTarget = Math.max(0, absNow + deltaSec);

  const bufStart = syncState.previewStart || 0;
  const bufEnd = bufStart + (video.buffered.length > 0
    ? video.buffered.end(video.buffered.length - 1) : 0);

  if (absTarget >= bufStart && absTarget <= bufEnd) {
    video.currentTime = absTarget - bufStart;
  } else {
    // Seek outside buffer: restart the stream from the new position.
    syncState.previewStart = absTarget;
    if (!syncState.previewBuffered) {
      startMSEStream(video, absTarget);
    } else {
      video.src = `/api/preview/video?path=${
         encodeURIComponent(syncState.videoPath)
         }&start=${  absTarget  }&buffered=true`;
    }
    video.play().catch(() => {});
    video.addEventListener('loadedmetadata', () => {
      reloadSubtitleTrack(video);
    }, { once: true });
  }
}

function updatePreviewTrack(): void {
  reloadSubtitleTrack(null);
}

// syncIcon builds a waveform SVG inline because it uses a custom
// path that doesn't map to the CSS mask-image icon system.
function syncIcon(): SVGSVGElement {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('width', '14');
  svg.setAttribute('height', '14');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '2');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  // Waveform icon
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('d', 'M2 12h2l3-7 4 14 4-14 3 7h2');
  svg.appendChild(path);
  return svg;
}

// previewPlayIcon builds a large play button SVG (48x48) with
// filled shapes, unlike the stroke-based icon system.
function previewPlayIcon(): SVGSVGElement {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('width', '48');
  svg.setAttribute('height', '48');
  svg.setAttribute('viewBox', '0 0 48 48');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('aria-hidden', 'true');
  const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
  circle.setAttribute('cx', '24');
  circle.setAttribute('cy', '24');
  circle.setAttribute('r', '22');
  circle.setAttribute('fill', 'oklch(0% 0 0deg / 50%)');
  circle.setAttribute('stroke', 'white');
  circle.setAttribute('stroke-width', '2');
  svg.appendChild(circle);
  const poly = document.createElementNS('http://www.w3.org/2000/svg', 'polygon');
  poly.setAttribute('points', '19,14 35,24 19,34');
  poly.setAttribute('fill', 'white');
  svg.appendChild(poly);
  return svg;
}
