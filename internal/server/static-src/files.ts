// files.ts — subtitle file manager page with delete controls

import * as notify from "./notify.js";
import * as store from "./store.js";
import { $, el, icon, patch, emptyDiv, errDiv, confirm } from "./dom.js";
import { apiGet } from "./api-client.js";
import { apiAction, retryNetwork, RETRY_STANDARD } from "./actions/index.js";
import { fmtEpisode, langName } from "./utils.js";
import { DEFAULT_VARIANT } from "./constants.js";
import { emit, BusEvent } from "./bus.js";
import { openSyncDialog } from "./sync.js";
import type { MediaType } from "./api-types.js";
import { reconcile } from "./lib/reactive/reconcile.js";

// --- API response shapes ---

interface FileEntry {
  source: string;
  media_id: string;
  language: string;
  variant: string;
  codec: string;
  path: string;
  offset_ms: number;
  size: number;
}

interface BulkDeleteResponse {
  deleted: number;
}

let filesData: FileEntry[] = [];
let currentMediaType: MediaType | "" = "";
let currentMediaID = "";
let currentTitle = "";
let currentBackPath = "/";
/** Sonarr/Radarr internal numeric ID for poster lookups. */
let currentArrId = 0;
/** media_id or '' → video path */
let videoPaths = new Map<string, string>();

/**
 * Open the file manager for a media item.
 */
export function openFileManager(
  mediaType: MediaType,
  mediaID: string,
  title: string,
  backPath: string,
  paths?: Map<string, string>,
  arrId?: number,
): void {
  currentMediaType = mediaType;
  currentMediaID = mediaID;
  currentTitle = title;
  currentBackPath = backPath || "/";
  currentArrId = arrId ?? 0;
  videoPaths = paths ?? new Map<string, string>();

  const path =
    mediaType === "movie"
      ? `/movie/${mediaID.replace("tmdb-", "")}/files`
      : `/series/${mediaID.replace(/^tvdb-/, "").replace(/-$/, "")}/files`;
  if (location.pathname !== path) {
    history.pushState(null, "", path);
  }

  store.batch(() => {
    store.set("currentPage", "files");
    store.set("detailCtx", null);
  });
  void loadFiles();
}

async function loadFiles(): Promise<void> {
  const out = $.coverageContent;

  emit(BusEvent.PanelConfigure, false, {
    title: currentTitle,
    info: "Subtitle Files",
    backPath: currentBackPath,
  });

  // Mark as files view so needsRefresh reloads files, not library.
  store.set("detailCtx", { files: true });

  patch(
    out,
    el("div", { className: "empty" }, el("span", { className: "spinner" }), " Loading files\u2026"),
  );

  await refreshFileData();
}

async function refreshFileData(): Promise<void> {
  const out = $.coverageContent;
  const params = new URLSearchParams({
    media_type: currentMediaType,
    media_id: currentMediaID,
  });
  const data = await apiGet<FileEntry[] | null>(`/api/files?${params}`);
  if (data === null) {
    patch(out, errDiv("Failed to load files"));
    return;
  }
  filesData = data;
  renderFiles();
}

