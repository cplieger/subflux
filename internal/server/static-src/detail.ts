// detail.ts — series and movie detail drilldown views

import * as store from "./store.js";
import { $, el, icon, emptyDiv, errDiv, pad, insertNavButton } from "./dom.js";
import { apiGet, apiGetArray } from "./api-client.js";
import { decodeSubtitleEntry } from "./wire/decoders.gen.js";
import { registerCleanup } from "@cplieger/actions";
import { fmtEpisode, tvdbMediaId, langName, fmtLangVariant } from "./utils.js";
import { DEFAULT_VARIANT, EMBEDDED_PROVIDER } from "./constants.js";
import { on, emit, BusEvent } from "./bus.js";
import { openSearchPopup } from "./search.js";
import { openSyncDialog } from "./sync.js";
import { openFileManager } from "./files.js";
import { triggerSeriesScan, triggerSeasonScan, triggerMovieScan } from "./detail-scan.js";
import { confirmSeasonSync } from "./detail-season-sync.js";
import type { SeasonSyncEpisode } from "./detail-season-sync.js";
import type { SubtitleEntry, MovieDetail, Episode, SeasonGroup, SeriesItem } from "./api-types.js";
import { reconcile, patch } from "@cplieger/reactive";

// Module-level abort controller for detail navigation fetches. Self-cleans
// on internal navigation (each new openSeriesDetail/openMovieDetail aborts
// the previous), but page unload also needs a cleanup hook so in-flight
// fetches don't outlive the document.
let detailAbort: AbortController | null = null;
registerCleanup(() => {
  detailAbort?.abort();
  detailAbort = null;
});

// --- API response shapes ---

// --- Series detail drilldown ---

// Build a coverage badge for a set of subtitle entries.
// When langLabel is provided, creates a split badge: left side = language
// (darker), right side = codec/source details.
// Without langLabel (movie detail), just the detail part.
function coverageBadge(entries: SubtitleEntry[] | null, langLabel: string | null): HTMLElement {
  if (!entries || entries.length === 0) {
    if (langLabel) {
      return el(
        "span",
        { className: "badge badge-split", "data-status": "err" },
        el("span", { className: "badge-lang" }, langLabel),
        el("span", { className: "badge-detail" }, "\u2014"),
      );
    }
    return el("span", { className: "badge", "data-status": "err" }, "\u2014");
  }

  const byCodec: Record<string, string[]> = {};
  for (const sub of entries) {
    const codec = sub.codec ?? "srt";
    byCodec[codec] ??= [];
    byCodec[codec].push(sub.source === EMBEDDED_PROVIDER ? "emb" : "ext");
  }

  const allIgnored = entries.every(
    (sub) => sub.source === EMBEDDED_PROVIDER && store.get("ignoredCodecs").has(sub.codec ?? ""),
  );
  const badgeStatus = allIgnored ? "warn" : "ok";

  const parts = Object.entries(byCodec).map(
    ([codec, sources]) => `${codec}: ${sources.join(" \u00B7 ")}`,
  );
  let detail = parts.join(" + ");

  const topScore = Math.max(...entries.map((s) => s.score ?? 0));
  if (topScore > 0) {
    detail += ` \u2605${topScore}`;
  }

  const tip = allIgnored ? "Ignored codec" : undefined;

  if (langLabel) {
    return el(
      "span",
      {
        className: "badge badge-split",
        "data-status": badgeStatus,
        "data-tip": tip,
      },
      el("span", { className: "badge-lang" }, langLabel),
      el("span", { className: "badge-detail" }, detail),
    );
  }
  return el(
    "span",
    {
      className: "badge",
      "data-status": badgeStatus,
      "data-tip": tip,
    },
    detail,
  );
}

function buildArrLink(series: SeriesItem): string | null {
  const cfg = store.get("config");
  if (!cfg) {
    return null;
  }
  const url = cfg.sonarr_url;
  if (!url) {
    return null;
  }
  const base = url.replace(/\/+$/, "");
  return `${base}/series/${encodeURIComponent(
    series.title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, ""),
  )}`;
}

