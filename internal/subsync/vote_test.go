package subsync

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

// --- Candidate fixtures ---

// shiftCandidate builds a valid pure-shift candidate over inc.
func shiftCandidate(inc []Cue, shiftMs int64, source CandidateSource, method SyncMethod, conf Confidence) SyncResult {
	return SyncResult{
		Cues:       ShiftCues(inc, time.Duration(shiftMs)*time.Millisecond),
		Offset:     shiftMs,
		Rate:       1.0,
		Confidence: conf,
		Method:     method,
		Source:     source,
		Transform:  Transform{Kind: TransformShift, Shift: shiftMs},
	}
}

// framerateCandidate builds a valid framerate candidate over inc.
func framerateCandidate(inc []Cue, ratio float64, conf Confidence) SyncResult {
	return SyncResult{
		Cues:       scaleCues(inc, ratio),
		Rate:       ratio,
		Confidence: conf,
		Method:     MethodFramerate,
		Source:     SourceFramerate,
		Transform:  Transform{Kind: TransformFramerate, Ratio: ratio},
	}
}

// splitCandidate builds a valid two-segment split candidate over inc with a
// headline Offset of 0 (per-segment shifts are applied directly to cues).
func splitCandidate(inc []Cue, boundary int, shift1, shift2 time.Duration, conf Confidence) SyncResult {
	segs := []segment{
		{startIdx: 0, endIdx: boundary, offset: shift1},
		{startIdx: boundary, endIdx: len(inc), offset: shift2},
	}
	return SyncResult{
		Cues:       alignSegments(inc, segs),
		Confidence: conf,
		Method:     MethodSplit,
		Source:     SourceSplit,
		Transform:  Transform{Kind: TransformSegments, Segments: transformSegments(segs)},
	}
}

// --- Winner selection ---

