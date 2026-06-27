package crosslang

import (
	"testing"

	"pgregory.net/rapid"
)

// TestDPAlign_monotonicity asserts the core DP invariant: the returned path is
// strictly increasing in both IncIdx and RefIdx, so the alignment never
// reorders cues.
func TestDPAlign_monotonicity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 50).Draw(t, "n")
		pairs := make([]CuePair, n)
		for i := range pairs {
			pairs[i] = CuePair{
				IncIdx: rapid.IntRange(0, 100).Draw(t, "incIdx"),
				RefIdx: rapid.IntRange(0, 100).Draw(t, "refIdx"),
				Score:  rapid.Float64Range(0.01, 1.0).Draw(t, "score"),
			}
		}
		result := DPAlign(pairs)
		for i := 1; i < len(result); i++ {
			if result[i].IncIdx <= result[i-1].IncIdx {
				t.Fatalf("IncIdx not strictly increasing: [%d]=%d, [%d]=%d",
					i-1, result[i-1].IncIdx, i, result[i].IncIdx)
			}
			if result[i].RefIdx <= result[i-1].RefIdx {
				t.Fatalf("RefIdx not strictly increasing: [%d]=%d, [%d]=%d",
					i-1, result[i-1].RefIdx, i, result[i].RefIdx)
			}
		}
	})
}

// TestWeightedMedianOffset_selectsInputOffset asserts the weighted median is a
// selection, never an interpolation: for any non-empty input the result is one
// of the offsets that was passed in (and 0 for empty input). This catches a
// regression that averaged offsets instead of picking the weighted-median
// element, which would corrupt the chosen sync offset.
func TestWeightedMedianOffset_selectsInputOffset(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 40).Draw(t, "n")
		pairs := make([]CuePair, n)
		offsets := make(map[int64]bool, n)
		for i := range pairs {
			off := rapid.Int64Range(-1_000_000, 1_000_000).Draw(t, "offset")
			pairs[i] = CuePair{
				IncIdx:   i,
				RefIdx:   i,
				Score:    rapid.Float64Range(0, 1000).Draw(t, "score"),
				OffsetMs: off,
			}
			offsets[off] = true
		}
		got := WeightedMedianOffset(pairs)
		if n == 0 {
			if got != 0 {
				t.Fatalf("WeightedMedianOffset(empty) = %d, want 0", got)
			}
			return
		}
		if !offsets[got] {
			t.Fatalf("WeightedMedianOffset returned %d, which is not one of the input offsets", got)
		}
	})
}
