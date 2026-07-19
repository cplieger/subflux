// detail.ts — series and movie detail drilldown views

import * as store from "./store.js";
import { $, el, icon, errDiv, pad, insertNavButton } from "./dom.js";
import { skeletonTiming } from "@cplieger/ui-primitives/skeleton";
import { coverageSeriesDetail, mediaEpisodes, stateIDs } from "./wire/client.gen.js";
import { registerCleanup } from "@cplieger/actions";
import {
  fmtEpisode,
  tvdbMediaId,
  langName,
  fmtLangVariant,
  emptyState,
  setDocTitle,
} from "./utils.js";
import { openConfig } from "./config.js";
import { DEFAULT_VARIANT, EMBEDDED_PROVIDER } from "./constants.js";
import { on, emit, BusEvent } from "./bus.js";
import { openSearchPopup } from "./search.js";
import { openSyncDialog } from "./sync.js";
import { openFileManager } from "./files.js";
import {
  triggerSeriesScan,
  triggerSeasonScan,
  triggerMovieScan,
  applyScanButtonState,
} from "./detail-scan.js";
import { seasonScopeKey } from "./scan-scope.js";
import { confirmSeasonSync } from "./detail-season-sync.js";
import type { SeasonSyncEpisode } from "./detail-season-sync.js";
import { subtitleRef } from "./file-ref.js";
import type {
  SubtitleEntry,
  MovieDetail,
  EpisodeItem,
  SeasonGroup,
  SeriesItem,
} from "./api-types.js";
import { patch, createCollection, bindList, type ListSpec } from "@cplieger/reactive";

// Module-level abort controller for detail navigation fetches. Self-cleans
// on internal navigation (each new openSeriesDetail/openMovieDetail aborts
// the previous), but page unload also needs a cleanup hook so in-flight
// fetches don't outlive the document.
let detailAbort: AbortController | null = null;
registerCleanup(() => {
  detailAbort?.abort();
  detailAbort = null;
});

// --- Persistent two-tier render state (mirrors coverage.ts / files.ts) ---
//
// Each detail view owns a module-level `createCollection` + a `bindList`
// binding so a coverage SSE refresh updates rows IN PLACE (stable DOM node
// identity, buttons not re-created, only changed coverage cells repaint)
// instead of rebuilding the whole <table>. A render REUSES the live binding
// when it targets the same media id and the bound <tbody> is still in the DOM;
// otherwise it REBUILDS (fresh collection + binding + table shell).

let seriesColl: ReturnType<typeof createCollection<DetailRow>> | null = null;
let seriesTbody: HTMLElement | null = null;
let seriesUnbind: (() => void) | null = null;
let seriesKey = ""; // String(tvdb_id) of the currently-bound series

let movieColl: ReturnType<typeof createCollection<MovieRow>> | null = null;
let movieTbody: HTMLElement | null = null;
let movieUnbind: (() => void) | null = null;
let movieKey = ""; // String(tmdb_id) of the currently-bound movie

function disposeSeriesBinding(): void {
  seriesUnbind?.();
  seriesUnbind = null;
  seriesColl = null;
  seriesTbody = null;
  seriesKey = "";
}

function disposeMovieBinding(): void {
  movieUnbind?.();
  movieUnbind = null;
  movieColl = null;
  movieTbody = null;
  movieKey = "";
}

/** Drop null entries so a children list can be spread into `replaceChildren`
 *  (which, unlike `el`, does not skip nulls). */
