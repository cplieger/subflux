package coveragehandlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/subflux"
)

// mockCoverageStore implements CoverageStore for testing.
type mockCoverageStore struct {
	err           error
	subtitleFiles []subflux.SubtitleEntry
	scanStates    []subflux.ScanStateRow
}

func (m *mockCoverageStore) SubtitleFiles(_ context.Context, _ subflux.MediaType, _ string) ([]subflux.SubtitleEntry, error) {
	return m.subtitleFiles, m.err
}

func (m *mockCoverageStore) ScanStates(_ context.Context, _ subflux.MediaType, _ string) ([]subflux.ScanStateRow, error) {
	return m.scanStates, m.err
}

func TestHandleCoverage(t *testing.T) {
	t.Parallel()

	t.Run("series_nil_sonarr_returns_empty_array", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store: &mockCoverageStore{},
			StateFunc: func() *LiveState {
				return &LiveState{Sonarr: nil}
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageSeries(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if body := w.Body.String(); body != "[]" && body != "[]\n" {
			t.Errorf("body = %q, want empty array", body)
		}
	})

	t.Run("movies_nil_radarr_returns_empty_array", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store: &mockCoverageStore{},
			StateFunc: func() *LiveState {
				return &LiveState{Radarr: nil}
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageMovies(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if body := w.Body.String(); body != "[]" && body != "[]\n" {
			t.Errorf("body = %q, want empty array", body)
		}
	})

	t.Run("series_POST_rejected", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store:     &mockCoverageStore{},
			StateFunc: func() *LiveState { return &LiveState{} },
		})
		req := httptest.NewRequest(http.MethodPost, "/api/coverage/series", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageSeries(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	t.Run("movies_POST_rejected", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store:     &mockCoverageStore{},
			StateFunc: func() *LiveState { return &LiveState{} },
		})
		req := httptest.NewRequest(http.MethodPost, "/api/coverage/movies", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageMovies(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})
}

// --- Migrated handler tests (previously drove the server-root delegates) ---

var errMock = errors.New("mock error")

// trackingCoverageStore records the params passed to ScanStates.
type trackingCoverageStore struct {
	mockCoverageStore
	lastType   subflux.MediaType
	lastPrefix string
}

func (m *trackingCoverageStore) ScanStates(_ context.Context, mediaType subflux.MediaType, prefix string) ([]subflux.ScanStateRow, error) {
	m.lastType = mediaType
	m.lastPrefix = prefix
	return m.scanStates, m.err
}

// covSonarrFake implements CoverageSonarrClient with canned series.
type covSonarrFake struct {
	err    error
	series []arrapi.Series
}

func (f *covSonarrFake) Series(_ context.Context) ([]arrapi.Series, error) {
	return f.series, f.err
}

func (f *covSonarrFake) SeriesByTvdbID(_ context.Context, tvdbID int) (arrapi.Series, bool, error) {
	if f.err != nil {
		return arrapi.Series{}, false, f.err
	}
	for i := range f.series {
		if f.series[i].TvdbID == tvdbID {
			return f.series[i], true, nil
		}
	}
	return arrapi.Series{}, false, nil
}

func (f *covSonarrFake) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	return nil
}

func (f *covSonarrFake) ResolveExcludeTagIDsErr(_ context.Context, _ []string, _ bool) (map[int]struct{}, error) {
	return nil, nil
}

// covRadarrFake implements CoverageRadarrClient with canned movies.
type covRadarrFake struct {
	err    error
	movies []arrapi.Movie
}

func (f *covRadarrFake) Movies(_ context.Context) ([]arrapi.Movie, error) {
	return f.movies, f.err
}

func (f *covRadarrFake) MovieByTmdbID(_ context.Context, tmdbID int) (arrapi.Movie, bool, error) {
	if f.err != nil {
		return arrapi.Movie{}, false, f.err
	}
	for i := range f.movies {
		if f.movies[i].TmdbID == tmdbID {
			return f.movies[i], true, nil
		}
	}
	return arrapi.Movie{}, false, nil
}

func (f *covRadarrFake) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	return nil
}