func TestVoteOnCandidates(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second) // correct correction: shift by -2000ms
	incSame := makeLongCues(30, 10*time.Minute)
	incFar := ShiftCues(ref, 31*time.Second) // correct correction beyond LargeOffsetMs
	tests := []struct {
		check      func(t *testing.T, got SyncResult)
		name       string
		ref        []Cue
		inc        []Cue
		candidates []SyncResult
	}{
		{
			name: "single candidate passthrough",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				shiftCandidate(inc, -2000, SourceOffset, MethodOffset, 0.7),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Method != MethodOffset {
					t.Errorf("method = %q, want %q", got.Method, MethodOffset)
				}
				if got.Offset != -2000 {
					t.Errorf("offset = %d, want -2000", got.Offset)
				}
			},
		},
		{
			name: "agreeing cluster outranks lone higher-confidence candidate",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				shiftCandidate(inc, -2000, SourceCrosslang, MethodCrosslang, 0.6),
				shiftCandidate(inc, -2100, SourceOffset, MethodOffset, 0.5),
				// Disagrees with everyone; highest calibrated confidence.
				shiftCandidate(inc, -20000, SourceSplit, MethodSplit, 0.9),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Source != SourceCrosslang && got.Source != SourceOffset {
					t.Errorf("winner source = %v, want member of the agreeing shift cluster", got.Source)
				}
			},
		},
		{
			name: "rating selects best member within the winning cluster",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				// Both cluster (corrected cues agree within 100ms); -2000 is
				// exact, so it out-rates -2100 despite the lower calibrated
				// confidence.
				shiftCandidate(inc, -2100, SourceCrosslang, MethodCrosslang, 0.9),
				shiftCandidate(inc, -2000, SourceOffset, MethodOffset, 0.5),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Offset != -2000 {
					t.Errorf("offset = %d, want -2000 (rating selects, not confidence)", got.Offset)
				}
				if got.Confidence != 0.5 {
					t.Errorf("confidence = %f, want the winner's calibrated 0.5", float64(got.Confidence))
				}
			},
		},
		{
			name: "rating decides between equal-size clusters",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				shiftCandidate(inc, -8000, SourceCrosslang, MethodCrosslang, 0.9),
				shiftCandidate(inc, -2000, SourceOffset, MethodOffset, 0.5),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Offset != -2000 {
					t.Errorf("offset = %d, want -2000 (higher alignment rating)", got.Offset)
				}
			},
		},
		{
			name: "source order breaks exact rating ties",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				// Identical corrected cues: same rating; canonical source
				// order (crosslang before offset) breaks the tie.
				shiftCandidate(inc, -2000, SourceOffset, MethodOffset, 0.9),
				shiftCandidate(inc, -2000, SourceCrosslang, MethodCrosslang, 0.5),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Source != SourceCrosslang {
					t.Errorf("winner source = %v, want crosslang (canonical tie-break)", got.Source)
				}
				if got.Confidence != 0.5 {
					t.Errorf("confidence = %f, want 0.5 (calibrated, untouched)", float64(got.Confidence))
				}
			},
		},
		{
			name: "larger consensus outranks a plausible singleton",
			ref:  ref,
			inc:  incSame,
			candidates: []SyncResult{
				// Two agreeing implausible shifts form the larger validated
				// cluster; the plausible singleton must not jump the size
				// key (R3.2: size, then rating, then source order).
				shiftCandidate(incSame, 40000, SourceOffset, MethodOffset, 0.7),
				shiftCandidate(incSame, 40800, SourceSplit, MethodSplit, 0.6),
				shiftCandidate(incSame, -1000, SourceCrosslang, MethodCrosslang, 0.4),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Offset == -1000 {
					t.Error("plausible singleton must not outrank the larger validated consensus")
				}
			},
		},
		{
			name: "guard picks the plausible member of a mixed cluster",
			ref:  ref,
			inc:  incFar,
			candidates: []SyncResult{
				// One cluster (corrected cues agree within 1200ms). The
				// exact -31000 correction is beyond LargeOffsetMs on
				// similar-duration content, so the plausibility guard hands
				// the cluster to its plausible member despite the lower
				// rating (the guard's only seat: member selection).
				shiftCandidate(incFar, -31000, SourceOffset, MethodOffset, 0.9),
				shiftCandidate(incFar, -29800, SourceCrosslang, MethodCrosslang, 0.4),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Offset != -29800 {
					t.Errorf("offset = %d, want -29800 (plausibility guard on shift winners)", got.Offset)
				}
				if got.Confidence != 0.4 {
					t.Errorf("confidence = %f, want the calibrated 0.4", float64(got.Confidence))
				}
			},
		},
		{
			name: "all-implausible set still returns a winner",
			ref:  ref,
			inc:  incSame,
			candidates: []SyncResult{
				shiftCandidate(incSame, 40000, SourceOffset, MethodOffset, 0.7),
				shiftCandidate(incSame, 50000, SourceCrosslang, MethodCrosslang, 0.4),
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Method == "" {
					t.Error("returned empty method")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := voteOnCandidates(tt.candidates, tt.ref, tt.inc)
			tt.check(t, got)
		})
	}
}

// --- Clustering ---

