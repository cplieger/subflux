package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/scorer"
)

// --- SearchTargets ---

func TestSearchTargets_manually_locked_skips(t *testing.T) {
	t.Parallel()
	ms := &mockStore{manualLocked: true}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
}

func TestSearchTargets_adaptive_backoff_skips(t *testing.T) {
	t.Parallel()
	backedStore := &mockStoreWithBackoff{
		mockStore: mockStore{},
		backedOff: []api.ProviderID{"test"},
	}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true, MaxAttempts: 5},
		searchCfg:   api.SearchConfig{},
	}
	metrics := &mockMetrics{}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, backedStore, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
	if metrics.adaptiveSkips.Load() != 1 {
		t.Errorf("adaptiveSkips = %d, want 1", metrics.adaptiveSkips.Load())
	}
}

func TestSearchTargets_no_results_records_failure(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true},
		searchCfg:   api.SearchConfig{},
	}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
	if !ms.failureCalled {
		t.Error("RecordNoResult not called after no results")
	}
}

func TestSearchTargets_success_downloads_and_saves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  0,
	}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"},
		},
		data: subData,
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 1 {
		t.Fatalf("SearchTargets() returned %d paths, want 1", len(result.Paths()))
	}
	if _, err := os.Stat(result.Paths()[0]); err != nil {
		t.Errorf("subtitle file not found at %q: %v", result.Paths()[0], err)
	}
	if !ms.successCalled {
		t.Error("SaveDownload not called")
	}
}

func TestSearchTargets_hi_fallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  0,
	}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			// Only HI subs available, no regular.
			{
				Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb",
				Language: "fr", HearingImp: true,
			},
		},
		data: subData,
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 1 {
		t.Fatalf("SearchTargets() returned %d paths, want 1 (HI fallback)", len(result.Paths()))
	}
}

func TestSearchTargets_forced_subs_filtered_out(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true},
		searchCfg:   api.SearchConfig{},
		minScore:    0,
	}
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			// Only forced subs - should be filtered out.
			{
				Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb",
				Language: "fr", Forced: true,
			},
		},
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
	if !ms.failureCalled {
		t.Error("RecordNoResult not called after forced-only results")
	}
}

func TestSearchTargets_below_min_score(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true},
		searchCfg:   api.SearchConfig{},
		minScore:    9999,
	}
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "title"},
		},
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
}

// --- searchProvidersFiltered ---

func TestSearchProvidersFiltered_error_handling(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	metrics := &mockMetrics{}

	goodProv := &mockProvider{
		name: "good",
		results: []api.Subtitle{
			{Provider: "good", ReleaseName: "Movie-GRP"},
		},
	}
	badProv := &mockProvider{
		name:      "bad",
		searchErr: errors.New("provider error"),
	}
	e := newEngine([]api.Provider{goodProv, badProv}, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		Languages: []string{"fr"},
	}

	outcome := e.searchProvidersFiltered(context.Background(), req,
		[]api.Provider{goodProv, badProv})

	if len(outcome.results) != 1 {
		t.Errorf("searchProvidersFiltered() returned %d results, want 1", len(outcome.results))
	}
	if len(outcome.succeeded()) != 1 || outcome.succeeded()[0] != "good" {
		t.Errorf("succeeded = %v, want [good]", outcome.succeeded())
	}
	if errored := outcome.errored(); len(errored) != 1 || errored[0] != "bad" {
		t.Errorf("errored = %v, want [bad]", errored)
	}
	if metrics.searches.Load() != 2 {
		t.Errorf("metrics.searches = %d, want 2", metrics.searches.Load())
	}
}

func TestSearchProvidersFiltered_all_failed(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	metrics := &mockMetrics{}

	bad1 := &mockProvider{name: "bad1", searchErr: errors.New("timeout")}
	bad2 := &mockProvider{name: "bad2", searchErr: errors.New("connection refused")}
	e := newEngine([]api.Provider{bad1, bad2}, &mockStore{}, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		Languages: []string{"fr"},
	}

	outcome := e.searchProvidersFiltered(context.Background(), req,
		[]api.Provider{bad1, bad2})

	if len(outcome.results) != 0 {
		t.Errorf("results = %d, want 0", len(outcome.results))
	}
	if len(outcome.succeeded()) != 0 {
		t.Errorf("succeeded = %v, want empty", outcome.succeeded())
	}
	if errored := outcome.errored(); len(errored) != 2 {
		t.Errorf("errored = %v, want 2 entries", errored)
	}
}

// --- targetLocked ---

func TestTargetLocked_manual_lock_true(t *testing.T) {
	t.Parallel()
	ms := &mockStore{manualLocked: true}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	if !e.targetLocked(context.Background(), "movie", "tt123", "Test", "fr", api.VariantStandard) {
		t.Error("targetLocked() = false for locked quad, want true")
	}
}

func TestTargetLocked_not_locked_false(t *testing.T) {
	t.Parallel()
	ms := &mockStore{manualLocked: false}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	if e.targetLocked(context.Background(), "movie", "tt123", "Test", "fr", api.VariantStandard) {
		t.Error("targetLocked() = true, want false (not locked)")
	}
}

