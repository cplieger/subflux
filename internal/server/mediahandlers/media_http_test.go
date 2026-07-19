package mediahandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/arrapi"
)

var errMock = errors.New("mock error")

// fakeSonarr implements MediaSonarrClient with canned data.
type fakeSonarr struct {
	err      error
	series   []arrapi.Series
	episodes []arrapi.Episode
}

func (f *fakeSonarr) GetSeries(_ context.Context) ([]arrapi.Series, error) {
	return f.series, f.err
}

func (f *fakeSonarr) GetEpisodes(_ context.Context, _ int) ([]arrapi.Episode, error) {
	return f.episodes, f.err
}

// fakeRadarr implements MediaRadarrClient with canned data.
type fakeRadarr struct {
	err    error
	movies []arrapi.Movie
}

func (f *fakeRadarr) GetMovies(_ context.Context) ([]arrapi.Movie, error) {
	return f.movies, f.err
}

// newMediaHandler builds a Handler around the given arr clients.
func newMediaHandler(sonarr MediaSonarrClient, radarr MediaRadarrClient) *Handler {
	return NewHandler(Deps{
		StateFunc: func() *LiveState {
			return &LiveState{Sonarr: sonarr, Radarr: radarr}
		},
		ServerCtx: context.Background,
	})
}

func TestHandleMediaSeries_no_sonarr_returns_empty(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/media/series", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaSeries(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "[]\n" {
		t.Errorf("body = %q, want empty array", rec.Body.String())
	}
}

