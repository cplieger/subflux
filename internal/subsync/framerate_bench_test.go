package subsync

import (
	"context"
	"testing"
	"time"
)

func BenchmarkCorrectFramerate(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		// Simulate a framerate mismatch: reference at 1.0x, incorrect at 1.001x
		// (a subtle 23.976→24.000 drift). This exercises measureDrifts,
		// linearRegression, matchKnownRatio, and goldenSectionSearch.
		ref := makeBenchCues(n, 0)
		inc := makeFramerateDriftCues(n, 1.001)
		b.Run(cueCountLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				correctFramerate(context.Background(), ref, inc, "")
			}
		})
	}
}

// makeFramerateDriftCues creates cues with a simulated framerate drift.
// Each cue's timing is scaled by ratio relative to a 3-second spacing.
func makeFramerateDriftCues(n int, ratio float64) []Cue {
	cues := make([]Cue, n)
	for i := range cues {
		start := time.Duration(float64(i*3000)*ratio) * time.Millisecond
		cues[i] = Cue{Start: start, End: start + 2*time.Second}
	}
	return cues
}
