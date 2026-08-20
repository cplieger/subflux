package search

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// Syncer is a test-only alias for syncing.Syncer after shim elimination.
type Syncer = syncing.Syncer

// --- Mock implementations ---

// noopDetector implements TrackDetector with no results.
type noopDetector = NoopDetector

// errDetector implements TrackDetector with a fixed probe failure, for the
// detector-error fail-open + coverage-retention fixtures.
type errDetector struct{ err error }

func (d errDetector) DetectTracks(_ context.Context, _ string) ([]subflux.EmbeddedTrack, error) {
	return nil, d.err
}

type mockStore struct {
	testsupport.NopStore

	manualLocked  bool
	successCalled bool
	failureCalled bool
}

func (m *mockStore) RecordNoResult(_ context.Context, _ subflux.MediaType, _, _ string, _ subflux.ProviderID, _ subflux.BackoffParams) error {
	m.failureCalled = true
	return nil
}

func (m *mockStore) SaveDownload(_ context.Context, _ *subflux.DownloadRecord) error {
	m.successCalled = true
	return nil
}

func (m *mockStore) IsManuallyLocked(_ context.Context, _ subflux.ManualLockKey) (bool, error) {
	return m.manualLocked, nil
}

// mockStoreLockErr fails every lock check, for the fail-closed test.
type mockStoreLockErr struct {
	testsupport.NopStore
}

func (m *mockStoreLockErr) IsManuallyLocked(_ context.Context, _ subflux.ManualLockKey) (bool, error) {
	return false, errors.New("lock check failed")
}

// noPriority is declared in release_test.go (same package).

type mockConfig struct {
	searchCfg   subflux.SearchConfig
	adaptiveCfg subflux.AdaptiveConfig
	embedded    subflux.EmbeddedPolicy
	minScore    int
}

func (m *mockConfig) Scores() subflux.Scores { return subflux.DefaultScores }
func (m *mockConfig) ProvidersForTarget(_ *subflux.SubtitleTarget, all []subflux.ProviderID) []subflux.ProviderID {
	return all
}

func (m *mockConfig) MinScoreForTarget(_ *subflux.SubtitleTarget, _ subflux.MediaType) int {
	return m.minScore
}
func (m *mockConfig) Adaptive() subflux.AdaptiveConfig          { return m.adaptiveCfg }
func (m *mockConfig) Search() subflux.SearchConfig              { return m.searchCfg }
func (m *mockConfig) EmbeddedPolicy() subflux.EmbeddedPolicy    { return m.embedded }
func (m *mockConfig) ProviderPriority(_ subflux.ProviderID) int { return 99 }
func (m *mockConfig) PostProcess() subflux.PostProcessConfig {
	return subflux.PostProcessConfig{
		NormalizeUTF8:    true,
		NormalizeEndings: true,
		CleanWhitespace:  true,
		RemoveEmpty:      true,
		StripTags:        true,
	}
}

func (m *mockConfig) Sync() subflux.SyncConfig {
	return subflux.SyncConfig{SyncSubtitles: true}
}

type mockMetrics struct {
	searches      atomic.Int64
	downloads     atomic.Int64
	adaptiveSkips atomic.Int64
	detectorErrs  atomic.Int64
}

func (m *mockMetrics) RecordSearch(_ subflux.ProviderID, _ time.Duration, _ error) { m.searches.Add(1) }

func (m *mockMetrics) RecordDownload(_ subflux.ProviderID, _ error) { m.downloads.Add(1) }

func (m *mockMetrics) AdaptiveSkip() { m.adaptiveSkips.Add(1) }

func (m *mockMetrics) RecordEmbeddedDetectorError()         { m.detectorErrs.Add(1) }
func (m *mockMetrics) RecordScan(_, _ int, _ time.Duration) {}
func (m *mockMetrics) RecordImport(_ subflux.PollKey)       {}
func (m *mockMetrics) TotalSearches() int64                 { return m.searches.Load() }
func (m *mockMetrics) Handler() http.HandlerFunc            { return nil }

type mockProvider struct {
	name        string
	results     []subflux.Subtitle
	searchErr   error
	downloadErr error
	data        []byte
}

func (m *mockProvider) Name() subflux.ProviderID { return subflux.ProviderID(m.name) }
func (m *mockProvider) Search(_ context.Context, _ *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return m.results, m.searchErr
}

func (m *mockProvider) Download(_ context.Context, _ *subflux.Subtitle) ([]byte, error) {
	return m.data, m.downloadErr
}

// mockStoreWithBackoff extends mockStore with BackedOffProviders support.
type mockStoreWithBackoff struct {
	backedOff []subflux.ProviderID
	mockStore
}

func (m *mockStoreWithBackoff) BackedOffProviders(_ context.Context, _ subflux.MediaType, _, _ string, _ int) ([]subflux.ProviderID, error) {
	return m.backedOff, nil
}

// mockStoreWithScore extends mockStore with CurrentScore support for upgrade tests.
type mockStoreWithScore struct {
	mediaImported time.Time
	score         int
	mockStore

	found bool
}

func (m *mockStoreWithScore) CurrentScore(_ context.Context, _ subflux.MediaType, _, _ string, _ subflux.Variant) (int, time.Time, bool, error) {
	return m.score, m.mediaImported, m.found, nil
}

// mockFilterConfig returns only the target's providers (not all).
type mockFilterConfig struct {
	mockConfig
}

func (m *mockFilterConfig) ProvidersForTarget(target *subflux.SubtitleTarget, all []subflux.ProviderID) []subflux.ProviderID {
	if len(target.Providers) > 0 {
		return target.Providers
	}
	return all
}

// newEngine is a test helper that mirrors the old 7-parameter New signature.
func newEngine(providers []provider.Provider, db Store, cfg Cfg,
	m Metrics, sc Scorer, syncer SubtitleSyncer, tracks TrackDetector,
) *Engine {
	return New(providers, WithStore(db), WithConfig(cfg),
		WithMetrics(m), WithScorer(sc), WithSyncer(syncer), WithTracks(tracks))
}
