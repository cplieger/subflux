package subsync

import (
	"math"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestDetectSplits_sorted_ascending verifies that detectSplits always returns
// split indices in ascending order.
func TestDetectSplits_sorted_ascending(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 200).Draw(t, "n")
		offsets := make([]perCueOffset, n)
		for i := range offsets {
			offsets[i] = perCueOffset{offsetMs: rapid.Int64Range(-10000, 10000).Draw(t, "offset")}
		}
		penalty := rapid.Float64Range(100, 5000).Draw(t, "penalty")

		splits := detectSplits(offsets, penalty)
		if len(splits) == 0 {
			return
		}
		// First element must be 0.
		if splits[0] != 0 {
			t.Fatalf("first split = %d, want 0", splits[0])
		}
		// Must be sorted ascending.
		for i := 1; i < len(splits); i++ {
			if splits[i] <= splits[i-1] {
				t.Fatalf("splits not ascending: splits[%d]=%d <= splits[%d]=%d",
					i, splits[i], i-1, splits[i-1])
			}
		}
	})
}

// TestDetectSplits_max_splits verifies that the number of splits never
// exceeds maxSplits+1.
func TestDetectSplits_max_splits(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 500).Draw(t, "n")
		offsets := make([]perCueOffset, n)
		for i := range offsets {
			offsets[i] = perCueOffset{offsetMs: rapid.Int64Range(-50000, 50000).Draw(t, "offset")}
		}
		penalty := rapid.Float64Range(1, 10000).Draw(t, "penalty")

		splits := detectSplits(offsets, penalty)
		if len(splits) > maxSplits+1 {
			t.Fatalf("len(splits) = %d, exceeds maxSplits+1 = %d", len(splits), maxSplits+1)
		}
	})
}

// TestDetectSplits_monotone_penalty verifies that increasing penalty
// monotonically reduces (or maintains) the number of splits.
func TestDetectSplits_monotone_penalty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(5, 100).Draw(t, "n")
		offsets := make([]perCueOffset, n)
		for i := range offsets {
			offsets[i] = perCueOffset{offsetMs: rapid.Int64Range(-10000, 10000).Draw(t, "offset")}
		}
		p1 := rapid.Float64Range(100, 2000).Draw(t, "penalty_low")
		p2 := p1 + rapid.Float64Range(100, 3000).Draw(t, "penalty_delta")

		splits1 := detectSplits(offsets, p1)
		splits2 := detectSplits(offsets, p2)

		if len(splits2) > len(splits1) {
			t.Fatalf("higher penalty produced more splits: penalty %.0f → %d splits, penalty %.0f → %d splits",
				p1, len(splits1), p2, len(splits2))
		}
	})
}

// TestAlignWithSplits_output_length verifies that alignWithSplits always
// returns exactly len(incorrect) cues — it shifts cues but never adds or
// removes them.
func TestAlignWithSplits_output_length(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 100).Draw(t, "n")
		ref := genCues(t, n, "ref")
		inc := genCues(t, n, "inc")

		result := alignWithSplits(t.Context(), ref, inc, 0)
		if len(result.Cues) != len(inc) {
			t.Fatalf("alignWithSplits returned %d cues, want %d", len(result.Cues), len(inc))
		}
	})
}

// TestAlignWithSplits_identity verifies the no-candidate contract on
// identity input: the cue count is preserved, and when the generator emits
// no candidate (zero confidence — the no-split branch, since the
// constant-offset hypothesis belongs to the offset generator) the input
// cues are returned verbatim, never half-corrected.
func TestAlignWithSplits_identity(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(5, 50).Draw(t, "n")
		cues := genCues(t, n, "cues")

		result := alignWithSplits(t.Context(), cues, cues, 0)
		if len(result.Cues) != len(cues) {
			t.Fatalf("identity: returned %d cues, want %d", len(result.Cues), len(cues))
		}
		if result.Confidence == ConfidenceNone {
			for i := range cues {
				if result.Cues[i] != cues[i] {
					t.Fatalf("identity: zero-confidence result altered cue %d: %+v", i, result.Cues[i])
				}
			}
		}
	})
}

// genCues generates a sorted slice of n cues with random but monotonically
// increasing timestamps.
func genCues(t *rapid.T, n int, label string) []Cue {
	cues := make([]Cue, n)
	var pos time.Duration
	for i := range cues {
		gap := time.Duration(rapid.IntRange(100, 5000).Draw(t, label+"_gap")) * time.Millisecond
		dur := time.Duration(rapid.IntRange(500, 3000).Draw(t, label+"_dur")) * time.Millisecond
		pos += gap
		cues[i] = Cue{
			Start: pos,
			End:   pos + dur,
			Text:  "cue",
		}
		pos += dur
	}
	return cues
}

