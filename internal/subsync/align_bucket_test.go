package subsync

import (
	"context"
	"testing"
)

func TestAlignBucketSort_zero_length_spans_skipped(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{
		{Start: 1000, End: 1000}, // zero-length
		{Start: 5000, End: 7000},
	}
	inc := []TimeSpan{
		{Start: 5000, End: 7000},
	}
	// Should not panic on zero-length spans.
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != 0 {
		t.Errorf("alignConstantOffset() with zero-length ref span = %d, want 0", got)
	}
}

func TestAddDelta_boundary_conditions(t *testing.T) {
	t.Parallel()
	deltas := make([]float64, 10)

	// Valid index.
	addDelta(deltas, 5, 1.5, 10)
	if deltas[5] != 1.5 {
		t.Errorf("addDelta(5) = %v, want 1.5", deltas[5])
	}

	// Snapshot the slice to verify no-op cases don't corrupt it.
	snapshot := make([]float64, len(deltas))
	copy(snapshot, deltas)

	// Negative index — should be no-op.
	addDelta(deltas, -1, 1.0, 10)

	// Index at size — should be no-op.
	addDelta(deltas, 10, 1.0, 10)

	// Index beyond size — should be no-op.
	addDelta(deltas, 100, 1.0, 10)

	for i := range deltas {
		if deltas[i] != snapshot[i] {
			t.Errorf("addDelta(out-of-bounds) modified deltas[%d]: got %v, want %v",
				i, deltas[i], snapshot[i])
		}
	}
}

func TestAlignBucketSort_direct(t *testing.T) {
	t.Parallel()
	// Call alignBucketSort directly with a small range.
	ref := []TimeSpan{
		{Start: 1000, End: 3000},
		{Start: 5000, End: 7000},
	}
	inc := []TimeSpan{
		{Start: 1000, End: 3000},
		{Start: 5000, End: 7000},
	}
	got := alignBucketSort(context.Background(), ref, inc, -6000, 6000)
	// Bucket sort returns -1 for identical spans due to discrete indexing.
	if got != -1 {
		t.Errorf("alignBucketSort(same spans) = %d, want -1", got)
	}
}

func TestAlignBucketSort_with_offset(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{
		{Start: 5000, End: 7000},
		{Start: 10000, End: 12000},
	}
	inc := []TimeSpan{
		{Start: 3000, End: 5000},
		{Start: 8000, End: 10000},
	}
	got := alignBucketSort(context.Background(), ref, inc, -9000, 9000)
	// Bucket sort may be off by 1 from merge sort due to discrete bins.
	if got != 1999 {
		t.Errorf("alignBucketSort(+2000 offset) = %d, want 1999", got)
	}
}

func TestAlignBucketSort_size_computation(t *testing.T) {
	t.Parallel()
	// Direct call to verify size computation is correct.
	ref := []TimeSpan{{Start: 1000, End: 2000}}
	inc := []TimeSpan{{Start: 1000, End: 2000}}
	// minOffset = 1000-2000 = -1000, maxOffset = 2000-1000 = 1000
	got := alignBucketSort(context.Background(), ref, inc, -1000, 1000)
	// Should find offset ~0 for identical spans (bucket sort returns -1 due to discrete bins).
	if got < -2 || got > 2 {
		t.Errorf("alignBucketSort(identical spans) = %d, want ~0", got)
	}
}

func TestAlignBucketSort_size_overflow_guard(t *testing.T) {
	t.Parallel()
	// Range > 100_000_000 triggers fallback to merge sort.
	// Exact boundary (100M) would require ~800MB allocation, so we test
	// a clearly-over-limit range and verify the fallback produces correct results.
	ref := []TimeSpan{{Start: 0, End: 1000}}
	inc := []TimeSpan{{Start: 0, End: 1000}}
	got := alignBucketSort(context.Background(), ref, inc, 0, 200_000_000)
	if got < -2 || got > 2 {
		t.Errorf("alignBucketSort(huge range fallback) = %d, want ~0", got)
	}
}

