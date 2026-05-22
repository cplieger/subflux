package arrapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/httputil"
)

// --- api.HistoryEntry integration ---

func TestHistoryEntry_UnmarshalJSON_radarr_format(t *testing.T) {
	t.Parallel()

	raw := `{"id":1,"eventType":"downloadFolderImported","movieId":42,"data":{"importedPath":"/movies/test.mkv"}}`
	var entry api.HistoryEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatalf("Unmarshal() unexpected error: %v", err)
	}
	if entry.MovieID != 42 {
		t.Errorf("MovieID = %d, want 42", entry.MovieID)
	}
	if entry.ImportedPath() != "/movies/test.mkv" {
		t.Errorf("ImportedPath() = %q, want %q", entry.ImportedPath(), "/movies/test.mkv")
	}
}

// --- GetHistorySince ---

func TestGetHistorySince_returns_entries(t *testing.T) {
	t.Parallel()

	entries := []api.HistoryEntry{
		{SeriesID: 10, EpisodeID: 100,
			Data: map[string]string{"importedPath": "/tv/show/s01e01.mkv"}},
		{MovieID: 5},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := c.GetHistorySince(context.Background(), since, api.HistoryImported)

	if err != nil {
		t.Fatalf("GetHistorySince() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetHistorySince() got %d entries, want 2", len(got))
	}
	if got[0].ImportedPath() != "/tv/show/s01e01.mkv" {
		t.Errorf("entries[0].ImportedPath() = %q, want %q",
			got[0].ImportedPath(), "/tv/show/s01e01.mkv")
	}
}

func TestGetHistorySince_sends_correct_params(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	since := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	_, err := c.GetHistorySince(context.Background(), since, api.HistoryImported)

	if err != nil {
		t.Fatalf("GetHistorySince() unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "date=2024-06-15") {
		t.Errorf("GetHistorySince() path = %q, want to contain date param", gotPath)
	}
	if !strings.Contains(gotPath, "eventType=3") {
		t.Errorf("GetHistorySince() path = %q, want to contain eventType=3", gotPath)
	}
}

func TestGetHistorySince_zero_event_type_omits_param(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHistorySince(context.Background(), time.Now(), 0)

	if err != nil {
		t.Fatalf("GetHistorySince() unexpected error: %v", err)
	}
	if strings.Contains(gotPath, "eventType") {
		t.Errorf("GetHistorySince(0) path = %q, want eventType param omitted", gotPath)
	}
}

func TestGetHistorySince_server_error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHistorySince(context.Background(), time.Now(), 0)

	if err == nil {
		t.Fatal("GetHistorySince() expected error for 500 status")
	}
}

func TestGetHistorySince_invalid_json(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHistorySince(context.Background(), time.Now(), 0)

	if err == nil {
		t.Fatal("GetHistorySince() expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "decode history") {
		t.Errorf("GetHistorySince() error = %q, want to contain %q",
			err.Error(), "decode history")
	}
}

// --- fetchByID (table-driven) ---

func TestFetchByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		callFn     func(c *Client) (any, error)
		status     int
		body       string
		wantPath   string
		wantErr    bool
		errContain string
		assert     func(t *testing.T, got any)
	}{
		{
			name:     "series_success",
			status:   http.StatusOK,
			body:     `{"id":42,"title":"Breaking Bad","imdbId":"tt0903747","year":2008}`,
			wantPath: apiPrefix + "/series/42",
			callFn:   func(c *Client) (any, error) { return c.GetSeriesByID(context.Background(), 42) },
			assert: func(t *testing.T, got any) {
				s := got.(*api.Series)
				if s.Title != "Breaking Bad" {
					t.Errorf("Title = %q, want %q", s.Title, "Breaking Bad")
				}
				if s.ImdbID != "tt0903747" {
					t.Errorf("ImdbID = %q, want %q", s.ImdbID, "tt0903747")
				}
			},
		},
		{
			name:     "series_server_error",
			status:   http.StatusNotFound,
			body:     "not found",
			wantPath: apiPrefix + "/series/999",
			callFn:   func(c *Client) (any, error) { return c.GetSeriesByID(context.Background(), 999) },
			wantErr:  true,
		},
		{
			name:     "episode_success",
			status:   http.StatusOK,
			body:     `{"id":100,"seasonNumber":1,"episodeNumber":3,"hasFile":true}`,
			wantPath: apiPrefix + "/episode/100",
			callFn:   func(c *Client) (any, error) { return c.GetEpisodeByID(context.Background(), 100) },
			assert: func(t *testing.T, got any) {
				ep := got.(*api.Episode)
				if ep.SeasonNumber != 1 || ep.EpisodeNumber != 3 {
					t.Errorf("Episode = S%02dE%02d, want S01E03", ep.SeasonNumber, ep.EpisodeNumber)
				}
			},
		},
		{
			name:     "episode_server_error",
			status:   http.StatusInternalServerError,
			body:     "",
			wantPath: apiPrefix + "/episode/999",
			callFn:   func(c *Client) (any, error) { return c.GetEpisodeByID(context.Background(), 999) },
			wantErr:  true,
		},
		{
			name:     "movie_success",
			status:   http.StatusOK,
			body:     `{"id":7,"title":"Inception","imdbId":"tt1375666","year":2010}`,
			wantPath: apiPrefix + "/movie/7",
			callFn:   func(c *Client) (any, error) { return c.GetMovieByID(context.Background(), 7) },
			assert: func(t *testing.T, got any) {
				m := got.(*api.Movie)
				if m.Title != "Inception" {
					t.Errorf("Title = %q, want %q", m.Title, "Inception")
				}
			},
		},
		{
			name:     "movie_server_error",
			status:   http.StatusNotFound,
			body:     "",
			wantPath: apiPrefix + "/movie/999",
			callFn:   func(c *Client) (any, error) { return c.GetMovieByID(context.Background(), 999) },
			wantErr:  true,
		},
		{
			name:       "series_invalid_json",
			status:     http.StatusOK,
			body:       "not valid json",
			wantPath:   apiPrefix + "/series/1",
			callFn:     func(c *Client) (any, error) { return c.GetSeriesByID(context.Background(), 1) },
			wantErr:    true,
			errContain: "decode series",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.wantPath != "" && !strings.HasPrefix(r.URL.Path, tt.wantPath) {
					t.Errorf("path = %q, want prefix %q", r.URL.Path, tt.wantPath)
				}
				if tt.status >= 400 {
					w.WriteHeader(tt.status)
					if tt.body != "" {
						fmt.Fprint(w, tt.body)
					}
					return
				}
				w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			c := newTestClient(t, srv)
			got, err := tt.callFn(c)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error = %q, want containing %q", err, tt.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.assert != nil {
				tt.assert(t, got)
			}
		})
	}
}
