package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/provider/embedded"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/testsupport"
)

// This file holds the package-wide shared test fakes and the test-server
// builder used across the server suite. Behavior-specific fakes live next to
// the tests that use them; only the cross-file fakes belong here.

// --- Shared store / config / provider fakes ---

type qhMockStore struct {
	testsupport.NopStore

	stateErr   error
	backoffErr error
	locksErr   error
	state      []api.StateEntry
	backoff    []api.BackoffEntry
	locks      []api.ManualLockEntry
	stateLimit int
	downloads  int
	attempts   int
}

func (m *qhMockStore) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	m.stateLimit = q.Limit
	return m.state, m.stateErr
}

func (m *qhMockStore) GetBackoffItems(_ context.Context) ([]api.BackoffEntry, error) {
	return m.backoff, m.backoffErr
}

func (m *qhMockStore) GetManualLocks(_ context.Context) ([]api.ManualLockEntry, error) {
	return m.locks, m.locksErr
}

func (m *qhMockStore) Stats(_ context.Context) (int, int, error) {
	return m.downloads, m.attempts, nil
}

type qhMockConfig struct {
	providers   map[api.ProviderID]api.ProviderCfg
	sonarrCfg   api.ArrConfig
	radarrCfg   api.ArrConfig
	languages   []string
	targets     []api.SubtitleTarget
	searchCfg   api.SearchConfig
	adaptiveCfg api.AdaptiveConfig
}

func (m *qhMockConfig) Scores() api.Scores { return api.DefaultScores }

func (m *qhMockConfig) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return m.targets
}

func (m *qhMockConfig) LanguageCodes() []string { return m.languages }

func (m *qhMockConfig) ProvidersForTarget(_ *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	return all
}

func (m *qhMockConfig) MinScoreForTarget(_ *api.SubtitleTarget, _ api.MediaType) int { return 0 }
func (m *qhMockConfig) Adaptive() api.AdaptiveConfig                                 { return m.adaptiveCfg }
func (m *qhMockConfig) Search() api.SearchConfig                                     { return m.searchCfg }
func (m *qhMockConfig) SonarrConfig() api.ArrConfig                                  { return m.sonarrCfg }
func (m *qhMockConfig) RadarrConfig() api.ArrConfig                                  { return m.radarrCfg }
func (m *qhMockConfig) ProviderConfigs() map[api.ProviderID]api.ProviderCfg          { return m.providers }
func (m *qhMockConfig) ProviderPriority(_ api.ProviderID) int                        { return 99 }
func (m *qhMockConfig) ServerPort() int                                              { return 8374 }
func (m *qhMockConfig) PollInterval() time.Duration                                  { return 30 * time.Second }
func (m *qhMockConfig) LoggingLevel() api.LogLevel                                   { return "info" }
func (m *qhMockConfig) LoggingFormat() api.LogFormat                                 { return "json" }
func (m *qhMockConfig) MediaRoots() []string                                         { return nil }
func (m *qhMockConfig) ValidatePath(_ context.Context, _ string) error               { return nil }
func (m *qhMockConfig) RemoveUnderRoot(_ context.Context, path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
		return err
	}
	return nil
}
func (m *qhMockConfig) PostProcessConfig() api.PostProcessConfig  { return api.PostProcessConfig{} }
func (m *qhMockConfig) SyncConfig() api.SyncConfig                { return api.SyncConfig{SyncSubtitles: true} }
func (m *qhMockConfig) LanguageRulesForUI() api.LanguageRulesJSON { return api.LanguageRulesJSON{} }
func (m *qhMockConfig) AuthEnabled() bool                         { return false }
func (m *qhMockConfig) BasicAuthEnabled() bool                    { return true }
func (m *qhMockConfig) OIDCEnabled() bool                         { return false }
func (m *qhMockConfig) OIDCConfig() api.OIDCConfig                { return api.OIDCConfig{} }
func (m *qhMockConfig) SessionIdleTimeout() time.Duration         { return 24 * time.Hour }
func (m *qhMockConfig) SessionAbsoluteTimeout() time.Duration     { return 7 * 24 * time.Hour }
func (m *qhMockConfig) CheckBreachedPasswords() bool              { return false }
func (m *qhMockConfig) WebAuthnRPID() string                      { return "" }

