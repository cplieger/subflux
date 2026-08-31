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
import { join } from "@cplieger/keyenc";
import { PATH_SYNC_AUDIO, PATH_SYNC_OFFSET } from "./wire/client.gen.js";
import { decodeSyncAccepted } from "./wire/decoders.gen.js";
import type { SyncAccepted, SyncAudioRequest, SyncOffsetRequest } from "./wire/types.gen.js";
import { refKey } from "./file-ref.js";

// Both dedupe keys nest `refKey(args)`, which is itself a keyenc value, so the
// OUTER key is a `join` too: nesting is composition, and a raw
// `sync.audio:${refKey(args)}` would reintroduce the forgeable shape one level
// up (an inner value may legitimately carry escaped separators, and the outer
// join must still treat the whole thing as ONE component).
//
// These keys never leave the browser. They live in the actions framework's
// in-flight dedupe map only: the wire `Idempotency-Key` header is populated
// from a definition's separate `idempotencyKey` field, which subflux sets on no
// action, and the Go server has no Idempotency-Key middleware to read one.

/** Dispatch one async audio-sync job: an instant 202 {activity_id, job_id};
 *  the analysis result arrives via the sync:done event matched on job_id.
 *  Carries NO retry: the server's capacity refusal is a 429, which the
 *  framework's network classifier would auto-retry — the refusal must stay
 *  visible (the dialog renders it inline) and must cost exactly one request.
 *  dedupe by FileRef so a second click collapses onto the first dispatch
 *  (the server's same-file dedupe answers the same ids anyway). */
export const audioSyncAction = apiAction<SyncAudioRequest, SyncAccepted>({
  name: "sync.audio",
  request: (args) => ({ method: "POST", path: PATH_SYNC_AUDIO, body: args }),
  decode: (data) => decodeSyncAccepted(data),
  dedupe: (args) => join("sync.audio", refKey(args)),
  error: false, // callers handle inline result UI; toast would be redundant
});

/** Save a manually-entered offset. Idempotent server-side (overwrites any
 *  previous offset for the file). dedupe protects against double-click. */
export const saveManualOffsetAction = apiAction<SyncOffsetRequest>({
  name: "sync.save_offset",
  request: (args) => ({ method: "POST", path: PATH_SYNC_OFFSET, body: args }),
  dedupe: (args) => join("sync.save_offset", refKey(args), String(args.offset_ms)),
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: "Save failed",
});
