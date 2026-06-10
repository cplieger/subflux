// Shared API response types used by multiple modules.
// Wire types are generated from Go structs — see wire/types.gen.ts.
// This file re-exports them and adds client-only narrowed types.

export type {
  MediaType,
  ActivityEntry,
  CoverageTarget,
  KeyGenerated,
  MeResponse,
  MovieItem,
  ProviderSchema,
  SchemaField,
  SchemaSection,
  SeriesItem,
  SubtitleEntry,
} from "./wire/types.gen.js";

import type { MovieItem, CoverageTarget, SubtitleEntry } from "./wire/types.gen.js";

// --- Client-only types ---

/** CoverageItem is the client-side merged view of series and movie coverage.
 *  All fields from both SeriesItem and MovieItem are present; fields unique
 *  to one variant are optional. The _type discriminant identifies the source. */
export interface CoverageItem {
  _type: "series" | "movie";
  id: number;
  title: string;
  year: number;
  rule: string;
  audio_lang: string;
  targets: CoverageTarget[];
  // Series-only
  tvdb_id?: number;
  episodes?: number;
  first_aired?: string;
  // Movie-only
  tmdb_id?: number;
  has_file?: boolean;
  path?: string;
  scene_name?: string;
  in_cinemas?: string;
  digital_release?: string;
  subs?: SubtitleEntry[];
  // Shared optional
  imdb_id?: string;
  tags?: number[];
  excluded?: boolean;
}

/** Narrowed MovieItem with required id+title for detail views and bus events. */
export type MovieDetail = MovieItem & { id: number; title: string };

/** Audio sync response from POST /api/sync. */
export interface AudioSyncResponse {
  readonly applied: boolean;
  readonly offset_ms: number;
  readonly confidence: number;
}

/** Episode detail for series drill-down. */
export interface Episode {
  episode: number;
  absolute_episode?: number;
  title?: string;
  has_file: boolean;
  path?: string;
}

/** Season group for series drill-down. */
export interface SeasonGroup {
  season: number;
  episodes?: Episode[];
}