function compact(nodes: (HTMLElement | null)[]): HTMLElement[] {
  return nodes.filter((n): n is HTMLElement => n !== null);
}

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
  setDocTitle(s.title);
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
  // Anti-flicker loading skeleton (150ms show-delay + 300ms min-visible,
  // abort-aware): a cached/fast load never paints it, a slow one keeps it up
  // long enough not to blink. The commit ALWAYS detaches the current content
  // (fresh-fragment patch) before rendering — preserving the always-REBUILD
  // guarantee the old eager skeleton patch provided, so renderSeriesDetail
  // never reuses a stale live-table binding from a previous detail view. (A
  // coverage SSE refresh paints no skeleton, so it reuses — unchanged.)
  const timing = skeletonTiming(
    () => {
      const skel = document.createDocumentFragment();
      for (let i = 0; i < 6; i++) {
        skel.appendChild(
          el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
        );
      }
      patch(out, skel);
    },
    { minVisibleMs: 300, signal },
  );

  Promise.all([
    mediaEpisodes(s.id, { signal }),
    coverageSeriesDetail(s.tvdb_id, { signal }),
    stateIDs({ type: "episode", prefix: `tvdb-${s.tvdb_id}-` }, { signal }),
  ])
    .then(([seasons, subFiles, historyIDs]) => {
      if (signal.aborted) {
        timing.cancel();
        return;
      }
      timing.commit(() => {
        patch(out, document.createDocumentFragment()); // detach: rebuild guarantee
        renderSeriesDetail(s, seasons ?? [], subFiles ?? [], new Set(historyIDs ?? []));
      });
    })
    .catch((e: unknown) => {
      if (signal.aborted) {
        timing.cancel();
        return;
      }
      const msg = e instanceof Error ? e.message : String(e);
      timing.commit(() => {
        patch(out, errDiv(msg));
      });
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
  for (const ep of sg.episodes) {
    if (!ep.has_file) {
      continue;
    }
    const mediaId = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
    const subs = subIdx[mediaId] ?? {};
    for (const t of targetLangs) {
      const key = `${t.lang}|${t.variant}`;
      const entries = subs[key];
      if (entries) {
        for (const sub of entries) {
          if (sub.source !== EMBEDDED_PROVIDER) {
            result.push({
              ref: subtitleRef("episode", sub),
              label: fmtEpisode(sg.season, ep.episode),
            });
          }
        }
      }
    }
  }
  return result;
}

// --- Series row model (two-tier collection rows) ---
//
// Each row carries EVERYTHING its mount/update needs, so the bindList specs
// (bound once at REBUILD) never read render-scoped state that would go stale
// on a REUSE refresh. The `sig` is a content signature: update() compares it
// against the row element's `data-sig` and short-circuits when unchanged, so a
// setAll (which bumps every per-row signal) repaints ONLY the rows whose
// coverage/history actually changed.

type DetailRow =
  | { kind: "gap"; season: number }
  | {
      kind: "head";
      season: number;
      label: string;
      series: SeriesItem;
      syncEps: SeasonSyncEpisode[];
      hasHistory: boolean;
      sig: string;
    }
  | { kind: "cols"; season: number }
  | {
      kind: "ep";
      season: number;
      ep: EpisodeItem;
      series: SeriesItem;
      subs: Partial<Record<string, SubtitleEntry[]>>;
      targetLangs: { lang: string; variant: string }[];
      hasAbsOrder: boolean;
      hasHistory: boolean;
      sig: string;
    };

type DetailHeadRow = Extract<DetailRow, { kind: "head" }>;
type DetailEpRow = Extract<DetailRow, { kind: "ep" }>;

function detailRowKey(r: DetailRow): string {
  switch (r.kind) {
    case "gap":
      return `gap-${r.season}`;
    case "head":
      return `head-${r.season}`;
    case "cols":
      return `cols-${r.season}`;
    case "ep":
      return tvdbMediaId(r.series.tvdb_id, r.season, r.ep.episode);
  }
}

/** Content signature for an episode row: every field that drives the coverage
 *  cell (per-lang entry source/codec/score/path) plus history-button presence.
 *  Stable across a refresh that did not touch this episode -> update no-op. */
function epSig(
  subs: Partial<Record<string, SubtitleEntry[]>>,
  targetLangs: { lang: string; variant: string }[],
  hasHistory: boolean,
): string {
  let parts: string;
  if (targetLangs.length > 0) {
    parts = targetLangs
      .map((t) => {
        const entries = subs[`${t.lang}|${t.variant}`] ?? [];
        return entries
          .map((e) => `${e.source}:${e.codec ?? ""}:${e.score ?? 0}:${e.ordinal ?? 0}`)
          .join(",");
      })
      .join("|");
  } else {
    parts = `subs:${Object.keys(subs).length}`;
  }
  const ic = [...store.get("ignoredCodecs")].sort().join(",");
  return `${parts}#h:${hasHistory ? 1 : 0}#i:${ic}`;
}

// Column header labels (recreated per season since DOM nodes can only appear
// once in the document).
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

/** Coverage-cell children for an episode row (the badge spans). */
function episodeCoverageChildren(row: DetailEpRow): HTMLElement[] {
  const { subs, targetLangs } = row;
  if (targetLangs.length > 0) {
    return targetLangs.map((t) => {
      const key = `${t.lang}|${t.variant}`;
      const entries = subs[key];
      const langAttr = fmtLangVariant(t.lang, t.variant);
      return coverageBadge(entries ?? null, langAttr);
    });
  }
  return [
    Object.keys(subs).length > 0
      ? el("span", { className: "badge", "data-status": "ok" }, `${Object.keys(subs).length} subs`)
      : el("span", { className: "badge", "data-status": "err" }, "\u2014"),
  ];
}

/** Action-group children for an episode row: [sync?, history?, search]. */
function episodeActionChildren(row: DetailEpRow): (HTMLElement | null)[] {
  const { series, ep, season, subs, targetLangs, hasHistory } = row;

  const searchBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      "data-tip": "Manual: browse and pick subtitles for this episode",
      onclick: () => {
        openSearchPopup("episode", series, season, ep);
      },
    },
    icon("search"),
    el("span", { className: "btn-text" }, " Search"),
  );

  const extEpSubs: SubtitleEntry[] = [];
  for (const t of targetLangs) {
    const key = `${t.lang}|${t.variant}`;
    const entries = subs[key];
    if (entries) {
      for (const sub of entries) {
        if (sub.source !== EMBEDDED_PROVIDER) {
          extEpSubs.push(sub);
        }
      }
    }
  }

  const histBtn = hasHistory
    ? el(
        "button",
        {
          type: "button",
          className: "ghost",
          "data-tip": "View download history for this episode",
          onclick: () => {
            const label = `${series.title} ${fmtEpisode(season, ep.episode)}`;
            emit(BusEvent.NavHistory, label);
          },
        },
        icon("history"),
        el("span", { className: "btn-text" }, " History"),
      )
    : null;

  const firstExtSub = extEpSubs[0];
  const syncBtn =
    extEpSubs.length > 0 && firstExtSub && ep.has_file
      ? el(
          "button",
          {
            type: "button",
            className: "ghost",
            "data-tip": "Adjust subtitle timing",
            onclick: () => {
              openSyncDialog(extEpSubs, "series", series.id, fmtEpisode(season, ep.episode));
            },
          },
          icon("sync"),
          el("span", { className: "btn-text" }, " Sync"),
        )
      : null;

  return [syncBtn, histBtn, searchBtn];
}

