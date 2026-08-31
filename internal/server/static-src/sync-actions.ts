// sync-actions.ts — shared action defs for the subtitle sync flows.
//
// One caller (sync.ts: the single-subtitle sync dialog and the season batch
// dialog), one home: the defs stay here so every dispatch site gets
// identical retry/dedupe/error semantics without duplicating request shapes.
//
// S7 addressing: the sync verbs carry a FileRef (media_type, media_id,
// language, variant, source, ordinal) — never a filesystem path. The server
// resolves the subtitle path from the store row and the video path from the
// same media. The season verb carries only {series_id, season}: the server
// enumerates the files itself (D2).

import { apiAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import { join } from "@cplieger/keyenc";
import { PATH_SYNC_AUDIO, PATH_SYNC_OFFSET, PATH_SYNC_SEASON } from "./wire/client.gen.js";
import { decodeSeasonSyncAccepted, decodeSyncAccepted } from "./wire/decoders.gen.js";
import type {
  SeasonSyncAccepted,
  SyncAccepted,
  SyncAudioRequest,
  SyncOffsetRequest,
  SyncSeasonRequest,
} from "./wire/types.gen.js";
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

/** Dispatch one server-owned season sync batch: an instant 202
 *  {activity_id}; the server enumerates the season's files itself, so this
 *  is the season UI's ONE request — no client fan-out. Carries NO retry for
 *  the same reason as sync.audio (the 429 cap refusal renders inline and
 *  must cost exactly one request); dedupe collapses a double-click, and the
 *  server's same-scope idempotency answers the live batch's id anyway. */
export const seasonSyncAction = apiAction<SyncSeasonRequest, SeasonSyncAccepted>({
  name: "sync.season",
  request: (args) => ({ method: "POST", path: PATH_SYNC_SEASON, body: args }),
  decode: (data) => decodeSeasonSyncAccepted(data),
  dedupe: (args) => join("sync.season", String(args.series_id), String(args.season)),
  error: false, // the dialog renders refusals inline
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
