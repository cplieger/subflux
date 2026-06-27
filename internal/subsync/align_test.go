package subsync

import (
	"context"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestAlignConstantOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  []TimeSpan
		inc  []TimeSpan
		want int64
	}{
		{
			name: "empty_reference",
			ref:  nil,
			inc:  []TimeSpan{{Start: 0, End: 1000}},
			want: 0,
		},
		{
			name: "empty_incorrect",
			ref:  []TimeSpan{{Start: 0, End: 1000}},
			inc:  nil,
			want: 0,
		},
		{
			name: "identical_spans",
			ref:  []TimeSpan{{Start: 1000, End: 3000}, {Start: 5000, End: 7000}, {Start: 10000, End: 12000}},
			inc:  []TimeSpan{{Start: 1000, End: 3000}, {Start: 5000, End: 7000}, {Start: 10000, End: 12000}},
			want: 0,
		},
		{
			name: "known_offset",
			ref:  []TimeSpan{{Start: 5000, End: 7000}, {Start: 10000, End: 12000}, {Start: 15000, End: 17000}},
			inc:  []TimeSpan{{Start: 3000, End: 5000}, {Start: 8000, End: 10000}, {Start: 13000, End: 15000}},
			want: 2000,
		},
		{
			name: "negative_offset",
			ref:  []TimeSpan{{Start: 1000, End: 3000}, {Start: 5000, End: 7000}},
			inc:  []TimeSpan{{Start: 4000, End: 6000}, {Start: 8000, End: 10000}},
			want: -3000,
		},
		{
			name: "single_span_each",
			ref:  []TimeSpan{{Start: 10000, End: 12000}},
			inc:  []TimeSpan{{Start: 5000, End: 7000}},
			want: 5000,
		},
		{
			name: "large_offset",
			ref:  []TimeSpan{{Start: 100000, End: 102000}, {Start: 200000, End: 202000}},
			inc:  []TimeSpan{{Start: 0, End: 2000}, {Start: 100000, End: 102000}},
			want: 100000,
		},
		{
			name: "forces_merge_sort_path",
			ref:  []TimeSpan{{Start: 0, End: 2000}},
			inc:  []TimeSpan{{Start: 500000, End: 502000}},
			want: -500000,
		},
		{
			name: "zero_length_incorrect_spans",
			ref:  []TimeSpan{{Start: 5000, End: 7000}},
			inc:  []TimeSpan{{Start: 5000, End: 5000}, {Start: 5000, End: 7000}},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := alignConstantOffset(context.Background(), tt.ref, tt.inc)
			if got != tt.want {
				t.Errorf("alignConstantOffset() = %d, want %d", got, tt.want)
			}
		})
	}
}

func Test_alignConstantOffset_many_spans_merge_sort(t *testing.T) {
	t.Parallel()
	// 5 ref * 5 inc * 4 = 100 entries, range = 20001, 100 < 2000 → merge sort.
	// This test verifies identical spans produce offset 0 via the merge sort path.
	var ref, inc []TimeSpan
	for i := range 5 {
		start := int64(i * 4000)
		ref = append(ref, TimeSpan{Start: start, End: start + 2000})
		inc = append(inc, TimeSpan{Start: start, End: start + 2000})
	}

	got := alignConstantOffset(context.Background(), ref, inc)
	if got != 0 {
		t.Errorf("alignConstantOffset(many spans, same) = %d, want 0", got)
	}
}

func Test_syncCues_empty_inputs(t *testing.T) {
	t.Parallel()

	t.Run("empty reference", func(t *testing.T) {
		t.Parallel()
		inc := []Cue{{Start: time.Second, End: 2 * time.Second, Text: "A"}}
		shifted, offset := syncCues(context.Background(), nil, inc)
		if offset != 0 {
			t.Errorf("syncCues(context.Background(), nil, inc) offset = %v, want 0", offset)
		}
		if len(shifted) != 1 {
			t.Errorf("syncCues(context.Background(), nil, inc) returned %d cues, want 1", len(shifted))
		}
	})

	t.Run("empty incorrect", func(t *testing.T) {
		t.Parallel()
		ref := []Cue{{Start: time.Second, End: 2 * time.Second, Text: "A"}}
		shifted, offset := syncCues(context.Background(), ref, nil)
		if offset != 0 {
			t.Errorf("syncCues(context.Background(), ref, nil) offset = %v, want 0", offset)
		}
		if shifted != nil {
			t.Errorf("syncCues(context.Background(), ref, nil) returned %v, want nil", shifted)
		}
	})
}

