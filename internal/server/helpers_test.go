package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/auth/v4"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/obs"
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

// --- Config fixture ---

// testConfigYAML is the minimal valid document every server test starts from:
// one arr, one language rule, one enabled provider. Tests append their own
// sections through testConfig's extra argument.
const testConfigYAML = `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
`

// testConfig builds a REAL *config.Config through the production loader, with
// media_roots pointed at a scratch directory so the containment accessors
// (ValidatePath, RemoveUnderRoot) answer against something the test owns.
// extra is appended YAML for whatever section the individual test needs.
//
// It replaces the 28-method hand-written fake this file used to carry. The
// server's live snapshot holds the concrete *config.Config, so a fake would
// have to BE one; and the loader is what makes a config's accessors agree with
// each other — targets derived from the rules, caches built, roots opened —
// which a fake with independent per-method returns could always contradict.
// Every knob the fake exposed as a struct field is a YAML key here.
func testConfig(t *testing.T, extra ...string) *config.Config {
	t.Helper()
	return testConfigInRoot(t, t.TempDir(), extra...)
}

// testConfigInRoot is testConfig with an explicit media root, for tests that
// must place a file under it and then drive a handler over that path.
func testConfigInRoot(t *testing.T, root string, extra ...string) *config.Config {
	t.Helper()
	doc := testConfigYAML + "media_roots:\n  - " + strconv.Quote(root) + "\n" +
		strings.Join(extra, "\n")
	cfg, err := config.LoadFromBytes(t.Context(), []byte(doc))
	if err != nil {
		t.Fatalf("config.LoadFromBytes() unexpected error: %v\n%s", err, doc)
	}
	t.Cleanup(func() { _ = cfg.Close() })
	return cfg
}

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
func newTestServer(t *testing.T, db *qhMockStore) *Server {
	t.Helper()
	cfg := testConfig(t)
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(db), search.WithConfig(cfg),
		search.WithMetrics(obs.New()), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}))
	s := &Server{
		db: db,
		stores: storeFacade{
			query: db,
			sync:  db,
		},
		metrics:  obs.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(0),
		// context.Background(): no *testing.T in scope, and this is the server's own long-lived context rather than a per-test one.
		lifetime: context.Background(),
		loadConfig: func([]byte) (*config.Config, error) {
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
