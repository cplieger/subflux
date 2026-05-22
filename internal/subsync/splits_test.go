package subsync

import (
	"context"
	"testing"
	"time"
)

func TestAlignWithSplits_empty_inputs(t *testing.T) {
	t.Parallel()
	result := alignWithSplits(context.Background(), nil, nil, 0)
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence, got %f", float64(result.Confidence))
	}
}

func TestAlignWithSplits_empty_reference(t *testing.T) {
	t.Parallel()
	inc := makeCues(10, 0, 2*time.Second)
	result := alignWithSplits(context.Background(), nil, inc, 0)
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence, got %f", float64(result.Confidence))
	}
}

func TestAlignWithSplits_identical_subtitles(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(30, 10*time.Minute)
	result := alignWithSplits(context.Background(), cues, cues, 0)
	if result.Method != MethodSplit {
		t.Fatalf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_constant_offset(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	result := alignWithSplits(context.Background(), ref, inc, 0)
	if result.Method != MethodSplit {
		t.Fatalf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_single_segment_fallback(t *testing.T) {
	t.Parallel()
	// When all cues have the same offset, detectSplits returns a single
	// segment (len(splits) <= 1), triggering the Sync fallback path.
	// Use a very high penalty to force a single segment.
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 3*time.Second)
	result := alignWithSplits(context.Background(), ref, inc, 1e12)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
	if result.Confidence != ConfidenceModerate {
		t.Errorf("expected moderate confidence for single-segment fallback, got %f",
			float64(result.Confidence))
	}
	// The offset should be close to -3000ms (shifting inc back to align with ref).
	if result.Offset < -3500 || result.Offset > -2500 {
		t.Errorf("expected offset ~-3000ms, got %d", result.Offset)
	}
}

func TestAlignWithSplits_two_segments(t *testing.T) {
	t.Parallel()
	// Create reference with 20 cues.
	ref := makeLongCues(20, 10*time.Minute)

	// Create incorrect with two different offsets:
	// first 10 cues shifted by +1s, last 10 by +5s.
	inc := make([]Cue, 20)
	for i := range 10 {
		inc[i] = Cue{
			Start: ref[i].Start + time.Second,
			End:   ref[i].End + time.Second,
			Text:  ref[i].Text,
		}
	}
	for i := 10; i < 20; i++ {
		inc[i] = Cue{
			Start: ref[i].Start + 5*time.Second,
			End:   ref[i].End + 5*time.Second,
			Text:  ref[i].Text,
		}
	}

	result := alignWithSplits(context.Background(), ref, inc, 500)
	if result.Confidence == ConfidenceNone {
		t.Fatal("expected some confidence for two-segment case")
	}
	if len(result.Cues) != 20 {
		t.Fatalf("expected 20 cues, got %d", len(result.Cues))
	}
}

func TestDetectSplits_single_segment(t *testing.T) {
	t.Parallel()
	offsets := make([]perCueOffset, 10)
	for i := range offsets {
		offsets[i] = perCueOffset{offsetMs: 1000}
	}
	splits := detectSplits(offsets, 10000)
	// High penalty should produce a single segment.
	if len(splits) != 1 {
		t.Fatalf("expected 1 split point, got %d", len(splits))
	}
	if splits[0] != 0 {
		t.Fatalf("expected split at 0, got %d", splits[0])
	}
}

func TestDetectSplits_two_segments(t *testing.T) {
	t.Parallel()
	offsets := make([]perCueOffset, 20)
	for i := range 10 {
		offsets[i] = perCueOffset{offsetMs: 1000}
	}
	for i := 10; i < 20; i++ {
		offsets[i] = perCueOffset{offsetMs: 5000}
	}
	// Low penalty should detect the split.
	splits := detectSplits(offsets, 1)
	if len(splits) < 2 {
		t.Fatalf("expected at least 2 split points, got %d", len(splits))
	}
}

func TestDetectSplits_empty(t *testing.T) {
	t.Parallel()
	splits := detectSplits(nil, 1000)
	if splits != nil {
		t.Fatalf("expected nil, got %v", splits)
	}
}

func TestSegmentCost_uniform(t *testing.T) {
	t.Parallel()
	offsets := []perCueOffset{
		{offsetMs: 100},
		{offsetMs: 100},
		{offsetMs: 100},
	}
	cost := segmentCost(offsets)
	if cost != 0 {
		t.Fatalf("expected 0 cost for uniform offsets, got %f", cost)
	}
}

func TestSegmentCost_varied(t *testing.T) {
	t.Parallel()
	offsets := []perCueOffset{
		{offsetMs: 0},
		{offsetMs: 1000},
	}
	cost := segmentCost(offsets)
	// mean=500, variance=(0+1e6)/2 - 250000 = 250000, cost = 250000*2 = 500000
	if cost != 500000 {
		t.Errorf("segmentCost([0,1000]) = %f, want 500000", cost)
	}
}

func TestSegmentCost_single(t *testing.T) {
	t.Parallel()
	offsets := []perCueOffset{{offsetMs: 500}}
	cost := segmentCost(offsets)
	if cost != 0 {
		t.Fatalf("expected 0 for single element, got %f", cost)
	}
}

func TestSegmentCost_large_identical_values(t *testing.T) {
	t.Parallel()
	// Large identical values can cause floating-point cancellation in
	// sumSq/n - mean*mean. The variance < 0 guard handles this.
	offsets := make([]perCueOffset, 100)
	for i := range offsets {
		offsets[i] = perCueOffset{offsetMs: 1_000_000_000}
	}
	cost := segmentCost(offsets)
	if cost < 0 {
		t.Errorf("segmentCost(large identical) = %f, want >= 0", cost)
	}
}

func TestSegmentConfidence_no_segments(t *testing.T) {
	t.Parallel()
	c := segmentConfidence(nil, nil, nil)
	if c != ConfidenceNone {
		t.Fatalf("expected none, got %f", float64(c))
	}
}

func TestSegmentConfidence_single_segment_good_overlap(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(50, 10*time.Minute)
	refSpans := cuesToSpans(ref)
	// Single segment with correct offset (perfect overlap).
	segs := []segment{{startIdx: 0, endIdx: 50, offset: 0}}
	c := segmentConfidence(segs, ref, refSpans)
	if c < 0.7 {
		t.Fatalf("expected high confidence for perfect overlap, got %f", float64(c))
	}
}

func TestSegmentConfidence_many_segments(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(100, 10*time.Minute)
	refSpans := cuesToSpans(ref)
	segs := make([]segment, 10)
	for i := range segs {
		segs[i] = segment{startIdx: i * 10, endIdx: (i + 1) * 10, offset: 0}
	}
	c := segmentConfidence(segs, ref, refSpans)
	// Many segments should lower confidence even with good overlap.
	if c > 0.6 {
		t.Fatalf("expected lower confidence for many segments, got %f", float64(c))
	}
}

func TestBuildSegments_merges_tiny(t *testing.T) {
	t.Parallel()
	ref := cuesToSpans(makeLongCues(20, 10*time.Minute))
	inc := makeLongCues(20, 10*time.Minute)
	// Splits at 0, 15, 18 (last segment has only 2 cues).
	splits := []int{0, 15, 18}
	segs := buildSegments(context.Background(), ref, inc, splits)
	// The tiny segment [18:20] should be merged with [15:18].
	for _, seg := range segs {
		size := seg.endIdx - seg.startIdx
		if size < minSegmentCues && size < len(inc) {
			// Only acceptable if it's the only segment.
			if len(segs) > 1 {
				t.Fatalf("segment [%d:%d] has only %d cues, should have been merged",
					seg.startIdx, seg.endIdx, size)
			}
		}
	}
}

func TestMaxSegmentLen(t *testing.T) {
	t.Parallel()
	if maxSegmentLen(100) != 100 {
		t.Fatal("expected 100 for small n")
	}
	if maxSegmentLen(1000) != 500 {
		t.Fatal("expected 500 cap for large n")
	}
}

func TestAlignWithSplits_default_penalty(t *testing.T) {
	t.Parallel()
	// splitPenalty <= 0 should use defaultSplitPenalty.
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	result := alignWithSplits(context.Background(), ref, inc, 0)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_negative_penalty(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	result := alignWithSplits(context.Background(), ref, inc, -100)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_empty_incorrect(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(10, 5*time.Minute)
	result := alignWithSplits(context.Background(), ref, nil, 0)
	if result.Confidence != ConfidenceNone {
		t.Errorf("expected no confidence for nil incorrect, got %f",
			float64(result.Confidence))
	}
}

func TestDetectSplits_many_segments_capped(t *testing.T) {
	t.Parallel()
	// Create offsets with many distinct values to trigger maxSplits cap.
	offsets := make([]perCueOffset, 200)
	for i := range offsets {
		// Each group of 5 has a different offset, creating 40 potential segments.
		offsets[i] = perCueOffset{offsetMs: int64(i/5) * 10000}
	}
	splits := detectSplits(offsets, 1) // very low penalty → many splits
	if len(splits) > maxSplits+1 {
		t.Errorf("detectSplits returned %d splits, want <= %d", len(splits), maxSplits+1)
	}
}

func TestPerCueOffsets_basic(t *testing.T) {
	t.Parallel()
	// Use ref spans with varying lengths so each inc cue has a unique best match.
	ref := []TimeSpan{
		{Start: 0, End: 5000},      // 5s
		{Start: 10000, End: 12000}, // 2s
	}
	inc := []Cue{
		{Start: 0, End: 5 * time.Second, Text: "A"},                 // matches ref[0] perfectly
		{Start: 10 * time.Second, End: 12 * time.Second, Text: "B"}, // matches ref[1] perfectly
	}
	offsets := perCueOffsets(context.Background(), ref, inc)
	if len(offsets) != 2 {
		t.Fatalf("perCueOffsets returned %d, want 2", len(offsets))
	}
	// Both cues match their corresponding ref span with offset 0.
	for i, o := range offsets {
		if o.offsetMs != 0 {
			t.Errorf("perCueOffsets[%d].offsetMs = %d, want 0", i, o.offsetMs)
		}
	}
}

func TestPerCueOffsets_no_overlap(t *testing.T) {
	t.Parallel()
	// Reference and incorrect don't overlap at all.
	ref := cuesToSpans(makeCues(5, 0, 2*time.Second))
	inc := makeCues(5, time.Hour, 2*time.Second)
	offsets := perCueOffsets(context.Background(), ref, inc)
	if len(offsets) != 5 {
		t.Fatalf("perCueOffsets returned %d, want 5", len(offsets))
	}
}

func TestSegmentConfidence_zero_total_cues(t *testing.T) {
	t.Parallel()
	segs := []segment{{startIdx: 0, endIdx: 10}}
	c := segmentConfidence(segs, nil, nil)
	if c != ConfidenceNone {
		t.Errorf("segmentConfidence(nil cues) = %f, want 0", float64(c))
	}
}

func TestSegmentConfidence_zero_length_ref_spans(t *testing.T) {
	t.Parallel()
	// All refSpans have zero length (Start == End), so totalRef == 0.
	// This hits the totalRef == 0 guard after the overlap loop.
	inc := makeCues(5, 0, 2*time.Second)
	refSpans := []TimeSpan{
		{Start: 0, End: 0},
		{Start: 1000, End: 1000},
	}
	segs := []segment{{startIdx: 0, endIdx: 5, offset: 0}}
	c := segmentConfidence(segs, inc, refSpans)
	if c != ConfidenceNone {
		t.Errorf("segmentConfidence(zero-length refs) = %f, want 0", float64(c))
	}
}

func TestSegmentConfidence_penalty_floor(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(150, 10*time.Minute)
	refSpans := cuesToSpans(ref)
	segs := make([]segment, 15)
	for i := range segs {
		segs[i] = segment{startIdx: i * 10, endIdx: (i + 1) * 10, offset: 0}
	}
	c := segmentConfidence(segs, ref, refSpans)
	// 15 segments: penalty floor should cap confidence low.
	if c > 0.5 {
		t.Errorf("segmentConfidence(15 segments) = %f, want <= 0.5", float64(c))
	}
}

func TestSegmentConfidence_overlap_ratio_capped(t *testing.T) {
	t.Parallel()
	// Craft a scenario where totalOverlap > totalRef, triggering the
	// overlapRatio > 1.0 cap. Use a short reference span and a long
	// incorrect cue that fully covers it multiple times via overlap.
	inc := []Cue{
		{Start: 0, End: 10 * time.Second, Text: "long cue"},
	}
	// Reference span is very short (100ms). The corrected cue (10s) fully
	// covers it, so overlap = 100ms and totalRef = 100ms → ratio = 1.0.
	// To get ratio > 1.0, we need multiple ref spans that the same cue overlaps.
	refSpans := []TimeSpan{
		{Start: 0, End: 50},
		{Start: 100, End: 150},
	}
	segs := []segment{{startIdx: 0, endIdx: 1, offset: 0}}
	c := segmentConfidence(segs, inc, refSpans)
	// Should not exceed maxConf (0.85 for 1 segment).
	if c > Confidence(0.86) {
		t.Errorf("segmentConfidence(overlap ratio capped) = %f, want <= 0.85", float64(c))
	}
	if c == ConfidenceNone {
		t.Error("segmentConfidence(overlap ratio capped) = 0, want > 0")
	}
}

func TestAlignSegments_preserves_text(t *testing.T) {
	t.Parallel()
	inc := []Cue{
		{Start: time.Second, End: 2 * time.Second, Text: "hello"},
		{Start: 3 * time.Second, End: 4 * time.Second, Text: "world"},
	}
	segs := []segment{
		{startIdx: 0, endIdx: 2, offset: 500 * time.Millisecond},
	}
	corrected := alignSegments(inc, segs)
	if corrected[0].Text != "hello" {
		t.Errorf("alignSegments lost text: got %q, want %q", corrected[0].Text, "hello")
	}
	if corrected[0].Start != time.Second+500*time.Millisecond {
		t.Errorf("alignSegments[0].Start = %v, want 1.5s", corrected[0].Start)
	}
}

func TestAlignSegments_negative_offset_clamps_to_zero(t *testing.T) {
	t.Parallel()
	inc := []Cue{
		{Start: 500 * time.Millisecond, End: time.Second, Text: "early"},
	}
	segs := []segment{
		{startIdx: 0, endIdx: 1, offset: -2 * time.Second},
	}
	corrected := alignSegments(inc, segs)
	if corrected[0].Start != 0 {
		t.Errorf("alignSegments should clamp negative start to 0, got %v", corrected[0].Start)
	}
	if corrected[0].End != 0 {
		t.Errorf("alignSegments should clamp negative end to 0, got %v", corrected[0].End)
	}
}

func TestAlignWithSplits_three_segments(t *testing.T) {
	t.Parallel()
	// Create reference with 30 cues and incorrect with three different offsets.
	ref := makeLongCues(30, 15*time.Minute)
	inc := make([]Cue, 30)
	for i := range 10 {
		inc[i] = Cue{
			Start: ref[i].Start + time.Second,
			End:   ref[i].End + time.Second,
			Text:  ref[i].Text,
		}
	}
	for i := 10; i < 20; i++ {
		inc[i] = Cue{
			Start: ref[i].Start + 8*time.Second,
			End:   ref[i].End + 8*time.Second,
			Text:  ref[i].Text,
		}
	}
	for i := 20; i < 30; i++ {
		inc[i] = Cue{
			Start: ref[i].Start + 15*time.Second,
			End:   ref[i].End + 15*time.Second,
			Text:  ref[i].Text,
		}
	}

	result := alignWithSplits(context.Background(), ref, inc, 100)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
	if result.Confidence == ConfidenceNone {
		t.Error("expected some confidence for three-segment case")
	}
	if len(result.Cues) != 30 {
		t.Errorf("expected 30 cues, got %d", len(result.Cues))
	}
}

func TestBuildSegments_single_split(t *testing.T) {
	t.Parallel()
	ref := cuesToSpans(makeLongCues(20, 10*time.Minute))
	inc := makeLongCues(20, 10*time.Minute)
	splits := []int{0, 10}
	segs := buildSegments(context.Background(), ref, inc, splits)
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[0].startIdx != 0 || segs[0].endIdx != 10 {
		t.Errorf("segment 0: [%d:%d], want [0:10]", segs[0].startIdx, segs[0].endIdx)
	}
	if segs[1].startIdx != 10 || segs[1].endIdx != 20 {
		t.Errorf("segment 1: [%d:%d], want [10:20]", segs[1].startIdx, segs[1].endIdx)
	}
}

func TestSegmentCost_empty(t *testing.T) {
	t.Parallel()
	cost := segmentCost(nil)
	if cost != 0 {
		t.Errorf("segmentCost(nil) = %f, want 0", cost)
	}
}

func TestAlignSegments_empty_segments(t *testing.T) {
	t.Parallel()
	inc := []Cue{
		{Start: time.Second, End: 2 * time.Second, Text: "unchanged"},
	}
	corrected := alignSegments(inc, nil)
	if len(corrected) != 1 {
		t.Fatalf("alignSegments(nil segments) returned %d cues, want 1", len(corrected))
	}
	if corrected[0].Start != time.Second {
		t.Errorf("alignSegments(nil segments)[0].Start = %v, want 1s", corrected[0].Start)
	}
	if corrected[0].Text != "unchanged" {
		t.Errorf("alignSegments(nil segments)[0].Text = %q, want %q", corrected[0].Text, "unchanged")
	}
}

func TestAlignSegments_segment_exceeds_cue_count(t *testing.T) {
	t.Parallel()
	inc := []Cue{
		{Start: time.Second, End: 2 * time.Second, Text: "only one"},
	}
	segs := []segment{
		{startIdx: 0, endIdx: 10, offset: 500 * time.Millisecond},
	}
	corrected := alignSegments(inc, segs)
	if len(corrected) != 1 {
		t.Fatalf("alignSegments(oversized segment) returned %d cues, want 1", len(corrected))
	}
	if corrected[0].Start != time.Second+500*time.Millisecond {
		t.Errorf("alignSegments(oversized segment)[0].Start = %v, want 1.5s", corrected[0].Start)
	}
}

func TestBuildSegments_empty_splits(t *testing.T) {
	t.Parallel()
	ref := cuesToSpans(makeLongCues(10, 5*time.Minute))
	inc := makeLongCues(10, 5*time.Minute)
	segs := buildSegments(context.Background(), ref, inc, nil)
	if len(segs) != 0 {
		t.Errorf("buildSegments(nil splits) returned %d segments, want 0", len(segs))
	}
}
