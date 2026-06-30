package arrapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// --- GetMovies ---

func TestGetMovies_returns_all_movies(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{
		{ID: 1, Title: "Inception", HasFile: true},
		{ID: 2, Title: "Tenet", HasFile: false},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(movies)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetMovies(context.Background())
	if err != nil {
		t.Fatalf("GetMovies() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetMovies() got %d movies, want 2", len(got))
	}
}

// --- GetWantedMovies ---

func TestGetWantedMovies_filters_correctly(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{
		{
			ID: 1, Title: "Has file", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/a.mkv"},
		},
		{ID: 2, Title: "No file", HasFile: false},
		{
			ID: 3, Title: "Unmonitored with file", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/c.mkv"},
		},
		{
			ID: 4, Title: "Has file but nil movieFile", HasFile: true,
			MovieFile: nil,
		},
		{
			ID: 5, Title: "Excluded by tag", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/e.mkv"}, Tags: []int{42},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(movies)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	excludeTags := map[int]struct{}{42: {}}
	var got []api.Movie
	err := c.GetWantedMovies(context.Background(), excludeTags, func(m api.Movie) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatalf("GetWantedMovies() unexpected error: %v", err)
	}
	// Movie 1 (has file), Movie 3 (unmonitored but no exclude tag) = 2 results.
	// Movie 2 (no file), Movie 4 (nil movieFile), Movie 5 (excluded tag) filtered.
	if len(got) != 2 {
		t.Fatalf("GetWantedMovies() got %d movies, want 2", len(got))
	}
}

func TestGetWantedMovies_callback_error_propagates(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{
		{
			ID: 1, Title: "Movie", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/a.mkv"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(movies)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	wantErr := errSentinel("callback failed")
	err := c.GetWantedMovies(context.Background(), nil, func(_ api.Movie) error {
		return wantErr
	})

	if err != wantErr {
		t.Errorf("GetWantedMovies() error = %v, want %v", err, wantErr)
	}
}

// --- GetWantedMovies error propagation ---

func TestGetWantedMovies_server_error_propagates(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.GetWantedMovies(context.Background(), nil, func(_ api.Movie) error {
		t.Fatal("callback should not be called on fetch error")
		return nil
	})

	if err == nil {
		t.Fatal("GetWantedMovies() expected error for server failure")
	}
}

func TestGetWantedMovies_cancelled_context_during_iteration(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{
		{
			ID: 1, Title: "Movie 1", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/a.mkv"},
		},
		{
			ID: 2, Title: "Movie 2", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/b.mkv"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(movies)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())

	var count int
	err := c.GetWantedMovies(ctx, nil, func(_ api.Movie) error {
		count++
		cancel() // Cancel after first callback.
		return nil
	})

	// Context cancellation is checked at the top of the loop, so the first
	// movie is processed, then ctx.Err() fires before the second.
	if err == nil {
		t.Fatal("GetWantedMovies() expected context error")
	}
	if count != 1 {
		t.Errorf("GetWantedMovies() callback count = %d, want 1", count)
	}
}

func TestGetWantedMovies_nil_exclude_includes_all(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{
		{
			ID: 1, Title: "Monitored", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/a.mkv"},
		},
		{
			ID: 2, Title: "Unmonitored", HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/b.mkv"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(movies)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	var got []string
	err := c.GetWantedMovies(context.Background(), nil, func(m api.Movie) error {
		got = append(got, m.Title)
		return nil
	})
	if err != nil {
		t.Fatalf("GetWantedMovies(nil exclude) unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetWantedMovies(nil exclude) got %d movies, want 2", len(got))
	}
}

func TestGetWantedMovies_empty_list_succeeds(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	var called bool
	err := c.GetWantedMovies(context.Background(), nil, func(_ api.Movie) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("GetWantedMovies(empty) unexpected error: %v", err)
	}
	if called {
		t.Error("GetWantedMovies(empty) callback was invoked, want no calls")
	}
}

// TestGetWantedMovies_logs_missing_movieFile asserts the diagnostic emitted
// for a movie that reports HasFile but carries no MovieFile payload. The log
// is guarded by `m.HasFile && m.MovieFile == nil`; negating the nil check
// would log for the wrong movies (e.g. a tag-excluded movie that does have a
// file) and stay silent for this one. Asserting the logged title pins the
// exact movie, so the mutant is caught whether it logs nothing or the wrong
// title.
func TestGetWantedMovies_logs_missing_movieFile(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	movies := []api.Movie{
		{ID: 1, Title: "Ghost File", HasFile: true, MovieFile: nil},
		{ID: 2, Title: "Excluded With File", HasFile: true, MovieFile: &api.MovieFile{Path: "/m/e.mkv"}, Tags: []int{7}},
		{ID: 3, Title: "Wanted", HasFile: true, MovieFile: &api.MovieFile{Path: "/m/w.mkv"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		json.NewEncoder(w).Encode(movies)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.GetWantedMovies(context.Background(), map[int]struct{}{7: {}}, func(_ api.Movie) error { return nil })
	if err != nil {
		t.Fatalf("GetWantedMovies() unexpected error: %v", err)
	}
	rec, ok := h.find("movie has file but no movieFile data")
	if !ok {
		t.Fatal("GetWantedMovies() did not log the missing-movieFile diagnostic")
	}
	if got := rec.attrs["movie"]; got != "Ghost File" {
		t.Errorf("missing-movieFile log movie = %v, want %q", got, "Ghost File")
	}
}