func TestClusterCandidates(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(30, 10*time.Minute)
	tests := []struct {
		name         string
		candidates   []SyncResult
		wantMembers  []int
		wantClusters int
	}{
		{
			name: "two shifts within CorrectedCueAgreementMs cluster",
			candidates: []SyncResult{
				// 1400ms apart: every corrected cue start and end agrees
				// within CorrectedCueAgreementMs (1500).
				shiftCandidate(inc, 0, SourceCrosslang, MethodCrosslang, 0.5),
				shiftCandidate(inc, 1400, SourceOffset, MethodOffset, 0.6),
			},
			wantClusters: 1,
			wantMembers:  []int{2},
		},
		{
			name: "shifts beyond CorrectedCueAgreementMs split even within ClusterMs",
			candidates: []SyncResult{
				// 2900ms apart: inside the ClusterMs (3000) pre-grouping
				// prefilter, but the corrected cues disagree (2900 > 1500).
				// The prefilter is an early rejection, never a grant: cluster
				// membership is always the corrected-cue predicate (R2.1).
				shiftCandidate(inc, 0, SourceCrosslang, MethodCrosslang, 0.5),
				shiftCandidate(inc, 2900, SourceOffset, MethodOffset, 0.6),
			},
			wantClusters: 2,
			wantMembers:  []int{1, 1},
		},
		{
			name: "two shifts beyond ClusterMs split",
			candidates: []SyncResult{
				shiftCandidate(inc, 0, SourceCrosslang, MethodCrosslang, 0.5),
				shiftCandidate(inc, 3001, SourceOffset, MethodOffset, 0.6),
			},
			wantClusters: 2,
			wantMembers:  []int{1, 1},
		},
		{
			name: "framerate joins a shift cluster through the corrected-cue predicate",
			candidates: []SyncResult{
				shiftCandidate(inc, 0, SourceOffset, MethodOffset, 0.5),
				// Ratio 1.0 leaves cues identical to shift(0)'s cues.
				framerateCandidate(inc, 1.0, 0.7),
			},
			wantClusters: 1,
			wantMembers:  []int{2},
		},
		{
			name: "framerate and split at headline offset 0 with differing cues do not collude",
			candidates: []SyncResult{
				// Both report Offset 0; their corrected cues differ wildly.
				// The retired scalar clustering keyed on Offset and merged
				// them; corrected-cue clustering must keep them apart.
				framerateCandidate(inc, 0.959, 0.7),
				splitCandidate(inc, 15, 5*time.Second, -5*time.Second, 0.6),
			},
			wantClusters: 2,
			wantMembers:  []int{1, 1},
		},
		{
			name: "complete linkage requires agreement with every member",
			candidates: []SyncResult{
				// a(0) agrees b(1400); b(1400) agrees c(2800); a disagrees c
				// (2800 > CorrectedCueAgreementMs). Single linkage would
				// chain all three; complete linkage keeps c out of {a,b}.
				shiftCandidate(inc, 0, SourceCrosslang, MethodCrosslang, 0.5),
				shiftCandidate(inc, 1400, SourceFramerate, MethodFramerate, 0.5),
				shiftCandidate(inc, 2800, SourceOffset, MethodOffset, 0.5),
			},
			wantClusters: 2,
			wantMembers:  []int{2, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := clusterCandidates(tt.candidates)
			if len(clusters) != tt.wantClusters {
				t.Fatalf("clusters = %d, want %d", len(clusters), tt.wantClusters)
			}
			for i, wm := range tt.wantMembers {
				if len(clusters[i].members) != wm {
					t.Errorf("cluster[%d] members = %d, want %d", i, len(clusters[i].members), wm)
				}
			}
		})
	}
}

func TestClusterCandidates_orderCanonical(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(30, 10*time.Minute)
	// Offsets {0, 1400, 2800}: pairwise corrected-cue agreement is
	// non-transitive (adjacent pairs agree within 1500ms, the outer pair
	// does not), the case where greedy first-fit clustering depended on
	// arrival order.
	a := shiftCandidate(inc, 0, SourceCrosslang, MethodCrosslang, 0.5)
	b := shiftCandidate(inc, 1400, SourceOffset, MethodOffset, 0.6)
	c := shiftCandidate(inc, 2800, SourceSplit, MethodSplit, 0.7)

	base := clusterCandidates([]SyncResult{a, b, c})
	perms := [][]SyncResult{
		{a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a},
	}
	for _, p := range perms {
		got := clusterCandidates(p)
		if len(got) != len(base) {
			t.Fatalf("cluster count varies with input order: %d vs %d", len(got), len(base))
		}
		for i := range got {
			if len(got[i].members) != len(base[i].members) {
				t.Fatalf("cluster[%d] size varies with input order: %d vs %d",
					i, len(got[i].members), len(base[i].members))
			}
			for j := range got[i].members {
				if got[i].members[j].Source != base[i].members[j].Source {
					t.Fatalf("cluster[%d].members[%d] source varies with input order", i, j)
				}
			}
		}
	}
}

