package subsync

import (
	"context"
	"testing"
	"time"
)

func BenchmarkAlignWithSplits(b *testing.B) {
	// Build reference and incorrect cues simulating a 2-hour movie.
	n := 500
	ref := make([]Cue, n)
	inc := make([]Cue, n)
	for i := range n {
		start := time.Duration(i) * 7200 * time.Millisecond / time.Duration(n)
		end := start + 3*time.Second
		ref[i] = Cue{Start: start, End: end, Text: "Reference cue"}
		// Incorrect subs have a 2s offset in the first half, 5s in the second.
		offset := 2 * time.Second
		if i > n/2 {
			offset = 5 * time.Second
		}
		inc[i] = Cue{Start: start + offset, End: end + offset, Text: "Incorrect cue"}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = alignWithSplits(context.Background(), ref, inc, 7.0)
	}
}
