package boltstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/cplieger/subflux/internal/api"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file holds the maintenance domain (MaintStore). DeleteStateByPaths
// (task 6.1) and CleanupDrift (task 6.2) are implemented here; ReconcileState
// (6.3) remains a stub replaced by a later task. The private helpers below
// (collectVideoPathIDs, mediaHasState, deleteSubtitleFilesByMedia,
// cleanOrphanedCoverageFor) are shared so 6.3 can reuse the same orphan-cleanup
// fan-out.

// deletePathBatchSize bounds how many video paths are processed per bbolt
// Update transaction. DeleteStateByPaths is usually called with a single path
// (the poller's video-gone cleanup) or a small reconcile batch, but bounding
// the batch keeps the single write lock held for only the low tens of
// milliseconds even on a large delete (design "Transactions and concurrency").
// Each batch is all-or-none; the operation as a whole is not, which is safe
// because every step is idempotent (a re-run finds the rows already gone).
const deletePathBatchSize = 200

// mediaRef identifies a (media_type, media_id) coverage owner. subtitle_files
// and scan_state are owned per media item (language is irrelevant to coverage
// ownership), so orphan cleanup is keyed by this pair.
type mediaRef struct {
	mt  api.MediaType
	mid string
}

// DeleteStateByPaths removes every subtitle_state row backed by any of the
// given video paths and cleans the resulting orphans, mirroring the old SQLite
// DeleteStateByPaths (Requirement 7.6). A single video file backs SEVERAL state
// rows (an auto row plus manual rows across languages), so for each path it:
//
//   - prefix-scans ix_state_video to collect every row's surrogate id,
//   - deletes each row through the deleteState chokepoint (which removes the
//     ix_state_triple / ix_state_imported / ix_state_video entries and
//     decrements the downloads counter in the same tx),
//   - clears every affected triple's search_attempts backoff (and ix_attempts_due
//     entries) via clearTripleBackoff,
//   - deletes the subtitle_files and scan_state rows for any affected media item
//     left with no remaining subtitle_state row (the "media left with no state"
//     condition, matching the old cleanOrphanedCoverage).
//
// It returns the non-empty subtitle file paths of the deleted rows so the
// caller can remove them from disk as a fallback (the arr usually deletes them
// already). Work is split into bounded Update transactions; the operation is
// idempotent, so re-running it on the same paths is a safe no-op.
func (d *DB) DeleteStateByPaths(_ context.Context, videoPaths []string) (api.CleanupResult, error) {
	if len(videoPaths) == 0 {
		return api.CleanupResult{}, nil
	}

	var subPaths []string
	for start := 0; start < len(videoPaths); start += deletePathBatchSize {
		end := min(start+deletePathBatchSize, len(videoPaths))
		batchPaths, err := d.deletePathsBatch(videoPaths[start:end])
		if err != nil {
			return api.CleanupResult{}, err
		}
		subPaths = append(subPaths, batchPaths...)
	}

	if len(subPaths) > 0 {
		slog.Info("deleted state by video paths",
			"video_paths", len(videoPaths), "subtitle_paths", len(subPaths))
	}
	return api.CleanupResult{Paths: subPaths}, nil
}

// deletePathsBatch processes one bounded batch of video paths in a single
// all-or-none Update transaction: it deletes the matching state rows, clears
// the affected triples' backoff, and cleans orphaned coverage. It returns the
// non-empty subtitle paths of the deleted rows (for the caller's disk cleanup).
func (d *DB) deletePathsBatch(paths []string) ([]string, error) {
	var subPaths []string
	err := d.db.Update(func(tx *bolt.Tx) error {
		// subtitle_state does not store its (mt, mid, lang) triple in the value
		// (it lives only in the ix_state_triple key), so recover an id -> triple
		// map once per batch to route deletes through the deleteState chokepoint.
		triples, err := buildStateTripleMap(tx)
		if err != nil {
			return err
		}
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if sb == nil {
			return errors.New("boltstore: subtitle_state bucket not found")
		}

		affectedTriples := make(map[stateTripleInfo]struct{})
		affectedMedia := make(map[mediaRef]struct{})

		for _, p := range paths {
			batchPaths, perr := d.deleteStateRowsForPath(tx, triples, sb, p, affectedTriples, affectedMedia)
			if perr != nil {
				return perr
			}
			subPaths = append(subPaths, batchPaths...)
		}

		// Success/removal clears the affected triples' adaptive backoff.
		if err := clearBackoffForTriples(tx, affectedTriples); err != nil {
			return err
		}

		// Drop subtitle_files + scan_state for any affected media item left with
		// no remaining subtitle_state row.
		return cleanOrphanedCoverageFor(tx, affectedMedia)
	})
	if err != nil {
		return nil, err
	}
	return subPaths, nil
}

