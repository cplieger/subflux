package subsync

import (
	"testing"
	"time"
)

func BenchmarkVoteOnCandidates(b *testing.B) {
	ref := makeBenchCues(100, 0)
	inc := makeBenchCues(100, 500*time.Millisecond)

	cases := []struct {
		name string
		n    int
	}{
		{"2_candidates", 2},
		{"5_candidates", 5},
		{"10_candidates", 10},
	}

	for _, tc := range cases {
		candidates := make([]SyncResult, tc.n)
		for i := range candidates {
			offsetMs := int64(i * 100)
			candidates[i] = SyncResult{
				Cues:       makeBenchCues(100, time.Duration(offsetMs)*time.Millisecond),
				Confidence: Confidence(0.5 + float64(i)*0.05),
				Method:     MethodOffset,
				Offset:     offsetMs,
			}
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				voteOnCandidates(candidates, ref, inc)
			}
		})
	}
}
