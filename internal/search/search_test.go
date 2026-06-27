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
	"pgregory.net/rapid"
)

// --- Tests ---

// --- New ---

func TestNew_creates_engine(t *testing.T) {
	t.Parallel()
	e := newEngine(nil, &mockStore{}, &mockConfig{}, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})
	if e == nil {
		t.Fatal("New() returned nil")
	}
}

// --- filterByVariant ---

func TestFilterByHI_regular_preferred_over_hi(t *testing.T) {
	t.Parallel()
	results := []api.Subtitle{
		{Provider: "os", ReleaseName: "Regular", HearingImp: false, Forced: false},
		{Provider: "os", ReleaseName: "HI-Sub", HearingImp: true, Forced: false},
	}
	got, varFallback := filterByVariant(results, "standard")
	if len(got) != 1 {
		t.Fatalf("filterByVariant() returned %d results, want 1 (regular only)", len(got))
	}
	if got[0].HearingImp {
		t.Error("filterByVariant() returned HI sub when regular available")
	}
	if varFallback {
		t.Error("filterByVariant() varFallback = true, want false (regular subs exist)")
	}
}

func TestFilterByHI_hi_fallback_when_no_regular(t *testing.T) {
	t.Parallel()
	results := []api.Subtitle{
		{Provider: "os", ReleaseName: "HI-Sub", HearingImp: true, Forced: false},
	}
	got, varFallback := filterByVariant(results, "standard")
	if len(got) != 1 {
		t.Fatalf("filterByVariant() returned %d results, want 1 (HI fallback)", len(got))
	}
	if !varFallback {
		t.Error("filterByVariant() varFallback = false, want true (no regular subs)")
	}
}

func TestFilterByHI_forced_excluded(t *testing.T) {
	t.Parallel()
	results := []api.Subtitle{
		{Provider: "os", ReleaseName: "Forced", Forced: true},
	}
	got, varFallback := filterByVariant(results, "standard")
	if len(got) != 0 {
		t.Errorf("filterByVariant() returned %d results, want 0 (forced excluded)", len(got))
	}
	if varFallback {
		t.Error("filterByVariant() varFallback = true, want false (no HI subs returned)")
	}
}

func TestFilterByHI_empty_input(t *testing.T) {
	t.Parallel()
	got, varFallback := filterByVariant(nil, "standard")
	if len(got) != 0 {
		t.Errorf("filterByVariant(nil) returned %d results, want 0", len(got))
	}
	if varFallback {
		t.Error("filterByVariant(nil) varFallback = true, want false")
	}
}

// --- recordProviderNoResults ---

func TestRecordProviderNoResults_adaptive_enabled_records(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{
			Enabled:           true,
			InitialDelay:      1,
			MaxDelay:          1,
			BackoffMultiplier: 1,
		},
		searchCfg: api.SearchConfig{},
	}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	e.recordProviderNoResults(context.Background(), "movie", "tt123", "fr", "Test Movie", []api.ProviderID{"prov1"})
	if !ms.failureCalled {
		t.Error("recordProviderNoResults() did not call RecordNoResult when adaptive enabled")
	}
}

func TestRecordProviderNoResults_adaptive_disabled_noop(t *testing.T) {
	t.Parallel()
	ms := &mockStore{}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: false},
		searchCfg:   api.SearchConfig{},
	}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	e.recordProviderNoResults(context.Background(), "movie", "tt123", "fr", "Test Movie", []api.ProviderID{"prov1"})
	if ms.failureCalled {
		t.Error("recordProviderNoResults() called RecordNoResult when adaptive disabled")
	}
}

// --- filterProviders ---