// deleteStateRowsForPath deletes every subtitle_state row backed by videoPath
// (collected from ix_state_video), routing each delete through the deleteState
// chokepoint, and records the affected triples and media items in the shared
// sets. It returns the non-empty subtitle file paths of the deleted rows (for
// the caller's disk cleanup, matching the old `... AND path != ”` projection).
// An ix_state_video entry with no ix_state_triple counterpart is skipped
// defensively: its triple is unrecoverable, so the row cannot be routed through
// deleteState; a correctly maintained index never produces this.
func (d *DB) deleteStateRowsForPath(tx *bolt.Tx, triples map[int64]stateTripleInfo, sb *bolt.Bucket, videoPath string, affectedTriples map[stateTripleInfo]struct{}, affectedMedia map[mediaRef]struct{}) ([]string, error) {
	ids, err := collectVideoPathIDs(tx, videoPath)
	if err != nil {
		return nil, err
	}
	var subPaths []string
	for _, id := range ids {
		tr, ok := triples[id]
		if !ok {
			continue
		}
		// Capture the subtitle file path (non-empty only) before delete.
		if p := statePath(sb, id); p != "" {
			subPaths = append(subPaths, p)
		}
		if _, derr := deleteState(tx, tr.mt, tr.mid, tr.lang, id); derr != nil {
			return nil, derr
		}
		affectedTriples[tr] = struct{}{}
		affectedMedia[mediaRef{mt: tr.mt, mid: tr.mid}] = struct{}{}
	}
	return subPaths, nil
}

// statePath returns the non-empty subtitle file path stored for the surrogate
// id, or "" when the row is absent, undecodable, or has an empty path.
func statePath(sb *bolt.Bucket, id int64) string {
	raw := sb.Get(stateKey(id))
	if raw == nil {
		return ""
	}
	var sr stateRec
	if boltkv.Decode(raw, &sr) != nil {
		return ""
	}
	return sr.Path
}

// clearBackoffForTriples clears the adaptive backoff of every triple in the set
// through the clearTripleBackoff chokepoint, so ix_attempts_due and the attempts
// counter stay consistent.
func clearBackoffForTriples(tx *bolt.Tx, triples map[stateTripleInfo]struct{}) error {
	for tr := range triples {
		if err := clearTripleBackoff(tx, tr.mt, tr.mid, tr.lang); err != nil {
			return err
		}
	}
	return nil
}

