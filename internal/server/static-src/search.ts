// search.ts — search popup with provider results and async download

import * as store from "./store.js";
import * as notify from "./notify.js";
import { prettyLabel, fmtEpisode, langName } from "./utils.js";
import { el, option, icon, dialog, closeDialog, emptyDiv, errDiv } from "./dom.js";
import { openDialog } from "@cplieger/ui-primitives/dialog";
import { patch } from "@cplieger/reactive";
import { join } from "@cplieger/keyenc";
import { manualSearchRaw, PATH_DOWNLOAD_SUBTITLE } from "./wire/client.gen.js";
import type { QueryValue } from "./wire/client.gen.js";
import { decodeDownloadAccepted } from "./wire/decoders.gen.js";
import type { DownloadAccepted, SearchResult } from "./wire/types.gen.js";
import { apiAction, retryNetwork, registerCleanup } from "@cplieger/actions";
import { observeActivities } from "./status.js";
import { hasCode, ErrorCode } from "./error_codes.js";
import { SEARCH_TIMEOUT_MS } from "./constants.js";
import type { ActivityEntry, MediaType } from "./api-types.js";

/** Data-driven error-code-to-message mapping for search popup errors. */
const SEARCH_ERROR_MAP: readonly { code: ErrorCode; msg: string; empty?: boolean }[] = [
  {
    code: ErrorCode.SearchProviderDisabled,
    msg: "All search providers are disabled. Enable at least one in settings.",
  },
  { code: ErrorCode.SearchNoResults, msg: "No results found from any provider.", empty: true },
  {
    code: ErrorCode.ProviderTimedOut,
    msg: "This provider is in cooldown; try again in a few minutes.",
  },
];

interface CoverageEpisode {
  episode: number;
  scene_season?: number;
  scene_episode?: number;
  absolute_episode?: number;
  title?: string;
  scene_name?: string;
}

interface CoverageMedia {
  /** Arr internal ID (Radarr movie / Sonarr series) — the MediaRef the
   *  server resolves video paths from. */
  id: number;
  tvdb_id?: number;
  tmdb_id?: number;
  imdb_id?: string;
  title: string;
  year?: number;
  scene_name?: string;
}

interface DownloadOpts {
  sub: SearchResult;
  lang: string;
  mediaType: MediaType;
  media: CoverageMedia;
  season: number | null;
  episode: CoverageEpisode | null;
  isTop: boolean;
}

interface DownloadArgs {
  provider: string;
  subtitle_id: string;
  release_name: string;
  language: string;
  season: number;
  episode: number;
  media_type: MediaType;
  /** Arr internal ID: with media_type + season/episode this is the MediaRef
   *  the server resolves the video file path from (no path on the wire). */
  media_id: number;
  top_pick: boolean;
  score: number;
  hearing_impaired: boolean;
  forced: boolean;
}

/** Download a subtitle. Server returns 202 + activity_id; completion arrives
 *  through the activity store (SSE activity events, or the degraded 5s poll
 *  while the stream is down). retryNetwork allows the framework
 *  to recover from transient blips, but app-level "download_failed"
 *  errors (provider 4xx, IO errors) are NOT retried — the user re-clicks
 *  via the manually-re-enabled button. */
const downloadAction = apiAction<DownloadArgs, DownloadAccepted>({
  name: "search.download",
  request: (args) => ({ method: "POST", path: PATH_DOWNLOAD_SUBTITLE, body: args }),
  decode: (data) => decodeDownloadAccepted(data),
  retryable: (err) => err.code !== ErrorCode.DownloadFailed && retryNetwork(err),
  error: false, // callsite drives icon + tooltip + per-error re-enable logic
});

let searchAbort: AbortController | null = null;

// --- Download tracking (task 12): the activity store flips the buttons ---

/** A download in flight, keyed by its subtitle identity so a reopened popup
 *  re-derives the button state (the dialog content is rebuilt per open).
 *  `seen` latches once the activity entry has been observed; an entry that
 *  later vanishes counts as completed — the server evicts only terminal
 *  rows, so eviction after `seen` means the done frame was missed. */