func TestFilterProviders_respects_target_include(t *testing.T) {
	t.Parallel()
	// Use a config that actually filters by target providers.
	mc := &mockFilterConfig{}
	p1 := &mockProvider{name: "os"}
	p2 := &mockProvider{name: "yify"}
	e := newEngine([]api.Provider{p1, p2}, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	target := &api.SubtitleTarget{Code: "fr", Providers: []api.ProviderID{"os"}}
	got := e.filterProviders(target)
	if len(got) != 1 {
		t.Fatalf("filterProviders() returned %d providers, want 1", len(got))
	}
	if got[0].Name() != "os" {
		t.Errorf("filterProviders()[0].Name() = %q, want %q", got[0].Name(), "os")
	}
}

// --- filterByScore boundary ---

func TestFilterByScore_exact_boundary(t *testing.T) {
	t.Parallel()
	scored := []scoredSub{
		{sub: api.Subtitle{Provider: "os"}, score: 100},
		{sub: api.Subtitle{Provider: "yify"}, score: 99},
		{sub: api.Subtitle{Provider: "test"}, score: 101},
	}

	// minScore=100: should include score=100 and score=101.
	got := filterByScore(scored, 100)
	if len(got) != 2 {
		t.Errorf("filterByScore(100) returned %d results, want 2", len(got))
	}

	// minScore=101: should include only score=101.
	got = filterByScore(scored, 101)
	if len(got) != 1 {
		t.Errorf("filterByScore(101) returned %d results, want 1", len(got))
	}

	// minScore=0: should include all.
	got = filterByScore(scored, 0)
	if len(got) != 3 {
		t.Errorf("filterByScore(0) returned %d results, want 3", len(got))
	}
}

// --- timeout ---

func TestEngine_timeout_set_when_enabled(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: time.Hour}}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	if e.timeout == nil {
		t.Error("Engine.timeout = nil, want non-nil when ProviderTimeout > 0")
	}
}

func TestEngine_timeout_noop_when_disabled(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: 0}}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	if _, ok := e.timeout.(noopHealth); !ok {
		t.Errorf("Engine.timeout = %T, want noopHealth when ProviderTimeout = 0", e.timeout)
	}
}

// --- ProviderTimeouts ---

func TestEngine_ProviderTimeouts_nil_timeout(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: 0}}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	status, ok := e.ProviderTimeouts()
	if ok {
		t.Error("ProviderTimeouts() ok = true, want false when timeout disabled")
	}
	if status != nil {
		t.Errorf("ProviderTimeouts() status = %v, want nil", status)
	}
}

func TestEngine_ProviderTimeouts_enabled(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: time.Hour}}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	status, ok := e.ProviderTimeouts()
	if !ok {
		t.Error("ProviderTimeouts() ok = false, want true when timeout enabled")
	}
	if status == nil {
		t.Error("ProviderTimeouts() status = nil, want non-nil")
	}
}

// --- ResetTimeouts ---

func TestEngine_ResetTimeouts_nil_timeout_noop(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: 0}}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	// Should not panic when timeout is nil.
	e.ResetTimeouts()
}

func TestEngine_ResetTimeouts_clears_state(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: time.Hour}}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	e.timeout.RecordFailure("prov1", nil)
	e.ResetTimeouts()

	status, _ := e.ProviderTimeouts()
	if len(status) != 0 {
		t.Errorf("ProviderTimeouts() after reset = %d entries, want 0", len(status))
	}
}

// --- ScoreSubtitles ---

func TestEngine_ScoreSubtitles_returns_sorted_results(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	sc := scorer.New(&api.DefaultScores)
	e := newEngine(nil, &mockStore{}, mc, nil, sc, Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP",
	}
	subs := []api.Subtitle{
		{ReleaseName: "Movie.2024.WEB-DL.720p-OTHER", MatchedBy: "title"},
		{ReleaseName: "Movie.2024.BluRay.1080p.x264-GRP", MatchedBy: "hash"},
	}

	results := e.ScoreSubtitles(req, subs)

	if len(results) != 2 {
		t.Fatalf("ScoreSubtitles() returned %d results, want 2", len(results))
	}
	if results[0].Score < results[1].Score {
		t.Errorf("ScoreSubtitles() not sorted: scores %d, %d",
			results[0].Score, results[1].Score)
	}
	// Hash-matched should be first (highest score).
	if results[0].Sub.MatchedBy != "hash" {
		t.Errorf("ScoreSubtitles()[0].Sub.MatchedBy = %q, want %q",
			results[0].Sub.MatchedBy, "hash")
	}
}