// collectVideoPathIDs prefix-scans ix_state_video for one video path and
// returns the surrogate ids of every subtitle_state row backed by it. The
// trailing-separator videoPrefix means a scan for "/m/a.mkv" never matches
// "/m/a.mkv.bak" (component-boundary safety). It is shared with ReconcileState
// (task 6.3), which deletes a video-gone row's whole fan-out the same way.
func collectVideoPathIDs(tx *bolt.Tx, videoPath string) ([]int64, error) {
	idx := tx.Bucket([]byte(bucketIxStateVideo))
	if idx == nil {
		return nil, errors.New("boltstore: ix_state_video bucket not found")
	}
	prefix := videoPrefix(videoPath)
	var ids []int64
	c := idx.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		_, id, ok := splitStateVideoKey(k)
		if !ok {
			continue // malformed index key; a correctly built key always parses
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// mediaHasState reports whether any subtitle_state row remains for the media
// item, by checking ix_state_triple for any entry under mediaPrefix(mt, mid).
// It is the "media left with no state" predicate that gates orphan cleanup
// (matching the old findOrphans GROUP BY). The check is index-only (no primary
// dereference).
func mediaHasState(tx *bolt.Tx, mt api.MediaType, mid string) (bool, error) {
	idx := tx.Bucket([]byte(bucketIxStateTriple))
	if idx == nil {
		return false, errors.New("boltstore: ix_state_triple bucket not found")
	}
	prefix := mediaPrefix(mt, mid)
	c := idx.Cursor()
	k, _ := c.Seek(prefix)
	return k != nil && bytes.HasPrefix(k, prefix), nil
}

// deleteSubtitleFilesByMedia removes every subtitle_files row for a media item
// (all languages/variants/sources) via the deleteSubtitleFile chokepoint, so
// the TotalSubtitleFiles counter is decremented in the same tx. Keys are
// collected before deletion because bbolt skips the next key if you delete
// during cursor iteration.
func deleteSubtitleFilesByMedia(tx *bolt.Tx, mt api.MediaType, mid string) error {
	b := tx.Bucket([]byte(bucketSubtitleFiles))
	if b == nil {
		return errors.New("boltstore: subtitle_files bucket not found")
	}
	prefix := mediaPrefix(mt, mid)
	var keys [][]byte
	c := b.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		kc := make([]byte, len(k))
		copy(kc, k)
		keys = append(keys, kc)
	}
	for _, k := range keys {
		if _, err := deleteSubtitleFile(tx, k); err != nil {
			return err
		}
	}
	return nil
}

// cleanOrphanedCoverageFor deletes the subtitle_files and scan_state rows for
// each affected media item that no longer has any subtitle_state row,
// reproducing the old cleanOrphanedCoverage. It is shared so ReconcileState
// (task 6.3) cleans the same orphans after a video-gone delete.
func cleanOrphanedCoverageFor(tx *bolt.Tx, media map[mediaRef]struct{}) error {
	for mr := range media {
		has, err := mediaHasState(tx, mr.mt, mr.mid)
		if err != nil {
			return err
		}
		if has {
			continue // state remains: coverage is still owned, preserve it
		}
		if err := deleteSubtitleFilesByMedia(tx, mr.mt, mr.mid); err != nil {
			return err
		}
		if _, err := deleteScanState(tx, mr.mt, mr.mid); err != nil {
			return err
		}
	}
	return nil
}

// CleanupDrift clears adaptive-backoff state after a config change, mirroring
// the old SQLite CleanupDrift (Requirements 7.7, 7.8). The drift describes what
// the new config dropped relative to the old one:
//
//   - AdaptiveDisabled: adaptive search was turned off, so EVERY search_attempts
//     row (and its ix_attempts_due entry) is cleared. This short-circuits the
//     per-language/provider cleanup (the old store returned early after the
//     blanket DELETE), since clearing everything subsumes it.
//   - RemovedLanguages / RemovedProviders: a language or provider left the
//     config, so the rows whose attempt-key carries that language or provider
//     component are cleared (the language and provider are components of the
//     `mt 0x00 mid 0x00 lang 0x00 provider` key).
//
// All deletes route through the deleteAttempt chokepoint, so the ix_attempts_due
// index and the attempts counter stay consistent. The whole operation runs in
// one bbolt Update: config-drift cleanup is a hot-reload-time event over a small
// bucket, not a per-item hot path, so it does not need the bounded batching that
// DeleteStateByPaths / ReconcileState use. Keys are collected before deletion
// (bbolt skips the next key if you delete during cursor iteration). The
// operation is idempotent: re-running it finds the matching rows already gone.
func (d *DB) CleanupDrift(_ context.Context, drift api.ConfigDrift) error {
	if drift.Empty() {
		slog.Debug("config drift: no cleanup needed")
		return nil
	}

	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSearchAttempts))
		if b == nil {
			return errors.New("boltstore: search_attempts bucket not found")
		}

		// Adaptive disabled clears all backoff and subsumes the per-language /
		// per-provider cleanup, so it short-circuits like the old store's early
		// return after the blanket DELETE.
		if drift.AdaptiveDisabled {
			return clearAllAttempts(tx, b)
		}
		if err := clearAttemptsForLanguages(tx, b, drift.RemovedLanguages); err != nil {
			return err
		}
		return clearAttemptsForProviders(tx, b, drift.RemovedProviders)
	})
}