interface TrackedDownload {
  activityId: string;
  seen: boolean;
  /** How many server restarts had been observed when this download was
   *  dispatched. An activity id only means anything to the process that issued
   *  it, so this is what tells a dead process's entry from a live one's. */
  boot: number;
}

// Subtitle key → in-flight download. Entries leave on a terminal observation.
const trackedDownloads = new Map<string, TrackedDownload>();

// How many server restarts events.ts has reported. A restart drops the activity
// log with the process, so a tracked download that belonged to an EARLIER boot
// will never see its completion event: the first snapshot after the restart —
// the recovery transaction's own authoritative status read — resolves it instead
// of waiting forever. A download dispatched after the restart was observed
// carries the new number and is left alone, which a one-shot sweep flag could
// not express: it retired every unobserved entry, healthy ones included, because
// the status snapshot it consumed may have been read before that download
// existed.
let bootGeneration = 0;

/** A server restart happened (events.ts, on a boot_id change): every download
 *  dispatched up to now belongs to a process that is gone. */
export function noteServerRestart(): void {
  bootGeneration += 1;
}

function downloadKey(sub: SearchResult, lang: string): string {
  return join("dl", sub.provider, sub.subtitle_id, lang);
}

/** The running-download render, shared by the fresh 202 and the reopen
 *  re-derive: disabled, spinner, and the activity id the observer keys on. */
function markDownloading(btn: HTMLButtonElement, activityId: string): void {
  btn.disabled = true;
  btn.dataset["activityId"] = activityId;
  patch(btn, el("span", { className: "spinner" }));
  btn.setAttribute("data-tip", "Downloading\u2026");
}

/** Terminal render for a download button; a no-op when the popup is closed
 *  or re-rendered without the row — the tracking entry is already gone, so
 *  a later reopen renders a fresh button. */
function settleDownloadButton(activityId: string, failed: boolean): void {
  const btn = searchDlg.querySelector<HTMLButtonElement>(
    `button[data-activity-id="${CSS.escape(activityId)}"]`,
  );
  if (!btn) {
    return;
  }
  if (failed) {
    btn.dataset["status"] = "err";
    patch(btn, icon("close"));
  } else {
    btn.dataset["status"] = "ok";
    patch(btn, icon("check"));
  }
  btn.removeAttribute("data-tip");
}

/** Idle render for a download button: re-clickable, carrying no verdict. The
 *  state a freshly built row starts in, reached again when a tracked download
 *  can be resolved neither way — the outcome is unknowable, so claiming one
 *  would be a lie in either direction. */
function resetDownloadButton(activityId: string): void {
  const btn = searchDlg.querySelector<HTMLButtonElement>(
    `button[data-activity-id="${CSS.escape(activityId)}"]`,
  );
  if (!btn) {
    return;
  }
  delete btn.dataset["activityId"];
  btn.disabled = false;
  patch(btn, icon("download"));
  btn.setAttribute("data-tip", "Server restarted \u2014 outcome unknown, retry");
}

/** Flip tracked download buttons from the activity store: an entry observed
 *  done settles its button; an entry evicted after being seen counts as
 *  completed; an entry not yet observed keeps waiting (the 202 precedes the
 *  event by construction — the server starts the activity before answering),
 *  unless its own boot is gone, which retires that wait. */
function reconcileDownloadButtons(activities: readonly ActivityEntry[]): void {
  if (trackedDownloads.size === 0) {
    return;
  }
  const byId = new Map(activities.map((a) => [a.id, a]));
  for (const [key, t] of trackedDownloads) {
    const act = byId.get(t.activityId);
    if (act && !act.done) {
      t.seen = true;
      continue;
    }
    if (!act && !t.seen) {
      if (t.boot === bootGeneration) {
        continue; // the process that issued this id is still running
      }
      // This snapshot is the restarted server's authoritative state and it has
      // never heard of the entry, so nothing will ever complete it.
      trackedDownloads.delete(key);
      resetDownloadButton(t.activityId);
      continue;
    }
    trackedDownloads.delete(key);
    settleDownloadButton(t.activityId, act?.failed ?? false);
  }
}