// --- SimulateScore ---

func TestEngine_SimulateScore_hash_match(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{minScore: 0}
	sc := scorer.New(&api.DefaultScores)
	e := newEngine(nil, &mockStore{}, mc, nil, sc, Syncer{}, noopDetector{})

	result := e.SimulateScore("movie",
		"Movie.2024.BluRay.1080p.x264-GRP",
		"Movie.2024.BluRay.1080p.x264-GRP",
		"hash")

	if result.Score != 100 {
		t.Errorf("SimulateScore(hash).Score = %d, want 100", result.Score)
	}
	if result.Tier == "" {
		t.Error("SimulateScore(hash).Tier is empty, want non-empty")
	}
	if result.ScoreNoHash != 0 {
		t.Errorf("SimulateScore(hash).ScoreNoHash = %d, want 0", result.ScoreNoHash)
	}
}

func TestEngine_SimulateScore_release_attributes(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{minScore: 0}
	sc := scorer.New(&api.DefaultScores)
	e := newEngine(nil, &mockStore{}, mc, nil, sc, Syncer{}, noopDetector{})

	result := e.SimulateScore("movie",
		"Movie.2024.BluRay.1080p.x264-GRP",
		"Different.2024.WEB-DL.720p-OTHER",
		"title")

	// No release attributes match, score should be 0.
	if result.Score != 0 {
		t.Errorf("SimulateScore(no match).Score = %d, want 0", result.Score)
	}
}

// --- filterBackedOff error path ---

// mockStoreWithBackoffError returns an error from BackedOffProviders.
type mockStoreWithBackoffError struct {
	mockStore
}

func (m *mockStoreWithBackoffError) BackedOffProviders(_ context.Context, _ api.MediaType, _, _ string, _ int) ([]api.ProviderID, error) {
	return nil, errors.New("db error")
}

// --- filterByScore ---

func TestFilterByScore_empty_input(t *testing.T) {
	t.Parallel()
	got := filterByScore(nil, 0)
	if len(got) != 0 {
		t.Errorf("filterByScore(nil, 0) returned %d results, want 0", len(got))
	}
}

func TestFilterByScore_all_below(t *testing.T) {
	t.Parallel()
	scored := []scoredSub{
		{sub: api.Subtitle{Provider: "os"}, score: 10},
		{sub: api.Subtitle{Provider: "yify"}, score: 20},
	}
	got := filterByScore(scored, 100)
	if len(got) != 0 {
		t.Errorf("filterByScore(100) returned %d results, want 0", len(got))
	}
}

// --- recordProviderNoResults error path ---

// mockStoreWithRecordError returns an error from RecordNoResult.
type mockStoreWithRecordError struct {
	mockStore
}

func (m *mockStoreWithRecordError) RecordNoResult(_ context.Context, _ api.MediaType, _, _ string, _ api.ProviderID, _ api.BackoffParams) error {
	return errors.New("db write error")
}

func TestRecordProviderNoResults_store_error_continues(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{
			Enabled:           true,
			InitialDelay:      1,
			MaxDelay:          1,
			BackoffMultiplier: 1,
		},
		searchCfg: api.SearchConfig{},
	}
	errStore := &mockStoreWithRecordError{}
	e := newEngine(nil, errStore, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	// Should not panic even when store returns error.
	e.recordProviderNoResults(context.Background(), "movie", "tt123", "fr", "Test Movie", []api.ProviderID{"prov1", "prov2"})
}

// --- checkUpgradeEligibility ---

