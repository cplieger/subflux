// Reactive state container — backed by @cplieger/reactive store.
// API is unchanged: get, set, batch, subscribe, effect, computed.

import type { SeriesItem, SeasonGroup } from "./api-types.js";
import type { ParsedConfig } from "./wire/types.gen.js";
import type { RunningScansByScope } from "./scan-scope.js";
import { createStore } from "@cplieger/reactive";

// --- Typed store key registry ---

// ParsedConfig is the generated wire type for GET /api/config/parsed;
// re-exported here because the store key below is where consumers meet it.
export type { ParsedConfig } from "./wire/types.gen.js";

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
  currentPage: string;
  // Running/queued background scans keyed by scope, derived from the
  // activity feed by the status poll (scan-scope.ts). Scan buttons key off
  // this shared map — never a local in-flight flag.
  runningScansByScope: RunningScansByScope;
  isUnconfigured: boolean;
  isReady: boolean;
  isAdmin: boolean;
  // Allow arbitrary keys for test usage.
  [key: string]: unknown;
}

const store = createStore<StoreMap>();

export const get = store.get.bind(store);
export const set = store.set.bind(store);
export const batch = store.batch.bind(store);
export const subscribe = store.subscribe.bind(store);
export const effect = store.effect.bind(store);
export const computed = store.computed.bind(store);
