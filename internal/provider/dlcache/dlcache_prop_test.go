package dlcache

import (
	"strconv"
	"testing"

	"pgregory.net/rapid"
)

// TestPut_neverExceedsMaxEntries is the cache's core safety invariant: no
// sequence of Puts (with key reuse and eviction) can grow the resident set
// beyond maxEntries.
func TestPut_neverExceedsMaxEntries(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		maxEntries := rapid.IntRange(1, 8).Draw(rt, "maxEntries")
		dc := New(maxEntries, 1<<20)

		ops := rapid.IntRange(0, 50).Draw(rt, "ops")
		for range ops {
			key := strconv.Itoa(rapid.IntRange(0, 12).Draw(rt, "key"))
			val := []byte(rapid.String().Draw(rt, "val"))
			dc.Put(key, val, nil)
			if got := len(dc.cache); got > maxEntries {
				rt.Fatalf("cache size %d exceeds maxEntries %d", got, maxEntries)
			}
		}
	})
}
