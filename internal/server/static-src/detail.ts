// detail.ts — series and movie detail drilldown views

import * as store from "./store.js";
import { $, el, icon, errDiv, pad, insertNavButton } from "./dom.js";
import { skeletonTiming } from "@cplieger/ui-primitives/skeleton";
import { coverageSeriesDetail, mediaEpisodes, stateIDs } from "./wire/client.gen.js";
import { readMovieDetail, type MovieDetailReads } from "./movie-detail-read.js";
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
import { openSyncDialog, confirmSeasonSync } from "./sync.js";
import { openFileManager } from "./files.js";
import {
  triggerSeriesScan,
  triggerSeasonScan,
  triggerMovieScan,
  registerScanButton,
} from "./detail-scan.js";
import { seasonScopeKey } from "./scan-scope.js";
import type {
  SubtitleEntry,
  MovieDetail,
  EpisodeItem,
  SeasonGroup,
  SeriesItem,
} from "./api-types.js";
import { patch, createCollection, bindList, type ListSpec } from "@cplieger/reactive";
import { join } from "@cplieger/keyenc";
import { contentView, movieViewId, ownedByRoute, seriesViewId, type Scope } from "./view-scope.js";

// Self-cleans on internal navigation; also needed on page unload so
// in-flight fetches don't outlive the document.
let detailAbort: AbortController | null = null;
registerCleanup(() => {
  detailAbort?.abort();
  detailAbort = null;
});

// --- Persistent two-tier render state (mirrors coverage.ts / files.ts) ---
//
// Each detail view registers its bindList binding in the scope view-scope.ts
// returns, so a coverage SSE refresh updates rows in place instead of
// rebuilding the table. A render REUSES the live binding while the host
// still holds that view; otherwise it mounts fresh, releasing everything
// the previous occupant registered.

let seriesColl: ReturnType<typeof createCollection<DetailRow>> | null = null;
let movieColl: ReturnType<typeof createCollection<MovieRow>> | null = null;

/** Mount a detail view: the content host releases the previous occupant,
 *  and the router owns this one too (a navigation away must release it). */
function mountDetailView(viewId: string): Scope {
  const scope = contentView.mount(viewId);
  ownedByRoute(scope);
  return scope;
}

/** Drop null entries so a children list can be spread into `replaceChildren`
 *  (which, unlike `el`, does not skip nulls). */
function compact(nodes: (HTMLElement | null)[]): HTMLElement[] {
  return nodes.filter((n): n is HTMLElement => n !== null);
}

/**
 * Index key for one (language, variant) pair. ONE helper for every producer
 * and consumer: the subtitle index is built from stored files' lang/variant
 * and looked up with the config target's lang/variant, so both ends must
 * encode identically.
 *
 * keyenc-encoded rather than pipe-joined: both fields come from the
 * operator's config.yaml (validated for non-emptiness only), so a value
 * carrying a separator could shift the field split and alias two targets.
 */
function langVariantKey(lang: string, variant: string): string {
  return join(lang, variant);
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
  // Anti-flicker skeleton (150ms show-delay + 300ms min-visible, abort-aware).
  // The commit always releases the pane and detaches current content before
  // rendering, so a series detail render never reuses a previous view's binding.
  const timing = skeletonTiming(
    () => {
      contentView.clear();
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
    mediaEpisodes(s.id, undefined, { signal }),
    coverageSeriesDetail(s.tvdb_id, { signal }),
    stateIDs({ type: "episode", prefix: `tvdb-${s.tvdb_id}-` }, { signal }),
  ])
    .then(([seasons, subFiles, historyIDs]) => {
      if (signal.aborted) {
        timing.cancel();
        return;
      }
      timing.commit(() => {
        contentView.clear();
        patch(out, document.createDocumentFragment()); // detach: mount guarantee
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
        contentView.clear();
        patch(out, errDiv(msg));
      });
    });
}

/** Count the season's syncable files (external subs on file-bearing
 *  episodes matching a configured target) — gates the season-head Sync
 *  button and hints the dialog's count; the server enumerates
 *  authoritatively at batch acceptance. */
function countSeasonSyncFiles(
  sg: SeasonGroup,
  series: SeriesItem,
  subIdx: Record<string, Partial<Record<string, SubtitleEntry[]>>>,
  targetLangs: { lang: string; variant: string }[],
): number {
  let count = 0;
  for (const ep of sg.episodes) {
    if (!ep.has_file) {
      continue;
    }
    const mediaId = tvdbMediaId(series.tvdb_id, sg.season, ep.episode);
    const subs = subIdx[mediaId] ?? {};
    for (const t of targetLangs) {
      const key = langVariantKey(t.lang, t.variant);
      const entries = subs[key];
      if (entries) {
        for (const sub of entries) {
          if (sub.source !== EMBEDDED_PROVIDER) {
            count++;
          }
        }
      }
    }
  }
  return count;
}

