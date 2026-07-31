// file-ref.ts — client-side FileRef helpers (S7 typed addressing).
//
// A FileRef (media_type, media_id, language, variant, source, ordinal)
// addresses exactly one stored subtitle file; the server resolves the
// filesystem path from the store. This is a leaf module (no framework
// imports) so view modules and action defs can share it freely.

import { join } from "@cplieger/keyenc";
import type { MediaType, SubtitleEntry } from "./api-types.js";

/** FileRef fields shared by every file-addressed request. */
export interface FileRefArgs {
  media_type: MediaType;
  media_id: string;
  language: string;
  variant?: string;
  source?: string;
  ordinal?: number;
}

/** Build the FileRef for a listed subtitle entry. */
export function subtitleRef(mediaType: MediaType, sub: SubtitleEntry): FileRefArgs {
  return {
    media_type: mediaType,
    media_id: sub.media_id,
    language: sub.language,
    variant: sub.variant,
    source: sub.source,
    ordinal: sub.ordinal ?? 0,
  };
}

/**
 * Stable string identity for a FileRef (dedupe + collection keys).
 *
 * keyenc-encoded rather than pipe-joined: `language` arrives verbatim from the
 * operator's config.yaml (the language rules are validated for non-emptiness
 * only), so a code carrying the separator used to shift the field split and
 * make two different FileRefs share one key. `join` escapes the separator
 * inside each component instead, so no field's content can forge another
 * field's boundary.
 *
 * The separator moved from `|` to `:`, so these bytes changed. That costs
 * nothing: this key is in-memory only — a `createCollection` key and a lookup
 * in the actions framework's in-flight dedupe map — never persisted and never
 * sent to the server (the wire carries the FileRef fields themselves).
 */
export function refKey(ref: FileRefArgs): string {
  return join(
    ref.media_type,
    ref.media_id,
    ref.language,
    ref.variant ?? "standard",
    ref.source ?? "external",
    String(ref.ordinal ?? 0),
  );
}
