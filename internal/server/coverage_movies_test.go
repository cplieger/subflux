package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- handleCoverageMovies ---

func TestHandleCoverageMovies_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleCoverageMovies(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCoverageMovies_no_radarr_returns_empty(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageMovies() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleCoverageMovies(no radarr) body = %q, want %q", body, "[]")
	}
}

// --- handleCoverageMovies full path tests ---

// coverageMoviesArrClient returns canned movie data for coverage tests.
type coverageMoviesArrClient struct{ dummyArrClient }

func (c coverageMoviesArrClient) GetMovies(_ context.Context) ([]arrapi.Movie, error) {
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
	}, nil
}

// coverageMoviesStore returns subtitle files for coverage movie tests.
type coverageMoviesStore struct{ qhMockStore }

func (m *coverageMoviesStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return []api.SubtitleEntry{
		{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}, nil
}

func TestHandleCoverageMovies_returns_movies_with_coverage(t *testing.T) {
	t.Parallel()
	db := &coverageMoviesStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		radarr: coverageMoviesArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageMovies() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []movieCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Movie without file should be skipped.
	if len(result) != 1 {
		t.Fatalf("handleCoverageMovies() returned %d movies, want 1", len(result))
	}

	m0 := result[0]
	if m0.Title != "Test Movie" {
		t.Errorf("movie title = %q, want %q", m0.Title, "Test Movie")
	}
	if m0.TmdbID != 12345 {
		t.Errorf("movie tmdb_id = %d, want %d", m0.TmdbID, 12345)
	}
	if m0.Path != "/movies/test.mkv" {
		t.Errorf("movie path = %q, want %q", m0.Path, "/movies/test.mkv")
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

// coverageMoviesErrorArrClient returns an error from GetMovies.
type coverageMoviesErrorArrClient struct{ dummyArrClient }

func (c coverageMoviesErrorArrClient) GetMovies(_ context.Context) ([]arrapi.Movie, error) {
	return nil, errMock
}

func TestHandleCoverageMovies_get_movies_error_returns_502(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		radarr: coverageMoviesErrorArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("handleCoverageMovies(GetMovies error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

// coverageMoviesDBErrorStore returns an error from GetSubtitleFiles.
type coverageMoviesDBErrorStore struct{ qhMockStore }

func (m *coverageMoviesDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return nil, errMock
}

func TestHandleCoverageMovies_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &coverageMoviesDBErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{
		cfg:    &qhMockConfig{},
		radarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleCoverageMovies(DB error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// coverageMoviesNilFileArrClient returns a movie with HasFile=true but nil MovieFile.
type coverageMoviesNilFileArrClient struct{ dummyArrClient }

func (c coverageMoviesNilFileArrClient) GetMovies(_ context.Context) ([]arrapi.Movie, error) {
	return []arrapi.Movie{
		{
			ID:      1,
			Title:   "Nil File Movie",
			TmdbID:  77777,
			HasFile: true,
		},
	}, nil
}

func TestHandleCoverageMovies_nil_movie_file_omits_path(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	s := newTestServer(db, cfg)
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		radarr: coverageMoviesNilFileArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []movieCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(result))
	}
	if result[0].Path != "" {
		t.Errorf("path = %q, want empty (nil MovieFile)", result[0].Path)
	}
	if result[0].SceneName != "" {
		t.Errorf("scene_name = %q, want empty (nil MovieFile)", result[0].SceneName)
	}
}
