package search

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"subflux/internal/api"
	"subflux/internal/search/syncing"
	"subflux/internal/testsupport"
)

// Syncer is a test-only alias for syncing.Syncer after shim elimination.
type Syncer = syncing.Syncer

// --- Mock implementations ---

// noopDetector implements TrackDetector with no results.
type noopDetector struct{}

func (noopDetector) DetectTracks(_ context.Context, _ string) []api.EmbeddedTrack { return nil }

type mockStore struct {
	testsupport.NopStore

	manualLocked  bool
	successCalled bool
	failureCalled bool
}

func (m *mockStore) RecordNoResult(_ context.Context, _ api.MediaType, _, _ string, _ api.ProviderID, _ api.BackoffParams) error {
	m.failureCalled = true
	return nil
}
func (m *mockStore) SaveDownload(_ context.Context, _ *api.DownloadRecord) error {
	m.successCalled = true
	return nil
}
func (m *mockStore) IsManuallyLocked(_ context.Context, _ api.MediaType, _, _ string) (bool, error) {
	return m.manualLocked, nil
}

// noPriority is declared in release_test.go (same package).

type mockConfig struct {
	searchCfg   api.SearchConfig
	adaptiveCfg api.AdaptiveConfig
	minScore    int
}

func (m *mockConfig) Scores() api.Scores { return api.DefaultScores }
func (m *mockConfig) ProvidersForTarget(_ *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	return all
}
func (m *mockConfig) MinScoreForTarget(_ *api.SubtitleTarget, _ api.MediaType) int { return m.minScore }
func (m *mockConfig) Adaptive() api.AdaptiveConfig                                 { return m.adaptiveCfg }
func (m *mockConfig) Search() api.SearchConfig                                     { return m.searchCfg }
func (m *mockConfig) ProviderConfigs() map[api.ProviderID]api.ProviderCfg          { return nil }
func (m *mockConfig) ProviderPriority(_ api.ProviderID) int                        { return 99 }
func (m *mockConfig) PostProcessConfig() api.PostProcessConfig {
	return api.PostProcessConfig{
		NormalizeUTF8:    true,
		NormalizeEndings: true,
		CleanWhitespace:  true,
		RemoveEmpty:      true,
		StripTags:        true,
	}
}
func (m *mockConfig) SyncConfig() api.SyncConfig {
	return api.SyncConfig{SyncSubtitles: true}
}

type mockMetrics struct {
	searches      atomic.Int64
	downloads     atomic.Int64
	adaptiveSkips atomic.Int64
}

func (m *mockMetrics) RecordSearch(_ api.ProviderID, _ time.Duration, _ error) { m.searches.Add(1) }
func (m *mockMetrics) RecordDownload(_ api.ProviderID, _ error)                { m.downloads.Add(1) }
func (m *mockMetrics) AdaptiveSkip()                                           { m.adaptiveSkips.Add(1) }
func (m *mockMetrics) RecordScan(_, _ int, _ time.Duration)                    {}
func (m *mockMetrics) RecordImport(_ api.PollKey)                              {}
func (m *mockMetrics) TotalSearches() int64                                    { return m.searches.Load() }
func (m *mockMetrics) Handler() http.HandlerFunc                               { return nil }

type mockProvider struct {
	name        string
	results     []api.Subtitle
	searchErr   error
	downloadErr error
	data        []byte
}

func (m *mockProvider) Name() api.ProviderID { return api.ProviderID(m.name) }
func (m *mockProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return m.results, m.searchErr
}
func (m *mockProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return m.data, m.downloadErr
}

// mockStoreWithBackoff extends mockStore with BackedOffProviders support.
type mockStoreWithBackoff struct {
	backedOff []api.ProviderID
	mockStore
}

func (m *mockStoreWithBackoff) BackedOffProviders(_ context.Context, _ api.MediaType, _, _ string, _ int) ([]api.ProviderID, error) {
	return m.backedOff, nil
}

// mockStoreWithScore extends mockStore with CurrentScore support for upgrade tests.
type mockStoreWithScore struct {
	mediaImported time.Time
	score         int
	mockStore

	found bool
}

func (m *mockStoreWithScore) CurrentScore(_ context.Context, _ api.MediaType, _, _ string) (int, time.Time, bool, error) {
	return m.score, m.mediaImported, m.found, nil
}

// mockFilterConfig returns only the target's providers (not all).
type mockFilterConfig struct {
	mockConfig
}

func (m *mockFilterConfig) ProvidersForTarget(target *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	if len(target.Providers) > 0 {
		return target.Providers
	}
	return all
}

// newEngine is a test helper that mirrors the old 7-parameter New signature.
func newEngine(providers []api.Provider, db SearchStore, cfg SearchCfg,
	m SearchMetrics, sc api.Scorer, syncer SubtitleSyncer, tracks TrackDetector) *Engine {
	return New(providers, WithStore(db), WithConfig(cfg),
		WithMetrics(m), WithScorer(sc), WithSyncer(syncer), WithTracks(tracks))
}