function buildRadarrLink(movie: MovieDetail): string | null {
  const cfg = store.get("config");
  if (!cfg) {
    return null;
  }
  const url = cfg.radarr_url;
  if (!url) {
    return null;
  }
  const base = url.replace(/\/+$/, "");
  return `${base}/movie/${movie.tmdb_id}`;
}

function openSeriesDetail(s: SeriesItem, skipPush?: boolean): void {
  // Abort any prior in-flight detail fetch.
  if (detailAbort) {
    detailAbort.abort();
  }
  detailAbort = new AbortController();
  const { signal } = detailAbort;

  if (!skipPush) {
    history.pushState(null, "", `/series/${s.tvdb_id}`);
  }
  const targets = s.targets;
  const subsInfo =
    targets.length > 0 ? targets.map((t) => fmtLangVariant(t.language, t.variant)).join(", ") : "";
  const info = `${s.episodes} ep \u00B7 audio: ${langName(
    s.rule || "default",
  )}${subsInfo ? ` \u00B7 subs: ${subsInfo}` : ""}`;
  emit(BusEvent.PanelConfigure, {
    visible: false,
    detail: {
      title: s.title,
      info,
      backPath: "/",
      arrLink: buildArrLink(s),
      arrName: "Sonarr",
    },
  });
  const out = $.coverageContent;
  const skel = document.createDocumentFragment();
  for (let i = 0; i < 6; i++) {
    skel.appendChild(
      el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
    );
  }
  patch(out, skel);

  Promise.all([
    apiGet<SeasonGroup[]>(`/api/media/series/${s.id}/episodes`, signal),
    apiGetArray(`/api/coverage/series/${s.tvdb_id}`, decodeSubtitleEntry, signal),
    apiGet<string[]>(`/api/state/ids?type=episode&prefix=tvdb-${s.tvdb_id}-`, signal),
  ])
    .then(([seasons, subFiles, historyIDs]) => {
      if (signal.aborted) {
        return;
      }
      renderSeriesDetail(s, seasons ?? [], subFiles ?? [], new Set(historyIDs ?? []));
    })
    .catch((e: unknown) => {
      if (signal.aborted) {
        return;
      }
      const msg = e instanceof Error ? e.message : String(e);
      patch(out, errDiv(msg));
    });
}

/** Collect episodes with external subs + video for season audio sync. */
function collectSeasonSyncEps(
  sg: SeasonGroup,
  series: SeriesItem,
  subIdx: Record<string, Partial<Record<string, SubtitleEntry[]>>>,
  targetLangs: { lang: string; variant: string }[],
): SeasonSyncEpisode[] {
  const result: SeasonSyncEpisode[] = [];
  for (const ep of sg.episodes ?? []) {
    if (!ep.has_file || !ep.path) {
      continue;
    }
    const mediaId = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
    const subs = subIdx[mediaId] ?? {};
    for (const t of targetLangs) {
      const key = `${t.lang}|${t.variant}`;
      const entries = subs[key];
      if (entries) {
        for (const sub of entries) {
          if (sub.source !== EMBEDDED_PROVIDER && sub.path) {
            result.push({
              subPath: sub.path,
              videoPath: ep.path,
              label: fmtEpisode(sg.season, ep.episode),
            });
          }
        }
      }
    }
  }
  return result;
}

