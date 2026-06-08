package arrapi

import (
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
)

// logMovieSummary logs pre-scan totals from the movie list.
func logMovieSummary(allMovies []api.Movie, excludeTagIDs map[int]struct{}) {
	var targetMovies int
	for i := range allMovies {
		if wantedMovie(&allMovies[i], excludeTagIDs) {
			targetMovies++
		}
	}
	slog.Info("fetched movie list",
		"total_movies", len(allMovies),
		"target_movies", targetMovies)
}

// wantedMovie reports whether a movie qualifies for subtitle search.
func wantedMovie(m *api.Movie, excludeTagIDs map[int]struct{}) bool {
	return m.HasFile &&
		m.MovieFile != nil &&
		!api.HasExcludeTag(m.Tags, excludeTagIDs)
}
