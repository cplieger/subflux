package subsync

import (
	"context"
	"log/slog"
	"time"
)

// syncCues aligns downloaded subtitle cues to reference cues.
// Returns the shifted cues and the applied offset.
func syncCues(ctx context.Context, reference, incorrect []Cue) ([]Cue, time.Duration) {
	refSpans := cuesToSpans(reference)
	incSpans := cuesToSpans(incorrect)

	offsetMs := alignConstantOffset(ctx, refSpans, incSpans)
	offset := time.Duration(offsetMs) * time.Millisecond

	slog.Debug("subtitle sync complete",
		"offset_ms", offsetMs,
		"ref_cues", len(reference),
		"inc_cues", len(incorrect))

	if offsetMs == 0 {
		return incorrect, 0
	}
	return ShiftCues(incorrect, offset), offset
}

// maxAlignSpans caps the number of spans per input to prevent O(n*m) memory
// exhaustion in the alignment algorithm. A typical movie has ~1500 cues;
// 10 000 provides generous headroom. Beyond this, only the first N spans
// are used (still produces a usable alignment for the covered portion).
const maxAlignSpans = 10_000

// maxAlignEvents caps the total event allocation in merge sort to ~256 MB
// (16 bytes per event). This prevents OOM when crafted SRT files with
// extreme timestamps force the bucket sort → merge sort fallback.
const maxAlignEvents = 16_000_000

// maxBucketRangeMs caps the bucket-sort delta array at 32M float64 entries
// (~256 MB — the same memory class as maxAlignEvents). Larger offset ranges
// only arise from pathological or crafted timestamps (32M ms ≈ 9 hours of
// offset), where the event-based merge sort computes the same peak without
// the dense allocation; a single align call must never approach the 1 GiB
// container envelope on its own.
const maxBucketRangeMs = 32_000_000

// alignConstantOffset finds the single best constant time shift to align
// the "incorrect" subtitle spans to the "reference" spans.
//
// This is a Go port of alass's align_constant_delta algorithm.
// It computes a piecewise-linear rating function over all possible offsets
// using differential computation (delta-of-deltas), then finds the maximum.
//
// Returns the optimal offset in milliseconds.
func alignConstantOffset(ctx context.Context, reference, incorrect []TimeSpan) int64 {
	if len(reference) == 0 || len(incorrect) == 0 {
		return 0
	}
	if len(reference) > maxAlignSpans {
		slog.Warn("alignment: capping reference spans",
			"original", len(reference), "cap", maxAlignSpans)
		reference = reference[:maxAlignSpans]
	}
	if len(incorrect) > maxAlignSpans {
		slog.Warn("alignment: capping incorrect spans",
			"original", len(incorrect), "cap", maxAlignSpans)
		incorrect = incorrect[:maxAlignSpans]
	}

	refStart := reference[0].Start
	refEnd := reference[len(reference)-1].End
	inStart := incorrect[0].Start
	inEnd := incorrect[len(incorrect)-1].End

	minOffset := refStart - inEnd
	maxOffset := refEnd - inStart

	rangeSize := maxOffset - minOffset + 1
	if rangeSize <= 0 {
		return 0
	}

	numEntries := int64(len(incorrect)) * int64(len(reference)) * 4
	// Integer division is intentional: for small ranges, bucket sort is
	// always preferred since the array allocation is trivial.
	if numEntries > rangeSize/10 {
		slog.Debug("alignment: chose bucket sort",
			"ref_spans", len(reference), "inc_spans", len(incorrect),
			"range_ms", rangeSize, "num_entries", numEntries)
		return alignBucketSort(ctx, reference, incorrect, minOffset, maxOffset)
	}
	slog.Debug("alignment: chose merge sort",
		"ref_spans", len(reference), "inc_spans", len(incorrect),
		"range_ms", rangeSize, "num_entries", numEntries)
	return alignMergeSort(ctx, reference, incorrect, minOffset)
}

// spanScore returns the overlap quality score for a reference/incorrect span pair.
// Zero-length or inverted spans return 0 (caller should skip).
func spanScore(r, s TimeSpan) float64 {
	rLen := float64(r.End - r.Start)
	sLen := float64(s.End - s.Start)
	if rLen <= 0 || sLen <= 0 {
		return 0
	}
	return min(rLen, sLen) / max(rLen, sLen)
}

// alignBucketSort uses a dense array indexed by offset to accumulate rating
// derivative changes. Each span pair contributes 4 delta entries (the
// breakpoints of the piecewise-linear rating function). A single sweep
// integrates the deltas twice (derivative -> rating) to find the peak.
// Efficient when the offset range is small relative to the number of span pairs.
func alignBucketSort(ctx context.Context, ref, inc []TimeSpan, minOffset, maxOffset int64) int64 {
	size := maxOffset - minOffset + 1

	// Bounded dense allocation (~256 MB max, see maxBucketRangeMs); fall
	// back to the event-based merge sort beyond it to avoid OOM.
	if size > maxBucketRangeMs {
		slog.Warn("alignment range too large, falling back to merge sort",
			"range_ms", size)
		return alignMergeSort(ctx, ref, inc, minOffset)
	}

	deltas := make([]float64, size)

	if !accumulateDeltas(ctx, deltas, ref, inc, minOffset, size) {
		return 0
	}

	var derivative, rating, bestRating float64
	var bestOffset int64

	for i := range size {
		derivative += deltas[i]
		rating += derivative
		if rating > bestRating {
			bestRating = rating
			bestOffset = i + minOffset
		}
	}

	slog.Debug("alignment complete (bucket)",
		"offset_ms", bestOffset, "rating", bestRating, "range_ms", size)
	return bestOffset
}

// accumulateDeltas fills the bucket-sort delta array with the piecewise-linear
// rating-derivative breakpoints for every reference/incorrect span pair. It
// reports false if the context was cancelled mid-accumulation.
func accumulateDeltas(ctx context.Context, deltas []float64, ref, inc []TimeSpan, minOffset, size int64) bool {
	var iterations int
	for _, r := range ref {
		for _, s := range inc {
			iterations++
			if iterations%10000 == 0 && ctx.Err() != nil {
				return false
			}
			score := spanScore(r, s)
			if score == 0 {
				continue
			}
			addDelta(deltas, r.Start-s.End-minOffset, score, size)
			addDelta(deltas, r.End-s.End-minOffset, -score, size)
			addDelta(deltas, r.Start-s.Start-minOffset, -score, size)
			addDelta(deltas, r.End-s.Start-minOffset, score, size)
		}
	}
	return true
}

// addDelta safely adds val to deltas[idx] if idx is within bounds.
func addDelta(deltas []float64, idx int64, val float64, size int64) {
	if idx >= 0 && idx < size {
		deltas[idx] += val
	}
}
