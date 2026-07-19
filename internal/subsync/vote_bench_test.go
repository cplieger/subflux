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
				Source:     SourceOffset,
				Offset:     offsetMs,
				Transform:  Transform{Kind: TransformShift, Shift: offsetMs},
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

// BenchmarkClusterCandidates measures corrected-cue clustering with
// non-shift transforms (the path that compares every cue pair).
func BenchmarkClusterCandidates(b *testing.B) {
	inc := makeBenchCues(1500, 0)
	candidates := []SyncResult{
		{
			Cues: makeBenchCues(1500, 100*time.Millisecond), Confidence: 0.6,
			Method: MethodFramerate, Source: SourceFramerate,
			Transform: Transform{Kind: TransformFramerate, Ratio: 1.001},
		},
		{
			Cues: inc, Confidence: 0.6,
			Method: MethodSplit, Source: SourceSplit,
			Transform: Transform{Kind: TransformSegments, Segments: []Segment{{StartIdx: 0, EndIdx: 1500}}},
		},
		{
			Cues: makeBenchCues(1500, 50*time.Millisecond), Confidence: 0.7,
			Method: MethodOffset, Source: SourceOffset,
			Transform: Transform{Kind: TransformShift, Shift: 50},
		},
	}
	b.ReportAllocs()
	for range b.N {
		clusterCandidates(candidates)
	}
}
