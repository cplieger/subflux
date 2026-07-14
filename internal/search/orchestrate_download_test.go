package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/scorer"
)

func TestSearchTargets_save_download_error_still_returns_path(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStoreWithSaveError{}
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
	// Path should still be returned even when SaveDownload fails (it's a warning).
	if len(result.Paths) != 1 {
		t.Fatalf("SearchTargets() returned %d paths, want 1 (SaveDownload error is non-fatal)", len(result.Paths))
	}
	if ms.saveCalled != true {
		t.Error("SaveDownload not called")
	}
}

func TestSearchTargets_atomic_write_error_returns_empty(t *testing.T) {
	t.Parallel()
	// Use a videoPath in a non-existent directory so AtomicWriteFile fails.
	videoPath := filepath.Join(t.TempDir(), "nonexistent", "subdir", "movie.mkv")

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
	// AtomicWriteFile should fail, so no paths returned.
	if len(result.Paths) != 0 {
		t.Errorf("SearchTargets() = %v, want empty (write error)", result.Paths)
	}
	// SaveDownload should NOT be called since write failed.
	if ms.successCalled {
		t.Error("SaveDownload called after write error, want not called")
	}
}

// --- searchProvidersFiltered: embedded provider path ---

func TestSearchProvidersFiltered_embedded_provider_success(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: time.Hour}}
	metrics := &mockMetrics{}
	embProv := &mockProvider{
		name: "embedded",
		results: []api.Subtitle{
			{Provider: "embedded", ReleaseName: "embedded-track", Language: "en"},
		},
	}
	e := newEngine([]api.Provider{embProv}, &mockStore{}, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", Languages: []string{"en"}}
	outcome := e.searchProvidersFiltered(context.Background(), req, []api.Provider{embProv})

	if len(outcome.results) != 1 {
		t.Errorf("results = %d, want 1", len(outcome.results))
	}
	if s := outcome.succeeded(); len(s) != 1 || s[0] != "embedded" {
		t.Errorf("succeeded = %v, want [embedded]", s)
	}
	if metrics.searches.Load() != 1 {
		t.Errorf("metrics.searches = %d, want 1", metrics.searches.Load())
	}
}

func TestSearchProvidersFiltered_embedded_provider_error(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{ProviderTimeout: time.Hour}}
	metrics := &mockMetrics{}
	embProv := &mockProvider{
		name:      "embedded",
		searchErr: errors.New("ffprobe not found"),
	}
	e := newEngine([]api.Provider{embProv}, &mockStore{}, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", Languages: []string{"en"}}
	outcome := e.searchProvidersFiltered(context.Background(), req, []api.Provider{embProv})

	if len(outcome.results) != 0 {
		t.Errorf("results = %d, want 0", len(outcome.results))
	}
	if errored := outcome.errored(); len(errored) != 1 || errored[0] != "embedded" {
		t.Errorf("errored = %v, want [embedded]", errored)
	}
	if len(outcome.succeeded()) != 0 {
		t.Errorf("succeeded = %v, want empty", outcome.succeeded())
	}
	if metrics.searches.Load() != 1 {
		t.Errorf("metrics.searches = %d, want 1", metrics.searches.Load())
	}
}

// --- SearchTargets: binary data rejection ---

func TestSearchTargets_binary_data_rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  0,
	}
	// RAR magic bytes — ValidateSubtitleData rejects this.
	rarData := []byte("Rar!\x1a\x07\x00" + strings.Repeat("\x00", 100))
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"},
		},
		data: rarData,
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
	// Binary data should be rejected, no subtitle saved.
	if len(result.Paths) != 0 {
		t.Errorf("SearchTargets() = %v, want empty (binary data rejected)", result.Paths)
	}
}

// --- filterByVariant: forced and hi variants ---

// --- SearchTargets: ForceUpgrade paths ---

func TestSearchTargets_force_upgrade_with_external_sub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create an existing external subtitle.
	srtPath := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(srtPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := &mockStoreWithScore{
		score: 30, mediaImported: time.Now(), found: true,
	}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{
			UpgradeEnabled: false,
		},
		minScore: 0,
	}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nBetter\r\n")
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie.2024.BluRay.x264-GRP", MatchedBy: "imdb", Language: "fr"},
		},
		data: subData,
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:    "movie",
		ImdbID:       "tt123",
		ReleaseName:  "Movie.2024.BluRay.x264-GRP",
		ForceUpgrade: true,
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Errorf("SearchTargets(ForceUpgrade) returned %d paths, want 1", len(result.Paths))
	}
}

func TestSearchTargets_force_upgrade_skips_embedded_only(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{
			UpgradeEnabled: false,
		},
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
		MediaType:    "movie",
		ImdbID:       "tt123",
		ReleaseName:  "Movie-GRP",
		ForceUpgrade: true,
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Errorf("SearchTargets(ForceUpgrade, no existing) returned %d paths, want 1", len(result.Paths))
	}
}

// --- SearchTargets: variant-specific download paths ---

