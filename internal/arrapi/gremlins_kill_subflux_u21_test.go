package arrapi

// Tests in this file kill surviving gremlins mutants for unit subflux-u21
// (package internal/arrapi). They add tests only; no production code is
// touched. Each test documents the file:line and mutator it targets.
//
// Several targets are debug/info logs whose only observable effect is a
// slog record. Those tests capture the default slog logger (see
// gk_subflux_u21_installCapture) and therefore do NOT call t.Parallel():
// slog.Default is a global, and non-parallel tests run in the sequential
// phase where parallel tests are parked, so the capture is isolated and the
// default is restored before any parallel test runs.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- slog capture infrastructure ---

type gk_subflux_u21_logRec struct {
	attrs map[string]any
	msg   string
}

type gk_subflux_u21_capHandler struct {
	mu   sync.Mutex
	recs []gk_subflux_u21_logRec
}

func (h *gk_subflux_u21_capHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *gk_subflux_u21_capHandler) Handle(_ context.Context, r slog.Record) error {
	rec := gk_subflux_u21_logRec{msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.recs = append(h.recs, rec)
	h.mu.Unlock()
	return nil
}

func (h *gk_subflux_u21_capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *gk_subflux_u21_capHandler) WithGroup(string) slog.Handler      { return h }

func (h *gk_subflux_u21_capHandler) count(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for i := range h.recs {
		if h.recs[i].msg == msg {
			n++
		}
	}
	return n
}

func (h *gk_subflux_u21_capHandler) find(msg string) (gk_subflux_u21_logRec, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.recs {
		if h.recs[i].msg == msg {
			return h.recs[i], true
		}
	}
	return gk_subflux_u21_logRec{}, false
}

// gk_subflux_u21_installCapture redirects the default slog logger to an
// in-memory capture handler for the duration of the test and restores it
// afterward. Callers must NOT use t.Parallel().
func gk_subflux_u21_installCapture(t *testing.T) *gk_subflux_u21_capHandler {
	t.Helper()
	h := &gk_subflux_u21_capHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func gk_subflux_u21_attrInt(t *testing.T, rec gk_subflux_u21_logRec, key string) int64 {
	t.Helper()
	v, ok := rec.attrs[key]
	if !ok {
		t.Fatalf("log attr %q missing; have %v", key, rec.attrs)
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		t.Fatalf("log attr %q = %T (%v), want integer", key, v, v)
		return 0
	}
}

// gk_subflux_u21_jsonClient returns a Client whose backing server replies 200
// with the JSON encoding of payload on every path.
func gk_subflux_u21_jsonClient(t *testing.T, payload any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return newTestClient(t, srv)
}

// --- arrapi_http.go:73:37 ARITHMETIC_BASE (idPath make() capacity) ---

// The capacity hint is len(base)+20+len(suffix). Flipping a '+' to '-' makes
// the capacity negative for the chosen inputs, which panics make(). Two calls
// each pin the exact result string while making one capacity term dominate, so
// whichever '+' the mutator targets the test fails (panic) while the original
// returns the correct string.
func Test_gk_subflux_u21_IdPathCapacity(t *testing.T) {
	t.Parallel()
	// Long suffix, empty base: original cap = 0+20+25 = 45 (ok).
	// '+len(suffix)' -> '-' gives 0+20-25 = -5 -> make panics.
	longSuffix := strings.Repeat("z", 25)
	if got, want := idPath("", 7, longSuffix), "7"+longSuffix; got != want {
		t.Fatalf("idPath(%q, 7, <25 z>) = %q, want %q", "", got, want)
	}
	// Short base+suffix: original cap = 2+20+2 = 24 (ok).
	// 'len(base)+20' -> 'len(base)-20' gives 2-20+2 = -16 -> make panics.
	if got, want := idPath("ab", 3, "cd"), "ab3cd"; got != want {
		t.Fatalf("idPath(%q, 3, %q) = %q, want %q", "ab", "cd", got, want)
	}
}

// --- client.go:86:18 CONDITIONALS_NEGATION (maxRetries clamp) ---

// `if c.maxRetries < 0 { c.maxRetries = 0 }`. A negative maxRetries must clamp
// to 0; the negation `>= 0` leaves it at -5.
// (The 86:18 CONDITIONALS_BOUNDARY `<` -> `<=` is equivalent: the clamp target
// is 0, so at maxRetries==0 both branches yield 0 and no input distinguishes.)
func Test_gk_subflux_u21_NewClientClampsNegativeMaxRetries(t *testing.T) {
	t.Parallel()
	c, err := NewClient("http://localhost", "k", func(c *Client) { c.maxRetries = -5 })
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}
	if c.maxRetries != 0 {
		t.Errorf("NewClient(maxRetries=-5).maxRetries = %d, want 0", c.maxRetries)
	}
}

// --- client.go:89:18 CONDITIONALS_NEGATION + 89:23 ARITHMETIC_BASE (retryDelay clamp) ---

// `if c.retryDelay < 100*time.Millisecond { c.retryDelay = 100 * time.Millisecond }`.
// A 50ms retryDelay is below the floor, so the original clamps it to 100ms.
//   - 89:18 negation `>= 100ms`: 50ms >= 100ms is false -> no clamp -> stays 50ms.
//   - 89:23 arithmetic `100/time.Millisecond` == 0 -> guard becomes `< 0`:
//     50ms < 0 is false -> no clamp -> stays 50ms.
// Both leave 50ms; asserting 100ms kills both.
// (The 89:18 CONDITIONALS_BOUNDARY `<` -> `<=` is equivalent: the clamp target
// equals the boundary 100ms, so at retryDelay==100ms both yield 100ms.)
func Test_gk_subflux_u21_NewClientClampsLowRetryDelay(t *testing.T) {
	t.Parallel()
	c, err := NewClient("http://localhost", "k", func(c *Client) { c.retryDelay = 50 * time.Millisecond })
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}
	if c.retryDelay != 100*time.Millisecond {
		t.Errorf("NewClient(retryDelay=50ms).retryDelay = %v, want %v", c.retryDelay, 100*time.Millisecond)
	}
}

