package api

import (
	"strings"
	"testing"
)

func FuzzBuildEpisodeID(f *testing.F) {
	f.Add(12345, "tt1234567", 1, 1)
	f.Add(0, "tt0000001", 0, 0)
	f.Add(0, "", 99, 99)
	f.Add(-1, "", -1, -1)
	f.Add(999999, "", 100, 100)
	f.Fuzz(func(t *testing.T, tvdbID int, imdbID string, season, episode int) {
		result := BuildEpisodeID(tvdbID, imdbID, season, episode)
		if tvdbID != 0 && !strings.HasPrefix(result, "tvdb-") {
			t.Errorf("tvdbID=%d but result=%q does not start with tvdb-", tvdbID, result)
		}
	})
}

func FuzzBuildMovieID(f *testing.F) {
	f.Add(100, "tt1234567")
	f.Add(0, "tt0000001")
	f.Add(0, "")
	f.Add(-5, "")
	f.Fuzz(func(t *testing.T, tmdbID int, imdbID string) {
		result := BuildMovieID(tmdbID, imdbID)
		if tmdbID != 0 && !strings.HasPrefix(result, "tmdb-") {
			t.Errorf("tmdbID=%d but result=%q does not start with tmdb-", tmdbID, result)
		}
		if tmdbID == 0 && imdbID != "" && result != imdbID {
			t.Errorf("tmdbID=0, imdbID=%q but result=%q", imdbID, result)
		}
		if tmdbID == 0 && imdbID == "" && result != "" {
			t.Errorf("both zero but result=%q", result)
		}
	})
}

func FuzzBuildSeriesPrefix(f *testing.F) {
	f.Add(12345, "tt1234567")
	f.Add(0, "tt0000001")
	f.Add(0, "")
	f.Add(-1, "")
	f.Fuzz(func(t *testing.T, tvdbID int, imdbID string) {
		result := BuildSeriesPrefix(tvdbID, imdbID)
		if result != "" && !strings.HasSuffix(result, "-") {
			t.Errorf("non-empty result=%q does not end with -", result)
		}
		if tvdbID != 0 && !strings.HasPrefix(result, "tvdb-") {
			t.Errorf("tvdbID=%d but result=%q does not start with tvdb-", tvdbID, result)
		}
	})
}
