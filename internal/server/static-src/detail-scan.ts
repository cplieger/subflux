// detail-scan.ts — Granular scan trigger helpers extracted from detail.ts.
//
// Scan starts are async on the server (202 + activity_id at accept time);
// completion rides the activity feed + scan SSE events. Each start is
// dispatched through the action framework for registry recording and
// rapid-click dedupe. retryNetwork is deliberately ABSENT: a lost-202 retry
// landing after completion would silently double-start a scan; failures
// surface as a toast and the user re-clicks, which the server's idempotent
// same-scope start makes safe.
//
// Button feedback keys off the shared runningScansByScope store (published
// by the status poll from the activity feed), not a local in-flight flag:
// every button annotated with data-scan-scope shows disabled+spinner while a
// running scan covers its scope — including scans started in another tab or
// before a reload — and re-enables on completion/cancellation.

import * as store from "./store.js";
import { apiAction } from "@cplieger/actions";
import { el } from "./dom.js";
import { fillPath } from "./api-client.js";
import { pollStatus } from "./status.js";
import { PATH_SCAN_MOVIE, PATH_SCAN_SEASON, PATH_SCAN_SERIES } from "./wire/client.gen.js";
import { decodeScanAccepted } from "./wire/decoders.gen.js";
import {
  movieScopeKey,
  seriesScopeKey,
  seasonScopeKey,
  type RunningScansByScope,
} from "./scan-scope.js";
import type { ScanAccepted } from "./wire/types.gen.js";
import type { SeriesItem, MovieDetail } from "./api-types.js";

interface ScanStartArgs {
  url: string;
  scopeKey: string;
}

const scanAction = apiAction<ScanStartArgs, ScanAccepted>({
  name: "scan.run",
  request: ({ url }) => ({ method: "POST", path: url }),
  decode: (data) => decodeScanAccepted(data),
  dedupe: ({ url }) => `scan:${url}`,
  error: "Failed to start scan",
});

/**
 * Trigger a scan start and reflect it in the shared scope store.
 * @param url - API endpoint to POST
 * @param scopeKey - canonical scan scope key (scan-scope.ts)
 */
async function triggerScan(url: string, scopeKey: string): Promise<void> {
  await scanAction.dispatch(
    { url, scopeKey },
    {
      onSuccess: (accepted) => {
        // Optimistic: mark the scope running immediately with the accepted
        // activity id so the button spins before the next poll lands; the
        // poll-derived map reconciles it moments later.
        const next = new Map(store.get("runningScansByScope"));
        next.set(scopeKey, { activityId: accepted.activity_id, cancellable: true });
        store.set("runningScansByScope", next);
        void pollStatus();
      },
    },
  );
}

export async function triggerSeriesScan(series: SeriesItem): Promise<void> {
  await triggerScan(fillPath(PATH_SCAN_SERIES, { id: series.id }), seriesScopeKey(series.id));
}

export async function triggerSeasonScan(series: SeriesItem, seasonNum: number): Promise<void> {
  await triggerScan(
    fillPath(PATH_SCAN_SEASON, { id: series.id, season: seasonNum }),
    seasonScopeKey(series.id, seasonNum),
  );
}

export async function triggerMovieScan(movie: MovieDetail): Promise<void> {
  await triggerScan(fillPath(PATH_SCAN_MOVIE, { id: movie.id }), movieScopeKey(movie.id));
}

// --- Store-driven button state ---

/** Patch one scan button against the running map: disabled + aria-busy +
 *  spinner while its scope has a running scan, search icon otherwise. */
function patchScanButton(btn: HTMLButtonElement, running: RunningScansByScope): void {
  const key = btn.dataset["scanScope"] ?? "";
  const active = key !== "" && running.has(key);
  const slot = btn.querySelector<HTMLElement>(".icon, .spinner");
  btn.disabled = active;
  if (active) {
    btn.setAttribute("aria-busy", "true");
    if (slot?.classList.contains("spinner") === false) {
      slot.replaceWith(el("span", { className: "spinner" }));
    }
  } else {
    btn.removeAttribute("aria-busy");
    if (slot?.classList.contains("spinner") === true) {
      slot.replaceWith(el("span", { className: "icon icon-search" }));
    }
  }
}

/** The shared running-scans map (initialized by app boot before any render). */
function runningMap(): RunningScansByScope {
  return store.get("runningScansByScope");
}

// The mounted-button registry (R8.5): render paths register each scan button
// at creation, and the store effect repaints THESE — never a document-wide
// query per publish. There is no unmount hook, so removal is a prune of
// disconnected buttons: once per registration burst (a microtask after the
// render pass, when the built tree is in the DOM and any discarded template
// buttons are not), and again on every publish as the belt.
const scanButtons = new Set<HTMLButtonElement>();
let prunePending = false;

function pruneDisconnected(): void {
  for (const btn of scanButtons) {
    if (!btn.isConnected) {
      scanButtons.delete(btn);
    }
  }
}

function schedulePrune(): void {
  if (prunePending) {
    return;
  }
  prunePending = true;
  queueMicrotask(() => {
    prunePending = false;
    pruneDisconnected();
  });
}

/** Register a freshly created scan button: it joins the store-driven registry
 *  and gets the current state painted, so rows painted mid-scan show the
 *  running state without waiting for the next store change. */
export function registerScanButton(btn: HTMLButtonElement): void {
  scanButtons.add(btn);
  schedulePrune();
  patchScanButton(btn, runningMap());
}

/** Registry population, for the registry-hygiene tests. */
export function _scanButtonCountForTest(): number {
  return scanButtons.size;
}

/** Sync every registered scan button with the shared store. */
function syncScanButtons(): void {
  pruneDisconnected();
  const running = runningMap();
  for (const btn of scanButtons) {
    patchScanButton(btn, running);
  }
}

/** Install the reactive effect that repaints scan buttons whenever the
 *  running-scans map changes. Called once from app boot. */
export function initScanButtons(): void {
  store.effect(() => {
    // store.effect tracks this get; the body re-runs on every publish.
    void store.get("runningScansByScope");
    syncScanButtons();
  });
}