function renderFiles(): void {
  const out = $.coverageContent;

  let data = filesData.filter((f: FileEntry) => f.source === "external");

  const isSeries = currentMediaType === "episode";

  // Parse season/episode from media_id for series.
  function parseEp(mediaID: string): { season: number; episode: number } {
    const m = /s(\d+)e(\d+)$/.exec(mediaID);
    if (!m) {
      return { season: 0, episode: 0 };
    }
    return { season: Number(m[1]), episode: Number(m[2]) };
  }

  // Sort: episode order for series, language for movies.
  const cmp = (a: FileEntry, b: FileEntry): number => {
    const ea = parseEp(a.media_id);
    const eb = parseEp(b.media_id);
    if (isSeries) {
      return (
        ea.season - eb.season || ea.episode - eb.episode || a.language.localeCompare(b.language)
      );
    }
    return a.language.localeCompare(b.language) || a.source.localeCompare(b.source);
  };
  data = [...data].sort(cmp);

  const frag = document.createDocumentFragment();

  // Add delete all button to the header row (next to back button).
  const headerEl = document.querySelector("#coveragePanel .card-head");
  if (headerEl) {
    const oldBulk = headerEl.querySelector('[data-nav="bulk-delete"]');
    if (oldBulk) {
      oldBulk.remove();
    }
    const extCount = filesData.filter((f: FileEntry) => f.source === "external").length;
    if (extCount > 0) {
      const bulkBtn = el(
        "button",
        {
          type: "button",
          className: "ghost btn-delete",
          "data-nav": "bulk-delete",
          "data-tip": "Delete all external subtitle files",
          onclick: () => bulkDelete(),
        },
        icon("trash"),
        el("span", { className: "btn-text" }, ` Delete all (${extCount})`),
      );
      headerEl.appendChild(bulkBtn);
    }
  }

  if (data.length === 0) {
    frag.appendChild(emptyDiv("No external subtitles."));
    patch(out, frag);
    return;
  }

  // Table.
  const headerCols: HTMLElement[] = [];
  if (isSeries) {
    headerCols.push(el("th", null, "Episode"));
  }
  headerCols.push(
    el("th", null, "Language"),
    el("th", null, "Format"),
    el("th", null, "Offset"),
    el("th", null, "Size"),
    el("th", null, ""),
  );
  const thead = el("thead", null, el("tr", null, ...headerCols));
  const tbody = el("tbody");

  reconcile(tbody, data, {
    key: (f) => `${f.media_id}-${f.language}-${f.source}`,
    mount: (f) => buildFileRow(f, isSeries, parseEp),
  });

  frag.appendChild(el("table", { className: "files-table" }, thead, tbody));
  patch(out, frag);
}

function buildFileRow(
  f: FileEntry,
  isSeries: boolean,
  parseEp: (id: string) => { season: number; episode: number },
): HTMLElement {
  const lang = langName(f.language) + (f.variant !== DEFAULT_VARIANT ? ` (${f.variant})` : "");
  let codec = f.codec || "";
  if (!codec && f.path) {
    const parts = f.path.split(".");
    const ext = (parts[parts.length - 1] ?? "").toLowerCase();
    const extMap: Record<string, string> = {
      srt: "srt",
      ass: "ass",
      ssa: "ssa",
      sub: "sub",
      vtt: "vtt",
    };
    codec = extMap[ext] ?? ext;
  }
  const formatLabel = codec ? `${codec}: ext` : "ext";
  const offset = f.offset_ms
    ? `${f.offset_ms > 0 ? "+" : ""}${(f.offset_ms / 1000).toFixed(1)}s`
    : "0.0s";
  const size = f.size ? formatSize(f.size) : "";
  const ep = parseEp(f.media_id);

  const actionGroup = el("div", { className: "action-group" });
  if (f.source === "external" && f.path) {
    const vp = videoPaths.get(f.media_id) ?? videoPaths.get("") ?? "";
    if (vp) {
      actionGroup.appendChild(
        el(
          "button",
          {
            type: "button",
            className: "ghost",
            "data-tip": "Adjust subtitle timing",
            onclick: (e: MouseEvent) => {
              e.stopPropagation();
              openSyncDialog(
                [f],
                vp,
                isSeries ? "series" : (currentMediaType as MediaType),
                currentArrId,
                isSeries ? fmtEpisode(ep.season, ep.episode) : currentTitle,
              );
            },
          },
          icon("sync"),
          el("span", { className: "btn-text" }, " Sync"),
        ),
      );
    }
    actionGroup.appendChild(
      el(
        "button",
        {
          type: "button",
          className: "ghost btn-delete",
          "data-tip": "Delete this file",
          onclick: (e: MouseEvent) => {
            e.stopPropagation();
            void deleteFile(f);
          },
        },
        icon("trash"),
        el("span", { className: "btn-text" }, " Delete"),
      ),
    );
  }

  const rowCols: HTMLElement[] = [];
  if (isSeries) {
    rowCols.push(el("td", null, fmtEpisode(ep.season, ep.episode)));
  }
  const formatBadge = el("span", { className: "badge", "data-status": "ok" }, formatLabel);
  rowCols.push(
    el("td", null, lang),
    el("td", null, formatBadge),
    el("td", { "data-col": "offset" }, offset),
    el("td", { "data-col": "size" }, size),
    el("td", { "data-col": "actions" }, actionGroup),
  );

  return el("tr", null, ...rowCols);
}

