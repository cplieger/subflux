package vad

import "testing"

// mintrackerSeed is a strictly-increasing 16-element low-value list, so the
// sorted-insert position for any in-between value is unambiguous.
func mintrackerSeed() [16]int16 {
	return [16]int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160}
}

// expectSortedInsert is an independent (linear-scan) oracle for findMinimum's
// insert step: it finds the first slot whose value exceeds fv, shifts the tail
// right by one (dropping the last element), and writes fv there. If fv is not
// smaller than any element, nothing is inserted. The production code reaches
// the same result via an unrolled binary search, so this is a genuine
// cross-check, not a copy of the implementation.
func expectSortedInsert(sv [16]int16, fv int16) [16]int16 {
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

// medianSmoothReference mirrors findMinimum's median-selection + fixed-point
// smoothing tail, so the expected smoothed output is readable instead of a
// hand-traced magic constant. The constants match the production locals.
func medianSmoothReference(frameCounter int32, sv [16]int16, meanVal int16) int16 {
	const (
		smoothDown int16 = 6553  // 0.2 in Q15
		smoothUp   int16 = 32439 // 0.99 in Q15
	)
	var currentMedian int16 = 1600
	if frameCounter > 2 {
		currentMedian = sv[2]
	} else if frameCounter > 0 {
		currentMedian = sv[0]
	}
	var alpha int16
	if frameCounter > 0 {
		if currentMedian < meanVal {
			alpha = smoothDown
		} else {
			alpha = smoothUp
		}
	}
	tmp := int32(alpha+1)*int32(meanVal) + int32(32767-alpha)*int32(currentMedian) + 16384
	return int16(tmp >> 15)
}

// assertLow16 compares the first 16 elements of got to want.
func assertLow16(t *testing.T, name string, got []int16, want [16]int16) {
	t.Helper()
	var g [16]int16
	copy(g[:], got)
	if g != want {
		t.Errorf("%s = %v, want %v", name, g, want)
	}
}

// TestFindMinimum_initial_state verifies the fresh-instance smoothing: with
// frameCounter 0 the median defaults to meanVal (1600) and alpha is 0, so the
// smoother returns meanVal unchanged.
func TestFindMinimum_initial_state(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	result := v.findMinimum(500, 0)
	if result != 1600 {
		t.Errorf("findMinimum(500, 0) on fresh instance = %d, want 1600", result)
	}
}

func TestFindMinimum_decreasing_values(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed decreasing values; the minimum tracker should follow down.
	var last int16
	for i := range 50 {
		v.frameCounter = int32(i)
		last = v.findMinimum(int16(5000-i*50), 0)
	}
	if last >= 5000 {
		t.Errorf("findMinimum after decreasing values = %d, want < 5000", last)
	}
}

func TestFindMinimum_increasing_values(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed increasing values (1000 to 3450).
	var last int16
	for i := range 50 {
		v.frameCounter = int32(i)
		last = v.findMinimum(int16(1000+i*50), 0)
	}
	// The smoothed minimum stays well below the final input (3450) because the
	// upward smoothing factor (0.99) tracks up very slowly.
	if last >= 3450 {
		t.Errorf("findMinimum after increasing values = %d, want < 3450", last)
	}
}

func TestFindMinimum_age_eviction(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed 110 frames with value 5000 to fill and age the tracker entries.
	for i := range 110 {
		v.frameCounter = int32(i)
		v.findMinimum(5000, 0)
	}
	// Now feed a much lower value. Old entries should be evicted by age,
	// and the minimum should converge toward the new value.
	v.frameCounter = 110
	result := v.findMinimum(100, 0)
	if result >= 5000 {
		t.Errorf("findMinimum after age eviction = %d, want < 5000", result)
	}
}

func TestFindMinimum_channel_independence(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed different values to channel 0 and channel 1.
	for i := range 50 {
		v.frameCounter = int32(i)
		v.findMinimum(500, 0)  // ch 0: low value
		v.findMinimum(5000, 1) // ch 1: high value
	}
	// After convergence, meanVal for ch 0 and ch 1 should diverge.
	if v.meanVal[0] == v.meanVal[1] {
		t.Errorf("channels not independent: meanVal[0]=%d == meanVal[1]=%d",
			v.meanVal[0], v.meanVal[1])
	}
	if v.meanVal[0] >= v.meanVal[1] {
		t.Errorf("channel 0 (low input) meanVal=%d >= channel 1 (high input) meanVal=%d",
			v.meanVal[0], v.meanVal[1])
	}
}

// TestFindMinimum_sorted_insert feeds values that land at every position 0..15
// (plus a value above the maximum that must not insert) and checks the tracker
// list against the independent sorted-insert oracle. This exercises every node
// of the binary-search insertion. Each fed value sits strictly between seed
// elements, so the insert position is unambiguous.
func TestFindMinimum_sorted_insert(t *testing.T) {
	t.Parallel()
	seed := mintrackerSeed()
	for _, fv := range []int16{5, 15, 25, 35, 45, 55, 65, 75, 85, 95, 105, 115, 125, 135, 145, 155, 165} {
		v := newVADInst(ModeVeryAggressive)
		copy(v.lowValue[:16], seed[:])
		v.findMinimum(fv, 0)
		var got [16]int16
		copy(got[:], v.lowValue[:16])
		if want := expectSortedInsert(seed, fv); got != want {
			t.Errorf("findMinimum(%d): lowValue = %v, want %v", fv, got, want)
		}
	}
}

// TestFindMinimum_median_smoothing pins findMinimum's smoothed return value
// against the median-smoothing oracle across the frame-counter cases. Each case
// freezes the tracker (ages != 100 so no eviction, featureVal above every slot
// so no insertion) to isolate the median-selection + smoothing tail.
func TestFindMinimum_median_smoothing(t *testing.T) {
	t.Parallel()
	flat16384 := [16]int16{16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384, 16384}
	cases := []struct {
		name         string
		frameCounter int32
		sv           [16]int16
		meanVal      int16
	}{
		// frameCounter==2 selects sv[0] (the > 2 branch is not yet taken).
		{"frameCounter2", 2, mintrackerSeed(), 1000},
		// frameCounter==0 keeps alpha 0 and the default median.
		{"frameCounter0", 0, [16]int16{}, 1000},
		// median == meanVal selects the slow upward smoothing factor.
		{"highMean", 10, flat16384, 16384},
		// median below mean selects the fast downward smoothing factor.
		{"general", 10, mintrackerSeed(), 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newVADInst(ModeVeryAggressive)
			v.frameCounter = tc.frameCounter
			v.meanVal[0] = tc.meanVal
			copy(v.lowValue[:16], tc.sv[:])
			for i := range 16 {
				v.indexVec[i] = 50 // != 100 -> aging only increments, never shifts sv
			}
			// featureVal above every slot -> no insertion; the median/smoothing
			// tail is what is observed.
			got := v.findMinimum(30000, 0)
			if want := medianSmoothReference(tc.frameCounter, tc.sv, tc.meanVal); got != want {
				t.Errorf("findMinimum smoothed mean = %d, want %d", got, want)
			}
		})
	}
}

