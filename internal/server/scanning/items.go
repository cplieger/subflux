package scanning

import (
	"slices"
	"strings"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// ScanItem holds either an episode or a movie for alphabetical scanning.
type ScanItem struct {
	Series *arrapi.Series  // non-nil for episodes
	Ep     *arrapi.Episode // non-nil for episodes
	Movie  *arrapi.Movie   // non-nil for movies
}

// SortByTitle merges episodes and movies into a single slice sorted
// alphabetically by title (case-insensitive).
func SortByTitle(episodes, movies []ScanItem) []ScanItem {
	total := len(episodes) + len(movies)
	if total == 0 {
		return nil
	}
	out := make([]ScanItem, 0, total)
	out = append(out, episodes...)
	out = append(out, movies...)

	type sortKey struct {
		title   string
		season  int
		episode int
	}
	keys := make([]sortKey, len(out))
	for i := range out {
		s, e := ScanItemSeasonEp(out[i])
		keys[i] = sortKey{
			title:   strings.ToLower(ScanItemTitle(out[i])),
			season:  s,
			episode: e,
		}
	}

	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int {
		if c := strings.Compare(keys[a].title, keys[b].title); c != 0 {
			return c
		}
		if keys[a].season != keys[b].season {
			return keys[a].season - keys[b].season
		}
		return keys[a].episode - keys[b].episode
	})

	sorted := make([]ScanItem, len(out))
	for i, j := range idx {
		sorted[i] = out[j]
	}
	return sorted
}

// ScanItemSeasonEp returns season and episode for sorting.
func ScanItemSeasonEp(item ScanItem) (season, episode int) {
	if item.Ep != nil {
		return item.Ep.SeasonNumber, item.Ep.EpisodeNumber
	}
	return 0, 0
}

// ScanItemTitle returns the title used for sort ordering.
func ScanItemTitle(item ScanItem) string {
	if item.Series != nil {
		return item.Series.Title
	}
	if item.Movie != nil {
		return item.Movie.Title
	}
	return ""
}

// SkipResumed checks if a scan item was already processed recently.
func SkipResumed(item ScanItem, recent map[string]bool, stats *api.ScanStats) bool {
	if recent == nil {
		return false
	}
	var mediaID string
	if item.Ep != nil {
		mediaID = api.BuildEpisodeID(
			item.Series.TvdbID, item.Series.ImdbID,
			item.Ep.SeasonNumber, item.Ep.EpisodeNumber)
	} else {
		mediaID = api.BuildMovieID(item.Movie.TmdbID, item.Movie.ImdbID)
		if mediaID == "" {
			return false
		}
	}
	if !recent[mediaID] {
		return false
	}
	if item.Ep != nil {
		stats.EpisodesSkipped++
		stats.EpisodesSearched++
	} else {
		stats.MoviesSkipped++
		stats.MoviesSearched++
	}
	return true
}