func (f *covRadarrFake) ResolveExcludeTagIDsErr(_ context.Context, _ []string, _ bool) (map[int]struct{}, error) {
	return nil, nil
}

// newCoverageHandler builds a Handler around the given store, config, and
// arr clients.
func newCoverageHandler(store CoverageStore, cfg *fakeCoverageCfg, sonarr CoverageSonarrClient, radarr CoverageRadarrClient) *Handler {
	return NewHandler(Deps{
		Store: store,
		StateFunc: func() *LiveState {
			return &LiveState{Cfg: cfg, Sonarr: sonarr, Radarr: radarr}
		},
	})
}

// --- HandleCoverageDetail ---

func TestHandleCoverageDetail(t *testing.T) {
	t.Parallel()

	t.Run("rejects_non_get", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/coverage/series/123", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("HandleCoverageDetail(POST) status = %d, want %d",
				rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("missing_tvdb_id", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		// Path without a tvdb ID segment.
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleCoverageDetail(missing id) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("invalid_tvdb_id", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/abc", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleCoverageDetail(non-numeric id) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("negative_tvdb_id", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/-5", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleCoverageDetail(negative id) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("zero_tvdb_id", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		// Zero is not a tvdb ID any series can have, so it is refused with
		// the negatives rather than sent to the store as a prefix.
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/0", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleCoverageDetail(zero id) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("valid_tvdb_id_returns_files", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/81189", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleCoverageDetail(valid id) status = %d, want %d",
				rec.Code, http.StatusOK)
		}
		// With empty DB, should return null (no files).
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{err: errMock}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/81189", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("HandleCoverageDetail(db error) status = %d, want %d",
				rec.Code, http.StatusInternalServerError)
		}
	})
}

// --- HandleScanStates ---

func TestHandleScanStates(t *testing.T) {
	t.Parallel()

	t.Run("rejects_non_get", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/coverage/scan-state", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("HandleScanStates(POST) status = %d, want %d",
				rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("defaults_to_episode", func(t *testing.T) {
		t.Parallel()
		store := &trackingCoverageStore{}
		h := newCoverageHandler(store, &fakeCoverageCfg{}, nil, nil)
		// No type param — should default to "episode".
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/scan-state", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("HandleScanStates() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if store.lastType != "episode" {
			t.Errorf("ScanStates mediaType = %q, want %q", store.lastType, "episode")
		}
	})

	t.Run("passes_type_and_prefix", func(t *testing.T) {
		t.Parallel()
		store := &trackingCoverageStore{}
		h := newCoverageHandler(store, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet,
			"/api/coverage/scan-state?type=movie&prefix=tmdb-123-", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("HandleScanStates() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if store.lastType != "movie" {
			t.Errorf("ScanStates mediaType = %q, want %q", store.lastType, "movie")
		}
		if store.lastPrefix != "tmdb-123-" {
			t.Errorf("ScanStates prefix = %q, want %q", store.lastPrefix, "tmdb-123-")
		}
	})

	t.Run("invalid_type_returns_400", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&trackingCoverageStore{}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/scan-state?type=foo", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleScanStates(type=foo) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{err: errMock}, &fakeCoverageCfg{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/scan-state", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("HandleScanStates(db error) status = %d, want %d",
				rec.Code, http.StatusInternalServerError)
		}
	})
}

// --- HandleCoverageSeries full paths ---

// coverageSeriesFixture returns the canned two-series inventory: one series
// with three episode files, one without any.
func coverageSeriesFixture() []arrapi.Series {
	return []arrapi.Series{
		{
			ID:               1,
			Title:            "Test Show",
			Year:             2024,
			TvdbID:           81189,
			ImdbID:           "tt1234567",
			FirstAired:       "2024-01-01",
			OriginalLanguage: &arrapi.Language{Name: "English"},
			Statistics:       &arrapi.SeriesStatistics{EpisodeFileCount: 3},
			Tags:             []int{1},
		},
		{
			ID:         2,
			Title:      "No Episodes",
			TvdbID:     99999,
			Statistics: &arrapi.SeriesStatistics{EpisodeFileCount: 0},
		},
	}
}

