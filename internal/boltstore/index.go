package boltstore

import (
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file is the boltstore-specific WIRING layer over the generic
// index-maintenance helpers in internal/store/kv (kv). It declares each
// secondary index as a concrete kv.IndexSpec[T], declares the O(1)
// maintained meta counters, and exposes the typed put/delete helpers the
// per-bucket domain methods (tasks 3-7) call.
//
// These helpers are the single chokepoint for the secondary-index maintenance
// invariant (design: "Secondary-index maintenance invariant"): every primary
// write routes through kv.PutIndexed / kv.DeleteIndexed with the right
// IndexSpecs and CounterSpecs, so each write maintains its indexes AND its
// counters in the same all-or-nothing transaction. Index maintenance is never
// hand-rolled at the call sites; they only choose which helper to call.
//
// # Why some specs are built per call
//
// kv derives an index entry's key from (primaryKey, *record). That is
// enough for search_attempts and scan_state, whose primary key already carries
// every component an index needs, so their IndexSpecs are static.
//
// subtitle_state is different: its primary key is the bare be64(id) surrogate,
// and stateRec deliberately omits media_type / media_id / language (those live
// in the ix_state_triple index key, not the value; see codec.go). So the
// triple components cannot be recovered from (primaryKey, *stateRec) and MUST
// be supplied by the caller. stateIndexes therefore closes over (mt, mid, lang)
// and the typed putState / deleteState helpers take them as parameters. A given
// surrogate id belongs to exactly one triple for its whole life, so the
// read-old-delete-stale step inside kv always rebuilds the same triple key
// it originally wrote.

// --- Maintained meta counters ---
//
// bbolt has no O(1) live row count (Bucket.Sequence is a high-water mark, not a
// count), so the read-heavy Stats and TotalSubtitleFiles paths are served from
// integer counters kept in the meta bucket and moved inside the same Update as
// the row write. The counters mirror the old SQLite COUNT(*) queries exactly:
//
//   - downloads  = COUNT(*) subtitle_state   (Stats)
//   - attempts   = COUNT(*) search_attempts  (Stats)
//   - subtitle_files = COUNT(*) subtitle_files (TotalSubtitleFiles)
//
// kv only moves a counter on a genuine insert/delete (not on an update of
// an existing key), so the counter tracks row existence, matching COUNT(*).

var (
	metaKeyDownloadCount = []byte("cnt_downloads")      // subtitle_state row count
	metaKeyAttemptCount  = []byte("cnt_attempts")       // search_attempts row count
	metaKeyFileCount     = []byte("cnt_subtitle_files") // subtitle_files row count
)

// downloadCounters maintains the subtitle_state row counter (Stats.downloads).
func downloadCounters() []kv.CounterSpec {
	return []kv.CounterSpec{{Bucket: bucketMeta, Key: metaKeyDownloadCount}}
}

// attemptCounters maintains the search_attempts row counter (Stats.attempts).
func attemptCounters() []kv.CounterSpec {
	return []kv.CounterSpec{{Bucket: bucketMeta, Key: metaKeyAttemptCount}}
}

// fileCounters maintains the subtitle_files row counter (TotalSubtitleFiles).
func fileCounters() []kv.CounterSpec {
	return []kv.CounterSpec{{Bucket: bucketMeta, Key: metaKeyFileCount}}
}

// readDownloadCount returns the maintained subtitle_state row count (O(1)),
// used by Stats (task 4.4). Returns 0 when the counter is unset.
func readDownloadCount(tx *bolt.Tx) int64 {
	return kv.ReadCounter(tx.Bucket([]byte(bucketMeta)), metaKeyDownloadCount)
}

// readAttemptCount returns the maintained search_attempts row count (O(1)),
// used by Stats (task 4.4). Returns 0 when the counter is unset.
func readAttemptCount(tx *bolt.Tx) int64 {
	return kv.ReadCounter(tx.Bucket([]byte(bucketMeta)), metaKeyAttemptCount)
}

// readFileCount returns the maintained subtitle_files row count (O(1)), used by
// TotalSubtitleFiles (task 5.1). Returns 0 when the counter is unset.
func readFileCount(tx *bolt.Tx) int64 {
	return kv.ReadCounter(tx.Bucket([]byte(bucketMeta)), metaKeyFileCount)
}

// --- ix_state_triple projected-value codec ---
//
// ix_state_triple carries a small projection of (manual, score, provider) as
// its index entry value so the hot read paths (IsManuallyLocked,
// ManualDownloadCount, DownloadedRefs, CurrentScore) answer from the index walk
// alone, never dereferencing the primary subtitle_state row. The full row is
// decoded only for detail reads (CLI `state`). The index-equals-rescan property
// test cross-checks this projection against the primary so it cannot drift.
//
// Layout: byte 0 = manual flag (0/1); bytes 1..9 = be64(uint64(int64(score)))
// (fixed width so the provider boundary is unambiguous); bytes 9.. = provider
// string. Score is stored as a reinterpreted int64 so a negative value (none
// expected, but defensive) still round-trips.

const stateProjectionMinLen = 1 + 8 // manual byte + be64 score

// encodeStateProjection serialises the ix_state_triple projected value from a
// subtitle_state record's (manual, score, provider).
func encodeStateProjection(manual bool, score int, provider api.ProviderID) []byte {
	buf := make([]byte, 0, stateProjectionMinLen+len(provider))
	var m byte
	if manual {
		m = 1
	}
	buf = append(buf, m)
	buf = append(buf, kv.Be64(uint64(int64(score)))...) //nolint:gosec // G115: reinterpret for fixed-width round-trip
	buf = append(buf, provider...)
	return buf
}

// decodeStateProjection parses an ix_state_triple projected value back into its
// (manual, score, provider) fields. ok is false for a value shorter than the
// fixed manual+score prefix.
func decodeStateProjection(b []byte) (manual bool, score int, provider api.ProviderID, ok bool) {
	if len(b) < stateProjectionMinLen {
		return false, 0, "", false
	}
	manual = b[0] == 1
	v, _ := kv.DecodeBe64(b[1:stateProjectionMinLen])
	score = int(int64(v)) //nolint:gosec // G115: inverse of encodeStateProjection
	provider = api.ProviderID(b[stateProjectionMinLen:])
	return manual, score, provider, true
}

// --- IndexSpec declarations ---

// stateIndexes declares the three subtitle_state secondary indexes for the
// triple (mt, mid, lang) the row belongs to:
//
//   - ix_state_triple   (mt 0x00 mid 0x00 lang 0x00 be64(id)) carrying the
//     (manual, score, provider) projection
//   - ix_state_imported (be64(media_imported) 0x00 be64(id)), existence-only
//   - ix_state_video    (video_path 0x00 be64(id)), existence-only
//
// The triple is closed over because it is not stored in stateRec (see the
// file-level note). ix_state_imported / ix_state_video keys derive entirely
// from the record, so on a reset that changes media_imported (or a row whose
// video_path changes) kv's read-old-delete-stale step removes the old entry
// and adds the new one in the same tx.
func stateIndexes(mt api.MediaType, mid, lang string) []kv.IndexSpec[stateRec] {
	return []kv.IndexSpec[stateRec]{
		{
			Bucket: bucketIxStateTriple,
			Key: func(_ []byte, v *stateRec) []byte {
				return stateTripleKey(mt, mid, lang, v.ID)
			},
			Value: func(_ []byte, v *stateRec) []byte {
				return encodeStateProjection(v.Manual, v.Score, v.Provider)
			},
		},
		{
			Bucket: bucketIxStateImported,
			Key: func(_ []byte, v *stateRec) []byte {
				return stateImportedKey(v.MediaImported, v.ID)
			},
		},
		{
			Bucket: bucketIxStateVideo,
			Key: func(_ []byte, v *stateRec) []byte {
				return stateVideoKey(v.VideoPath, v.ID)
			},
		},
	}
}

// attemptIndexes declares the search_attempts secondary index ix_attempts_due
// (be64(next_retry) 0x00 primary), existence-only. The primary key already
// carries (mt, mid, lang, provider), so the spec is static: it derives the due
// key from the record's NextRetry and the primary key kv passes in.
func attemptIndexes() []kv.IndexSpec[attemptRec] {
	return []kv.IndexSpec[attemptRec]{{
		Bucket: bucketIxAttemptsDue,
		Key: func(pk []byte, v *attemptRec) []byte {
			return attemptsDueKey(v.NextRetry, pk)
		},
	}}
}

// scanIndexes declares the scan_state secondary index ix_scan_at
// (be64(scanned_at) 0x00 primary), existence-only. The primary key carries
// (mt, mid), so the spec is static.
func scanIndexes() []kv.IndexSpec[scanRec] {
	return []kv.IndexSpec[scanRec]{{
		Bucket: bucketIxScanAt,
		Key: func(pk []byte, v *scanRec) []byte {
			return scanAtKey(v.ScannedAt, pk)
		},
	}}
}

// --- Typed write/delete helpers (the index-maintenance chokepoints) ---

// putState inserts or updates a subtitle_state row (keyed by its surrogate id)
// and maintains the three triple indexes and the downloads counter, all in tx.
// The caller supplies (mt, mid, lang) because they are not stored in the record
// but are needed to build the ix_state_triple key. rec.ID must already be
// allocated (via kv.NextID on the subtitle_state bucket).
func putState(tx *bolt.Tx, mt api.MediaType, mid, lang string, rec *stateRec) error {
	return kv.PutIndexed(tx, bucketSubtitleState, stateKey(rec.ID), rec,
		stateIndexes(mt, mid, lang), downloadCounters())
}

// deleteState removes the subtitle_state row with the given surrogate id, its
// three triple index entries, and decrements the downloads counter, all in tx.
// (mt, mid, lang) identify the triple for the ix_state_triple key. It is
// idempotent: deleting an absent id is a no-op and returns existed=false.
func deleteState(tx *bolt.Tx, mt api.MediaType, mid, lang string, id int64) (existed bool, err error) {
	return kv.DeleteIndexed(tx, bucketSubtitleState, stateKey(id),
		stateIndexes(mt, mid, lang), downloadCounters())
}

// putAttempt inserts or updates a search_attempts row and maintains the
// ix_attempts_due index and the attempts counter, all in tx.
func putAttempt(tx *bolt.Tx, mt api.MediaType, mid, lang string, p api.ProviderID, rec *attemptRec) error {
	return kv.PutIndexed(tx, bucketSearchAttempts, attemptKey(mt, mid, lang, p), rec,
		attemptIndexes(), attemptCounters())
}

// deleteAttempt removes a search_attempts row, its ix_attempts_due entry, and
// decrements the attempts counter, all in tx. Idempotent on an absent key.
func deleteAttempt(tx *bolt.Tx, mt api.MediaType, mid, lang string, p api.ProviderID) (existed bool, err error) {
	return kv.DeleteIndexed[attemptRec](tx, bucketSearchAttempts, attemptKey(mt, mid, lang, p),
		attemptIndexes(), attemptCounters())
}

// putScanState upserts a scan_state row (one per (mt, mid)) and maintains the
// ix_scan_at index, all in tx. scan_state has no maintained counter.
func putScanState(tx *bolt.Tx, mt api.MediaType, mid string, rec *scanRec) error {
	return kv.PutIndexed(tx, bucketScanState, scanStateKey(mt, mid), rec,
		scanIndexes(), nil)
}

// deleteScanState removes a scan_state row and its ix_scan_at entry, all in tx.
// Idempotent on an absent key. Used by reconcile/orphan cleanup (task 6).
func deleteScanState(tx *bolt.Tx, mt api.MediaType, mid string) (existed bool, err error) {
	return kv.DeleteIndexed[scanRec](tx, bucketScanState, scanStateKey(mt, mid),
		scanIndexes(), nil)
}

// putSubtitleFile inserts or updates a subtitle_files row at the supplied
// composite key (built by subtitleFileKey) and maintains the total-files
// counter, all in tx. subtitle_files has no secondary index (per-media coverage
// is a key-only prefix walk), but it DOES maintain the TotalSubtitleFiles
// counter, so writes still route through this chokepoint rather than a bare
// bucket Put.
func putSubtitleFile(tx *bolt.Tx, key []byte, rec *fileRec) error {
	return kv.PutIndexed[fileRec](tx, bucketSubtitleFiles, key, rec, nil, fileCounters())
}

// deleteSubtitleFile removes the subtitle_files row at the supplied composite
// key and decrements the total-files counter, all in tx. Idempotent on an
// absent key.
func deleteSubtitleFile(tx *bolt.Tx, key []byte) (existed bool, err error) {
	return kv.DeleteIndexed[fileRec](tx, bucketSubtitleFiles, key, nil, fileCounters())
}