// TestFindMinimum_aging_increment verifies the aging step increments every
// entry's age when none has reached the 100-frame eviction limit. All ages
// start at 5 and a no-insert value is fed, so each age becomes 6.
func TestFindMinimum_aging_increment(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	seed := mintrackerSeed()
	copy(v.lowValue[:16], seed[:])
	for i := range 16 {
		v.indexVec[i] = 5
	}
	v.findMinimum(200, 0) // 200 > seed max -> no insertion, only aging
	assertLow16(t, "indexVec after aging increment", v.indexVec[:16],
		[16]int16{6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6})
}

// TestFindMinimum_eviction_shift verifies that an entry reaching the 100-frame
// limit is evicted: the entries above it shift down by one and the top slot is
// refilled with the 10000 sentinel. indexVec[5] hits 100, so sv[5..14] shift
// left and sv[15] becomes 10000.
func TestFindMinimum_eviction_shift(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	seed := mintrackerSeed()
	copy(v.lowValue[:16], seed[:])
	for i := range 16 {
		v.indexVec[i] = 1
	}
	v.indexVec[5] = 100
	// 10001 is above the post-eviction sv[15]=10000, so no insertion perturbs sv.
	v.findMinimum(10001, 0)
	assertLow16(t, "lowValue after eviction shift", v.lowValue[:16],
		[16]int16{10, 20, 30, 40, 50, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160, 10000})
}
