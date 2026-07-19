package scanning

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// mockShowCounter implements api.ShowSubtitleCounter for testing.
type mockShowCounter struct {
	counts map[string]int
	err    error
	calls  atomic.Int32
}

func (m *mockShowCounter) CountShowSubtitles(_ context.Context, imdbID, lang string) (int, error) {
	m.calls.Add(1)
	if m.err != nil {
		return 0, m.err
	}
	return m.counts[imdbID+"-"+lang], nil
}

func (m *mockShowCounter) Name() api.ProviderID { return "opensubtitles" }
func (m *mockShowCounter) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (m *mockShowCounter) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, nil
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
			got := st.shouldSkipShow(context.Background(), tc.imdb, tc.episodes, tc.langs)
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
	ctx := context.Background()
	st.shouldSkipShow(ctx, "tt123", 100, []string{"fr"})
	st.shouldSkipShow(ctx, "tt123", 100, []string{"fr"})
	if int(mock.calls.Load()) != 1 {
		t.Fatalf("expected 1 API call (cached), got %d", int(mock.calls.Load()))
	}
}

func TestSeasonTracker_no_early_stop_below_minimum(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("should not skip after only 2 no-results (minimum is 3)")
	}
}

func TestSeasonTracker_early_stop_at_minimum(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if !st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("expected skip after 3 consecutive no-results")
	}
}

func TestSeasonTracker_early_stop_large_season(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 5 {
		st.recordOutcome(context.Background(), "tt121220", 3, "fr", "", ScanNoResult, 33)
	}
	if st.shouldSkipSeason("tt121220", 3, "fr") {
		t.Fatal("should not skip after only 5 no-results (threshold is 6)")
	}
	st.recordOutcome(context.Background(), "tt121220", 3, "fr", "", ScanNoResult, 33)
	if !st.shouldSkipSeason("tt121220", 3, "fr") {
		t.Fatal("expected skip after 6 consecutive no-results")
	}
}

func TestSeasonTracker_found_resets_streak(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanFound, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("should not skip: found reset the streak")
	}
}

func TestSeasonTracker_skipped_does_not_affect_streak(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanSkipped, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	if !st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("expected skip: skipped doesn't reset streak, 3 no-results reached")
	}
}

func TestSeasonTracker_independent_seasons(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	}
	if st.shouldSkipSeason("tt1", 2, "fr") {
		t.Fatal("season 2 should not be affected by season 1")
	}
}

func TestSeasonTracker_independent_languages(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
	}
	if st.shouldSkipSeason("tt1", 1, "en") {
		t.Fatal("en should not be affected by fr early stop")
	}
}

func TestShouldSkipEpisode_all_langs_stopped(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
		st.recordOutcome(context.Background(), "tt1", 1, "en", "", ScanNoResult, 10)
	}
	if !st.shouldSkipEpisode("tt1", 1, []string{"fr", "en"}) {
		t.Fatal("expected skip: both languages hit early stop")
	}
}

func TestShouldSkipEpisode_one_lang_still_active(t *testing.T) {
	t.Parallel()
	st := newSeasonTracker(nil, showskip.New(1*time.Hour), seedDeps{})
	for range 3 {
		st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 10)
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
		st.recordOutcome(context.Background(), "tt1", 1, "fr", "", ScanNoResult, 0)
	}
	if !st.shouldSkipSeason("tt1", 1, "fr") {
		t.Fatal("expected skip after 3 no-results with zero season count")
	}
}

func TestResolveShowCounter_picks_first(t *testing.T) {
	t.Parallel()
	first := &mockShowCounter{counts: map[string]int{"tt1-fr": 0}}
	second := &mockShowCounter{counts: map[string]int{"tt1-fr": 99}}
	counter := provider.ResolveShowCounter([]api.Provider{first, second})
	st := newSeasonTracker(counter, showskip.New(1*time.Hour), seedDeps{})
	count, _ := st.counter.CountShowSubtitles(context.Background(), "tt1", "fr")
	if count != 0 {
		t.Fatalf("expected count 0 from first provider, got %d", count)
	}
}

// showLevelSkip uses an inclusive threshold: a show is skipped when the
// available subtitle count is at or below episodeCount * showSkipThresholdPct.
// With 10 episodes the threshold is 2, so a count of exactly 2 still skips.
func TestShowLevelSkip_count_equals_threshold(t *testing.T) {
	t.Parallel()
	mock := &mockShowCounter{counts: map[string]int{"tt1-en": 2}}
	st := newSeasonTracker(mock, showskip.New(time.Hour), seedDeps{})

	got := st.showLevelSkip(context.Background(), "tt1", 10, "en")

	if !got {
		t.Errorf("showLevelSkip(count==threshold) = false, want true")
	}
}

// The multi-language show-skip path runs per-language checks in an errgroup
// whose goroutines never return an error, so it must not emit the
// "show skip check error" warning on a clean run.
func TestShouldSkipShow_no_spurious_errgroup_warn(t *testing.T) {
	// No t.Parallel: this test swaps the global slog default logger.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	mock := &mockShowCounter{counts: map[string]int{"tt1-en": 100, "tt1-fr": 100}}
	st := newSeasonTracker(mock, showskip.New(time.Hour), seedDeps{})

	// Two languages forces the concurrent errgroup branch that reaches the
	// g.Wait() error check.
	st.shouldSkipShow(context.Background(), "tt1", 10, []string{"en", "fr"})

	const warnMsg = "show skip check error"
	if strings.Contains(buf.String(), warnMsg) {
		t.Errorf("shouldSkipShow emitted a spurious errgroup warning %q; log was:\n%s",
			warnMsg, buf.String())
	}
}
