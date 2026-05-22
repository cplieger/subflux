package subsync

import (
	"context"
	"testing"
	"time"
)

func makeSpans(n int, startOffset time.Duration) []TimeSpan {
	spans := make([]TimeSpan, n)
	ms := startOffset.Milliseconds()
	for i := range spans {
		spans[i] = TimeSpan{Start: ms, End: ms + 2000}
		ms += 3000
	}
	return spans
}

func makeBenchCues(n int, startOffset time.Duration) []Cue {
	cues := make([]Cue, n)
	t := startOffset
	for i := range cues {
		cues[i] = Cue{Start: t, End: t + 2*time.Second}
		t += 3 * time.Second
	}
	return cues
}

func BenchmarkSyncCues(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		ref := makeBenchCues(n, 0)
		inc := makeBenchCues(n, 500*time.Millisecond)
		b.Run(cueCountLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				syncCues(context.Background(), ref, inc)
			}
		})
	}
}

func BenchmarkAlignConstantOffset(b *testing.B) {
	cases := []struct {
		name     string
		refCount int
		incCount int
	}{
		{"small_50", 50, 50},
		{"medium_500", 500, 500},
		{"large_2000", 2000, 2000},
		{"asymmetric_100x1500", 100, 1500},
	}
	for _, tc := range cases {
		ref := makeSpans(tc.refCount, 0)
		inc := makeSpans(tc.incCount, 500*time.Millisecond)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				alignConstantOffset(context.Background(), ref, inc)
			}
		})
	}
}

func cueCountLabel(n int) string {
	switch {
	case n >= 2000:
		return "2000_cues"
	case n >= 500:
		return "500_cues"
	default:
		return "100_cues"
	}
}