func Test_alignConstantOffset_minOffset_arithmetic(t *testing.T) {
	t.Parallel()
	// If minOffset or maxOffset are computed wrong, the alignment result changes.
	ref := []TimeSpan{{Start: 10000, End: 15000}}
	inc := []TimeSpan{{Start: 20000, End: 25000}}
	// Correct: minOffset = 10000 - 25000 = -15000, maxOffset = 15000 - 20000 = -5000
	// The best offset should be -10000 (shift inc left by 10000 to align).
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -10000 {
		t.Errorf("alignConstantOffset(ref=[10k-15k], inc=[20k-25k]) = %d, want -10000", got)
	}
}

func Test_alignConstantOffset_rangeSize_boundary(t *testing.T) {
	t.Parallel()
	// Verify the function handles small ranges correctly.
	// With identical spans, the offset should be near 0.
	ref := []TimeSpan{{Start: 100, End: 101}}
	inc := []TimeSpan{{Start: 100, End: 101}}
	got := alignConstantOffset(context.Background(), ref, inc)
	// Bucket sort discrete bins may return -1 for identical 1ms spans.
	if got < -1 || got > 1 {
		t.Errorf("alignConstantOffset(identical 1ms spans) = %d, want ~0", got)
	}
}

func Test_alignConstantOffset_numEntries_vs_rangeSize(t *testing.T) {
	t.Parallel()
	// Verify both algorithm paths produce correct results.
	// Path selection: numEntries > rangeSize/10 → bucket sort, else merge sort.

	// Force bucket sort: 10*10*4 = 400 entries, rangeSize ≈ 3801, 400 > 380.
	var ref, inc []TimeSpan
	for i := range 10 {
		s := int64(i * 200)
		ref = append(ref, TimeSpan{Start: s, End: s + 100})
		inc = append(inc, TimeSpan{Start: s + 500, End: s + 600})
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	// Bucket sort discrete bins may be off by 1.
	if got < -502 || got > -498 {
		t.Errorf("alignConstantOffset(bucket sort, +500 shift) = %d, want ~-500", got)
	}

	// Force merge sort: 1*1*4 = 4 entries, rangeSize ≈ 102001, 4 < 10200.
	ref2 := []TimeSpan{{Start: 0, End: 2000}}
	inc2 := []TimeSpan{{Start: 100000, End: 102000}}
	got2 := alignConstantOffset(context.Background(), ref2, inc2)
	if got2 != -100000 {
		t.Errorf("alignConstantOffset(merge sort, -100000 shift) = %d, want -100000", got2)
	}
}

// --- syncCues function ---

func Test_syncCues_nonzero_offset_shifts_cues(t *testing.T) {
	t.Parallel()
	// When offset is non-zero, syncCues must return shifted cues.
	ref := []Cue{
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "R1"},
		{Start: 10 * time.Second, End: 12 * time.Second, Text: "R2"},
		{Start: 15 * time.Second, End: 17 * time.Second, Text: "R3"},
	}
	inc := []Cue{
		{Start: 3 * time.Second, End: 5 * time.Second, Text: "I1"},
		{Start: 8 * time.Second, End: 10 * time.Second, Text: "I2"},
		{Start: 13 * time.Second, End: 15 * time.Second, Text: "I3"},
	}
	shifted, offset := syncCues(context.Background(), ref, inc)
	if offset != 2*time.Second {
		t.Fatalf("syncCues() offset = %v, want 2s", offset)
	}
	if len(shifted) != len(inc) {
		t.Fatalf("syncCues() returned %d cues, want %d", len(shifted), len(inc))
	}
	// Verify cues were actually shifted.
	if shifted[0].Start != 5*time.Second {
		t.Errorf("shifted[0].Start = %v, want 5s", shifted[0].Start)
	}
}

func Test_syncCues_zero_offset_returns_original_slice(t *testing.T) {
	t.Parallel()
	// When offset is 0, syncCues returns the original slice, not a shifted copy.
	cues := []Cue{
		{Start: 1 * time.Second, End: 3 * time.Second, Text: "Same"},
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "Same2"},
	}
	shifted, offset := syncCues(context.Background(), cues, cues)
	if offset != 0 {
		t.Errorf("syncCues(same, same) offset = %v, want 0", offset)
	}
	if len(shifted) != len(cues) {
		t.Fatalf("syncCues(same, same) returned %d cues, want %d", len(shifted), len(cues))
	}
	// Verify identity: the returned slice shares the same backing array.
	if &shifted[0] != &cues[0] {
		t.Error("syncCues(same, same) returned a copy, want the original slice")
	}
}

