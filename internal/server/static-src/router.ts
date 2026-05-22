// Client-side routing: URL-driven page navigation with view transitions.

import * as store from './store.js';
import { $, el, icon, input, patch, select } from './dom.js';
import { apiGet } from './api-client.js';
import { openConfig } from './config.js';
import { loadCoverage, configurePanel, renderCoverage } from './coverage.js';
import { on, emit, BusEvent } from './bus.js';
import { openSearchPopup } from './search.js';
import { openFileManager } from './files.js';
import { viewTransition } from './utils.js';
import { ROUTE_TRANSITION_MS } from './constants.js';
import type { CoverageItem, SeasonGroup } from './api-types.js';

// --- Page navigation and client-side routing ---

// Immediately prepare the card for a detail route: hide library
// controls and heading so they don't flash before the detail loads.
function prepareDetailView(): void {
  showPage('library', true);
  const ctrl = $.coveragePanel.querySelector('.controls') as HTMLElement | null;
  if (ctrl) ctrl.style.display = 'none';
  $.libHeading.textContent = '';
  const out = $.coverageContent;
  const skel = document.createDocumentFragment();
  for (let i = 0; i < 4; i++) {
    skel.appendChild(el('div', { className: 'skeleton-row' },
      el('div', { className: 'skeleton' })
    ));
  }
  patch(out, skel);
}

const covTypeFilter = select('cov-type-filter');
const covFilter = input('cov-filter');
const covMissing = input('cov-missing');
const covSort = select('cov-sort');

