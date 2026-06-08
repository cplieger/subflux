package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzFilterByIdentity exercises the identity filter's title-matching,
// episode-number logic, and release-name parsing with fuzzed inputs.
func FuzzFilterByIdentity(f *testing.F) {
	// Seed corpus with representative inputs.
	f.Add("Breaking Bad", "breaking.bad.s01e01.720p", "Breaking Bad", 1, 1, 0, 0, "episode")
	f.Add("The Office", "The.Office.S02E03.HDTV", "The Office US", 2, 3, 0, 0, "episode")
	f.Add("Inception", "Inception.2010.1080p.BluRay", "Inception", 0, 0, 0, 0, "movie")
	f.Add("Attack on Titan", "[SubGroup] Shingeki no Kyojin - 25", "Shingeki no Kyojin", 1, 25, 0, 25, "episode")
	f.Add("", "", "", 0, 0, 0, 0, "episode")
	f.Add("Show: With Colon", "Show.With.Colon.S01E01", "Show With Colon", 1, 1, 0, 0, "episode")

	f.Fuzz(func(t *testing.T, subTitle, releaseName, reqTitle string, season, episode, sceneSeason, absEpisode int, mediaType string) {
		// Clamp to reasonable ranges to avoid meaningless inputs.
		if season < 0 || season > 99 {
			return
		}
		if episode < 0 || episode > 9999 {
			return
		}
		if sceneSeason < 0 || sceneSeason > 99 {
			return
		}
		if absEpisode < 0 || absEpisode > 9999 {
			return
		}

		mt := api.MediaType(mediaType)
		if mt != api.MediaTypeEpisode && mt != api.MediaTypeMovie {
			mt = api.MediaTypeEpisode
		}

		req := &api.SearchRequest{
			Title:           reqTitle,
			Season:          season,
			Episode:         episode,
			SceneSeason:     sceneSeason,
			AbsoluteEpisode: absEpisode,
			MediaType:       mt,
		}

		sub := api.Subtitle{
			Title:       subTitle,
			ReleaseName: releaseName,
			Season:      season,
			Episode:     episode,
		}

		results := []api.Subtitle{sub}

		// Should not panic.
		_, _ = FilterByIdentity(results, req)
	})
}