/** Build a single episode table row with coverage badges and action buttons. */
function buildEpisodeRow(
  series: SeriesItem,
  sg: SeasonGroup,
  ep: Episode,
  subIdx: Record<string, Partial<Record<string, SubtitleEntry[]>>>,
  targetLangs: { lang: string; variant: string }[],
  hasAbsOrder: boolean,
  historySet: Set<string>,
): HTMLElement {
  const mediaId = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
  const subs = subIdx[mediaId] ?? {};

  const langCells =
    targetLangs.length > 0
      ? targetLangs.map((t) => {
          const key = `${t.lang}|${t.variant}`;
          const entries = subs[key];
          const langAttr = fmtLangVariant(t.lang, t.variant);
          const badge = coverageBadge(entries ?? null, langAttr);
          return el("td", { "data-lang": langAttr }, badge);
        })
      : [
          el(
            "td",
            null,
            Object.keys(subs).length > 0
              ? el(
                  "span",
                  { className: "badge", "data-status": "ok" },
                  `${Object.keys(subs).length} subs`,
                )
              : el("span", { className: "badge", "data-status": "err" }, "\u2014"),
          ),
        ];

  const searchBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      "data-tip": "Manual: browse and pick subtitles for this episode",
      onclick: () => {
        openSearchPopup("episode", series, sg.season, ep);
      },
    },
    icon("search"),
    el("span", { className: "btn-text" }, " Search"),
  );

  const absEp = ep.absolute_episode;
  const airedEp = ep.episode;
  const epLabel = hasAbsOrder && absEp ? `#${absEp}` : `E${pad(airedEp)}`;

  const epNumCell =
    hasAbsOrder && absEp
      ? el(
          "td",
          {
            className: "ep-num",
            "data-tip": `Absolute #${absEp}, aired ${fmtEpisode(sg.season, airedEp)}`,
          },
          epLabel,
          el("span", { className: "ep-aired" }, ` E${pad(airedEp)}`),
        )
      : el("td", { className: "ep-num" }, epLabel);

  const covCell = el(
    "td",
    {
      className: "ep-coverage",
    },
    ...langCells.map((td) => td.firstChild),
  );

  const extEpSubs: SubtitleEntry[] = [];
  for (const t of targetLangs) {
    const key = `${t.lang}|${t.variant}`;
    const entries = subs[key];
    if (entries) {
      for (const sub of entries) {
        if (sub.source !== EMBEDDED_PROVIDER && sub.path) {
          extEpSubs.push(sub);
        }
      }
    }
  }

  const epMediaID = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
  const histBtn = historySet.has(epMediaID)
    ? el(
        "button",
        {
          type: "button",
          className: "ghost",
          "data-tip": "View download history for this episode",
          onclick: () => {
            const label = `${series.title} ${fmtEpisode(sg.season, ep.episode)}`;
            emit(BusEvent.NavHistory, label);
          },
        },
        icon("history"),
        el("span", { className: "btn-text" }, " History"),
      )
    : null;

  const firstExtSub = extEpSubs[0];
  const syncBtn =
    extEpSubs.length > 0 && firstExtSub
      ? el(
          "button",
          {
            type: "button",
            className: "ghost",
            "data-tip": "Adjust subtitle timing",
            onclick: () => {
              openSyncDialog(
                extEpSubs,
                ep.path ?? "",
                "series",
                series.id,
                fmtEpisode(sg.season, ep.episode),
              );
            },
          },
          icon("sync"),
          el("span", { className: "btn-text" }, " Sync"),
        )
      : null;

  return el(
    "tr",
    null,
    epNumCell,
    el("td", { className: "ep-title", "data-ep": epLabel }, ep.title ?? ""),
    covCell,
    el(
      "td",
      { "data-col": "actions" },
      el("div", { className: "action-group" }, syncBtn, histBtn, searchBtn),
    ),
  );
}