// --- Regression fixtures for the two named arbitration defects ---

// makeVariedCues creates n cues with strictly distinct durations so that
// per-cue best-offset measurement (which scores by span-length ratio) is
// position-accurate — the shape real subtitles have. With uniform-length
// cues (makeLongCues) the split generator's per-cue offsets are degenerate
// and it detects spurious splits instead of taking the no-split branch.
func makeVariedCues(n int, gap time.Duration) []Cue {
	cues := make([]Cue, n)
	for i := range n {
		start := time.Duration(i) * gap
		dur := 500*time.Millisecond + time.Duration(i)*37*time.Millisecond
		cues[i] = Cue{Start: start, End: start + dur, Text: "cue"}
	}
	return cues
}

// TestArbitration_regression_noSplitCandidate pins defect 1: the split
// generator's no-split branch used to re-emit the constant-offset hypothesis
// as a second candidate with a flat moderate confidence, granting it a
// structural self-agreement bonus. The generator must now emit NO candidate
// (zero confidence) so the hypothesis appears at most once in arbitration.
func TestArbitration_regression_noSplitCandidate(t *testing.T) {
	t.Parallel()
	ref := makeVariedCues(40, 15*time.Second)
	inc := ShiftCues(ref, 2*time.Second)

	// Generator level: a pure constant offset has no split point, so the
	// split generator emits nothing.
	got := alignWithSplits(t.Context(), ref, inc, 0)
	if got.Confidence != ConfidenceNone {
		t.Fatalf("alignWithSplits(no-split input) confidence = %f, want 0 (no candidate)",
			float64(got.Confidence))
	}

	// Arbitration level: with splits enabled the winner comes from another
	// generator; the constant-offset hypothesis is represented exactly once.
	opts := SyncOptions{EnableSplits: true, MinConfidence: 0.5}
	winner := referenceSync(t.Context(), ref, inc, &opts)
	if winner.Source == SourceSplit {
		t.Errorf("winner source = split, want the split generator absent on no-split content")
	}
	if abs64(winner.Offset-(-2000)) > 100 {
		t.Errorf("winner offset = %d, want ~-2000", winner.Offset)
	}
}

// TestArbitration_regression_scalarZeroCollusion pins defect 2: scalar
// clustering keyed on SyncResult.Offset treated a framerate transform
// (headline 0, Rate set) and a multi-segment split (headline 0 by
// convention) as agreeing even though they correct cues differently.
func TestArbitration_regression_scalarZeroCollusion(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(30, 10*time.Minute)
	fr := framerateCandidate(inc, 0.959, 0.7)
	sp := splitCandidate(inc, 15, 5*time.Second, -5*time.Second, 0.6)
	if fr.Offset != 0 || sp.Offset != 0 {
		t.Fatalf("fixture: headline offsets = %d, %d, want 0, 0", fr.Offset, sp.Offset)
	}

	clusters := clusterCandidates([]SyncResult{fr, sp})
	if len(clusters) != 2 {
		t.Fatalf("clusters = %d, want 2 (no false consensus at offset 0)", len(clusters))
	}
}

// --- Alignment rating ---

