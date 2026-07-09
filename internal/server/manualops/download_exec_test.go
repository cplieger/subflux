package manualops

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// fakeArr is a minimal ManualArrClient for exercising the media-ID and title
// lookups. Get* return the configured value (or error); Refresh* are unused by
// the functions under test and are no-ops.
type fakeArr struct {
	movie  arrapi.Movie
	series arrapi.Series
	getErr error
}

var (
	_ ManualSonarrClient = (*fakeArr)(nil)
	_ ManualRadarrClient = (*fakeArr)(nil)
)

func (f *fakeArr) GetMovieByID(context.Context, int) (arrapi.Movie, error) { return f.movie, f.getErr }
func (f *fakeArr) GetSeriesByID(context.Context, int) (arrapi.Series, error) {
	return f.series, f.getErr
}
func (f *fakeArr) RescanMovie(context.Context, int) error  { return nil }
func (f *fakeArr) RescanSeries(context.Context, int) error { return nil }

func TestResolveMediaIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		radarr      ManualRadarrClient
		sonarr      ManualSonarrClient
		mediaType   api.MediaType
		arrID       int
		season      int
		episode     int
		wantCover   string
		wantHistory string
	}{
		{
			name:        "movie resolved via radarr uses tmdb id for both",
			radarr:      &fakeArr{movie: arrapi.Movie{TmdbID: 123}},
			mediaType:   api.MediaTypeMovie,
			arrID:       5,
			wantCover:   "tmdb-123",
			wantHistory: "tmdb-123",
		},
		{
			name:        "movie without radarr falls back to radarr-<id> history",
			mediaType:   api.MediaTypeMovie,
			arrID:       5,
			wantCover:   "",
			wantHistory: "radarr-5",
		},
		{
			name:        "movie radarr error falls back to radarr-<id> history",
			radarr:      &fakeArr{getErr: errors.New("radarr unreachable")},
			mediaType:   api.MediaTypeMovie,
			arrID:       5,
			wantCover:   "",
			wantHistory: "radarr-5",
		},
		{
			name:        "episode resolved via sonarr uses tvdb id for both",
			sonarr:      &fakeArr{series: arrapi.Series{TvdbID: 999}},
			mediaType:   api.MediaTypeEpisode,
			arrID:       7,
			season:      1,
			episode:     2,
			wantCover:   "tvdb-999-s01e02",
			wantHistory: "tvdb-999-s01e02",
		},
		{
			name:        "episode without sonarr falls back to zero-padded sonarr-<id> history",
			mediaType:   api.MediaTypeEpisode,
			arrID:       7,
			season:      1,
			episode:     2,
			wantCover:   "",
			wantHistory: "sonarr-7-s01e02",
		},
		{
			name:        "episode without arr id uses build-media-id fallback",
			mediaType:   api.MediaTypeEpisode,
			arrID:       0,
			wantCover:   "",
			wantHistory: "s00e00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := &LiveState{Radarr: tt.radarr, Sonarr: tt.sonarr}
			cover, history := ResolveMediaIDs(context.Background(), ls, tt.mediaType, tt.arrID, tt.season, tt.episode)
			if cover != tt.wantCover {
				t.Errorf("coverageID = %q, want %q", cover, tt.wantCover)
			}
			if history != tt.wantHistory {
				t.Errorf("historyID = %q, want %q", history, tt.wantHistory)
			}
		})
	}
}

func TestLookupMediaTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		radarr    ManualRadarrClient
		sonarr    ManualSonarrClient
		mediaType api.MediaType
		arrID     int
		want      string
	}{
		{
			name:      "movie title from radarr",
			radarr:    &fakeArr{movie: arrapi.Movie{Title: "The Matrix"}},
			mediaType: api.MediaTypeMovie,
			arrID:     5,
			want:      "The Matrix",
		},
		{
			name:      "episode title from sonarr",
			sonarr:    &fakeArr{series: arrapi.Series{Title: "Breaking Bad"}},
			mediaType: api.MediaTypeEpisode,
			arrID:     7,
			want:      "Breaking Bad",
		},
		{
			name:      "zero arr id never consults the client",
			radarr:    &fakeArr{movie: arrapi.Movie{Title: "Should Not Be Read"}},
			mediaType: api.MediaTypeMovie,
			arrID:     0,
			want:      "",
		},
		{
			name:      "movie with nil radarr returns empty",
			mediaType: api.MediaTypeMovie,
			arrID:     5,
			want:      "",
		},
		{
			name:      "movie radarr error returns empty",
			radarr:    &fakeArr{getErr: errors.New("radarr unreachable")},
			mediaType: api.MediaTypeMovie,
			arrID:     5,
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := &LiveState{Radarr: tt.radarr, Sonarr: tt.sonarr}
			if got := LookupMediaTitle(context.Background(), ls, tt.mediaType, tt.arrID); got != tt.want {
				t.Errorf("LookupMediaTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