// TestPerCueOffsets verifies that perCueOffsets produces deterministic results
// regardless of parallelism, and that each offset aligns the cue to some
// reference span.
func TestPerCueOffsets(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		nRef := rapid.IntRange(1, 50).Draw(t, "nRef")
		nInc := rapid.IntRange(1, 50).Draw(t, "nInc")
		ref := genCues(t, nRef, "ref")
		inc := genCues(t, nInc, "inc")

		refSpans := cuesToSpans(ref)
		offsets := perCueOffsets(t.Context(), refSpans, inc)

		if len(offsets) != len(inc) {
			t.Fatalf("perCueOffsets returned %d offsets, want %d", len(offsets), len(inc))
		}

		// Each offset must correspond to aligning the cue start to some ref span start.
		for i, o := range offsets {
			incStart := inc[i].Start.Milliseconds()
			found := false
			for _, rs := range refSpans {
				if o.offsetMs == rs.Start-incStart {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("perCueOffsets[%d].offsetMs=%d does not match any ref span alignment", i, o.offsetMs)
			}
		}

		// Determinism: running again should produce the same result.
		offsets2 := perCueOffsets(t.Context(), refSpans, inc)
		for i := range offsets {
			if offsets[i] != offsets2[i] {
				t.Fatalf("perCueOffsets non-deterministic at index %d: %v vs %v", i, offsets[i], offsets2[i])
			}
		}
	})
}

// squaredDeviation returns the total squared deviation of a run of offsets
// about its own mean — the per-segment cost detectSplits is documented to
// minimize. It sums (x - mean)^2 directly rather than reusing the production
// code's algebraically equivalent sum-of-squares rearrangement, so a mistake in
// that rearrangement cannot hide here.
func squaredDeviation(offsets []perCueOffset) float64 {
	if len(offsets) < 2 {
		return 0
	}
	var sum float64
	for _, o := range offsets {
		sum += float64(o.offsetMs)
	}
	mean := sum / float64(len(offsets))
	var total float64
	for _, o := range offsets {
		d := float64(o.offsetMs) - mean
		total += d * d
	}
	return total
}

// segmentationCost scores one segmentation against the documented objective:
// the total squared deviation of every segment, plus one penalty per split
// point beyond the implicit split at 0.
func segmentationCost(offsets []perCueOffset, splits []int, penalty float64) float64 {
	var cost float64
	for i, start := range splits {
		end := len(offsets)
		if i+1 < len(splits) {
			end = splits[i+1]
		}
		cost += squaredDeviation(offsets[start:end])
	}
	return cost + penalty*float64(len(splits)-1)
}

// cheapestSegmentationCost enumerates every way of cutting offsets into
// consecutive runs and returns the lowest cost any of them achieves. Exhaustive
// rather than dynamic, so it shares no structure with the function it checks.
func cheapestSegmentationCost(offsets []perCueOffset, penalty float64) float64 {
	n := len(offsets)
	if n == 0 {
		return 0
	}
	best := math.Inf(1)
	for mask := range 1 << (n - 1) {
		var cost float64
		var splits int
		start := 0
		for i := 1; i <= n; i++ {
			if i < n && mask&(1<<(i-1)) == 0 {
				continue
			}
			cost += squaredDeviation(offsets[start:i])
			if start > 0 {
				splits++
			}
			start = i
		}
		best = min(best, cost+penalty*float64(splits))
	}
	return best
}

// TestDetectSplits_finds_the_cheapest_segmentation checks the dynamic program
// against an exhaustive search over small inputs: whichever segmentation it
// returns must cost exactly what the best possible one costs. Several
// segmentations can tie, so this compares cost rather than split positions.
func TestDetectSplits_finds_the_cheapest_segmentation(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Capped at 9 so the exhaustive search stays at 256 segmentations, and
		// well under maxSplits so the cap cannot truncate an optimal answer.
		n := rapid.IntRange(1, 9).Draw(t, "n")
		offsets := make([]perCueOffset, n)
		for i := range offsets {
			offsets[i] = perCueOffset{offsetMs: rapid.Int64Range(-50, 50).Draw(t, "offset")}
		}
		// Offsets this small keep segment costs and penalties the same order of
		// magnitude, so the optimum is a genuine trade rather than always one
		// segment or always n.
		penalty := rapid.Float64Range(1, 500).Draw(t, "penalty")

		splits := detectSplits(offsets, penalty)
		got := segmentationCost(offsets, splits, penalty)
		want := cheapestSegmentationCost(offsets, penalty)
		// Relative tolerance: the two cost formulas round differently.
		if math.Abs(got-want) > 1e-6*math.Max(1, math.Abs(want)) {
			t.Fatalf("detectSplits(%v, penalty=%v) = %v costing %v, but the cheapest segmentation costs %v",
				offsets, penalty, splits, got, want)
		}
	})
}

