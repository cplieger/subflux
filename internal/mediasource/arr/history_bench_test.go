package arr

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// BenchmarkChangesQuietAfterBurst measures one quiet poll after a 5,000-record
// burst: before the advance, which re-reads the burst's window, and after it.
func BenchmarkChangesQuietAfterBurst(b *testing.B) {
	now := time.Now().Truncate(time.Millisecond)
	burst := func(end time.Time) []Record {
		recs := make([]Record, 5000)
		for i := range recs {
			recs[i] = Record{
				Date: end.Add(-time.Duration(len(recs)-i) * 24 * time.Millisecond),
				Root: mediasource.NativeRef{ID: Native(i%50 + 1), Kind: media.Episode},
				ID:   i + 1,
			}
		}
		return recs
	}
	for name, tc := range map[string]struct {
		recs []Record
		cur  func([]Record) cursor
	}{
		"before the advance": {burst(now), func(r []Record) cursor {
			since := r[len(r)-1].Date
			return cursor{since: since, seen: seenIn(r, since)}
		}},
		"after the advance": {burst(now.Add(-4*time.Minute - time.Second)), func([]Record) cursor {
			return cursor{since: now.Add(-overlap)}
		}},
	} {
		b.Run(name, func(b *testing.B) {
			served := 0
			history := func(_ context.Context, since time.Time) ([]Record, error) {
				from := since.Truncate(time.Second)
				var out []Record
				for _, r := range tc.recs {
					if !r.Date.Before(from) {
						out = append(out, r)
					}
				}
				served += len(out)
				return out, nil
			}
			cur := tc.cur(tc.recs).encode()
			log := slog.New(slog.DiscardHandler)
			b.ReportAllocs()
			polls := 0
			for b.Loop() {
				polls++
				if changes, _, err := Changes(b.Context(), history, cur, log); err != nil || len(changes) != 0 {
					b.Fatalf("Changes = %v, %v; want a quiet answer", changes, err)
				}
			}
			b.ReportMetric(float64(served)/float64(polls), "records/op")
		})
	}
}
