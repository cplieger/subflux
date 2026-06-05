package crosslang

import "testing"

// FuzzDPAlignMonotonic verifies that DPAlign output is always monotonically
// increasing in both IncIdx and RefIdx (the core invariant of the DP).
//
// Bug class: off-by-one in predecessor search or index comparison could
// produce crossing alignments, leading to incorrect subtitle offsets that
// reorder cues in the final sync result.
func FuzzDPAlignMonotonic(f *testing.F) {
	f.Add(0, 1, 2, 3, 4, 5, int64(100), int64(200), int64(300))
	f.Add(1, 0, 3, 2, 5, 4, int64(-50), int64(0), int64(50))
	f.Add(0, 0, 1, 1, 2, 2, int64(0), int64(0), int64(0))
	f.Add(10, 20, 11, 21, 12, 22, int64(1000), int64(-1000), int64(500))
	f.Add(0, 0, 0, 0, 0, 0, int64(0), int64(0), int64(0))

	f.Fuzz(func(t *testing.T, i0, r0, i1, r1, i2, r2 int, o0, o1, o2 int64) {
		pairs := []CuePair{
			{IncIdx: i0, RefIdx: r0, Score: 1.0, OffsetMs: o0},
			{IncIdx: i1, RefIdx: r1, Score: 1.0, OffsetMs: o1},
			{IncIdx: i2, RefIdx: r2, Score: 1.0, OffsetMs: o2},
		}
		result := DPAlign(pairs)
		for i := 1; i < len(result); i++ {
			if result[i].IncIdx <= result[i-1].IncIdx {
				t.Fatalf("DPAlign monotonicity violated: result[%d].IncIdx=%d <= result[%d].IncIdx=%d",
					i, result[i].IncIdx, i-1, result[i-1].IncIdx)
			}
			if result[i].RefIdx <= result[i-1].RefIdx {
				t.Fatalf("DPAlign monotonicity violated: result[%d].RefIdx=%d <= result[%d].RefIdx=%d",
					i, result[i].RefIdx, i-1, result[i-1].RefIdx)
			}
		}
	})
}
