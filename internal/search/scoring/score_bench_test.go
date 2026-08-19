package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

func BenchmarkBuildMatches(b *testing.B) {
	deps := MatchDeps{
		ParseRelease: func(s string) ReleaseInfo {
			return ReleaseInfo{
				Source:       "bluray",
				VideoCodec:   "x264",
				ReleaseGroup: "GRP",
			}
		},
		CompareSource: func(m *subflux.MatchSet, videoSrc, subSrc string) {
			if videoSrc != "" && subSrc != "" && videoSrc == subSrc {
				m.Source = true
			}
		},
		IsSeasonPack: func(string) bool { return false },
	}

	video := &subflux.VideoInfo{
		MediaType:    subflux.MediaTypeEpisode,
		ReleaseGroup: "Show.S01E01.1080p.BluRay.x264-GRP",
	}

	makeSubs := func(n int) []*subflux.Subtitle {
		subs := make([]*subflux.Subtitle, n)
		for i := range subs {
			subs[i] = &subflux.Subtitle{
				ReleaseName: "Show.S01E01.1080p.BluRay.x264-GRP.srt",
				MatchedBy:   subflux.MatchByHash,
			}
		}
		return subs
	}

	for _, count := range []int{1, 10, 50} {
		subs := makeSubs(count)
		b.Run(benchName(count), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				for _, sub := range subs {
					BuildMatches(video, sub, deps)
				}
			}
		})
	}
}

func benchName(n int) string {
	switch n {
	case 1:
		return "1_subtitle"
	case 10:
		return "10_subtitles"
	default:
		return "50_subtitles"
	}
}