// clearAllAttempts deletes every search_attempts row (the adaptive-disabled
// branch), logging the count when non-zero.
func clearAllAttempts(tx *bolt.Tx, b *bolt.Bucket) error {
	n, err := deleteAttemptsMatching(tx, b, func(string, api.ProviderID) bool { return true })
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("config drift: adaptive disabled, cleared all attempts", "rows", n)
	}
	return nil
}

// clearAttemptsForLanguages deletes the search_attempts rows whose language key
// component is in the removed set, logging the count when non-zero. An empty set
// is a no-op.
func clearAttemptsForLanguages(tx *bolt.Tx, b *bolt.Bucket, languages []string) error {
	if len(languages) == 0 {
		return nil
	}
	removed := make(map[string]struct{}, len(languages))
	for _, l := range languages {
		removed[l] = struct{}{}
	}
	n, err := deleteAttemptsMatching(tx, b, func(lang string, _ api.ProviderID) bool {
		_, ok := removed[lang]
		return ok
	})
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("config drift: cleared attempts for removed language",
			"values", languages, "rows", n)
	}
	return nil
}

// clearAttemptsForProviders deletes the search_attempts rows whose provider key
// component is in the removed set, logging the count when non-zero. An empty set
// is a no-op.
func clearAttemptsForProviders(tx *bolt.Tx, b *bolt.Bucket, providers []api.ProviderID) error {
	if len(providers) == 0 {
		return nil
	}
	removed := make(map[api.ProviderID]struct{}, len(providers))
	for _, p := range providers {
		removed[p] = struct{}{}
	}
	n, err := deleteAttemptsMatching(tx, b, func(_ string, provider api.ProviderID) bool {
		_, ok := removed[provider]
		return ok
	})
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("config drift: cleared attempts for removed provider",
			"values", providers, "rows", n)
	}
	return nil
}

// deleteAttemptsMatching deletes every search_attempts row whose (language,
// provider) key components satisfy match, routing each delete through the
// deleteAttempt chokepoint so the ix_attempts_due index and attempts counter
// stay consistent. It walks the primary bucket collecting matching keys first,
// then deletes them after the cursor walk (bbolt skips the next key if you
// delete mid-iteration). It returns the number of rows deleted (for logging).
func deleteAttemptsMatching(tx *bolt.Tx, b *bolt.Bucket, match func(lang string, provider api.ProviderID) bool) (int, error) {
	type attemptComponents struct {
		mt   api.MediaType
		mid  string
		lang string
		prov api.ProviderID
	}
	var toDelete []attemptComponents
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		parts := boltkv.Split(k)
		if len(parts) != 4 {
			// Malformed key (not mt 0x00 mid 0x00 lang 0x00 provider); skip
			// rather than misroute a delete. A correctly built key always
			// has four components.
			continue
		}
		lang := parts[2]
		prov := api.ProviderID(parts[3])
		if !match(lang, prov) {
			continue
		}
		toDelete = append(toDelete, attemptComponents{
			mt:   api.MediaType(parts[0]),
			mid:  parts[1],
			lang: lang,
			prov: prov,
		})
	}
	for _, a := range toDelete {
		if _, err := deleteAttempt(tx, a.mt, a.mid, a.lang, a.prov); err != nil {
			return 0, err
		}
	}
	return len(toDelete), nil
}