func TestHandleCoverageSeries_returns_series_with_coverage(t *testing.T) {
	t.Parallel()
	store := &mockCoverageStore{subtitleFiles: []subflux.SubtitleEntry{
		{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
	}}
	cfg := &fakeCoverageCfg{
		targets: []subflux.SubtitleTarget{{Code: "fr"}},
		// The typed embedded policy (embedded_subtitles section) is the
		// server-side source for have_ignored badges: this pins the handler
		// consumer of the ONE policy resolver, not only the engine.
		embedded: subflux.EmbeddedPolicy{IgnorePGS: true},
	}
	h := newCoverageHandler(store, cfg, &covSonarrFake{series: coverageSeriesFixture()}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleCoverageSeries() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []SeriesItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Series with 0 episodes should be skipped.
	if len(result) != 1 {
		t.Fatalf("HandleCoverageSeries() returned %d series, want 1", len(result))
	}

	s0 := result[0]
	if s0.Title != "Test Show" {
		t.Errorf("series title = %q, want %q", s0.Title, "Test Show")
	}
	if s0.TvdbID != 81189 {
		t.Errorf("series tvdb_id = %d, want %d", s0.TvdbID, 81189)
	}
	if s0.Episodes != 3 {
		t.Errorf("series episodes = %d, want %d", s0.Episodes, 3)
	}
	if s0.AudioLang != "en" {
		t.Errorf("series audio_lang = %q, want %q", s0.AudioLang, "en")
	}
	if s0.Rule != "en" {
		t.Errorf("series rule = %q, want %q", s0.Rule, "en")
	}

	if len(s0.Targets) != 1 {
		t.Fatalf("series targets count = %d, want 1", len(s0.Targets))
	}
	tc := s0.Targets[0]
	if tc.Language != "fr" {
		t.Errorf("target language = %q, want %q", tc.Language, "fr")
	}
	if tc.Have != 1 {
		t.Errorf("target have = %d, want 1 (one external srt)", tc.Have)
	}
	if tc.HaveIgnored != 1 {
		t.Errorf("target have_ignored = %d, want 1 (one ignored pgs)", tc.HaveIgnored)
	}
	if tc.Total != 3 {
		t.Errorf("target total = %d, want 3", tc.Total)
	}
}

func TestHandleCoverageSeries_get_series_error_returns_502(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{},
		&covSonarrFake{err: errMock}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("HandleCoverageSeries(Series error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

// seriesDBErrorStore fails SubtitleFiles but not Series, so the
// coverage fetch surfaces the store error as a 500 (vs the arr 502).
type seriesDBErrorStore struct{ mockCoverageStore }

func (m *seriesDBErrorStore) SubtitleFiles(_ context.Context, _ subflux.MediaType, _ string) ([]subflux.SubtitleEntry, error) {
	return nil, errMock
}

func TestHandleCoverageSeries_db_error_returns_500(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&seriesDBErrorStore{}, &fakeCoverageCfg{},
		&covSonarrFake{series: coverageSeriesFixture()}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleCoverageSeries(DB error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleCoverageSeries_no_targets_sets_rule_no_targets(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{},
		&covSonarrFake{series: coverageSeriesFixture()}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleCoverageSeries() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []SeriesItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result))
	}
	if result[0].Rule != coverage.RuleNoTargets {
		t.Errorf("series rule = %q, want %q", result[0].Rule, coverage.RuleNoTargets)
	}
}

func TestHandleCoverageSeries_no_original_language_uses_default_rule(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&mockCoverageStore{},
		&fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "fr"}}},
		&covSonarrFake{series: []arrapi.Series{{
			ID:         1,
			Title:      "No Lang Show",
			TvdbID:     55555,
			Statistics: &arrapi.SeriesStatistics{EpisodeFileCount: 2},
		}}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []SeriesItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result))
	}
	if result[0].Rule != "default" {
		t.Errorf("rule = %q, want %q", result[0].Rule, "default")
	}
}

