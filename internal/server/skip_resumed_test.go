package server

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/scanning"
)

// --- scanning.SkipResumed ---

func TestSkipResumed_nil_recent_returns_false(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 100}}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, nil, stats)

	if got {
		t.Error("scanning.SkipResumed(nil recent) = true, want false")
	}
	if stats.MoviesSkipped != 0 {
		t.Errorf("MoviesSkipped = %d, want 0", stats.MoviesSkipped)
	}
}

func TestSkipResumed_movie_not_in_recent(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 100}}
	recent := map[string]bool{"tmdb-200": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if got {
		t.Error("scanning.SkipResumed(movie not in recent) = true, want false")
	}
	if stats.MoviesSkipped != 0 {
		t.Errorf("MoviesSkipped = %d, want 0", stats.MoviesSkipped)
	}
}

func TestSkipResumed_movie_in_recent(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 100}}
	recent := map[string]bool{"tmdb-100": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if !got {
		t.Error("scanning.SkipResumed(movie in recent) = false, want true")
	}
	if stats.MoviesSkipped != 1 {
		t.Errorf("MoviesSkipped = %d, want 1", stats.MoviesSkipped)
	}
	if stats.MoviesSearched != 1 {
		t.Errorf("MoviesSearched = %d, want 1", stats.MoviesSearched)
	}
	if stats.EpisodesSkipped != 0 {
		t.Errorf("EpisodesSkipped = %d, want 0 (movie path)", stats.EpisodesSkipped)
	}
}

func TestSkipResumed_episode_not_in_recent(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{
		Series: &api.Series{TvdbID: 81189},
		Ep:     &api.Episode{SeasonNumber: 1, EpisodeNumber: 3},
	}
	recent := map[string]bool{"tvdb-81189-s01e01": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if got {
		t.Error("scanning.SkipResumed(episode not in recent) = true, want false")
	}
	if stats.EpisodesSkipped != 0 {
		t.Errorf("EpisodesSkipped = %d, want 0", stats.EpisodesSkipped)
	}
}

func TestSkipResumed_episode_in_recent(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{
		Series: &api.Series{TvdbID: 81189},
		Ep:     &api.Episode{SeasonNumber: 2, EpisodeNumber: 5},
	}
	recent := map[string]bool{"tvdb-81189-s02e05": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if !got {
		t.Error("scanning.SkipResumed(episode in recent) = false, want true")
	}
	if stats.EpisodesSkipped != 1 {
		t.Errorf("EpisodesSkipped = %d, want 1", stats.EpisodesSkipped)
	}
	if stats.EpisodesSearched != 1 {
		t.Errorf("EpisodesSearched = %d, want 1", stats.EpisodesSearched)
	}
	if stats.MoviesSkipped != 0 {
		t.Errorf("MoviesSkipped = %d, want 0 (episode path)", stats.MoviesSkipped)
	}
}

func TestSkipResumed_episode_media_id_format(t *testing.T) {
	t.Parallel()
	// Verify the exact media ID format: tvdb-{id}-s{SS}e{EE}
	item := scanning.ScanItem{
		Series: &api.Series{TvdbID: 12345},
		Ep:     &api.Episode{SeasonNumber: 10, EpisodeNumber: 99},
	}
	recent := map[string]bool{"tvdb-12345-s10e99": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if !got {
		t.Error("scanning.SkipResumed(tvdb-12345-s10e99) = false, want true")
	}
}

func TestSkipResumed_empty_recent_map(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 100}}
	recent := map[string]bool{}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if got {
		t.Error("scanning.SkipResumed(empty recent) = true, want false")
	}
}

func TestSkipResumed_stats_accumulate(t *testing.T) {
	t.Parallel()
	stats := &api.ScanStats{MoviesSearched: 5, MoviesSkipped: 2}
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 100}}
	recent := map[string]bool{"tmdb-100": true}

	scanning.SkipResumed(item, recent, stats)

	if stats.MoviesSearched != 6 {
		t.Errorf("MoviesSearched = %d, want 6 (5+1)", stats.MoviesSearched)
	}
	if stats.MoviesSkipped != 3 {
		t.Errorf("MoviesSkipped = %d, want 3 (2+1)", stats.MoviesSkipped)
	}
}

func TestSkipResumed_movie_empty_media_id_returns_false(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 0}}
	recent := map[string]bool{"tmdb-0": true, "": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if got {
		t.Error("scanning.SkipResumed(movie with empty mediaID) = true, want false")
	}
	if stats.MoviesSkipped != 0 {
		t.Errorf("MoviesSkipped = %d, want 0 (empty mediaID skipped)", stats.MoviesSkipped)
	}
}

func TestSkipResumed_movie_imdb_fallback(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{Movie: &api.Movie{TmdbID: 0, ImdbID: "tt1234567"}}
	recent := map[string]bool{"tt1234567": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if !got {
		t.Error("scanning.SkipResumed(movie IMDB fallback in recent) = false, want true")
	}
	if stats.MoviesSkipped != 1 {
		t.Errorf("MoviesSkipped = %d, want 1", stats.MoviesSkipped)
	}
}

func TestSkipResumed_episode_imdb_fallback(t *testing.T) {
	t.Parallel()
	item := scanning.ScanItem{
		Series: &api.Series{TvdbID: 0, ImdbID: "tt9999999"},
		Ep:     &api.Episode{SeasonNumber: 1, EpisodeNumber: 5},
	}
	recent := map[string]bool{"tt9999999-s01e05": true}
	stats := &api.ScanStats{}

	got := scanning.SkipResumed(item, recent, stats)

	if !got {
		t.Error("scanning.SkipResumed(episode IMDB fallback in recent) = false, want true")
	}
	if stats.EpisodesSkipped != 1 {
		t.Errorf("EpisodesSkipped = %d, want 1", stats.EpisodesSkipped)
	}
}
