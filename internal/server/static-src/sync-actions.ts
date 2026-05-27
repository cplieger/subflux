// sync-actions.ts — shared action defs for the subtitle sync flows.
//
// Two callers:
//   - sync.ts: single-subtitle sync dialog (audio sync + manual offset save)
//   - detail-season-sync.ts: bulk season sync dialog (per-episode audio sync
//     in a worker pool)
//
// Centralising the action defs here means both dispatch sites get identical
// retry/dedupe/error semantics without duplicating the request shape.

import { apiAction, retryNetwork, RETRY_STANDARD } from "./actions/index.js";
import type { AudioSyncResponse } from "./api-types.js";

export interface AudioSyncArgs {
  subtitle_path: string;
  video_path: string;
  dry_run?: boolean;
}

/** Audio-sync analysis. Long-running (FFmpeg + correlation), retryable on
 *  network failures. dedupe by subtitle path so a second click on the same
 *  row collapses onto the first dispatch instead of running twice. */
export const audioSyncAction = apiAction<AudioSyncArgs, AudioSyncResponse>({
  name: "sync.audio",
  request: (args) => ({ method: "POST", path: "/api/sync/audio", body: args }),
  dedupe: (args) => `sync.audio:${args.subtitle_path}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: false, // callers handle inline result UI; toast would be redundant
});

export interface ManualOffsetArgs {
  subtitle_path: string;
  offset_ms: number;
}

/** Save a manually-entered offset. Idempotent server-side (overwrites any
 *  previous offset for the file). dedupe protects against double-click. */
export const saveManualOffsetAction = apiAction<ManualOffsetArgs>({
  name: "sync.save_offset",
  request: (args) => ({ method: "POST", path: "/api/sync/offset", body: args }),
  dedupe: (args) => `sync.save_offset:${args.subtitle_path}:${args.offset_ms}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: "Save failed",
});
