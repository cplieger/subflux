package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
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
		CompareSource: func(m *api.MatchSet, videoSrc, subSrc string) {
			if videoSrc != "" && subSrc != "" && videoSrc == subSrc {
				m.Source = true
			}
		},
		IsSeasonPack: func(string) bool { return false },
	}

	video := &api.VideoInfo{
		MediaType:    api.MediaTypeEpisode,
		ReleaseGroup: "Show.S01E01.1080p.BluRay.x264-GRP",
	}

	makeSubs := func(n int) []*api.Subtitle {
		subs := make([]*api.Subtitle, n)
		for i := range subs {
			subs[i] = &api.Subtitle{
				ReleaseName: "Show.S01E01.1080p.BluRay.x264-GRP.srt",
				MatchedBy:   api.MatchByHash,
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
