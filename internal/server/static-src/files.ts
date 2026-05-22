// files.ts — subtitle file manager page with delete controls

import * as notify from './notify.js';
import * as store from './store.js';
import { $, el, icon, patch, emptyDiv, errDiv, confirm } from './dom.js';
import { apiGet, apiDeleteRaw } from './api-client.js';
import { fmtEpisode, langName } from './utils.js';
import { DEFAULT_VARIANT } from './constants.js';
import { emit, BusEvent } from './bus.js';
import { openSyncDialog } from './sync.js';
import type { MediaType } from './api-types.js';

// --- API response shapes ---

interface FileEntry {
  source: string;
  media_id: string;
  language: string;
  variant: string;
  codec: string;
  path: string;
  offset_ms: number;
  size: number;
}

interface BulkDeleteResponse {
  deleted: number;
}

let filesData: FileEntry[] = [];
let currentMediaType: MediaType | '' = '';
let currentMediaID = '';
let currentTitle = '';
let currentBackPath = '/';
/** Sonarr/Radarr internal numeric ID for poster lookups. */
let currentArrId = 0;
/** media_id or '' → video path */
let videoPaths: Map<string, string> = new Map();

/**
 * Open the file manager for a media item.
 */
export function openFileManager(
  mediaType: MediaType,
  mediaID: string,
  title: string,
  backPath: string,
  paths?: Map<string, string>,
  arrId?: number,
): void {
  currentMediaType = mediaType;
  currentMediaID = mediaID;
  currentTitle = title;
  currentBackPath = backPath || '/';
  currentArrId = arrId || 0;
  videoPaths = paths || new Map();

  const path = mediaType === 'movie'
    ? `/movie/${mediaID.replace('tmdb-', '')}/files`
    : `/series/${mediaID.replace(/^tvdb-/, '').replace(/-$/, '')}/files`;
  if (location.pathname !== path) {
    history.pushState(null, '', path);
  }

  store.batch(() => {
    store.set('currentPage', 'files');
    store.set('detailCtx', null);
  });
  loadFiles();
}

async function loadFiles(): Promise<void> {
  const out = $.coverageContent;

  emit(BusEvent.PanelConfigure, false, {
    title: currentTitle,
    info: 'Subtitle Files',
    backPath: currentBackPath
  });

  // Mark as files view so needsRefresh reloads files, not library.
  store.set('detailCtx', { files: true });

  patch(out, el('div', { className: 'empty' },
    el('span', { className: 'spinner' }), ' Loading files\u2026'));

  await refreshFileData();
}

async function refreshFileData(): Promise<void> {
  const out = $.coverageContent;
  const params = new URLSearchParams({
    media_type: currentMediaType,
    media_id: currentMediaID
  });
  const data = await apiGet<FileEntry[] | null>(`/api/files?${params}`);
  if (data === null) {
    patch(out, errDiv('Failed to load files'));
    return;
  }
  filesData = data || [];
  renderFiles();
}