func TestSearchTargets_hi_variant_preserves_hi_flag(t *testing.T) {
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
	targets := []api.SubtitleTarget{{Code: "fr", Variant: "hi"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("SearchTargets(hi variant) returned %d paths, want 1", len(result.Paths))
	}
	if !strings.Contains(result.Paths[0], ".hi.") {
		t.Errorf("SearchTargets(hi variant) path = %q, want containing '.hi.'", result.Paths[0])
	}
}

func TestSearchTargets_forced_variant_downloads_forced(t *testing.T) {
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
			{
				Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb",
				Language: "fr", Forced: true,
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
	targets := []api.SubtitleTarget{{Code: "fr", Variant: "forced"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("SearchTargets(forced variant) returned %d paths, want 1", len(result.Paths))
	}
	if !strings.Contains(result.Paths[0], ".forced.") {
		t.Errorf("SearchTargets(forced variant) path = %q, want containing '.forced.'", result.Paths[0])
	}
}

func TestSearchTargets_strip_hi_standard_variant_removes_hi_flag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &mockStore{}
	mc := &mockConfigWithStripHI{mockConfig: mockConfig{minScore: 0}}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\n[door creaks] Hello\r\n")
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
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
	// Standard variant (default) — HI sub is the only option (fallback).
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("SearchTargets() returned %d paths, want 1", len(result.Paths))
	}
	// With StripHI enabled and standard variant, the .hi. should be stripped.
	if strings.Contains(result.Paths[0], ".hi.") {
		t.Errorf("SearchTargets(strip_hi, standard) path = %q, want no '.hi.' suffix", result.Paths[0])
	}
}

func TestSearchTargets_hash_match_skips_sync(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create a video file large enough for hashing.
	videoData := make([]byte, hashBlockSize*2)
	if err := os.WriteFile(videoPath, videoData, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ms := &mockStore{}
	mc := &mockConfig{
		searchCfg: api.SearchConfig{},
		minScore:  0,
	}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	p := &mockProvider{
		name: "test",
		results: []api.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "hash", Language: "fr"},
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
	if len(result.Paths) != 1 {
		t.Fatalf("SearchTargets(hash match) returned %d paths, want 1", len(result.Paths))
	}
	// Verify the subtitle was saved (hash match bypasses sync).
	if _, err := os.Stat(result.Paths[0]); err != nil {
		t.Errorf("subtitle file not found at %q: %v", result.Paths[0], err)
	}
}

// --- all providers backed off ---

func TestSearchTargets_all_providers_backed_off_returns_backed_off(t *testing.T) {
	t.Parallel()

	backedStore := &mockStoreWithBackoff{
		mockStore: mockStore{},
		backedOff: []api.ProviderID{"prov1", "prov2"},
	}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true, MaxAttempts: 5},
		searchCfg:   api.SearchConfig{},
	}
	metrics := &mockMetrics{}
	p1 := &mockProvider{name: "prov1", results: nil}
	p2 := &mockProvider{name: "prov2", results: nil}
	e := newEngine([]api.Provider{p1, p2}, backedStore, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType: "movie",
		ImdbID:    "tt123",
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	result, err := e.SearchTargets(context.Background(), req, "", targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths) != 0 {
		t.Errorf("SearchTargets() = %v, want empty", result.Paths)
	}
	// When all providers are backed off, the target counts in its OWN
	// category: not searched (no provider query ran, so it must not feed the
	// season tracker or the searched stats) and not skipped-as-covered.
	if result.Searched != 0 {
		t.Errorf("SearchTargets().Searched = %d, want 0", result.Searched)
	}
	if result.BackedOff != 1 {
		t.Errorf("SearchTargets().BackedOff = %d, want 1", result.BackedOff)
	}
	if len(result.SearchedLangs) != 0 {
		t.Errorf("SearchTargets().SearchedLangs = %v, want empty", result.SearchedLangs)
	}
	// Adaptive skip should be recorded for each backed-off provider.
	if metrics.adaptiveSkips.Load() != 2 {
		t.Errorf("adaptiveSkips = %d, want 2", metrics.adaptiveSkips.Load())
	}
}

// --- multi-variant same language ---

func TestSearchTargets_multi_variant_same_language(t *testing.T) {
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
			{
				Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb",
				Language: "fr", HearingImp: false, Forced: false,
			},
			{
				Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb",
				Language: "fr", HearingImp: false, Forced: true,
			},
		},
		data: subData,
	}
	metrics := &mockMetrics{}
	e := newEngine([]api.Provider{p}, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{
		MediaType:   "movie",
		ImdbID:      "tt123",
		ReleaseName: "Movie-GRP",
	}
	// Two targets for the same language: standard + forced.
	targets := []api.SubtitleTarget{
		{Code: "fr"},
		{Code: "fr", Variant: "forced"},
	}

	result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	// Both variants should find a subtitle.
	if len(result.Paths) != 2 {
		t.Errorf("SearchTargets(multi-variant) returned %d paths, want 2", len(result.Paths))
	}
	// Provider should only be queried once (grouped by language).
	if metrics.searches.Load() != 1 {
		t.Errorf("metrics.searches = %d, want 1 (single query for grouped language)",
			metrics.searches.Load())
	}
}