func TestAlignmentRating(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	tests := []struct {
		name      string
		reference []Cue
		corrected []Cue
		wantMin   float64
		wantMax   float64
	}{
		{"identical cues rate 1.0", ref, ref, 1.0, 1.0},
		{"disjoint cues rate 0", ref, ShiftCues(ref, 5*time.Second), 0, 0},
		{"half-overlapped cues rate 0.5", ref, ShiftCues(ref, 500*time.Millisecond), 0.5, 0.5},
		{"empty reference rates 0", nil, ref, 0, 0},
		{"empty corrected rates 0", ref, nil, 0, 0},
		{
			// A reference cue fully covered by several corrected cues is
			// fully overlapped: the rating accumulates the union of ALL
			// corrected spans per reference span, not just the first.
			"reference cue covered by two adjacent corrected cues rates 1.0",
			[]Cue{{Start: 0, End: 10 * time.Second, Text: "ref"}},
			[]Cue{
				{Start: 0, End: 5 * time.Second, Text: "a"},
				{Start: 5 * time.Second, End: 10 * time.Second, Text: "b"},
			},
			1.0, 1.0,
		},
		{
			// Corrected cues overlapping each other cover (0s,6s)+(4s,8s):
			// the union is 8s of the 10s reference, never 10s (the 2s
			// double-covered stretch counts once).
			"overlapping corrected cues are not double-counted",
			[]Cue{{Start: 0, End: 10 * time.Second, Text: "ref"}},
			[]Cue{
				{Start: 0, End: 6 * time.Second, Text: "a"},
				{Start: 4 * time.Second, End: 8 * time.Second, Text: "b"},
			},
			0.8, 0.8,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := alignmentRating(tt.reference, tt.corrected)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("alignmentRating = %f, want in [%f, %f]", got, tt.wantMin, tt.wantMax)
			}
			if got < 0 || got > 1 {
				t.Errorf("alignmentRating = %f, out of [0, 1]", got)
			}
		})
	}
}

func TestAlignmentRating_ordersByCloseness(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	exact := alignmentRating(ref, ref)
	close := alignmentRating(ref, ShiftCues(ref, 200*time.Millisecond))
	far := alignmentRating(ref, ShiftCues(ref, 800*time.Millisecond))
	if !(exact > close && close > far) {
		t.Errorf("rating not ordered by closeness: exact=%f close=%f far=%f", exact, close, far)
	}
}

// TestAlignmentRating_noHiddenOffsetFitting pins the contract that the
// rating scores cues exactly as corrected: a candidate whose cues are still
// misaligned rates low even though a latent constant offset would fix it.
func TestAlignmentRating_noHiddenOffsetFitting(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	misaligned := ShiftCues(ref, 3*time.Second) // a fitted offset would recover 1.0
	if got := alignmentRating(ref, misaligned); got != 0 {
		t.Errorf("alignmentRating(misaligned) = %f, want 0 (no hidden offset fitting)", got)
	}
}

// --- Validity guard ---

