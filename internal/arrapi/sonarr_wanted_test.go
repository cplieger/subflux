package arrapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// --- logSeriesSummary ---

// TestLogSeriesSummary verifies the documented pre-scan INFO summary: total
// series, the count not excluded by tag, and the episode-file estimate
// accumulated from per-series statistics (excluded series and series without
// statistics do not contribute).
func TestLogSeriesSummary(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	series := []api.Series{
		{ID: 1, Title: "With stats", Statistics: &api.SeriesStatistics{EpisodeFileCount: 10}},
		{ID: 2, Title: "Without stats"},
		{ID: 3, Title: "Excluded", Tags: []int{9}, Statistics: &api.SeriesStatistics{EpisodeFileCount: 5}},
	}
	logSeriesSummary(series, map[int]struct{}{9: {}})

	rec, ok := h.find("fetched series list")
	if !ok {
		t.Fatal("logSeriesSummary() did not emit 'fetched series list'")
	}
	if got := logAttrInt(t, rec, "total_series"); got != 3 {
		t.Errorf("total_series = %d, want 3", got)
	}
	// Series 3 is excluded by tag, so only series 1 and 2 are targets.
	if got := logAttrInt(t, rec, "target_series"); got != 2 {
		t.Errorf("target_series = %d, want 2", got)
	}
	// Only non-excluded series with stats contribute: series 1 (10). Series 2
	// has nil stats; series 3 (5) is excluded.
	if got := logAttrInt(t, rec, "estimated_episode_files"); got != 10 {
		t.Errorf("estimated_episode_files = %d, want 10", got)
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
			{
				ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e01.mkv"},
			},
			{ID: 11, HasFile: false},
			{
				ID: 12, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e03.mkv"},
			},
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
			{
				ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e01.mkv"},
			},
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
			{
				ID: 20, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/show/s01e01.mkv"},
			},
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
			{
				ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/s01e01.mkv"},
			},
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
			{
				ID: 1, Title: "Show With Stats",
				Statistics: &api.SeriesStatistics{EpisodeFileCount: 10},
			},
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
	// Exercises the Statistics != nil accumulation branch in logSeriesSummary.
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
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Episode{
			{
				ID: 10, HasFile: true,
				EpisodeFile: &api.EpisodeFile{Path: "/tv/s01e01.mkv"},
			},
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

// --- wantedEpisode ---

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

// TestGetWantedEpisodes_processed_count_excludes_empty_series asserts that a
// non-excluded series with zero wanted episodes is NOT recorded as
// "processed". The collection guard `len(wanted) > 0` decides whether a series
// is recorded; a boundary mutant (`>= 0`) records empty series too, inflating
// the processed count without changing the callback count, which the log
// assertion detects.
func TestGetWantedEpisodes_processed_count_excludes_empty_series(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]api.Series{
			{ID: 1, Title: "Has wanted"},
			{ID: 2, Title: "No wanted"},
		})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("seriesId") == "1" {
			json.NewEncoder(w).Encode([]api.Episode{
				{ID: 10, HasFile: true, EpisodeFile: &api.EpisodeFile{Path: "/tv/s01e01.mkv"}},
			})
			return
		}
		// Series 2: episodes exist but none qualify -> zero wanted episodes.
		json.NewEncoder(w).Encode([]api.Episode{
			{ID: 20, HasFile: false},
			{ID: 21, HasFile: true, EpisodeFile: nil},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	var count int
	err := c.GetWantedEpisodes(context.Background(), nil, func(_ api.Series, _ api.Episode) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("GetWantedEpisodes() unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("callback count = %d, want 1 (only series 1's wanted episode)", count)
	}
	rec, ok := h.find("finished iterating series")
	if !ok {
		t.Fatal("GetWantedEpisodes() did not emit 'finished iterating series'")
	}
	if got := logAttrInt(t, rec, "processed"); got != 1 {
		t.Errorf("processed = %d, want 1 (empty series must not be recorded)", got)
	}
}
