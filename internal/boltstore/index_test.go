package boltstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	bolt "go.etcd.io/bbolt"
	"pgregory.net/rapid"
)

// This file proves the secondary-index maintenance invariant for the boltstore
// wiring helpers in index.go: after any sequence of put/delete operations, every
// index bucket equals what a full re-scan of its primary would produce (no
// stale, duplicate, or missing entries), the ix_state_quad projection matches
// the primary record's (manual, score, provider), and the maintained meta
// counters equal a full primary count. Keys are drawn from small pools so the
// random sequences force collisions, updates, and deletes.

// --- Fixed pools (small, to force collisions / updates / deletes) ---

type tripleKey struct {
	mt   api.MediaType
	mid  string
	lang string
}

// statePool binds each surrogate id to exactly one language triple; together
// with variantPool it binds each id to exactly one QUAD (an id belongs to a
// single quad for its whole life). "tt1" appears under two languages to force
// prefix collisions in ix_state_quad, and the variant cycle makes one triple
// carry two different variants (quad-boundary collisions within a language).
var statePool = []tripleKey{
	{api.MediaTypeMovie, "tt1", "en"},
	{api.MediaTypeMovie, "tt1", "fr"},
	{api.MediaTypeEpisode, "tt2", "en"},
}

var variantPool = []api.Variant{api.VariantStandard, api.VariantForced}

// quadFor maps a surrogate id to its (deterministic) quad, so both the write
// path and the verification re-derive the same ix_state_quad key without a
// separate model.
func quadFor(id int64) (tripleKey, api.Variant) {
	return statePool[int(id-1)%len(statePool)], variantPool[int(id-1)%len(variantPool)]
}

var providerPool = []api.ProviderID{
	api.ProviderNameOpenSubtitles,
	api.ProviderNameGestdown,
}

var videoPathPool = []string{"/media/a.mkv", "/media/b.mkv"}

// scanMediaPool is the (mt, mid) pool for scan_state rows.
var scanMediaPool = []tripleKey{
	{mt: api.MediaTypeMovie, mid: "tt1"},
	{mt: api.MediaTypeEpisode, mid: "tt2"},
}

type fileSpec struct {
	mt      api.MediaType
	mid     string
	lang    string
	variant api.Variant
	source  api.SubtitleSource
	path    string
}

func (f fileSpec) key() []byte {
	return subtitleFileKey(f.mt, f.mid, f.lang, f.variant, f.source, f.path)
}

var fileSpecPool = []fileSpec{
	{api.MediaTypeMovie, "tt1", "en", api.VariantStandard, api.SourceExternal, "/media/a.en.srt"},
	{api.MediaTypeMovie, "tt1", "fr", api.VariantHI, api.SourceExternal, "/media/a.fr.srt"},
	{api.MediaTypeEpisode, "tt2", "en", api.VariantStandard, api.SourceEmbedded, ""},
	{api.MediaTypeEpisode, "tt2", "en", api.VariantStandard, api.SourceExternal, "/media/b.en.srt"},
}

var baseTime = time.Unix(1_700_000_000, 0).UTC()

func timeAtHour(h int) time.Time { return baseTime.Add(time.Duration(h) * time.Hour) }

// openPropDB opens a fresh store for one rapid iteration. Each iteration gets
// its own temp directory (os.MkdirTemp; rapid.T has no TempDir) so the file is
// isolated, and both the handle and the directory are torn down via rt.Cleanup.
func openPropDB(rt *rapid.T) *DB {
	rt.Helper()
	dir, err := os.MkdirTemp("", "boltprop-")
	if err != nil {
		rt.Fatalf("MkdirTemp: %v", err)
	}
	rt.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "subflux.bolt")
	db, err := Open(path)
	if err != nil {
		rt.Fatalf("Open(%q): %v", path, err)
	}
	db.db.StrictMode = true // consistency check every commit (test-only)
	rt.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// bucketMap copies a bucket's entries into a map of string(key) -> string(value)
// (values copied out of the tx). Used to compare actual index buckets against
// the primary-derived expectation.
func bucketMap(tx *bolt.Tx, name string) map[string]string {
	out := map[string]string{}
	_ = tx.Bucket([]byte(name)).ForEach(func(k, v []byte) error {
		out[string(k)] = string(v)
		return nil
	})
	return out
}

// compareIndex asserts got equals want exactly (same keys, same values),
// reporting the first missing / orphan / mismatched entry.
func compareIndex(rt *rapid.T, name string, want, got map[string]string) {
	rt.Helper()
	if len(got) != len(want) {
		rt.Errorf("%s: %d entries, want %d (= primary rescan)", name, len(got), len(want))
	}
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			rt.Errorf("%s: missing entry for primary-derived key %x", name, k)
			continue
		}
		if gv != wv {
			rt.Errorf("%s: value for %x = %x, want %x", name, k, gv, wv)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			rt.Errorf("%s: orphan entry %x has no matching primary row", name, k)
		}
	}
}