// --- Series row model (two-tier collection rows) ---
//
// Each row carries everything its mount/update needs, so the bindList
// specs never read render-scoped state that would go stale on a reuse
// refresh. `sig` is a content signature: update() compares it against the
// row's `data-sig` and short-circuits when unchanged.

type DetailRow =
  | { kind: "gap"; season: number }
  | {
      kind: "head";
      season: number;
      label: string;
      series: SeriesItem;
      syncFiles: number;
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

/** Content signature for an episode row: every field that drives the
 *  coverage cell plus history-button presence. Stable across a refresh that
 *  did not touch this episode.
 *
 *  Assembled with keyenc at every nesting level (rather than a separator per
 *  level) so a codec or source carrying a separator can't alias two
 *  different coverage states. A collision only skips a needed repaint
 *  (stale badge until the next change), never a wrong entity.
 *
 *  A single target with no entries encodes as one empty component, which
 *  keyenc hashes rather than let alias the no-component encoding — so an
 *  uncovered single-target row carries a `sha256:` block. */
function epSig(
  subs: Partial<Record<string, SubtitleEntry[]>>,
  targetLangs: { lang: string; variant: string }[],
  hasHistory: boolean,
  icSig: string,
): string {
  let parts: string;
  if (targetLangs.length > 0) {
    parts = join(
      ...targetLangs.map((t) => {
        const entries = subs[langVariantKey(t.lang, t.variant)] ?? [];
        return join(
          ...entries.map((e) =>
            join(e.source, e.codec ?? "", String(e.score ?? 0), String(e.ordinal ?? 0)),
          ),
        );
      }),
    );
  } else {
    parts = join("subs", String(Object.keys(subs).length));
  }
  return join(parts, hasHistory ? "1" : "0", icSig);
}

/** Ignored-codec component of every row signature, derived once per rebuild
 *  rather than sorted per row. */
function ignoredCodecsSig(): string {
  return join(...[...store.get("ignoredCodecs")].sort());
}

function makeColHeaders(): HTMLElement {
  return el(
    "tr",
    { className: "season-head", role: "row" },
    el("th", { role: "columnheader" }, "Ep"),
    el("th", { role: "columnheader" }, "Title"),
    el("th", { role: "columnheader" }, "Subtitles"),
    el("th", { role: "columnheader" }, ""),
  );
}

/** Coverage-cell children for an episode row. */
function episodeCoverageChildren(row: DetailEpRow): HTMLElement[] {
  const { subs, targetLangs } = row;
  if (targetLangs.length > 0) {
    return targetLangs.map((t) => {
      const key = langVariantKey(t.lang, t.variant);
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
    const key = langVariantKey(t.lang, t.variant);
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

/** Build a single episode table row with coverage badges and action
 *  buttons. The coverage cell and action group are separately addressable
 *  so update() can repaint just them. */
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
            role: "cell",
            "data-tip": `Absolute #${absEp}, aired ${fmtEpisode(season, airedEp)}`,
          },
          epLabel,
          el("span", { className: "ep-aired" }, ` E${pad(airedEp)}`),
        )
      : el("td", { className: "ep-num", role: "cell" }, epLabel);

  const covCell = el(
    "td",
    { className: "ep-coverage", role: "cell" },
    ...episodeCoverageChildren(row),
  );

  const tr = el(
    "tr",
    { role: "row" },
    epNumCell,
    el("td", { className: "ep-title", role: "cell", "data-ep": epLabel }, ep.title),
    covCell,
    el(
      "td",
      { role: "cell", "data-col": "actions" },
      el("div", { className: "action-group" }, ...episodeActionChildren(row)),
    ),
  );
  tr.dataset["sig"] = row.sig;
  return tr;
}

/** Repaint just the dynamic parts of an episode row. */
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

/** Action-group children for a season-head row. `scope` is the row's
 *  scope, fresh per paint, so this paint's scan button is released when
 *  the next paint discards it. */
function seasonHeadActionChildren(row: DetailHeadRow, scope: Scope): (HTMLElement | null)[] {
  const { series, season, syncFiles, hasHistory } = row;

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
  registerScanButton(searchBtn, scope);
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
    syncFiles > 0
      ? el(
          "button",
          {
            type: "button",
            className: "ghost",
            "data-tip": "Audio sync all subtitles in this season",
            onclick: () => {
              confirmSeasonSync(series.title, season, series.id, syncFiles);
            },
          },
          icon("sync"),
          el("span", { className: "btn-text" }, " Sync"),
        )
      : null;
  return [syncBtn, histBtn, searchBtn];
}

/** Build a season-head row. The label cell spans the data columns on
 *  desktop; the two empty cells exist only for mobile's flex layout. */
function buildSeasonHeadRow(row: DetailHeadRow, scope: Scope): HTMLElement {
  const tr = el(
    "tr",
    { className: "season-head", role: "row" },
    el("td", { role: "cell" }, row.label),
    el("td", { role: "cell" }),
    el("td", { role: "cell" }),
    el(
      "td",
      { role: "cell", "data-col": "actions" },
      el("div", { className: "action-group" }, ...seasonHeadActionChildren(row, scope)),
    ),
  );
  tr.dataset["sig"] = row.sig;
  return tr;
}

/** Repaint a season-head row's action buttons. */
function paintSeasonHead(node: HTMLElement, row: DetailHeadRow, scope: Scope): void {
  const actionGroup = node.querySelector('[data-col="actions"] .action-group');
  if (actionGroup) {
    actionGroup.replaceChildren(...compact(seasonHeadActionChildren(row, scope)));
  }
  node.dataset["sig"] = row.sig;
}

// bindList row lifecycle for the series table. Built once per mount, over
// that view's scope: closures are data-driven (read only the row argument)
// so a reuse setAll feeds fresh row objects with no stale render-scope capture.
function makeSeriesSpec(scope: Scope): ListSpec<DetailRow> {
  return {
    mount: (r, id) => {
      switch (r.kind) {
        case "gap":
          // Spans every column via grid-column: 1 / -1.
          return el("tr", { className: "season-gap", role: "row" }, el("td", { role: "cell" }));
        case "head":
          return buildSeasonHeadRow(r, scope.child(id));
        case "cols":
          return makeColHeaders();
        case "ep":
          return buildEpisodeRow(r);
      }
    },
    update: (node, r, id) => {
      switch (r.kind) {
        case "gap":
        case "cols":
          return; // static rows — never repaint
        case "head":
          if (node.dataset["sig"] === r.sig) {
            return;
          }
          paintSeasonHead(node, r, scope.child(id));
          return;
        case "ep":
          if (node.dataset["sig"] === r.sig) {
            return;
          }
          paintEpisodeRow(node, r);
          return;
      }
    },
    onRemove: (_node, id) => {
      scope.release(id);
    },
  };
}

/** Seasons with download history, derived in ONE pass over the history set
 *  (C3): the per-season `[...historySet].some(startsWith)` scan was a full
 *  re-walk of the set multiplied by the season count. Episode ids are
 *  `tvdb-{id}-s{NN}e{NN}` (tvdbMediaId), so the season number sits between
 *  the `s` and the first `e` after it. */
function seasonsWithHistory(tvdbId: number, historySet: Set<string>): Set<number> {
  const prefix = `tvdb-${tvdbId}-s`;
  const out = new Set<number>();
  for (const id of historySet) {
    if (!id.startsWith(prefix)) {
      continue;
    }
    const e = id.indexOf("e", prefix.length);
    if (e <= prefix.length) {
      continue;
    }
    const season = Number(id.slice(prefix.length, e));
    if (Number.isFinite(season)) {
      out.add(season);
    }
  }
  return out;
}

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
  const icSig = ignoredCodecsSig();
  const historySeasons = seasonsWithHistory(series.tvdb_id, historySet);
  let first = true;
  for (const sg of sortedSeasons) {
    if (!sg.episodes.some((ep) => ep.has_file)) {
      continue;
    }
    if (!first) {
      rows.push({ kind: "gap", season: sg.season });
    }
    first = false;
    const syncFiles = countSeasonSyncFiles(sg, series, subIdx, targetLangs);
    const hasHist = historySeasons.has(sg.season);
    rows.push({
      kind: "head",
      season: sg.season,
      label: sg.season === 0 ? "Specials" : `Season ${sg.season}`,
      series,
      syncFiles,
      hasHistory: hasHist,
      sig: `${syncFiles}:${hasHist}`,
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
        sig: epSig(subs, targetLangs, hasHistory, icSig),
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
    subIdx[mediaId] = Object.groupBy(files ?? [], (f) => langVariantKey(f.language, f.variant));
  }

  if (seasons.length === 0) {
    contentView.clear();
    const frag = document.createDocumentFragment();
    frag.appendChild(
      emptyState(
        "No episodes with video files were found for this series. Episodes appear here once Sonarr has imported them.",
      ),
    );
    patch(out, frag);
    return;
  }

  // Sort seasons: regular ascending, specials (season 0) last.
  const sortedSeasons = [...seasons].sort((a, b) => {
    if (a.season === 0) {
      return 1;
    }
    if (b.season === 0) {
      return -1;
    }
    return a.season - b.season;
  });

  // Detect absolute numbering that genuinely diverges from sequential aired
  // order (not just a running count across seasons).
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

  // REUSE: this series view is still the content host's occupant, so the
  // existing binding just gets fresh rows.
  const key = String(series.tvdb_id);
  const viewId = seriesViewId(key);
  if (contentView.scopeFor(viewId) !== null && seriesColl) {
    seriesColl.setAll(rows);
    return;
  }

  const scope = mountDetailView(viewId);
  const coll = createCollection<DetailRow>(detailRowKey);
  const tbody = el("tbody", { role: "rowgroup" });
  scope.add(bindList(tbody, coll, makeSeriesSpec(scope)));
  coll.setAll(rows);
  seriesColl = coll;
  scope.add(() => {
    seriesColl = null;
  });

  // Detach previous content BEFORE patching the fresh shell: patch reuses
  // position-matched elements, so patching over a live table would keep the
  // old tbody on screen instead of this binding's.
  patch(out, document.createDocumentFragment());

  // The desktop tree leaves the table formatting context, so roles are
  // explicit. Accessible name is #lib-heading, populated before this commits.
  const frag = document.createDocumentFragment();
  frag.appendChild(
    el(
      "table",
      {
        className: "series-detail",
        role: "table",
        "aria-labelledby": "lib-heading",
        "data-series-id": key,
      },
      tbody,
    ),
  );
  patch(out, frag);
}

// --- Movie detail drilldown ---

// One language-target row of the movie detail table.
interface MovieRow {
  key: string; // langVariantKey(language, variant)
  label: string;
  entries: SubtitleEntry[];
  lang: string; // target language code (for the Search popup)
  movie: MovieDetail;
  sig: string;
}

/** Content signature for a movie language row: entries' source/codec/score.
 *  Same nested-join assembly as epSig, same bounded collision cost. */
function movieSig(entries: SubtitleEntry[]): string {
  const ic = join(...[...store.get("ignoredCodecs")].sort());
  return join(join(...entries.map((e) => join(e.source, e.codec ?? "", String(e.score ?? 0)))), ic);
}

/** Coverage-cell children for a movie row. */
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

/** Build a movie language row. */
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

/** The movie detail's header. The buttons depending on subtitle rows (sync,
 *  Files) are added by renderMovieDetail once /subs rows are in hand. */
function configureMovieHeader(m: MovieDetail): void {
  setDocTitle(m.title);
  const targets = m.targets;
  const subsInfo =
    targets.length > 0 ? targets.map((t) => fmtLangVariant(t.language, t.variant)).join(", ") : "";
  const info = `${m.year} \u00B7 audio: ${langName(
    m.rule,
  )}${subsInfo ? ` \u00B7 subs: ${subsInfo}` : ""}`;
  emit(BusEvent.PanelConfigure, {
    visible: false,
    detail: {
      title: m.title,
      info,
      backPath: "/",
      arrLink: buildRadarrLink(m),
      arrName: "Radarr",
    },
  });
}

/** Add the History nav button (idempotent). */
function addMovieHistoryButton(m: MovieDetail): void {
  const headerEl = document.querySelector("#coveragePanel .card-head");
  if (!headerEl || headerEl.querySelector('[data-nav="hist"]')) {
    return;
  }
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

/** Enter the movie-detail view for a row already in hand. Called before
 *  reads (header is known from the cached row) so a refresh mid-read still
 *  classifies as this movie. */
function enterMovieDetail(m: MovieDetail): void {
  configureMovieHeader(m);
  store.set("detailCtx", { movie: true, tmdbId: m.tmdb_id });
}

/** The transaction page leg's movie entry: paint from the leg's own
 *  pre-fetched triple. The leg owns the three reads on the raw client, so
 *  commit waits for them and a failed read aborts the transaction instead
 *  of painting an empty subs table. */
export function renderMovieDetailFromLeg(m: MovieDetail, reads: MovieDetailReads): void {
  if (detailAbort) {
    detailAbort.abort();
    detailAbort = null;
  }
  enterMovieDetail(m);
  renderMovieDetail(m, reads);
}

export function openMovieDetail(m: MovieDetail, skipPush?: boolean, legSignal?: AbortSignal): void {
  if (detailAbort) {
    detailAbort.abort();
  }
  detailAbort = new AbortController();
  // legSignal (page-leg dispatcher's movie arm) means a route leave aborts
  // this open's /subs + stateIDs reads too.
  const signal = legSignal ? AbortSignal.any([detailAbort.signal, legSignal]) : detailAbort.signal;

  if (!skipPush) {
    history.pushState(null, "", `/movie/${m.tmdb_id}`);
  }
  enterMovieDetail(m);
  const out = $.coverageContent;

  const timing = skeletonTiming(
    () => {
      contentView.clear();
      const skel = document.createDocumentFragment();
      for (let i = 0; i < 3; i++) {
        skel.appendChild(
          el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
        );
      }
      patch(out, skel);
    },
    { minVisibleMs: 300, signal },
  );

  // Read under ONE signal so the skeleton covers the whole load; a
  // navigation has no commit to refuse, so an unanswered read paints empty
  // rather than latching a transaction (unlike the leg).
  readMovieDetail(m.tmdb_id, { signal })
    .then((r) => {
      if (signal.aborted) {
        timing.cancel();
        return;
      }
      timing.commit(() => {
        renderMovieDetail(m, r.reads);
      });
    })
    .catch((e: unknown) => {
      if (signal.aborted) {
        timing.cancel();
        return;
      }
      const msg = e instanceof Error ? e.message : String(e);
      timing.commit(() => {
        contentView.clear();
        patch(out, errDiv(msg));
      });
    });
}

/** The movie-detail render: paint the view from an already-read payload.
 *  Both callers (plain open, transaction leg) land here so the History
 *  button, sync/Files buttons, and table are settled in one place. */
function renderMovieDetail(m: MovieDetail, reads: MovieDetailReads): void {
  const { subs, historyIDs } = reads;
  const targets = m.targets;
  const out = $.coverageContent;

  if (historyIDs.length > 0) {
    addMovieHistoryButton(m);
  }

  // Collect all external subtitles for the sync button.
  const extSubs = subs.filter((s) => s.source !== EMBEDDED_PROVIDER);
  const headerEl = document.querySelector("#coveragePanel .card-head");
  if (headerEl) {
    // Replace, not append: a same-movie refresh re-runs this with fresh rows.
    headerEl.querySelector('[data-nav="sync"]')?.remove();    const firstExtSub = extSubs[0];
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

    headerEl.querySelector('[data-nav="files"]')?.remove();
    if (extSubs.length > 0 && store.get("isAdmin")) {
      const filesBtn = el(
        "button",
        {
          type: "button",
          className: "ghost",
          "data-nav": "files",
          "data-tip": "Manage subtitle files",
          onclick: () => {
            openFileManager("movie", `tmdb-${m.tmdb_id}`, m.title, `/movie/${m.tmdb_id}`, m.id);
          },
        },
        icon("file"),
        el("span", { className: "btn-text" }, " Files"),
      );
      insertNavButton(filesBtn);
    }
  }

  if (targets.length === 0) {
    contentView.clear();
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

  const subIdx = Object.groupBy(subs, (s) => langVariantKey(s.language, s.variant));
  const rows: MovieRow[] = targets.map((t) => {
    const key = langVariantKey(t.language, t.variant);
    const entries = (subIdx as Record<string, SubtitleEntry[] | undefined>)[key] ?? [];
    const label = langName(t.language) + (t.variant !== DEFAULT_VARIANT ? ` (${t.variant})` : "");
    return { key, label, entries, lang: t.language, movie: m, sig: movieSig(entries) };
  });

  // REUSE: this movie view is still the content host's occupant.
  const mKey = String(m.tmdb_id);
  const viewId = movieViewId(mKey);
  if (contentView.scopeFor(viewId) !== null && movieColl) {
    movieColl.setAll(rows);
    return;
  }

  const scope = mountDetailView(viewId);
  const coll = createCollection<MovieRow>((r) => r.key);
  const tbody = el("tbody");
  scope.add(bindList(tbody, coll, movieSpec));
  coll.setAll(rows);
  movieColl = coll;
  scope.add(() => {
    movieColl = null;
  });

  // Detach previous content before patching the fresh shell: patch keys
  // tables by data-movie-id, so a same-movie mount over a live table would
  // keep the old tbody instead of this binding's.
  patch(out, document.createDocumentFragment());

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
