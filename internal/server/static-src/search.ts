// search.ts — search popup with provider results and async download

import * as store from "./store.js";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { prettyLabel, fmtEpisode, langName } from "./utils.js";
import { el, option, icon, dialog, closeDialog, emptyDiv, errDiv } from "./dom.js";
import { patch } from "@cplieger/reactive";
import { apiGetArray, apiGetRaw } from "./api-client.js";
import { decodeActivityEntry } from "./wire/decoders.gen.js";
import {
  apiAction,
  type ActionError,
  retryNetwork,
  registerCleanup,
  pollUntil,
} from "@cplieger/actions";
import { hasCode, ErrorCode } from "./error_codes.js";
import { SEARCH_TIMEOUT_MS, DOWNLOAD_POLL_MS, DOWNLOAD_DEADLINE_MS } from "./constants.js";
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
  path?: string;
}

interface CoverageMedia {
  id: number;
  tvdb_id?: number;
  tmdb_id?: number;
  imdb_id?: string;
  title: string;
  year?: number;
  scene_name?: string;
  path?: string;
}

interface SearchResult {
  provider: string;
  language: string;
  release_name: string;
  score: number;
  matches: Record<string, number>;
  matched_by: string;
  hearing_impaired: boolean;
  forced: boolean;
  subtitle_id: string;
  on_disk: boolean;
}

interface SearchResponse {
  results?: SearchResult[];
}

interface DownloadResponse {
  activity_id: string;
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
  file_path: string;
  language: string;
  season: number;
  episode: number;
  media_type: MediaType;
  media_id: number;
  top_pick: boolean;
  score: number;
  hearing_impaired: boolean;
  forced: boolean;
}

/** Polled state for the post-download activity poll. `act` is the matching
 *  activity entry (undefined once it is evicted from the activity list);
 *  `seen` latches true once the entry has appeared, so an eviction after the
 *  entry was seen counts as completion (matches the old seenActivity flag). */
interface DownloadPollState {
  act: ActivityEntry | undefined;
  seen: boolean;
}

/** Download a subtitle. Server returns 202 + activity_id; the caller
 *  polls /api/activity for completion. retryNetwork allows the framework
 *  to recover from transient blips, but app-level "download_failed"
 *  errors (provider 4xx, IO errors) are NOT retried — the user re-clicks
 *  via the manually-re-enabled button. */
const downloadAction = apiAction<DownloadArgs, DownloadResponse>({
  name: "search.download",
  request: (args) => ({ method: "POST", path: "/api/search/download", body: args }),
  retryable: (err) => err.code !== ErrorCode.DownloadFailed && retryNetwork(err),
  error: false, // callsite drives icon + tooltip + per-error re-enable logic
});

let searchAbort: AbortController | null = null;
const activePolls = new Set<AbortController>();

// Drain in-flight search + download polls on page unload. Each poll's
// AbortController is also removed from the Set when it terminates
// naturally; this hook is a safety net for unload + tests.
registerCleanup(() => {
  searchAbort?.abort();
  searchAbort = null;
  for (const p of activePolls) {
    p.abort();
  }
  activePolls.clear();
});

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
  // closeDialog (@cplieger/ui-primitives) fades out via an `is-leaving` class,
  // not inline styles; clear it so a reopen within the fade window renders
  // visible and the pending close callback no-ops (it is guarded on the class).
  searchDlg.classList.remove("is-leaving");
  searchDlg.showModal();
  searchDlg.focus();

  // Auto-run search.
  void runPopupSearch(mediaType, media, season, episode, defaultLang);
}

