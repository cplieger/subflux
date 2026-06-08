package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func FuzzBuildMatches(f *testing.F) {
	f.Add("Movie.2024.BluRay.x264-GRP", "Movie.2024.BluRay.x264-GRP.srt", "hash", "movie")
	f.Add("Show.S01E01.1080p.WEB-DL", "Show.S01E01.720p.HDTV.srt", "imdb", "episode")
	f.Add("", "", "title", "movie")
	f.Add("Anime.Episode.25", "[Sub] Anime.25.srt", "title", "episode")

	f.Fuzz(func(t *testing.T, videoRelease, subRelease, matchedBy, mediaType string) {
		mt := api.MediaType(mediaType)
		if mt != api.MediaTypeEpisode && mt != api.MediaTypeMovie {
			mt = api.MediaTypeMovie
		}
		mm := api.MatchMethod(matchedBy)
		switch mm {
		case api.MatchByHash, api.MatchByIMDB, api.MatchByTitle, api.MatchByTVDB, api.MatchByTMDB:
		default:
			mm = api.MatchByTitle
		}

		video := &api.VideoInfo{
			MediaType:    mt,
			ReleaseGroup: videoRelease,
		}
		sub := &api.Subtitle{
			ReleaseName: subRelease,
			MatchedBy:   mm,
		}
		deps := MatchDeps{
			ParseRelease: func(s string) ReleaseInfo {
				// Minimal extraction: just use raw string as all fields for coverage.
				return ReleaseInfo{
					Source:       s,
					VideoCodec:   s,
					ReleaseGroup: s,
				}
			},
			CompareSource: func(m *api.MatchSet, a, b string) {
				if a != "" && b != "" && a == b {
					m.Source = true
				}
			},
			IsSeasonPack: func(name string) bool {
				return IsSeasonPack(name)
			},
		}

		// Must not panic.
		matches := BuildMatches(video, sub, deps)

		// Invariant: if MatchedBy is hash, Hash must be set.
		if mm == api.MatchByHash && !matches.Hash {
			t.Fatal("MatchByHash should set Hash=true")
		}
	})
}