// TestTargetLocked_store_error_fails_closed asserts a lock-check error is
// treated as locked (fail closed), matching the store's own stance.
func TestTargetLocked_store_error_fails_closed(t *testing.T) {
	t.Parallel()
	ms := &mockStoreLockErr{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	if !e.targetLocked(context.Background(), "movie", "tt123", "Test", "fr", api.VariantStandard) {
		t.Error("targetLocked() = false on store error, want true (fail closed)")
	}
}

// --- filterBackedOff ---

func TestFilterBackedOff_removes_backed_off_providers(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true, MaxAttempts: 5},
		searchCfg:   api.SearchConfig{},
	}
	metrics := &mockMetrics{}
	// Use a mock store that returns "prov1" as backed off.
	backedStore := &mockStoreWithBackoff{
		mockStore: *ms,
		backedOff: []api.ProviderID{"prov1"},
	}
	e := newEngine(nil, backedStore, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	prov1 := &mockProvider{name: "prov1"}
	prov2 := &mockProvider{name: "prov2"}
	result := e.filterBackedOff(context.Background(), "movie", "tt123", "fr",
		[]api.Provider{prov1, prov2})

	if len(result) != 1 {
		t.Fatalf("filterBackedOff() returned %d providers, want 1", len(result))
	}
	if result[0].Name() != "prov2" {
		t.Errorf("remaining provider = %q, want %q", result[0].Name(), "prov2")
	}
	if metrics.adaptiveSkips.Load() != 1 {
		t.Errorf("adaptiveSkips = %d, want 1", metrics.adaptiveSkips.Load())
	}
}

// --- SearchTargets existing subtitle check ---

func TestSearchTargets_existing_regular_subtitle_skips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create an existing subtitle.
	srtPath := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(srtPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  0,
	}
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb"},
		},
		data: []byte("new subtitle"),
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	// Should skip because subtitle already exists.
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
}

func TestSearchTargets_download_failure_continues(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  0,
	}
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"},
		},
		downloadErr: errors.New("network error"),
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
}

func TestSearchTargets_all_providers_failed_skips_backoff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
	}
	p := &mockProvider{name: "test", searchErr: errors.New("api down")}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
	if ms.failureCalled {
		t.Error("RecordNoResult called, want no backoff for errored providers")
	}
}

func TestSearchTargets_partial_failure_records_for_succeeded_only(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true},
		searchCfg:   api.SearchConfig{},
	}
	good := &mockProvider{name: "good", results: nil} // succeeds with no results
	bad := &mockProvider{name: "bad", searchErr: errors.New("api down")}
	e := newEngine([]api.Provider{good, bad}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths())
	}
	// The "good" provider responded with no results, so backoff is recorded.
	if !ms.failureCalled {
		t.Error("RecordNoResult not called for succeeded provider with no results")
	}
}

func TestSearchTargets_exact_min_score_is_accepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"},
		},
		data: subData,
	}

	// Score the result to find the exact score, then set minScore to match.
	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	video := videoInfoFromRequest(req)
	scored := scoreResults(scorer.New(&api.DefaultScores), &video, p.results, noPriority)
	exactScore := scored[0].score

	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  exactScore,
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	targets := []api.SubtitleTarget{{Code: "fr"}}
	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 1 {
		t.Errorf("SearchTargets() returned %d paths, want 1 (exact min score)", len(result.Paths()))
	}
}

func TestSearchTargets_context_cancelled_returns_error(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
	}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}, {Code: "en"}}

	_, err := e.SearchTargets(ctx, req, "", targets)
	if err == nil {
		t.Fatal("SearchTargets() expected error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("SearchTargets() error = %v, want context.Canceled", err)
	}
}

func TestFilterBackedOff_query_error_returns_all_providers(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true, MaxAttempts: 5},
		searchCfg:   api.SearchConfig{},
	}
	errStore := &mockStoreWithBackoffError{}
	e := newEngine(nil, errStore, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	prov1 := &mockProvider{name: "prov1"}
	prov2 := &mockProvider{name: "prov2"}
	result := e.filterBackedOff(context.Background(), "movie", "tt123", "fr",
		[]api.Provider{prov1, prov2})

	if len(result) != 2 {
		t.Errorf("filterBackedOff() returned %d providers, want 2 (error fallback)", len(result))
	}
}

func TestFilterBackedOff_adaptive_disabled_returns_all(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: false},
		searchCfg:   api.SearchConfig{},
	}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	prov1 := &mockProvider{name: "prov1"}
	result := e.filterBackedOff(context.Background(), "movie", "tt123", "fr", []api.Provider{prov1})

	if len(result) != 1 {
		t.Errorf("filterBackedOff() returned %d providers, want 1 (disabled)", len(result))
	}
}

func TestFilterBackedOff_no_backed_off_returns_all(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true, MaxAttempts: 5},
		searchCfg:   api.SearchConfig{},
	}
	// mockStore.BackedOffProviders returns nil, nil by default.
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	prov1 := &mockProvider{name: "prov1"}
	result := e.filterBackedOff(context.Background(), "movie", "tt123", "fr", []api.Provider{prov1})

	if len(result) != 1 {
		t.Errorf("filterBackedOff() returned %d providers, want 1 (none backed off)", len(result))
	}
}

