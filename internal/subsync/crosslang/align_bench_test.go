package crosslang

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"
)

func makeBenchCues(n int) []Cue {
	cues := make([]Cue, n)
	for i := range n {
		start := time.Duration(i*3000) * time.Millisecond
		cues[i] = Cue{
			Start: start,
			End:   start + 2*time.Second,
			Text:  "The quick brown fox jumps over the lazy dog.",
		}
	}
	return cues
}

func makeBenchPairs(n int) []CuePair {
	pairs := make([]CuePair, n)
	for i := range n {
		pairs[i] = CuePair{
			IncIdx:   i,
			RefIdx:   i + rand.IntN(3),
			Score:    0.5 + rand.Float64()*0.5,
			OffsetMs: int64(rand.IntN(200) - 100),
		}
	}
	return pairs
}

func BenchmarkAlign(b *testing.B) {
	for _, n := range []int{50, 200, 500} {
		ref := makeBenchCues(n)
		inc := makeBenchCues(n)
		// Shift incorrect cues by a constant offset.
		for i := range inc {
			inc[i].Start += 500 * time.Millisecond
			inc[i].End += 500 * time.Millisecond
		}
		b.Run(sizeLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = Align(context.Background(), ref, inc)
			}
		})
	}
}

func BenchmarkDPAlign(b *testing.B) {
	for _, n := range []int{100, 500, 1000} {
		pairs := makeBenchPairs(n)
		b.Run(sizeLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = DPAlign(pairs)
			}
		})
	}
}

func BenchmarkWeightedMedianOffset(b *testing.B) {
	pairs := makeBenchPairs(200)
	b.ReportAllocs()
	for range b.N {
		_ = WeightedMedianOffset(pairs)
	}
}

func sizeLabel(n int) string {
	switch {
	case n >= 1000:
		return "1000"
	case n >= 500:
		return "500"
	case n >= 200:
		return "200"
	case n >= 100:
		return "100"
	default:
		return "50"
	}
}
