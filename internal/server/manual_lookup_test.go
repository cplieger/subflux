package server

import (
	"context"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- lookupMediaTitle ---

// movieTitleArrClient returns a movie with a known title.
type movieTitleArrClient struct{ dummyArrClient }

func (movieTitleArrClient) GetMovieByID(_ context.Context, _ int) (arrapi.Movie, error) {
	return arrapi.Movie{Title: "Inception", TmdbID: 27205}, nil
}

// seriesTitleArrClient returns a series with a known title.
type seriesTitleArrClient struct{ dummyArrClient }

func (seriesTitleArrClient) GetSeriesByID(_ context.Context, _ int) (arrapi.Series, error) {
	return arrapi.Series{Title: "Breaking Bad", TvdbID: 81189}, nil
}

// arrErrorClient returns errors from GetMovieByID and GetSeriesByID.
type arrErrorClient struct{ dummyArrClient }

func (arrErrorClient) GetMovieByID(_ context.Context, _ int) (arrapi.Movie, error) {
	return arrapi.Movie{}, errMock
}

func (arrErrorClient) GetSeriesByID(_ context.Context, _ int) (arrapi.Series, error) {
	return arrapi.Series{}, errMock
}

func TestLookupMediaTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		radarr    api.RadarrClient
		sonarr    api.SonarrClient
		name      string
		mediaType api.MediaType
		want      string
		arrID     int
	}{
		{name: "movie with radarr", mediaType: "movie", arrID: 42, radarr: movieTitleArrClient{}, sonarr: nil, want: "Inception"},
		{name: "episode with sonarr", mediaType: "episode", arrID: 7, radarr: nil, sonarr: seriesTitleArrClient{}, want: "Breaking Bad"},
		{name: "movie with nil radarr", mediaType: "movie", arrID: 42, radarr: nil, sonarr: nil, want: ""},
		{name: "episode with nil sonarr", mediaType: "episode", arrID: 7, radarr: nil, sonarr: nil, want: ""},
		{name: "zero arrID", mediaType: "movie", arrID: 0, radarr: movieTitleArrClient{}, sonarr: nil, want: ""},
		{name: "negative arrID", mediaType: "movie", arrID: -1, radarr: movieTitleArrClient{}, sonarr: nil, want: ""},
		{name: "movie radarr error", mediaType: "movie", arrID: 42, radarr: arrErrorClient{}, sonarr: nil, want: ""},
		{name: "episode sonarr error", mediaType: "episode", arrID: 7, radarr: nil, sonarr: arrErrorClient{}, want: ""},
		{name: "unknown media type", mediaType: "unknown", arrID: 42, radarr: movieTitleArrClient{}, sonarr: seriesTitleArrClient{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := &liveState{
				radarr: tt.radarr,
				sonarr: tt.sonarr,
			}
			got := lookupMediaTitle(context.Background(), ls, tt.mediaType, tt.arrID)
			if got != tt.want {
				t.Errorf("lookupMediaTitle(ctx, ls, %q, %d) = %q, want %q",
					tt.mediaType, tt.arrID, got, tt.want)
			}
		})
	}
}

// --- lookupMovieMediaID ---

func TestLookupMovieMediaID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		radarr api.RadarrClient
		name   string
		want   string
		arrID  int
	}{
		{name: "success returns tmdb prefix", radarr: movieTitleArrClient{}, arrID: 42, want: "tmdb-27205"},
		{name: "nil radarr returns empty", radarr: nil, arrID: 42, want: ""},
		{name: "radarr error returns empty", radarr: arrErrorClient{}, arrID: 42, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				db:       &qhMockStore{},
				activity: activity.New(50),
				alerts:   activity.NewAlertLog(100),
			}
			s.live.Store(&liveState{radarr: tt.radarr})

			got := s.lookupMovieMediaID(context.Background(), s.state(), tt.arrID)
			if got != tt.want {
				t.Errorf("lookupMovieMediaID(ctx, ls, %d) = %q, want %q",
					tt.arrID, got, tt.want)
			}
		})
	}
}

// --- lookupEpisodeMediaID ---