// --- HandleCoverageMovies full paths ---

// coverageMoviesFixture returns the canned two-movie inventory: one with a
// file, one without.
func coverageMoviesFixture() []arrapi.Movie {
	return []arrapi.Movie{
		{
			ID:               1,
			Title:            "Test Movie",
			Year:             2024,
			TmdbID:           12345,
			ImdbID:           "tt9876543",
			InCinemas:        "2024-06-01",
			DigitalRelease:   "2024-09-01",
			HasFile:          true,
			OriginalLanguage: &arrapi.Language{Name: "English"},
			MovieFile:        &arrapi.MovieFile{Path: "/movies/test.mkv", SceneName: "Test.Movie.2024"},
			Tags:             []int{2},
		},
		{
			ID:      2,
			Title:   "No File Movie",
			TmdbID:  99999,
			HasFile: false,
		},
	}
}

func TestHandleCoverageMovies_returns_movies_with_coverage(t *testing.T) {
	t.Parallel()
	store := &mockCoverageStore{subtitleFiles: []subflux.SubtitleEntry{
		{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}}
	cfg := &fakeCoverageCfg{
		targets: []subflux.SubtitleTarget{{Code: "fr"}},
	}
	h := newCoverageHandler(store, cfg, nil, &covRadarrFake{movies: coverageMoviesFixture()})

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleCoverageMovies() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []MovieItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Movie without file should be skipped.
	if len(result) != 1 {
		t.Fatalf("HandleCoverageMovies() returned %d movies, want 1", len(result))
	}

	m0 := result[0]
	if m0.Title != "Test Movie" {
		t.Errorf("movie title = %q, want %q", m0.Title, "Test Movie")
	}
	if m0.TmdbID != 12345 {
		t.Errorf("movie tmdb_id = %d, want %d", m0.TmdbID, 12345)
	}
	if m0.SceneName != "Test.Movie.2024" {
		t.Errorf("movie scene_name = %q, want %q", m0.SceneName, "Test.Movie.2024")
	}
	if m0.AudioLang != "en" {
		t.Errorf("movie audio_lang = %q, want %q", m0.AudioLang, "en")
	}
	if !m0.HasFile {
		t.Error("movie has_file should be true")
	}

	if len(m0.Targets) != 1 {
		t.Fatalf("movie targets count = %d, want 1", len(m0.Targets))
	}
	tc := m0.Targets[0]
	if tc.Have != 1 {
		t.Errorf("target have = %d, want 1", tc.Have)
	}
	if tc.Total != 1 {
		t.Errorf("target total = %d, want 1", tc.Total)
	}
}

func TestHandleCoverageMovies_get_movies_error_returns_502(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&mockCoverageStore{}, &fakeCoverageCfg{},
		nil, &covRadarrFake{err: errMock})

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("HandleCoverageMovies(Movies error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

func TestHandleCoverageMovies_db_error_returns_500(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&seriesDBErrorStore{}, &fakeCoverageCfg{},
		nil, &covRadarrFake{movies: coverageMoviesFixture()})

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleCoverageMovies(DB error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleCoverageMovies_nil_movie_file_omits_path(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&mockCoverageStore{},
		&fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "fr"}}},
		nil, &covRadarrFake{movies: []arrapi.Movie{{
			ID:      1,
			Title:   "Nil File Movie",
			TmdbID:  77777,
			HasFile: true,
		}}})

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []MovieItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(result))
	}
	if result[0].SceneName != "" {
		t.Errorf("scene_name = %q, want empty (nil MovieFile)", result[0].SceneName)
	}
}

// --- The movies wire cut (A3): no inlined rows, positive canonical ids ---

