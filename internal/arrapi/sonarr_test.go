package arrapi

import (
	"sync/atomic"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/httputil"
)

// --- fetchAll ---

func TestFetchAll_decodes_items(t *testing.T) {
	t.Parallel()

	items := []api.Series{
		{ID: 1, Title: "Show A"},
		{ID: 2, Title: "Show B"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(items)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := fetchAll[api.Series](context.Background(), c, apiPrefix+"/series")

	if err != nil {
		t.Fatalf("fetchAll() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("fetchAll() got %d items, want 2", len(got))
	}
	if got[0].Title != "Show A" {
		t.Errorf("fetchAll()[0].Title = %q, want %q", got[0].Title, "Show A")
	}
	if got[1].Title != "Show B" {
		t.Errorf("fetchAll()[1].Title = %q, want %q", got[1].Title, "Show B")
	}
}

func TestFetchAll_empty_array(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := fetchAll[api.Series](context.Background(), c, apiPrefix+"/series")

	if err != nil {
		t.Fatalf("fetchAll() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("fetchAll() got %d items, want 0", len(got))
	}
}

func TestFetchAll_invalid_json(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := fetchAll[api.Series](context.Background(), c, apiPrefix+"/series")

	if err == nil {
		t.Fatal("fetchAll() expected error for invalid JSON")
	}
}

func TestFetchAll_server_error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := fetchAll[api.Series](context.Background(), c, apiPrefix+"/series")

	if err == nil {
		t.Fatal("fetchAll() expected error for 500 status")
	}
}

// --- get ---

func TestGet_sends_api_key_header(t *testing.T) {
	t.Parallel()

	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(api.HeaderXAPIKey)
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.get(context.Background(), apiPrefix+"/series")

	if err != nil {
		t.Fatalf("get() unexpected error: %v", err)
	}
	resp.Body.Close()
	if gotKey != "test-key" {
		t.Errorf("get() sent X-Api-Key = %q, want %q", gotKey, "test-key")
	}
}

func TestGet_non_200_returns_error_with_body(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "not found")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.get(context.Background(), apiPrefix+"/missing")
	if resp != nil {
		resp.Body.Close()
	}

	if err == nil {
		t.Fatal("get() expected error for 404 status")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("get() error type = %T, want *StatusError", err)
	}
	if se.Code != http.StatusNotFound {
		t.Errorf("get() StatusError.Code = %d, want %d", se.Code, http.StatusNotFound)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("get() error = %q, want to contain response body", err.Error())
	}
}

func TestGet_non_200_empty_body(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.get(context.Background(), apiPrefix+"/series")
	if resp != nil {
		resp.Body.Close()
	}

	if err == nil {
		t.Fatal("get() expected error for 403 status")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("get() error type = %T, want *StatusError", err)
	}
	if se.Code != http.StatusForbidden {
		t.Errorf("get() StatusError.Code = %d, want %d", se.Code, http.StatusForbidden)
	}
}

func TestGet_cancelled_context(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := c.get(ctx, apiPrefix+"/series")
	if resp != nil {
		resp.Body.Close()
	}

	if err == nil {
		t.Fatal("get() expected error for cancelled context")
	}
}

// --- GetSeries ---

func TestGetSeries_streams_all_series(t *testing.T) {
	t.Parallel()

	series := []api.Series{
		{ID: 1, Title: "Breaking Bad"},
		{ID: 2, Title: "The Wire"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(series)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetSeries(context.Background())

	if err != nil {
		t.Fatalf("GetSeries() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetSeries() got %d series, want 2", len(got))
	}
}

// --- GetEpisodes ---

func TestGetEpisodes_returns_episodes(t *testing.T) {
	t.Parallel()

	episodes := []api.Episode{
		{ID: 10, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true},
		{ID: 11, SeasonNumber: 1, EpisodeNumber: 2, HasFile: false},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("seriesId"); got != "5" {
			t.Errorf("GetEpisodes() seriesId param = %q, want %q", got, "5")
		}
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(episodes)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetEpisodes(context.Background(), 5)

	if err != nil {
		t.Fatalf("GetEpisodes() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetEpisodes() got %d episodes, want 2", len(got))
	}
	if got[0].SeasonNumber != 1 || got[0].EpisodeNumber != 1 {
		t.Errorf("GetEpisodes()[0] = S%02dE%02d, want S01E01",
			got[0].SeasonNumber, got[0].EpisodeNumber)
	}
}

func TestGetEpisodes_server_error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetEpisodes(context.Background(), 1)

	if err == nil {
		t.Fatal("GetEpisodes() expected error for 500 status")
	}
}

func TestGetEpisodes_invalid_json_response(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "not valid json")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetEpisodes(context.Background(), 1)

	if err == nil {
		t.Fatal("GetEpisodes() expected error for invalid JSON response")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("GetEpisodes() error = %q, want to contain %q",
			err.Error(), "decode")
	}
}

// --- GetWantedEpisodes ---

func TestGetWantedEpisodes_filters_excluded_series(t *testing.T) {
	t.Parallel()

	series := []api.Series{{ID: 1, Title: "Excluded", Tags: []int{99}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		if strings.Contains(r.URL.Path, "episode") {
			t.Error("GetWantedEpisodes() should not fetch episodes for excluded series")
		}
		json.NewEncoder(w).Encode(series)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	excludeTags := map[int]struct{}{99: {}}
	var count int
	err := c.GetWantedEpisodes(context.Background(), excludeTags, func(_ api.Series, _ api.Episode) error {
		count++
		return nil
	})

	if err != nil {
		t.Fatalf("GetWantedEpisodes() unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("GetWantedEpisodes() called callback %d times, want 0", count)
	}
}

func TestGetWantedEpisodes_filters_episodes_without_files(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{{ID: 1, Title: "Show"}})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e01.mkv"}},
			{ID: 11, HasFile: false},
			{ID: 12, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e03.mkv"}},
			{ID: 13, HasFile: true, EpisodeFile: nil},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	var gotEpisodeIDs []int
	err := c.GetWantedEpisodes(context.Background(), nil, func(_ api.Series, ep api.Episode) error {
		gotEpisodeIDs = append(gotEpisodeIDs, ep.ID)
		return nil
	})

	if err != nil {
		t.Fatalf("GetWantedEpisodes() unexpected error: %v", err)
	}
	if len(gotEpisodeIDs) != 2 {
		t.Fatalf("GetWantedEpisodes() got %d episodes, want 2", len(gotEpisodeIDs))
	}
}

func TestGetWantedEpisodes_callback_error_propagates(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{{ID: 1, Title: "Show"}})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e01.mkv"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	wantErr := errSentinel("stop")
	err := c.GetWantedEpisodes(context.Background(), nil, func(_ api.Series, _ api.Episode) error { return wantErr })

	if err != wantErr {
		t.Errorf("GetWantedEpisodes() error = %v, want %v", err, wantErr)
	}
}

func TestGetWantedEpisodes_skips_series_on_episode_fetch_error(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{
			{ID: 1, Title: "Failing"},
			{ID: 2, Title: "Working"},
		})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("seriesId") == "1" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "error")
			return
		}
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 20, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e01.mkv"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	var gotEpisodeIDs []int
	err := c.GetWantedEpisodes(context.Background(), nil, func(_ api.Series, ep api.Episode) error {
		gotEpisodeIDs = append(gotEpisodeIDs, ep.ID)
		return nil
	})

	if err != nil {
		t.Fatalf("GetWantedEpisodes() unexpected error: %v", err)
	}
	if len(gotEpisodeIDs) != 1 {
		t.Fatalf("GetWantedEpisodes() got %d episodes, want 1", len(gotEpisodeIDs))
	}
	if gotEpisodeIDs[0] != 20 {
		t.Errorf("GetWantedEpisodes() episode ID = %d, want 20", gotEpisodeIDs[0])
	}
}

// --- GetWantedEpisodes error propagation ---

func TestGetWantedEpisodes_series_fetch_error_propagates(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.GetWantedEpisodes(context.Background(), nil, func(_ api.Series, _ api.Episode) error {
		t.Fatal("callback should not be called on series fetch error")
		return nil
	})

	if err == nil {
		t.Fatal("GetWantedEpisodes() expected error for series fetch failure")
	}
	if !strings.Contains(err.Error(), "fetch series list") {
		t.Errorf("GetWantedEpisodes() error = %q, want to contain %q",
			err.Error(), "fetch series list")
	}
}

func TestGetWantedEpisodes_cancelled_context_during_iteration(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{
			{ID: 1, Title: "Show 1"},
			{ID: 2, Title: "Show 2"},
		})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/s01e01.mkv"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())

	var count int
	err := c.GetWantedEpisodes(ctx, nil, func(_ api.Series, _ api.Episode) error {
		count++
		cancel() // Cancel after first callback.
		return nil
	})

	if err == nil {
		t.Fatal("GetWantedEpisodes() expected context error")
	}
	if count != 1 {
		t.Errorf("GetWantedEpisodes() callback count = %d, want 1", count)
	}
}

func TestGetWantedEpisodes_statistics_accumulation(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{
			{ID: 1, Title: "Show With Stats",
				Statistics: &api.SeriesStatistics{EpisodeFileCount: 10}},
			{ID: 2, Title: "Show Without Stats"},
		})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Episode{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.GetWantedEpisodes(context.Background(), nil, func(_ api.Series, _ api.Episode) error {
		return nil
	})

	// This test exercises the Statistics != nil branch (sonarr.go:40-42).
	// No callback invocations expected (no episodes with files).
	if err != nil {
		t.Fatalf("GetWantedEpisodes() unexpected error: %v", err)
	}
}

func TestGetWantedEpisodes_nil_exclude_includes_all(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{
			{ID: 1, Title: "Monitored"},
			{ID: 2, Title: "Unmonitored"},
		})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/s01e01.mkv"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	var gotSeriesTitles []string
	err := c.GetWantedEpisodes(context.Background(), nil, func(s api.Series, _ api.Episode) error {
		gotSeriesTitles = append(gotSeriesTitles, s.Title)
		return nil
	})

	if err != nil {
		t.Fatalf("GetWantedEpisodes(nil exclude) unexpected error: %v", err)
	}
	if len(gotSeriesTitles) != 2 {
		t.Fatalf("GetWantedEpisodes(nil exclude) got %d series, want 2",
			len(gotSeriesTitles))
	}
}