// stubProvider implements api.Provider for test setup.
type stubProvider struct {
	name string
}

func (p *stubProvider) Name() api.ProviderID { return api.ProviderID(p.name) }

func (p *stubProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (p *stubProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, nil
}

// dummyArrClient is a non-nil api.ArrClient for tests that need sonarr/radarr != nil
// to reach deeper handler branches. All methods return errors or empty results.
type dummyArrClient struct{}

func (dummyArrClient) Ping(context.Context) error                      { return nil }
func (dummyArrClient) GetSeries(context.Context) ([]api.Series, error) { return nil, nil }
func (dummyArrClient) GetEpisodes(context.Context, int) ([]api.Episode, error) {
	return nil, nil
}
func (dummyArrClient) GetMovies(context.Context) ([]api.Movie, error) { return nil, nil }
func (dummyArrClient) GetHistorySince(context.Context, time.Time, api.HistoryEventType) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (dummyArrClient) GetWantedEpisodes(context.Context, map[int]struct{}, func(api.Series, api.Episode) error) error {
	return nil
}

func (dummyArrClient) GetWantedMovies(context.Context, map[int]struct{}, func(api.Movie) error) error {
	return nil
}

func (dummyArrClient) ResolveExcludeTagIDs(context.Context, []string, bool) map[int]struct{} {
	return nil
}
func (dummyArrClient) RefreshSeries(context.Context, int) error { return nil }
func (dummyArrClient) RefreshMovie(context.Context, int) error  { return nil }
func (dummyArrClient) GetSeriesByID(context.Context, int) (*api.Series, error) {
	return nil, nil
}

func (dummyArrClient) GetEpisodeByID(context.Context, int) (*api.Episode, error) {
	return nil, nil
}

func (dummyArrClient) GetMovieByID(context.Context, int) (*api.Movie, error) {
	return nil, nil
}

// newTestServer creates a minimal Server for handler testing.
// Uses a real search.Engine for accurate score simulation.
func newTestServer(db *qhMockStore, cfg *qhMockConfig) *Server {
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(db), search.WithConfig(cfg),
		search.WithMetrics(metrics.New()), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.ProviderDirect{}))
	s := &Server{
		db: db,
		stores: storeFacade{
			file:  db,
			query: db,
			cov:   db,
			sync:  db,
			dl:    db,
		},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
		ctx:      context.Background(),
		loadConfig: func(data []byte) (api.ConfigProvider, error) {
			return nil, fmt.Errorf("not implemented in test")
		},
		schemaFunc: func(_ []api.ProviderSchema) []api.SchemaSection {
			return nil
		},
		// Tests exercise handlers directly (injecting users via context) or
		// via handleUI (no auth needed for static-asset serving). A bypass
		// authenticator keeps the Server invariant (auth is always wired)
		// without requiring each test to stand up real auth state.
		authDeps: authDeps{
			authenticator: &authhandlers.Authenticator{Bypass: func() bool { return true }},
		},
	}
	s.configured.Store(true)
	s.live.Store(&liveState{
		cfg:    cfg,
		engine: engine,
		scorer: sc,
	})
	s.scanH = s.initScanHandler()
	s.manualH = s.initManualHandler()
	s.configH = confighandlers.New(&confighandlers.Deps{
		LoadConfig:    s.loadConfig,
		SchemaFunc:    s.schemaFunc,
		DefaultConfig: s.defaultConfig,
		Configured:    func() bool { return s.configured.Load() },
		ConfigPath:    func() string { return cfgFilePath },
	})
	// coverageH, fileH, mediaH are lazily initialized by their delegate methods
	// when first called, reading from s.stores.cov/file at call time. This allows
	// tests to override s.stores.cov/file after construction.
	return s
}