// reconcileResetBatchSize bounds how many missing-subtitle rows are processed
// per bbolt Update transaction in the reset phase. ReconcileState is the one
// sustained write burst on a large library, so it is split into bounded batches
// instead of one whole-library transaction (Requirement 7.4): holding the single
// write lock across a 52k-item pass would starve the live poller. The design
// targets 200-500 rows per batch so the worst-case write-lock hold stays in the
// low tens of milliseconds. Each batch is all-or-none; the pass as a whole is
// not, which is safe because every operation is idempotent (Requirement 7.5).
const reconcileResetBatchSize = 500

// reconcileEntry is a copy of the fields ReconcileState needs from one
// subtitle_state row, snapshotted under a short read transaction so the
// filesystem stats happen outside any bbolt transaction (design "Transactions
// and concurrency": never hold a read txn open while doing filesystem I/O).
type reconcileEntry struct {
	tr        stateTripleInfo
	subPath   string
	videoPath string
	id        int64
	manual    bool
}

// reconcileAction is the three-way classification of a single row against the
// filesystem, reproducing the old reconcile/classify.go ClassifyEntry exactly.
type reconcileAction int

const (
	reconcileSkip       reconcileAction = iota // video stat error, or video present but sub_path empty
	reconcileDelete                            // video gone
	reconcileSubMissing                        // video present, this subtitle gone
	reconcileSubPresent                        // video present, this subtitle present (or sub stat error)
)

// ReconcileState reconciles subtitle_state against the filesystem in three
// branches, group-aware by triple, mirroring the corrected three-way semantics
// of the old store (Requirements 7.1-7.5):
//
//   - VIDEO GONE: every row whose video file no longer exists is deleted along
//     with its orphaned subtitle file, the triple's backoff, and any scan_state
//     for a media item left with no state. This fan-out is delegated to
//     DeleteStateByPaths (task 6.1), which already prefix-scans ix_state_video,
//     routes deletes through the deleteState chokepoint, clears backoff, and
//     cleans orphaned coverage in bounded transactions.
//   - SUBTITLE GONE, A SIBLING STILL PRESENT: when a row's subtitle file is gone
//     but its video exists AND at least one other row for the SAME triple still
//     has its subtitle on disk, only that one row is deleted. The remaining
//     rows and any manual lock are PRESERVED, backoff is NOT cleared, and no row
//     is reset (Requirement 7.2).
//   - ALL SUBTITLES FOR A TRIPLE GONE: when every row for a triple has lost its
//     subtitle (video still present), the auto rows are reset in place (clear
//     path/score/provider/release_name, bump media_imported to now) so the next
//     scan re-searches, the manual rows are deleted, and the triple's backoff is
//     cleared (Requirement 7.3). Counted once per triple in ResetCount.
//
// The read phase snapshots the rows under short View transactions and performs
// all filesystem stats with no transaction open; the mutation phase runs in
// bounded Update batches (Requirement 7.4). Every operation is idempotent, so an
// interrupted pass converges on re-run (Requirement 7.5): a deleted row stays
// gone, and a reset auto row has an empty sub_path that classifies as skip on
// the next pass, so media_imported is not bumped again.
func (d *DB) ReconcileState(ctx context.Context) (api.ReconcileResult, error) {
	entries, err := d.loadReconcileEntries()
	if err != nil {
		return api.ReconcileResult{}, err
	}
	if len(entries) == 0 {
		return api.ReconcileResult{}, nil
	}

	slog.Info("reconcile: starting", "rows", len(entries))

	// Classify every row against the filesystem with no transaction open.
	videoGonePaths, subMissing, subPresent := d.classifyReconcileEntries(entries)

	slog.Info("reconcile: classified",
		"video_gone_paths", len(videoGonePaths),
		"groups_sub_missing", len(subMissing))

	// Branch 1: video gone. Delegate the whole fan-out to DeleteStateByPaths,
	// which deletes rows + orphaned files + backoff + orphaned scan_state in
	// bounded transactions.
	var deleted api.CleanupResult
	if len(videoGonePaths) > 0 {
		deleted, err = d.DeleteStateByPaths(ctx, videoGonePaths)
		if err != nil {
			return api.ReconcileResult{Deleted: deleted}, err
		}
	}

	// Branches 2 and 3: reset/delete missing-subtitle rows in bounded batches.
	resetCount, err := d.reconcileMissingGroups(subMissing, subPresent)
	if err != nil {
		return api.ReconcileResult{Deleted: deleted, ResetCount: resetCount}, err
	}

	slog.Info("reconcile: complete",
		"deleted_paths", len(deleted.Paths), "reset_groups", resetCount)
	return api.ReconcileResult{Deleted: deleted, ResetCount: resetCount}, nil
}

