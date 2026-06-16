package boltstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	bolt "go.etcd.io/bbolt"
	"pgregory.net/rapid"
)

// This file holds the END-TO-END index-equals-rescan property (task 9.3,
// Requirements 14.3, 18.5). It is the public-method sibling of
// TestIndexHelpers_indexEqualsRescan in index_test.go: where that one drives the
// typed wiring chokepoints (putState/deleteState/...) directly, this one drives
// the PUBLIC api.Store domain methods (SaveDownload, RecordNoResult,
// RecordSubtitleFiles, RecordScanState, ClearManualLock, DeleteStateByPaths,
// ReconcileState) through a rich op alphabet, keys drawn from a SMALL pool to
// force collisions, in-place upserts, and deletes. After every op sequence it
// asserts the secondary indexes, the ix_state_triple projection, and the O(1)
// meta counters all agree with a full primary re-scan. This is a strictly
// stronger invariant than the helper-level test because the multi-bucket domain
// methods (SaveDownload clearing backoff, DeleteStateByPaths fan-out, the
// three-way ReconcileState) must each leave every index consistent.
//
// # Oracle design
//
// The primary buckets are the source of truth. For the indexes whose key
// derives entirely from a primary value (ix_state_imported, ix_state_video,
// ix_attempts_due, ix_scan_at) the expectation is rebuilt by re-scanning the
// primary and compared exactly (compareIndex). The maintained counters are
// compared to a primary row count (verifyCounters). ix_state_triple is special:
// its key encodes the (media_type, media_id, language) triple, which stateRec
// deliberately does NOT store (the triple lives only in the index key, see
// codec.go), so it cannot be rebuilt from a primary value alone. It is instead
// held to the two properties that fully pin it to the primary: a bijection by
// surrogate id (every primary row has exactly one triple entry and vice versa,
// no stale/duplicate/orphan), and a projected value that decodes to the
// primary's (manual, score, provider). The helper-level test in index_test.go
// already pins the exact triple-KEY bytes via its deterministic id->triple
// binding (tripleFor); here the bijection + projection cross-check guards the
// same buckets across the real domain methods.

// pubTriple is a (media_type, media_id, language) triple drawn by the
// public-method op driver.
type pubTriple struct {
	mt   api.MediaType
	mid  string
	lang string
}

// pubTriples is the small triple pool. "tt1" appears under two languages and a
// media id is shared across types so triple- and media-prefix collisions are
// forced; the pool stays tiny so random sequences repeatedly hit the same
// triples (upserts, manual stacking, deletes).
var pubTriples = []pubTriple{
	{api.MediaTypeMovie, "tt1", "en"},
	{api.MediaTypeMovie, "tt1", "fr"},
	{api.MediaTypeEpisode, "tt2", "en"},
}

// pubVideos is the small video-path pool. A single video path backs rows across
// several triples, so a DeleteStateByPaths / video-gone reconcile fans out over
// multiple rows (exercising ix_state_video prefix scans and orphan cleanup).
var pubVideos = []string{"/m/a.mkv", "/m/b.mkv"}

// pubScanMedia is the (media_type, media_id) pool for scan_state rows.
var pubScanMedia = []struct {
	mt  api.MediaType
	mid string
}{
	{api.MediaTypeMovie, "tt1"},
	{api.MediaTypeEpisode, "tt2"},
}

// pubBackoffParams are fixed backoff parameters for RecordNoResult; the exact
// next_retry does not matter to the index invariant, only that the row and its
// due-index entry stay consistent.
var pubBackoffParams = api.BackoffParams{
	InitialDelay: time.Minute,
	MaxDelay:     time.Hour,
	Multiplier:   2,
}

// statEnv is the mutable filesystem oracle the ReconcileState op drives. The
// store's statFn closes over it; an op flips which paths are "gone" before a
// reconcile so the three-way branches (video gone / subtitle gone / present)
// are all exercised. It is only ever mutated from the single property goroutine.
type statEnv struct {
	gone map[string]bool
}

// pubAutoPath / pubManualPath build the subtitle paths SaveDownload stores, so
// the ReconcileState op can mark them gone to drive the subtitle-missing branch.
func pubAutoPath(mid, lang string) string { return fmt.Sprintf("/m/%s.%s.srt", mid, lang) }

func pubManualPath(mid, lang string, ord int) string {
	return fmt.Sprintf("/m/%s.%s.%d.srt", mid, lang, ord)
}

// allPubSubPaths is every subtitle path the SaveDownload op can produce, used as
// the candidate set the ReconcileState op marks gone.
func allPubSubPaths() []string {
	var out []string
	for _, tr := range pubTriples {
		out = append(out, pubAutoPath(tr.mid, tr.lang))
		for ord := 1; ord <= 3; ord++ {
			out = append(out, pubManualPath(tr.mid, tr.lang, ord))
		}
	}
	return out
}