// TestIndexHelpers_indexEqualsRescan is the index-equals-rescan property test
// (design Requirement 8.1). It drives a random op sequence through the typed
// wiring helpers, then asserts every index bucket, the ix_state_quad
// projection, and the maintained counters all agree with a full primary
// re-scan.
func TestIndexHelpers_indexEqualsRescan(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := openPropDB(rt)

		n := rapid.IntRange(0, 80).Draw(rt, "ops")
		for range n {
			drawAndApplyOp(rt, db)
		}

		if err := db.db.View(func(tx *bolt.Tx) error {
			verifyStateIndexes(rt, tx)
			verifyScanIndex(rt, tx)
			verifyCounters(rt, tx)
			return nil
		}); err != nil {
			rt.Fatalf("verify View: %v", err)
		}
	})
}

// drawAndApplyOp draws one operation from the op alphabet and applies it inside
// its own Update, mirroring how a domain method would route a single primary
// write through the index-maintenance chokepoints.
func drawAndApplyOp(rt *rapid.T, db *DB) {
	op := rapid.SampledFrom([]string{
		"putStateAuto", "putStateManual", "deleteState",
		"putAttempt", "deleteAttempt",
		"putScan",
		"putFile", "deleteFile",
	}).Draw(rt, "op")

	err := db.db.Update(func(tx *bolt.Tx) error {
		switch op {
		case "putStateAuto", "putStateManual":
			id := rapid.Int64Range(1, 4).Draw(rt, "stateID")
			tr, variant := quadFor(id)
			rec := stateRec{
				ID:            id,
				MediaType:     tr.mt,
				MediaID:       tr.mid,
				Language:      tr.lang,
				Variant:       variant,
				Provider:      rapid.SampledFrom(providerPool).Draw(rt, "stateProvider"),
				ReleaseName:   rapid.SampledFrom([]string{"", "Rel.A", "Rel.B"}).Draw(rt, "release"),
				Score:         rapid.IntRange(0, 100).Draw(rt, "score"),
				Manual:        op == "putStateManual",
				VideoPath:     rapid.SampledFrom(videoPathPool).Draw(rt, "videoPath"),
				MediaImported: timeAtHour(rapid.IntRange(0, 12).Draw(rt, "imported")),
			}
			return putState(tx, &rec)
		case "deleteState":
			id := rapid.Int64Range(1, 4).Draw(rt, "delStateID")
			_, err := deleteState(tx, id)
			return err
		case "putAttempt":
			tr := rapid.SampledFrom(statePool).Draw(rt, "attemptTriple")
			p := rapid.SampledFrom(providerPool).Draw(rt, "attemptProvider")
			rec := attemptRec{
				LastTried: timeAtHour(rapid.IntRange(0, 12).Draw(rt, "lastTried")),
				NextRetry: timeAtHour(rapid.IntRange(0, 12).Draw(rt, "nextRetry")),
				Failures:  rapid.IntRange(0, 5).Draw(rt, "failures"),
			}
			return putAttempt(tx, tr.mt, tr.mid, tr.lang, p, &rec)
		case "deleteAttempt":
			tr := rapid.SampledFrom(statePool).Draw(rt, "delAttemptTriple")
			p := rapid.SampledFrom(providerPool).Draw(rt, "delAttemptProvider")
			_, err := deleteAttempt(tx, tr.mt, tr.mid, tr.lang, p)
			return err
		case "putScan":
			m := rapid.SampledFrom(scanMediaPool).Draw(rt, "scanMedia")
			rec := scanRec{
				Title:     "title",
				AudioLang: "en",
				Season:    rapid.IntRange(0, 3).Draw(rt, "season"),
				Episode:   rapid.IntRange(0, 12).Draw(rt, "episode"),
				ScannedAt: timeAtHour(rapid.IntRange(0, 12).Draw(rt, "scannedAt")),
			}
			return putScanState(tx, m.mt, m.mid, &rec)
		case "putFile":
			f := rapid.SampledFrom(fileSpecPool).Draw(rt, "putFileSpec")
			rec := fileRec{
				Codec:     rapid.SampledFrom([]string{"subrip", "ass", ""}).Draw(rt, "codec"),
				UpdatedAt: timeAtHour(rapid.IntRange(0, 12).Draw(rt, "updatedAt")),
			}
			return putSubtitleFile(tx, f.key(), &rec)
		case "deleteFile":
			f := rapid.SampledFrom(fileSpecPool).Draw(rt, "delFileSpec")
			_, err := deleteSubtitleFile(tx, f.key())
			return err
		default:
			return fmt.Errorf("unknown op %q", op)
		}
	})
	if err != nil {
		rt.Fatalf("apply op %q: %v", op, err)
	}
}