// --- searchProvidersFiltered with timeout ---

func TestSearchProvidersFiltered_records_timeout_on_failure(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{
		ProviderTimeout: time.Hour,
	}}
	metrics := &mockMetrics{}
	bad := &mockProvider{name: "bad", searchErr: errors.New("timeout")}
	e := newEngine([]api.Provider{bad}, &mockStore{}, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", Languages: []string{"fr"}}
	outcome := e.searchProvidersFiltered(context.Background(), req, []api.Provider{bad})

	if errored := outcome.errored(); len(errored) != 1 {
		t.Errorf("errored = %v, want 1 entry", errored)
	}

	// The provider timeout tracker should have recorded the failure.
	if e.timeout == nil {
		t.Fatal("timeout tracker is nil")
	}
	// Not yet timed out (threshold=5, only 1 failure).
	if e.timeout.IsTimedOut("bad") {
		t.Error("provider should not be timed out after 1 failure")
	}
}

func TestSearchProvidersFiltered_records_success_clears_timeout(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{
		ProviderTimeout: time.Hour,
	}}
	good := &mockProvider{name: "good", results: []api.Subtitle{
		{Provider: "good", ReleaseName: "Movie-GRP"},
	}}
	e := newEngine([]api.Provider{good}, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	// Pre-record some failures.
	e.timeout.RecordFailure("good", nil)
	e.timeout.RecordFailure("good", nil)

	req := &api.SearchRequest{MediaType: "movie", Languages: []string{"fr"}}
	outcome := e.searchProvidersFiltered(context.Background(), req, []api.Provider{good})

	if len(outcome.succeeded()) != 1 {
		t.Errorf("succeeded = %v, want 1 entry", outcome.succeeded())
	}
	// Success should have cleared the failure history.
	status := e.timeout.Status()
	if _, ok := status["good"]; ok {
		t.Error("provider should not be in status after success")
	}
}

func TestSearchProvidersFiltered_skips_timed_out_provider(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{
		ProviderTimeout: time.Hour,
	}}
	p := &mockProvider{name: "timed-out", results: []api.Subtitle{
		{Provider: "timed-out", ReleaseName: "Movie-GRP"},
	}}
	e := newEngine([]api.Provider{p}, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	// Trip the provider timeout (threshold=5).
	for range 5 {
		e.timeout.RecordFailure("timed-out", nil)
	}

	req := &api.SearchRequest{MediaType: "movie", Languages: []string{"fr"}}
	outcome := e.searchProvidersFiltered(context.Background(), req, []api.Provider{p})

	if len(outcome.results) != 0 {
		t.Errorf("results = %d, want 0 (provider timed out)", len(outcome.results))
	}
	if len(outcome.succeeded()) != 0 {
		t.Errorf("succeeded = %v, want empty", outcome.succeeded())
	}
	if errored := outcome.errored(); len(errored) != 0 {
		t.Errorf("errored = %v, want empty (skipped, not errored)", errored)
	}
}

// --- SearchTargets video hash computation ---

func TestSearchTargets_computes_video_hash_when_empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create a file large enough for hashing.
	data := make([]byte, hashBlockSize*2)
	if err := os.WriteFile(videoPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
	}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
		// VideoHash intentionally empty — should be computed.
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	_, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if req.VideoHash == "" {
		t.Error("SearchTargets() did not compute VideoHash")
	}
	if req.VideoSize != hashBlockSize*2 {
		t.Errorf("SearchTargets() VideoSize = %d, want %d", req.VideoSize, hashBlockSize*2)
	}
}

func TestSearchTargets_skips_hash_when_already_set(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
	}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
		VideoHash: "prehashed",
		VideoSize: 12345,
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	_, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if req.VideoHash != "prehashed" {
		t.Errorf("SearchTargets() changed VideoHash to %q, want %q", req.VideoHash, "prehashed")
	}
}

// --- SearchTargets searched/skipped counters ---

func TestSearchTargets_counts_searched_and_skipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create existing subtitle for "fr" so it gets skipped.
	srtPath := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(srtPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true},
		searchCfg:   api.SearchConfig{},
	}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	targets := []api.SubtitleTarget{{Code: "fr"}, {Code: "en"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	// "fr" should be skipped (existing sub), "en" should be searched.
	kinds := map[string]api.LangOutcomeKind{}
	for _, o := range result.Langs {
		kinds[o.Lang] = o.Kind
	}
	if kinds["fr"] != api.LangSkipped {
		t.Errorf("fr outcome = %v, want %v", kinds["fr"], api.LangSkipped)
	}
	if kinds["en"] != api.LangSearched {
		t.Errorf("en outcome = %v, want %v", kinds["en"], api.LangSearched)
	}
	if got := result.TargetsSearched(); got != 1 {
		t.Errorf("SearchTargets().TargetsSearched() = %d, want 1", got)
	}
}
