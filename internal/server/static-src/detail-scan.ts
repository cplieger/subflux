// detail-scan.ts — Granular scan trigger helpers extracted from detail.ts.
//
// Each scan is dispatched through the action framework so we get:
//   - registry recording (visible to subscribeToActions error logger)
//   - retry on transient network failures (retryNetwork)
//   - dedupe protection against rapid-click double-scans
//
// The custom UI feedback (icon swap: spinner → check/close) is preserved
// via onSuccess/onError callbacks since framework toasts would clash
// with the inline icon-on-button pattern (we set `error: false`).

import * as store from "./store.js";
import { apiAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import type { SeriesItem, MovieDetail } from "./api-types.js";

interface ScanResponse {
  found: number;
}

const scanAction = apiAction<string, ScanResponse>({
  name: "scan.run",
  request: (url) => ({ method: "POST", path: url }),
  dedupe: (url) => `scan:${url}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: false, // UI feedback is on-button; framework toast would double up
});

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
  const iconEl = btn ? btn.querySelector(".icon, .spinner") : null;
  if (btn && iconEl) {
    btn.disabled = true;
    iconEl.className = "spinner";
  }
  store.set("scanInFlight", true);
  const data = await scanAction.dispatch(url, {
    onSuccess: (result) => {
      if (btn && iconEl) {
        iconEl.className = "icon icon-check";
        btn.disabled = false;
      }
      if (onOk) {
        onOk(result);
      }
    },
    onError: () => {
      if (btn && iconEl) {
        iconEl.className = "icon icon-close";
        btn.disabled = false;
      }
    },
    onSettled: () => {
      store.set("scanInFlight", false);
      if (store.get("refreshPending")) {
        store.batch(() => {
          store.set("refreshPending", false);
          store.set("needsRefresh", true);
        });
      }
    },
  });
  // Suppress unused-var warning; result handling is in onSuccess above.
  void data;
}

export async function triggerSeriesScan(
  series: SeriesItem,
  btn: HTMLButtonElement | null,
): Promise<void> {
  await triggerScan(`/api/scan/series/${series.id}`, btn);
}

export async function triggerSeasonScan(
  series: SeriesItem,
  seasonNum: number,
  btn: HTMLButtonElement | null,
): Promise<void> {
  await triggerScan(`/api/scan/season/${series.id}/${seasonNum}`, btn, (data) => {
    if (data.found > 0) {
      store.set("refreshPending", true);
    }
  });
}

export async function triggerMovieScan(
  movie: MovieDetail,
  btn: HTMLButtonElement | null,
): Promise<void> {
  await triggerScan(`/api/scan/movie/${movie.id}`, btn);
}
