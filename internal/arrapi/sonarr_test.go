package arrapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
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
		httpClient:  srv.Client(),
		baseURL:     srv.URL,
		apiKey:      "test-key",
		maxAttempts: 3,
		retryDelay:  10 * time.Second, // Long delay — context cancel will fire first.
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

// --- success-summary debug logs ---

// TestGetSeries_logs_count_on_success asserts the documented "fetched series
// from Sonarr" debug summary is emitted (with the correct count) on a
// successful fetch. The log is guarded by `if err == nil`; negating that
// guard suppresses the summary on success, which this test detects.
func TestGetSeries_logs_count_on_success(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	series := []api.Series{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}, {ID: 3, Title: "C"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(series)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.GetSeries(context.Background()); err != nil {
		t.Fatalf("GetSeries() unexpected error: %v", err)
	}
	rec, ok := h.find("fetched series from Sonarr")
	if !ok {
		t.Fatal("GetSeries() did not emit 'fetched series from Sonarr' on success")
	}
	if got := logAttrInt(t, rec, "count"); got != 3 {
		t.Errorf("fetched series count = %d, want 3", got)
	}
}

// TestGetTags_logs_count_on_success asserts the "fetched tags from arr" debug
// summary is emitted (with the correct count) on a successful fetch. The log
// is guarded by `if err == nil`; negating that guard suppresses the summary on
// success, which this test detects.
func TestGetTags_logs_count_on_success(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	tags := []api.Tag{{ID: 1, Label: "x"}, {ID: 2, Label: "y"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(tags)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.getTags(context.Background()); err != nil {
		t.Fatalf("getTags() unexpected error: %v", err)
	}
	rec, ok := h.find("fetched tags from arr")
	if !ok {
		t.Fatal("getTags() did not emit 'fetched tags from arr' on success")
	}
	if got := logAttrInt(t, rec, "count"); got != 2 {
		t.Errorf("fetched tags count = %d, want 2", got)
	}
}