export function renderSeriesDetail(
  series: SeriesItem,
  seasons: SeasonGroup[],
  subFiles: SubtitleEntry[],
  historySet?: Set<string>,
): void {
  historySet ??= new Set();
  store.set("detailCtx", { series, seasons, tvdbId: series.tvdb_id });

  // Show Files button only if any external subtitle exists.
  const hasExtSubs = subFiles.some((f) => f.source === "external");
  const headerEl = document.querySelector("#coveragePanel .card-head");
  if (headerEl) {
    const oldFiles = headerEl.querySelector('[data-nav="files"]');
    if (oldFiles) {
      oldFiles.remove();
    }
    if (hasExtSubs && store.get("isAdmin")) {
      // Build media_id → video path map from episode data.
      const epPaths = new Map<string, string>();
      for (const sg of seasons) {
        for (const ep of sg.episodes ?? []) {
          if (ep.has_file && ep.path) {
            const mid = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
            epPaths.set(mid, ep.path);
          }
        }
      }
      const filesBtn = el(
        "button",
        {
          type: "button",
          className: "ghost",
          "data-nav": "files",
          onclick: () => {
            openFileManager(
              "episode",
              `tvdb-${series.tvdb_id}-`,
              series.title,
              `/series/${series.tvdb_id}`,
              epPaths,
              series.id,
            );
          },
        },
        icon("file"),
        el("span", { className: "btn-text" }, " Files"),
      );
      insertNavButton(filesBtn);
    }
  }

  const out = $.coverageContent;
  const ls = series.targets;
  const targetLangs = ls.map((t) => ({
    lang: t.language,
    variant: t.variant,
  }));

  // Index subtitle files by media_id, then by lang|variant.
  const byMedia = Object.groupBy(subFiles, (f) => f.media_id);
  const subIdx: Record<string, Partial<Record<string, SubtitleEntry[]>>> = {};
  for (const [mediaId, files] of Object.entries(byMedia)) {
    subIdx[mediaId] = Object.groupBy(files ?? [], (f) => `${f.language}|${f.variant}`);
  }

  const frag = document.createDocumentFragment();

  if (seasons.length === 0) {
    frag.appendChild(emptyDiv("No episodes found."));
    patch(out, frag);
    return;
  }

  // Sort seasons: regular seasons first (ascending), specials (season 0) last.
  const sortedSeasons = [...seasons].sort((a, b) => {
    if (a.season === 0) {
      return 1;
    }
    if (b.season === 0) {
      return -1;
    }
    return a.season - b.season;
  });

  // Detect if any episode uses absolute numbering that genuinely
  // diverges from sequential aired order (not just a running count
  // across seasons, which Sonarr sets for every multi-season show).
  // Compute the expected absolute as cumulative episode count from
  // prior seasons + current episode number; only flag when actual
  // absolute differs.
  const hasAbsOrder = (() => {
    let prior = 0;
    for (const sg of sortedSeasons) {
      if (sg.season === 0) {
        continue;
      }
      const eps = sg.episodes ?? [];
      for (const ep of eps) {
        if (ep.absolute_episode && ep.absolute_episode !== prior + ep.episode) {
          return true;
        }
      }
      prior += eps.length;
    }
    return false;
  })();

  // Build column header labels (recreated per season since DOM nodes
  // can only appear once).
  function makeColHeaders(): HTMLElement {
    return el(
      "tr",
      { className: "season-head" },
      el("th", null, "Ep"),
      el("th", null, "Title"),
      el("th", null, "Subtitles"),
      el("th", null, ""),
    );
  }

  // Single table for all seasons so columns align across seasons.
  const tbody = el("tbody");

  type DetailRow =
    | { kind: "gap"; season: number }
    | {
        kind: "head";
        season: number;
        label: string;
        syncEps: SeasonSyncEpisode[];
        hasHistory: boolean;
      }
    | { kind: "cols"; season: number }
    | { kind: "ep"; season: number; ep: Episode };

  const rows: DetailRow[] = [];
  let first = true;
  for (const sg of sortedSeasons) {
    if (!(sg.episodes ?? []).some((ep) => ep.has_file)) {
      continue;
    }
    if (!first) {
      rows.push({ kind: "gap", season: sg.season });
    }
    first = false;
    const syncEps = collectSeasonSyncEps(sg, series, subIdx, targetLangs);
    const hasHist = [...historySet].some((id) =>
      id.startsWith(`tvdb-${series.tvdb_id}-s${pad(sg.season)}e`),
    );
    rows.push({
      kind: "head",
      season: sg.season,
      label: sg.season === 0 ? "Specials" : `Season ${sg.season}`,
      syncEps,
      hasHistory: hasHist,
    });
    rows.push({ kind: "cols", season: sg.season });
    for (const ep of sg.episodes ?? []) {
      if (!ep.has_file) {
        continue;
      }
      rows.push({ kind: "ep", season: sg.season, ep });
    }
  }

  reconcile(tbody, rows, {
    key: (r) => {
      switch (r.kind) {
        case "gap":
          return `gap-${r.season}`;
        case "head":
          return `head-${r.season}`;
        case "cols":
          return `cols-${r.season}`;
        case "ep":
          return tvdbMediaId(series.tvdb_id, r.season, r.ep.episode);
      }
    },
    mount: (r) => {
      switch (r.kind) {
        case "gap":
          return el("tr", { className: "season-gap" }, el("td", { colSpan: 999 }));
        case "head": {
          const searchBtn = el(
            "button",
            {
              type: "button",
              className: "ghost",
              "data-tip": "Auto: scan and download missing subtitles for this season",
              onclick: (e: MouseEvent) =>
                triggerSeasonScan(series, r.season, e.currentTarget as HTMLButtonElement),
            },
            icon("search"),
            el("span", { className: "btn-text" }, " Search"),
          );
          const histBtn = r.hasHistory
            ? el(
                "button",
                {
                  type: "button",
                  className: "ghost",
                  "data-tip": "View download history for this season",
                  onclick: () => {
                    emit(BusEvent.NavHistory, `${series.title} S${pad(r.season)}`);
                  },
                },
                icon("history"),
                el("span", { className: "btn-text" }, " History"),
              )
            : null;
          const syncBtn =
            r.syncEps.length > 0
              ? el(
                  "button",
                  {
                    type: "button",
                    className: "ghost",
                    "data-tip": "Audio sync all subtitles in this season",
                    onclick: () => {
                      confirmSeasonSync(series.title, r.season, r.syncEps);
                    },
                  },
                  icon("sync"),
                  el("span", { className: "btn-text" }, " Sync"),
                )
              : null;
          return el(
            "tr",
            { className: "season-head" },
            el("td", {}, r.label),
            el("td", {}),
            el("td", {}),
            el(
              "td",
              { "data-col": "actions" },
              el("div", { className: "action-group" }, syncBtn, histBtn, searchBtn),
            ),
          );
        }
        case "cols":
          return makeColHeaders();
        case "ep":
          return buildEpisodeRow(
            series,
            { season: r.season, episodes: [] },
            r.ep,
            subIdx,
            targetLangs,
            hasAbsOrder,
            historySet,
          );
      }
    },
  });

  frag.appendChild(
    el(
      "table",
      { className: "series-detail" },
      el(
        "colgroup",
        null,
        el("col", { style: "width: 8%" }),
        el("col", { style: "width: 34%" }),
        el("col", { style: "width: 34%" }),
        el("col", { style: "width: 24%" }),
      ),
      tbody,
    ),
  );
  patch(out, frag);
}