// Serialize current library filter state into URL query params.
function buildLibraryQuery(): string {
  const params = new URLSearchParams();
  const type = covTypeFilter.value;
  const q = covFilter.value;
  const missing = covMissing.checked;
  const sort = covSort.value;
  if (type && type !== 'all') params.set('type', type);
  if (q) params.set('q', q);
  if (missing) params.set('missing', '1');
  if (sort && sort !== 'title') params.set('sort', sort);
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

// Restore library filter state from URL query params.
function restoreLibraryFilters(): void {
  const params = new URLSearchParams(location.search);
  covTypeFilter.value = params.get('type') || 'all';
  covFilter.value = params.get('q') || '';
  covMissing.checked = params.get('missing') === '1';
  covSort.value = params.get('sort') || 'title';
}

// Push a new URL and apply the route. Use replace=true for initial load
// or when correcting the URL without adding a history entry.
export function navigate(path: string, replace?: boolean): void {
  if (path !== location.pathname + location.search) {
    if (replace) history.replaceState(null, '', path);
    else history.pushState(null, '', path);
  }
  viewTransition(() => applyRoute());
}

// Update library filter query params in the URL without a full
// navigation. Called when filter controls change.
export function updateLibraryFilters(): void {
  const qs = buildLibraryQuery();
  const newUrl = `/${qs}`;
  if (newUrl !== location.pathname + location.search) {
    history.replaceState(null, '', newUrl);
  }
}

// --- Route handlers ---

interface Route {
  pattern: RegExp;
  handler: (m: RegExpMatchArray) => Promise<void> | void;
}

async function withSeries(
  m: RegExpMatchArray,
  action: (s: CoverageItem) => void | Promise<void>
): Promise<void> {
  prepareDetailView();
  const tvdbId = Number(m[1]);
  const s = await findCoverageItem('series', 'tvdb_id', tvdbId);
  if (s) await action(s);
}

async function withMovie(
  m: RegExpMatchArray,
  action: (mv: CoverageItem) => void | Promise<void>
): Promise<void> {
  prepareDetailView();
  const tmdbId = Number(m[1]);
  const mv = await findCoverageItem('movie', 'tmdb_id', tmdbId);
  if (mv) await action(mv);
}

async function handleSeriesSearch(m: RegExpMatchArray): Promise<void> {
  const lang = m[2] ?? null;
  await withSeries(m, (s) => {
    emit(BusEvent.OpenSeries, s, true);
    setTimeout(() => openSearchPopup('episode', s, null, null, lang), ROUTE_TRANSITION_MS);
  });
}

async function handleSeriesSync(m: RegExpMatchArray): Promise<void> {
  await withSeries(m, (s) => {
    emit(BusEvent.OpenSeries, s, true);
    setTimeout(() => {
      const btn = document.querySelector(
        '[data-nav="sync"]') as HTMLElement | null;
      if (btn) btn.click();
      else navigate(`/series/${m[1]!}`, true);
    }, ROUTE_TRANSITION_MS);
  });
}

async function handleSeriesFiles(m: RegExpMatchArray): Promise<void> {
  const tvdbId = Number(m[1]);
  await withSeries(m, async (s) => {
    const epPaths = new Map<string, string>();
    const seasons = (await apiGet<SeasonGroup[]>(`/api/media/series/${s.id}/episodes`)) || [];
    for (const sg of seasons) {
      for (const ep of (sg.episodes || [])) {
        if (ep.has_file && ep.path) {
          const sn = String(sg.season).padStart(2, '0');
          const en = String(ep.episode).padStart(2, '0');
          epPaths.set(`tvdb-${tvdbId}-s${sn}e${en}`, ep.path);
        }
      }
    }
    openFileManager('episode', `tvdb-${tvdbId}-`,
      s.title, `/series/${tvdbId}`, epPaths, s.id);
  });
}

async function handleSeriesDetail(m: RegExpMatchArray): Promise<void> {
  await withSeries(m, (s) => emit(BusEvent.OpenSeries, s, true));
}

async function handleMovieSearch(m: RegExpMatchArray): Promise<void> {
  const lang = m[2] ?? null;
  await withMovie(m, (mv) => {
    emit(BusEvent.OpenMovie, mv, true);
    setTimeout(() => openSearchPopup('movie', mv, null, null, lang), ROUTE_TRANSITION_MS);
  });
}

async function handleMovieSync(m: RegExpMatchArray): Promise<void> {
  await withMovie(m, (mv) => {
    emit(BusEvent.OpenMovie, mv, true);
    setTimeout(() => {
      const btn = document.querySelector(
        '[data-nav="sync"]') as HTMLElement | null;
      if (btn) btn.click();
      else navigate(`/movie/${m[1]!}`, true);
    }, ROUTE_TRANSITION_MS);
  });
}

async function handleMovieFiles(m: RegExpMatchArray): Promise<void> {
  const tmdbId = Number(m[1]);
  await withMovie(m, (mv) => {
    openFileManager('movie', `tmdb-${tmdbId}`,
      mv.title, `/movie/${tmdbId}`,
      new Map([['', mv.path || '']]), mv.id);
  });
}

async function handleMovieDetail(m: RegExpMatchArray): Promise<void> {
  await withMovie(m, (mv) => emit(BusEvent.OpenMovie, mv, true));
}

// More specific patterns must precede less specific ones (e.g.
// /series/{id}/search/{lang} before /series/{id}).
const routes: Route[] = [
  { pattern: /^\/series\/(\d+)\/search\/([a-z]{2,3})$/, handler: handleSeriesSearch },
  { pattern: /^\/series\/(\d+)\/sync$/, handler: handleSeriesSync },
  { pattern: /^\/series\/(\d+)\/files$/, handler: handleSeriesFiles },
  { pattern: /^\/series\/(\d+)$/, handler: handleSeriesDetail },
  { pattern: /^\/movie\/(\d+)\/search\/([a-z]{2,3})$/, handler: handleMovieSearch },
  { pattern: /^\/movie\/(\d+)\/sync$/, handler: handleMovieSync },
  { pattern: /^\/movie\/(\d+)\/files$/, handler: handleMovieFiles },
  { pattern: /^\/movie\/(\d+)$/, handler: handleMovieDetail },
];

// Read location.pathname and render the matching view.
// This is called on initial load, pushState navigation, and popstate.
export async function applyRoute(): Promise<void> {
  const path = location.pathname;

  // Simple path matches (no regex needed).
  if (path === '/settings') {
    showPage('library');
    openConfig(true);
    return;
  }
  if (path === '/history') { showPage('history'); return; }
  if (path === '/movies') { navigate('/?type=movies', true); return; }

  // Regex-based route table.
  for (const route of routes) {
    const m = path.match(route.pattern);
    if (m) { await route.handler(m); return; }
  }

  // Default: / (library)
  restoreLibraryFilters();
  showPage('library');
}

// Show a page without pushing history (used by applyRoute).
// skipRender: true to toggle panels without re-rendering content
// (used by detail routes that replace content themselves).
function showPage(page: string, skipRender?: boolean): void {
  store.set('currentPage', page);
  $.coveragePanel.hidden = page !== 'library';
  $.historyPanel.hidden = page !== 'history';
  $.historyBtn.classList.toggle('active', page === 'history');
  if (page === 'history') setHistoryHeader();
  if (skipRender) return;
  if (page === 'history') emit(BusEvent.LoadHistory);
  if (page === 'library') {
    configurePanel(true);
    // Re-render from cache if available; otherwise fetch fresh data.
    if (store.get('coverageData')) renderCoverage();
    else loadCoverage();
  }
}

let historyBackPath: string | null = null;

// Manage the history panel header: add/remove back button.
function setHistoryHeader(): void {
  const headerEl = document.querySelector('#historyPanel .card-head');
  const heading = document.getElementById('hist-heading');
  if (!headerEl || !heading) return;
  headerEl.querySelectorAll('.detail-nav').forEach((e: Element) => e.remove());

  heading.textContent = 'History';

  const backPath = historyBackPath || '/';
  const backText = historyBackPath ? ' Back' : ' Library';
  const backBtn = el('button', {
    type: 'button', className: 'ghost detail-nav',
    onclick: () => { historyBackPath = null; navigate(backPath); }
  }, icon('arrow-left'),
    el('span', { className: 'btn-text' }, backText));
  headerEl.insertBefore(backBtn, heading);
}


// Ensure coverage data is loaded, then find a media item by type and ID.
async function findCoverageItem(
  type: string, idField: 'tvdb_id' | 'tmdb_id', id: number
): Promise<CoverageItem | null> {
  if (!store.get('coverageData')) {
    try { await loadCoverage(); } catch { /* fall through */ }
  }
  const data = store.get('coverageData');
  if (!data) return null;
  return data.find((item: CoverageItem) =>
    item._type === type && item[idField] === id) ?? null;
}

export function navigateToHistory(mediaFilter?: string): void {
  const filterEl = input('h-filter');
  if (mediaFilter) filterEl.value = mediaFilter;
  else filterEl.value = '';
  // Remember where we came from so the history page can show a back button.
  historyBackPath = mediaFilter ? location.pathname : null;
  navigate('/history');
}
// --- Bus handlers ---
on(BusEvent.NavRoute, (path) => navigate(path));
on(BusEvent.NavHistory, (filter) => navigateToHistory(filter));
