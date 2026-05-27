package mediahandlers_test

import (
	"testing"
)

// --- HandleMediaSeries ---

func TestHandleMediaSeries_ReturnsAllSeries(t *testing.T) {
	t.Skip("TODO: returns series list with coverage targets")
}

func TestHandleMediaSeries_IncludesExcludedFlag(t *testing.T) {
	t.Skip("TODO: marks series with arr exclusion tag")
}

// --- HandleMediaMovies ---

func TestHandleMediaMovies_ReturnsAllMovies(t *testing.T) {
	t.Skip("TODO: returns movie list with coverage targets")
}

func TestHandleMediaMovies_IncludesSubtitleInfo(t *testing.T) {
	t.Skip("TODO: includes embedded + external subtitle entries per movie")
}

// --- HandleMediaEpisodes ---

func TestHandleMediaEpisodes_GroupsBySeason(t *testing.T) {
	t.Skip("TODO: returns episodes grouped into SeasonGroup structs")
}

func TestHandleMediaEpisodes_IncludesAbsoluteEpisodeNumber(t *testing.T) {
	t.Skip("TODO: includes absolute_episode when available from Sonarr")
}

func TestHandleMediaEpisodes_FiltersToHasFile(t *testing.T) {
	t.Skip("TODO: only includes episodes that have a video file")
}

// --- groupEpisodesBySeason ---

func TestGroupEpisodesBySeason_SortsSeasons(t *testing.T) {
	t.Skip("TODO: seasons sorted ascending, specials last")
}

// --- extractPathSegment ---

func TestExtractPathSegment(t *testing.T) {
	t.Skip("TODO: extracts segment between prefix and suffix")
}
