// file-ref.ts — client-side FileRef helpers (S7 typed addressing).
//
// A FileRef (media_type, media_id, language, variant, source, ordinal)
// addresses exactly one stored subtitle file; the server resolves the
// filesystem path from the store. This is a leaf module (no framework
// imports) so view modules and action defs can share it freely.

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

/** Stable string identity for a FileRef (dedupe + collection keys). */
export function refKey(ref: FileRefArgs): string {
  return `${ref.media_type}|${ref.media_id}|${ref.language}|${ref.variant ?? "standard"}|${
    ref.source ?? "external"
  }|${ref.ordinal ?? 0}`;
}
