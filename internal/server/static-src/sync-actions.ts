// sync-actions.ts — shared action defs for the subtitle sync flows.
//
// Two callers:
//   - sync.ts: single-subtitle sync dialog (audio sync + manual offset save)
//   - detail-season-sync.ts: bulk season sync dialog (per-episode audio sync
//     in a worker pool)
//
// Centralising the action defs here means both dispatch sites get identical
// retry/dedupe/error semantics without duplicating the request shape.
//
// S7 addressing: the sync verbs carry a FileRef (media_type, media_id,
// language, variant, source, ordinal) — never a filesystem path. The server
// resolves the subtitle path from the store row and the video path from the
// same media.

import { apiAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import { PATH_SYNC_AUDIO, PATH_SYNC_OFFSET } from "./wire/client.gen.js";
import { decodeSyncAudioResponse } from "./wire/decoders.gen.js";
import type { SyncAudioRequest, SyncOffsetRequest } from "./wire/types.gen.js";
import type { SyncAudioResponse } from "./api-types.js";
import { refKey } from "./file-ref.js";

/** Audio-sync analysis. Long-running (FFmpeg + correlation), retryable on
 *  network failures. dedupe by FileRef so a second click on the same row
 *  collapses onto the first dispatch instead of running twice. */
export const audioSyncAction = apiAction<SyncAudioRequest, SyncAudioResponse>({
  name: "sync.audio",
  request: (args) => ({ method: "POST", path: PATH_SYNC_AUDIO, body: args }),
  decode: (data) => decodeSyncAudioResponse(data),
  dedupe: (args) => `sync.audio:${refKey(args)}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: false, // callers handle inline result UI; toast would be redundant
});

/** Save a manually-entered offset. Idempotent server-side (overwrites any
 *  previous offset for the file). dedupe protects against double-click. */
export const saveManualOffsetAction = apiAction<SyncOffsetRequest>({
  name: "sync.save_offset",
  request: (args) => ({ method: "POST", path: PATH_SYNC_OFFSET, body: args }),
  dedupe: (args) => `sync.save_offset:${refKey(args)}:${args.offset_ms}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: "Save failed",
});
