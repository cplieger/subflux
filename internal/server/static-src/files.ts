// files.ts — subtitle file manager page with delete controls.
//
// Two-tier reactive model (mirrors coverage.ts/history.ts): external files
// live in a `createCollection` keyed by their unique on-disk `path`; the table
// is rendered once via `bindList` over a computed `sortedIds` view (episode
// order for series, language for movies). Optimistic delete removes one entry
// (rollback re-upserts it); bulk delete clears the collection; a refresh is
// `setAll`. Keying on `path` is what fixes the multiple-files-per-language
// collision the old `${media_id}-${language}-${source}` key silently dropped.

import * as notify from "./notify.js";
import * as store from "./store.js";
import { $, el, icon, emptyDiv, errDiv, confirm } from "./dom.js";
import { apiGet } from "./api-client.js";
import { apiAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import { fmtEpisode, langName } from "./utils.js";
import { DEFAULT_VARIANT } from "./constants.js";
import { emit, BusEvent } from "./bus.js";
import { openSyncDialog } from "./sync.js";
import type { MediaType } from "./api-types.js";
import { computed, effect, createCollection, bindList, patch } from "@cplieger/reactive";

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

// External files only — embedded tracks (no path) are never shown or deletable
// here, so the table and the collection both deal exclusively with externals.
// `path` is unique per external file, so it is a collision-free collection key.
const fileKey = (f: FileEntry): string => f.path;
const files = createCollection<FileEntry>(fileKey);

let currentMediaType: MediaType | "" = "";
let currentMediaID = "";
let currentTitle = "";
let currentBackPath = "/";
/** Sonarr/Radarr internal numeric ID for poster lookups. */
let currentArrId = 0;
/** media_id or '' → video path */
let videoPaths = new Map<string, string>();

/** Parse season/episode from a series media_id (`...s01e02`). */
function parseEp(mediaID: string): { season: number; episode: number } {
  const m = /s(\d+)e(\d+)$/.exec(mediaID);
  if (!m) {
    return { season: 0, episode: 0 };
  }
  return { season: Number(m[1]), episode: Number(m[2]) };
}

/** Stable sort: episode order for series, language for movies. The trailing
 *  `path` tiebreaker gives a deterministic order for the multiple-files-per-
 *  language case (e.g. movie.fr.srt vs movie.fr.1.srt). */
function compareFiles(a: FileEntry, b: FileEntry): number {
  const ea = parseEp(a.media_id);
  const eb = parseEp(b.media_id);
  if (currentMediaType === "episode") {
    return (
      ea.season - eb.season ||
      ea.episode - eb.episode ||
      a.language.localeCompare(b.language) ||
      a.path.localeCompare(b.path)
    );
  }
  return a.language.localeCompare(b.language) || a.path.localeCompare(b.path);
}

// Sorted id list — the structure tier `bindList` renders. Shallow-equal so a
// no-op refresh (same set/order) does not trigger a structural reconcile.
const sortedIds = computed<readonly string[]>(
  () => [...files.items()].sort(compareFiles).map(fileKey),
  { equals: (a, b) => a.length === b.length && a.every((x, i) => x === b[i]) },
);

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

  emit(BusEvent.PanelConfigure, {
    visible: false,
    detail: {
      title: currentTitle,
      info: "Subtitle Files",
      backPath: currentBackPath,
    },
  });

  // Mark as files view so data:invalidate reloads files, not library.
  store.set("detailCtx", { files: true });

  // Design-system skeleton (matching the library/history tables) instead of
  // the one-off centered spinner — one loading pattern per concern.
  const skel = document.createDocumentFragment();
  for (let i = 0; i < 4; i++) {
    skel.appendChild(
      el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
    );
  }
  patch(out, skel);

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
    disposeBindings();
    patch(out, errDiv("Failed to load files"));
    return;
  }
  files.setAll(data.filter((f: FileEntry) => f.source === "external"));
  ensureMounted();
}

function buildFileRow(f: FileEntry): HTMLElement {
  const isSeries = currentMediaType === "episode";
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

// --- Render: build the table shell once, bind the tbody, react for the rest ---

let bindings: (() => void)[] = [];

function disposeBindings(): void {
  for (const dispose of bindings) {
    dispose();
  }
  bindings = [];
}

function ensureMounted(): void {
  const out = $.coverageContent;
  // Already mounted and still in the DOM (navigation away replaces the
  // container, so re-mount when the table is gone).
  if (out.querySelector("table.files-table") !== null) {
    return;
  }
  disposeBindings();

  const isSeries = currentMediaType === "episode";

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
  const tbl = el("table", { className: "files-table" }, thead, tbody);
  const emptyEl = emptyDiv("No external subtitles.");
  patch(out, el("div", { className: "files-list" }, emptyEl, tbl));

  // Structure tier: reconcile rows keyed by path on add/remove/reorder.
  bindings.push(
    bindList(
      tbody,
      { ids: sortedIds, signalFor: (id: string) => files.signalFor(id) },
      { mount: (f) => buildFileRow(f) },
    ),
  );

  // "Delete all" button in the panel header (sibling of the back/arr nav).
  // Built once here; its count + visibility track the collection reactively.
  const bulkText = el("span", { className: "btn-text" });
  const bulkBtn = el(
    "button",
    {
      type: "button",
      className: "ghost btn-delete",
      "data-nav": "bulk-delete",
      "data-tip": "Delete all external subtitle files",
      onclick: () => {
        void bulkDelete();
      },
    },
    icon("trash"),
    bulkText,
  );
  const headerEl = document.querySelector("#coveragePanel .card-head");
  if (headerEl) {
    headerEl.querySelector('[data-nav="bulk-delete"]')?.remove();
    headerEl.appendChild(bulkBtn);
  }

  // Empty-state, table, and bulk-button visibility/count derived from the
  // collection's id list.
  bindings.push(
    effect(() => {
      const n = files.ids.value.length;
      emptyEl.hidden = n > 0;
      tbl.hidden = n === 0;
      bulkBtn.hidden = n === 0;
      bulkText.textContent = ` Delete all (${n})`;
    }),
  );
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
  entry: FileEntry;
}

/** Single-file delete with optimistic remove + rollback restore on failure.
 *  The sorted view re-places a restored entry at its deterministic position,
 *  so no index bookkeeping is needed. dedupe protects against double-click. */
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
    if (!files.has(f.path)) {
      return undefined;
    }
    files.remove(f.path);
    return { entry: f };
  },
  rollback: (_args, op) => {
    if (op === undefined) {
      return;
    }
    files.upsert(op.entry);
  },
  dedupe: (f) => `files.delete:${f.path}:${f.variant}:${f.language}`,
  success: "File deleted",
  error: "Delete failed",
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
});

async function bulkDelete(): Promise<void> {
  const extCount = files.size;
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

/** Bulk delete optimistically clears the collection with rollback
 *  restoration. The custom `success` is set to false so the dispatch
 *  wrapper above can show the count from the response payload. */
const bulkDeleteAction = apiAction<BulkDeleteArgs, BulkDeleteResponse, BulkDeleteOp>({
  name: "files.delete_bulk",
  request: (args) => ({ method: "DELETE", path: "/api/files/bulk", body: args }),
  optimistic: (): BulkDeleteOp => {
    const externals = files.items();
    files.clear();
    return { externals };
  },
  rollback: (_args, op) => {
    if (op === undefined) {
      return;
    }
    // Restore externals; the next refreshFileData() canonicalises the list.
    files.setAll(op.externals);
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