// --- GetEpisodes context cancellation ---

func TestGetEpisodesWithRetry_context_cancelled_before_attempt(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately — caught at ctx.Err() check inside retry loop.

	_, err := c.GetEpisodes(ctx, 1)

	if err == nil {
		t.Fatal("GetEpisodes() expected error for cancelled context")
	}
}

func TestGetEpisodesWithRetry_context_cancelled_during_delay(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	// Use a long retry delay so the context cancellation fires during the select.
	c := &Client{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		apiKey:     "test-key",
		maxRetries: 3,
		retryDelay: 10 * time.Second, // Long delay — context cancel will fire first.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := c.GetEpisodes(ctx, 1)

	if err == nil {
		t.Fatal("GetEpisodes() expected error for context timeout during delay")
	}
	if attempts.Load() < 1 {
		t.Errorf("GetEpisodes() attempts = %d, want >= 1", attempts.Load())
	}
}

func TestGetEpisodesWithRetry_succeeds_after_transient_failure(t *testing.T) {
	t.Parallel()

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "transient error")
			return
		}
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 10, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetEpisodes(context.Background(), 1)

	if err != nil {
		t.Fatalf("GetEpisodes() unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("GetEpisodes() attempts = %d, want 2", attempts)
	}
	if len(got) != 1 {
		t.Fatalf("GetEpisodes() got %d episodes, want 1", len(got))
	}
	if got[0].ID != 10 {
		t.Errorf("GetEpisodes() episode ID = %d, want 10", got[0].ID)
	}
}

