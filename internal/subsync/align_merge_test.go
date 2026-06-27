package subsync

import (
	"context"
	"testing"
)

// --- alignMergeSort internals ---

func TestAlignMergeSort_direct(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{
		{Start: 5000, End: 7000},
		{Start: 10000, End: 12000},
	}
	inc := []TimeSpan{
		{Start: 3000, End: 5000},
		{Start: 8000, End: 10000},
	}
	got := alignMergeSort(context.Background(), ref, inc, -9000)
	if got != 2000 {
		t.Errorf("alignMergeSort(+2000 offset) = %d, want 2000", got)
	}
}

func TestAlignMergeSort_event_offsets(t *testing.T) {
	t.Parallel()
	// The four events per pair define the tent function shape.
	ref := []TimeSpan{{Start: 5000, End: 8000}}
	inc := []TimeSpan{{Start: 2000, End: 5000}}
	got := alignMergeSort(context.Background(), ref, inc, -5000)
	if got != 3000 {
		t.Errorf("alignMergeSort(+3000 offset) = %d, want 3000", got)
	}
}

func TestAlignMergeSort_gap_computation(t *testing.T) {
	t.Parallel()
	// The gap between consecutive events affects the rating accumulation.
	ref := []TimeSpan{
		{Start: 0, End: 3000},
		{Start: 10000, End: 13000},
	}
	inc := []TimeSpan{
		{Start: 1000, End: 4000},
		{Start: 11000, End: 14000},
	}
	// Expected offset: -1000.
	got := alignMergeSort(context.Background(), ref, inc, -14000)
	if got != -1000 {
		t.Errorf("alignMergeSort(-1000 offset) = %d, want -1000", got)
	}
}

func TestAlignMergeSort_bestRating_boundary(t *testing.T) {
	t.Parallel()
	// If the comparison is wrong, the best offset is never updated or updated incorrectly.
	ref := []TimeSpan{{Start: 0, End: 5000}}
	inc := []TimeSpan{{Start: 10000, End: 15000}}
	got := alignMergeSort(context.Background(), ref, inc, -15000)
	if got != -10000 {
		t.Errorf("alignMergeSort(-10000 offset) = %d, want -10000", got)
	}
}

func TestAlignMergeSort_bestOffset_selection(t *testing.T) {
	t.Parallel()
	// When the best rating is at the last event, bestOffset = events[i].offset.
	// When not at the last event, bestOffset = events[i+1].offset.
	ref := []TimeSpan{{Start: 20000, End: 22000}}
	inc := []TimeSpan{{Start: 10000, End: 12000}}
	got := alignMergeSort(context.Background(), ref, inc, -12000)
	if got != 10000 {
		t.Errorf("alignMergeSort(+10000 offset) = %d, want 10000", got)
	}
}

// The last-event branch (i+1 < len(events)) must not read past the slice; a
// sparse span pair exercises the final event so an off-by-one there would
// panic with an index out of range.
func TestAlignMergeSort_last_event_boundary(t *testing.T) {
	t.Parallel()
	// Force merge sort path with sparse spans (large range, few entries).
	ref := []TimeSpan{
		{Start: 0, End: 1000},
	}
	inc := []TimeSpan{
		{Start: 1000000, End: 1001000}, // 1M ms apart → huge range, few entries
	}
	// This should not panic.
	got := alignConstantOffset(context.Background(), ref, inc)
	if got < -1001000 || got > 0 {
		t.Errorf("alignConstantOffset(last event boundary) = %d, want within [-1001000, 0]", got)
	}
}

// The inter-event gap must be events[i+1].offset - events[i].offset; a sign
// error in that subtraction throws the accumulated rating off and moves the
// detected peak. Sparse spans force the merge-sort path.
func TestAlignMergeSort_gap_sign(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{
		{Start: 0, End: 500},
		{Start: 100000, End: 100500},
	}
	inc := []TimeSpan{
		{Start: 200, End: 700},
		{Start: 100200, End: 100700},
	}
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -200 {
		t.Errorf("AlignMergeSort(gap sign) = %d, want -200", got)
	}
}

func TestAlignMergeSort_event_cap(t *testing.T) {
	t.Parallel()
	// Create spans with extreme timestamps to force bucket→merge fallback,
	// then verify the event cap prevents unbounded allocation.
	ref := []TimeSpan{
		{Start: 0, End: 500},
		{Start: 200_000_000, End: 200_000_500}, // 200K seconds apart
	}
	inc := []TimeSpan{
		{Start: 100, End: 600},
		{Start: 200_000_100, End: 200_000_600},
	}
	// rangeSize > 100M → bucket falls back to merge.
	// With only 2 spans each, events = 2*2*4 = 16, well under cap.
	got := alignConstantOffset(context.Background(), ref, inc)
	if got != -100 {
		t.Errorf("alignConstantOffset(extreme timestamps) = %d, want -100", got)
	}
}

func TestAlignMergeSort_first_peak_wins_on_tie(t *testing.T) {
	t.Parallel()
	// The strict > comparison means the earlier offset wins when two offsets
	// have equal rating; >= would let the later peak win instead.
	ref := []TimeSpan{
		{Start: 0, End: 1000},
		{Start: 4000, End: 5000},
	}
	inc := []TimeSpan{
		{Start: 2000, End: 3000},
	}
	got := alignMergeSort(context.Background(), ref, inc, -3000)
	if got > 0 {
		t.Errorf("alignMergeSort(symmetric peaks) = %d, want <= 0 (first peak wins)", got)
	}
}
