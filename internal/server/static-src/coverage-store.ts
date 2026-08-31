// coverage-store.ts — the coverage row collection and every write to it.
//
// A leaf under BOTH coverage orchestrators: coverage.ts (fetch + render) and
// coverage-heal.ts (event-driven per-root maintenance) import this module and
// neither imports the other. That is what lets the A6 reset rule and the
// heal's row writes be ordinary synchronous calls instead of bus traffic.
//
// Two-tier reactive model: rows live in a `createCollection` keyed by media id
// (a signal per row) plus a separate structure signal, so a content change
// repaints one row while add/remove/reorder reconciles structure.

import { createCollection, type ReadonlySignal } from "@cplieger/reactive";
import { join } from "@cplieger/keyenc";
import { coverageMediaId } from "./utils.js";
import { openTransaction } from "./transaction.js";
import type { CoverageItem } from "./api-types.js";
import type { SeriesItem, MovieItem } from "./wire/types.gen.js";

const coverage = createCollection<CoverageItem>(coverageMediaId);

/** Reactive ordered row ids — the structure tier `bindList` renders over, and
 *  the untracked row count via `peek()`. */
export const coverageIds: ReadonlySignal<readonly string[]> = coverage.ids;

/** Reactive per-row signal — the content tier `bindList` subscribes to. */
export function coverageSignalFor(rootKey: string): ReadonlySignal<CoverageItem> | undefined {
  return coverage.signalFor(rootKey);
}

// --- A6 pair-landing state: the heal gate + task 9's collection-leg seam ---

// True once a full series+movies pair has LANDED this tab. Row-level upserts
// (a heal or deep-link insert into an incomplete collection) never set it, so
// an incomplete collection cannot open the heal gate.
let pairLanded = false;
// Collections a landed pair registered for later transaction collection legs
// (task 9 reads registeredCollections ∪ the current route's needs). Tab
// state — survives SSE boot_id changes.
const registeredCollectionNames = new Set<string>();

// --- Task 9 transaction seams: tombstones, covered writers, the leg join ---

// Whether an SSE transaction is open is transaction.ts's fact (events.ts
// brackets it). While one is open, a heal 404-delete records its root as a
// TOMBSTONE, and every full-pair snapshot application drops tombstoned rows —
// the pair may have been read before the arr delete, so an un-dropped row would
// resurrect what the heal already removed.
const tombstones = new Set<string>();
// Full-pair writers whose fetch began while the transaction was open
// ("covered" writers). Tombstones clear at settle only when none is in
// flight — else when the last one lands, so a loader dispatched during the
// transaction that lands after settle still drops.
let coveredPairWriters = 0;

// The transaction's in-flight collection leg (null when the leg is empty or
// no transaction runs). A route loader arriving mid-transaction whose pair
// the leg covers JOINS it instead of issuing a second read.
type CollectionLegJoin = "landed" | "failed" | "uncovered";
let pendingLeg: Promise<CollectionLegJoin> | null = null;

/** Task 9: settle (commit or abort) — tombstones clear only when no covered
 *  full-pair writer is still in flight. Idempotent, and safe in either order
 *  against `settleTransaction`: an open transaction keeps them. */
export function releaseCoverageTombstones(): void {
  maybeClearTombstones();
}

/** Tombstones outlive the transaction exactly as long as a covered writer
 *  could still apply a pre-delete snapshot. */
function maybeClearTombstones(): void {
  if (openTransaction() === null && coveredPairWriters === 0) {
    tombstones.clear();
  }
}

/** Task 9: register (or clear) the transaction's in-flight collection leg
 *  for loaders to join. */
export function setCollectionLegJoin(p: Promise<CollectionLegJoin> | null): void {
  pendingLeg = p;
}

/** The collection leg a loader may join, or null. */
export function pendingCollectionLeg(): Promise<CollectionLegJoin> | null {
  return pendingLeg;
}

/** Task 9: mark a full-pair write in flight; returns the matching end call.
 *  Only a write that BEGINS during an open transaction is covered. */
export function beginCoveredPairWrite(): () => void {
  if (openTransaction() === null) {
    return () => {
      /* not covered */
    };
  }
  coveredPairWriters += 1;
  return () => {
    coveredPairWriters -= 1;
    maybeClearTombstones();
  };
}

/** A6's heal gate: true once the full library pair has landed this tab. */
export function libraryLoaded(): boolean {
  return pairLanded;
}

/** Collections registered by landed pair loads (task 9's collection leg). */
export function registeredCollections(): ReadonlySet<string> {
  return registeredCollectionNames;
}

/** A failed pair read registers the pair without opening the gate, so the
 *  latch's forced transaction still fetches it (task 9). */
export function registerCollectionPair(): void {
  registeredCollectionNames.add("series").add("movies");
}

