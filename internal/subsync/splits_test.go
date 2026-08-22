package subsync

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAlignWithSplits_empty_inputs(t *testing.T) {
	t.Parallel()
	result := alignWithSplits(t.Context(), nil, nil, 0)
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence, got %f", float64(result.Confidence))
	}
}

func TestAlignWithSplits_empty_reference(t *testing.T) {
	t.Parallel()
	inc := makeCues(10, 0, 2*time.Second)
	result := alignWithSplits(t.Context(), nil, inc, 0)
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence, got %f", float64(result.Confidence))
	}
}

func TestAlignWithSplits_identical_subtitles(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(30, 10*time.Minute)
	result := alignWithSplits(t.Context(), cues, cues, 0)
	if result.Method != MethodSplit {
		t.Fatalf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_constant_offset(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	result := alignWithSplits(t.Context(), ref, inc, 0)
	if result.Method != MethodSplit {
		t.Fatalf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_no_split_emits_no_candidate(t *testing.T) {
	t.Parallel()
	// When all cues share one offset, detectSplits returns a single segment
	// (len(splits) <= 1). The constant-offset hypothesis is already the
	// offset generator's candidate, so the split generator must emit
	// nothing: zero confidence and unchanged cues (R1.1). A very high
	// penalty forces the single segment.
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 3*time.Second)
	result := alignWithSplits(t.Context(), ref, inc, 1e12)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
	if result.Confidence != ConfidenceNone {
		t.Errorf("expected no candidate (zero confidence) for no-split input, got %f",
			float64(result.Confidence))
	}
	if result.Offset != 0 {
		t.Errorf("expected no correction (offset 0), got %d", result.Offset)
	}
	for i := range inc {
		if result.Cues[i] != inc[i] {
			t.Fatalf("cue %d changed: got %+v, want input unchanged", i, result.Cues[i])
		}
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

	result := alignWithSplits(t.Context(), ref, inc, 500)
	if result.Confidence == ConfidenceNone {
		t.Error("expected some confidence for two-segment case")
	}
	if len(result.Cues) != 20 {
		t.Errorf("expected 20 cues, got %d", len(result.Cues))
	}
	// Multi-segment results carry the segments out as a transform descriptor.
	if result.Source != SourceSplit {
		t.Errorf("source = %v, want split", result.Source)
	}
	if result.Transform.Kind != TransformSegments {
		t.Errorf("transform kind = %v, want segments", result.Transform.Kind)
	}
	// Tiny-segment merging can collapse detected splits, so only the
	// descriptor's presence is guaranteed, not a segment count.
	if len(result.Transform.Segments) < 1 {
		t.Errorf("transform segments = %d, want >= 1", len(result.Transform.Segments))
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
	// The offsets change exactly once, at index 10, so that is the only split
	// worth making: any extra one costs the penalty and saves nothing.
	if want := []int{0, 10}; !slices.Equal(splits, want) {
		t.Errorf("detectSplits(two offset blocks of 10, penalty 1) = %v, want %v", splits, want)
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
	segs := buildSegments(t.Context(), ref, inc, splits)
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
	if got := maxSegmentLen(100); got != 100 {
		t.Errorf("maxSegmentLen(100) = %d, want 100 (no cap below the ceiling)", got)
	}
	if got := maxSegmentLen(1000); got != 500 {
		t.Errorf("maxSegmentLen(1000) = %d, want 500 (capped)", got)
	}
}

func TestAlignWithSplits_default_penalty(t *testing.T) {
	t.Parallel()
	// splitPenalty <= 0 should use defaultSplitPenalty.
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	result := alignWithSplits(t.Context(), ref, inc, 0)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_negative_penalty(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	result := alignWithSplits(t.Context(), ref, inc, -100)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
}

func TestAlignWithSplits_empty_incorrect(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(10, 5*time.Minute)
	result := alignWithSplits(t.Context(), ref, nil, 0)
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
	// 40 groups are available, so the cap is what decides the count: it must be
	// reached exactly, neither exceeded nor undershot.
	if len(splits) != maxSplits+1 {
		t.Errorf("detectSplits(40 offset groups, penalty 1) returned %d splits, want %d",
			len(splits), maxSplits+1)
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
	offsets := perCueOffsets(t.Context(), ref, inc)
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
	offsets := perCueOffsets(t.Context(), ref, inc)
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

func TestOverlapTotal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		corr        []TimeSpan
		ref         []TimeSpan
		wantOverlap float64
		wantRef     float64
	}{
		{
			// Each reference span accumulates the UNION of all corrected
			// spans overlapping it: two adjacent 5s corrected spans fully
			// cover a 10s reference span (previously only the first
			// counted, rating the pair 0.5).
			name:        "reference span covered by two adjacent corrected spans counts fully",
			corr:        []TimeSpan{{Start: 0, End: 5000}, {Start: 5000, End: 10000}},
			ref:         []TimeSpan{{Start: 0, End: 10000}},
			wantOverlap: 10000,
			wantRef:     10000,
		},
		{
			// (0,6000) and (4000,10000) overlap each other by 2000; the
			// union is 10000, never 12000.
			name:        "overlapping corrected spans are not double-counted",
			corr:        []TimeSpan{{Start: 0, End: 6000}, {Start: 4000, End: 10000}},
			ref:         []TimeSpan{{Start: 0, End: 10000}},
			wantOverlap: 10000,
			wantRef:     10000,
		},
		{
			// A corrected span fully contained in the already-covered
			// stretch contributes nothing.
			name:        "contained corrected span adds nothing",
			corr:        []TimeSpan{{Start: 0, End: 8000}, {Start: 2000, End: 3000}},
			ref:         []TimeSpan{{Start: 0, End: 10000}},
			wantOverlap: 8000,
			wantRef:     10000,
		},
		{
			// One corrected span may overlap consecutive reference spans;
			// it counts against each (per-span union, per-span cap).
			name:        "one corrected span overlaps consecutive reference spans",
			corr:        []TimeSpan{{Start: 0, End: 3000}},
			ref:         []TimeSpan{{Start: 0, End: 1000}, {Start: 2000, End: 3000}},
			wantOverlap: 2000,
			wantRef:     2000,
		},
		{
			name:        "disjoint spans overlap zero",
			corr:        []TimeSpan{{Start: 5000, End: 6000}},
			ref:         []TimeSpan{{Start: 0, End: 1000}},
			wantOverlap: 0,
			wantRef:     1000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotOverlap, gotRef := overlapTotal(tt.corr, tt.ref)
			if gotOverlap != tt.wantOverlap || gotRef != tt.wantRef {
				t.Errorf("overlapTotal = (%v, %v), want (%v, %v)",
					gotOverlap, gotRef, tt.wantOverlap, tt.wantRef)
			}
		})
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

	result := alignWithSplits(t.Context(), ref, inc, 100)
	if result.Method != MethodSplit {
		t.Errorf("expected method 'split', got %q", result.Method)
	}
	if result.Confidence == ConfidenceNone {
		t.Error("expected some confidence for three-segment case")
	}
	if len(result.Cues) != 30 {
		t.Errorf("expected 30 cues, got %d", len(result.Cues))
	}
	if result.Transform.Kind != TransformSegments {
		t.Errorf("transform kind = %v, want segments", result.Transform.Kind)
	}
	if got, want := len(result.Transform.Segments), 2; got < want {
		t.Errorf("transform segments = %d, want >= %d", got, want)
	}
}

func TestBuildSegments_single_split(t *testing.T) {
	t.Parallel()
	ref := cuesToSpans(makeLongCues(20, 10*time.Minute))
	inc := makeLongCues(20, 10*time.Minute)
	splits := []int{0, 10}
	segs := buildSegments(t.Context(), ref, inc, splits)
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
	segs := buildSegments(t.Context(), ref, inc, nil)
	if len(segs) != 0 {
		t.Errorf("buildSegments(nil splits) returned %d segments, want 0", len(segs))
	}
}

// jitteredPair returns a reference track and a copy shifted by 2s whose
// per-cue best offsets differ by at most 3ms. Each span has a distinct
// duration, so every cue's best-scoring reference span is its positional
// counterpart and the resulting offsets carry only that millisecond jitter —
// a total squared deviation far below defaultSplitPenalty.
func jitteredPair() (reference, incorrect []Cue) {
	const n = 30
	reference = make([]Cue, n)
	incorrect = make([]Cue, n)
	for i := range n {
		dur := time.Duration(100+i*7) * time.Millisecond
		refStart := time.Duration(i*1000) * time.Millisecond
		incStart := refStart + 2*time.Second + time.Duration(i%4)*time.Millisecond
		reference[i] = Cue{Start: refStart, End: refStart + dur, Text: "ref"}
		incorrect[i] = Cue{Start: incStart, End: incStart + dur, Text: "inc"}
	}
	return reference, incorrect
}

// A penalty of zero means "use the default", not "splits are free". On a track
// whose offsets wander by only a few milliseconds the default suppresses every
// split, so the generator emits no candidate and hands the cues back untouched;
// a literal zero penalty would instead split at nearly every cue.
func TestAlignWithSplits_non_positive_penalty_falls_back_to_the_default(t *testing.T) {
	t.Parallel()
	ref, inc := jitteredPair()
	tests := []struct {
		name    string
		penalty float64
	}{
		{name: "zero", penalty: 0},
		{name: "negative", penalty: -5},
		{name: "the_default_stated_explicitly", penalty: defaultSplitPenalty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := alignWithSplits(t.Context(), ref, inc, tt.penalty)
			if got.Confidence != ConfidenceNone {
				t.Errorf("alignWithSplits(millisecond jitter, penalty=%v).Confidence = %v, want %v",
					tt.penalty, float64(got.Confidence), float64(ConfidenceNone))
			}
			if len(got.Transform.Segments) != 0 {
				t.Errorf("alignWithSplits(millisecond jitter, penalty=%v) produced %d segments, want 0: %+v",
					tt.penalty, len(got.Transform.Segments), got.Transform.Segments)
			}
			if !slices.Equal(got.Cues, inc) {
				t.Errorf("alignWithSplits(millisecond jitter, penalty=%v) altered the cues; want them returned verbatim",
					tt.penalty)
			}
		})
	}
}

// twoBlockPair returns a reference track and a copy whose second half carries a
// different shift, so split detection finds exactly one split point.
func twoBlockPair() (reference, incorrect []Cue) {
	const n = 40
	reference = make([]Cue, n)
	incorrect = make([]Cue, n)
	for i := range n {
		start := time.Duration(i*2000) * time.Millisecond
		dur := time.Duration(300+i*11) * time.Millisecond
		reference[i] = Cue{Start: start, End: start + dur, Text: "ref"}
		shift := 3 * time.Second
		if i >= n/2 {
			shift = 12 * time.Second
		}
		incorrect[i] = Cue{Start: start + shift, End: start + dur + shift, Text: "inc"}
	}
	return reference, incorrect
}

// The completion line reports split POINTS, one fewer than the segment count:
// two segments are separated by a single split. An operator reading the line
// compares it against the segment count, so the two must agree.
func TestAlignWithSplits_logs_one_fewer_split_than_segments(t *testing.T) {
	// slog's default logger is process-global: this test must stay serial.
	ref, inc := twoBlockPair()
	var got SyncResult
	logs := captureAlignLogs(t, func() {
		got = alignWithSplits(t.Context(), ref, inc, 0)
	})
	if len(got.Transform.Segments) != 2 {
		t.Fatalf("alignWithSplits(two-block track) produced %d segments, want 2: %+v",
			len(got.Transform.Segments), got.Transform.Segments)
	}
	if !strings.Contains(logs, "segments=2") {
		t.Errorf("alignWithSplits(two-block track) did not report segments=2; logged:\n%s", logs)
	}
	if !strings.Contains(logs, "splits=1") {
		t.Errorf("alignWithSplits(two-block track) did not report splits=1; logged:\n%s", logs)
	}
}

// Two reference spans of equal length score identically against a cue, and the
// incumbent is only replaced by a strictly better score, so the earlier span
// wins. The choice decides the cue's offset, so the direction is load-bearing.
func TestPerCueOffsets_keeps_the_first_of_two_equally_scoring_spans(t *testing.T) {
	t.Parallel()
	// Both reference spans are 1000ms, as is the cue, so both score 1.0.
	refSpans := []TimeSpan{
		{Start: 0, End: 1000},
		{Start: 5000, End: 6000},
	}
	inc := []Cue{{Start: 2 * time.Second, End: 3 * time.Second, Text: "cue"}}
	got := perCueOffsets(t.Context(), refSpans, inc)
	if len(got) != 1 {
		t.Fatalf("perCueOffsets(2 reference spans, 1 cue) returned %d offsets, want 1", len(got))
	}
	if got[0].offsetMs != -2000 {
		t.Errorf("perCueOffsets(equal-length reference spans at 0 and 5000) = %d, want -2000 (the earlier span)",
			got[0].offsetMs)
	}
}

// A zero-length reference span cannot overlap anything, so it scores zero and
// must never become a cue's chosen offset — a cue with no usable reference is
// reported as needing no shift.
func TestPerCueOffsets_ignores_a_reference_span_that_cannot_overlap(t *testing.T) {
	t.Parallel()
	refSpans := []TimeSpan{{Start: 4000, End: 4000}}
	inc := []Cue{{Start: 2 * time.Second, End: 3 * time.Second, Text: "cue"}}
	got := perCueOffsets(t.Context(), refSpans, inc)
	if len(got) != 1 {
		t.Fatalf("perCueOffsets(1 zero-length reference span, 1 cue) returned %d offsets, want 1", len(got))
	}
	if got[0].offsetMs != 0 {
		t.Errorf("perCueOffsets(zero-length reference span at 4000) = %d, want 0", got[0].offsetMs)
	}
}

// A segment holding exactly minSegmentCues cues is large enough to keep its own
// offset; only a shorter one folds into its neighbour. Getting this boundary
// wrong merges a genuine timing change away, which shows up as a visibly
// mistimed tail.
func TestBuildSegments_keeps_a_trailing_segment_of_exactly_the_minimum_size(t *testing.T) {
	t.Parallel()
	inc := make([]Cue, 20)
	for i := range inc {
		start := time.Duration(i*1000) * time.Millisecond
		inc[i] = Cue{Start: start, End: start + 400*time.Millisecond, Text: "cue"}
	}
	refSpans := cuesToSpans(inc)
	tests := []struct {
		name        string
		trailing    int
		wantCount   int
		wantFirstTo int
	}{
		{name: "one_below_the_minimum", trailing: minSegmentCues - 1, wantCount: 1, wantFirstTo: 20},
		{name: "exactly_the_minimum", trailing: minSegmentCues, wantCount: 2, wantFirstTo: 15},
		{name: "one_above_the_minimum", trailing: minSegmentCues + 1, wantCount: 2, wantFirstTo: 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildSegments(t.Context(), refSpans, inc, []int{0, len(inc) - tt.trailing})
			if len(got) != tt.wantCount {
				t.Fatalf("buildSegments(20 cues, trailing segment of %d) returned %d segments, want %d: %+v",
					tt.trailing, len(got), tt.wantCount, got)
			}
			if got[0].endIdx != tt.wantFirstTo {
				t.Errorf("buildSegments(20 cues, trailing segment of %d) first segment ends at %d, want %d",
					tt.trailing, got[0].endIdx, tt.wantFirstTo)
			}
		})
	}
}

// Confidence is the measured overlap ratio scaled by a ceiling that drops by
// SplitPenaltyPerSegment for every segment beyond the first. Half the reference
// time covered across two segments therefore rates 0.5 * (0.85 - 0.05).
func TestSegmentConfidence_scales_the_overlap_ratio_by_the_segment_ceiling(t *testing.T) {
	t.Parallel()
	// Four 1s cues, left in place by two zero-offset segments.
	inc := []Cue{
		{Start: 0, End: time.Second, Text: "a"},
		{Start: 2 * time.Second, End: 3 * time.Second, Text: "b"},
		{Start: 4 * time.Second, End: 5 * time.Second, Text: "c"},
		{Start: 6 * time.Second, End: 7 * time.Second, Text: "d"},
	}
	// 4000ms of reference time, of which the cues cover 2000ms.
	refSpans := []TimeSpan{
		{Start: 0, End: 1000},
		{Start: 2000, End: 3000},
		{Start: 20000, End: 22000},
	}
	tests := []struct {
		name     string
		segments []segment
		want     float64
	}{
		{
			name:     "one_segment_pays_no_penalty",
			segments: []segment{{startIdx: 0, endIdx: 4}},
			want:     0.5 * 0.85,
		},
		{
			name:     "two_segments_pay_one_penalty",
			segments: []segment{{startIdx: 0, endIdx: 2}, {startIdx: 2, endIdx: 4}},
			want:     0.5 * (0.85 - 0.05),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Tolerance covers only the ordering of the same float64 operations;
			// every mutation of this arithmetic moves the result by >= 0.05.
			const tolerance = 1e-9
			got := float64(segmentConfidence(tt.segments, inc, refSpans))
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("segmentConfidence(%d segments, half the reference covered) = %v, want %v (+/- %v)",
					len(tt.segments), got, tt.want, tolerance)
			}
		})
	}
}

// A split must pay for itself: when segmenting costs exactly what it saves, the
// coarser answer wins. Preferring the finer one on a tie splits a track that has
// no real timing change in it.
func TestDetectSplits_prefers_the_coarser_segmentation_on_a_tie(t *testing.T) {
	t.Parallel()
	// Total squared deviation of [0 0 10 10] as one segment is exactly 100;
	// split at index 2 it is 0, so a penalty of 100 makes the two equal.
	offsets := []perCueOffset{{offsetMs: 0}, {offsetMs: 0}, {offsetMs: 10}, {offsetMs: 10}}
	tests := []struct {
		name    string
		penalty float64
		want    []int
	}{
		{name: "the_split_pays_for_itself", penalty: 99, want: []int{0, 2}},
		{name: "the_split_exactly_breaks_even", penalty: 100, want: []int{0}},
		{name: "the_split_costs_more_than_it_saves", penalty: 101, want: []int{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectSplits(offsets, tt.penalty)
			if !slices.Equal(got, tt.want) {
				t.Errorf("detectSplits([0 0 10 10], penalty=%v) = %v, want %v", tt.penalty, got, tt.want)
			}
		})
	}
}
