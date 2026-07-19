package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/testsupport"
)

// This file holds the package-wide shared test fakes and the test-server
// builder used across the server suite. Behavior-specific fakes live next to
// the tests that use them; only the cross-file fakes belong here.

// --- Authenticator double ---

// testAdminUser is the principal bypassAuthenticator resolves every request
// to (mirrors the library's synthetic bypass admin).
var testAdminUser = &auth.User{Username: "admin", Role: auth.RoleAdmin, Enabled: true}

// bypassAuthenticator is a sessionAuthenticator double that authenticates
// every request as testAdminUser, for tests that exercise handlers without
// standing up real auth state (the double is what the sessionAuthenticator
// interface exists for).
type bypassAuthenticator struct{}

func (bypassAuthenticator) Authenticate(*http.Request) (*auth.User, string, error) {
	return testAdminUser, "", nil
}

func (bypassAuthenticator) RequireAuth(http.ResponseWriter, *http.Request) (*auth.User, string, bool) {
	return testAdminUser, "", true
}

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
	embedded    api.EmbeddedPolicy
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
func (m *qhMockConfig) EmbeddedPolicy() api.EmbeddedPolicy                           { return m.embedded }
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
func (m *qhMockConfig) OIDCConfig() auth.OIDCConfig               { return auth.OIDCConfig{} }
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

// dummyArrClient is a non-nil fake satisfying BOTH api.SonarrClient and
// api.RadarrClient, for tests that need sonarr/radarr != nil to reach deeper
// handler branches. All methods return empty results; role-specific fakes
// embed it and override the methods they exercise.
type dummyArrClient struct{}

var (
	_ api.SonarrClient = dummyArrClient{}
	_ api.RadarrClient = dummyArrClient{}
)

func (dummyArrClient) Ping(context.Context) error                         { return nil }
func (dummyArrClient) GetSeries(context.Context) ([]arrapi.Series, error) { return nil, nil }
func (dummyArrClient) GetEpisodes(context.Context, int) ([]arrapi.Episode, error) {
	return nil, nil
}
func (dummyArrClient) GetMovies(context.Context) ([]arrapi.Movie, error) { return nil, nil }
func (dummyArrClient) GetHistorySince(context.Context, time.Time, ...arrapi.EventType) ([]arrapi.HistoryRecord, error) {
	return nil, nil
}

func (dummyArrClient) GetWantedEpisodes(context.Context, map[int]struct{}, func(arrapi.Series, arrapi.Episode) error) error {
	return nil
}

func (dummyArrClient) GetWantedMovies(context.Context, map[int]struct{}, func(arrapi.Movie) error) error {
	return nil
}

func (dummyArrClient) ResolveExcludeTagIDs(context.Context, []string, bool) map[int]struct{} {
	return nil
}
func (dummyArrClient) RescanSeries(context.Context, int) error { return nil }
func (dummyArrClient) RescanMovie(context.Context, int) error  { return nil }
func (dummyArrClient) GetSeriesByID(context.Context, int) (arrapi.Series, error) {
	return arrapi.Series{}, nil
}

func (dummyArrClient) GetEpisodeByID(context.Context, int) (arrapi.Episode, error) {
	return arrapi.Episode{}, nil
}

func (dummyArrClient) GetMovieByID(context.Context, int) (arrapi.Movie, error) {
	return arrapi.Movie{}, nil
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
		search.WithTracks(embedded.Detector{}))
	s := &Server{
		db: db,
		stores: storeFacade{
			query: db,
			sync:  db,
		},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(0),
		ctx:      context.Background(),
		loadConfig: func(data []byte) (api.ConfigProvider, error) {
			return nil, fmt.Errorf("not implemented in test")
		},
		schemaFunc: func(_ []api.ProviderSchema) []api.SchemaSection {
			return nil
		},
		// Tests exercise handlers directly (injecting users via context) or
		// via handleUI (no auth needed for static-asset serving). A bypass
		// authenticator double keeps the Server invariant (auth is always
		// wired) without requiring each test to stand up real auth state.
		authDeps: authDeps{
			authenticator: bypassAuthenticator{},
		},
	}
	s.configured.Store(true)
	s.live.Store(&liveState{
		cfg:    cfg,
		engine: engine,
		scorer: sc,
	})
	s.scanH = s.initScanHandler()
	s.manualH = s.initManualHandler(s.newResolver())
	s.configH = confighandlers.New(&confighandlers.Deps{
		LoadConfig:    s.loadConfig,
		SchemaFunc:    s.schemaFunc,
		DefaultConfig: s.defaultConfig,
		Configured:    func() bool { return s.configured.Load() },
		ConfigPath:    func() string { return cfgFilePath },
	})
	return s
}