// loadReconcileEntries snapshots every subtitle_state row with a non-empty
// video_path into reconcileEntry copies under a single short View transaction,
// matching the old loadEntries `WHERE video_path != ”`. The triple components
// (media_type, media_id, language) are not stored in stateRec, so they are
// recovered from the ix_state_triple key while each primary is dereferenced for
// the path/video_path/manual fields. Values are copied out so the filesystem
// stats in the classify phase run with no transaction open.
func (d *DB) loadReconcileEntries() ([]reconcileEntry, error) {
	var entries []reconcileEntry
	err := d.db.View(func(tx *bolt.Tx) error {
		idx := tx.Bucket([]byte(bucketIxStateTriple))
		if idx == nil {
			return errors.New("boltstore: ix_state_triple bucket not found")
		}
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if sb == nil {
			return errors.New("boltstore: subtitle_state bucket not found")
		}
		return idx.ForEach(func(k, _ []byte) error {
			entry, ok, derr := reconcileEntryFromIndex(sb, k)
			if derr != nil {
				return derr
			}
			if ok {
				entries = append(entries, entry)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// reconcileEntryFromIndex resolves one ix_state_triple entry into a
// reconcileEntry by reading and decoding its primary subtitle_state row. ok is
// false (skip the entry) for a malformed index key, index/primary drift, a
// tolerated bad derived record, or an empty video_path (matching the old
// `WHERE video_path != ”` filter). A fail-closed decode error is returned.
func reconcileEntryFromIndex(sb *bolt.Bucket, k []byte) (reconcileEntry, bool, error) {
	mt, mid, lang, id, ok := splitStateTripleKey(k)
	if !ok {
		return reconcileEntry{}, false, nil // malformed index key; a correct index never produces one
	}
	raw := sb.Get(stateKey(id))
	if raw == nil {
		return reconcileEntry{}, false, nil // index/primary drift: no primary for this access path
	}
	var sr stateRec
	// subtitle_state is a derived bucket: tolerate-skip a bad record (the next
	// scan rebuilds it) rather than abort the whole pass.
	skip, derr := decodeRecord(bucketDecodeMode(bucketSubtitleState), bucketSubtitleState, stateKey(id), raw, &sr)
	if derr != nil {
		return reconcileEntry{}, false, derr
	}
	if skip || sr.VideoPath == "" {
		return reconcileEntry{}, false, nil // matches the old `WHERE video_path != ''` filter
	}
	return reconcileEntry{
		id:        id,
		tr:        stateTripleInfo{mt: mt, mid: mid, lang: lang},
		subPath:   sr.Path,
		videoPath: sr.VideoPath,
		manual:    sr.Manual,
	}, true, nil
}

// classifyReconcileEntries classifies each snapshotted row against the
// filesystem (via d.statFn, no transaction open) and groups the results,
// reproducing the old Classify exactly:
//
//   - video gone -> its video path is collected (deduplicated, deterministic
//     order) for DeleteStateByPaths;
//   - this subtitle gone -> the entry is appended to subMissing[triple];
//   - this subtitle present (or its stat errored) -> subPresent[triple] is set.
//
// A row whose video stat errors (other than not-exist), or whose video is
// present but whose sub_path is empty, classifies as skip and contributes to
// neither map, matching the old classifier.
func (d *DB) classifyReconcileEntries(entries []reconcileEntry) (
	videoGonePaths []string,
	subMissing map[stateTripleInfo][]reconcileEntry,
	subPresent map[stateTripleInfo]bool,
) {
	subMissing = make(map[stateTripleInfo][]reconcileEntry)
	subPresent = make(map[stateTripleInfo]bool)
	seenVideo := make(map[string]struct{})

	for i := range entries {
		e := entries[i]
		switch d.classifyReconcileEntry(&e) {
		case reconcileDelete:
			if _, ok := seenVideo[e.videoPath]; !ok {
				seenVideo[e.videoPath] = struct{}{}
				videoGonePaths = append(videoGonePaths, e.videoPath)
			}
		case reconcileSubMissing:
			subMissing[e.tr] = append(subMissing[e.tr], e)
		case reconcileSubPresent:
			subPresent[e.tr] = true
		case reconcileSkip:
			// contributes to neither map
		}
	}
	return videoGonePaths, subMissing, subPresent
}

// classifyReconcileEntry is the pure per-row classifier, a direct port of the
// old reconcile/classify.go ClassifyEntry: video gone -> delete; video present
// but this subtitle gone -> sub-missing; otherwise (subtitle present, sub stat
// error, or empty sub_path with a present video) -> present/skip. A video stat
// error other than not-exist is treated as skip (do not delete on a transient
// stat failure).
func (d *DB) classifyReconcileEntry(e *reconcileEntry) reconcileAction {
	if e.videoPath == "" {
		return reconcileSkip
	}
	if _, err := d.statFn(e.videoPath); errors.Is(err, os.ErrNotExist) {
		return reconcileDelete
	} else if err != nil {
		return reconcileSkip
	}
	if e.subPath == "" {
		return reconcileSkip
	}
	if _, err := d.statFn(e.subPath); errors.Is(err, os.ErrNotExist) {
		return reconcileSubMissing
	} else if err != nil {
		return reconcileSubPresent
	}
	return reconcileSubPresent
}

// reconcileMissingGroups applies the sub-missing branches over the grouped
// entries in bounded Update batches and returns the number of all-subtitles-gone
// triples reset (Requirement 7.4). Group keys are sorted for deterministic batch
// boundaries; a batch accumulates whole groups until it reaches
// reconcileResetBatchSize rows, so a group is never split across transactions
// (each triple's delete-manual / reset-auto / clear-backoff stays atomic).
func (d *DB) reconcileMissingGroups(
	subMissing map[stateTripleInfo][]reconcileEntry,
	subPresent map[stateTripleInfo]bool,
) (int64, error) {
	if len(subMissing) == 0 {
		return 0, nil
	}

	keys := make([]stateTripleInfo, 0, len(subMissing))
	for k := range subMissing {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].mt != keys[j].mt {
			return keys[i].mt < keys[j].mt
		}
		if keys[i].mid != keys[j].mid {
			return keys[i].mid < keys[j].mid
		}
		return keys[i].lang < keys[j].lang
	})

	var resetCount int64
	for start := 0; start < len(keys); {
		// Accumulate whole groups into one batch until the row budget is hit.
		end, rows := start, 0
		for end < len(keys) && (end == start || rows < reconcileResetBatchSize) {
			rows += len(subMissing[keys[end]])
			end++
		}
		n, err := d.reconcileResetBatch(keys[start:end], subMissing, subPresent)
		if err != nil {
			return resetCount, err
		}
		resetCount += n
		start = end
	}
	return resetCount, nil
}

// reconcileResetBatch applies the sub-missing branches for one bounded batch of
// triples in a single all-or-none Update, returning how many triples were reset
// (the all-subtitles-gone branch). Per triple:
//
//   - if a sibling subtitle is still present (subPresent[triple]): delete ONLY
//     the missing-subtitle rows via the deleteState chokepoint, preserving the
//     remaining rows and any manual lock, clearing no backoff and resetting
//     nothing (Requirement 7.2);
//   - otherwise (all subtitles for the triple gone): delete the manual rows and
//     reset the auto rows in place (clear path/score/provider/release_name, bump
//     media_imported to now) via putState, then clear the triple's backoff;
//     counted once in resetCount (Requirement 7.3).
//
// All mutations route through the deleteState / putState / clearTripleBackoff
// chokepoints so the secondary indexes and the downloads/attempts counters stay
// consistent in the same transaction.
func (d *DB) reconcileResetBatch(
	keys []stateTripleInfo,
	subMissing map[stateTripleInfo][]reconcileEntry,
	subPresent map[stateTripleInfo]bool,
) (int64, error) {
	var resetCount int64
	err := d.db.Update(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if sb == nil {
			return errors.New("boltstore: subtitle_state bucket not found")
		}
		for _, tr := range keys {
			reset, rerr := reconcileTriple(tx, sb, tr, subMissing[tr], subPresent[tr])
			if rerr != nil {
				return rerr
			}
			if reset {
				resetCount++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return resetCount, nil
}

// reconcileTriple applies the missing-subtitle branches for one triple inside an
// open Update. When a sibling subtitle is still present it deletes ONLY the
// missing-subtitle rows (preserving the remaining rows and any manual lock,
// clearing no backoff) and reports reset=false (Requirement 7.2). Otherwise all
// subtitles for the triple are gone, so it resets the whole triple (delete
// manual rows, reset auto rows in place, clear backoff) via reconcileResetTriple
// and reports reset=true (Requirement 7.3).
func reconcileTriple(tx *bolt.Tx, sb *bolt.Bucket, tr stateTripleInfo, missing []reconcileEntry, siblingPresent bool) (bool, error) {
	if siblingPresent {
		return false, deleteMissingRows(tx, tr, missing)
	}
	if err := reconcileResetTriple(tx, sb, tr, missing); err != nil {
		return false, err
	}
	return true, nil
}

// deleteMissingRows deletes only the missing-subtitle rows of a triple (the
// sibling-still-present branch) via the deleteState chokepoint, preserving the
// remaining rows and any manual lock.
func deleteMissingRows(tx *bolt.Tx, tr stateTripleInfo, missing []reconcileEntry) error {
	for _, e := range missing {
		if _, derr := deleteState(tx, tr.mt, tr.mid, tr.lang, e.id); derr != nil {
			return derr
		}
	}
	return nil
}

// reconcileResetTriple applies the all-subtitles-gone branch for a single
// triple inside an open Update: delete each manual row, reset each auto row in
// place, then clear the triple's backoff. An auto row is reset by clearing
// path/score/provider/release_name and bumping media_imported to now (matching
// the old `UPDATE ... SET path=”, score=0, provider=”, release_name=”,
// media_imported=CURRENT_TIMESTAMP`), preserving its surrogate id, video_path,
// and the auto (manual=false) flag. The reset routes through putState so the
// changed media_imported re-keys ix_state_imported and the cleared score/
// provider rewrites the ix_state_triple projection in the same tx.
func reconcileResetTriple(tx *bolt.Tx, sb *bolt.Bucket, tr stateTripleInfo, missing []reconcileEntry) error {
	now := time.Now()
	for _, e := range missing {
		if e.manual {
			if _, derr := deleteState(tx, tr.mt, tr.mid, tr.lang, e.id); derr != nil {
				return derr
			}
			continue
		}
		raw := sb.Get(stateKey(e.id))
		if raw == nil {
			continue // row vanished between snapshot and now; nothing to reset
		}
		var sr stateRec
		if derr := boltkv.Decode(raw, &sr); derr != nil {
			return fmt.Errorf("boltstore: decode subtitle_state id=%d: %w", e.id, derr)
		}
		sr.Path = ""
		sr.Score = 0
		sr.Provider = ""
		sr.ReleaseName = ""
		sr.MediaImported = now
		if perr := putState(tx, tr.mt, tr.mid, tr.lang, &sr); perr != nil {
			return perr
		}
	}
	// Clear the triple's adaptive backoff so the next scan re-searches.
	return clearTripleBackoff(tx, tr.mt, tr.mid, tr.lang)
}
