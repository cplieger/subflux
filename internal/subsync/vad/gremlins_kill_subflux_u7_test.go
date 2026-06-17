package vad

// Mutant-killing tests for unit subflux-u7 (package internal/subsync/vad).
//
// Target (the sole living mutant for this unit):
//
//	vad_mintracker.go:114:25 — ARITHMETIC_BASE  [local]
//
// Line 114 is the first multiply in findMinimum's fixed-point median smoother:
//
//	tmp32 := int32(alpha+1)*int32(v.meanVal[ch]) +     // col 25 '*' is the mutant
//	        int32(32767-alpha)*int32(currentMedian) + 16384
//	v.meanVal[ch] = int16(tmp32 >> 15)
//
// ARITHMETIC_BASE rewrites that '*' to '/'. The cases below drive findMinimum
// so the returned (post-smoothing) v.meanVal[ch] depends on the product
// int32(alpha+1)*int32(v.meanVal[ch]); replacing '*' with '/' collapses that
// term from tens-of-millions to a single-digit integer, changing the pinned
// return value, so each assertion fails under the mutation.
//
// Both cases hold the 16-slot per-channel state still so the trace is exact:
//   - every age slot is preset to 50 (!= 100), so the aging loop only
//     increments and never shifts sv;
//   - sv (lowValue[0:16]) is ascending and featureVal (10000) exceeds sv[15],
//     so the binary search leaves pos == -1 (no insert, no shift) and sv[2]
//     keeps the value we set;
//   - frameCounter == 10 (> 2) selects currentMedian = sv[2];
//   - currentMedian vs meanVal selects the alpha branch (smoothUp 32439 when
//     currentMedian >= meanVal, smoothDown 6553 otherwise).
//
// Hand-traced expected return values (original, unmutated) vs mutant ('/'):
//
//	smoothUp:   alpha=32439, mean=1600, median=5000
//	  orig: 32440*1600 + 328*5000 + 16384   = 53560384; >>15 = 1634
//	  mut:  32440/1600=20; 20 + 1640000 + 16384 = 1656404; >>15 =   50
//	smoothDown: alpha=6553,  mean=5000, median=1000
//	  orig: 6554*5000 + 26214*1000 + 16384  = 59000384; >>15 = 1800
//	  mut:  6554/5000=1;  1 + 26214000 + 16384  = 26230385; >>15 =  800
//
// 1634 != 50 and 1800 != 800, so each case kills the mutant. The mutant is not
// equivalent. All identifiers are prefixed gk_subflux_u7_ / TestGkU7_ so they
// never collide with the sibling u1..u6 files sharing this package this wave.

import "testing"

// gk_subflux_u7_ascending fills dst[0:16] with base + step*i (strictly
// ascending), so findMinimum's binary search can be steered by choosing
// featureVal relative to dst[7] and dst[15].
func gk_subflux_u7_ascending(dst []int16, base, step int16) {
	for i := range 16 {
		dst[i] = base + step*int16(i)
	}
}

// TestGkU7_FindMinimumSmoothMultiply pins findMinimum's smoothed-mean return
// value for both alpha branches. The pinned values depend on the first multiply
// at vad_mintracker.go:114:25; '*'->'/' changes both, killing the mutant.
func TestGkU7_FindMinimumSmoothMultiply(t *testing.T) {
	cases := []struct {
		name    string
		svBase  int16 // sv[0]; sv[2] = svBase+200 becomes currentMedian
		oldMean int16
		want    int16
	}{
		// currentMedian(sv[2]=5000) >= oldMean(1600) -> alpha = smoothUp(32439)
		{"smoothUp", 4800, 1600, 1634},
		// currentMedian(sv[2]=1000) <  oldMean(5000) -> alpha = smoothDown(6553)
		{"smoothDown", 800, 5000, 1800},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVADInst(ModeVeryAggressive)
			v.frameCounter = 10  // > 2 -> currentMedian = sv[2]
			v.meanVal[0] = tc.oldMean
			for i := range 16 {
				v.indexVec[i] = 50 // != 100 -> aging only increments, never shifts sv
			}
			gk_subflux_u7_ascending(v.lowValue[0:16], tc.svBase, 100)

			// featureVal 10000 > sv[15] -> binary search leaves pos == -1
			// (no insert/shift), so sv[2] stays svBase+200.
			got := v.findMinimum(10000, 0)
			if got != tc.want {
				t.Errorf("findMinimum(10000, 0) = %d, want %d (smoothing multiply at line 114)", got, tc.want)
			}
		})
	}
}