func TestLookupEpisodeMediaID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sonarr  api.SonarrClient
		name    string
		want    string
		series  int
		season  int
		episode int
	}{
		{name: "success returns tvdb episode ID", sonarr: seriesTitleArrClient{}, series: 7, season: 3, episode: 5, want: "tvdb-81189-s03e05"},
		{name: "nil sonarr returns empty", sonarr: nil, series: 7, season: 3, episode: 5, want: ""},
		{name: "sonarr error returns empty", sonarr: arrErrorClient{}, series: 7, season: 3, episode: 5, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				db:       &qhMockStore{},
				activity: activity.New(50),
				alerts:   activity.NewAlertLog(100),
			}
			s.live.Store(&liveState{sonarr: tt.sonarr})

			got := s.lookupEpisodeMediaID(context.Background(), s.state(), tt.series, tt.season, tt.episode)
			if got != tt.want {
				t.Errorf("lookupEpisodeMediaID(ctx, ls, %d, %d, %d) = %q, want %q",
					tt.series, tt.season, tt.episode, got, tt.want)
			}
		})
	}
}

func TestResolveMediaIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		radarr       api.RadarrClient
		sonarr       api.SonarrClient
		name         string
		mediaType    api.MediaType
		wantCoverage string
		wantHistory  string
		arrID        int
		season       int
		episode      int
	}{
		{
			name:         "movie with successful radarr lookup",
			mediaType:    "movie",
			arrID:        42,
			radarr:       movieTitleArrClient{},
			wantCoverage: "tmdb-27205",
			wantHistory:  "tmdb-27205",
		},
		{
			name:         "episode with successful sonarr lookup",
			mediaType:    "episode",
			arrID:        7,
			season:       3,
			episode:      5,
			sonarr:       seriesTitleArrClient{},
			wantCoverage: "tvdb-81189-s03e05",
			wantHistory:  "tvdb-81189-s03e05",
		},
		{
			name:         "movie with radarr error falls back to radarr-N",
			mediaType:    "movie",
			arrID:        42,
			radarr:       arrErrorClient{},
			wantCoverage: "",
			wantHistory:  "radarr-42",
		},
		{
			name:         "episode with sonarr error falls back to sonarr-N-sNNeNN",
			mediaType:    "episode",
			arrID:        7,
			season:       3,
			episode:      5,
			sonarr:       arrErrorClient{},
			wantCoverage: "",
			wantHistory:  "sonarr-7-s03e05",
		},
		{
			name:         "movie with nil radarr and arrID falls back to radarr-N",
			mediaType:    "movie",
			arrID:        10,
			wantCoverage: "",
			wantHistory:  "radarr-10",
		},
		{
			name:         "episode with nil sonarr and arrID falls back to sonarr-N-sNNeNN",
			mediaType:    "episode",
			arrID:        10,
			season:       1,
			episode:      2,
			wantCoverage: "",
			wantHistory:  "sonarr-10-s01e02",
		},
		{
			name:         "movie with zero arrID falls back to BuildMediaID",
			mediaType:    "movie",
			arrID:        0,
			radarr:       movieTitleArrClient{},
			wantCoverage: "",
			wantHistory:  "",
		},
		{
			name:         "episode with zero arrID falls back to BuildMediaID",
			mediaType:    "episode",
			arrID:        0,
			season:       1,
			episode:      2,
			sonarr:       seriesTitleArrClient{},
			wantCoverage: "",
			wantHistory:  "s00e00",
		},
		{
			name:         "unknown media type with arrID falls back to sonarr format",
			mediaType:    "unknown",
			arrID:        5,
			season:       2,
			episode:      3,
			wantCoverage: "",
			wantHistory:  "sonarr-5-s02e03",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				db:       &qhMockStore{},
				activity: activity.New(50),
				alerts:   activity.NewAlertLog(100),
			}
			s.live.Store(&liveState{radarr: tt.radarr, sonarr: tt.sonarr})

			coverageID, historyID := s.resolveMediaIDs(
				context.Background(), s.state(),
				tt.mediaType, tt.arrID, tt.season, tt.episode,
			)
			if coverageID != tt.wantCoverage {
				t.Errorf("resolveMediaIDs() coverageID = %q, want %q",
					coverageID, tt.wantCoverage)
			}
			if historyID != tt.wantHistory {
				t.Errorf("resolveMediaIDs() historyID = %q, want %q",
					historyID, tt.wantHistory)
			}
		})
	}
}