// TestDetectSplits_at_its_smallest_inputs pins the base cases: a run too short
// to split, and the first input where splitting is even possible.
func TestDetectSplits_at_its_smallest_inputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		offsets []perCueOffset
		penalty float64
		want    []int
	}{
		{
			name:    "a_single_offset_cannot_be_split",
			offsets: []perCueOffset{{offsetMs: 42}},
			penalty: 1,
			want:    []int{0},
		},
		{
			name:    "two_equal_offsets_have_nothing_to_gain_from_a_split",
			offsets: []perCueOffset{{offsetMs: 7}, {offsetMs: 7}},
			penalty: 1,
			want:    []int{0},
		},
		{
			name:    "two_differing_offsets_split_when_the_penalty_is_small",
			offsets: []perCueOffset{{offsetMs: 0}, {offsetMs: 1000}},
			penalty: 1,
			want:    []int{0, 1},
		},
		{
			name:    "two_differing_offsets_stay_whole_when_the_penalty_is_large",
			offsets: []perCueOffset{{offsetMs: 0}, {offsetMs: 1000}},
			penalty: 1e9,
			want:    []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectSplits(tt.offsets, tt.penalty)
			if !slices.Equal(got, tt.want) {
				t.Errorf("detectSplits(%v, penalty=%v) = %v, want %v",
					tt.offsets, tt.penalty, got, tt.want)
			}
		})
	}
}

// TestAlignWithSplits_is_invariant_to_shifting_the_incorrect_track checks that
// moving the whole incorrect timebase does not change where its cues end up.
// Every per-cue offset moves by the same amount, so the deviation within each
// run is untouched, the split positions are the same, and each segment's shift
// absorbs the move exactly. Confidence is measured against the reference, so it
// must not move either.
func TestAlignWithSplits_is_invariant_to_shifting_the_incorrect_track(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(6, 30).Draw(t, "n")
		ref := genCues(t, n, "ref")
		// Give the two halves different shifts so splits actually occur.
		lead := time.Duration(rapid.IntRange(0, 4000).Draw(t, "lead_shift")) * time.Millisecond
		tail := lead + time.Duration(rapid.IntRange(5000, 15000).Draw(t, "tail_extra"))*time.Millisecond
		inc := make([]Cue, n)
		for i := range ref {
			shift := lead
			if i >= n/2 {
				shift = tail
			}
			inc[i] = Cue{Start: ref[i].Start + shift, End: ref[i].End + shift, Text: "inc"}
		}
		// Bounded at ~17 minutes: the variance formula subtracts two large
		// nearly-equal sums, so an unbounded timebase would lose the precision
		// this invariance rests on.
		shift := time.Duration(rapid.Int64Range(1, 1_000_000).Draw(t, "timebase_shift")) * time.Millisecond

		base := alignWithSplits(t.Context(), ref, inc, 0)
		moved := alignWithSplits(t.Context(), ref, ShiftCues(inc, shift), 0)

		if len(moved.Cues) != len(base.Cues) {
			t.Fatalf("alignWithSplits(track shifted by %v) returned %d cues, want %d",
				shift, len(moved.Cues), len(base.Cues))
		}
		for i := range base.Cues {
			if moved.Cues[i].Start != base.Cues[i].Start || moved.Cues[i].End != base.Cues[i].End {
				t.Fatalf("alignWithSplits(track shifted by %v) put cue %d at [%v,%v], want [%v,%v]",
					shift, i, moved.Cues[i].Start, moved.Cues[i].End,
					base.Cues[i].Start, base.Cues[i].End)
			}
		}
		if moved.Confidence != base.Confidence {
			t.Fatalf("alignWithSplits(track shifted by %v).Confidence = %v, want %v",
				shift, float64(moved.Confidence), float64(base.Confidence))
		}
	})
}

// TestDetectSplits_never_splits_a_constant_offset checks that a track with no
// timing change in it comes back whole. A uniform run has zero deviation, so
// cutting it saves nothing and costs the penalty — however small that penalty.
func TestDetectSplits_never_splits_a_constant_offset(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 60).Draw(t, "n")
		offset := rapid.Int64Range(-100_000, 100_000).Draw(t, "offset")
		penalty := rapid.Float64Range(1e-12, 1e9).Draw(t, "penalty")
		offsets := make([]perCueOffset, n)
		for i := range offsets {
			offsets[i] = perCueOffset{offsetMs: offset}
		}
		got := detectSplits(offsets, penalty)
		if want := []int{0}; !slices.Equal(got, want) {
			t.Fatalf("detectSplits(%d offsets all %dms, penalty=%v) = %v, want %v",
				n, offset, penalty, got, want)
		}
	})
}