// --- response.go:50:34 NEG, 51:19 ARITH, 52:11 NEG (decodeJSONSlice cap hint) ---

// With ContentLength=2000 and fetchAllAvgItemSize=200 the hint is 10, so a
// 3-element body decodes into a slice whose capacity stays exactly 10
// (pre-allocated, no growth). Asserting cap==10 pins:
//   - 50:34 NEGATION (`cl > 0` -> `cl <= 0`): skips block -> nil -> cap grows to 4.
//   - 51:19 ARITHMETIC (`/` -> `*`): hint=2000*200 -> cap 400000.
//   - 52:11 NEGATION (`hint > 0` -> `hint <= 0`): skips make -> nil -> cap 4.
// (The 50:34 and 52:11 CONDITIONALS_BOUNDARY `>` -> `>=` mutants are
// equivalent: at cl/hint == 0 the extra entry computes hint 0 / makes a cap-0
// slice, producing an identical decoded result.)
func Test_gk_subflux_u21_DecodeJSONSliceCapHint(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		Body:          io.NopCloser(strings.NewReader("[1,2,3]")),
		ContentLength: 2000,
	}
	items, err := decodeJSONSlice[int](resp, int64(1<<20), "gk")
	if err != nil {
		t.Fatalf("decodeJSONSlice() unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("decodeJSONSlice() len = %d, want 3", len(items))
	}
	if cap(items) != 10 {
		t.Errorf("decodeJSONSlice() cap = %d, want 10 (ContentLength 2000 / avg 200)", cap(items))
	}
}

// --- filter.go:14:16 INCREMENT_DECREMENT (logMovieSummary target count) ---

// `targetMovies++` over two wanted movies must log target_movies=2; `--`
// yields -2.
func Test_gk_subflux_u21_LogMovieSummaryTargetCount(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	movies := []api.Movie{
		{ID: 1, Title: "A", HasFile: true, MovieFile: &api.MovieFile{Path: "/a.mkv"}},
		{ID: 2, Title: "B", HasFile: true, MovieFile: &api.MovieFile{Path: "/b.mkv"}},
	}
	logMovieSummary(movies, nil)
	rec, ok := h.find("fetched movie list")
	if !ok {
		t.Fatal("logMovieSummary() did not emit 'fetched movie list'")
	}
	if got := gk_subflux_u21_attrInt(t, rec, "target_movies"); got != 2 {
		t.Errorf("logMovieSummary() target_movies = %d, want 2", got)
	}
}

// --- sonarr_wanted.go:98:15 INCREMENT_DECREMENT (logSeriesSummary target count) ---

// `targetSeries++` over two non-excluded series must log target_series=2; `--`
// yields -2.
func Test_gk_subflux_u21_LogSeriesSummaryTargetCount(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	series := []api.Series{
		{ID: 1, Title: "A"},
		{ID: 2, Title: "B"},
	}
	logSeriesSummary(series, nil)
	rec, ok := h.find("fetched series list")
	if !ok {
		t.Fatal("logSeriesSummary() did not emit 'fetched series list'")
	}
	if got := gk_subflux_u21_attrInt(t, rec, "target_series"); got != 2 {
		t.Errorf("logSeriesSummary() target_series = %d, want 2", got)
	}
}

// --- notify.go:90:95 CONDITIONALS_NEGATION (postCommand drain-error log) ---