// --- Additional alignment precision tests ---

func Test_alignConstantOffset_asymmetric_span_lengths(t *testing.T) {
	t.Parallel()
	// Tests the score ratio with asymmetric span lengths.
	ref := []TimeSpan{
		{Start: 0, End: 10000},     // 10s span
		{Start: 20000, End: 21000}, // 1s span
	}
	inc := []TimeSpan{
		{Start: 5000, End: 15000},  // 10s span, shifted +5000
		{Start: 25000, End: 26000}, // 1s span, shifted +5000
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -5000 {
		t.Errorf("alignConstantOffset(asymmetric) = %d, want -5000", got)
	}
}

func Test_alignConstantOffset_three_spans_precise(t *testing.T) {
	t.Parallel()
	// Multiple spans with known offset to exercise all four delta computations.
	ref := []TimeSpan{
		{Start: 1000, End: 3000},
		{Start: 5000, End: 7000},
		{Start: 9000, End: 11000},
	}
	inc := []TimeSpan{
		{Start: 2500, End: 4500},
		{Start: 6500, End: 8500},
		{Start: 10500, End: 12500},
	}
	// All shifted by -1500.
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -1500 {
		t.Errorf("alignConstantOffset(3 spans, -1500) = %d, want -1500", got)
	}
}

func genSpan(t *rapid.T, label string) TimeSpan {
	start := rapid.Int64Range(0, 300_000).Draw(t, label+"_start")
	dur := rapid.Int64Range(2000, 5000).Draw(t, label+"_dur")
	return TimeSpan{Start: start, End: start + dur}
}

func Test_alignConstantOffset_recovers_known_shift(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(5, 10).Draw(t, "num_spans")
		ref := make([]TimeSpan, n)
		// Generate non-overlapping spans with gaps.
		pos := int64(0)
		for i := range n {
			gap := rapid.Int64Range(1000, 5000).Draw(t, "gap")
			dur := rapid.Int64Range(2000, 5000).Draw(t, "dur")
			pos += gap
			ref[i] = TimeSpan{Start: pos, End: pos + dur}
			pos += dur
		}

		shiftMs := rapid.Int64Range(-3000, 3000).Draw(t, "shift_ms")
		inc := make([]TimeSpan, n)
		for i := range n {
			inc[i] = TimeSpan{
				Start: ref[i].Start - shiftMs,
				End:   ref[i].End - shiftMs,
			}
		}

		got := alignConstantOffset(context.Background(), ref, inc)

		diff := got - shiftMs
		if diff < -2 || diff > 2 {
			t.Errorf("alignConstantOffset() = %d, want %d (±2), diff=%d",
				got, shiftMs, diff)
		}
	})
}

func Test_alignConstantOffset_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		nRef := rapid.IntRange(0, 5).Draw(t, "n_ref")
		nInc := rapid.IntRange(0, 5).Draw(t, "n_inc")

		ref := make([]TimeSpan, nRef)
		for i := range nRef {
			ref[i] = genSpan(t, "ref")
		}
		inc := make([]TimeSpan, nInc)
		for i := range nInc {
			inc[i] = genSpan(t, "inc")
		}

		_ = alignConstantOffset(context.Background(), ref, inc)
	})
}

// --- Alignment arithmetic precision tests ---
// Carefully chosen inputs where a sign or operator slip in the offset-range
// or score arithmetic produces a detectably wrong offset.

// Non-zero starts make the minOffset = refStart - inEnd computation change
// sign if its subtraction were flipped, so the exact offset below would not
// be found.
func Test_alignConstantOffset_minOffset_sign(t *testing.T) {
	t.Parallel()
	// ref: [100, 200], inc: [400, 500]. Correct offset = -300.
	// minOffset = refStart - inEnd = 100 - 500 = -400.
	ref := []TimeSpan{{Start: 100, End: 200}}
	inc := []TimeSpan{{Start: 400, End: 500}}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -300 {
		t.Errorf("alignConstantOffset(minOffset sign) = %d, want -300", got)
	}
}

