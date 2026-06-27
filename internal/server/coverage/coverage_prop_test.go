package coverage_test

import (
	"strconv"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/coverage"
	"pgregory.net/rapid"
)

// TestExtractSeriesPrefix_matchesBuildSeriesPrefix is a cross-function
// consistency property: the prefix extracted from an episode media ID built by
// BuildEpisodeID must equal the series prefix BuildSeriesPrefix produces for
// the same identifiers. It pins the producer/consumer contract between the two
// api ID helpers and the coverage extractor, so a format change on either side
// that breaks coverage grouping is caught.
func TestExtractSeriesPrefix_matchesBuildSeriesPrefix(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		season := rapid.IntRange(0, 99).Draw(rt, "season")
		episode := rapid.IntRange(0, 99).Draw(rt, "episode")

		// TVDB-based IDs (the canonical Sonarr path).
		tvdbID := rapid.IntRange(1, 1_000_000).Draw(rt, "tvdbID")
		epID := api.BuildEpisodeID(tvdbID, "", season, episode)
		if got, want := coverage.ExtractSeriesPrefix(epID), api.BuildSeriesPrefix(tvdbID, ""); got != want {
			rt.Fatalf("ExtractSeriesPrefix(%q) = %q, want %q", epID, got, want)
		}

		// IMDB-fallback IDs (tvdbID == 0).
		imdbID := "tt" + strconv.Itoa(rapid.IntRange(1, 99_999_999).Draw(rt, "imdbNum"))
		epID = api.BuildEpisodeID(0, imdbID, season, episode)
		if got, want := coverage.ExtractSeriesPrefix(epID), api.BuildSeriesPrefix(0, imdbID); got != want {
			rt.Fatalf("ExtractSeriesPrefix(%q) = %q, want %q", epID, got, want)
		}
	})
}