func TestIsValidCandidate(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(10, 5*time.Minute)
	nonMonotonic := make([]Cue, len(inc))
	copy(nonMonotonic, inc)
	nonMonotonic[4], nonMonotonic[5] = nonMonotonic[5], nonMonotonic[4]
	tests := []struct {
		name string
		c    SyncResult
		want bool
	}{
		{"valid shifted cues", shiftCandidate(inc, -2000, SourceOffset, MethodOffset, 0.5), true},
		{"nil cues", SyncResult{Method: MethodOffset, Confidence: 0.5}, false},
		{"wrong length", SyncResult{Cues: inc[:5], Method: MethodOffset, Confidence: 0.5}, false},
		{"non-monotonic starts", SyncResult{Cues: nonMonotonic, Method: MethodOffset, Confidence: 0.5}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidCandidate(&tt.c, len(inc)); got != tt.want {
				t.Errorf("isValidCandidate = %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("equal starts are monotonic", func(t *testing.T) {
		t.Parallel()
		clamped := SyncResult{Cues: ShiftCues(inc, -time.Hour)} // clamps leading starts to 0
		if !isValidCandidate(&clamped, len(inc)) {
			t.Error("zero-clamped shift (equal starts) should be valid")
		}
	})
}

func TestFilterValidCandidates(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(10, 5*time.Minute)
	good := shiftCandidate(inc, -1000, SourceOffset, MethodOffset, 0.6)
	bad := SyncResult{Cues: inc[:3], Method: MethodSplit, Source: SourceSplit, Confidence: 0.7}
	got := filterValidCandidates([]SyncResult{good, bad}, inc)
	if len(got) != 1 {
		t.Fatalf("filterValidCandidates kept %d candidates, want 1", len(got))
	}
	if got[0].Source != SourceOffset {
		t.Errorf("kept candidate source = %v, want offset", got[0].Source)
	}
}

// --- Plausibility guard ---

func TestPlausibleCandidate(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(10, 5*time.Minute)
	tests := []struct {
		name            string
		c               SyncResult
		similarDuration bool
		want            bool
	}{
		{"small shift similar duration", shiftCandidate(inc, 5000, SourceOffset, MethodOffset, 0.5), true, true},
		{"large shift similar duration", shiftCandidate(inc, 40000, SourceOffset, MethodOffset, 0.5), true, false},
		{"large shift different duration", shiftCandidate(inc, 40000, SourceOffset, MethodOffset, 0.5), false, true},
		{"boundary shift exactly LargeOffsetMs", shiftCandidate(inc, 30000, SourceOffset, MethodOffset, 0.5), true, true},
		{"framerate transform never implausible", framerateCandidate(inc, 0.959, 0.5), true, true},
		{"segments transform never implausible", splitCandidate(inc, 5, 40*time.Second, 50*time.Second, 0.5), true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := plausibleCandidate(&tt.c, tt.similarDuration); got != tt.want {
				t.Errorf("plausibleCandidate = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Winner ladder (pickWinner) ---

func TestPickWinner(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	incFar := ShiftCues(ref, 31*time.Second)
	exact := shiftCandidate(inc, -2000, SourceOffset, MethodOffset, 0.5)
	near := shiftCandidate(inc, -2100, SourceCrosslang, MethodCrosslang, 0.9)
	lone := shiftCandidate(inc, -9000, SourceSplit, MethodSplit, 0.95)
	implausible := shiftCandidate(inc, 45000, SourceOffset, MethodOffset, 0.9)
	implausible2 := shiftCandidate(inc, 45500, SourceSplit, MethodSplit, 0.9)
	plausibleWeak := shiftCandidate(inc, -2000, SourceCrosslang, MethodCrosslang, 0.3)
	// Over incFar the exact correction (-31000) is itself beyond
	// LargeOffsetMs; -29800 is plausible but rates 0 (1.2s off 1s cues).
	exactFar := shiftCandidate(incFar, -31000, SourceOffset, MethodOffset, 0.9)
	nearFar := shiftCandidate(incFar, -29800, SourceCrosslang, MethodCrosslang, 0.3)

	tests := []struct {
		name            string
		clusters        []voteCluster
		wantSource      CandidateSource
		wantConfidence  Confidence
		similarDuration bool
	}{
		{
			name: "larger cluster wins over higher-rated singleton",
			clusters: []voteCluster{
				{members: []SyncResult{near, exact}},
				{members: []SyncResult{lone}},
			},
			wantSource:     SourceOffset, // exact out-rates near within the winning cluster
			wantConfidence: 0.5,
		},
		{
			name: "rating decides equal-size clusters",
			clusters: []voteCluster{
				{members: []SyncResult{lone}},
				{members: []SyncResult{exact}},
			},
			wantSource:     SourceOffset,
			wantConfidence: 0.5,
		},
		{
			name: "larger implausible consensus outranks a plausible singleton",
			clusters: []voteCluster{
				{members: []SyncResult{implausible, implausible2}},
				{members: []SyncResult{plausibleWeak}},
			},
			// R3.2: validated-cluster size is the FIRST cross-cluster key;
			// the plausibility prior never jumps it. Within the winning
			// cluster both members rate 0, so the earliest member in
			// cluster order wins the tie.
			wantSource:      SourceOffset,
			wantConfidence:  0.9,
			similarDuration: true,
		},
		{
			name: "guard restricts a mixed cluster to its plausible member",
			clusters: []voteCluster{
				{members: []SyncResult{exactFar, nearFar}},
			},
			// exactFar out-rates nearFar (1.0 vs 0) but is beyond
			// LargeOffsetMs on similar-duration content; the guard's only
			// seat is member selection within the cluster.
			wantSource:      SourceCrosslang,
			wantConfidence:  0.3,
			similarDuration: true,
		},
		{
			name: "guard disarmed on different-duration content",
			clusters: []voteCluster{
				{members: []SyncResult{exactFar, nearFar}},
			},
			// Guard off: the exact correction's rating decides.
			wantSource:      SourceOffset,
			wantConfidence:  0.9,
			similarDuration: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pickWinner(tt.clusters, ref, tt.similarDuration)
			if got.Source != tt.wantSource {
				t.Errorf("winner source = %v, want %v", got.Source, tt.wantSource)
			}
			if got.Confidence != tt.wantConfidence {
				t.Errorf("winner confidence = %f, want %f (calibrated member value)",
					float64(got.Confidence), float64(tt.wantConfidence))
			}
		})
	}
}

// --- Two-value contract ---

// TestArbitration_twoValueContract pins the hard invariant that the rating
// selects the winner but is NEVER written into SyncResult.Confidence: the
// value reaching the consumer gates (sync_min_confidence for the reference
// path, ShouldApplyThreshold for the audio fallback) is the winning member's
// raw calibrated confidence.
func TestArbitration_twoValueContract(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)

	// The crosslang candidate wins (exact correction, rating 1.0) with a
	// calibrated confidence chosen to sit between the audio fallback
	// threshold (0.5) and the reference auto-sync gate (0.6).
	calibrated := Confidence(0.55)
	winner := voteOnCandidates([]SyncResult{
		shiftCandidate(inc, -2000, SourceCrosslang, MethodCrosslang, calibrated),
		shiftCandidate(inc, -7000, SourceOffset, MethodOffset, 0.9),
	}, ref, inc)

	if winner.Source != SourceCrosslang {
		t.Fatalf("winner source = %v, want crosslang", winner.Source)
	}
	if winner.Confidence != calibrated {
		t.Fatalf("winner confidence = %f, want calibrated %f (rating must not overwrite)",
			float64(winner.Confidence), float64(calibrated))
	}
	if rating := alignmentRating(ref, winner.Cues); rating != 1.0 {
		t.Fatalf("fixture: winner rating = %f, want 1.0 so the contrast is real", rating)
	}

	// Both consumer gates read the calibrated value untouched.
	if float64(winner.Confidence) >= api.DefaultSyncMinConfidence {
		t.Errorf("reference gate: %f should be below sync_min_confidence %f",
			float64(winner.Confidence), api.DefaultSyncMinConfidence)
	}
	if !winner.ShouldApply() {
		t.Errorf("audio-fallback threshold: %f should pass ShouldApply (>= %f)",
			float64(winner.Confidence), float64(ShouldApplyThreshold))
	}
}

// --- Order-invariance property (R5.3) ---

// TestProperty_winnerIdentity_permutationStable asserts that the FULL winner
// identity (source, transform digest, rating, confidence) is stable under
// any permutation of the candidate slice — true only because clustering
// canonicalizes its input order first.
func TestProperty_winnerIdentity_permutationStable(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		inc := genCues(t, rapid.IntRange(5, 40).Draw(t, "n"), "inc")
		ref := genCues(t, rapid.IntRange(5, 40).Draw(t, "nref"), "ref")

		var candidates []SyncResult
		if rapid.Bool().Draw(t, "crosslang") {
			candidates = append(candidates, shiftCandidate(inc,
				rapid.Int64Range(-60000, 60000).Draw(t, "clShift"),
				SourceCrosslang, MethodCrosslang,
				Confidence(rapid.Float64Range(0.01, 1).Draw(t, "clConf"))))
		}
		if rapid.Bool().Draw(t, "framerate") {
			candidates = append(candidates, framerateCandidate(inc,
				rapid.Float64Range(0.9, 1.1).Draw(t, "frRatio"),
				Confidence(rapid.Float64Range(0.01, 1).Draw(t, "frConf"))))
		}
		candidates = append(candidates, shiftCandidate(inc,
			rapid.Int64Range(-60000, 60000).Draw(t, "offShift"),
			SourceOffset, MethodOffset,
			Confidence(rapid.Float64Range(0.01, 1).Draw(t, "offConf"))))
		if rapid.Bool().Draw(t, "split") {
			a := rapid.Int64Range(0, 5000).Draw(t, "segShiftA")
			b := a + rapid.Int64Range(0, 5000).Draw(t, "segShiftB")
			boundary := rapid.IntRange(1, len(inc)-1).Draw(t, "boundary")
			candidates = append(candidates, splitCandidate(inc, boundary,
				time.Duration(a)*time.Millisecond, time.Duration(b)*time.Millisecond,
				Confidence(rapid.Float64Range(0.01, 1).Draw(t, "spConf"))))
		}

		base := voteOnCandidates(candidates, ref, inc)
		baseRating := alignmentRating(ref, base.Cues)

		perm := rapid.Permutation(candidates).Draw(t, "perm")
		got := voteOnCandidates(perm, ref, inc)

		if got.Source != base.Source {
			t.Fatalf("winner source varies with order: %v vs %v", got.Source, base.Source)
		}
		if got.Transform.Digest() != base.Transform.Digest() {
			t.Fatalf("winner transform varies with order: %s vs %s",
				got.Transform.Digest(), base.Transform.Digest())
		}
		if got.Confidence != base.Confidence {
			t.Fatalf("winner confidence varies with order: %f vs %f",
				float64(got.Confidence), float64(base.Confidence))
		}
		if r := alignmentRating(ref, got.Cues); r != baseRating {
			t.Fatalf("winner rating varies with order: %f vs %f", r, baseRating)
		}
	})
}

// TestProperty_voteOnCandidates keeps the general vote invariants: the
// winner is always one of the inputs, returned verbatim.
func TestProperty_voteOnCandidates(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ref := makeLongCues(30, 10*time.Minute)
		inc := ShiftCues(ref, 2*time.Second)

		sources := []CandidateSource{SourceCrosslang, SourceFramerate, SourceOffset, SourceSplit}
		methods := []SyncMethod{MethodCrosslang, MethodFramerate, MethodOffset, MethodSplit}
		n := rapid.IntRange(1, 4).Draw(t, "numCandidates")

		candidates := make([]SyncResult, n)
		for i := range n {
			candidates[i] = shiftCandidate(inc,
				rapid.Int64Range(-60000, 60000).Draw(t, "offset"),
				sources[i], methods[i],
				Confidence(rapid.Float64Range(0.01, 1.0).Draw(t, "confidence")))
		}

		got := voteOnCandidates(candidates, ref, inc)

		// The winner is one of the input candidates, returned verbatim.
		found := false
		for _, c := range candidates {
			if c.Source == got.Source && c.Offset == got.Offset &&
				c.Confidence == got.Confidence && c.Method == got.Method {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("winner (source=%v offset=%d conf=%f) is not an input candidate",
				got.Source, got.Offset, float64(got.Confidence))
		}

		// Single candidate returns itself.
		if n == 1 && got.Offset != candidates[0].Offset {
			t.Fatalf("single candidate: offset = %d, want %d", got.Offset, candidates[0].Offset)
		}
	})
}