/** Build a single episode table row with coverage badges and action buttons.
 *  The coverage <td> (`.ep-coverage`) and the action group are separately
 *  addressable so `update` can repaint just them. */
function buildEpisodeRow(row: DetailEpRow): HTMLElement {
  const { ep, season, hasAbsOrder } = row;

  const absEp = ep.absolute_episode;
  const airedEp = ep.episode;
  const epLabel = hasAbsOrder && absEp ? `#${absEp}` : `E${pad(airedEp)}`;

  const epNumCell =
    hasAbsOrder && absEp
      ? el(
          "td",
          {
            className: "ep-num",
            "data-tip": `Absolute #${absEp}, aired ${fmtEpisode(season, airedEp)}`,
          },
          epLabel,
          el("span", { className: "ep-aired" }, ` E${pad(airedEp)}`),
        )
      : el("td", { className: "ep-num" }, epLabel);

  const covCell = el("td", { className: "ep-coverage" }, ...episodeCoverageChildren(row));

  const tr = el(
    "tr",
    null,
    epNumCell,
    el("td", { className: "ep-title", "data-ep": epLabel }, ep.title),
    covCell,
    el(
      "td",
      { "data-col": "actions" },
      el("div", { className: "action-group" }, ...episodeActionChildren(row)),
    ),
  );
  tr.dataset["sig"] = row.sig;
  return tr;
}

/** Repaint just the dynamic parts of an episode row: the coverage cell and the
 *  sync/history action buttons (external-sub presence + history can change). */
function paintEpisodeRow(node: HTMLElement, row: DetailEpRow): void {
  const covCell = node.querySelector("td.ep-coverage");
  if (covCell) {
    covCell.replaceChildren(...episodeCoverageChildren(row));
  }
  const actionGroup = node.querySelector('[data-col="actions"] .action-group');
  if (actionGroup) {
    actionGroup.replaceChildren(...compact(episodeActionChildren(row)));
  }
  node.dataset["sig"] = row.sig;
}

