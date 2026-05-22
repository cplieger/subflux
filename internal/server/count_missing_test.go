package server

import (
	"context"
	"testing"

	"subflux/internal/api"
)

// countMissingStore returns configurable subtitle files for countMissing tests.
type countMissingStore struct {
	qhMockStore

	episodeFiles []api.SubtitleFileRow
	movieFiles   []api.SubtitleFileRow
}

func (m *countMissingStore) GetSubtitleFiles(_ context.Context, mediaType api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	if mediaType == api.MediaTypeEpisode {
		return m.episodeFiles, nil
	}
	return m.movieFiles, nil
}

// countMissingConfig returns configurable targets for countMissing tests.
type countMissingConfig struct {
	qhMockConfig

	targets []api.SubtitleTarget
}

func (m *countMissingConfig) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return m.targets
}

// --- countMissing ---

func TestCountMissing_empty_series_and_movies(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{}
	db := &countMissingStore{}

	got := countMissing(context.Background(), cfg, db, nil, nil)
	if got != 0 {
		t.Errorf("countMissing(nil, nil) = %d, want 0", got)
	}
}

// --- countMissingSeries ---

func TestCountMissingSeries_empty_series(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{}
	db := &countMissingStore{}

	got := countMissingSeries(context.Background(), cfg, db, nil, nil)
	if got != 0 {
		t.Errorf("countMissingSeries(nil) = %d, want 0", got)
	}
}

func TestCountMissingSeries_series_with_zero_episodes(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 0}},
	}

	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 0 {
		t.Errorf("countMissingSeries(0 episodes) = %d, want 0", got)
	}
}

func TestCountMissingSeries_series_with_nil_statistics(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{}
	series := []api.Series{
		{TvdbID: 81189, Statistics: nil},
	}

	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 0 {
		t.Errorf("countMissingSeries(nil stats) = %d, want 0", got)
	}
}

func TestCountMissingSeries_all_episodes_covered(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard"},
			{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard"},
			{MediaID: "tvdb-81189-s01e03", Language: "fr", Variant: "standard"},
		},
	}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 3}},
	}

	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 0 {
		t.Errorf("countMissingSeries(all covered) = %d, want 0", got)
	}
}

func TestCountMissingSeries_some_episodes_missing(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard"},
		},
	}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 3}},
	}

	// 3 episodes, 1 covered → 2 missing.
	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 2 {
		t.Errorf("countMissingSeries(1 of 3 covered) = %d, want 2", got)
	}
}

func TestCountMissingSeries_multiple_targets(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{
			{Code: "fr"},
			{Code: "en"},
		},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard"},
			{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard"},
		},
	}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 2}},
	}

	// 2 targets × 2 episodes = 4 expected. fr: 2 covered, en: 0 covered → 2 missing.
	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 2 {
		t.Errorf("countMissingSeries(2 targets, partial) = %d, want 2", got)
	}
}

func TestCountMissingSeries_no_targets(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: nil,
	}
	db := &countMissingStore{}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 5}},
	}

	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 0 {
		t.Errorf("countMissingSeries(no targets) = %d, want 0", got)
	}
}

func TestCountMissingSeries_different_series_isolated(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			{MediaID: "tvdb-100-s01e01", Language: "fr", Variant: "standard"},
		},
	}
	series := []api.Series{
		{TvdbID: 100, Statistics: &api.SeriesStatistics{EpisodeFileCount: 1}},
		{TvdbID: 200, Statistics: &api.SeriesStatistics{EpisodeFileCount: 2}},
	}

	// Series 100: 1 ep, 1 covered → 0 missing.
	// Series 200: 2 eps, 0 covered → 2 missing.
	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 2 {
		t.Errorf("countMissingSeries(two series) = %d, want 2", got)
	}
}

func TestCountMissingSeries_variant_matching(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "en", Variant: "hi"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			// Has standard variant, not hi.
			{MediaID: "tvdb-81189-s01e01", Language: "en", Variant: "standard"},
		},
	}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 1}},
	}

	// Target is en/hi, but only en/standard exists → 1 missing.
	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 1 {
		t.Errorf("countMissingSeries(wrong variant) = %d, want 1", got)
	}
}

// --- countMissingMovies ---

func TestCountMissingMovies_empty_movies(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{}
	db := &countMissingStore{}

	got := countMissingMovies(context.Background(), cfg, db, nil, nil)
	if got != 0 {
		t.Errorf("countMissingMovies(nil) = %d, want 0", got)
	}
}

func TestCountMissingMovies_movie_without_file(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: false},
	}

	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 0 {
		t.Errorf("countMissingMovies(no file) = %d, want 0", got)
	}
}

func TestCountMissingMovies_movie_fully_covered(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		movieFiles: []api.SubtitleFileRow{
			{MediaID: "tmdb-1271", Language: "fr", Variant: "standard"},
		},
	}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 0 {
		t.Errorf("countMissingMovies(covered) = %d, want 0", got)
	}
}

func TestCountMissingMovies_movie_missing_subtitle(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 1 {
		t.Errorf("countMissingMovies(missing) = %d, want 1", got)
	}
}

func TestCountMissingMovies_multiple_targets(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{
			{Code: "fr"},
			{Code: "en"},
		},
	}
	db := &countMissingStore{
		movieFiles: []api.SubtitleFileRow{
			{MediaID: "tmdb-1271", Language: "fr", Variant: "standard"},
		},
	}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	// 2 targets, 1 covered → 1 missing.
	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 1 {
		t.Errorf("countMissingMovies(2 targets, 1 covered) = %d, want 1", got)
	}
}