func TestCheckUpgradeEligibility_perfect_score_not_eligible(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	srtPath := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(srtPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := &mockStoreWithScore{
		score: 100, mediaImported: time.Now(), found: true,
	}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{
			UpgradeEnabled:    true,
			UpgradeWindowDays: 7,
		},
	}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	existing := detectExisting(context.Background(), videoPath, noopDetector{}, nil)
	searchCfg := mc.Search()
	cutoff := time.Now().AddDate(0, 0, -searchCfg.UpgradeWindowDays)

	score, eligible := e.checkUpgradeEligibility(context.Background(),
		&existing, &searchCfg, "movie", "tt123", "fr", "standard", "Test", cutoff)

	if eligible {
		t.Error("checkUpgradeEligibility() eligible = true, want false (score=100)")
	}
	if score != 0 {
		t.Errorf("checkUpgradeEligibility() score = %d, want 0", score)
	}
}

func TestCheckUpgradeEligibility_no_store_record_not_eligible(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	srtPath := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(srtPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := &mockStoreWithScore{
		score: 0, mediaImported: time.Time{}, found: false,
	}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{
			UpgradeEnabled:    true,
			UpgradeWindowDays: 7,
		},
	}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	existing := detectExisting(context.Background(), videoPath, noopDetector{}, nil)
	searchCfg := mc.Search()
	cutoff := time.Now().AddDate(0, 0, -searchCfg.UpgradeWindowDays)

	_, eligible := e.checkUpgradeEligibility(context.Background(),
		&existing, &searchCfg, "movie", "tt123", "fr", "standard", "Test", cutoff)

	if eligible {
		t.Error("checkUpgradeEligibility() eligible = true, want false (not found in store)")
	}
}

// --- Property-based tests ---

func TestFilterByHI_never_returns_forced(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 10).Draw(t, "n")
		subs := make([]api.Subtitle, n)
		for i := range n {
			subs[i] = api.Subtitle{
				Provider:   "test",
				HearingImp: rapid.Bool().Draw(t, "hi"),
				Forced:     rapid.Bool().Draw(t, "forced"),
			}
		}

		got, _ := filterByVariant(subs, "standard")
		for _, s := range got {
			if s.Forced {
				t.Error("filterByVariant() returned a forced subtitle")
			}
		}
	})
}

func TestFilterByHI_prefers_regular_over_hi(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "n")
		subs := make([]api.Subtitle, n)
		hasRegular := false
		for i := range n {
			subs[i] = api.Subtitle{
				Provider:   "test",
				HearingImp: rapid.Bool().Draw(t, "hi"),
				Forced:     false, // No forced subs to simplify.
			}
			if !subs[i].HearingImp {
				hasRegular = true
			}
		}

		got, varFallback := filterByVariant(subs, "standard")
		if hasRegular {
			// When regular subs exist, no HI subs should be returned.
			for _, s := range got {
				if s.HearingImp {
					t.Error("filterByVariant() returned HI sub when regular available")
				}
			}
			if varFallback {
				t.Error("filterByVariant() varFallback = true when regular subs exist")
			}
		}
	})
}

func TestFilterByScore_preserves_order(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 10).Draw(t, "n")
		scored := make([]scoredSub, n)
		for i := range n {
			scored[i] = scoredSub{
				sub:   api.Subtitle{Provider: "test"},
				score: rapid.IntRange(0, 200).Draw(t, "score"),
			}
		}
		minScore := rapid.IntRange(0, 200).Draw(t, "min")

		got := filterByScore(scored, minScore)

		// All returned scores must be >= minScore.
		for _, s := range got {
			if s.score < minScore {
				t.Errorf("filterByScore(%d) returned score %d", minScore, s.score)
			}
		}

		// Count should match manual count.
		expected := 0
		for _, s := range scored {
			if s.score >= minScore {
				expected++
			}
		}
		if len(got) != expected {
			t.Errorf("filterByScore(%d) returned %d, want %d", minScore, len(got), expected)
		}
	})
}

