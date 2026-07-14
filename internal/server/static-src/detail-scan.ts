// detail-scan.ts — Granular scan trigger helpers extracted from detail.ts.
//
// Each scan is dispatched through the action framework so we get:
//   - registry recording (visible to subscribeToActions error logger)
//   - retry on transient network failures (retryNetwork)
//   - dedupe protection against rapid-click double-scans
//
// The on-button UI feedback (icon swap: spinner → check/close) is provided by
// @cplieger/actions' withAsyncFeedback in single-slot `target` mode: it
// replaces ONLY the icon <span> (the " Search" label sibling is untouched),
// disables the button (+ aria-busy + sr-only announce) during the scan, and
// with `resetMs: 0` PERSISTS the ✓/✗ glyph (no auto-revert) until a coverage
// SSE re-renders the row. The action keeps `error: false` so the framework
// toast doesn't clash with the on-button glyph.

import * as store from "./store.js";
import { emit, BusEvent } from "./bus.js";
import { apiAction, retryNetwork, RETRY_STANDARD, withAsyncFeedback } from "@cplieger/actions";
import { el } from "./dom.js";
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
 * @param btn - button element (icon slot swapped to spinner/check/close)
 * @param onOk - optional callback with parsed JSON on success
 */
export async function triggerScan(
  url: string,
  btn: HTMLButtonElement | null,
  onOk?: (data: ScanResponse) => void,
): Promise<void> {
  store.set("scanInFlight", true);

  // dispatch() resolves the parsed body on success or `null` on error/cancel
  // (error: false ⇒ it never rejects). onSuccess runs onOk; onSettled clears
  // the in-flight flag and flushes any coverage refresh deferred during the
  // scan — both preserved verbatim from the prior hand-rolled version.
  const dispatchScan = (): Promise<ScanResponse | null> =>
    scanAction.dispatch(url, {
      onSuccess: (result) => {
        if (onOk) {
          onOk(result);
        }
      },
      onSettled: () => {
        store.set("scanInFlight", false);
        if (store.get("refreshPending")) {
          store.batch(() => {
            store.set("refreshPending", false);
            emit(BusEvent.DataInvalidate);
          });
        }
      },
    });

  const iconEl = btn ? btn.querySelector<HTMLElement>(".icon, .spinner") : null;
  if (btn && iconEl) {
    // withAsyncFeedback picks ✓/✗ from resolve/reject; since dispatch resolves
    // `null` (never throws) on failure, throw on null to surface the ✗ glyph.
    // `target` swaps only the icon <span>; `resetMs: 0` persists the glyph
    // until a coverage SSE re-renders (and replaces) the row.
    await withAsyncFeedback(
      btn,
      async () => {
        if ((await dispatchScan()) === null) {
          throw new Error("scan failed");
        }
      },
      {
        target: iconEl,
        resetMs: 0,
        renderPending: () => el("span", { className: "spinner" }),
        renderSuccess: () => el("span", { className: "icon icon-check" }),
        renderError: () => el("span", { className: "icon icon-close" }),
        // Keep the library's built-in screen-reader announcement: the ✓/✗
        // glyph is the ONLY other outcome signal (scan activities are
        // excluded from completion toasts), so suppressing it made the
        // result invisible to assistive tech.
      },
    );
  } else {
    // No button/icon slot to drive — run the scan for its side effects only.
    await dispatchScan();
  }
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
