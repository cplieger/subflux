package coveragehandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/testsupport"
)

// mockCoverageStore implements CoverageStore for testing.
type mockCoverageStore struct {
	err           error
	subtitleFiles []api.SubtitleEntry
	scanStates    []api.ScanStateRow
}

func (m *mockCoverageStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return m.subtitleFiles, m.err
}

func (m *mockCoverageStore) GetScanStates(_ context.Context, _ api.MediaType, _ string) ([]api.ScanStateRow, error) {
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

// trackingCoverageStore records the params passed to GetScanStates.
type trackingCoverageStore struct {
	mockCoverageStore
	lastType   api.MediaType
	lastPrefix string
}

func (m *trackingCoverageStore) GetScanStates(_ context.Context, mediaType api.MediaType, prefix string) ([]api.ScanStateRow, error) {
	m.lastType = mediaType
	m.lastPrefix = prefix
	return m.scanStates, m.err
}

// covSonarrFake implements CoverageSonarrClient with canned series.
type covSonarrFake struct {
	err    error
	series []arrapi.Series
}

func (f *covSonarrFake) GetSeries(_ context.Context) ([]arrapi.Series, error) {
	return f.series, f.err
}

func (f *covSonarrFake) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	return nil
}

// covRadarrFake implements CoverageRadarrClient with canned movies.
type covRadarrFake struct {
	err    error
	movies []arrapi.Movie
}

func (f *covRadarrFake) GetMovies(_ context.Context) ([]arrapi.Movie, error) {
	return f.movies, f.err
}

func (f *covRadarrFake) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	return nil
}

// newCoverageHandler builds a Handler around the given store, config, and
// arr clients.
func newCoverageHandler(store CoverageStore, cfg *testsupport.NopConfig, sonarr CoverageSonarrClient, radarr CoverageRadarrClient) *Handler {
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
		h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series/-5", nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageDetail(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleCoverageDetail(negative id) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("valid_tvdb_id_returns_files", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(&mockCoverageStore{err: errMock}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(store, &testsupport.NopConfig{}, nil, nil)
		// No type param — should default to "episode".
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/scan-state", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleScanStates() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if store.lastType != "episode" {
			t.Errorf("GetScanStates mediaType = %q, want %q", store.lastType, "episode")
		}
	})

	t.Run("passes_type_and_prefix", func(t *testing.T) {
		t.Parallel()
		store := &trackingCoverageStore{}
		h := newCoverageHandler(store, &testsupport.NopConfig{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet,
			"/api/coverage/scan-state?type=movie&prefix=tmdb-123-", nil)
		rec := httptest.NewRecorder()
		h.HandleScanStates(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleScanStates() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if store.lastType != "movie" {
			t.Errorf("GetScanStates mediaType = %q, want %q", store.lastType, "movie")
		}
		if store.lastPrefix != "tmdb-123-" {
			t.Errorf("GetScanStates prefix = %q, want %q", store.lastPrefix, "tmdb-123-")
		}
	})

	t.Run("invalid_type_returns_400", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&trackingCoverageStore{}, &testsupport.NopConfig{}, nil, nil)
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
		h := newCoverageHandler(&mockCoverageStore{err: errMock}, &testsupport.NopConfig{}, nil, nil)
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
	store := &mockCoverageStore{subtitleFiles: []api.SubtitleEntry{
		{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
	}}
	cfg := &testsupport.NopConfig{
		Targets: []api.SubtitleTarget{{Code: "fr"}},
		// The typed embedded policy (embedded_subtitles section) is the
		// server-side source for have_ignored badges: this pins the handler
		// consumer of the ONE policy resolver, not only the engine.
		Embedded: api.EmbeddedPolicy{IgnorePGS: true},
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
	h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{},
		&covSonarrFake{err: errMock}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeries(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("HandleCoverageSeries(GetSeries error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

// seriesDBErrorStore fails GetSubtitleFiles but not GetSeries, so the
// coverage fetch surfaces the store error as a 500 (vs the arr 502).
type seriesDBErrorStore struct{ mockCoverageStore }

func (m *seriesDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return nil, errMock
}

func TestHandleCoverageSeries_db_error_returns_500(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&seriesDBErrorStore{}, &testsupport.NopConfig{},
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
	h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{},
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
		&testsupport.NopConfig{Targets: []api.SubtitleTarget{{Code: "fr"}}},
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
	store := &mockCoverageStore{subtitleFiles: []api.SubtitleEntry{
		{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}}
	cfg := &testsupport.NopConfig{
		Targets: []api.SubtitleTarget{{Code: "fr"}},
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
	h := newCoverageHandler(&mockCoverageStore{}, &testsupport.NopConfig{},
		nil, &covRadarrFake{err: errMock})

	req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageMovies(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("HandleCoverageMovies(GetMovies error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

func TestHandleCoverageMovies_db_error_returns_500(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&seriesDBErrorStore{}, &testsupport.NopConfig{},
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
		&testsupport.NopConfig{Targets: []api.SubtitleTarget{{Code: "fr"}}},
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
