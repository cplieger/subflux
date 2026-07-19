package boltstore

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file pins the cost of the two full-index-walk queries at large-library
// scale (the README's 52k-episode reference deployment), so "optimize or
// leave" decisions rest on measurements instead of intuition:
//
//   - GetManualLocks: index-only walk of ix_state_quad (manual flag lives in
//     the projection value; no primary dereference).
//   - HistoryMediaIDs: DISTINCT media_id per media type, currently a full
//     bucket ForEach with map dedup.
//
// distinctMediaIDsSkipScan below is the textbook KV alternative for the
// DISTINCT query (seek to the media-type prefix, then leapfrog past all rows
// of each id by seeking mid+0x01), benchmarked against the current shape.

// populateQuadIndex writes a full-coverage large library through the putState
// chokepoint: series*epsPer episode rows plus movie rows, one language each,
// with `locks` quads carrying one extra manual row (a manual lock's shape).
func populateQuadIndex(b *testing.B, db *DB, series, epsPer, movies, locks int) (episodeIDs, movieIDs int) {
	b.Helper()

	type rowSpec struct {
		mt     api.MediaType
		mid    string
		manual bool
	}
	rows := make([]rowSpec, 0, series*epsPer+movies+locks)
	for s := range series {
		for e := range epsPer {
			rows = append(rows, rowSpec{api.MediaTypeEpisode,
				fmt.Sprintf("tt%06d-s01e%02d", 100000+s, e+1), false})
		}
	}
	epRows := len(rows)
	for m := range movies {
		rows = append(rows, rowSpec{api.MediaTypeMovie, fmt.Sprintf("tt%07d", 2000000+m), false})
	}
	// Sprinkle manual rows over existing episode quads (a locked quad = its
	// auto row plus one manual row).
	if locks > 0 {
		stride := max(1, epRows/locks)
		for i := 0; i < locks; i++ {
			src := rows[i*stride%epRows]
			rows = append(rows, rowSpec{src.mt, src.mid, true})
		}
	}

	const batch = 2000
	for start := 0; start < len(rows); start += batch {
		end := min(start+batch, len(rows))
		if err := db.db.Update(func(tx *bolt.Tx) error {
			sb := tx.Bucket([]byte(bucketSubtitleState))
			for _, r := range rows[start:end] {
				seq, _, err := kv.NextID(sb)
				if err != nil {
					return err
				}
				rec := stateRec{
					MediaImported: time.Unix(1700000000+int64(seq), 0), //nolint:gosec // G115: bounded test sequence
					MediaType:     r.mt,
					MediaID:       r.mid,
					Language:      "fr",
					Variant:       api.VariantStandard,
					Provider:      "opensubtitles",
					Path:          "/media/x/" + r.mid + ".fr.srt",
					VideoPath:     "/media/x/" + r.mid + ".mkv",
					ID:            int64(seq), //nolint:gosec // G115: NextSequence fits
					Score:         80,
					Manual:        r.manual,
				}
				if err := putState(tx, &rec); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
	return series * epsPer, movies
}

// BenchmarkQuadIndexQueriesAtScale populates one 52k-episode-shaped library
// and times the index-walk queries against it. HistoryMediaIDs now uses the
// skip-scan (adopted after this benchmark showed ~8.9x on the later-sorting
// media type at parity in the single-language worst case); the benchmark
// remains to catch regressions and to price GetManualLocks' full walk.
func BenchmarkQuadIndexQueriesAtScale(b *testing.B) {
	ctx := context.Background()
	db, err := Open(filepath.Join(b.TempDir(), "bench.bolt"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close(ctx)

	const (
		seriesN = 3000
		epsPer  = 18 // 54k episode rows
		moviesN = 4000
		locksN  = 25
	)
	eps, movs := populateQuadIndex(b, db, seriesN, epsPer, moviesN, locksN)
	b.Logf("populated: %d episode rows, %d movie rows, %d manual-lock rows", eps, movs, locksN)

	b.Run("GetManualLocks/current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			locks, err := db.GetManualLocks(ctx)
			if err != nil {
				b.Fatal(err)
			}
			if len(locks) != locksN {
				b.Fatalf("locks = %d, want %d", len(locks), locksN)
			}
		}
	})

	b.Run("HistoryMediaIDs/episodes/current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ids, err := db.HistoryMediaIDs(ctx, api.MediaTypeEpisode, "")
			if err != nil {
				b.Fatal(err)
			}
			if len(ids) != eps {
				b.Fatalf("ids = %d, want %d", len(ids), eps)
			}
		}
	})

	b.Run("HistoryMediaIDs/movies/current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ids, err := db.HistoryMediaIDs(ctx, api.MediaTypeMovie, "")
			if err != nil {
				b.Fatal(err)
			}
			if len(ids) != movs {
				b.Fatalf("ids = %d, want %d", len(ids), movs)
			}
		}
	})

}