function renderFiles(): void {
  const out = $.coverageContent;

  let data = filesData.filter((f: FileEntry) => f.source === 'external');

  const isSeries = currentMediaType === 'episode';

  // Parse season/episode from media_id for series.
  function parseEp(mediaID: string): { season: number; episode: number } {
    const m = mediaID.match(/s(\d+)e(\d+)$/);
    if (!m) return { season: 0, episode: 0 };
    return { season: Number(m[1]), episode: Number(m[2]) };
  }

  // Sort: episode order for series, language for movies.
  const cmp = (a: FileEntry, b: FileEntry): number => {
    const ea = parseEp(a.media_id);
    const eb = parseEp(b.media_id);
    if (isSeries) {
      return (ea.season - eb.season)
        || (ea.episode - eb.episode)
        || a.language.localeCompare(b.language);
    }
    return a.language.localeCompare(b.language)
      || a.source.localeCompare(b.source);
  };
  data = [...data].sort(cmp);

  const frag = document.createDocumentFragment();

  // Add delete all button to the header row (next to back button).
  const headerEl = document.querySelector('#coveragePanel .card-head');
  if (headerEl) {
    const oldBulk = headerEl.querySelector('[data-nav="bulk-delete"]');
    if (oldBulk) oldBulk.remove();
    const extCount = filesData.filter((f: FileEntry) => f.source === 'external').length;
    if (extCount > 0) {
      const bulkBtn = el('button', {
        type: 'button', className: 'ghost btn-delete',
        'data-nav': 'bulk-delete',
        'data-tip': 'Delete all external subtitle files',
        onclick: () => bulkDelete()
      }, icon('trash'),
        el('span', { className: 'btn-text' },
          ` Delete all (${extCount})`));
      headerEl.appendChild(bulkBtn);
    }
  }

  if (data.length === 0) {
    frag.appendChild(emptyDiv('No external subtitles.'));
    patch(out, frag);
    return;
  }

  // Table.
  const headerCols: HTMLElement[] = [];
  if (isSeries) headerCols.push(el('th', null, 'Episode'));
  headerCols.push(
    el('th', null, 'Language'),
    el('th', null, 'Format'),
    el('th', null, 'Offset'),
    el('th', null, 'Size'),
    el('th', null, ''));
  const thead = el('thead', null, el('tr', null, ...headerCols));
  const tbody = el('tbody');

  for (const f of data) {
    const lang = langName(f.language)
      + (f.variant !== DEFAULT_VARIANT ? ` (${f.variant})` : '');
    // Infer codec from file extension for external files.
    let codec = f.codec || '';
    if (!codec && f.path) {
      const parts = f.path.split('.');
      const ext = (parts[parts.length - 1] ?? '').toLowerCase();
      const extMap: Record<string, string> = { srt: 'srt', ass: 'ass', ssa: 'ssa', sub: 'sub', vtt: 'vtt' };
      codec = extMap[ext] ?? ext;
    }
    const formatLabel = codec ? `${codec}: ext` : 'ext';
    const offset = f.offset_ms
      ? `${f.offset_ms > 0 ? '+' : ''}${(f.offset_ms / 1000).toFixed(1)}s`
      : '0.0s';
    const size = f.size ? formatSize(f.size) : '';
    const ep = parseEp(f.media_id);

    const actionGroup = el('div', { className: 'action-group' });
    if (f.source === 'external' && f.path) {
      // Video path: from caller map (keyed by media_id or '' for movies).
      const vp = videoPaths.get(f.media_id) || videoPaths.get('') || '';
      if (vp) {
        actionGroup.appendChild(el('button', {
          type: 'button', className: 'ghost',
          'data-tip': 'Adjust subtitle timing',
          onclick: (e: MouseEvent) => {
            e.stopPropagation();
            openSyncDialog(
              [f], vp, isSeries ? 'series' : currentMediaType as MediaType,
              currentArrId, isSeries
                ? fmtEpisode(ep.season, ep.episode)
                : currentTitle);
          }
        }, icon('sync'),
          el('span', { className: 'btn-text' }, ' Sync')));
      }
      actionGroup.appendChild(el('button', {
        type: 'button', className: 'ghost btn-delete',
        'data-tip': 'Delete this file',
        onclick: (e: MouseEvent) => {
          e.stopPropagation();
          deleteFile(f);
        }
      }, icon('trash'), el('span', { className: 'btn-text' }, ' Delete')));
    }

    const rowCols: HTMLElement[] = [];
    if (isSeries) {
      rowCols.push(el('td', null, fmtEpisode(ep.season, ep.episode)));
    }
    const formatBadge = el('span', {
      className: 'badge',
      'data-status': 'ok'
    }, formatLabel);
    const formatCell = el('td', null, formatBadge);
    rowCols.push(
      el('td', null, lang),
      formatCell,
      el('td', { 'data-col': 'offset' }, offset),
      el('td', { 'data-col': 'size' }, size),
      el('td', { 'data-col': 'actions' }, actionGroup));

    tbody.appendChild(el('tr', null, ...rowCols));
  }

  frag.appendChild(el('table', { className: 'files-table' }, thead, tbody));
  patch(out, frag);
}

async function deleteFile(f: FileEntry): Promise<void> {
  if (!f.path || f.path.includes('..')) {
    notify.error('Invalid file path');
    return;
  }
  const parts = f.path.split('/');
  const fileName = parts[parts.length - 1] ?? f.path;
  const ok = await confirm('Delete File',
    `Delete "${fileName}"? This cannot be undone.`, 'Delete');
  if (!ok) return;

  try {
    const params = new URLSearchParams({
      path: f.path,
      media_type: currentMediaType,
      media_id: f.media_id,
      language: f.language,
      variant: f.variant
    });
    const r = await apiDeleteRaw(`/api/files?${params}`);
    if (!r.ok) {
      notify.error(`Delete failed: ${r.error || 'Unknown error'}`);
      return;
    }
    notify.success('File deleted');
    refreshFileData();
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    notify.error(`Delete failed: ${msg}`);
  }
}

async function bulkDelete(): Promise<void> {
  const extCount = filesData.filter((f: FileEntry) => f.source === 'external').length;
  const ok = await confirm('Delete All External Subtitles',
    `Delete all ${extCount} external subtitle file(s)? This cannot be undone.`,
    'Delete All');
  if (!ok) return;

  const r = await apiDeleteRaw<BulkDeleteResponse>('/api/files/bulk', {
    media_type: currentMediaType,
    media_id: currentMediaID
  });
  if (r.ok && r.data) {
    notify.success(`Deleted ${r.data.deleted} file(s)`);
    refreshFileData();
  } else {
    notify.error(`Bulk delete failed: ${r.error || 'Unknown error'}`);
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
