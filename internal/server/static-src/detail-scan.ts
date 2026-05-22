// detail-scan.ts — Granular scan trigger helpers extracted from detail.ts.

import * as store from './store.js';
import { apiPostRaw } from './api-client.js';
import type { SeriesItem, MovieDetail } from './api-types.js';

interface ScanResponse {
  found: number;
}

/**
 * Trigger a scan and update the button icon with the result.
 * @param url - API endpoint to POST
 * @param btn - button element (icon swapped to spinner/check/close)
 * @param onOk - optional callback with parsed JSON on success
 */
export async function triggerScan(
  url: string,
  btn: HTMLButtonElement | null,
  onOk?: (data: ScanResponse) => void,
): Promise<void> {
  const iconEl = btn
    ? btn.querySelector('.icon, .spinner') as HTMLElement | null
    : null;
  if (btn && iconEl) {
    btn.disabled = true;
    iconEl.className = 'spinner';
  }
  store.set('scanInFlight', true);
  const r = await apiPostRaw<ScanResponse>(url);
  if (btn && iconEl) {
    if (r.ok && r.data) {
      iconEl.className = 'icon icon-check';
      btn.disabled = false;
      if (onOk) onOk(r.data);
    } else {
      iconEl.className = 'icon icon-close';
      btn.disabled = false;
    }
  }
  store.set('scanInFlight', false);
  if (store.get('refreshPending')) {
    store.batch(() => {
      store.set('refreshPending', false);
      store.set('needsRefresh', true);
    });
  }
}

export async function triggerSeriesScan(series: SeriesItem, btn: HTMLButtonElement | null): Promise<void> {
  await triggerScan(`/api/scan/series/${series.id}`, btn);
}

export async function triggerSeasonScan(
  series: SeriesItem,
  seasonNum: number,
  btn: HTMLButtonElement | null,
): Promise<void> {
  await triggerScan(
    `/api/scan/season/${series.id}/${seasonNum}`, btn,
    (data) => { if (data.found > 0) store.set('refreshPending', true); });
}

export async function triggerMovieScan(movie: MovieDetail, btn: HTMLButtonElement | null): Promise<void> {
  await triggerScan(`/api/scan/movie/${movie.id}`, btn);
}