observeActivities(reconcileDownloadButtons);

/** Drain the in-flight search and the download tracking. */
function drainSearchState(): void {
  searchAbort?.abort();
  searchAbort = null;
  trackedDownloads.clear();
  bootGeneration = 0;
}

/** Test-only. */
export function _resetSearchForTest(): void {
  drainSearchState();
}

// Page-unload safety net (also covers soft navigations in tests).
registerCleanup(drainSearchState);

const searchDlg: HTMLDialogElement = dialog("searchResultPopup");

// Whether openSearchPopup pushed a history entry that closeSearchPopup
// needs to pop (vs. direct URL navigation where no entry was pushed).
let searchPushedHistory = false;

// --- Search popup ---

export function openSearchPopup(
  mediaType: MediaType,
  media: CoverageMedia,
  season: number | null,
  episode: CoverageEpisode | null,
  lang?: string | null,
): void {
  const cfg = store.get("config");
  const langs: string[] = cfg?.languages ?? ["en"];
  const defaultLang: string = lang ?? langs[0] ?? "en";

  // Push search URL.
  const idPart: string =
    mediaType === "episode" ? `/series/${media.tvdb_id ?? ""}` : `/movie/${media.tmdb_id ?? ""}`;
  const searchPath = `${idPart}/search/${defaultLang}`;
  if (location.pathname !== searchPath) {
    history.pushState(null, "", searchPath);
    searchPushedHistory = true;
  } else {
    searchPushedHistory = false;
  }

  // Build header.
  let title: string = media.title;
  if (mediaType === "episode" && episode) {
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- episode implies season
    title += ` ${fmtEpisode(season!, episode.episode)}`;
  }

  const langSel = el("select", {
    id: "popup-lang-sel",
    "aria-label": "Language",
  }) as HTMLSelectElement;
  for (const l of langs) {
    langSel.appendChild(option(l, langName(l)));
  }
  langSel.value = defaultLang;
  langSel.addEventListener("change", () => {
    history.replaceState(null, "", `${idPart}/search/${langSel.value}`);
    void runPopupSearch(mediaType, media, season, episode, langSel.value);
  });

  const header: HTMLElement = el(
    "div",
    { className: "dlg-head" },
    el("div", { className: "dlg-title" }, title),
    el(
      "div",
      { className: "dlg-controls" },
      langSel,
      el(
        "button",
        {
          type: "button",
          className: "close-btn ghost",
          "aria-label": "Close search",
          onclick: closeSearchPopup,
        },
        icon("close"),
      ),
    ),
  );

  const resultsDiv: HTMLElement = el("div", {
    id: "popup-search-results",
    className: "dlg-body",
  });

  patch(searchDlg, header, resultsDiv);
  if (searchDlg.open) {
    searchDlg.close();
  }
  // openDialog cancels a pending is-leaving fade before showing, so a reopen
  // within the fade window renders visible and the stale close no-ops.
  openDialog(searchDlg);
  searchDlg.focus();

  // Auto-run search.
  void runPopupSearch(mediaType, media, season, episode, defaultLang);
}

export function closeSearchPopup(): void {
  closeDialog(searchDlg);
  if (searchPushedHistory && location.pathname.includes("/search/")) {
    searchPushedHistory = false;
    history.back();
  } else if (location.pathname.includes("/search/")) {
    const parent: string = location.pathname.replace(/\/search\/[a-z]{2,3}$/, "");
    history.replaceState(null, "", parent || "/");
  }
}

