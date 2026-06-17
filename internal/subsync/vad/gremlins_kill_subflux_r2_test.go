package vad

// Round-2 mutant-killing tests for package internal/subsync/vad.
//
// These target the still-living mutants left after round 1, using DIRECT,
// readable references instead of hand-traced magic constants:
//
//   - vad_mintracker.go binary-search NEGATION nodes (50,51,56,62,63,64,69,
//     74,75,80): a value fed into the sorted min-tracker must land at its
//     sorted insertion position. A plain linear sorted-insert reference makes
//     the expectation self-evident; any negated comparison in the production
//     binary search routes the value to the wrong slot, diverging from it.
//   - vad_mintracker.go median selection + smoothing (99,107,114): a faithful
//     copy of the median-smoothing tail computes the expected output; any
//     arithmetic/branch mutation in the production tail diverges from it.
//   - vad_classifier.go per-channel decision boundary (87:13).
//   - vad_filterbank.go vadLogEnergy rshifts>=0 boundary (90:14).
//
// The pre-existing CONDITIONALS_BOUNDARY mutants on the binary-search nodes
// (`<` vs `<=`) are genuine equivalents (inserting a duplicate of sv[k] at k
// vs k+1 yields the identical sorted array); the inputs below are chosen
// strictly between seed elements so those equivalents are untouched.

import "testing"

// gk_subflux_r2_ascendingSeed is a strictly-increasing 16-element low-value
// seed, so the sorted-insert position for any in-between value is unambiguous.
func gk_subflux_r2_ascendingSeed() [16]int16 {
	return [16]int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160}
}

// gk_subflux_r2_expectInsert mirrors findMinimum's insert step: find the first
// slot whose value exceeds fv, shift the tail right by one (dropping the last
// element), and write fv there. If fv is >= every element, nothing is inserted.
func gk_subflux_r2_expectInsert(sv [16]int16, fv int16) [16]int16 {
	pos := -1
	for i := range 16 {
		if fv < sv[i] {
			pos = i
			break
		}
	}
	if pos < 0 {
		return sv
	}
	out := sv
	for i := 15; i > pos; i-- {
		out[i] = out[i-1]
	}
	out[pos] = fv
	return out
}

// TestGkSubfluxR2_FindMinimumSortedInsert feeds values that land at every
// position 0..15 (plus a no-insert value), and asserts the resulting low-value
// array equals the sorted-insert reference. This exercises every internal node
// of the binary search; any negated node comparison (50,51,56,62,63,64,69,74,
// 75,80) routes the value to a different slot, failing the assertion.
func TestGkSubfluxR2_FindMinimumSortedInsert(t *testing.T) {
	seed := gk_subflux_r2_ascendingSeed()
	// One value strictly between each pair of seed elements (never equal to a
	// seed value, so the equivalent `<`->`<=` boundary mutants are untouched),
	// plus 165 which exceeds sv[15]=160 (no insertion).
	for _, fv := range []int16{5, 15, 25, 35, 45, 55, 65, 75, 85, 95, 105, 115, 125, 135, 145, 155, 165} {
		v := newVADInst(ModeVeryAggressive)
		copy(v.lowValue[:16], seed[:])
		v.findMinimum(fv, 0)
		var got [16]int16
		copy(got[:], v.lowValue[:16])
		if want := gk_subflux_r2_expectInsert(seed, fv); got != want {
			t.Errorf("findMinimum(%d): lowValue = %v, want %v", fv, got, want)
		}
	}
}

// Median-smoothing reference: a faithful copy of vad_mintracker.go's tail
// (median selection by frame counter, then the fixed-point smoothing). The
// constants match the production function's local consts.
const (
	gk_subflux_r2_smoothDown int16 = 6553  // 0.2 in Q15
	gk_subflux_r2_smoothUp   int16 = 32439 // 0.99 in Q15
)

func gk_subflux_r2_medianSmooth(frameCounter int32, sv [16]int16, meanVal int16) int16 {
	var currentMedian int16 = 1600
	if frameCounter > 2 {
		currentMedian = sv[2]
	} else if frameCounter > 0 {
		currentMedian = sv[0]
	}
	var alpha int16
	if frameCounter > 0 {
		if currentMedian < meanVal {
			alpha = gk_subflux_r2_smoothDown
		} else {
			alpha = gk_subflux_r2_smoothUp
		}
	}
	tmp := int32(alpha+1)*int32(meanVal) + int32(32767-alpha)*int32(currentMedian) + 16384
	return int16(tmp >> 15)
}