func TestHandleMediaMovies_no_radarr_returns_empty(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/media/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaMovies(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "[]\n" {
		t.Errorf("body = %q, want empty array", rec.Body.String())
	}
}

func TestHandleMediaEpisodes_no_sonarr(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/media/series/1/episodes", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaEpisodes(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- Series tests ---

func TestHandleMediaSeries_returns_series_with_statistics(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(&fakeSonarr{series: []arrapi.Series{
		{
			ID:     1,
			Title:  "Breaking Bad",
			Year:   2008,
			TvdbID: 81189,
			ImdbID: "tt0903747",
			Statistics: &arrapi.SeriesStatistics{
				EpisodeFileCount: 62,
				SeasonCount:      5,
			},
		},
		{
			ID:     2,
			Title:  "The Wire",
			Year:   2002,
			TvdbID: 79126,
			ImdbID: "",
		},
	}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/media/series", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []SeriesItem
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	if got[0].Title != "Breaking Bad" {
		t.Errorf("[0].Title = %q, want %q", got[0].Title, "Breaking Bad")
	}
	if got[0].Episodes != 62 {
		t.Errorf("[0].Episodes = %d, want 62", got[0].Episodes)
	}
	if got[0].Seasons != 5 {
		t.Errorf("[0].Seasons = %d, want 5", got[0].Seasons)
	}
	if got[0].ImdbID != "tt0903747" {
		t.Errorf("[0].ImdbID = %q, want %q", got[0].ImdbID, "tt0903747")
	}

	if got[1].Title != "The Wire" {
		t.Errorf("[1].Title = %q, want %q", got[1].Title, "The Wire")
	}
	if got[1].Episodes != 0 {
		t.Errorf("[1].Episodes = %d, want 0", got[1].Episodes)
	}
	if got[1].Seasons != 0 {
		t.Errorf("[1].Seasons = %d, want 0", got[1].Seasons)
	}
}

func TestHandleMediaSeries_api_error_returns_502(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(&fakeSonarr{err: errMock}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/media/series", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaSeries(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

// --- Movies tests ---

func TestHandleMediaMovies_returns_movies_with_file_info(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(nil, &fakeRadarr{movies: []arrapi.Movie{
		{
			ID:      10,
			Title:   "Inception",
			Year:    2010,
			TmdbID:  27205,
			ImdbID:  "tt1375666",
			HasFile: true,
			MovieFile: &arrapi.MovieFile{
				Path:      "/movies/Inception (2010)/Inception.mkv",
				SceneName: "Inception.2010.1080p.BluRay",
			},
		},
		{
			ID:      20,
			Title:   "Dune",
			Year:    2021,
			TmdbID:  438631,
			HasFile: false,
		},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/media/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []MovieItem
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	if got[0].Title != "Inception" {
		t.Errorf("[0].Title = %q, want %q", got[0].Title, "Inception")
	}
	if !got[0].HasFile {
		t.Errorf("[0].HasFile = false, want true")
	}
	if got[0].SceneName != "Inception.2010.1080p.BluRay" {
		t.Errorf("[0].SceneName = %q, want %q",
			got[0].SceneName, "Inception.2010.1080p.BluRay")
	}

	if got[1].Title != "Dune" {
		t.Errorf("[1].Title = %q, want %q", got[1].Title, "Dune")
	}
	if got[1].HasFile {
		t.Errorf("[1].HasFile = true, want false")
	}
	if got[1].SceneName != "" {
		t.Errorf("[1].SceneName = %q, want empty", got[1].SceneName)
	}
}

func TestHandleMediaMovies_api_error_returns_502(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(nil, &fakeRadarr{err: errMock})

	req := httptest.NewRequest(http.MethodGet, "/api/media/movies", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaMovies(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

// --- Episodes tests ---

func TestHandleMediaEpisodes_returns_grouped_sorted_episodes(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(&fakeSonarr{episodes: []arrapi.Episode{
		{
			ID:            101,
			Title:         "Pilot",
			SeasonNumber:  1,
			EpisodeNumber: 1,
			HasFile:       true,
			EpisodeFile: &arrapi.EpisodeFile{
				Path:      "/tv/Show/S01E01.mkv",
				SceneName: "Show.S01E01.720p",
			},
		},
		{
			ID:                    103,
			Title:                 "Episode 3",
			SeasonNumber:          2,
			EpisodeNumber:         1,
			SceneSeasonNumber:     2,
			SceneEpisodeNumber:    1,
			AbsoluteEpisodeNumber: 11,
			HasFile:               false,
		},
		{
			ID:            102,
			Title:         "Cat's in the Bag",
			SeasonNumber:  1,
			EpisodeNumber: 2,
			HasFile:       true,
			EpisodeFile: &arrapi.EpisodeFile{
				Path:      "/tv/Show/S01E02.mkv",
				SceneName: "Show.S01E02.720p",
			},
		},
	}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/media/series/1/episodes", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaEpisodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []SeasonGroup
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("season count = %d, want 2", len(got))
	}
	if got[0].Season != 1 {
		t.Errorf("[0].Season = %d, want 1", got[0].Season)
	}
	if got[1].Season != 2 {
		t.Errorf("[1].Season = %d, want 2", got[1].Season)
	}

	// Season 1: 2 episodes, sorted by episode number.
	if len(got[0].Episodes) != 2 {
		t.Fatalf("S1 episode count = %d, want 2", len(got[0].Episodes))
	}
	if got[0].Episodes[0].EpisodeNumber != 1 {
		t.Errorf("S1E[0].EpisodeNumber = %d, want 1",
			got[0].Episodes[0].EpisodeNumber)
	}
	if got[0].Episodes[0].Title != "Pilot" {
		t.Errorf("S1E[0].Title = %q, want %q",
			got[0].Episodes[0].Title, "Pilot")
	}
	if got[0].Episodes[1].EpisodeNumber != 2 {
		t.Errorf("S1E[1].EpisodeNumber = %d, want 2",
			got[0].Episodes[1].EpisodeNumber)
	}

	// Season 2: 1 episode, no file.
	if len(got[1].Episodes) != 1 {
		t.Fatalf("S2 episode count = %d, want 1", len(got[1].Episodes))
	}
	if got[1].Episodes[0].AbsoluteEpisodeNumber != 11 {
		t.Errorf("S2E[0].AbsoluteEpisodeNumber = %d, want 11",
			got[1].Episodes[0].AbsoluteEpisodeNumber)
	}
	if got[1].Episodes[0].HasFile {
		t.Errorf("S2E[0].HasFile = true, want false")
	}
}

func TestHandleMediaEpisodes_api_error_returns_502(t *testing.T) {
	t.Parallel()
	h := newMediaHandler(&fakeSonarr{err: errMock}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/media/series/1/episodes", nil)
	rec := httptest.NewRecorder()
	h.HandleMediaEpisodes(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

// --- Episodes path/id validation (sonarr configured) ---

func TestHandleMediaEpisodes_invalid_series_id_returns_400(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{"missing_id", "/api/media/series//episodes"},
		{"non_numeric_id", "/api/media/series/abc/episodes"},
		{"negative_id", "/api/media/series/-1/episodes"},
		{"zero_id", "/api/media/series/0/episodes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Sonarr must be non-nil to reach the ID extraction branch.
			h := newMediaHandler(&fakeSonarr{}, nil)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			h.HandleMediaEpisodes(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("HandleMediaEpisodes(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}