// verifyStateIndexes derives the three subtitle_state indexes from a full
// primary re-scan and compares them to the live buckets. It additionally
// cross-checks the ix_state_quad projected value by decoding it and comparing
// (manual, score, provider) back to the primary record, so the projection
// cannot silently drift.
func verifyStateIndexes(rt *rapid.T, tx *bolt.Tx) {
	rt.Helper()
	wantQuad := map[string]string{}
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
		// The quad index key derives from the record's own quad fields (the
		// self-contained value); assert those fields still match the pool
		// binding the write path used, so a drifted write can't hide behind a
		// verification that trusts the record.
		tr, variant := quadFor(id)
		if rec.MediaType != tr.mt || rec.MediaID != tr.mid || rec.Language != tr.lang || rec.Variant != variant {
			rt.Errorf("subtitle_state id %d: stored quad (%s/%s/%s/%s) != pool quad (%s/%s/%s/%s)",
				id, rec.MediaType, rec.MediaID, rec.Language, rec.Variant, tr.mt, tr.mid, tr.lang, variant)
		}
		wantQuad[string(stateQuadKey(rec.MediaType, rec.MediaID, rec.Language, rec.Variant, id))] = string(encodeStateProjection(rec.Manual, rec.Score, rec.Provider))
		wantImported[string(stateImportedKey(rec.MediaImported, id))] = ""
		wantVideo[string(stateVideoKey(rec.VideoPath, id))] = ""
		return nil
	})

	compareIndex(rt, "ix_state_quad", wantQuad, bucketMap(tx, bucketIxStateQuad))
	compareIndex(rt, "ix_state_imported", wantImported, bucketMap(tx, bucketIxStateImported))
	compareIndex(rt, "ix_state_video", wantVideo, bucketMap(tx, bucketIxStateVideo))

	// Projection cross-check: every quad index value must decode to the
	// primary record's (manual, score, provider).
	_ = tx.Bucket([]byte(bucketIxStateQuad)).ForEach(func(k, v []byte) error {
		id, ok := stateQuadKeyID(k)
		if !ok {
			rt.Errorf("ix_state_quad: malformed key %x", k)
			return nil
		}
		manual, score, provider, ok := decodeStateProjection(v)
		if !ok {
			rt.Errorf("ix_state_quad: undecodable projection for id %d: %x", id, v)
			return nil
		}
		raw := tx.Bucket([]byte(bucketSubtitleState)).Get(stateKey(id))
		if raw == nil {
			rt.Errorf("ix_state_quad: entry for id %d has no primary row", id)
			return nil
		}
		var rec stateRec
		if err := decodeRec(raw, &rec); err != nil {
			rt.Errorf("ix_state_quad: decode primary id %d: %v", id, err)
			return nil
		}
		if manual != rec.Manual || score != rec.Score || provider != rec.Provider {
			rt.Errorf("ix_state_quad projection for id %d = (manual=%v, score=%d, provider=%q), want (%v, %d, %q)",
				id, manual, score, provider, rec.Manual, rec.Score, rec.Provider)
		}
		return nil
	})
}

// verifyScanIndex derives ix_scan_at from the scan_state primary and compares
// it to the live bucket.
func verifyScanIndex(rt *rapid.T, tx *bolt.Tx) {
	rt.Helper()
	want := map[string]string{}
	_ = tx.Bucket([]byte(bucketScanState)).ForEach(func(k, v []byte) error {
		var rec scanRec
		if err := decodeRec(v, &rec); err != nil {
			rt.Errorf("scan_state: decode %x: %v", k, err)
			return nil
		}
		want[string(scanAtKey(rec.ScannedAt, k))] = ""
		return nil
	})
	compareIndex(rt, "ix_scan_at", want, bucketMap(tx, bucketIxScanAt))
}

// verifyCounters asserts the maintained meta counters equal a full primary row
// count for each counted bucket.
func verifyCounters(rt *rapid.T, tx *bolt.Tx) {
	rt.Helper()
	checks := []struct {
		name    string
		bucket  string
		counter int64
	}{
		{"cnt_downloads", bucketSubtitleState, readDownloadCount(tx)},
		{"cnt_attempts", bucketSearchAttempts, readAttemptCount(tx)},
		{"cnt_subtitle_files", bucketSubtitleFiles, readFileCount(tx)},
	}
	for _, c := range checks {
		if want := primaryRows(tx, c.bucket); c.counter != want {
			rt.Errorf("%s = %d, want %d (= %s rows)", c.name, c.counter, want, c.bucket)
		}
	}
}

// primaryRows counts the rows in a primary bucket.
func primaryRows(tx *bolt.Tx, name string) int64 {
	var n int64
	_ = tx.Bucket([]byte(name)).ForEach(func(_, _ []byte) error {
		n++
		return nil
	})
	return n
}

// decodeRec is a thin JSON decode used by the verification re-scans. It uses
// the plain decoder (not the policy wrapper) because a corrupt value at verify
// time is a test failure, not a tolerated skip.
func decodeRec[T any](data []byte, v *T) error {
	_, err := decodeRecord(0, "verify", nil, data, v) // mode 0 = FailClosed
	return err
}