// --- Engine.HashFile wrapper ---

func TestEngine_HashFile_delegates_to_hashFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mkv")
	data := make([]byte, hashBlockSize*2)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mc := &mockConfig{}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	hash, size, err := e.HashFile(t.Context(), path)
	if err != nil {
		t.Fatalf("HashFile() unexpected error: %v", err)
	}
	if size != hashBlockSize*2 {
		t.Errorf("HashFile() size = %d, want %d", size, hashBlockSize*2)
	}
	if hash != "0000000000020000" {
		t.Errorf("HashFile() = %q, want %q", hash, "0000000000020000")
	}
}

func TestEngine_HashFile_error_propagates(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	_, _, err := e.HashFile(t.Context(), "/nonexistent/path/video.mkv")
	if err == nil {
		t.Fatal("HashFile(nonexistent) expected error, got nil")
	}
}

// --- downloadAndSave error paths ---

// mockStoreWithSaveError returns an error from SaveDownload.
type mockStoreWithSaveError struct {
	mockStore

	saveCalled bool
}

func (m *mockStoreWithSaveError) SaveDownload(_ context.Context, _ *api.DownloadRecord) error {
	m.saveCalled = true
	return errors.New("db write error")
}

// --- StripHI and hash match edge cases ---

// mockConfigWithStripHI enables StripHI in PostProcessConfig.
type mockConfigWithStripHI struct {
	mockConfig
}

func (m *mockConfigWithStripHI) PostProcessConfig() api.PostProcessConfig {
	return api.PostProcessConfig{
		NormalizeUTF8:    true,
		NormalizeEndings: true,
		CleanWhitespace:  true,
		RemoveEmpty:      true,
		StripTags:        true,
		StripHI:          true,
	}
}

// --- filterByVariant forced/hi edge cases ---

func TestFilterByVariant_forced_empty_input(t *testing.T) {
	t.Parallel()
	got, fallback := filterByVariant(nil, "forced")
	if len(got) != 0 {
		t.Errorf("filterByVariant(nil, forced) returned %d results, want 0", len(got))
	}
	if fallback {
		t.Error("filterByVariant(nil, forced) fallback = true, want false")
	}
}

func TestFilterByVariant_hi_empty_input(t *testing.T) {
	t.Parallel()
	got, fallback := filterByVariant(nil, "hi")
	if len(got) != 0 {
		t.Errorf("filterByVariant(nil, hi) returned %d results, want 0", len(got))
	}
	if fallback {
		t.Error("filterByVariant(nil, hi) fallback = true, want false")
	}
}

// --- PBT: filterByVariant forced and hi ---

func TestFilterByVariant_forced_never_returns_non_forced(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 10).Draw(t, "n")
		subs := make([]api.Subtitle, n)
		for i := range n {
			subs[i] = api.Subtitle{
				Provider:   "test",
				HearingImp: rapid.Bool().Draw(t, "hi"),
				Forced:     rapid.Bool().Draw(t, "forced"),
			}
		}

		got, _ := filterByVariant(subs, "forced")
		for _, s := range got {
			if !s.Forced {
				t.Error("filterByVariant(forced) returned a non-forced subtitle")
			}
		}
	})
}

func TestFilterByVariant_hi_never_returns_forced(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 10).Draw(t, "n")
		subs := make([]api.Subtitle, n)
		for i := range n {
			subs[i] = api.Subtitle{
				Provider:   "test",
				HearingImp: rapid.Bool().Draw(t, "hi"),
				Forced:     rapid.Bool().Draw(t, "forced"),
			}
		}

		got, _ := filterByVariant(subs, "hi")
		for _, s := range got {
			if s.Forced {
				t.Error("filterByVariant(hi) returned a forced subtitle")
			}
			if !s.HearingImp {
				t.Error("filterByVariant(hi) returned a non-HI subtitle")
			}
		}
	})
}
