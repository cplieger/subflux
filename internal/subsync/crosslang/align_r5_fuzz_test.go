package crosslang

import (
	"testing"
)

// FuzzWeightedMedianOffset exercises the weighted median computation with
// arbitrary CuePair data encoded as parallel int64/float64 arrays.
//
// Bug class: panic on empty/single-element slices; result outside the range
// of input offsets; NaN scores causing sort instability.
func FuzzWeightedMedianOffset(f *testing.F) {
	f.Add(int64(100), 1.0, int64(200), 2.0, int64(300), 3.0)
	f.Add(int64(0), 0.0, int64(0), 0.0, int64(0), 0.0)
	f.Add(int64(-1000), 0.5, int64(1000), 0.5, int64(0), 1.0)

	f.Fuzz(func(t *testing.T, o1 int64, s1 float64, o2 int64, s2 float64, o3 int64, s3 float64) {
		pairs := []CuePair{
			{IncIdx: 0, RefIdx: 0, Score: s1, OffsetMs: o1},
			{IncIdx: 1, RefIdx: 1, Score: s2, OffsetMs: o2},
			{IncIdx: 2, RefIdx: 2, Score: s3, OffsetMs: o3},
		}
		_ = WeightedMedianOffset(pairs) // must not panic
	})
}