// TestPublicStore_indexEqualsRescan drives the public api.Store domain methods
// with a rich op alphabet and a small key pool, then asserts every index, the
// ix_state_triple projection, and the maintained counters equal a full primary
// re-scan (Requirements 14.3, 18.5).
func TestPublicStore_indexEqualsRescan(t *testing.T) {
	ctx := context.Background()
	rapid.Check(t, func(rt *rapid.T) {
		db := openPropDB(rt)
		env := &statEnv{gone: map[string]bool{}}
		// The reconcile oracle: a path is present unless the current op marked
		// it gone. classifyReconcileEntry only inspects os.ErrNotExist, so a
		// present path returns (nil, nil).
		db.statFn = func(path string) (os.FileInfo, error) {
			if env.gone[path] {
				return nil, os.ErrNotExist
			}
			return nil, nil
		}

		n := rapid.IntRange(0, 60).Draw(rt, "ops")
		for range n {
			applyPublicOp(rt, db, ctx, env)
		}

		if err := db.db.View(func(tx *bolt.Tx) error {
			verifyStateIndexesEndToEnd(rt, tx)
			verifyAttemptIndex(rt, tx)
			verifyScanIndex(rt, tx)
			verifyCounters(rt, tx)
			return nil
		}); err != nil {
			rt.Fatalf("verify View: %v", err)
		}
	})
}

// applyPublicOp draws one operation from the public-method op alphabet and
// applies it through the real api.Store entry points.
func applyPublicOp(rt *rapid.T, db *DB, ctx context.Context, env *statEnv) {
	op := rapid.SampledFrom([]string{
		"saveAuto", "saveManual",
		"recordNoResult",
		"recordFiles",
		"recordScan",
		"clearLock",
		"deleteByPaths",
		"reconcile",
	}).Draw(rt, "op")

	switch op {
	case "saveAuto", "saveManual":
		tr := rapid.SampledFrom(pubTriples).Draw(rt, "saveTriple")
		manual := op == "saveManual"
		var path string
		if manual {
			path = pubManualPath(tr.mid, tr.lang, rapid.IntRange(1, 3).Draw(rt, "ordinal"))
		} else {
			path = pubAutoPath(tr.mid, tr.lang)
		}
		rec := &api.DownloadRecord{
			MediaType:    tr.mt,
			MediaID:      tr.mid,
			Language:     tr.lang,
			ProviderName: rapid.SampledFrom(providerPool).Draw(rt, "saveProvider"),
			ReleaseName:  rapid.SampledFrom([]string{"", "Rel.A", "Rel.B"}).Draw(rt, "saveRelease"),
			Path:         path,
			Score:        rapid.IntRange(0, 100).Draw(rt, "saveScore"),
			Meta: &api.DownloadMeta{
				Title:     "T",
				VideoPath: rapid.SampledFrom(pubVideos).Draw(rt, "saveVideo"),
				Manual:    manual,
			},
		}
		if err := db.SaveDownload(ctx, rec); err != nil {
			rt.Fatalf("SaveDownload: %v", err)
		}

	case "recordNoResult":
		tr := rapid.SampledFrom(pubTriples).Draw(rt, "noResultTriple")
		p := rapid.SampledFrom(providerPool).Draw(rt, "noResultProvider")
		if err := db.RecordNoResult(ctx, tr.mt, tr.mid, tr.lang, p, pubBackoffParams); err != nil {
			rt.Fatalf("RecordNoResult: %v", err)
		}

	case "recordFiles":
		m := rapid.SampledFrom(pubScanMedia).Draw(rt, "filesMedia")
		k := rapid.IntRange(0, 3).Draw(rt, "fileCount")
		var files []api.SubtitleFile
		for i := range k {
			lang := rapid.SampledFrom([]string{"en", "fr"}).Draw(rt, "fileLang")
			files = append(files, api.SubtitleFile{
				Language: lang,
				Variant:  rapid.SampledFrom([]api.Variant{api.VariantStandard, api.VariantHI}).Draw(rt, "fileVariant"),
				Source:   api.SourceExternal,
				Codec:    rapid.SampledFrom([]string{"subrip", "ass"}).Draw(rt, "fileCodec"),
				Path:     fmt.Sprintf("/m/%s.%s.%d.srt", m.mid, lang, i),
			})
		}
		if _, err := db.RecordSubtitleFiles(ctx, m.mt, m.mid, files); err != nil {
			rt.Fatalf("RecordSubtitleFiles: %v", err)
		}

	case "recordScan":
		m := rapid.SampledFrom(pubScanMedia).Draw(rt, "scanMedia")
		if err := db.RecordScanState(ctx, &api.ScanRecord{
			MediaType: m.mt, MediaID: m.mid, Title: "t", AudioLang: "en",
			Season:  rapid.IntRange(0, 3).Draw(rt, "scanSeason"),
			Episode: rapid.IntRange(0, 12).Draw(rt, "scanEpisode"),
		}); err != nil {
			rt.Fatalf("RecordScanState: %v", err)
		}

	case "clearLock":
		tr := rapid.SampledFrom(pubTriples).Draw(rt, "clearTriple")
		if err := db.ClearManualLock(ctx, tr.mt, tr.mid, tr.lang); err != nil {
			rt.Fatalf("ClearManualLock: %v", err)
		}

	case "deleteByPaths":
		var paths []string
		for _, v := range pubVideos {
			if rapid.Bool().Draw(rt, "delVideo:"+v) {
				paths = append(paths, v)
			}
		}
		if len(paths) == 0 {
			paths = []string{rapid.SampledFrom(pubVideos).Draw(rt, "delVideoFallback")}
		}
		if _, err := db.DeleteStateByPaths(ctx, paths); err != nil {
			rt.Fatalf("DeleteStateByPaths: %v", err)
		}

	case "reconcile":
		// Choose which video and subtitle paths are "gone" for this pass.
		gone := map[string]bool{}
		for _, v := range pubVideos {
			if rapid.Bool().Draw(rt, "goneVideo:"+v) {
				gone[v] = true
			}
		}
		for _, s := range allPubSubPaths() {
			if rapid.Bool().Draw(rt, "goneSub:"+s) {
				gone[s] = true
			}
		}
		env.gone = gone
		if _, err := db.ReconcileState(ctx); err != nil {
			rt.Fatalf("ReconcileState: %v", err)
		}
		env.gone = map[string]bool{} // reset so later non-reconcile ops are unaffected
	}
}