/** Action-group children for a season-head row: [sync?, history?, search]. */
function seasonHeadActionChildren(row: DetailHeadRow): (HTMLElement | null)[] {
  const { series, season, syncEps, hasHistory } = row;

  const searchBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      "data-tip": "Auto: scan and download missing subtitles for this season",
      "data-scan-scope": seasonScopeKey(series.id, season),
      onclick: () => triggerSeasonScan(series, season),
    },
    icon("search"),
    el("span", { className: "btn-text" }, " Search"),
  ) as HTMLButtonElement;
  // Rows painted while a scan runs restore the disabled+spinner state.
  applyScanButtonState(searchBtn);
  const histBtn = hasHistory
    ? el(
        "button",
        {
          type: "button",
          className: "ghost",
          "data-tip": "View download history for this season",
          onclick: () => {
            emit(BusEvent.NavHistory, `${series.title} S${pad(season)}`);
          },
        },
        icon("history"),
        el("span", { className: "btn-text" }, " History"),
      )
    : null;
  const syncBtn =
    syncEps.length > 0
      ? el(
          "button",
          {
            type: "button",
            className: "ghost",
            "data-tip": "Audio sync all subtitles in this season",
            onclick: () => {
              confirmSeasonSync(series.title, season, syncEps);
            },
          },
          icon("sync"),
          el("span", { className: "btn-text" }, " Sync"),
        )
      : null;
  return [syncBtn, histBtn, searchBtn];
}

/** Build a season-head row (label + action buttons). */
function buildSeasonHeadRow(row: DetailHeadRow): HTMLElement {
  const tr = el(
    "tr",
    { className: "season-head" },
    el("td", {}, row.label),
    el("td", {}),
    el("td", {}),
    el(
      "td",
      { "data-col": "actions" },
      el("div", { className: "action-group" }, ...seasonHeadActionChildren(row)),
    ),
  );
  tr.dataset["sig"] = row.sig;
  return tr;
}

/** Repaint a season-head row's action buttons (sync/history visibility tracks
 *  syncEps + hasHistory). */
function paintSeasonHead(node: HTMLElement, row: DetailHeadRow): void {
  const actionGroup = node.querySelector('[data-col="actions"] .action-group');
  if (actionGroup) {
    actionGroup.replaceChildren(...compact(seasonHeadActionChildren(row)));
  }
  node.dataset["sig"] = row.sig;
}

// bindList row lifecycle for the series table. Bound once per REBUILD; every
// closure is data-driven (reads only the row argument) so a REUSE setAll feeds
// fresh row objects without any stale render-scope capture.
const seriesSpec: ListSpec<DetailRow> = {
  mount: (r) => {
    switch (r.kind) {
      case "gap":
        return el("tr", { className: "season-gap" }, el("td", { colSpan: 999 }));
      case "head":
        return buildSeasonHeadRow(r);
      case "cols":
        return makeColHeaders();
      case "ep":
        return buildEpisodeRow(r);
    }
  },
  update: (node, r) => {
    switch (r.kind) {
      case "gap":
      case "cols":
        return; // static rows — never repaint
      case "head":
        if (node.dataset["sig"] === r.sig) {
          return;
        }
        paintSeasonHead(node, r);
        return;
      case "ep":
        if (node.dataset["sig"] === r.sig) {
          return;
        }
        paintEpisodeRow(node, r);
        return;
    }
  },
};

/** Build the ordered DetailRow list for a series (season heads, column
 *  headers, gaps, and episode rows in display order). */
