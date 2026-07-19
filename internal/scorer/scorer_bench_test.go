package scorer

import (
	"fmt"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func BenchmarkScore(b *testing.B) {
	engine := New(&api.DefaultScores)

	cases := []struct {
		name    string
		matches api.MatchSet
		sub     api.SubtitleInfo
	}{
		{"no_match", api.MatchSet{}, api.SubtitleInfo{}},
		{"source_only", api.MatchSet{Source: true}, api.SubtitleInfo{}},
		{"full_release", api.MatchSet{
			Source: true, ReleaseGroup: true,
			VideoCodec: true, StreamingService: true,
			Edition: true, HDR: true,
		}, api.SubtitleInfo{}},
		{"hash_verifiable", api.MatchSet{Hash: true}, api.SubtitleInfo{HashVerifiable: true}},
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
	engine := New(&api.DefaultScores)
	matches := api.MatchSet{Source: true, ReleaseGroup: true, VideoCodec: true}
	sub := api.SubtitleInfo{}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			engine.Score(sub, matches)
		}
	})
}

func BenchmarkScoreBatch(b *testing.B) {
	engine := New(&api.DefaultScores)

	for _, n := range []int{10, 50, 100} {
		subs := make([]api.SubtitleInfo, n)
		matchSets := make([]api.MatchSet, n)
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
