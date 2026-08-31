package mediahandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/arrsvc"
)

// The handler keeps no flight of its own: the arr-read wrapper beneath it owns
// per-key coalescing and the read TTL, so concurrent readers of one list share
// ONE upstream call. Pinned through the production wrapper against a counting
// arr, because a fake client cannot show whose coalescing is doing the work.

// countingArr is an arr that answers every read with an empty JSON array and
// counts requests per path prefix.
type countingArr struct {
	srv    *httptest.Server
	byPath map[string]int
	mu     sync.Mutex
}

func newCountingArr(t *testing.T) *countingArr {
	t.Helper()
	a := &countingArr{byPath: make(map[string]int)}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.byPath[r.URL.Path]++
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *countingArr) calls(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.byPath[path]
}

// wrappedSonarr builds the production Sonarr wrapper against the counting arr.
func wrappedSonarr(t *testing.T, a *countingArr) *arrsvc.CachedSonarr {
	t.Helper()
	c, err := arrsvc.NewCachedSonarr(a.srv.URL, "key",
		arrsvc.NewReadGate(func() context.Context { return t.Context() }, nil))
	if err != nil {
		t.Fatalf("build wrapped sonarr: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// fireConcurrently runs handler against path from n goroutines and fails on any
// non-200 or on bodies that disagree.
func fireConcurrently(t *testing.T, handler http.HandlerFunc, path string, n int) {
	t.Helper()
	bodies := make([]string, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, path, nil))
			codes[i] = rec.Code
			bodies[i] = rec.Body.String()
		})
	}
	wg.Wait()
	for i := range n {
		if codes[i] != http.StatusOK {
			t.Errorf("GET %s (caller %d) status = %d, want %d", path, i, codes[i], http.StatusOK)
		}
		if bodies[i] != bodies[0] {
			t.Errorf("GET %s (caller %d) body = %q, want caller 0's %q", path, i, bodies[i], bodies[0])
		}
	}
}

func TestHandleMediaEpisodes_concurrentReadersShareOneUpstreamCall(t *testing.T) {
	arr := newCountingArr(t)
	h := newMediaHandler(wrappedSonarr(t, arr), nil)

	fireConcurrently(t, h.HandleMediaEpisodes, "/api/media/series/7/episodes", 8)

	if got := arr.calls("/api/v3/episode"); got != 1 {
		t.Errorf("upstream episode requests for 8 concurrent readers = %d, want 1", got)
	}
}

func TestHandleMediaSeries_concurrentReadersShareOneUpstreamCall(t *testing.T) {
	arr := newCountingArr(t)
	h := newMediaHandler(wrappedSonarr(t, arr), nil)

	fireConcurrently(t, h.HandleMediaSeries, "/api/media/series", 8)

	if got := arr.calls("/api/v3/series"); got != 1 {
		t.Errorf("upstream series requests for 8 concurrent readers = %d, want 1", got)
	}
}

func TestHandleMediaMovies_concurrentReadersShareOneUpstreamCall(t *testing.T) {
	arr := newCountingArr(t)
	c, err := arrsvc.NewCachedRadarr(arr.srv.URL, "key",
		arrsvc.NewReadGate(func() context.Context { return t.Context() }, nil))
	if err != nil {
		t.Fatalf("build wrapped radarr: %v", err)
	}
	t.Cleanup(c.Close)
	h := newMediaHandler(nil, c)

	fireConcurrently(t, h.HandleMediaMovies, "/api/media/movies", 8)

	if got := arr.calls("/api/v3/movie"); got != 1 {
		t.Errorf("upstream movie requests for 8 concurrent readers = %d, want 1", got)
	}
}

// A read past the wrapper's TTL is a second upstream call, so the pin above is
// the wrapper's coalescing rather than a fixture that could never make two.
func TestMediaHandlers_distinctSeriesEachCostTheirOwnUpstreamCall(t *testing.T) {
	arr := newCountingArr(t)
	h := newMediaHandler(wrappedSonarr(t, arr), nil)

	for _, id := range []string{"7", "8"} {
		rec := httptest.NewRecorder()
		h.HandleMediaEpisodes(rec,
			httptest.NewRequest(http.MethodGet, "/api/media/series/"+id+"/episodes", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET episodes for series %s: status = %d, want %d", id, rec.Code, http.StatusOK)
		}
		if !strings.HasPrefix(rec.Body.String(), "[") {
			t.Fatalf("GET episodes for series %s: body = %q, want a JSON array", id, rec.Body.String())
		}
	}
	if got := arr.calls("/api/v3/episode"); got != 2 {
		t.Errorf("upstream episode requests for two distinct series = %d, want 2", got)
	}
}
