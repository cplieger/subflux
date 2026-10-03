package scanning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/subflux"
)

// mockShowCounter implements ShowCounter with canned per show+language counts.
type mockShowCounter struct {
	counts map[string]int
	err    error
	calls  atomic.Int32
}

func (m *mockShowCounter) CountShowSubtitles(_ context.Context, q subflux.ShowSubtitleQuery) (int, error) {
	imdbID, lang := q.ImdbID, q.Language
	m.calls.Add(1)
	if m.err != nil {
		return 0, m.err
	}
	return m.counts[imdbID+"-"+lang], nil
}

func TestNewSeasonTracker_with_counter(t *testing.T) {
	t.Parallel()
	mock := &mockShowCounter{}
	st := newSeasonTracker(mock, showskip.New(1*time.Hour), seedDeps{})
	if st == nil {
		t.Fatal("expected non-nil tracker")
	}
	if st.counter == nil {
		t.Fatal("expected counter to be set")
	}
}

func TestNewSeasonTracker_without_counter(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	if st == nil {
		t.Fatal("expected non-nil tracker even without counter")
	}
	if st.counter != nil {
		t.Fatal("expected nil counter")
	}
}

func TestShouldSkipShow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err      error
		counts   map[string]int
		name     string
		imdb     string
		langs    []string
		episodes int
		noCount  bool
		wantSkip bool
	}{
		{name: "no_counter", noCount: true, imdb: "tt123", episodes: 100, langs: []string{"fr"}, wantSkip: false},
		{name: "empty_imdb", counts: map[string]int{}, imdb: "", episodes: 100, langs: []string{"fr"}, wantSkip: false},
		{name: "zero_episodes", counts: map[string]int{}, imdb: "tt123", episodes: 0, langs: []string{"fr"}, wantSkip: false},
		{name: "below_threshold", counts: map[string]int{"tt123-fr": 5}, imdb: "tt123", episodes: 100, langs: []string{"fr"}, wantSkip: true},
		{name: "above_threshold", counts: map[string]int{"tt123-fr": 21}, imdb: "tt123", episodes: 100, langs: []string{"fr"}, wantSkip: false},
		{name: "api_error", err: errors.New("fail"), imdb: "tt123", episodes: 100, langs: []string{"fr"}, wantSkip: false},
		{name: "multi_lang_any_passes", counts: map[string]int{"tt123-en": 50}, imdb: "tt123", episodes: 100, langs: []string{"fr", "en"}, wantSkip: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var st *seasonTracker
			if tc.noCount {
				st = newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
			} else {
				st = newSeasonTracker(&mockShowCounter{counts: tc.counts, err: tc.err}, showskip.New(1*time.Hour), seedDeps{})
			}
			got := st.shouldSkipShow(t.Context(), tc.imdb, tc.episodes, tc.langs)
			if got != tc.wantSkip {
				t.Errorf("shouldSkipShow() = %v, want %v", got, tc.wantSkip)
			}
		})
	}
}

func TestShouldSkipShow_caches(t *testing.T) {
	t.Parallel()
	mock := &mockShowCounter{counts: map[string]int{}}
	st := newSeasonTracker(mock, showskip.New(1*time.Hour), seedDeps{})
	ctx := t.Context()
	st.shouldSkipShow(ctx, "tt123", 100, []string{"fr"})
	st.shouldSkipShow(ctx, "tt123", 100, []string{"fr"})
	if int(mock.calls.Load()) != 1 {
		t.Fatalf("expected 1 API call (cached), got %d", int(mock.calls.Load()))
	}
}

func TestSeasonTracker_no_early_stop_below_minimum(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("should not skip after only 2 no-results (minimum is 3)")
	}
}

func TestSeasonTracker_early_stop_at_minimum(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if !st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("expected skip after 3 consecutive no-results")
	}
}

func TestSeasonTracker_early_stop_large_season(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 5 {
		st.recordOutcome(t.Context(), "tt121220", 3, "fr", "", ScanNoResult, 33)
	}
	if st.shouldSkipSeason("tt121220", 3, "fr") {
		t.Fatal("should not skip after only 5 no-results (threshold is 6)")
	}
	st.recordOutcome(t.Context(), "tt121220", 3, "fr", "", ScanNoResult, 33)
	if !st.shouldSkipSeason("tt121220", 3, "fr") {
		t.Fatal("expected skip after 6 consecutive no-results")
	}
}

func TestSeasonTracker_found_resets_streak(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanFound, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("should not skip: found reset the streak")
	}
}

func TestSeasonTracker_skipped_does_not_affect_streak(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanSkipped, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if !st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("expected skip: skipped doesn't reset streak, 3 no-results reached")
	}
}

func TestSeasonTracker_independent_seasons(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	}
	if st.shouldSkipSeason("tt1", 2, "fr") {
		t.Fatal("season 2 should not be affected by season 1")
	}
}

func TestSeasonTracker_independent_languages(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	}
	if st.shouldSkipSeason("tt1", 1, "en") {
		t.Fatal("en should not be affected by fr early stop")
	}
}

func TestShouldSkipEpisode_all_langs_stopped(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
		st.recordOutcome(t.Context(), "tt1", 1, "en", "", ScanNoResult, 10)
	}
	if !st.shouldSkipEpisode("tt1", 1, []string{"fr", "en"}) {
		t.Fatal("expected skip: both languages hit early stop")
	}
}

func TestShouldSkipEpisode_one_lang_still_active(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 10)
	}
	if st.shouldSkipEpisode("tt1", 1, []string{"fr", "en"}) {
		t.Fatal("should not skip: en is still active")
	}
}