async function runPopupSearch(
  mediaType: MediaType,
  media: CoverageMedia,
  season: number | null,
  episode: CoverageEpisode | null,
  lang: string,
): Promise<void> {
  // Cancel any in-flight search.
  if (searchAbort) {
    searchAbort.abort();
  }
  searchAbort = new AbortController();
  const signal: AbortSignal = AbortSignal.any([
    searchAbort.signal,
    AbortSignal.timeout(SEARCH_TIMEOUT_MS),
  ]);

  const out: HTMLElement | null = document.getElementById("popup-search-results");
  if (!out) {
    return;
  }
  patch(
    out,
    el(
      "div",
      { className: "empty" },
      el("span", { className: "spinner" }),
      "Searching providers\u2026",
    ),
  );

  // Same params (and the same conditions) as the previous hand-built
  // URLSearchParams; the generated client serializes the object in insertion
  // order and skips undefined values.
  const query: Record<string, QueryValue> = { type: mediaType, lang };
  if (mediaType === "episode") {
    query["tvdb"] = String(media.tvdb_id);
    if (media.imdb_id) {
      query["imdb"] = media.imdb_id;
    }
    if (season != null) {
      query["season"] = season;
    }
    if (episode) {
      query["episode"] = episode.episode;
      if (episode.scene_season) {
        query["scene_season"] = episode.scene_season;
      }
      if (episode.scene_episode) {
        query["scene_episode"] = episode.scene_episode;
      }
      if (episode.absolute_episode) {
        query["absolute_episode"] = episode.absolute_episode;
      }
      if (episode.title) {
        query["episode_title"] = episode.title;
      }
      if (episode.scene_name) {
        query["release"] = episode.scene_name;
      }
    }
    query["title"] = media.title;
    if (media.year) {
      query["year"] = media.year;
    }
  } else {
    query["tmdb"] = String(media.tmdb_id);
    if (media.imdb_id) {
      query["imdb"] = media.imdb_id;
    }
    query["title"] = media.title;
    if (media.year) {
      query["year"] = media.year;
    }
    if (media.scene_name) {
      query["release"] = media.scene_name;
    }
  }
  // MediaRef for server-side video resolution (hash computation): the arr
  // internal ID; season/episode ride the episode params above.
  if (media.id > 0) {
    query["media_id"] = media.id;
  }

  const r = await manualSearchRaw(query, { signal });
  if (!r.ok) {
    if (r.status === 0 && signal.aborted) {
      return;
    }
    const matched = SEARCH_ERROR_MAP.find((e) => hasCode(r, e.code));
    if (matched) {
      patch(out, matched.empty ? emptyDiv(matched.msg) : errDiv(matched.msg));
    } else {
      patch(out, errDiv(r.error ?? "Search failed"));
    }
    return;
  }
  const payload = r.data;
  if (!payload) {
    patch(out, errDiv("Empty response"));
    return;
  }
  renderPopupResults(out, payload.results, lang, mediaType, media, season, episode);
}

