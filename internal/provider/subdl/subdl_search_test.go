package subdl

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cplieger/runesafe"
	"github.com/cplieger/subflux/internal/api"
)

// --- filterResults ---

func TestFilterResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		check     func(t *testing.T, got []api.Subtitle)
		name      string
		matchedBy api.MatchMethod
		items     []subtitleItem
		wantCount int
		isEpisode bool
	}{
		{
			name: "basic_mapping",
			items: []subtitleItem{
				{
					Name:        "sub1.srt",
					Language:    "EN",
					URL:         "/dl/sub1.zip",
					Releases:    []string{"Group.720p", "Group.1080p"},
					EpisodeFrom: 5,
					EpisodeEnd:  5,
				},
			},
			isEpisode: true,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				s := got[0]
				if s.Provider != "subdl" {
					t.Errorf("Provider = %q, want %q", s.Provider, "subdl")
				}
				if s.ID != "sub1.srt" {
					t.Errorf("ID = %q, want %q", s.ID, "sub1.srt")
				}
				if s.Language != "en" {
					t.Errorf("Language = %q, want %q", s.Language, "en")
				}
				if s.ReleaseName != "Group.720p Group.1080p" {
					t.Errorf("ReleaseName = %q, want %q", s.ReleaseName, "Group.720p Group.1080p")
				}
				if s.DownloadURL != "/dl/sub1.zip" {
					t.Errorf("DownloadURL = %q, want %q", s.DownloadURL, "/dl/sub1.zip")
				}
				if s.MatchedBy != api.MatchByIMDB {
					t.Errorf("MatchedBy = %q, want %q", s.MatchedBy, api.MatchByIMDB)
				}
				if s.Season != 0 {
					t.Errorf("Season = %d, want 0", s.Season)
				}
				if s.Episode != 0 {
					t.Errorf("Episode = %d, want 0", s.Episode)
				}
				if s.HearingImp {
					t.Error("HearingImp = true, want false (no HI indicators)")
				}
			},
		},
		{
			name: "skips_season_packs_for_episodes",
			items: []subtitleItem{
				{Name: "pack.srt", Language: "EN", EpisodeFrom: 1, EpisodeEnd: 10},
				{Name: "single.srt", Language: "EN", EpisodeFrom: 5, EpisodeEnd: 5},
			},
			isEpisode: true,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ID != "single.srt" {
					t.Errorf("ID = %q, want %q", got[0].ID, "single.srt")
				}
			},
		},
		{
			name: "allows_season_packs_for_movies",
			items: []subtitleItem{
				{Name: "pack.srt", Language: "EN", EpisodeFrom: 1, EpisodeEnd: 10},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
		},
		{
			name: "skips_unknown_language",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "UNKNOWN", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 0,
		},
		{
			name: "skips_forced",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", Comment: "Forced subtitles", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 0,
		},
		{
			name: "hi_from_api_flag",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", HI: true, EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if !got[0].HearingImp {
					t.Error("HearingImp = false, want true")
				}
			},
		},
		{
			name: "hi_from_comment",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", Comment: "SDH version", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if !got[0].HearingImp {
					t.Error("HearingImp = false, want true (SDH in comment)")
				}
			},
		},
		{
			name: "hi_from_filename",
			items: []subtitleItem{
				{Name: "movie_hi_eng.srt", Language: "EN", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if !got[0].HearingImp {
					t.Error("HearingImp = false, want true (_hi_ in filename)")
				}
			},
		},
		{
			name:      "nil_input",
			items:     nil,
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: -1, // signals nil check
		},
		{
			name:      "empty_input",
			items:     []subtitleItem{},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: -1, // signals nil check
		},
		{
			name: "empty_releases",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", Releases: nil, EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].ReleaseName != "" {
					t.Errorf("ReleaseName = %q, want empty", got[0].ReleaseName)
				}
			},
		},
		{
			name: "matchedBy_propagation_imdb",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].MatchedBy != api.MatchByIMDB {
					t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByIMDB)
				}
			},
		},
		{
			name: "matchedBy_propagation_title",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByTitle,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].MatchedBy != api.MatchByTitle {
					t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByTitle)
				}
			},
		},
		{
			name: "matchedBy_propagation_tmdb",
			items: []subtitleItem{
				{Name: "sub.srt", Language: "EN", EpisodeFrom: 1, EpisodeEnd: 1},
			},
			isEpisode: false,
			matchedBy: api.MatchByTMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].MatchedBy != api.MatchByTMDB {
					t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, api.MatchByTMDB)
				}
			},
		},
		{
			name: "season_episode_propagation",
			items: []subtitleItem{
				{
					Name:        "sub.srt",
					Language:    "EN",
					Season:      3,
					Episode:     7,
					EpisodeFrom: 7,
					EpisodeEnd:  7,
				},
			},
			isEpisode: true,
			matchedBy: api.MatchByIMDB,
			wantCount: 1,
			check: func(t *testing.T, got []api.Subtitle) {
				t.Helper()
				if got[0].Season != 3 {
					t.Errorf("Season = %d, want 3", got[0].Season)
				}
				if got[0].Episode != 7 {
					t.Errorf("Episode = %d, want 7", got[0].Episode)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterResults(tt.items, tt.isEpisode, tt.matchedBy)
			if tt.wantCount == -1 {
				if got != nil {
					t.Errorf("filterResults() = %v, want nil", got)
				}
				return
			}
			if len(got) != tt.wantCount {
				t.Fatalf("filterResults() = %d results, want %d", len(got), tt.wantCount)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// --- buildSearchParams ---

func TestBuildSearchParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		apiKey     string
		req        *api.SearchRequest
		langs      []string
		wantParams map[string]string
		wantAbsent []string
	}{
		{
			name:   "episode_with_imdb",
			apiKey: "key123",
			req:    &api.SearchRequest{MediaType: "episode", ImdbID: "tt1234567", Season: 3, Episode: 7},
			langs:  []string{"EN", "FR"},
			wantParams: map[string]string{
				"type":           "tv",
				"imdb_id":        "tt1234567",
				"season_number":  "3",
				"episode_number": "7",
			},
			wantAbsent: []string{"film_name"},
		},
		{
			name:   "episode_title_fallback",
			apiKey: "key",
			req:    &api.SearchRequest{MediaType: "episode", Title: "Breaking Bad", Season: 1, Episode: 1},
			langs:  []string{"EN"},
			wantParams: map[string]string{
				"film_name": "Breaking Bad",
			},
			wantAbsent: []string{"imdb_id"},
		},
		{
			name:   "movie_with_imdb",
			apiKey: "key",
			req:    &api.SearchRequest{MediaType: "movie", ImdbID: "tt0111161"},
			langs:  []string{"EN"},
			wantParams: map[string]string{
				"type":    "movie",
				"imdb_id": "tt0111161",
			},
		},
		{
			name:   "movie_tmdb_fallback",
			apiKey: "key",
			req:    &api.SearchRequest{MediaType: "movie", TmdbID: 550},
			langs:  []string{"EN"},
			wantParams: map[string]string{
				"tmdb_id": "550",
			},
			wantAbsent: []string{"imdb_id"},
		},
		{
			name:   "movie_title_fallback",
			apiKey: "key",
			req:    &api.SearchRequest{MediaType: "movie", Title: "Inception"},
			langs:  []string{"EN"},
			wantParams: map[string]string{
				"film_name": "Inception",
			},
		},
		{
			name:   "common_fields",
			apiKey: "mykey",
			req:    &api.SearchRequest{MediaType: "movie", ImdbID: "tt1"},
			langs:  []string{"EN", "FR"},
			wantParams: map[string]string{
				"api_key":       "mykey",
				"languages":     "EN,FR",
				"subs_per_page": "30",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			params := buildSearchParams(tt.apiKey, tt.req, tt.langs)
			for k, want := range tt.wantParams {
				if got := params.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			for _, k := range tt.wantAbsent {
				if got := params.Get(k); got != "" {
					t.Errorf("%s = %q, want empty", k, got)
				}
			}
		})
	}
}

// --- checkAPIStatus ---

func TestCheckAPIStatus_success_returns_items(t *testing.T) {
	t.Parallel()

	items := []subtitleItem{
		{Name: "sub1.srt", Language: "EN"},
		{Name: "sub2.srt", Language: "FR"},
	}
	resp := &apiResponse{Status: true, Subtitles: items}

	got, err := checkAPIStatus(resp, "Movie (2024)")
	if err != nil {
		t.Fatalf("checkAPIStatus(status=true) unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("checkAPIStatus(status=true) = %d items, want 2", len(got))
	}
	if got[0].Name != "sub1.srt" {
		t.Errorf("checkAPIStatus()[0].Name = %q, want %q", got[0].Name, "sub1.srt")
	}
}

func TestCheckAPIStatus_success_empty_items(t *testing.T) {
	t.Parallel()

	resp := &apiResponse{Status: true, Subtitles: nil}

	got, err := checkAPIStatus(resp, "Movie (2024)")
	if err != nil {
		t.Fatalf("checkAPIStatus(status=true, no items) unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("checkAPIStatus(status=true, nil subs) = %v, want nil", got)
	}
}

func TestCheckAPIStatus_cant_find_returns_nil_no_error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, errMsg string
	}{
		{name: "exact lowercase", errMsg: "can't find the movie"},
		{name: "mixed case", errMsg: "Can't Find this title"},
		{name: "uppercase", errMsg: "CAN'T FIND anything"},
		{name: "embedded in longer message", errMsg: "Sorry, we can't find that"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &apiResponse{Status: false, Error: runesafe.Untrusted(tt.errMsg)}

			got, err := checkAPIStatus(resp, "Test Movie")
			if err != nil {
				t.Errorf("checkAPIStatus(%q) unexpected error: %v", tt.errMsg, err)
			}
			if got != nil {
				t.Errorf("checkAPIStatus(%q) = %v, want nil", tt.errMsg, got)
			}
		})
	}
}

func TestCheckAPIStatus_other_error_returns_error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, errMsg, wantSub string
	}{
		{name: "invalid api key", errMsg: "Invalid API key", wantSub: "Invalid API key"},
		{name: "rate limit", errMsg: "Daily quota exceeded", wantSub: "Daily quota exceeded"},
		{name: "generic", errMsg: "something broke", wantSub: "something broke"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &apiResponse{Status: false, Error: runesafe.Untrusted(tt.errMsg)}

			got, err := checkAPIStatus(resp, "Test Movie")
			if err == nil {
				t.Fatalf("checkAPIStatus(%q) expected error", tt.errMsg)
			}
			if got != nil {
				t.Errorf("checkAPIStatus(%q) items = %v, want nil", tt.errMsg, got)
			}
			if !strings.Contains(err.Error(), "subdl API:") {
				t.Errorf("checkAPIStatus(%q) error = %q, want prefix %q",
					tt.errMsg, err.Error(), "subdl API:")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("checkAPIStatus(%q) error = %q, want containing %q",
					tt.errMsg, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestCheckAPIStatus_status_false_empty_error_returns_nil_no_error(t *testing.T) {
	t.Parallel()

	// Defensive branch: status=false with no error message is logged as a
	// warning and treated as no-results rather than an error.
	resp := &apiResponse{Status: false, Error: ""}

	got, err := checkAPIStatus(resp, "Test Movie")
	if err != nil {
		t.Errorf("checkAPIStatus(empty error) unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("checkAPIStatus(empty error) = %v, want nil", got)
	}
}

func TestCheckAPIStatus_status_true_overrides_error_field(t *testing.T) {
	t.Parallel()

	// If status=true, the Error field is ignored (some APIs fill it
	// with warnings/metadata even on success). Items take precedence.
	resp := &apiResponse{
		Status:    true,
		Error:     "non-fatal warning",
		Subtitles: []subtitleItem{{Name: "sub.srt", Language: "EN"}},
	}

	got, err := checkAPIStatus(resp, "Test Movie")
	if err != nil {
		t.Fatalf("checkAPIStatus(status=true + error) unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("checkAPIStatus(status=true + error) = %d items, want 1", len(got))
	}
}

// --- inferMatchedBy ---

func TestInferMatchedBy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params url.Values
		want   api.MatchMethod
	}{
		{name: "film_name wins", params: url.Values{"film_name": {"Inception"}, "imdb_id": {"tt1"}}, want: api.MatchByTitle},
		{name: "tmdb_id when no film_name", params: url.Values{"tmdb_id": {"550"}, "imdb_id": {"tt1"}}, want: api.MatchByTMDB},
		{name: "imdb is default", params: url.Values{"imdb_id": {"tt1"}}, want: api.MatchByIMDB},
		{name: "empty params defaults to imdb", params: url.Values{}, want: api.MatchByIMDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := inferMatchedBy(tt.params); got != tt.want {
				t.Errorf("inferMatchedBy(%v) = %q, want %q", tt.params, got, tt.want)
			}
		})
	}
}

// --- secret redaction (provider wiring) ---

// roundTripFunc adapts a function to http.RoundTripper so a test can drive
// the provider's HTTP client without a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSearch_redactsAPIKeyFromTransportError verifies the public Search path
// strips the api_key from transport errors: the search URL carries the
// api_key as a query parameter, so a failed request surfaces it inside the
// wrapping *url.Error unless the provider redacts it.
func TestSearch_redactsAPIKeyFromTransportError(t *testing.T) {
	t.Parallel()
	const apiKey = "supersecret32hex"
	p := &Provider{
		apiKey: apiKey,
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: i/o timeout")
		})},
	}

	_, err := p.Search(context.Background(), &api.SearchRequest{
		MediaType: "movie", ImdbID: "tt1375666", Languages: []string{"en"},
	})
	if err == nil {
		t.Fatal("Search() with a failing transport expected an error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("Search() leaked api_key in error: %q", err.Error())
	}
	// Redaction unwraps the *url.Error but must preserve the underlying cause.
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("Search() error lost the transport cause: %q", err.Error())
	}
}
