// Reactive state container — backed by shared lib/reactive/store.
// API is unchanged: get, set, batch, subscribe, effect, computed.

import type { CoverageItem, SeriesItem, SeasonGroup } from './api-types.js';
import { createStore } from './lib/reactive/store.js';

// --- Typed store key registry ---

export interface ParsedConfig {
  configured?: boolean;
  languages?: string[];
  providers?: Record<string, unknown>;
  ignored_codecs?: string[];
  language_rules?: unknown;
  radarr_url?: string;
  sonarr_url?: string;
}

interface SeriesContext {
  series: SeriesItem;
  seasons: SeasonGroup[];
  tvdbId: number;
  files?: boolean;
}

interface MovieContext {
  movie: boolean;
  tmdbId: number;
}

interface FilesContext {
  files: boolean;
}

type DetailCtx = SeriesContext | MovieContext | FilesContext | null;

export interface StoreMap {
  config: ParsedConfig | null;
  configChecked: boolean;
  ignoredCodecs: Set<string>;
  detailCtx: DetailCtx;
  coverageData: CoverageItem[] | null;
  currentPage: string;
  scanInFlight: boolean;
  refreshPending: boolean;
  needsRefresh: boolean;
  isUnconfigured: boolean;
  isReady: boolean;
}

const store = createStore<StoreMap>();

export const get = store.get;
export const set = store.set;
export const batch = store.batch;
export const subscribe = store.subscribe;
export const effect = store.effect;
export const computed = store.computed;
