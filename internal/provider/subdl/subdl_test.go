package subdl

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"subflux/internal/api"
	"subflux/internal/httputil"
	"subflux/internal/provider/classify"
	"testing"

	"pgregory.net/rapid"
)

func TestFactory_requires_api_key(t *testing.T) {
	t.Parallel()
	if _, err := Factory(context.Background(), nil); err == nil {
		t.Fatal("Factory(context.Background(), nil) expected error")
	}
	if _, err := Factory(context.Background(), map[string]any{"api_key": ""}); err == nil {
		t.Fatal("Factory(empty key) expected error")
	}
}

func TestFactory_with_api_key(t *testing.T) {
	t.Parallel()
	p, err := Factory(context.Background(), map[string]any{"api_key": "test"})
	if err != nil {
		t.Fatalf("Factory() unexpected error: %v", err)
	}
	if p.Name() != api.ProviderNameSubDL {
		t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameSubDL)
	}
}

func TestIso2ToSubDL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{name: "English", input: "en", want: "EN"},
		{name: "French", input: "fr", want: "FR"},
		{name: "Persian", input: "fa", want: "FA"},
		{name: "alpha3 English", input: "eng", want: "EN"},
		{name: "alpha3 French", input: "fre", want: "FR"},
		{name: "unknown", input: "xx", want: ""},
		{name: "empty", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := iso2ToSubDL(tt.input); got != tt.want {
				t.Errorf("iso2ToSubDL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSubdlToISO2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{name: "english", input: "EN", want: "en"},
		{name: "french", input: "FR", want: "fr"},
		{name: "persian", input: "FA", want: "fa"},
		{name: "lowercase input", input: "en", want: "en"},
		{name: "unknown", input: "XX", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := subdlToISO2(tt.input); got != tt.want {
				t.Errorf("subdlToISO2(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsHearingImpaired_via_provider(t *testing.T) {
	// Retained to document subdl's (comment + name) invocation pattern.
	// The heavy-lift cases live in provider.TestIsHearingImpaired; this
	// anchors the subdl call-site contract against the shared helper.
	t.Parallel()
	tests := []struct {
		name, comment, filename string
		want                    bool
	}{
		{name: "empty", comment: "", filename: "", want: false},
		{name: "sdh comment", comment: "SDH version", filename: "sub.srt", want: true},
		{name: "hi filename overrides non-hi comment", comment: "non hi version", filename: "movie_hi_eng.srt", want: true},
		{name: "nonhi excluded", comment: "nonhi version", filename: "sub.srt", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classify.IsHearingImpaired(tt.comment, tt.filename); got != tt.want {
				t.Errorf("classify.IsHearingImpaired(%q, %q) = %v, want %v",
					tt.comment, tt.filename, got, tt.want)
			}
		})
	}
}

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

// --- Download path validation ---

func TestDownload_rejects_non_relative_path(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: "test", client: http.DefaultClient}

	tests := []struct {
		name string
		url  string
	}{
		{name: "absolute URL", url: "https://evil.com/steal"},
		{name: "no leading slash", url: "dl/sub1.zip"},
		{name: "empty path", url: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sub := &api.Subtitle{DownloadURL: tt.url}
			_, err := p.Download(context.Background(), sub)
			if err == nil {
				t.Errorf("Download(%q) expected error", tt.url)
			}
		})
	}
}

// --- isForced is covered by provider.TestIsForced in the provider package ---

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

// --- handleDownloadResponse ---

func TestHandleDownloadResponse_success(t *testing.T) {
	t.Parallel()
	body := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	got, err := handleDownloadResponse(resp, 0, 0)
	if err != nil {
		t.Fatalf("handleDownloadResponse() error: %v", err)
	}
	if !strings.Contains(string(got), "Hello") {
		t.Errorf("handleDownloadResponse() = %q, want containing 'Hello'", got)
	}
}

func TestHandleDownloadResponse_429_rate_limit(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(429) expected error")
	}
	var rateErr *api.RateLimitError
	if !errors.As(err, &rateErr) {
		t.Errorf("handleDownloadResponse(429) error type = %T, want *api.RateLimitError", err)
	}
}

func TestHandleDownloadResponse_500_small_body_rate_limit(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode:    http.StatusInternalServerError,
		ContentLength: 10,
		Body:          io.NopCloser(strings.NewReader("limit")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(500, small) expected error")
	}
	var rateErr *api.RateLimitError
	if !errors.As(err, &rateErr) {
		t.Errorf("handleDownloadResponse(500, small) error type = %T, want *api.RateLimitError", err)
	}
}

func TestHandleDownloadResponse_500_large_body_generic(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode:    http.StatusInternalServerError,
		ContentLength: 500,
		Body:          io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(500, large) expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("handleDownloadResponse(500, large) error = %q, want 'HTTP 500'", err)
	}
}

func TestHandleDownloadResponse_500_unknown_length_generic(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode:    http.StatusInternalServerError,
		ContentLength: -1,
		Body:          io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(500, unknown) expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("handleDownloadResponse(500, unknown) error = %q, want 'HTTP 500'", err)
	}
}

func TestHandleDownloadResponse_other_error(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(403) expected error")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("handleDownloadResponse(403) error = %q, want 'HTTP 403'", err)
	}
}