async function deleteFile(f: FileEntry): Promise<void> {
  if (!f.path || f.path.includes("..")) {
    notify.error("Invalid file path");
    return;
  }
  const parts = f.path.split("/");
  const fileName = parts[parts.length - 1] ?? f.path;
  const ok = await confirm("Delete File", `Delete "${fileName}"? This cannot be undone.`, "Delete");
  if (!ok) {
    return;
  }

  await deleteFileAction.dispatch(f);
}

interface DeleteFileOp {
  index: number;
  entry: FileEntry;
}

/** Single-file delete with optimistic remove + rollback restore at the
 *  original index on failure. dedupe protects against rapid double-click. */
const deleteFileAction = apiAction<FileEntry, unknown, DeleteFileOp>({
  name: "files.delete",
  request: (f) => {
    const params = new URLSearchParams({
      path: f.path,
      media_type: currentMediaType,
      media_id: f.media_id,
      language: f.language,
      variant: f.variant,
    });
    return { method: "DELETE", path: `/api/files?${params.toString()}` };
  },
  optimistic: (f): DeleteFileOp | undefined => {
    const index = filesData.indexOf(f);
    if (index === -1) {
      return undefined;
    }
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- index validated above
    const entry = filesData[index]!;
    filesData.splice(index, 1);
    renderFiles();
    return { index, entry };
  },
  rollback: (_args, op) => {
    if (op === undefined) {
      return;
    }
    // Restore at original index so the table order survives the rollback.
    const safeIndex = Math.min(op.index, filesData.length);
    filesData.splice(safeIndex, 0, op.entry);
    renderFiles();
  },
  dedupe: (f) => `files.delete:${f.path}:${f.variant}:${f.language}`,
  success: "File deleted",
  error: "Delete failed",
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
});

async function bulkDelete(): Promise<void> {
  const extCount = filesData.filter((f: FileEntry) => f.source === "external").length;
  const ok = await confirm(
    "Delete All External Subtitles",
    `Delete all ${extCount} external subtitle file(s)? This cannot be undone.`,
    "Delete All",
  );
  if (!ok) {
    return;
  }

  const r = await bulkDeleteAction.dispatch({
    media_type: currentMediaType,
    media_id: currentMediaID,
  });
  if (r !== null) {
    notify.success(`Deleted ${r.deleted} file(s)`);
    void refreshFileData();
  }
}

interface BulkDeleteArgs {
  media_type: string;
  media_id: string;
}
interface BulkDeleteOp {
  externals: FileEntry[];
}

/** Bulk delete optimistically removes all external entries with rollback
 *  restoration. The custom `success` is set to false so the dispatch
 *  wrapper above can show the count from the response payload. */
const bulkDeleteAction = apiAction<BulkDeleteArgs, BulkDeleteResponse, BulkDeleteOp>({
  name: "files.delete_bulk",
  request: (args) => ({ method: "DELETE", path: "/api/files/bulk", body: args }),
  optimistic: (): BulkDeleteOp => {
    const externals = filesData.filter((f) => f.source === "external");
    filesData = filesData.filter((f) => f.source !== "external");
    renderFiles();
    return { externals };
  },
  rollback: (_args, op) => {
    if (op === undefined) {
      return;
    }
    // Restore externals; order doesn't strictly need to be preserved here
    // because the next refreshFileData() will canonicalise the list.
    filesData = [...filesData, ...op.externals];
    renderFiles();
  },
  dedupe: true,
  success: false, // dispatch wrapper handles the count-aware message
  error: "Bulk delete failed",
  retryable: retryNetwork,
});

function formatSize(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