func TestHandleCoverageMovies_serializes_no_subs_key(t *testing.T) {
	t.Parallel()
	// The store HOLDS rows for the movie; the collection must still serialize
	// none of them — a decode-level pin, so a re-added field under any name
	// tag ("subs") fails here whatever the Go struct looks like.
	store := &mockCoverageStore{subtitleFiles: []subflux.SubtitleEntry{
		{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}}
	cfg := &fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "fr"}}}
	h := newCoverageHandler(store, cfg, nil, &covRadarrFake{movies: coverageMoviesFixture()})

	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("collection serialized no rows; the no-subs assertion would be vacuous")
	}
	for i, row := range raw {
		if _, ok := row["subs"]; ok {
			t.Errorf("row %d carries a subs key; A3 removed inlined subtitle rows from the collection wire", i)
		}
	}
}

func TestHandleCoverageSeries_drops_rows_without_positive_canonical_id(t *testing.T) {
	t.Parallel()
	series := []arrapi.Series{
		{ID: 1, Title: "Keyed", TvdbID: 81189, Statistics: &arrapi.SeriesStatistics{EpisodeFileCount: 3}},
		// No TVDB id: the client keys rows "tvdb-{id}", so every such row
		// would collide onto "tvdb-0" (the key-collision class A3 closes) —
		// the imdb id does not rescue it.
		{ID: 2, Title: "Zero Id", TvdbID: 0, ImdbID: "tt0000001", Statistics: &arrapi.SeriesStatistics{EpisodeFileCount: 5}},
	}
	h := newCoverageHandler(&mockCoverageStore{},
		&fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "fr"}}},
		&covSonarrFake{series: series}, nil)

	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var result []SeriesItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 || result[0].TvdbID != 81189 {
		t.Errorf("collection = %+v, want only the tvdb-81189 row (zero-id rows dropped)", result)
	}
}

func TestHandleCoverageMovies_drops_rows_without_positive_canonical_id(t *testing.T) {
	t.Parallel()
	movies := []arrapi.Movie{
		{ID: 1, Title: "Keyed", TmdbID: 12345, HasFile: true},
		// No TMDB id: even with the imdb fallback available the row has no
		// positive canonical id — the client would key it "tmdb-0" and the
		// summary route could never address it.
		{ID: 2, Title: "Imdb Only", TmdbID: 0, ImdbID: "tt0000002", HasFile: true},
	}
	h := newCoverageHandler(&mockCoverageStore{},
		&fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "fr"}}},
		nil, &covRadarrFake{movies: movies})

	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var result []MovieItem
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 || result[0].TmdbID != 12345 {
		t.Errorf("collection = %+v, want only the tmdb-12345 row (zero-id rows dropped)", result)
	}
}

