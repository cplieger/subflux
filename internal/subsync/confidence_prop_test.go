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

		// If ShouldApply is true, confidence >= 0.5.
		if shouldApply && r.Confidence < 0.5 {
			t.Fatal("ShouldApply true but confidence < 0.5")
		}

		// If offset != 0, Applied must be true.
		if r.Offset != 0 && !applied {
			t.Fatal("nonzero offset but Applied() = false")
		}

		// If rate != 0 and rate != 1.0, Applied must be true.
		if r.Rate != 0 && r.Rate != 1.0 && !applied {
			t.Fatal("nonzero non-1 rate but Applied() = false")
		}
	})
}