func TestAlignBucketSort_score_sign_matters(t *testing.T) {
	t.Parallel()
	// The four addDelta calls create a tent function. If signs are wrong,
	// the peak moves to the wrong offset.
	ref := []TimeSpan{{Start: 5000, End: 8000}}
	inc := []TimeSpan{{Start: 2000, End: 5000}}
	// Expected offset: +3000 (shift inc right by 3000 to align with ref).
	got := alignBucketSort(context.Background(), ref, inc, -5000, 6000)
	// Bucket sort may be off by 1 from continuous optimum.
	if got < 2998 || got > 3002 {
		t.Errorf("alignBucketSort(+3000 offset) = %d, want ~3000", got)
	}
}

func TestAlignBucketSort_addDelta_offsets(t *testing.T) {
	t.Parallel()
	// The fourth addDelta uses r.End-inc.Start. If this is wrong, the
	// tent function shape changes and the peak offset shifts.
	ref := []TimeSpan{
		{Start: 0, End: 4000},
		{Start: 10000, End: 14000},
	}
	inc := []TimeSpan{
		{Start: 3000, End: 7000},
		{Start: 13000, End: 17000},
	}
	// Expected offset: -3000.
	got := alignBucketSort(context.Background(), ref, inc, -17000, 14000)
	if got < -3002 || got > -2998 {
		t.Errorf("alignBucketSort(-3000 offset) = %d, want ~-3000", got)
	}
}

func TestAlignBucketSort_score_formula(t *testing.T) {
	t.Parallel()
	// score = minF(rLen, iLen) / maxF(rLen, iLen)
	// With different-length spans, the score < 1.0. If the formula is wrong,
	// the relative weighting of span pairs changes.
	ref := []TimeSpan{
		{Start: 0, End: 1000},    // length 1000
		{Start: 5000, End: 9000}, // length 4000
	}
	inc := []TimeSpan{
		{Start: 2000, End: 3000},  // length 1000
		{Start: 7000, End: 11000}, // length 4000
	}
	// Both pairs shifted by -2000.
	got := alignBucketSort(context.Background(), ref, inc, -11000, 9000)
	if got < -2002 || got > -1998 {
		t.Errorf("alignBucketSort(mixed lengths, -2000 offset) = %d, want ~-2000", got)
	}
}

func TestAlignBucketSort_bestRating_tracking(t *testing.T) {
	t.Parallel()
	// The bestOffset = i + minOffset computation. If wrong, the returned offset is wrong.
	ref := []TimeSpan{{Start: 10000, End: 12000}}
	inc := []TimeSpan{{Start: 5000, End: 7000}}
	got := alignBucketSort(context.Background(), ref, inc, -7000, 7000)
	// Expected: offset ~5000 (shift inc right by 5000).
	if got < 4998 || got > 5002 {
		t.Errorf("alignBucketSort(+5000 offset) = %d, want ~5000", got)
	}
}

func TestAlignBucketSort_zero_length_incorrect_span_skipped(t *testing.T) {
	t.Parallel()
	// The table test's zero_length_incorrect_spans case goes through the merge
	// sort path (range too large for bucket sort). This test calls
	// alignBucketSort directly to cover the zero-length skip in the bucket sort
	// inner loop.
	ref := []TimeSpan{{Start: 1000, End: 3000}}
	inc := []TimeSpan{
		{Start: 2000, End: 2000}, // zero-length — must be skipped
		{Start: 1000, End: 3000}, // valid — drives the result
	}
	got := alignBucketSort(context.Background(), ref, inc, -3000, 3000)
	// With only the valid pair (identical spans), offset should be ~0.
	if got < -2 || got > 2 {
		t.Errorf("alignBucketSort(zero-length inc span) = %d, want ~0", got)
	}
}

func TestAlignBucketSort_first_peak_wins_on_tie(t *testing.T) {
	t.Parallel()
	// The strict > comparison means the earlier offset wins when two offsets
	// have equal rating; >= would let the later peak win instead. This
	// symmetric setup places two ref spans equidistant from one inc span.
	ref := []TimeSpan{
		{Start: 0, End: 1000},
		{Start: 4000, End: 5000},
	}
	inc := []TimeSpan{
		{Start: 2000, End: 3000},
	}
	got := alignBucketSort(context.Background(), ref, inc, -3000, 3000)
	if got > 0 {
		t.Errorf("alignBucketSort(symmetric peaks) = %d, want <= 0 (first peak wins)", got)
	}
}