func TestCountMissingMovies_variant_matching(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "en", Variant: "hi"}},
	}
	db := &countMissingStore{
		movieFiles: []api.SubtitleFileRow{
			{MediaID: "tmdb-1271", Language: "en", Variant: "standard"},
		},
	}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	// Target is en/hi, but only en/standard exists → 1 missing.
	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 1 {
		t.Errorf("countMissingMovies(wrong variant) = %d, want 1", got)
	}
}

func TestCountMissingMovies_no_targets(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: nil,
	}
	db := &countMissingStore{}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 0 {
		t.Errorf("countMissingMovies(no targets) = %d, want 0", got)
	}
}

func TestCountMissingMovies_multiple_movies(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		movieFiles: []api.SubtitleFileRow{
			{MediaID: "tmdb-100", Language: "fr", Variant: "standard"},
		},
	}
	movies := []api.Movie{
		{TmdbID: 100, HasFile: true},
		{TmdbID: 200, HasFile: true},
	}

	// Movie 100: covered. Movie 200: missing → 1 missing.
	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 1 {
		t.Errorf("countMissingMovies(2 movies, 1 covered) = %d, want 1", got)
	}
}

// --- countMissingSeries DB error ---

// countMissingErrorStore returns an error from GetSubtitleFiles.
type countMissingErrorStore struct{ qhMockStore }

func (m *countMissingErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return nil, errMock
}

func TestCountMissingSeries_db_error_returns_zero(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingErrorStore{}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 5}},
	}

	got := countMissingSeries(context.Background(), cfg, db, series, nil)
	if got != 0 {
		t.Errorf("countMissingSeries(db error) = %d, want 0", got)
	}
}

func TestCountMissingMovies_db_error_returns_zero(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingErrorStore{}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 0 {
		t.Errorf("countMissingMovies(db error) = %d, want 0", got)
	}
}

// --- countMissing with ignored codecs ---

func TestCountMissingSeries_ignored_codec_counts_as_missing(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			// Only an ignored-codec embedded sub exists.
			{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
		},
	}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 1}},
	}

	// PGS is ignored → episode counts as missing.
	got := countMissingSeries(context.Background(), cfg, db, series, map[string]bool{"pgs": true})
	if got != 1 {
		t.Errorf("countMissingSeries(ignored codec) = %d, want 1", got)
	}
}

func TestCountMissingSeries_usable_sub_not_missing(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		},
	}
	series := []api.Series{
		{TvdbID: 81189, Statistics: &api.SeriesStatistics{EpisodeFileCount: 1}},
	}

	got := countMissingSeries(context.Background(), cfg, db, series, map[string]bool{"pgs": true})
	if got != 0 {
		t.Errorf("countMissingSeries(usable external) = %d, want 0", got)
	}
}

func TestCountMissingMovies_ignored_codec_counts_as_missing(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		movieFiles: []api.SubtitleFileRow{
			{MediaID: "tmdb-1271", Language: "fr", Variant: "standard", Source: "embedded", Codec: "vobsub"},
		},
	}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	// VobSub is ignored → movie counts as missing.
	got := countMissingMovies(context.Background(), cfg, db, movies, map[string]bool{"vobsub": true})
	if got != 1 {
		t.Errorf("countMissingMovies(ignored codec) = %d, want 1", got)
	}
}

func TestCountMissingMovies_usable_overrides_ignored(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		movieFiles: []api.SubtitleFileRow{
			{MediaID: "tmdb-1271", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
			{MediaID: "tmdb-1271", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		},
	}
	movies := []api.Movie{
		{TmdbID: 1271, HasFile: true},
	}

	// Has both ignored PGS and usable external → not missing.
	got := countMissingMovies(context.Background(), cfg, db, movies, map[string]bool{"pgs": true})
	if got != 0 {
		t.Errorf("countMissingMovies(usable overrides ignored) = %d, want 0", got)
	}
}

func TestCountMissingMovies_empty_media_id_skipped(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{}
	// Movie with TmdbID=0 and no ImdbID produces empty mediaID.
	movies := []api.Movie{
		{TmdbID: 0, HasFile: true},
	}
	got := countMissingMovies(context.Background(), cfg, db, movies, nil)
	if got != 0 {
		t.Errorf("countMissingMovies(movie with empty mediaID) = %d, want 0", got)
	}
}

// --- countMissing (combined) ---

func TestCountMissing_sums_series_and_movies(t *testing.T) {
	t.Parallel()
	cfg := &countMissingConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	db := &countMissingStore{
		episodeFiles: []api.SubtitleFileRow{
			{MediaID: "tvdb-100-s01e01", Language: "fr", Variant: "standard"},
		},
		movieFiles: []api.SubtitleFileRow{},
	}
	series := []api.Series{
		{TvdbID: 100, Statistics: &api.SeriesStatistics{EpisodeFileCount: 2}},
	}
	movies := []api.Movie{
		{TmdbID: 200, HasFile: true},
	}

	// Series: 2 eps, 1 covered -> 1 missing. Movies: 1 missing. Total: 2.
	got := countMissing(context.Background(), cfg, db, series, movies)
	if got != 2 {
		t.Errorf("countMissing(1 series missing + 1 movie missing) = %d, want 2", got)
	}
}