// TestGkSubfluxR2_FindMinimumMedianSmoothing pins findMinimum's smoothed return
// value against the reference above for several states. Each case freezes the
// tracker (no eviction: ages != 100; no insert: featureVal exceeds every slot)
// so the only thing under test is the median-selection + smoothing tail.
//
// Kills:
//
//	99:20  CONDITIONALS_BOUNDARY  frameCounter > 2  (frameCounter==2 case:
//	       original picks sv[0], the >= mutant picks sv[2]; sv[0] != sv[2]).
//	107:20 CONDITIONALS_BOUNDARY  frameCounter > 0  (frameCounter==0 case:
//	       original keeps alpha 0, the >= mutant computes an alpha != 0).
//	114:22 ARITHMETIC_BASE        int32(alpha+1)    (meanVal 16384 case: the
//	       +1 -> -1 edit shifts the >>15 result by one).
//	114:25 ARITHMETIC_BASE        (alpha+1)*meanVal (the * -> / edit collapses
//	       the term, changing the result in every case).
func TestGkSubfluxR2_FindMinimumMedianSmoothing(t *testing.T) {
	flat16384 := [16]int16{16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384}
	cases := []struct {
		name         string
		frameCounter int32
		sv           [16]int16
		meanVal      int16
	}{
		// frameCounter==2 selects sv[0]; sv[0]=10 != sv[2]=30 isolates node 99.
		{"frameCounter2", 2, gk_subflux_r2_ascendingSeed(), 1000},
		// frameCounter==0 keeps alpha 0; meanVal != 1600 (the default median)
		// makes the alpha-on mutant diverge, isolating node 107.
		{"frameCounter0", 0, [16]int16{}, 1000},
		// meanVal==16384 makes 2*meanVal == 2^15, so the (alpha+1)->(alpha-1)
		// edit shifts the >>15 result; median==meanVal selects smoothUp.
		{"highMean", 10, flat16384, 16384},
		// general smoothing case (median below mean -> smoothDown).
		{"general", 10, gk_subflux_r2_ascendingSeed(), 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVADInst(ModeVeryAggressive)
			v.frameCounter = tc.frameCounter
			v.meanVal[0] = tc.meanVal
			copy(v.lowValue[:16], tc.sv[:])
			for i := range 16 {
				v.indexVec[i] = 50 // != 100 -> aging only increments, never shifts sv
			}
			// featureVal 30000 exceeds every seed value -> no insertion, so sv
			// is unchanged and the median/smoothing tail is what is observed.
			got := v.findMinimum(30000, 0)
			if want := gk_subflux_r2_medianSmooth(tc.frameCounter, tc.sv, tc.meanVal); got != want {
				t.Errorf("findMinimum smoothed mean = %d, want %d", got, want)
			}
		})
	}
}

// TestGkSubfluxR2_PerChannelDecisionBoundary kills 87:13 CONDITIONALS_BOUNDARY
// (`llr*4 > v.localThresh` -> `>=`). With the global trigger disabled and
// localThresh set exactly to the maximum per-channel llr*4 (= 100), no channel
// strictly exceeds it, so the original keeps vadflag 0; the `>=` mutant fires
// at the boundary, flipping vadflag to 1.
func TestGkSubfluxR2_PerChannelDecisionBoundary(t *testing.T) {
	fsSpeech := [vadNumCh]int16{1038, 1261, 1260, 1478, 1480, 789}
	v := newVADInst(ModeQuality)
	v.globalThresh = 32767 // disable the global-sum trigger
	v.localThresh = 100    // == max per-channel llr*4, so `>` is false for all
	flag, _ := v.gmmProbabilityLLR(fsSpeech, 100)
	if flag != 0 {
		t.Errorf("flag = %d, want 0 (no channel's llr*4 strictly exceeds localThresh 100)", flag)
	}
}

// TestGkSubfluxR2_LogEnergyRshiftsZero kills vad_filterbank.go:90:14
// CONDITIONALS_BOUNDARY (`if rshifts >= 0` -> `rshifts > 0`). An all-16 frame
// of 80 samples has energy 20480, which normalises to rshifts == 0. The
// original takes the `>= 0` branch and clamps totalE to vadMinEnergy+1 (11);
// the `> 0` mutant takes the else branch and returns int16(energy) (20480).
func TestGkSubfluxR2_LogEnergyRshiftsZero(t *testing.T) {
	data := make([]int16, 80)
	for i := range data {
		data[i] = 16
	}
	_, totalE := vadLogEnergy(data, 80, 368)
	if totalE != 11 {
		t.Errorf("vadLogEnergy(all-16) totalE = %d, want 11 (vadMinEnergy+1 on the rshifts==0 path)", totalE)
	}
}
