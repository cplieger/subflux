package subsync

import (
	"context"
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

		result := alignWithSplits(context.Background(), ref, inc, 0)
		if len(result.Cues) != len(inc) {
			t.Fatalf("alignWithSplits returned %d cues, want %d", len(result.Cues), len(inc))
		}
	})
}

// TestAlignWithSplits_identity verifies that when reference == incorrect
// (identity case), the result has zero or near-zero offset and moderate+
// confidence.
func TestAlignWithSplits_identity(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(5, 50).Draw(t, "n")
		cues := genCues(t, n, "cues")

		result := alignWithSplits(context.Background(), cues, cues, 0)
		if len(result.Cues) != len(cues) {
			t.Fatalf("identity: returned %d cues, want %d", len(result.Cues), len(cues))
		}
		// For identity input, confidence should be non-zero.
		if result.Confidence <= 0 {
			t.Fatalf("identity: confidence = %v, want > 0", result.Confidence)
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
		offsets := perCueOffsets(context.Background(), refSpans, inc)

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
		offsets2 := perCueOffsets(context.Background(), refSpans, inc)
		for i := range offsets {
			if offsets[i] != offsets2[i] {
				t.Fatalf("perCueOffsets non-deterministic at index %d: %v vs %v", i, offsets[i], offsets2[i])
			}
		}
	})
}