// On a 200 response the drain `io.Copy` returns err==nil, so the original
// `if err != nil` does NOT log "failed to drain command response". The negation
// `if err == nil` logs it on every success.
func Test_gk_subflux_u21_PostCommandDrainNoErrorLog(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	c := gk_subflux_u21_jsonClient(t, "ok")
	if err := c.postCommand(context.Background(), commandBody{Name: CommandRescanSeries, SeriesID: 1}); err != nil {
		t.Fatalf("postCommand() unexpected error: %v", err)
	}
	if n := h.count("failed to drain command response"); n != 0 {
		t.Errorf("postCommand() drain-failure logs = %d, want 0 (drain succeeded)", n)
	}
}

// --- client.go:113:95 CONDITIONALS_NEGATION (Ping drain-error log) ---

// A successful Ping drains with err==nil, so the original `if err != nil` does
// NOT log "failed to drain ping response". The negation logs it on success.
func Test_gk_subflux_u21_PingDrainNoErrorLog(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	c := gk_subflux_u21_jsonClient(t, "ok")
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() unexpected error: %v", err)
	}
	if n := h.count("failed to drain ping response"); n != 0 {
		t.Errorf("Ping() drain-failure logs = %d, want 0 (drain succeeded)", n)
	}
}

// --- radarr.go:36:54 CONDITIONALS_NEGATION (has-file/no-movieFile debug log) ---

// A movie with HasFile=true and MovieFile=nil enters the `!wantedMovie` branch
// and the inner `HasFile && MovieFile == nil` is true, logging "movie has file
// but no movieFile data". Flipping `== nil` to `!= nil` makes the inner guard
// false, suppressing the log.
func Test_gk_subflux_u21_GetWantedMoviesLogsMissingMovieFile(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	c := gk_subflux_u21_jsonClient(t, []api.Movie{
		{ID: 1, Title: "Orphan", HasFile: true, MovieFile: nil},
	})
	err := c.GetWantedMovies(context.Background(), nil, func(api.Movie) error { return nil })
	if err != nil {
		t.Fatalf("GetWantedMovies() unexpected error: %v", err)
	}
	if n := h.count("movie has file but no movieFile data"); n != 1 {
		t.Errorf("GetWantedMovies() missing-movieFile logs = %d, want 1", n)
	}
}

// --- sonarr.go:39:10 CONDITIONALS_NEGATION (GetSeries success debug log) ---

// A successful GetSeries (err==nil) emits "fetched series from Sonarr". The
// negation `err != nil` suppresses it on success.
func Test_gk_subflux_u21_GetSeriesLogsOnSuccess(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	c := gk_subflux_u21_jsonClient(t, []api.Series{{ID: 1, Title: "Show"}})
	if _, err := c.GetSeries(context.Background()); err != nil {
		t.Fatalf("GetSeries() unexpected error: %v", err)
	}
	if n := h.count("fetched series from Sonarr"); n != 1 {
		t.Errorf("GetSeries() success logs = %d, want 1", n)
	}
}

// --- sonarr.go:51:10 CONDITIONALS_NEGATION (getTags success debug log) ---

// A successful getTags (err==nil) emits "fetched tags from arr". The negation
// `err != nil` suppresses it on success.
func Test_gk_subflux_u21_GetTagsLogsOnSuccess(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	c := gk_subflux_u21_jsonClient(t, []api.Tag{{ID: 1, Label: "x"}})
	if _, err := c.getTags(context.Background()); err != nil {
		t.Fatalf("getTags() unexpected error: %v", err)
	}
	if n := h.count("fetched tags from arr"); n != 1 {
		t.Errorf("getTags() success logs = %d, want 1", n)
	}
}

// --- sonarr_wanted.go:63:19 CONDITIONALS_BOUNDARY (empty-series exclusion) ---

// `if len(wanted) > 0 { results = append(...) }`. A series whose episodes are
// all unwanted (len(wanted)==0) must NOT be added to results, so the
// "finished iterating series" log reports processed=0. The boundary mutant
// `len(wanted) >= 0` is always true, adding the empty series -> processed=1.
func Test_gk_subflux_u21_GetWantedEpisodesExcludesEmptySeries(t *testing.T) {
	h := gk_subflux_u21_installCapture(t)
	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/series", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.Series{{ID: 1, Title: "Show"}})
	})
	mux.HandleFunc(apiPrefix+"/episode", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.Episode{{ID: 10, HasFile: false}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv)

	called := 0
	err := c.GetWantedEpisodes(context.Background(), nil, func(api.Series, api.Episode) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("GetWantedEpisodes() unexpected error: %v", err)
	}
	if called != 0 {
		t.Fatalf("GetWantedEpisodes() callback called %d times, want 0", called)
	}
	rec, ok := h.find("finished iterating series")
	if !ok {
		t.Fatal("GetWantedEpisodes() did not emit 'finished iterating series'")
	}
	if got := gk_subflux_u21_attrInt(t, rec, "processed"); got != 0 {
		t.Errorf("GetWantedEpisodes() processed = %d, want 0 (empty series excluded)", got)
	}
}
