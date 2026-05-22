// Reactive state container with auto-tracked reactivity.
// get() records accessed keys when called inside effect() or
// computed(), enabling automatic dependency discovery.
//
// batch() defers subscriber notifications until the batch completes,
// coalescing multiple set() calls into a single notification pass
// (React-inspired batched state updates). Each key is notified at
// most once per batch with its final value.

import type { CoverageItem, SeriesItem, SeasonGroup } from './api-types.js';

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

const state: Record<string, unknown> = {};
const subscribers: Record<string, Array<(value: unknown) => void>> = {};

let tracking: Set<string> | null = null;
let batchDepth = 0;
let pendingKeys: Map<string, unknown> | null = null;

export function get<K extends keyof StoreMap>(key: K): StoreMap[K] {
  if (tracking) tracking.add(key);
  return state[key] as StoreMap[K];
}

export function set<K extends keyof StoreMap>(key: K, value: StoreMap[K]): void {
  const prev = state[key];
  state[key] = value;
  if (prev === value) return;
  if (batchDepth > 0) {
    if (!pendingKeys) pendingKeys = new Map();
    pendingKeys.set(key, value);
  } else {
    notifySubs(key, value);
  }
}

// Defer all subscriber notifications until fn completes.
// Nested batch() calls are safe; only the outermost flushes.
export function batch(fn: () => void): void {
  batchDepth++;
  try {
    fn();
  } finally {
    batchDepth--;
    if (batchDepth === 0 && pendingKeys) {
      const pending = pendingKeys;
      pendingKeys = null;
      for (const [key, value] of pending) {
        notifySubs(key, value);
      }
    }
  }
}

export function subscribe<K extends keyof StoreMap>(key: K, callback: (value: StoreMap[K]) => void): () => void {
  if (!subscribers[key]) subscribers[key] = [];
  subscribers[key].push(callback as (value: unknown) => void);
  return () => {
    const current = subscribers[key];
    if (current) subscribers[key] = current.filter(cb => cb !== callback);
  };
}

function notifySubs(key: string, value: unknown): void {
  for (const cb of (subscribers[key] || [])) {
    try { cb(value); } catch (e) { console.error('store subscriber error:', e); }
  }
}

export function effect(fn: () => void): () => void {
  let unsubs: Array<() => void> = [];
  const run = () => {
    for (const u of unsubs) u();
    const prev = tracking;
    tracking = new Set();
    try { fn(); } catch (e) { console.error('store effect error:', e); }
    const deps = tracking;
    tracking = prev;
    unsubs = [];
    for (const dep of deps) {
      unsubs.push(subscribe(dep as keyof StoreMap, run));
    }
  };
  run();
  return () => { for (const u of unsubs) u(); unsubs = []; };
}

export function computed<K extends keyof StoreMap>(outputKey: K, computeFn: () => StoreMap[K]): () => void {
  return effect(() => {
    set(outputKey, computeFn());
  });
}
