// Reactive state container — backed by @cplieger/reactive store.
// API is unchanged for consumers: get, set, batch, subscribe, effect, computed.
// `batch` and `effect` come straight from the package rather than off the Store:
// they are the engine's own functions, and reactive v2 stopped re-exposing them
// on the Store because `store.batch(…)` read as "batch this store's writes" while
// batching the whole graph. Re-exporting them here keeps every call site here
// unchanged.

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

export { batch, effect } from "@cplieger/reactive";

// Destructured, which is the shape createStore's own doc comment documents
// (`const { get, set, subscribe, computed } = createStore<MyMap>()`): the four
// methods are closures over the factory's private signal map and never read
// `this`, so the `.bind(store)` this replaced was a no-op wrapper. Dropping it
// also fixed a coverage artifact — v8 attributed the four `bind()` initializer
// expressions by byte offset into @cplieger/reactive and reported two of the
// five module-scope statements as never run, which read as 60.00% against a
// per-file threshold of 60 on a file whose every statement runs at import.
//
// unbound-method cannot see that the members are closures (it reads the
// declared `Store<M>` interface, whose method signatures imply a `this`), so it
// flags the documented usage. The fix belongs upstream — declaring the members
// with `this: void` would sanction destructuring for every consumer — and until
// then this is the one honest place to say so.
// eslint-disable-next-line @typescript-eslint/unbound-method -- closures, not methods; see above
export const { get, set, subscribe, computed } = createStore<StoreMap>();