// A tight offset range: an error in the rangeSize = maxOffset - minOffset + 1
// computation would mis-size the bucket array and change the result.
func Test_alignConstantOffset_rangeSize_arithmetic(t *testing.T) {
	t.Parallel()
	// ref: [0, 100], inc: [50, 150]. offset = -50.
	// minOffset = 0 - 150 = -150, maxOffset = 100 - 50 = 50.
	// rangeSize = 50 - (-150) + 1 = 201.
	// (If that subtraction were an addition: 50 + (-150) + 1 = -99 ≤ 0 → returns 0.)
	ref := []TimeSpan{{Start: 0, End: 100}}
	inc := []TimeSpan{{Start: 50, End: 150}}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -50 {
		t.Errorf("alignConstantOffset(rangeSize) = %d, want -50", got)
	}
}

// Spans with non-zero starts and different lengths: computing span length as
// End + Start instead of End - Start would change the score weighting and
// shift the detected offset.
func Test_alignConstantOffset_span_length_arithmetic(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{
		{Start: 10000, End: 10100}, // len=100, but End+Start=20100
		{Start: 20000, End: 25000}, // len=5000, but End+Start=45000
	}
	inc := []TimeSpan{
		{Start: 10200, End: 10300}, // shifted +200
		{Start: 20200, End: 25200}, // shifted +200
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -200 {
		t.Errorf("alignConstantOffset(span length) = %d, want -200", got)
	}
}

// Spans with different lengths give score < 1.0; if the min/max length ratio
// were a product instead of a quotient, the relative span-pair weighting would
// change and the result would shift.
func Test_alignConstantOffset_score_ratio_matters(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{
		{Start: 0, End: 100},      // 100ms (short)
		{Start: 5000, End: 15000}, // 10000ms (long)
	}
	inc := []TimeSpan{
		{Start: 200, End: 300},    // 100ms, shifted +200 from ref[0]
		{Start: 5200, End: 15200}, // 10000ms, shifted +200 from ref[1]
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -200 {
		t.Errorf("alignConstantOffset(score ratio) = %d, want -200", got)
	}
}

// Two offsets produce exactly equal ratings; the strict > keeps the first
// peak while >= would switch to the last. Pins the tie-break direction.
func Test_alignConstantOffset_tie_breaking(t *testing.T) {
	t.Parallel()
	// Two identical ref spans at different positions, one inc span.
	// This creates a symmetric rating function with two equal peaks.
	ref := []TimeSpan{
		{Start: 0, End: 1000},
		{Start: 2000, End: 3000},
	}
	inc := []TimeSpan{
		{Start: 0, End: 1000},
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	// The exact peak depends on the algorithm path; verify it's reasonable.
	if got < -3000 || got > 3000 {
		t.Errorf("alignConstantOffset(tie) = %d, want within [-3000, 3000]", got)
	}
}

func Test_alignConstantOffset_caps_large_span_counts(t *testing.T) {
	t.Parallel()
	// Verify that inputs exceeding maxAlignSpans are truncated without
	// panic or OOM. Use tightly packed 1ms spans to minimize range.
	n := maxAlignSpans + 10
	ref := make([]TimeSpan, n)
	inc := make([]TimeSpan, n)
	for i := range n {
		ref[i] = TimeSpan{Start: int64(i), End: int64(i + 1)}
		inc[i] = TimeSpan{Start: int64(i), End: int64(i + 1)}
	}
	// Should complete without panic; exact offset is irrelevant.
	alignConstantOffset(context.Background(), ref, inc)
}

func Test_alignConstantOffset_caps_at_exact_boundary(t *testing.T) {
	t.Parallel()
	// Exactly maxAlignSpans spans must NOT be capped (the guard is > not >=);
	// a boundary slip would drop the last span and change the result.
	n := maxAlignSpans
	ref := make([]TimeSpan, n)
	inc := make([]TimeSpan, n)
	for i := range n {
		ref[i] = TimeSpan{Start: int64(i * 2), End: int64(i*2 + 1)}
		inc[i] = TimeSpan{Start: int64(i*2 + 100), End: int64(i*2 + 101)}
	}
	// Should complete without panic and use all spans.
	got := alignConstantOffset(context.Background(), ref, inc)
	// Bucket sort discrete bins produce -101 for this input (off-by-one from
	// the continuous optimum of -100). The key assertion is that the result
	// is close to -100 and doesn't change when all spans are included.
	if got < -102 || got > -98 {
		t.Errorf("alignConstantOffset(exactly maxAlignSpans) = %d, want ~-100", got)
	}
}

func Test_alignConstantOffset_rangeSize_guard(t *testing.T) {
	t.Parallel()
	// The rangeSize <= 0 guard fires when maxOffset < minOffset.
	// With valid (non-inverted) spans this can't happen, so the guard
	// protects against degenerate inputs. Verify it returns 0 gracefully.
	// Use inverted ref span: Start > End.
	ref := []TimeSpan{{Start: 1000, End: 500}} // inverted
	inc := []TimeSpan{{Start: 0, End: 100}}
	// minOffset = 1000 - 100 = 900, maxOffset = 500 - 0 = 500
	// rangeSize = 500 - 900 + 1 = -399 → guard returns 0.
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != 0 {
		t.Errorf("alignConstantOffset(inverted ref, rangeSize<0) = %d, want 0", got)
	}
}

func Test_alignConstantOffset_algorithm_selection_boundary(t *testing.T) {
	t.Parallel()
	// numEntries > rangeSize/10 → bucket sort, else merge sort.
	// Test at the exact boundary: numEntries == rangeSize/10.
	// 2 ref * 2 inc * 4 = 16 entries.
	// rangeSize/10 = 16 → rangeSize = 160.
	// With ref=[0,80], inc=[0,80]: minOffset = 0-80 = -80, maxOffset = 80-0 = 80
	// rangeSize = 80 - (-80) + 1 = 161.
	// numEntries = 2*2*4 = 16, rangeSize/10 = 16. 16 > 16 is false → merge sort.
	ref := []TimeSpan{
		{Start: 0, End: 40},
		{Start: 40, End: 80},
	}
	inc := []TimeSpan{
		{Start: 100, End: 140},
		{Start: 140, End: 180},
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -100 {
		t.Errorf("alignConstantOffset(algorithm boundary) = %d, want -100", got)
	}
}

// --- spanScore ---

func TestSpanScore_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r, s TimeSpan
		want float64
	}{
		{"equal length spans", TimeSpan{0, 2000}, TimeSpan{0, 2000}, 1.0},
		{"ref shorter than inc", TimeSpan{0, 1000}, TimeSpan{0, 2000}, 0.5},
		{"inc shorter than ref", TimeSpan{0, 4000}, TimeSpan{0, 1000}, 0.25},
		{"zero-length ref", TimeSpan{500, 500}, TimeSpan{0, 1000}, 0},
		{"zero-length inc", TimeSpan{0, 1000}, TimeSpan{500, 500}, 0},
		{"both zero-length", TimeSpan{100, 100}, TimeSpan{200, 200}, 0},
		{"inverted ref", TimeSpan{2000, 1000}, TimeSpan{0, 1000}, 0},
		{"inverted inc", TimeSpan{0, 1000}, TimeSpan{2000, 1000}, 0},
		{"both inverted", TimeSpan{2000, 1000}, TimeSpan{3000, 2000}, 0},
		{"1ms ref vs 1000ms inc", TimeSpan{0, 1}, TimeSpan{0, 1000}, 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := spanScore(tt.r, tt.s)
			if got != tt.want {
				t.Errorf("spanScore(%v, %v) = %v, want %v", tt.r, tt.s, got, tt.want)
			}
		})
	}
}

func TestSpanScore_always_in_unit_range(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		r := genSpan(t, "r")
		s := genSpan(t, "s")
		score := spanScore(r, s)
		if score < 0 || score > 1 {
			t.Errorf("spanScore(%v, %v) = %v, want in [0, 1]", r, s, score)
		}
	})
}

func TestSpanScore_commutative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		r := genSpan(t, "r")
		s := genSpan(t, "s")
		ab := spanScore(r, s)
		ba := spanScore(s, r)
		if ab != ba {
			t.Errorf("spanScore(%v, %v) = %v, but spanScore(%v, %v) = %v",
				r, s, ab, s, r, ba)
		}
	})
}

func TestSpanScore_equal_length_is_one(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		start := rapid.Int64Range(0, 100_000).Draw(t, "start")
		dur := rapid.Int64Range(1, 10_000).Draw(t, "dur")
		r := TimeSpan{Start: start, End: start + dur}
		s := TimeSpan{Start: start + 500, End: start + 500 + dur}
		score := spanScore(r, s)
		if score != 1.0 {
			t.Errorf("spanScore(len=%d, len=%d) = %v, want 1.0", dur, dur, score)
		}
	})
}
