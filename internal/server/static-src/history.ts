// history.ts — download history page (server-side paginated)

import { el, input, select, option } from './dom.js';
import { apiAction, retryNetwork, RETRY_STANDARD } from './actions/index.js';
import { on, emit, BusEvent } from './bus.js';
import { fmtDateTime, fmtEpisode, clickableRow } from './utils.js';
import { createPagedList } from './paged-list.js';
import type { PagedList, Page } from './paged-list.js';
import { reconcile } from './lib/reactive/reconcile.js';

interface HistoryEntry {
  media_id: string;
  media_type: string;
  language: string;
  provider: string;
  release_name: string;
  title: string;
  season: number;
  episode: number;
  manual: boolean;
  media_imported: string;
}

const PAGE_SIZE = 50;
let list: PagedList | null = null;

function buildApiUrl(offset: number, limit: number): string {
  const params = new URLSearchParams();
  params.set('limit', String(limit));
  if (offset > 0) params.set('offset', String(offset));
  const type = select('h-type').value;
  const lang = select('h-lang').value;
  const prov = select('h-provider').value;
  const search = input('h-filter').value.trim();
  if (type) params.set('type', type);
  if (lang) params.set('lang', lang);
  if (prov) params.set('provider', prov);
  if (search) params.set('search', search);
  return `/api/state?${params.toString()}`;
}

/** Fetch a page of history entries. Pure GET; failures show as empty
 *  page (caller renders empty-state) but retryNetwork handles transient
 *  blips so a single network hiccup doesn't leave the user staring at
 *  "no downloads". error: false because the empty-state IS the error UI. */
const loadHistoryAction = apiAction<string, HistoryEntry[]>({
  name: "history.load",
  request: (path) => ({ method: "GET", path }),
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: false,
});

async function fetchPage(offset: number, limit: number): Promise<Page<HistoryEntry>> {
  const items = (await loadHistoryAction.dispatch(buildApiUrl(offset, limit))) ?? [];
  return { items, hasMore: items.length >= limit };
}

function historyMediaHref(entry: HistoryEntry): string {
  const mid = entry.media_id || '';
  const tm = mid.match(/^tmdb-(\d+)$/);
  if (tm) return `/movie/${tm[1]}`;
  const tv = mid.match(/^tvdb-(\d+)/);
  if (tv) return `/series/${tv[1]}`;
  return '';
}

function renderItems(items: HistoryEntry[]): DocumentFragment {
  const frag = document.createDocumentFragment();
  const thead = el('thead', null, el('tr', null,
    el('th', null, 'Time'),
    el('th', null, 'Media'),
    el('th', null, 'Lang'),
    el('th', null, 'Provider'),
    el('th', null, 'Mode'),
    el('th', null, 'Release')
  ));
  const tbody = el('tbody');

  reconcile(tbody, items, {
    key: (entry) => `${entry.media_id}-${entry.media_imported}`,
    mount: (entry) => buildHistoryRow(entry),
  });

  frag.appendChild(el('table', { className: 'history' }, thead, tbody));
  updateHistoryFilters(items);
  return frag;
}

function buildHistoryRow(entry: HistoryEntry): HTMLElement {
  const time = fmtDateTime(new Date(entry.media_imported));
  let label = entry.title || '';
  if (entry.season > 0 || entry.episode > 0)
    label += ` \u00B7 ${fmtEpisode(entry.season, entry.episode)}`;
  const href = historyMediaHref(entry);
  const cells = [
    el('td', { 'data-col': 'meta' }, time),
    el('td', { 'data-col': 'title' }, label),
    el('td', { 'data-col': 'meta' }, entry.language),
    el('td', { 'data-col': 'meta' }, entry.provider),
    el('td', { 'data-col': 'meta' }, entry.manual ? 'manual' : 'auto'),
    el('td', { 'data-col': 'meta' }, entry.release_name || '')
  ];
  return href
    ? clickableRow(() => emit(BusEvent.NavRoute, href), ...cells) as HTMLElement
    : el('tr', null, ...cells);
}

/** Populate language and provider dropdowns from accumulated data. */
function updateHistoryFilters(data: HistoryEntry[]): void {
  const langs = new Set<string>();
  const provs = new Set<string>();
  for (const entry of data) {
    if (entry.language) langs.add(entry.language);
    if (entry.provider) provs.add(entry.provider);
  }

  const hLang = select('h-lang');
  if (hLang) {
    const current = hLang.value;
    hLang.replaceChildren(option('', 'All languages'));
    for (const l of [...langs].sort()) hLang.appendChild(option(l, l));
    hLang.value = current;
  }

  const hProv = select('h-provider');
  if (hProv) {
    const current = hProv.value;
    hProv.replaceChildren(option('', 'All providers'));
    for (const p of [...provs].sort()) hProv.appendChild(option(p, p));
    hProv.value = current;
  }
}

function ensureList(): PagedList {
  if (!list) {
    const container = document.getElementById('historyContent');
    if (!container) throw new Error('historyContent not found');
    list = createPagedList<HistoryEntry>({
      container,
      fetchPage,
      renderItems,
      pageSize: PAGE_SIZE,
      emptyMessage: 'No downloads matching filter.',
      emptyNoData: 'No downloads yet.'
    });
  }
  return list;
}

async function loadHistory(): Promise<void> {
  await ensureList().reload();
}

export function reloadHistory(): void {
  ensureList().reload();
}

on(BusEvent.LoadHistory, () => loadHistory());
