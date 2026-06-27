package crosslang

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDPAlign(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		pairs     []CuePair
		wantLen   int
		checkMono bool
	}{
		{"empty", nil, 0, false},
		{"single_pair", []CuePair{{IncIdx: 0, RefIdx: 0, Score: 1.0}}, 1, true},
		{
			"monotonic_input",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.5},
				{IncIdx: 1, RefIdx: 1, Score: 0.8},
				{IncIdx: 2, RefIdx: 2, Score: 0.7},
			},
			3, true,
		},
		{
			"crossing_pairs_selects_optimal",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.5},
				{IncIdx: 1, RefIdx: 2, Score: 0.8},
				{IncIdx: 2, RefIdx: 1, Score: 0.9}, // crosses with previous
				{IncIdx: 3, RefIdx: 3, Score: 0.7},
			},
			-1, true, // length varies; just check monotonicity
		},
		{
			"prefers_higher_score_path",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.1},
				{IncIdx: 0, RefIdx: 1, Score: 0.9}, // same IncIdx, higher score
				{IncIdx: 1, RefIdx: 2, Score: 0.5},
			},
			2, true, // picks (0,1,0.9) then (1,2,0.5)
		},
		{
			"large_input_exceeds_dpMaxPredecessors",
			func() []CuePair {
				pairs := make([]CuePair, 400)
				for i := range pairs {
					pairs[i] = CuePair{IncIdx: i, RefIdx: i, Score: 0.5}
				}
				return pairs
			}(),
			400, true,
		},
		{
			"unsorted_input",
			[]CuePair{
				{IncIdx: 3, RefIdx: 3, Score: 0.7},
				{IncIdx: 0, RefIdx: 0, Score: 0.5},
				{IncIdx: 1, RefIdx: 1, Score: 0.8},
			},
			3, true,
		},
		{
			"duplicate_indices_different_scores",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.3},
				{IncIdx: 0, RefIdx: 0, Score: 0.9},
				{IncIdx: 1, RefIdx: 1, Score: 0.5},
			},
			2, true, // picks best from duplicates
		},
		{
			"all_same_index",
			[]CuePair{
				{IncIdx: 5, RefIdx: 5, Score: 0.3},
				{IncIdx: 5, RefIdx: 5, Score: 0.9},
				{IncIdx: 5, RefIdx: 5, Score: 0.5},
			},
			1, false, // can only pick one
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DPAlign(tt.pairs)
			if tt.wantLen >= 0 && len(got) != tt.wantLen {
				t.Errorf("DPAlign() returned %d pairs, want %d", len(got), tt.wantLen)
			}
			if tt.checkMono {
				for i := 1; i < len(got); i++ {
					if got[i].IncIdx <= got[i-1].IncIdx {
						t.Errorf("IncIdx not strictly increasing at %d: %d <= %d",
							i, got[i].IncIdx, got[i-1].IncIdx)
					}
					if got[i].RefIdx <= got[i-1].RefIdx {
						t.Errorf("RefIdx not strictly increasing at %d: %d <= %d",
							i, got[i].RefIdx, got[i-1].RefIdx)
					}
				}
			}
		})
	}
}

func TestDPAlign_tieBreakKeepsNearerPredecessor(t *testing.T) {
	t.Parallel()
	// Node (2,3) can chain through either (1,1) or (1,2); both give an equal
	// accumulated score. The predecessor scan walks backward from the node, so
	// the nearer candidate (1,2) is seen first and kept, fixing the path's
	// middle node.
	pairs := []CuePair{
		{IncIdx: 0, RefIdx: 0, Score: 5.0},
		{IncIdx: 1, RefIdx: 1, Score: 1.0},
		{IncIdx: 1, RefIdx: 2, Score: 1.0},
		{IncIdx: 2, RefIdx: 3, Score: 2.0},
	}
	got := DPAlign(pairs)
	if len(got) != 3 {
		t.Fatalf("DPAlign() len = %d, want 3 (path %+v)", len(got), got)
	}
	if got[1].IncIdx != 1 || got[1].RefIdx != 2 {
		t.Errorf("DPAlign() middle = (Inc %d, Ref %d), want (1, 2)",
			got[1].IncIdx, got[1].RefIdx)
	}
}

func TestDPAlign_tieBreakKeepsEarliestEndNode(t *testing.T) {
	t.Parallel()
	// Two independent end nodes with equal score and no chain between them
	// (same IncIdx). The earliest is chosen as the path end.
	pairs := []CuePair{
		{IncIdx: 0, RefIdx: 0, Score: 3.0},
		{IncIdx: 0, RefIdx: 1, Score: 3.0},
	}
	got := DPAlign(pairs)
	if len(got) != 1 {
		t.Fatalf("DPAlign() len = %d, want 1 (path %+v)", len(got), got)
	}
	if got[0].RefIdx != 0 {
		t.Errorf("DPAlign() best RefIdx = %d, want 0", got[0].RefIdx)
	}
}

func TestWeightedMedianOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		pairs []CuePair
		want  int64
	}{
		{"empty", nil, 0},
		{"single", []CuePair{{Score: 1.0, OffsetMs: 500}}, 500},
		{
			"two_equal_weight",
			[]CuePair{
				{Score: 1.0, OffsetMs: 100},
				{Score: 1.0, OffsetMs: 200},
			},
			100, // cum reaches half at first element
		},
		{
			"skewed_weight_picks_heavy",
			[]CuePair{
				{Score: 0.1, OffsetMs: 100},
				{Score: 10.0, OffsetMs: 500},
			},
			500,
		},
		{
			"three_middle_wins",
			[]CuePair{
				{Score: 1.0, OffsetMs: 100},
				{Score: 1.0, OffsetMs: 300},
				{Score: 1.0, OffsetMs: 500},
			},
			300,
		},
		{
			"unsorted_input",
			[]CuePair{
				{Score: 1.0, OffsetMs: 500},
				{Score: 1.0, OffsetMs: 100},
				{Score: 1.0, OffsetMs: 300},
			},
			300,
		},
		{
			"all_zero_weight",
			[]CuePair{
				{Score: 0.0, OffsetMs: 100},
				{Score: 0.0, OffsetMs: 300},
				{Score: 0.0, OffsetMs: 500},
			},
			100, // half=0, cum>=half on first sorted element
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := WeightedMedianOffset(tt.pairs)
			if got != tt.want {
				t.Errorf("WeightedMedianOffset() = %d, want %d", got, tt.want)
			}
		})
	}
}

// makeNumberedCues builds n cues 3s apart, each carrying a distinct number so
// anchor matching can lock onto the correct pairing. shift offsets every cue
// (used to simulate a constant timing error).
func makeNumberedCues(n int, shift time.Duration) []Cue {
	cues := make([]Cue, n)
	for i := range cues {
		start := time.Duration(i)*3*time.Second + shift
		cues[i] = Cue{
			Start: start,
			End:   start + 2*time.Second,
			Text:  fmt.Sprintf("It is %d.", 100+i),
		}
	}
	return cues
}

// makeUniformCues builds n cues with identical text starting at start with a
// fixed gap. A zero gap collapses every cue onto start (a zero-duration track).
func makeUniformCues(n int, start, gap time.Duration, text string) []Cue {
	cues := make([]Cue, n)
	for i := range cues {
		s := start + time.Duration(i)*gap
		cues[i] = Cue{Start: s, End: s + time.Second, Text: text}
	}
	return cues
}

// TestAlign_recoversConstantOffset is an oracle test: when the incorrect track
// is the reference shifted by a known constant offset, Align should recover
// that offset and realign the cues onto the reference timeline.
func TestAlign_recoversConstantOffset(t *testing.T) {
	t.Parallel()
	ref := makeNumberedCues(10, 0)
	inc := makeNumberedCues(10, 500*time.Millisecond) // 500ms late

	got := Align(context.Background(), ref, inc)

	if got.Confidence <= 0.3 {
		t.Fatalf("Align confidence = %v, want > 0.3 for a clean constant offset", got.Confidence)
	}
	if got.Offset != -500 {
		t.Errorf("Align offset = %d ms, want -500", got.Offset)
	}
	if got.Rate != 1.0 {
		t.Errorf("Align rate = %v, want 1.0", got.Rate)
	}
	if len(got.Cues) != len(inc) {
		t.Fatalf("Align returned %d cues, want %d", len(got.Cues), len(inc))
	}
	// Applying the recovered offset realigns the late cues with the reference.
	if got.Cues[0].Start != ref[0].Start {
		t.Errorf("first shifted cue start = %v, want %v", got.Cues[0].Start, ref[0].Start)
	}
	if got.Cues[len(got.Cues)-1].Start != ref[len(ref)-1].Start {
		t.Errorf("last shifted cue start = %v, want %v",
			got.Cues[len(got.Cues)-1].Start, ref[len(ref)-1].Start)
	}
}

// TestAlign_earlyReturnsHaveZeroConfidence pins each guard clause that makes
// Align bail out with the unmodified input and zero confidence.
func TestAlign_earlyReturnsHaveZeroConfidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  []Cue
		inc  []Cue
	}{
		{"too_few_reference_cues", makeUniformCues(4, 0, 5*time.Second, "Hello 42"), makeUniformCues(10, 0, 5*time.Second, "Bonjour 42")},
		{"too_few_incorrect_cues", makeUniformCues(10, 0, 5*time.Second, "Hello 42"), makeUniformCues(4, 0, 5*time.Second, "Bonjour 42")},
		{"too_few_anchored_cues", makeUniformCues(10, 0, 5*time.Second, "oh no"), makeUniformCues(10, 0, 5*time.Second, "ah bon")},
		{"zero_duration_reference", makeUniformCues(10, -10*time.Second, 0, "Hello 42"), makeUniformCues(10, 0, 5*time.Second, "Bonjour 42")},
		{
			// Reference clustered at the start, incorrect at the far end: their
			// normalized positions never overlap inside the ±10% pass-1 window.
			"too_few_pass1_candidates",
			makeUniformCues(10, 0, 500*time.Millisecond, "Hello 42"),
			makeUniformCues(10, 50*time.Second, 500*time.Millisecond, "Bonjour 42"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Align(context.Background(), tt.ref, tt.inc)
			if got.Confidence != 0 {
				t.Errorf("Align(%s) confidence = %v, want 0", tt.name, got.Confidence)
			}
			if len(got.Cues) != len(tt.inc) {
				t.Errorf("Align(%s) returned %d cues, want %d (input unchanged)",
					tt.name, len(got.Cues), len(tt.inc))
			}
		})
	}
}
