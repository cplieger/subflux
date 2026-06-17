package vad

// Round-1 unit subflux-u7, simplified for round 2.
//
// The original test pinned hand-traced smoothing outputs (1634 / 1800, with a
// ~25-line manual fixed-point derivation) for findMinimum's median smoother.
// Round 2 replaces those magic constants with a direct assertion against the
// shared readable reference gk_subflux_r2_medianSmooth (defined in
// gremlins_kill_subflux_r2_test.go), which mirrors vad_mintracker.go's
// median-selection + fixed-point smoothing tail. This still kills the
// ARITHMETIC_BASE mutant on the smoothing multiply (vad_mintracker.go:114:25)
// — and the +1 rounding term (114:22) — because any arithmetic edit in the
// production formula diverges from the unmutated reference.

import "testing"

// TestGkU7_FindMinimumSmoothMultiply drives findMinimum down a frozen path
// (frameCounter 10 -> currentMedian = sv[2]; ages != 100 -> no eviction shift;
// featureVal 10000 > sv[15] -> no insertion) so the returned value is purely
// the median-smoothing tail, then compares it to the reference formula.
func TestGkU7_FindMinimumSmoothMultiply(t *testing.T) {
	cases := []struct {
		name    string
		svBase  int16 // sv[i] = svBase + 100*i; sv[2] becomes the current median
		oldMean int16
	}{
		{"smoothUp", 4800, 1600},  // sv[2]=5000 >= meanVal 1600 -> smoothUp
		{"smoothDown", 800, 5000}, // sv[2]=1000 <  meanVal 5000 -> smoothDown
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sv [16]int16
			for i := range 16 {
				sv[i] = tc.svBase + 100*int16(i)
			}

			v := newVADInst(ModeVeryAggressive)
			v.frameCounter = 10 // > 2 -> currentMedian = sv[2]
			v.meanVal[0] = tc.oldMean
			for i := range 16 {
				v.indexVec[i] = 50 // != 100 -> aging only increments, never shifts sv
			}
			copy(v.lowValue[0:16], sv[:])

			got := v.findMinimum(10000, 0)
			if want := gk_subflux_r2_medianSmooth(10, sv, tc.oldMean); got != want {
				t.Errorf("findMinimum(10000, 0) = %d, want %d (smoothing tail)", got, want)
			}
		})
	}
}