// --- get with invalid URL ---

func TestGet_invalid_url_returns_error(t *testing.T) {
	t.Parallel()

	c := &Client{
		httpClient: http.DefaultClient,
		baseURL:    "http://[::1]:namedport", // Invalid URL that fails NewRequestWithContext.
		apiKey:     "test-key",
	}

	resp, err := c.get(context.Background(), "://bad")
	if resp != nil {
		resp.Body.Close()
	}

	if err == nil {
		t.Fatal("get() expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "build request") {
		t.Errorf("get() error = %q, want to contain %q", err.Error(), "build request")
	}
}

// --- getTags ---

func TestGetTags_returns_all_tags(t *testing.T) {
	t.Parallel()

	tags := []api.Tag{
		{ID: 1, Label: "no-subflux"},
		{ID: 2, Label: "anime"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiPrefix+"/tag" {
			t.Errorf("getTags() path = %q, want %q", r.URL.Path, apiPrefix+"/tag")
		}
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(tags)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.getTags(context.Background())

	if err != nil {
		t.Fatalf("getTags() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("getTags() got %d tags, want 2", len(got))
	}
	if got[0].Label != "no-subflux" {
		t.Errorf("getTags()[0].Label = %q, want %q", got[0].Label, "no-subflux")
	}
}

// --- ResolveExcludeTagIDs ---

func TestResolveExcludeTagIDs_returns_matching_ids(t *testing.T) {
	t.Parallel()

	tags := []api.Tag{
		{ID: 1, Label: "no-subflux"},
		{ID: 2, Label: "anime"},
		{ID: 3, Label: "other"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(tags)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got := c.ResolveExcludeTagIDs(context.Background(), []string{"no-subflux", "anime"}, false)

	if len(got) != 2 {
		t.Fatalf("ResolveExcludeTagIDs() got %d IDs, want 2", len(got))
	}
	if _, ok := got[1]; !ok {
		t.Error("ResolveExcludeTagIDs() missing ID 1 (no-subflux)")
	}
	if _, ok := got[2]; !ok {
		t.Error("ResolveExcludeTagIDs() missing ID 2 (anime)")
	}
}

func TestResolveExcludeTagIDs_empty_names_returns_nil(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for empty names")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got := c.ResolveExcludeTagIDs(context.Background(), nil, false)

	if got != nil {
		t.Errorf("ResolveExcludeTagIDs(nil) = %v, want nil", got)
	}
}

func TestResolveExcludeTagIDs_server_error_returns_nil(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got := c.ResolveExcludeTagIDs(context.Background(), []string{"no-subflux"}, false)

	if got != nil {
		t.Errorf("ResolveExcludeTagIDs(server error) = %v, want nil", got)
	}
}

func TestResolveExcludeTagIDs_unknown_tag_returns_empty(t *testing.T) {
	t.Parallel()

	tags := []api.Tag{{ID: 1, Label: "existing"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(tags)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got := c.ResolveExcludeTagIDs(context.Background(), []string{"nonexistent"}, true)

	if len(got) != 0 {
		t.Errorf("ResolveExcludeTagIDs(unknown) got %d IDs, want 0", len(got))
	}
}

func TestWantedEpisode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ep   api.Episode
		want bool
	}{
		{"has file with episode file", api.Episode{HasFile: true, EpisodeFile: &api.EpisodeFile{Path: "/a.mkv"}}, true},
		{"has file nil episode file", api.Episode{HasFile: true, EpisodeFile: nil}, false},
		{"no file with episode file", api.Episode{HasFile: false, EpisodeFile: &api.EpisodeFile{Path: "/a.mkv"}}, false},
		{"no file nil episode file", api.Episode{HasFile: false, EpisodeFile: nil}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := wantedEpisode(&tt.ep)
			if got != tt.want {
				t.Errorf("wantedEpisode(%+v) = %v, want %v", tt.ep, got, tt.want)
			}
		})
	}
}