// --- Movie detail drilldown ---

export function openMovieDetail(m: MovieDetail, skipPush?: boolean): void {
  // Abort any prior in-flight detail fetch.
  if (detailAbort) {
    detailAbort.abort();
  }
  detailAbort = new AbortController();
  const { signal } = detailAbort;

  if (!skipPush) {
    history.pushState(null, "", `/movie/${m.tmdb_id}`);
  }
  const targets = m.targets;
  const subsInfo =
    targets.length > 0 ? targets.map((t) => fmtLangVariant(t.language, t.variant)).join(", ") : "";
  const info = `${m.year} \u00B7 audio: ${langName(
    m.rule,
  )}${subsInfo ? ` \u00B7 subs: ${subsInfo}` : ""}`;
  const subs = m.subs;
  const hasExtSubs = subs.some((s) => s.source !== EMBEDDED_PROVIDER && s.path);
  emit(BusEvent.PanelConfigure, {
    visible: false,
    detail: {
      title: m.title,
      info,
      backPath: "/",
      arrLink: buildRadarrLink(m),
      arrName: "Radarr",
      filesAction: hasExtSubs
        ? () => {
            openFileManager(
              "movie",
              `tmdb-${m.tmdb_id}`,
              m.title,
              `/movie/${m.tmdb_id}`,
              new Map([["", m.path ?? ""]]),
              m.id,
            );
          }
        : null,
    },
  });

  // Check history async and add button if found.
  apiGet<string[]>(`/api/state/ids?type=movie&prefix=tmdb-${m.tmdb_id}`, signal)
    .then((ids: string[] | null) => {
      if (signal.aborted) {
        return;
      }
      if (ids && ids.length > 0) {
        const headerEl = document.querySelector("#coveragePanel .card-head");
        if (headerEl && !headerEl.querySelector('[data-nav="hist"]')) {
          const histBtn = el(
            "button",
            {
              type: "button",
              className: "ghost",
              "data-nav": "hist",
              onclick: () => {
                emit(BusEvent.NavHistory, m.title);
              },
            },
            icon("history"),
            el("span", { className: "btn-text" }, " History"),
          );
          insertNavButton(histBtn);
        }
      }
    })
    .catch(() => {
      /* ignore */
    });
  // Mark as detail view so refreshCurrentPage doesn't replace with library.
  store.set("detailCtx", { movie: true, tmdbId: m.tmdb_id });
  const out = $.coverageContent;
  const frag = document.createDocumentFragment();

  // Collect all external subtitles for the sync button.
  const extSubs = subs.filter((s) => s.source !== EMBEDDED_PROVIDER && s.path);

  // Add sync button to header if external subs exist.
  const firstExtSub = extSubs[0];
  if (extSubs.length > 0 && firstExtSub) {
    const syncBtn = el(
      "button",
      {
        type: "button",
        className: "ghost",
        "data-nav": "sync",
        "data-tip": "Adjust subtitle timing",
        onclick: () => {
          openSyncDialog(extSubs, m.path ?? "", "movie", m.id, m.title);
        },
      },
      icon("sync"),
      el("span", { className: "btn-text" }, " Sync"),
    );
    insertNavButton(syncBtn);
  }

  if (targets.length === 0) {
    frag.appendChild(el("span", { className: "badge", "data-status": "muted" }, "not scanned"));
  } else {
    const subIdx = Object.groupBy(subs, (s) => `${s.language}|${s.variant}`);

    const tbody = el("tbody");
    for (const t of targets) {
      const key = `${t.language}|${t.variant}`;
      const entries = (subIdx as Record<string, SubtitleEntry[] | undefined>)[key] ?? [];
      const label = langName(t.language) + (t.variant !== DEFAULT_VARIANT ? ` (${t.variant})` : "");

      const searchBtn = el(
        "button",
        {
          type: "button",
          className: "ghost",
          "data-tip": `Search ${label} subtitles`,
          onclick: (e: MouseEvent) => {
            e.stopPropagation();
            openSearchPopup("movie", m, null, null, t.language);
          },
        },
        icon("search"),
        el("span", { className: "btn-text" }, " Search"),
      );

      const covCell = el("td", { className: "ep-coverage" });

      if (entries.length > 0) {
        const byCodec: Record<string, SubtitleEntry[]> = {};
        for (const sub of entries) {
          const codec = sub.codec ?? "srt";
          byCodec[codec] ??= [];
          byCodec[codec].push(sub);
        }
        for (const [, codecEntries] of Object.entries(byCodec)) {
          covCell.appendChild(coverageBadge(codecEntries, null));
        }
      } else {
        covCell.appendChild(coverageBadge(null, null));
      }

      tbody.appendChild(
        el(
          "tr",
          null,
          el("td", null, label),
          covCell,
          el("td", { "data-col": "actions" }, searchBtn),
        ),
      );
    }
    frag.appendChild(
      el(
        "table",
        { className: "movie-detail" },
        el(
          "thead",
          null,
          el(
            "tr",
            null,
            el("th", null, "Language"),
            el("th", null, "Subtitles"),
            el("th", null, ""),
          ),
        ),
        tbody,
      ),
    );
  }

  patch(out, frag);
}

// --- Bus handlers: coverage.js emits these ---
on(BusEvent.OpenSeries, ({ item, skipPush }) => {
  openSeriesDetail(item as SeriesItem, skipPush);
});
on(BusEvent.OpenMovie, ({ item, skipPush }) => {
  openMovieDetail(item as MovieDetail, skipPush);
});
on(BusEvent.ScanSeries, ({ item, btn }) => {
  void triggerSeriesScan(item as SeriesItem, btn);
});
on(BusEvent.ScanMovie, ({ item, btn }) => {
  void triggerMovieScan(item as MovieDetail, btn);
});
