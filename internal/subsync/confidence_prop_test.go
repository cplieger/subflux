package subsync

import (
	"testing"

	"pgregory.net/rapid"
)

func TestSyncResult_predicates_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		r := SyncResult{
			Method:     rapid.SampledFrom([]SyncMethod{MethodNone, MethodOffset, MethodFramerate, MethodSplit, MethodAudio, MethodCrosslang}).Draw(t, "method"),
			Offset:     rapid.Int64Range(-100000, 100000).Draw(t, "offset"),
			Confidence: Confidence(rapid.Float64Range(0, 1).Draw(t, "confidence")),
			Rate:       rapid.Float64Range(0, 3).Draw(t, "rate"),
		}

		applied := r.Applied()
		shouldApply := r.ShouldApply()

		// ShouldApply is exactly Confidence >= 0.5 (checked in both directions).
		if shouldApply != (r.Confidence >= 0.5) {
			t.Fatalf("ShouldApply()=%v but Confidence=%f (threshold 0.5)", shouldApply, float64(r.Confidence))
		}

		// A nonzero offset always counts as applied.
		if r.Offset != 0 && !applied {
			t.Fatal("nonzero offset but Applied() = false")
		}

		// A rate other than 0 or 1.0 always counts as applied.
		if r.Rate != 0 && r.Rate != 1.0 && !applied {
			t.Fatal("nonzero non-1 rate but Applied() = false")
		}

		// No change at all (zero offset, unit rate, non-split method) is never applied.
		if r.Offset == 0 && r.Rate == 1.0 && r.Method != MethodSplit && applied {
			t.Fatal("Applied() = true with zero offset, unit rate, and non-split method")
		}
	})
}
