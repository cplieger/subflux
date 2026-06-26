package arrapi

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// TestLogMovieSummary verifies the documented pre-scan INFO summary: total
// movies and the count qualifying for subtitle search (has a file, and not
// excluded by tag).
func TestLogMovieSummary(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	movies := []api.Movie{
		{ID: 1, Title: "Wanted A", HasFile: true, MovieFile: &api.MovieFile{Path: "/a.mkv"}},
		{ID: 2, Title: "No file", HasFile: false},
		{ID: 3, Title: "Wanted B", HasFile: true, MovieFile: &api.MovieFile{Path: "/b.mkv"}},
		{ID: 4, Title: "Excluded by tag", HasFile: true, MovieFile: &api.MovieFile{Path: "/c.mkv"}, Tags: []int{7}},
		{ID: 5, Title: "Has file, nil movieFile", HasFile: true, MovieFile: nil},
	}
	logMovieSummary(movies, map[int]struct{}{7: {}})

	rec, ok := h.find("fetched movie list")
	if !ok {
		t.Fatal("logMovieSummary() did not emit 'fetched movie list'")
	}
	if got := logAttrInt(t, rec, "total_movies"); got != 5 {
		t.Errorf("total_movies = %d, want 5", got)
	}
	// Only movies 1 and 3 qualify: 2 (no file), 4 (excluded tag), 5 (nil movieFile) are skipped.
	if got := logAttrInt(t, rec, "target_movies"); got != 2 {
		t.Errorf("target_movies = %d, want 2", got)
	}
}
