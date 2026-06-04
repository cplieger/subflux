package subsync

import "testing"

// FuzzSegmentCostNonNegative verifies that segmentCost always returns
// a non-negative value (variance is non-negative invariant).
func FuzzSegmentCostNonNegative(f *testing.F) {
	f.Add(int64(100), int64(200), int64(150))
	f.Add(int64(0), int64(0), int64(0))
	f.Add(int64(-5000), int64(5000), int64(0))
	f.Fuzz(func(t *testing.T, a, b, c int64) {
		offsets := []perCueOffset{{a}, {b}, {c}}
		cost := segmentCost(offsets)
		if cost < 0 {
			t.Fatalf("segmentCost returned negative: %f for offsets [%d,%d,%d]", cost, a, b, c)
		}
	})
}

// FuzzSpanScoreBounded verifies that spanScore returns a value in [0, 1].
func FuzzSpanScoreBounded(f *testing.F) {
	f.Add(int64(0), int64(1000), int64(500), int64(1500))
	f.Add(int64(0), int64(100), int64(0), int64(100))
	f.Fuzz(func(t *testing.T, rs, re, ss, se int64) {
		r := TimeSpan{Start: rs, End: re}
		s := TimeSpan{Start: ss, End: se}
		score := spanScore(r, s)
		if score < 0 || score > 1 {
			t.Fatalf("spanScore out of bounds: %f", score)
		}
	})
}
