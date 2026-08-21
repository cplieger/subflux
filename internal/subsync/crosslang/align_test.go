package crosslang

import (
	"bytes"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"
)

// Abort vs report in this file: a value mismatch reports with t.Errorf so
// the siblings still run. A cue-count check keeps t.Fatalf when later lines
// index the slice it counted (got.Cues[0], got.Cues[len-1]).

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
			got := dpAlign(tt.pairs)
			if tt.wantLen >= 0 && len(got) != tt.wantLen {
				t.Errorf("dpAlign() returned %d pairs, want %d", len(got), tt.wantLen)
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
	got := dpAlign(pairs)
	if len(got) != 3 {
		t.Fatalf("dpAlign() len = %d, want 3 (path %+v)", len(got), got)
	}
	if got[1].IncIdx != 1 || got[1].RefIdx != 2 {
		t.Errorf("dpAlign() middle = (Inc %d, Ref %d), want (1, 2)",
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
	got := dpAlign(pairs)
	if len(got) != 1 {
		t.Fatalf("dpAlign() len = %d, want 1 (path %+v)", len(got), got)
	}
	if got[0].RefIdx != 0 {
		t.Errorf("dpAlign() best RefIdx = %d, want 0", got[0].RefIdx)
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
			got := weightedMedianOffset(tt.pairs)
			if got != tt.want {
				t.Errorf("weightedMedianOffset() = %d, want %d", got, tt.want)
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

	got := Align(t.Context(), ref, inc)

	if got.Confidence <= 0.3 {
		t.Errorf("Align confidence = %v, want > 0.3 for a clean constant offset", got.Confidence)
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
	// Applying the recovered offset realigns the late cues with the reference,
	// both ends of every cue: a cue shifted only at its start would silently
	// change its own duration.
	if got.Cues[0].Start != ref[0].Start {
		t.Errorf("first shifted cue start = %v, want %v", got.Cues[0].Start, ref[0].Start)
	}
	if got.Cues[0].End != ref[0].End {
		t.Errorf("first shifted cue end = %v, want %v", got.Cues[0].End, ref[0].End)
	}
	if got.Cues[len(got.Cues)-1].Start != ref[len(ref)-1].Start {
		t.Errorf("last shifted cue start = %v, want %v",
			got.Cues[len(got.Cues)-1].Start, ref[len(ref)-1].Start)
	}
	if got.Cues[len(got.Cues)-1].End != ref[len(ref)-1].End {
		t.Errorf("last shifted cue end = %v, want %v",
			got.Cues[len(got.Cues)-1].End, ref[len(ref)-1].End)
	}
}

func TestAbs64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   int64
		want int64
	}{
		{"negative", -1500, 1500},
		{"minus_one", -1, 1},
		{"zero", 0, 0},
		{"positive", 1500, 1500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := abs64(tt.in); got != tt.want {
				t.Errorf("abs64(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestFindFirstGE pins the half-open lower bound the windowed reference scan is
// built on: the answer is the FIRST index whose start time is at or after the
// target, so a target landing exactly on a reference cue includes that cue
// rather than skipping past it, and a run of equal start times resolves to the
// earliest of them.
func TestFindFirstGE(t *testing.T) {
	t.Parallel()
	refs := []strongRef{{startMs: 10}, {startMs: 20}, {startMs: 20}, {startMs: 30}}
	tests := []struct {
		name   string
		target int64
		want   int
	}{
		{"before_the_first", 5, 0},
		{"exactly_the_first", 10, 0},
		{"between_two", 15, 1},
		{"exactly_a_repeated_start", 20, 1},
		{"past_the_repeated_run", 25, 3},
		{"exactly_the_last", 30, 3},
		{"past_the_last", 35, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := findFirstGE(refs, tt.target); got != tt.want {
				t.Errorf("findFirstGE(%v, %d) = %d, want %d", refs, tt.target, got, tt.want)
			}
		})
	}
}

// TestEstimateMaxWindowMs pins the scan-width estimate that bounds how far the
// candidate search looks either side of an incorrect cue. It binary-searches
// FORWARD for the widest offset the window still accepts, then pads that by a
// tenth plus a one-second cushion so a window whose true edge sits between two
// probes is not clipped. A window that accepts nothing forward collapses to the
// cushion alone.
func TestEstimateMaxWindowMs(t *testing.T) {
	t.Parallel()
	// The reference set only supplies the search's upper bound (the last start
	// time); which cue is which does not matter here.
	refs := []strongRef{{startMs: 0, origIdx: 0}, {startMs: 5000, origIdx: 1}}
	tests := []struct {
		name   string
		refs   []strongRef
		window windowFunc
		want   int64
	}{
		{
			// Accepts a reference cue within 20ms in either direction.
			name: "window_open_both_ways",
			refs: refs,
			window: func(incStartMs, refStartMs int64) (bool, float64) {
				return abs64(refStartMs-incStartMs) <= 20, 0
			},
			want: 1022,
		},
		{
			// The same width but only later in time — the shape the second pass
			// takes once a large rough offset moves its window off the cue.
			name: "window_open_forward_only",
			refs: refs,
			window: func(incStartMs, refStartMs int64) (bool, float64) {
				d := refStartMs - incStartMs
				return d >= 1 && d <= 20, 0
			},
			want: 1022,
		},
		{
			name: "no_strongly_anchored_references",
			refs: nil,
			window: func(incStartMs, refStartMs int64) (bool, float64) {
				return true, 0
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := estimateMaxWindowMs(tt.refs, 0, tt.window); got != tt.want {
				t.Errorf("estimateMaxWindowMs(%v, 0, %s) = %d, want %d",
					tt.refs, tt.name, got, tt.want)
			}
		})
	}
}

// numberMatchWindow accepts any reference cue within 2000ms of the incorrect
// cue and reports a normalized distance of half the window, so the position
// term of a blended score is a known constant.
func numberMatchWindow(incStartMs, refStartMs int64) (bool, float64) {
	return abs64(refStartMs-incStartMs) <= 2000, 0.5
}

// TestScoredCandidatesForCue_blendsThePositionIntoTheAnchorScore pins the
// blend: a candidate's score is nine tenths of its anchor score plus one tenth
// of how much of the window it did NOT use up. Here the anchor score is the
// number weight over the number plus punctuation weights (0.4/0.45) and the
// position credit is half, giving 0.9*0.888... + 0.1*0.5.
func TestScoredCandidatesForCue_blendsThePositionIntoTheAnchorScore(t *testing.T) {
	t.Parallel()
	// The second reference cue is out of the window; it is here only to give the
	// width search an upper bound above the window itself.
	refs := []strongRef{{startMs: 1000, origIdx: 0}, {startMs: 5000, origIdx: 1}}
	incAnchors := []anchor{{Numbers: []string{"100"}}}
	refAnchors := []anchor{{Numbers: []string{"100"}}, {}}

	got := scoredCandidatesForCue(0, 0, refs, incAnchors, refAnchors, numberMatchWindow, 7)

	if len(got) != 1 {
		t.Fatalf("scoredCandidatesForCue() returned %d candidates, want 1 (%+v)", len(got), got)
	}
	const wantScore = 0.8500000000000001
	if math.Abs(got[0].score-wantScore) > scoreTol {
		t.Errorf("scoredCandidatesForCue() score = %v, want %v", got[0].score, wantScore)
	}
	if got[0].offsetMs != 1000 {
		t.Errorf("scoredCandidatesForCue() offsetMs = %d, want 1000", got[0].offsetMs)
	}
	if got[0].refIdx != 0 {
		t.Errorf("scoredCandidatesForCue() refIdx = %d, want 0", got[0].refIdx)
	}
}

// TestScoredCandidatesForCue_keepsAnAnchorScoreExactlyAtTheMinimum pins the
// admission boundary: a pair whose anchor score lands exactly on the configured
// minimum is kept, not dropped. Every feature is present on both sides and none
// of them matches, so the weights sum to one and the shared question mark alone
// is the score — exactly the minimum.
func TestScoredCandidatesForCue_keepsAnAnchorScoreExactlyAtTheMinimum(t *testing.T) {
	t.Parallel()
	refs := []strongRef{{startMs: 1000, origIdx: 0}, {startMs: 5000, origIdx: 1}}
	incAnchors := []anchor{{
		Numbers:     []string{"1"},
		ProperNouns: []string{"Aa"},
		Cognates:    []string{"aaaa"},
		Punctuation: "?",
	}}
	refAnchors := []anchor{{
		Numbers:     []string{"2"},
		ProperNouns: []string{"Bb"},
		Cognates:    []string{"bbbb"},
		Punctuation: "?",
	}, {}}

	got := scoredCandidatesForCue(0, 0, refs, incAnchors, refAnchors, numberMatchWindow, 7)

	if len(got) != 1 {
		t.Fatalf("scoredCandidatesForCue() returned %d candidates, want 1 (%+v)", len(got), got)
	}
	const wantScore = 0.095
	if math.Abs(got[0].score-wantScore) > scoreTol {
		t.Errorf("scoredCandidatesForCue() score = %v, want %v", got[0].score, wantScore)
	}
}

// TestScoredCandidatesForCue_reachesAReferenceAtTheEdgeOfTheScan pins that the
// scanned range is inclusive of its far edge. This window accepts one offset
// only, which the width search cannot narrow below its one-second cushion, so
// the reference cue sits exactly on the last scanned millisecond and must still
// be scored.
func TestScoredCandidatesForCue_reachesAReferenceAtTheEdgeOfTheScan(t *testing.T) {
	t.Parallel()
	refs := []strongRef{{startMs: 1000, origIdx: 0}}
	incAnchors := []anchor{{Numbers: []string{"100"}}}
	refAnchors := []anchor{{Numbers: []string{"100"}}}
	window := func(incStartMs, refStartMs int64) (bool, float64) {
		return refStartMs-incStartMs == 1000, 0.5
	}

	got := scoredCandidatesForCue(0, 0, refs, incAnchors, refAnchors, window, 7)

	if len(got) != 1 {
		t.Fatalf("scoredCandidatesForCue() returned %d candidates, want 1 (%+v)", len(got), got)
	}
	if got[0].refIdx != 0 {
		t.Errorf("scoredCandidatesForCue() refIdx = %d, want 0", got[0].refIdx)
	}
}

// TestDPAlign_ordersByIncorrectCueIndexBeforeReferenceIndex pins the canonical
// order the dynamic program fills in. These two candidates cannot chain (the
// earlier incorrect cue points at the later reference cue) and carry equal
// scores, so the path holds exactly one of them; ordering by incorrect-cue index
// makes that the first incorrect cue. Ordering by reference index instead would
// return the other one.
func TestDPAlign_ordersByIncorrectCueIndexBeforeReferenceIndex(t *testing.T) {
	t.Parallel()
	pairs := []CuePair{
		{IncIdx: 0, RefIdx: 5, Score: 1.0},
		{IncIdx: 1, RefIdx: 0, Score: 1.0},
	}
	got := dpAlign(pairs)
	if len(got) != 1 {
		t.Fatalf("dpAlign() len = %d, want 1 (path %+v)", len(got), got)
	}
	if got[0].IncIdx != 0 || got[0].RefIdx != 5 {
		t.Errorf("dpAlign() = (Inc %d, Ref %d), want (0, 5)", got[0].IncIdx, got[0].RefIdx)
	}
}

// agreeingPairs builds n pairs that all carry the same offset and an equal
// weight, so the weight ratio a confidence is derived from is just the share of
// pairs that agree.
func agreeingPairs(n int, offsetMs int64) []CuePair {
	pairs := make([]CuePair, n)
	for i := range pairs {
		pairs[i] = CuePair{IncIdx: i, RefIdx: i, Score: 0.5, OffsetMs: offsetMs}
	}
	return pairs
}

// TestComputeConfidence pins the confidence scale. Confidence is the weighted
// share of pairs whose offset agrees with the median, times a base factor,
// halved when fewer than five pairs agree and given a capped bonus once twenty
// do. The reference values are recorded rather than described because the
// downstream vote compares them numerically against the other strategies.
func TestComputeConfidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		desc   string
		pairs  []CuePair
		median int64
		want   float64
	}{
		{
			name:   "three_agreeing_pairs_are_halved",
			desc:   "too few agreeing pairs to trust: the base factor is halved",
			pairs:  agreeingPairs(3, 100),
			median: 100,
			want:   0.44,
		},
		{
			name:   "five_agreeing_pairs_are_not_halved",
			desc:   "five is the first count that escapes the penalty",
			pairs:  agreeingPairs(5, 100),
			median: 100,
			want:   0.88,
		},
		{
			name:   "nineteen_agreeing_pairs_earn_no_bonus",
			desc:   "the bonus needs twenty",
			pairs:  agreeingPairs(19, 0),
			median: 0,
			want:   0.88,
		},
		{
			name:   "twenty_agreeing_pairs_earn_the_capped_bonus",
			desc:   "the bonus takes the base above the cap, so the cap is what lands",
			pairs:  agreeingPairs(20, 0),
			median: 0,
			want:   0.92,
		},
		{
			name: "an_offset_exactly_at_the_agreement_limit_agrees",
			desc: "1500ms from the median is inside the agreement window",
			pairs: []CuePair{
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 1600},
			},
			median: 100,
			want:   0.88,
		},
		{
			name: "an_offset_one_past_the_agreement_limit_disagrees",
			desc: "1501ms is outside, dropping the count to four and re-arming the penalty",
			pairs: []CuePair{
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 1601},
			},
			median: 100,
			want:   0.352,
		},
		{
			name: "one_disagreeing_pair_of_six_lowers_the_ratio",
			desc: "five sixths of the weight agrees",
			pairs: []CuePair{
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 100},
				{Score: 0.5, OffsetMs: 9000},
			},
			median: 100,
			want:   0.7333333333333333,
		},
		{
			name: "distance_to_the_median_is_signed_not_summed",
			desc: "an offset the negative of the median is far from it, not on top of it",
			pairs: []CuePair{
				{Score: 0.5, OffsetMs: 800},
				{Score: 0.5, OffsetMs: 800},
				{Score: 0.5, OffsetMs: 800},
				{Score: 0.5, OffsetMs: 800},
				{Score: 0.5, OffsetMs: 800},
				{Score: 0.5, OffsetMs: -800},
			},
			median: 800,
			want:   0.7333333333333333,
		},
		{
			name:   "no_weight_at_all",
			desc:   "nothing to take a share of",
			pairs:  []CuePair{{Score: 0, OffsetMs: 5}},
			median: 5,
			want:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeConfidence(tt.pairs, tt.median)
			if math.Abs(got-tt.want) > scoreTol {
				t.Errorf("computeConfidence(%d pairs, median %d) = %v, want %v (%s)",
					len(tt.pairs), tt.median, got, tt.want, tt.desc)
			}
		})
	}
}

// cueAt builds one cue from millisecond bounds.
func cueAt(startMs, endMs int64, text string) Cue {
	return Cue{
		Start: time.Duration(startMs) * time.Millisecond,
		End:   time.Duration(endMs) * time.Millisecond,
		Text:  text,
	}
}

// anchoredTriplePair builds a ten-second cross-language pair in which exactly
// three incorrect cues are anchored — the fewest Align will work with. The
// reference cues carrying those anchors sit at 1s, 2s and 3s, the incorrect ones
// displacementMs earlier, and each pair shares a distinct number so it can only
// match its own partner. The remaining cues on both sides are unanchored filler
// whose only job is to hold both tracks' durations at exactly ten seconds, which
// is what makes a displacement a known fraction of the timeline.
func anchoredTriplePair(displacementMs int64) (reference, incorrect []Cue) {
	reference = []Cue{
		cueAt(1000, 1900, "It is 100."),
		cueAt(2000, 2900, "It is 101."),
		cueAt(3000, 3900, "It is 102."),
		cueAt(5000, 5900, "oh no"),
		cueAt(7000, 7900, "oh no"),
		cueAt(9100, 10000, "oh no"),
	}
	incorrect = []Cue{
		cueAt(1000-displacementMs, 1900-displacementMs, "Ah 100."),
		cueAt(2000-displacementMs, 2900-displacementMs, "Ah 101."),
		cueAt(3000-displacementMs, 3900-displacementMs, "Ah 102."),
		cueAt(4000, 4900, "oh non"),
		cueAt(6000, 6900, "oh non"),
		cueAt(9100, 10000, "oh non"),
	}
	return reference, incorrect
}

// TestAlign_alignsTheSmallestUsableAnchorSet pins that three anchored cues,
// three candidate pairs and a three-pair alignment path are each enough: they
// are the floor Align accepts, not one short of it. Three agreeing pairs is
// below the count that escapes the confidence penalty, so the halved value is
// the expectation.
func TestAlign_alignsTheSmallestUsableAnchorSet(t *testing.T) {
	t.Parallel()
	ref, inc := anchoredTriplePair(100)

	got := Align(t.Context(), ref, inc)

	if got.Offset != 100 {
		t.Errorf("Align offset = %d ms, want 100", got.Offset)
	}
	const wantConfidence = 0.44
	if math.Abs(got.Confidence-wantConfidence) > scoreTol {
		t.Errorf("Align confidence = %v, want %v", got.Confidence, wantConfidence)
	}
}

// TestAlign_admitsAMatchExactlyAtTheNormalizedWindowEdge pins the first pass's
// admission boundary. Every anchored pair here is displaced by exactly a tenth
// of the ten-second timeline, which is exactly the normalized window, so all
// three are admitted and the track aligns. Treating the edge as outside the
// window would leave two candidates — below the floor — and decline.
func TestAlign_admitsAMatchExactlyAtTheNormalizedWindowEdge(t *testing.T) {
	t.Parallel()
	ref, inc := anchoredTriplePair(1000)

	got := Align(t.Context(), ref, inc)

	if got.Offset != 1000 {
		t.Errorf("Align offset = %d ms, want 1000", got.Offset)
	}
	const wantConfidence = 0.44
	if math.Abs(got.Confidence-wantConfidence) > scoreTol {
		t.Errorf("Align confidence = %v, want %v", got.Confidence, wantConfidence)
	}
}

// TestAlign_alignsAtExactlyTheMinimumCueCount pins that MinCuesForSync cues on
// each side is enough to align, not one short. Five agreeing pairs is also the
// first count that escapes the confidence penalty.
func TestAlign_alignsAtExactlyTheMinimumCueCount(t *testing.T) {
	t.Parallel()
	ref := makeNumberedCues(MinCuesForSync, 0)
	inc := makeNumberedCues(MinCuesForSync, 500*time.Millisecond)

	got := Align(t.Context(), ref, inc)

	if got.Offset != -500 {
		t.Errorf("Align offset = %d ms, want -500", got.Offset)
	}
	const wantConfidence = 0.88
	if math.Abs(got.Confidence-wantConfidence) > scoreTol {
		t.Errorf("Align confidence = %v, want %v", got.Confidence, wantConfidence)
	}
}

// strayMatchPair builds a nearly four-minute cross-language pair displaced by a
// constant 7s, with two deliberate imperfections. The sixth reference cue is a
// further 3s late, so its pair disagrees with the median offset while staying
// inside the second pass's window — that is what makes the confidence depend on
// the candidate SCORES rather than collapsing to the base factor. A trailing
// pair shares a number across a 19s offset: far enough out that only the second
// pass can reject it, and near enough that the first pass, whose window is a
// tenth of the long timeline, still admits it.
func strayMatchPair() (reference, incorrect []Cue) {
	for i := range 10 {
		refStart := int64(20000 + i*20000)
		if i == 5 {
			refStart += 3000
		}
		reference = append(reference, cueAt(refStart, refStart+3000, fmt.Sprintf("It is %d.", 100+i)))
		incStart := int64(20000+i*20000) - 7000
		incorrect = append(incorrect, cueAt(incStart, incStart+3000, fmt.Sprintf("Ah %d.", 100+i)))
	}
	incorrect = append(incorrect, cueAt(213000, 216000, "Ah 999."))
	reference = append(reference, cueAt(232000, 235000, "It is 999."))
	return reference, incorrect
}

// TestAlign_secondPassDropsAStrayMatch pins the whole two-pass narrowing as one
// number. The stray pair survives the first pass and is rejected by the second,
// so the alignment path is the ten real pairs; nine of them agree with the
// recovered offset and one does not, which makes the confidence the agreeing
// share of the candidate weight. Both the position blend that produces those
// weights and the second pass that chose the pairs are inside this value.
func TestAlign_secondPassDropsAStrayMatch(t *testing.T) {
	t.Parallel()
	ref, inc := strayMatchPair()

	got := Align(t.Context(), ref, inc)

	if got.Offset != 7000 {
		t.Errorf("Align offset = %d ms, want 7000", got.Offset)
	}
	const wantConfidence = 0.7953138075313809
	if math.Abs(got.Confidence-wantConfidence) > scoreTol {
		t.Errorf("Align confidence = %v, want %v", got.Confidence, wantConfidence)
	}
}

// distrustedSecondPassPair builds a 200-second cross-language pair in which the
// second pass is DISTRUSTED, so the first pass's own candidate set — and its own
// position scores — are what the alignment is built from. Every number appears
// four times in the reference track: once 1s after its incorrect cue, and three
// more times 12-15s away. All four are inside the first pass's window, which is a
// tenth of the long timeline, and only the near one survives the second pass's
// eight-second window; discarding three quarters of the candidates is more than
// the pipeline will accept from its own narrowing, so it falls back. The third
// number's near copy is a further 3s late, which puts one pair of the alignment
// path outside the agreement window and makes the confidence a genuine weighted
// share rather than a flat base factor.
func distrustedSecondPassPair() (reference, incorrect []Cue) {
	for i := range 6 {
		incStart := int64(20000 + i*20000)
		nearRef := incStart + 1000
		if i == 2 {
			nearRef = incStart + 4000
		}
		text := fmt.Sprintf("It is %d.", 100+i)
		reference = append(reference,
			cueAt(nearRef, nearRef+3000, text),
			cueAt(nearRef+12000, nearRef+15000, text),
			cueAt(nearRef-12000, nearRef-9000, text),
			cueAt(nearRef-15000, nearRef-12000, text),
		)
		incorrect = append(incorrect, cueAt(incStart, incStart+3000, fmt.Sprintf("Ah %d.", 100+i)))
	}
	// Unanchored trailing cues that hold both timelines at exactly 200 seconds,
	// so a displacement is the same fraction of both.
	reference = append(reference, cueAt(197000, 200000, "oh no"))
	incorrect = append(incorrect, cueAt(197000, 200000, "oh non"))
	return reference, incorrect
}

// TestAlign_scoresWithTheFirstPassWhenTheSecondPassIsDistrusted pins the first
// pass's own position scaling. Its normalized distance is expressed as a
// fraction of its window before being blended into a candidate's score; leaving
// it unscaled would compress every position term to near zero and flatten the
// distinction between a near match and one twelve seconds away. That distinction
// is only observable when the first pass's candidate set is the one carried
// forward, which is what this fixture arranges.
func TestAlign_scoresWithTheFirstPassWhenTheSecondPassIsDistrusted(t *testing.T) {
	t.Parallel()
	ref, inc := distrustedSecondPassPair()

	got := Align(t.Context(), ref, inc)

	if got.Offset != 1000 {
		t.Errorf("Align offset = %d ms, want 1000", got.Offset)
	}
	const wantConfidence = 0.7353874883286647
	if math.Abs(got.Confidence-wantConfidence) > scoreTol {
		t.Errorf("Align confidence = %v, want %v", got.Confidence, wantConfidence)
	}
}

// spreadOffsetPair builds a nine-cue cross-language pair whose anchored matches
// fall into three equal groups: the middle three sit at baseOffsetMs and the
// outer three each sit spreadMs before and after it. Every cue carries a
// distinct number, so the first pass yields exactly nine candidates and the
// second pass keeps only the groups within its window of the median offset.
func spreadOffsetPair(baseOffsetMs, spreadMs int64) (reference, incorrect []Cue) {
	for i := range 9 {
		incStart := int64(20000 + i*20000)
		offset := baseOffsetMs
		switch {
		case i < 3:
			offset = baseOffsetMs - spreadMs
		case i >= 6:
			offset = baseOffsetMs + spreadMs
		}
		incorrect = append(incorrect, cueAt(incStart, incStart+3000, fmt.Sprintf("Ah %d.", 100+i)))
		refStart := incStart + offset
		reference = append(reference, cueAt(refStart, refStart+3000, fmt.Sprintf("It is %d.", 100+i)))
	}
	return reference, incorrect
}

// TestAlign_keepsTheSecondPassAtExactlyOneThirdOfTheFirst pins when the second
// pass is trusted. Its narrowing is discarded as over-aggressive only when it
// retains FEWER than a third of the first pass's candidates; retaining exactly a
// third — three of nine here, the outer groups being 9s from the median — is
// still trusted, so the recovered offset is the middle group's. Discarding it
// would realign against all nine scattered candidates and decline instead.
func TestAlign_keepsTheSecondPassAtExactlyOneThirdOfTheFirst(t *testing.T) {
	t.Parallel()
	ref, inc := spreadOffsetPair(4000, 9000)

	got := Align(t.Context(), ref, inc)

	if got.Offset != 4000 {
		t.Errorf("Align offset = %d ms, want 4000", got.Offset)
	}
	const wantConfidence = 0.44
	if math.Abs(got.Confidence-wantConfidence) > scoreTol {
		t.Errorf("Align confidence = %v, want %v", got.Confidence, wantConfidence)
	}
}

// TestAlign_secondPassAdmitsAMatchExactlyAtItsWindowEdge pins the second pass's
// own boundary. The outer groups sit exactly one window width from the median
// offset, so the second pass admits all nine candidates; the resulting spread is
// too scattered to agree on an offset and Align declines, returning the input
// untouched. Treating the edge as outside the window would instead narrow to the
// middle three and report a confident 4s offset — a correction invented from a
// third of the evidence.
func TestAlign_secondPassAdmitsAMatchExactlyAtItsWindowEdge(t *testing.T) {
	t.Parallel()
	ref, inc := spreadOffsetPair(4000, defaultConfig.Pass2WindowMs)

	got := Align(t.Context(), ref, inc)

	if got.Confidence != 0 {
		t.Errorf("Align confidence = %v, want 0 (too scattered to align)", got.Confidence)
	}
	if got.Offset != 0 {
		t.Errorf("Align offset = %d ms, want 0", got.Offset)
	}
	if len(got.Cues) != len(inc) {
		t.Errorf("Align returned %d cues, want %d (input unchanged)", len(got.Cues), len(inc))
	}
}

// TestAlign_rejectsADegenerateDurationAtTheDurationGuard pins that a track whose
// last cue ends at or before zero is refused by the duration guard, named in the
// debug line, rather than falling through to a division by that duration and
// being refused later for having found no candidates. The two refusals are
// indistinguishable in the returned value, so the log line is the observable.
//
// No t.Parallel: this swaps the process-wide default logger.
func TestAlign_rejectsADegenerateDurationAtTheDurationGuard(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tests := []struct {
		name string
		ref  []Cue
		inc  []Cue
	}{
		{"reference_ends_at_zero", numberedCuesEndingAt(0), numberedCuesEndingAt(8000)},
		{"incorrect_ends_at_zero", numberedCuesEndingAt(8000), numberedCuesEndingAt(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			got := Align(t.Context(), tt.ref, tt.inc)
			if got.Confidence != 0 {
				t.Errorf("Align confidence = %v, want 0", got.Confidence)
			}
			const want = "crosslang: zero or negative duration"
			if logged := buf.String(); !strings.Contains(logged, want) {
				t.Errorf("Align logged %q, want it to contain %q", logged, want)
			}
		})
	}
}

// numberedCuesEndingAt builds MinCuesForSync anchored cues that all end at
// endMs, so the track's duration — the last cue's end — is exactly endMs.
func numberedCuesEndingAt(endMs int64) []Cue {
	cues := make([]Cue, MinCuesForSync)
	for i := range cues {
		cues[i] = cueAt(endMs-2000, endMs, fmt.Sprintf("It is %d.", 100+i))
	}
	return cues
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
			got := Align(t.Context(), tt.ref, tt.inc)
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