// verifyStateIndexesEndToEnd rebuilds ix_state_imported and ix_state_video from
// a full subtitle_state primary re-scan and compares them exactly, then checks
// ix_state_triple is a bijection (by surrogate id) with the primary whose
// projected value decodes to the primary's (manual, score, provider). See the
// file-level "Oracle design" note for why ix_state_triple is checked this way
// rather than by exact key bytes.
func verifyStateIndexesEndToEnd(rt *rapid.T, tx *bolt.Tx) {
	rt.Helper()
	prim := map[int64]stateRec{}
	wantImported := map[string]string{}
	wantVideo := map[string]string{}

	_ = tx.Bucket([]byte(bucketSubtitleState)).ForEach(func(k, v []byte) error {
		id, ok := parseStateKey(k)
		if !ok {
			rt.Errorf("subtitle_state: non-8-byte primary key %x", k)
			return nil
		}
		var rec stateRec
		if err := decodeRec(v, &rec); err != nil {
			rt.Errorf("subtitle_state: decode id %d: %v", id, err)
			return nil
		}
		prim[id] = rec
		wantImported[string(stateImportedKey(rec.MediaImported, id))] = ""
		wantVideo[string(stateVideoKey(rec.VideoPath, id))] = ""
		return nil
	})

	compareIndex(rt, "ix_state_imported", wantImported, bucketMap(tx, bucketIxStateImported))
	compareIndex(rt, "ix_state_video", wantVideo, bucketMap(tx, bucketIxStateVideo))

	// ix_state_triple bijection + projection cross-check.
	seen := map[int64]bool{}
	_ = tx.Bucket([]byte(bucketIxStateTriple)).ForEach(func(k, v []byte) error {
		id, ok := stateTripleKeyID(k)
		if !ok {
			rt.Errorf("ix_state_triple: malformed key %x", k)
			return nil
		}
		rec, ok := prim[id]
		if !ok {
			rt.Errorf("ix_state_triple: orphan entry for id %d (no primary row)", id)
			return nil
		}
		if seen[id] {
			rt.Errorf("ix_state_triple: duplicate entry for id %d", id)
		}
		seen[id] = true
		manual, score, provider, ok := decodeStateProjection(v)
		if !ok {
			rt.Errorf("ix_state_triple: undecodable projection for id %d: %x", id, v)
			return nil
		}
		if manual != rec.Manual || score != rec.Score || provider != rec.Provider {
			rt.Errorf("ix_state_triple projection for id %d = (manual=%v, score=%d, provider=%q), want (%v, %d, %q)",
				id, manual, score, provider, rec.Manual, rec.Score, rec.Provider)
		}
		return nil
	})
	for id := range prim {
		if !seen[id] {
			rt.Errorf("subtitle_state id %d has no ix_state_triple entry", id)
		}
	}
}
