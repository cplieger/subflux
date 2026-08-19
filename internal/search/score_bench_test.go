package search

import (
	"fmt"
	"testing"

	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/subflux"
)

func makeBenchSubs(n int) []subflux.Subtitle {
	subs := make([]subflux.Subtitle, n)
	providers := []subflux.ProviderID{"opensubtitles", "subdl", "gestdown", "yifysubtitles", "betaseries"}
	for i := range n {
		subs[i] = subflux.Subtitle{
			Provider:    providers[i%len(providers)],
			ID:          fmt.Sprintf("sub-%d", i),
			Language:    "en",
			ReleaseName: fmt.Sprintf("Show.S01E%02d.720p.WEB-DL.DDP5.1.H.264-GROUP%d", i%20+1, i%5),
			Title:       "Show",
			Season:      1,
			Episode:     i%20 + 1,
		}
		if i%4 == 0 {
			subs[i].MatchedBy = "hash"
		}
	}
	return subs
}

func BenchmarkScoreResults(b *testing.B) {
	sc := scorer.New(&subflux.DefaultScores)
	video := &subflux.VideoInfo{
		ReleaseGroup: "Show.S01E05.720p.WEB-DL.DDP5.1.H.264-GROUP0",
		MediaType:    subflux.MediaTypeEpisode,
	}
	provPriority := func(name subflux.ProviderID) int {
		switch name {
		case "opensubtitles":
			return 1
		case "subdl":
			return 2
		default:
			return 3
		}
	}

	for _, n := range []int{10, 100, 500} {
		subs := makeBenchSubs(n)
		b.Run(fmt.Sprintf("subs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				scoreResults(sc, video, subs, provPriority)
			}
		})
	}
}

func BenchmarkFilterByIdentity(b *testing.B) {
	req := &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode,
		Title:     "Breaking Bad",
		Season:    3,
		Episode:   7,
		AlternativeTitles: []string{
			"Breaking Bad US",
			"BB",
		},
	}

	for _, n := range []int{10, 50, 200} {
		subs := make([]subflux.Subtitle, n)
		for i := range n {
			subs[i] = subflux.Subtitle{
				Provider:    "opensubtitles",
				ReleaseName: fmt.Sprintf("Breaking.Bad.S03E%02d.720p.BluRay.x264-DEMAND", i%12+1),
				Title:       "Breaking Bad",
				Season:      3,
				Episode:     i%12 + 1,
			}
			// Mix in some wrong-identity results
			if i%5 == 0 {
				subs[i].Title = "Better Call Saul"
				subs[i].Season = 2
				subs[i].Episode = i%10 + 1
			}
		}

		b.Run(fmt.Sprintf("subs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				filterByIdentity(subs, req)
			}
		})
	}
}

func BenchmarkFilterByIdentity_manyTitles(b *testing.B) {
	req := &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode,
		Title:     "Attack on Titan",
		Season:    4,
		Episode:   12,
		AlternativeTitles: []string{
			"Shingeki no Kyojin",
			"AoT",
			"Attack on Titan Final Season",
			"進撃の巨人",
		},
	}

	subs := make([]subflux.Subtitle, 200)
	for i := range 200 {
		subs[i] = subflux.Subtitle{
			Provider:    "subdl",
			ReleaseName: fmt.Sprintf("Shingeki.no.Kyojin.S04E%02d.1080p.WEB.H.264-GROUP", i%24+1),
			Title:       "Shingeki no Kyojin",
			Season:      4,
			Episode:     i%24 + 1,
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		filterByIdentity(subs, req)
	}
}