function renderPopupResults(
  out: HTMLElement,
  results: SearchResult[],
  lang: string,
  mediaType: MediaType,
  media: CoverageMedia,
  season: number | null,
  episode: CoverageEpisode | null,
): void {
  if (results.length === 0) {
    patch(out, emptyDiv("No results found."));
    return;
  }

  const frag: DocumentFragment = document.createDocumentFragment();
  // The list renders at most 30 rows; say so instead of a bare total that
  // contradicts the visible list ("80 results" over 30 rows read as a bug).
  const shown = Math.min(results.length, 30);
  const countText =
    results.length > shown
      ? `Showing ${shown} of ${results.length} results (best matches first)`
      : `${results.length} ${results.length === 1 ? "result" : "results"}`;
  frag.appendChild(el("div", { className: "muted result-count" }, countText));

  // Header row.
  frag.appendChild(
    el(
      "div",
      { className: "result-row muted" },
      el("span", { className: "result-score" }, "Score"),
      el("span", { className: "result-provider" }, "Provider"),
      el("span", { className: "result-match" }, "Match"),
      el("span", { className: "result-release" }, "Release"),
      el("span", { className: "result-dl" }, "Download"),
    ),
  );

  const visible: SearchResult[] = results.slice(0, 30);
  for (let i = 0; i < visible.length; i++) {
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- bounds checked
    const s: SearchResult = visible[i]!;
    const isTop: boolean = i === 0;

    const dlBtn: HTMLButtonElement = el(
      "button",
      {
        type: "button",
        "aria-label": isTop ? "Download (auto)" : "Download (manual)",
        "data-tip": isTop
          ? "Top match: downloads as auto subtitle"
          : "Not top match: downloads as manual (pauses automation)",
        onclick: () => {
          void downloadFromPopup(dlBtn, {
            sub: s,
            lang,
            mediaType,
            media,
            season,
            episode,
            isTop,
          });
        },
      },
      icon("download"),
    ) as HTMLButtonElement;
    // Reopen re-derive: a download dispatched from an earlier open of this
    // popup is still in flight — render its running state; the observer
    // flips it when the activity terminates.
    const tracked = trackedDownloads.get(downloadKey(s, lang));
    if (tracked) {
      markDownloading(dlBtn, tracked.activityId);
    }

    // Build score tooltip from match breakdown.
    const matches: Record<string, number> = s.matches ?? {};
    const keys: string[] = Object.keys(matches);
    const scoreTip: string =
      keys.length > 0
        ? // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- key from Object.keys
          keys.map((k: string) => `${prettyLabel(k)}: ${matches[k]!}`).join("\n")
        : "No attribute matches";

    const row: HTMLElement = el(
      "div",
      {
        className: `result-row${isTop ? " top" : ""}`,
      },
      el(
        "span",
        {
          className: "result-score",
          "data-tip": scoreTip,
        },
        String(s.score),
      ),
      el(
        "span",
        { className: "result-provider" },
        s.provider,
        s.on_disk ? el("span", null, " ", icon("check")) : null,
      ),
      el("span", { className: "result-match" }, s.matched_by || ""),
      el(
        "span",
        { className: "result-release" },
        `${s.release_name || ""} `,
        s.hearing_impaired ? el("span", { className: "result-hi" }, "[HI]") : null,
      ),
      el("span", { className: "result-dl" }, dlBtn),
    );
    frag.appendChild(row);
  }
  patch(out, frag);
}

async function downloadFromPopup(btn: HTMLElement, opts: DownloadOpts): Promise<void> {
  const { sub, lang, mediaType, media, season, episode, isTop } = opts;
  if (!media.id) {
    notify.error("Media reference not available for download.");
    return;
  }

  (btn as HTMLButtonElement).disabled = true;
  patch(btn, icon("hourglass"));

  const o = await downloadAction.dispatch({
    provider: sub.provider,
    subtitle_id: sub.subtitle_id,
    release_name: sub.release_name || "",
    language: lang,
    season: season ?? 0,
    episode: episode ? episode.episode : 0,
    media_type: mediaType,
    media_id: media.id,
    top_pick: isTop,
    score: sub.score,
    hearing_impaired: sub.hearing_impaired,
    forced: sub.forced,
  }).outcome;
  if (o.status !== "success") {
    // The framework already toasted the message; the typed outcome carries
    // the error (cancelled dispatches carry none) for the retry decision.
    const downloadErr = o.status === "error" ? o.error : undefined;
    btn.dataset["status"] = "err";
    patch(btn, icon("close"));
    btn.setAttribute("data-tip", downloadErr?.message ?? "Download failed");
    // Re-enable button for retry on download_failed (transient-by-design;
    // user can manually retry rather than re-search).
    if (downloadErr?.code === ErrorCode.DownloadFailed) {
      (btn as HTMLButtonElement).disabled = false;
    }
    return;
  }
  const data = o.value;
  // 202 Accepted: the download runs in a background goroutine. The button
  // carries the activity id; the activity-store observer flips it on the
  // terminal event (or the degraded poll while SSE is down). A close/reopen
  // re-derives the running state from the tracking map.
  trackedDownloads.set(downloadKey(sub, lang), {
    activityId: data.activity_id,
    seen: false,
    boot: bootGeneration,
  });
  markDownloading(btn as HTMLButtonElement, data.activity_id);
}