export function closeSearchPopup(): void {
  for (const p of activePolls) {
    p.abort();
  }
  activePolls.clear();
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

  const params: URLSearchParams = new URLSearchParams();
  params.set("type", mediaType);
  params.set("lang", lang);

  if (mediaType === "episode") {
    params.set("tvdb", String(media.tvdb_id));
    if (media.imdb_id) {
      params.set("imdb", media.imdb_id);
    }
    if (season != null) {
      params.set("season", String(season));
    }
    if (episode) {
      params.set("episode", String(episode.episode));
      if (episode.scene_season) {
        params.set("scene_season", String(episode.scene_season));
      }
      if (episode.scene_episode) {
        params.set("scene_episode", String(episode.scene_episode));
      }
      if (episode.absolute_episode) {
        params.set("absolute_episode", String(episode.absolute_episode));
      }
      if (episode.title) {
        params.set("episode_title", episode.title);
      }
      if (episode.scene_name) {
        params.set("release", episode.scene_name);
      }
      if (episode.path) {
        params.set("file", episode.path);
      }
    }
    params.set("title", media.title);
    if (media.year) {
      params.set("year", String(media.year));
    }
  } else {
    params.set("tmdb", String(media.tmdb_id));
    if (media.imdb_id) {
      params.set("imdb", media.imdb_id);
    }
    params.set("title", media.title);
    if (media.year) {
      params.set("year", String(media.year));
    }
    if (media.scene_name) {
      params.set("release", media.scene_name);
    }
    if (media.path) {
      params.set("file", media.path);
    }
  }

  const r = await apiGetRaw<SearchResponse | SearchResult[]>(
    `/api/search?${params.toString()}`,
    signal,
  );
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
  const resp = payload as SearchResponse;
  const results: SearchResult[] = resp.results ?? (payload as SearchResult[]);
  renderPopupResults(out, results, lang, mediaType, media, season, episode);
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
  frag.appendChild(el("div", { className: "muted result-count" }, `${results.length} result(s)`));

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

    // Build score tooltip from match breakdown.
    const matches: Record<string, number> = s.matches;
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
  let filePath = "";
  if (mediaType === "episode" && episode?.path) {
    filePath = episode.path;
  } else if (mediaType === "movie" && media.path) {
    filePath = media.path;
  }
  if (!filePath) {
    notify.error(
      "File path not available for download. " +
        "Use the detailed search panel for file-based downloads.",
    );
    return;
  }

  (btn as HTMLButtonElement).disabled = true;
  patch(btn, icon("hourglass"));

  let downloadErr: ActionError | undefined;
  const data = await downloadAction.dispatch(
    {
      provider: sub.provider,
      subtitle_id: sub.subtitle_id,
      release_name: sub.release_name || "",
      file_path: filePath,
      language: lang,
      season: season ?? 0,
      episode: episode ? episode.episode : 0,
      media_type: mediaType,
      media_id: media.id,
      top_pick: isTop,
      score: sub.score,
      hearing_impaired: sub.hearing_impaired,
      forced: sub.forced,
    },
    {
      // Capture the error so we can decide whether to re-enable the button.
      onError: (err) => {
        // ActionError shape; we only need the code here for retry-eligible
        // re-enable. The framework already toasted the message.
        downloadErr = err as ActionError;
      },
    },
  );
  if (data === null) {
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
  // 202 Accepted: download running in background.
  const actID: string = data.activity_id;
  patch(btn, el("span", { className: "spinner" }));
  btn.setAttribute("data-tip", "Downloading\u2026");

  // Poll activity until done, with AbortSignal timeout for clean cancellation.
  const abort = new AbortController();
  const timeout = AbortSignal.timeout(DOWNLOAD_DEADLINE_MS);
  const signal = AbortSignal.any([abort.signal, timeout]);
  let seen = false;
  activePolls.add(abort);

  // pollUntil drives the wait-then-poll loop. The step latches `seen` when the
  // activity entry appears; `until` is terminal when the entry is done OR was
  // seen and then evicted. timeoutMs mirrors the old AbortSignal.timeout
  // deadline, and the combined abort+timeout signal still cancels the in-flight
  // /api/activity fetch on deadline or teardown (cleanup aborts via activePolls).
  const outcome = await pollUntil<DownloadPollState>(
    async (sig: AbortSignal): Promise<DownloadPollState | null> => {
      const acts = await apiGetArray("/api/activity", decodeActivityEntry, sig);
      if (!acts) {
        return null;
      }
      const act: ActivityEntry | undefined = acts.find((a: ActivityEntry) => a.id === actID);
      if (act) {
        seen = true;
      }
      return { act, seen };
    },
    {
      intervalMs: DOWNLOAD_POLL_MS,
      timeoutMs: DOWNLOAD_DEADLINE_MS,
      until: (s) => (s.act?.done ?? false) || (s.act === undefined && s.seen),
      signal,
    },
  );

  if (outcome.status === "done") {
    // Either act.done is true, or the entry was evicted after being seen.
    activePolls.delete(abort);
    abort.abort();
    if (outcome.result.act?.failed) {
      btn.dataset["status"] = "err";
      patch(btn, icon("close"));
    } else {
      btn.dataset["status"] = "ok";
      patch(btn, icon("check"));
      emit(BusEvent.DataInvalidate);
    }
    btn.removeAttribute("data-tip");
  } else {
    // timeout (deadline reached) or aborted (cleanup / page unload): matches
    // the old signal-aborted branch — flag the button and stop tracking.
    activePolls.delete(abort);
    btn.dataset["status"] = "err";
    patch(btn, icon("close"));
    btn.setAttribute("data-tip", "Download timed out");
  }
}
