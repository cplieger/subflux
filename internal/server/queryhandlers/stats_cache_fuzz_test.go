package queryhandlers

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzStatsCacheGetAfterInvalidate verifies that after Invalidate(), the
// next get() call invokes the compute function (cache coherence invariant).
func FuzzStatsCacheGetAfterInvalidate(f *testing.F) {
	f.Add(10, int64(5))
	f.Add(0, int64(0))
	f.Add(999999, int64(42))

	f.Fuzz(func(t *testing.T, downloads int, attempts int64) {
		var c statsCache
		ctx := context.Background()

		computeCalls := 0
		compute := func(_ context.Context) api.Stats {
			computeCalls++
			return api.Stats{
				Downloads: downloads,
				Attempts:  attempts,
			}
		}

		// Prime the cache.
		resp1 := c.get(ctx, compute)
		if resp1.Downloads != downloads || resp1.Attempts != attempts {
			t.Fatalf("first get: got downloads=%d attempts=%d, want %d/%d",
				resp1.Downloads, resp1.Attempts, downloads, attempts)
		}
		firstCalls := computeCalls

		// Invalidate.
		c.Invalidate()

		// Next get must recompute.
		resp2 := c.get(ctx, compute)
		if computeCalls <= firstCalls {
			t.Fatal("get() after Invalidate() did not recompute")
		}
		if resp2.Downloads != downloads || resp2.Attempts != attempts {
			t.Fatalf("get after invalidate: got downloads=%d attempts=%d, want %d/%d",
				resp2.Downloads, resp2.Attempts, downloads, attempts)
		}
	})
}
