package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

func FuzzBuildMatches(f *testing.F) {
	f.Add("Movie.2024.BluRay.x264-GRP", "Movie.2024.BluRay.x264-GRP.srt", "hash", "movie")
	f.Add("Show.S01E01.1080p.WEB-DL", "Show.S01E01.720p.HDTV.srt", "imdb", "episode")
	f.Add("", "", "title", "movie")
	f.Add("Anime.Episode.25", "[Sub] Anime.25.srt", "title", "episode")

	f.Fuzz(func(t *testing.T, videoRelease, subRelease, matchedBy, mediaType string) {
		mt := subflux.MediaType(mediaType)
		if mt != subflux.MediaTypeEpisode && mt != subflux.MediaTypeMovie {
			mt = subflux.MediaTypeMovie
		}
		mm := subflux.MatchMethod(matchedBy)
		switch mm {
		case subflux.MatchByHash, subflux.MatchByIMDB, subflux.MatchByTitle, subflux.MatchByTVDB, subflux.MatchByTMDB:
		default:
			mm = subflux.MatchByTitle
		}

		video := &subflux.VideoInfo{
			MediaType:    mt,
			ReleaseGroup: videoRelease,
		}
		sub := &subflux.Subtitle{
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
			CompareSource: func(m *subflux.MatchSet, a, b string) {
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
		if mm == subflux.MatchByHash && !matches.Hash {
			t.Fatal("MatchByHash should set Hash=true")
		}
	})
}

func FuzzMatchBreakdown(f *testing.F) {
	f.Add(true, true, true, true, true, true, true, true)
	f.Add(false, false, false, false, false, false, false, false)
	f.Add(true, false, true, false, true, false, true, false)

	f.Fuzz(func(t *testing.T, hash, src, rg, ss, vc, hdr, ed, sp bool) {
		matches := subflux.MatchSet{
			Hash:             hash,
			Source:           src,
			ReleaseGroup:     rg,
			StreamingService: ss,
			VideoCodec:       vc,
			HDR:              hdr,
			Edition:          ed,
			SeasonPack:       sp,
		}
		scores := &subflux.DefaultScores
		breakdown := MatchBreakdown(scores, matches)
		// If hash is set, the breakdown must report the hash score.
		if hash && breakdown["hash"] != scores.Hash {
			t.Fatalf("MatchBreakdown: hash set but breakdown[hash]=%d, want %d", breakdown["hash"], scores.Hash)
		}
		// Every contribution must be non-negative.
		for k, v := range breakdown {
			if v < 0 {
				t.Fatalf("MatchBreakdown: negative value for %q: %d", k, v)
			}
		}
	})
}