function buildSeriesRows(
  series: SeriesItem,
  sortedSeasons: SeasonGroup[],
  subIdx: Record<string, Partial<Record<string, SubtitleEntry[]>>>,
  targetLangs: { lang: string; variant: string }[],
  hasAbsOrder: boolean,
  historySet: Set<string>,
): DetailRow[] {
  const rows: DetailRow[] = [];
  let first = true;
  for (const sg of sortedSeasons) {
    if (!sg.episodes.some((ep) => ep.has_file)) {
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
      series,
      syncEps,
      hasHistory: hasHist,
      sig: `${syncEps.length}:${hasHist}`,
    });
    rows.push({ kind: "cols", season: sg.season });
    for (const ep of sg.episodes) {
      if (!ep.has_file) {
        continue;
      }
      const mediaId = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
      const subs = subIdx[mediaId] ?? {};
      const hasHistory = historySet.has(mediaId);
      rows.push({
        kind: "ep",
        season: sg.season,
        ep,
        series,
        subs,
        targetLangs,
        hasAbsOrder,
        hasHistory,
        sig: epSig(subs, targetLangs, hasHistory),
      });
    }
  }
  return rows;
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

  if (seasons.length === 0) {
    // Tear down any live series binding so a later same-series render rebuilds.
    disposeSeriesBinding();
    const frag = document.createDocumentFragment();
    frag.appendChild(
      emptyState(
        "No episodes with video files were found for this series. Episodes appear here once Sonarr has imported them.",
      ),
    );
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
      const eps = sg.episodes;
      for (const ep of eps) {
        if (ep.absolute_episode && ep.absolute_episode !== prior + ep.episode) {
          return true;
        }
      }
      prior += eps.length;
    }
    return false;
  })();

  const rows = buildSeriesRows(series, sortedSeasons, subIdx, targetLangs, hasAbsOrder, historySet);

  // REUSE: same series and the bound <tbody> is still live in the DOM — just
  // push the new rows; the per-row signals repaint only the changed rows.
  const key = String(series.tvdb_id);
  if (
    seriesKey === key &&
    seriesColl &&
    seriesTbody &&
    seriesTbody.isConnected &&
    out.contains(seriesTbody)
  ) {
    seriesColl.setAll(rows);
    return;
  }

  // REBUILD: fresh collection + binding + table shell.
  disposeSeriesBinding();
  const coll = createCollection<DetailRow>(detailRowKey);
  const tbody = el("tbody");
  const unbind = bindList(tbody, coll, seriesSpec);
  coll.setAll(rows);

  const frag = document.createDocumentFragment();
  frag.appendChild(
    el(
      "table",
      { className: "series-detail", "data-series-id": key },
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
  seriesColl = coll;
  seriesTbody = tbody;
  seriesUnbind = unbind;
  seriesKey = key;
}

// --- Movie detail drilldown ---

// One language-target row of the movie detail table.
interface MovieRow {
  key: string; // `${language}|${variant}`
  label: string;
  entries: SubtitleEntry[];
  lang: string; // target language code (for the Search popup)
  movie: MovieDetail;
  sig: string;
}

/** Content signature for a movie language row: the entries' source/codec/score
 *  (everything the coverage badges render). */
function movieSig(entries: SubtitleEntry[]): string {
  const ic = [...store.get("ignoredCodecs")].sort().join(",");
  return `${entries.map((e) => `${e.source}:${e.codec ?? ""}:${e.score ?? 0}`).join(",")}#i:${ic}`;
}

/** Coverage-cell children for a movie row (codec-grouped badges, or the empty
 *  badge when there are no entries). */
function movieCoverageChildren(entries: SubtitleEntry[]): HTMLElement[] {
  if (entries.length > 0) {
    const byCodec: Record<string, SubtitleEntry[]> = {};
    for (const sub of entries) {
      const codec = sub.codec ?? "srt";
      byCodec[codec] ??= [];
      byCodec[codec].push(sub);
    }
    return Object.entries(byCodec).map(([, codecEntries]) => coverageBadge(codecEntries, null));
  }
  return [coverageBadge(null, null)];
}

/** Build a movie language row: label, coverage cell, and the per-lang Search
 *  button (captures movie + lang, both stable for a given tmdb_id). */
function buildMovieRow(row: MovieRow): HTMLElement {
  const { label, lang, movie } = row;

  const searchBtn = el(
    "button",
    {
      type: "button",
      className: "ghost",
      "data-tip": `Search ${label} subtitles`,
      onclick: (e: MouseEvent) => {
        e.stopPropagation();
        openSearchPopup("movie", movie, null, null, lang);
      },
    },
    icon("search"),
    el("span", { className: "btn-text" }, " Search"),
  );

  const covCell = el("td", { className: "ep-coverage" }, ...movieCoverageChildren(row.entries));

  const tr = el(
    "tr",
    null,
    el("td", null, label),
    covCell,
    el("td", { "data-col": "actions" }, searchBtn),
  );
  tr.dataset["sig"] = row.sig;
  return tr;
}

/** Repaint just the coverage cell of a movie row (Search button is stable). */
function paintMovieRow(node: HTMLElement, row: MovieRow): void {
  const covCell = node.querySelector("td.ep-coverage");
  if (covCell) {
    covCell.replaceChildren(...movieCoverageChildren(row.entries));
  }
  node.dataset["sig"] = row.sig;
}

const movieSpec: ListSpec<MovieRow> = {
  mount: (r) => buildMovieRow(r),
  update: (node, r) => {
    if (node.dataset["sig"] === r.sig) {
      return;
    }
    paintMovieRow(node, r);
  },
};

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
  setDocTitle(m.title);
  const targets = m.targets;
  const subsInfo =
    targets.length > 0 ? targets.map((t) => fmtLangVariant(t.language, t.variant)).join(", ") : "";
  const info = `${m.year} \u00B7 audio: ${langName(
    m.rule,
  )}${subsInfo ? ` \u00B7 subs: ${subsInfo}` : ""}`;
  const subs = m.subs;
  const hasExtSubs = subs.some((s) => s.source !== EMBEDDED_PROVIDER);
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
            openFileManager("movie", `tmdb-${m.tmdb_id}`, m.title, `/movie/${m.tmdb_id}`, m.id);
          }
        : null,
    },
  });

  // Check history async and add button if found.
  stateIDs({ type: "movie", prefix: `tmdb-${m.tmdb_id}` }, { signal })
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

  // Collect all external subtitles for the sync button.
  const extSubs = subs.filter((s) => s.source !== EMBEDDED_PROVIDER);

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
          openSyncDialog(extSubs, "movie", m.id, m.title);
        },
      },
      icon("sync"),
      el("span", { className: "btn-text" }, " Sync"),
    );
    insertNavButton(syncBtn);
  }

  if (targets.length === 0) {
    // No language targets: nothing to bind. Drop any live movie binding and
    // explain the state with a way out (a bare "not scanned" badge as the
    // whole page body was a dead end).
    disposeMovieBinding();
    patch(
      out,
      emptyState(
        "No language rule matches this movie, so there is nothing to search for. Add or adjust a language rule in Settings.",
        "Open Settings",
        () => {
          openConfig();
        },
      ),
    );
    return;
  }

  const subIdx = Object.groupBy(subs, (s) => `${s.language}|${s.variant}`);
  const rows: MovieRow[] = targets.map((t) => {
    const key = `${t.language}|${t.variant}`;
    const entries = (subIdx as Record<string, SubtitleEntry[] | undefined>)[key] ?? [];
    const label = langName(t.language) + (t.variant !== DEFAULT_VARIANT ? ` (${t.variant})` : "");
    return { key, label, entries, lang: t.language, movie: m, sig: movieSig(entries) };
  });

  // REUSE: same movie and the bound <tbody> is still live — push fresh rows.
  const mKey = String(m.tmdb_id);
  if (
    movieKey === mKey &&
    movieColl &&
    movieTbody &&
    movieTbody.isConnected &&
    out.contains(movieTbody)
  ) {
    movieColl.setAll(rows);
    return;
  }

  // REBUILD: fresh collection + binding + table shell.
  disposeMovieBinding();
  const coll = createCollection<MovieRow>((r) => r.key);
  const tbody = el("tbody");
  const unbind = bindList(tbody, coll, movieSpec);
  coll.setAll(rows);

  const frag = document.createDocumentFragment();
  frag.appendChild(
    el(
      "table",
      { className: "movie-detail", "data-movie-id": mKey },
      el(
        "thead",
        null,
        el("tr", null, el("th", null, "Language"), el("th", null, "Subtitles"), el("th", null, "")),
      ),
      tbody,
    ),
  );
  patch(out, frag);
  movieColl = coll;
  movieTbody = tbody;
  movieUnbind = unbind;
  movieKey = mKey;
}

// --- Bus handlers: coverage.js emits these ---
on(BusEvent.OpenSeries, ({ item, skipPush }) => {
  openSeriesDetail(item as SeriesItem, skipPush);
});
on(BusEvent.OpenMovie, ({ item, skipPush }) => {
  openMovieDetail(item as MovieDetail, skipPush);
});
on(BusEvent.ScanSeries, ({ item }) => {
  void triggerSeriesScan(item as SeriesItem);
});
on(BusEvent.ScanMovie, ({ item }) => {
  void triggerMovieScan(item as MovieDetail);
});