// TestHandleCoverageMovies_payload_size_evidence records the collection
// payload sizes at a realistic library scale, against a reconstruction of the
// pre-A3 wire (the same rows with their subtitle entries inlined, the shape
// HandleCoverageMovies serialized before the cut). EVIDENCE for the ≥60%
// size-reduction hypothesis, not a gate: the numbers land in the test log;
// the only assertion is that the cut payload stayed smaller.
func TestHandleCoverageMovies_payload_size_evidence(t *testing.T) {
	t.Parallel()
	const movieCount = 1000

	movies := make([]arrapi.Movie, 0, movieCount)
	var files []subflux.SubtitleEntry
	for i := range movieCount {
		tmdb := 10000 + i
		movies = append(movies, arrapi.Movie{
			ID:               i + 1,
			Title:            fmt.Sprintf("Deterministic Movie %04d: The Reckoning", i),
			Year:             1970 + i%55,
			TmdbID:           tmdb,
			ImdbID:           fmt.Sprintf("tt%07d", 1000000+i),
			InCinemas:        "2024-06-01",
			DigitalRelease:   "2024-09-01",
			HasFile:          true,
			OriginalLanguage: &arrapi.Language{Name: "English"},
			MovieFile: &arrapi.MovieFile{
				Path:      fmt.Sprintf("/movies/Deterministic Movie %04d (2024)/movie.mkv", i),
				SceneName: fmt.Sprintf("Deterministic.Movie.%04d.2024.1080p.BluRay.x264-GROUP", i),
			},
			Tags: []int{1, 3},
		})
		// Subtitle rows per movie, deterministic by index: one external per
		// covered target (en always, fr for every other movie) plus 0-7
		// embedded tracks — coverage indexes EVERY embedded subtitle track
		// (text and bitmap alike), and multi-language embeds dominate real
		// libraries. Average ≈ 5 rows per movie.
		mediaID := "tmdb-" + strconv.Itoa(tmdb)
		files = append(files, subflux.SubtitleEntry{
			MediaID: mediaID, Language: "en", Variant: "standard",
			Source: "opensubtitles", Codec: "srt", Score: 80 + i%20, OffsetMs: int64(i % 500),
		})
		if i%2 == 0 {
			files = append(files, subflux.SubtitleEntry{
				MediaID: mediaID, Language: "fr", Variant: "standard",
				Source: "subdl", Codec: "srt", Score: 60 + i%30, Ordinal: 1,
			})
		}
		embLangs := [...]string{"en", "fr", "de", "es", "ja", "pt", "it"}
		embCodecs := [...]string{"pgs", "ass", "srt"}
		for e := range i % 8 {
			files = append(files, subflux.SubtitleEntry{
				MediaID: mediaID, Language: embLangs[e%len(embLangs)], Variant: "standard",
				Source: "embedded", Codec: embCodecs[e%len(embCodecs)],
			})
		}
	}

	store := &mockCoverageStore{subtitleFiles: files}
	cfg := &fakeCoverageCfg{targets: []subflux.SubtitleTarget{{Code: "en"}, {Code: "fr"}}}
	h := newCoverageHandler(store, cfg, nil, &covRadarrFake{movies: movies})

	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	after := rec.Body.Bytes()

	// The pre-cut wire: each row plus its deduplicated inlined entries, the
	// exact attach the collection performed before A3.
	var items []MovieItem
	if err := json.Unmarshal(after, &items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	byMedia := make(map[string][]subflux.SubtitleEntry)
	for i := range files {
		byMedia[files[i].MediaID] = append(byMedia[files[i].MediaID], files[i])
	}
	type preCutMovieItem struct {
		MovieItem
		Subs []subflux.SubtitleEntry `json:"subs"`
	}
	preCut := make([]preCutMovieItem, 0, len(items))
	for _, item := range items {
		preCut = append(preCut, preCutMovieItem{
			MovieItem: item,
			Subs:      coverage.DeduplicateFileRows(byMedia["tmdb-"+strconv.Itoa(item.TmdbID)]),
		})
	}
	before, err := json.Marshal(preCut)
	if err != nil {
		t.Fatalf("marshal pre-cut payload: %v", err)
	}

	gzipSize := func(b []byte) int {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, werr := zw.Write(b); werr != nil {
			t.Fatalf("gzip write: %v", werr)
		}
		if cerr := zw.Close(); cerr != nil {
			t.Fatalf("gzip close: %v", cerr)
		}
		return buf.Len()
	}
	beforeGz, afterGz := gzipSize(before), gzipSize(after)
	pct := func(b, a int) float64 { return 100 * (1 - float64(a)/float64(b)) }
	t.Logf("movies collection payload at %d movies (%d subtitle rows):", movieCount, len(files))
	t.Logf("  raw:  %d -> %d bytes (-%.1f%%)", len(before), len(after), pct(len(before), len(after)))
	t.Logf("  gzip: %d -> %d bytes (-%.1f%%)", beforeGz, afterGz, pct(beforeGz, afterGz))

	if len(after) >= len(before) {
		t.Errorf("cut payload (%d bytes) is not smaller than the pre-cut payload (%d bytes)",
			len(after), len(before))
	}
}