// --- Row writes ---

/** A6 heal write: identity-preserving single-row upsert. A row whose
 *  signature is unchanged keeps its CURRENT object (its signal never fires,
 *  nothing repaints); a changed row lands whole and repaints through the
 *  data-sig-gated full-row updater; a new root is appended (display order is
 *  the filtered view's sort, not collection order). */
export function applyHealedRow(item: CoverageItem): void {
  const cur = coverage.get(coverageMediaId(item));
  if (cur !== undefined && coverageItemSignature(cur) === coverageItemSignature(item)) {
    return;
  }
  coverage.upsert(item);
}

/** A6 heal delete: a summary 404 means the collection omits this row now.
 *  During a transaction the root is TOMBSTONED so a full-pair snapshot read
 *  before the delete cannot resurrect it (task 9). */
export function removeCoverageRow(rootKey: string): void {
  if (openTransaction() !== null) {
    tombstones.add(rootKey);
  }
  coverage.remove(rootKey);
}

/** Untracked read of one row by collection key (`tvdb-{n}` / `tmdb-{n}`). */
export function coverageRow(rootKey: string): CoverageItem | undefined {
  return coverage.get(rootKey);
}

/** Ordered row snapshot. Reactive: read inside an effect it tracks order AND
 *  every row, which is what the filtered/sorted view wants. */
export function coverageItems(): CoverageItem[] {
  return coverage.items();
}

/** Canonical content signature over EVERY field the library view renders,
 *  keys, filters, sorts, or hands onward through a row action (row click →
 *  detail, Search → scan): the full SeriesItem/MovieItem wire structs plus
 *  the client `_type`. Audited by coverage.test.ts's per-field mutation
 *  tables, typed over `keyof Required<CoverageItem>` and CoverageTarget so a
 *  wire field change fails compilation there and re-runs the audit. Nested
 *  keyenc `join`s (detail.ts's epSig discipline): a separator-carrying value
 *  cannot alias field boundaries; a collision only skips a repaint, never
 *  misidentifies a row. */
export function coverageItemSignature(item: CoverageItem): string {
  return join(
    item._type,
    String(item.id),
    String(item.tvdb_id ?? ""),
    String(item.tmdb_id ?? ""),
    item.imdb_id ?? "",
    item.title,
    String(item.year),
    item.first_aired ?? "",
    item.in_cinemas ?? "",
    item.digital_release ?? "",
    item.rule,
    item.audio_lang,
    join(...(item.tags ?? []).map(String)),
    item.excluded ? "1" : "0",
    String(item.has_file ?? ""),
    item.scene_name ?? "",
    String(item.episodes ?? ""),
    join(
      ...item.targets.map((t) =>
        join(t.language, t.variant, String(t.have), String(t.have_ignored), String(t.total)),
      ),
    ),
  );
}

/** The row half of a full-pair application: merge identity-preservingly (an
 *  unchanged signature keeps the CURRENT object so nothing repaints), DROP
 *  tombstoned roots (a heal 404-delete during the transaction is newer
 *  authority than a pair read before the arr delete), and open the heal gate
 *  + register only when BOTH sides landed.
 *
 *  NOT the application site: the A6 reset rule that must precede every
 *  overwrite is coverage.ts's `applyCoveragePair`, which is what both
 *  full-pair writers call. */
export function setCoveragePair(
  series: SeriesItem[] | null,
  movies: MovieItem[] | null,
): CoverageItem[] {
  const merged: CoverageItem[] = [
    ...(series ?? []).map((s) => ({ ...s, _type: "series" as const })),
    ...(movies ?? []).map((m) => ({ ...m, _type: "movie" as const })),
  ]
    .filter((item) => !tombstones.has(coverageMediaId(item)))
    .map((item) => {
      const cur = coverage.get(coverageMediaId(item));
      return cur !== undefined && coverageItemSignature(cur) === coverageItemSignature(item)
        ? cur
        : item;
    });
  coverage.setAll(merged);
  if (series !== null && movies !== null) {
    // The pair LANDED: open A6's heal gate and register the pair for task 9's
    // transaction collection legs — set by WHICHEVER caller lands it. A null
    // leg is a failed read (the generated client null-collapses), and a
    // failed pair load must open nothing.
    pairLanded = true;
    registerCollectionPair();
  }
  return merged;
}

/** Test-only: drop all rows, close the heal gate, forget registrations, and
 *  clear the transaction seams. The open-transaction flag is not ours to
 *  reset — `settleTransaction` owns it. */
export function _resetCoverageStoreForTest(): void {
  coverage.clear();
  pairLanded = false;
  registeredCollectionNames.clear();
  tombstones.clear();
  coveredPairWriters = 0;
  pendingLeg = null;
}