func TestHandleDownloadResponse_extracts_from_zip(t *testing.T) {
	t.Parallel()

	// Build a minimal ZIP archive containing one SRT file.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("subtitle.srt")
	if err != nil {
		t.Fatal(err)
	}
	srtContent := "1\n00:00:01,000 --> 00:00:02,000\nHello from zip\n"
	if _, err := fw.Write([]byte(srtContent)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	got, err := handleDownloadResponse(resp, 0, 0)
	if err != nil {
		t.Fatalf("handleDownloadResponse(zip) error: %v", err)
	}
	if !strings.Contains(string(got), "Hello from zip") {
		t.Errorf("handleDownloadResponse(zip) = %q, want containing 'Hello from zip'", got)
	}
}

func TestHandleDownloadResponse_500_boundary_content_length(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		contentLength int64
		wantRateLimit bool
	}{
		{name: "zero length is rate limit", contentLength: 0, wantRateLimit: true},
		{name: "99 bytes is rate limit", contentLength: 99, wantRateLimit: true},
		{name: "100 bytes is generic error", contentLength: 100, wantRateLimit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode:    http.StatusInternalServerError,
				ContentLength: tt.contentLength,
				Body:          io.NopCloser(strings.NewReader("")),
			}
			_, err := handleDownloadResponse(resp, 0, 0)
			if err == nil {
				t.Fatal("handleDownloadResponse(500) expected error")
			}
			var rateErr *api.RateLimitError
			isRateLimit := errors.As(err, &rateErr)
			if isRateLimit != tt.wantRateLimit {
				t.Errorf("handleDownloadResponse(500, ContentLength=%d) rate_limit=%v, want %v",
					tt.contentLength, isRateLimit, tt.wantRateLimit)
			}
		})
	}
}

func TestHandleDownloadResponse_rejects_binary_raw_data(t *testing.T) {
	t.Parallel()
	// Binary data that isn't a recognized archive format but fails
	// ValidateSubtitleData's non-text byte threshold.
	binaryData := make([]byte, 200)
	for i := range binaryData {
		binaryData[i] = 0x01 // non-text control byte
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(binaryData)),
	}
	_, err := handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(binary raw) expected error")
	}
	if !strings.Contains(err.Error(), "subdl:") {
		t.Errorf("handleDownloadResponse(binary raw) error = %q, want containing 'subdl:'", err)
	}
}

func TestHandleDownloadResponse_rejects_binary_in_archive(t *testing.T) {
	t.Parallel()
	// Build a ZIP containing a .srt file with binary content.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("subtitle.srt")
	if err != nil {
		t.Fatal(err)
	}
	binaryContent := make([]byte, 200)
	for i := range binaryContent {
		binaryContent[i] = 0x01
	}
	if _, err := fw.Write(binaryContent); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	_, err = handleDownloadResponse(resp, 0, 0)
	if err == nil {
		t.Fatal("handleDownloadResponse(binary in zip) expected error")
	}
	if !strings.Contains(err.Error(), "subdl:") {
		t.Errorf("handleDownloadResponse(binary in zip) error = %q, want containing 'subdl:'", err)
	}
}

func TestLanguageMapping_roundtrip(t *testing.T) {
	t.Parallel()
	// Collect all valid ISO-2 codes from the map.
	codes := make([]string, 0, len(iso2ToSubDLMap))
	for k := range iso2ToSubDLMap {
		codes = append(codes, k)
	}

	rapid.Check(t, func(t *rapid.T) {
		idx := rapid.IntRange(0, len(codes)-1).Draw(t, "idx")
		code := codes[idx]

		sdl := iso2ToSubDL(code)
		if sdl == "" {
			t.Fatalf("iso2ToSubDL(%q) = empty, want non-empty", code)
		}
		back := subdlToISO2(sdl)
		if back != code {
			t.Fatalf("subdlToISO2(iso2ToSubDL(%q)) = %q, want %q", code, back, code)
		}
	})
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
			resp := &apiResponse{Status: false, Error: tt.errMsg}

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
			resp := &apiResponse{Status: false, Error: tt.errMsg}

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

// --- redactAPIKey ---

func TestRedactAPIKey_strips_secret_from_error_message(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: "supersecret32hex"}
	in := fmt.Errorf("Get https://api.subdl.com/api/v1/subtitles?api_key=%s: dial tcp: i/o timeout", p.apiKey)
	got := httputil.RedactSecret(in, p.apiKey)
	if got == nil {
		t.Fatal("redactAPIKey returned nil for non-nil input")
	}
	if strings.Contains(got.Error(), p.apiKey) {
		t.Errorf("redactAPIKey did not strip api_key: %q", got.Error())
	}
	if !strings.Contains(got.Error(), "REDACTED") {
		t.Errorf("redactAPIKey did not insert REDACTED marker: %q", got.Error())
	}
}

func TestRedactAPIKey_pass_through_when_apikey_absent_from_message(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: "supersecret32hex"}
	in := errors.New("some error that does not leak the secret")
	got := httputil.RedactSecret(in, p.apiKey)
	if got.Error() != in.Error() {
		t.Errorf("redactAPIKey mutated safe error: got %q, want %q", got.Error(), in.Error())
	}
}

func TestRedactAPIKey_nil_and_empty_apikey(t *testing.T) {
	t.Parallel()

	p := &Provider{apiKey: ""}
	if got := httputil.RedactSecret(nil, p.apiKey); got != nil {
		t.Errorf("redactAPIKey(nil) = %v, want nil", got)
	}

	in := errors.New("anything")
	if got := httputil.RedactSecret(in, p.apiKey); got.Error() != in.Error() {
		t.Errorf("redactAPIKey with empty apiKey mutated error: got %q", got.Error())
	}
}