func TestShouldSkipEpisode_empty_imdb(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	if st.shouldSkipEpisode("", 1, []string{"fr"}) {
		t.Fatal("empty IMDB should not skip")
	}
}

func TestSeasonTracker_zero_season_ep_count_uses_minimum(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(t.Context(), "tt1", 1, "fr", "", ScanNoResult, 0)
	}
	if !st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("expected skip after 3 no-results with zero season count")
	}
}

func TestShowLevelSkip_an_uncallable_counter_is_not_cached(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"gated":        fmt.Errorf("%w: opensubtitles: credentials rejected; next attempt in 5m", search.ErrProviderGated),
		"auth":         &subflux.AuthError{Msg: "401"},
		"rate limited": fmt.Errorf("count: %w", &subflux.RateLimitError{Msg: "429"}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cache := showskip.New(time.Hour)
			st := newSeasonTracker(&mockShowCounter{err: err}, cache, seedDeps{})
			if st.showLevelSkip(t.Context(), "tt1", 10, "en") {
				t.Error("showLevelSkip() = true, want false")
			}
			if _, cached := cache.Get(showSkipCacheKey("tt1", "en")); cached {
				t.Error("an uncallable counter's answer was cached")
			}
		})
	}
}

func TestShowLevelSkip_a_plain_count_failure_caches_not_skip(t *testing.T) {
	t.Parallel()
	cache := showskip.New(time.Hour)
	st := newSeasonTracker(&mockShowCounter{err: errors.New("HTTP 502")}, cache, seedDeps{})
	if st.showLevelSkip(t.Context(), "tt1", 10, "en") {
		t.Error("showLevelSkip() = true, want false")
	}
	if skip, cached := cache.Get(showSkipCacheKey("tt1", "en")); !cached || skip {
		t.Errorf("cache = (%v, cached %v), want false cached", skip, cached)
	}
}

// showLevelSkip uses an inclusive threshold: a show is skipped when the
// available subtitle count is at or below episodeCount * showSkipThresholdPct.
// With 10 episodes the threshold is 2, so a count of exactly 2 still skips.
func TestShowLevelSkip_count_equals_threshold(t *testing.T) {
	t.Parallel()
	mock := &mockShowCounter{counts: map[string]int{"tt1-en": 2}}
	st := newSeasonTracker(mock, showskip.New(time.Hour), seedDeps{})

	got := st.showLevelSkip(t.Context(), "tt1", 10, "en")

	if !got {
		t.Errorf("showLevelSkip(count==threshold) = false, want true")
	}
}

// The multi-language show-skip path runs per-language checks in an errgroup
// whose goroutines never return an error, so it must not emit the
// "show skip check error" warning on a clean run.
func TestShouldSkipShow_no_spurious_errgroup_warn(t *testing.T) {
	// No t.Parallel: this test swaps the global slog default logger.
	buf := captureLogs(t)

	mock := &mockShowCounter{counts: map[string]int{"tt1-en": 100, "tt1-fr": 100}}
	st := newSeasonTracker(mock, showskip.New(time.Hour), seedDeps{})

	// Two languages forces the concurrent errgroup branch that reaches the
	// g.Wait() error check.
	st.shouldSkipShow(t.Context(), "tt1", 10, []string{"en", "fr"})

	const warnMsg = "show skip check error"
	if strings.Contains(buf.String(), warnMsg) {
		t.Errorf("shouldSkipShow emitted a spurious errgroup warning %q; log was:\n%s",
			warnMsg, buf.String())
	}
}

func TestRecordEpisodeOutcomes_counts_only_evidence(t *testing.T) {
	t.Parallel()
	series := &arrapi.Series{ImdbID: "tt4396196", TvdbID: 81189}
	tests := []struct {
		name    string
		outcome subflux.LangOutcome
		want    int
	}{
		{name: "answered with nothing", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangSearched, Searched: 1, Answered: 1}, want: 1},
		{name: "a download failed", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangSearched, Searched: 1, Answered: 1, Failed: 1}},
		{name: "no provider answered", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangSearched, Searched: 1}},
		{name: "found", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangSearched, Searched: 1, Answered: 1, Paths: []string{"/m/s.en.srt"}}},
		{name: "backed off", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangBackedOff}},
		{name: "a target write-blocked", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangSearched, Searched: 2, Answered: 1, WriteBlocked: 1}},
		{name: "the folder already unwritable", outcome: subflux.LangOutcome{Lang: "en", Kind: subflux.LangWriteBlocked, WriteBlocked: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newSeasonTracker(nil, showskip.New(time.Hour), seedDeps{})
			recordEpisodeOutcomes(t.Context(), st, series, 1, []subflux.LangOutcome{tc.outcome}, 10)
			got := 0
			if s := st.seasons[seasonKey{ImdbID: "tt4396196", Season: 1, Lang: "en"}]; s != nil {
				got = s.noResults
			}
			if got != tc.want {
				t.Errorf("no-result streak after %s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestRecordEpisodeOutcomes_failed_downloads_never_end_a_season(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(time.Hour), seedDeps{})
	series := &arrapi.Series{ImdbID: "tt4396196", TvdbID: 81189}
	failed := []subflux.LangOutcome{{Lang: "en", Kind: subflux.LangSearched, Searched: 1, Answered: 1, Failed: 1}}
	for range 10 {
		recordEpisodeOutcomes(t.Context(), st, series, 1, failed, 4)
	}
	if st.shouldSkipSeason("tt4396196", 1, "en") {
		t.Error("season early-terminated after failed downloads, want it kept open")
	}
}
