package scorer

import (
	"fmt"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

func BenchmarkScore(b *testing.B) {
	engine := New(&subflux.DefaultScores)

	cases := []struct {
		name    string
		matches subflux.MatchSet
		sub     subflux.SubtitleInfo
	}{
		{"no_match", subflux.MatchSet{}, subflux.SubtitleInfo{}},
		{"source_only", subflux.MatchSet{Source: true}, subflux.SubtitleInfo{}},
		{"full_release", subflux.MatchSet{
			Source: true, ReleaseGroup: true,
			VideoCodec: true, StreamingService: true,
			Edition: true, HDR: true,
		}, subflux.SubtitleInfo{}},
		{"hash_verifiable", subflux.MatchSet{Hash: true}, subflux.SubtitleInfo{HashVerifiable: true}},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				engine.Score(tc.sub, tc.matches)
			}
		})
	}
}

func BenchmarkScoreParallel(b *testing.B) {
	engine := New(&subflux.DefaultScores)
	matches := subflux.MatchSet{Source: true, ReleaseGroup: true, VideoCodec: true}
	sub := subflux.SubtitleInfo{}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			engine.Score(sub, matches)
		}
	})
}

func BenchmarkScoreBatch(b *testing.B) {
	engine := New(&subflux.DefaultScores)

	for _, n := range []int{10, 50, 100} {
		subs := make([]subflux.SubtitleInfo, n)
		matchSets := make([]subflux.MatchSet, n)
		for i := range n {
			if i%3 == 0 {
				matchSets[i].Source = true
			}
			if i%5 == 0 {
				matchSets[i].ReleaseGroup = true
			}
			if i%7 == 0 {
				matchSets[i].VideoCodec = true
			}
		}

		b.Run(fmt.Sprintf("candidates_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for i := range n {
					engine.Score(subs[i], matchSets[i])
				}
			}
		})
	}
}
