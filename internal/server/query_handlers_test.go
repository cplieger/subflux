package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/auth"
	"subflux/internal/metrics"
	"subflux/internal/provider/embedded"
	"subflux/internal/scorer"
	"subflux/internal/search"
	"subflux/internal/search/syncing"
	"subflux/internal/server/activity"
	"subflux/internal/server/confighandlers"
	"subflux/internal/server/events"
	"subflux/internal/testsupport"
)

// --- Mock implementations for query handler tests ---

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
func (m *qhMockConfig) TOTPEncryptionKey() ([]byte, error)        { return nil, nil }
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
			authenticator: &auth.Authenticator{BypassAuth: true},
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

// --- Handler tests ---

func TestHandleState_returns_entries(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{
		state: []api.StateEntry{
			{ID: 1, MediaType: "movie", MediaID: "tt123",
				Language: "fr", Provider: "os", Score: 200},
		},
	}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleState() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var entries []api.StateEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("handleState() returned %d entries, want 1", len(entries))
	}
}

func TestHandleState_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleState(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleState_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{stateErr: errMock}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleState() status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleStateStats_returns_counts(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{downloads: 42, attempts: 100}
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{
			ScanInterval: 30 * time.Minute,
		},
	}
	s := newTestServer(db, cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/state/stats", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleStateStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleStateStats() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify all 8 expected response fields are present and correct.
	if int(result["downloads"].(float64)) != 42 {
		t.Errorf("downloads = %v, want 42", result["downloads"])
	}
	if int(result["attempts"].(float64)) != 100 {
		t.Errorf("attempts = %v, want 100 (DB fallback when metrics zero)", result["attempts"])
	}
	if result["last_scan"] != "" {
		t.Errorf("last_scan = %v, want empty string", result["last_scan"])
	}
	if int(result["scan_interval_seconds"].(float64)) != 1800 {
		t.Errorf("scan_interval_seconds = %v, want 1800", result["scan_interval_seconds"])
	}
	if int(result["total_subs"].(float64)) != 0 {
		t.Errorf("total_subs = %v, want 0", result["total_subs"])
	}
	if int(result["total_series"].(float64)) != 0 {
		t.Errorf("total_series = %v, want 0 (no sonarr configured)", result["total_series"])
	}
	if int(result["total_movies"].(float64)) != 0 {
		t.Errorf("total_movies = %v, want 0 (no radarr configured)", result["total_movies"])
	}
	if int(result["missing_subs"].(float64)) != 0 {
		t.Errorf("missing_subs = %v, want 0", result["missing_subs"])
	}
}

func TestHandleStateStats_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/state/stats", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleStateStats(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleStateStats(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBackoff_returns_entries(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{
		backoff: []api.BackoffEntry{
			{MediaType: "movie", MediaID: "tt123", Language: "fr", Failures: 3},
		},
	}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/backoff", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleBackoff() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []api.BackoffEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("handleBackoff() returned %d entries, want 1", len(entries))
	}
}

func TestHandleBackoff_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{backoffErr: errMock}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/backoff", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoff(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleBackoff() status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleLocks_returns_entries(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{
		locks: []api.ManualLockEntry{
			{MediaType: "episode", MediaID: "tt456-s01e01", Language: "fr", Count: 2},
		},
	}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/locks", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleLocks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleLocks() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []api.ManualLockEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("handleLocks() returned %d entries, want 1", len(entries))
	}
}

func TestHandleLocks_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{locksErr: errMock}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/locks", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleLocks(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleLocks() status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleProviders_returns_provider_info(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		providers: map[api.ProviderID]api.ProviderCfg{
			"opensubtitles": {Enabled: true},
			"yify":          {Enabled: false},
		},
	}
	s := newTestServer(&qhMockStore{}, cfg)
	// Store providers in the live state for the handleProviders test.
	ls := s.state()
	s.live.Store(&liveState{
		cfg:       ls.cfg,
		engine:    ls.engine,
		scorer:    ls.scorer,
		providers: []api.Provider{&stubProvider{name: "opensubtitles"}},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/providers", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleProviders() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Loaded  bool   `json:"loaded"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("handleProviders() returned %d providers, want 2", len(result))
	}

	for _, p := range result {
		switch p.Name {
		case "opensubtitles":
			if !p.Enabled {
				t.Error("opensubtitles.Enabled = false, want true")
			}
			if !p.Loaded {
				t.Error("opensubtitles.Loaded = false, want true")
			}
		case "yify":
			if p.Enabled {
				t.Error("yify.Enabled = true, want false")
			}
			if p.Loaded {
				t.Error("yify.Loaded = true, want false")
			}
		default:
			t.Errorf("unexpected provider %q in response", p.Name)
		}
	}
}

func TestHandleConfigParsed_returns_structured_config(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		languages: []string{"fr", "en"},
		providers: map[api.ProviderID]api.ProviderCfg{
			"os": {Enabled: true},
		},
		searchCfg: api.SearchConfig{
			UpgradeEnabled: true, UpgradeWindowDays: 7,
		},
		adaptiveCfg: api.AdaptiveConfig{Enabled: true},
		sonarrCfg:   api.ArrConfig{URL: "http://sonarr:8989"},
		radarrCfg:   api.ArrConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/config/parsed", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigParsed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleConfigParsed() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	langs, ok := result["languages"].([]any)
	if !ok || len(langs) != 2 {
		t.Errorf("languages = %v, want [fr, en]", result["languages"])
	}
	if result["sonarr_configured"] != true {
		t.Errorf("sonarr_configured = %v, want true", result["sonarr_configured"])
	}
	if result["radarr_configured"] != false {
		t.Errorf("radarr_configured = %v, want false", result["radarr_configured"])
	}
	// Verify all expected top-level keys are present.
	for _, key := range []string{"search", "adaptive", "providers", "scores"} {
		if _, exists := result[key]; !exists {
			t.Errorf("response missing %q field", key)
		}
	}
}

func TestHandleScore_returns_score_result(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	body := `{"media_type":"movie","release_name":"Movie.2024.1080p.WEB-DL-GRP","sub_release":"Movie.2024.1080p.WEB-DL-GRP","matched_by":"imdb"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/score", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleScore() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if _, ok := result["score"]; !ok {
		t.Error("response missing 'score' field")
	}
	if _, ok := result["score_no_hash"]; !ok {
		t.Error("response missing 'score_no_hash' field")
	}
	if _, ok := result["tier"]; !ok {
		t.Error("response missing 'tier' field")
	}
}

func TestHandleScore_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/score", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScore(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScore_invalid_json_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/score", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScore(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSearchTargets_returns_targets(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{
			{Code: "fr"},
			{Code: "en", Variant: "hi"},
		},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/search/targets?orig_lang=en&audio_langs=en,fr", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	targets, ok := result["targets"].([]any)
	if !ok {
		t.Fatal("targets not an array")
	}
	if len(targets) != 2 {
		t.Errorf("targets count = %d, want 2", len(targets))
	}
	if result["orig_lang"] != "en" {
		t.Errorf("orig_lang = %v, want %q", result["orig_lang"], "en")
	}
}

func TestHandleSearchTargets_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/search/targets", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleSearchTargets(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleSearchTargets(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleSearchTargets_filters_empty_audio_langs(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{
			{Code: "fr"},
		},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	// Send audio_langs with empty segments.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/targets?orig_lang=en&audio_langs=en,,fr,", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Empty segments should be filtered out, leaving only "en" and "fr".
	audioLangs, ok := result["audio_langs"].([]any)
	if !ok {
		t.Fatal("audio_langs not an array")
	}
	if len(audioLangs) != 2 {
		t.Errorf("audio_langs count = %d, want 2 (empty segments filtered)", len(audioLangs))
	}
}

func TestHandleSearchTargets_empty_audio_langs_param(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{
			{Code: "fr"},
		},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	// Send empty audio_langs.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/targets?orig_lang=en&audio_langs=", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Empty string param should result in nil audio_langs (no valid entries).
	if result["audio_langs"] != nil {
		t.Errorf("audio_langs = %v, want nil for empty param", result["audio_langs"])
	}
}

// --- activity.Log.start maxItems boundary ---

func TestActivityLog_start_exact_maxItems(t *testing.T) {
	t.Parallel()
	al := activity.New(2)

	al.Start("A", "first", "scheduled")
	al.Start("B", "second", "scheduled")

	al.RLock()
	count := len(al.EntriesUnsafe())
	al.RUnlock()

	// At exactly maxItems, should NOT trim.
	if count != 2 {
		t.Errorf("entries count = %d after 2 inserts with maxItems=2, want 2", count)
	}

	// One more should trigger trim.
	al.Start("C", "third", "scheduled")

	al.RLock()
	count = len(al.EntriesUnsafe())
	first := al.EntriesUnsafe()[0].Action
	al.RUnlock()

	if count != 2 {
		t.Errorf("entries count = %d after 3 inserts with maxItems=2, want 2", count)
	}
	if first != "B" {
		t.Errorf("entries[0].Action = %q after trim, want %q", first, "B")
	}
}

// --- activity.AlertLog.record max boundary ---

func TestAlertLog_record_exact_max(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(2)

	al.Record("a", "first")
	al.Record("b", "second")

	al.RLock()
	count := len(al.AlertsUnsafe())
	al.RUnlock()

	// At exactly max, should NOT trim.
	if count != 2 {
		t.Errorf("alerts count = %d after 2 inserts with max=2, want 2", count)
	}

	// One more should trigger trim.
	al.Record("c", "third")

	al.RLock()
	count = len(al.AlertsUnsafe())
	first := al.AlertsUnsafe()[0].Source
	al.RUnlock()

	if count != 2 {
		t.Errorf("alerts count = %d after 3 inserts with max=2, want 2", count)
	}
	if first != "b" {
		t.Errorf("alerts[0].Source = %q after trim, want %q", first, "b")
	}
}

// --- handleState limit parsing ---

func TestHandleState_limit_boundary_values(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     string
		wantLimit int
	}{
		{"default", "", 50},
		{"zero", "?limit=0", 50},      // n=0, n > 0 is false, keeps default 50
		{"negative", "?limit=-1", 50}, // n=-1, n > 0 is false, keeps default 50
		{"one", "?limit=1", 1},        // n=1, n > 0 is true
		{"ten_thousand", "?limit=10000", 10000},
		{"non_numeric", "?limit=abc", 50},   // strconv.Atoi fails, keeps default 50
		{"over_max", "?limit=20000", 10000}, // clamped to 10000
		{"one_over_max", "?limit=10001", 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := &qhMockStore{state: []api.StateEntry{}}
			s := newTestServer(db, &qhMockConfig{})

			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodGet, "/api/state"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			s.handleState(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if db.stateLimit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", db.stateLimit, tt.wantLimit)
			}
		})
	}
}

// --- handleScore media_type default ---

func TestHandleScore_media_type_variations(t *testing.T) {
	t.Parallel()
	// When media_type is empty, handleScore defaults it to "episode".
	// We verify this by checking the score output differs between episode and movie.

	t.Run("empty defaults to episode", func(t *testing.T) {
		t.Parallel()
		cfg := &qhMockConfig{searchCfg: api.SearchConfig{}}
		s := newTestServer(&qhMockStore{}, cfg)

		body := `{"release_name":"Test","sub_release":"Test","matched_by":"title"}`
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleScore(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("handleScore() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var result map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// Empty media_type defaults to "episode"; score and tier should be present.
		if _, ok := result["score"]; !ok {
			t.Error("response missing 'score' field")
		}
		if _, ok := result["tier"]; !ok {
			t.Error("response missing 'tier' field")
		}
	})

	t.Run("explicit movie vs episode same release", func(t *testing.T) {
		t.Parallel()
		cfg := &qhMockConfig{searchCfg: api.SearchConfig{}}
		s := newTestServer(&qhMockStore{}, cfg)

		// Same release for both; identity fields no longer scored,
		// so release attribute scores should be equal.
		release := "Movie.2024.BluRay.1080p.x264-GRP"
		movieBody := `{"media_type":"movie","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`
		epBody := `{"media_type":"episode","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`

		reqM := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(movieBody))
		recM := httptest.NewRecorder()
		s.handleScore(recM, reqM)

		if recM.Code != http.StatusOK {
			t.Fatalf("handleScore(movie) status = %d, want %d", recM.Code, http.StatusOK)
		}

		reqE := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(epBody))
		recE := httptest.NewRecorder()
		s.handleScore(recE, reqE)

		if recE.Code != http.StatusOK {
			t.Fatalf("handleScore(episode) status = %d, want %d", recE.Code, http.StatusOK)
		}

		var movieResult, epResult map[string]any
		if err := json.NewDecoder(recM.Body).Decode(&movieResult); err != nil {
			t.Fatalf("decode movie: %v", err)
		}
		if err := json.NewDecoder(recE.Body).Decode(&epResult); err != nil {
			t.Fatalf("decode episode: %v", err)
		}

		// Identity fields no longer scored; same release attributes = same score.
		movieScore := int(movieResult["score"].(float64))
		epScore := int(epResult["score"].(float64))
		if movieScore != epScore {
			t.Errorf("movie score (%d) should equal episode score (%d) for same release",
				movieScore, epScore)
		}
	})
}

// --- handleScore release attribute scoring ---

func TestHandleScore_release_attributes_scored(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	release := "Movie.2024.BluRay.1080p.x264-GRP"
	body := `{"media_type":"movie","release_name":"` + release + `","sub_release":"` + release + `","matched_by":"imdb"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/score", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScore(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleScore() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	score := int(result["score"].(float64))
	if score <= 0 {
		t.Errorf("score = %d, want > 0 for matching release attributes", score)
	}
}

// --- handleBackoff/handleLocks method checks ---

func TestHandleBackoff_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/backoff", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoff(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleBackoff(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleLocks_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/locks", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleLocks(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleLocks(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProviders_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/providers", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviders(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleProviders(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

// --- Mutant-killing tests ---

// Kills CONDITIONALS_NEGATION at query_handlers.go:188-190 (MatchedBy == "hash"/"imdb" → !=).
// Verifies that the score endpoint produces different scores for different matched_by values.
// If the negation mutant flips == to !=, hash-matched would get a lower score than title-matched.
func TestHandleScore_matched_by_affects_score(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	release := "Movie.2024.BluRay.1080p.x264-GRP"

	scoreFor := func(matchedBy string) float64 {
		body := fmt.Sprintf(
			`{"media_type":"movie","release_name":%q,"sub_release":%q,"matched_by":%q}`,
			release, release, matchedBy)
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/score", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleScore(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("handleScore(%s) status = %d", matchedBy, rec.Code)
		}
		var result map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return result["score"].(float64)
	}

	hashScore := scoreFor("hash")
	imdbScore := scoreFor("imdb")
	titleScore := scoreFor("title")

	// Hash match = 100, always highest.
	if hashScore <= imdbScore {
		t.Errorf("hash score (%.0f) must be > imdb score (%.0f)", hashScore, imdbScore)
	}
	// Identity fields no longer scored; imdb and title produce same release score.
	if imdbScore != titleScore {
		t.Errorf("imdb score (%.0f) should equal title score (%.0f); "+
			"identity fields no longer scored", imdbScore, titleScore)
	}
}

// --- handleProviderTimeout ---

func TestHandleProviderTimeout_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/providers/timeout", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviderTimeout(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleProviderTimeout(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProviderTimeout_nil_engine_returns_disabled(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/providers/timeout", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviderTimeout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleProviderTimeout() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["enabled"] != false {
		t.Errorf("enabled = %v, want false when engine is nil", result["enabled"])
	}
}

// --- handleProviderTimeoutReset ---

func TestHandleProviderTimeoutReset_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/providers/timeout/reset", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviderTimeoutReset(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleProviderTimeoutReset(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProviderTimeoutReset_nil_engine_returns_disabled(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/providers/timeout/reset", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviderTimeoutReset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleProviderTimeoutReset() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["enabled"] != false {
		t.Errorf("enabled = %v, want false when engine is nil", result["enabled"])
	}
}

// --- handleProviderTimeout enabled path ---

func TestHandleProviderTimeout_enabled_returns_providers(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{
			ProviderTimeout: 2 * time.Hour,
		},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/providers/timeout", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviderTimeout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleProviderTimeout() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["enabled"] != true {
		t.Errorf("enabled = %v, want true when timeout configured", result["enabled"])
	}
	if _, ok := result["providers"]; !ok {
		t.Error("response missing 'providers' field when enabled")
	}
}

func TestHandleProviderTimeoutReset_enabled_resets(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{
			ProviderTimeout: 2 * time.Hour,
		},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/providers/timeout/reset", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviderTimeoutReset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleProviderTimeoutReset() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("ok = %v, want true after reset", result["ok"])
	}
}

// --- handleProviders empty config ---

func TestHandleProviders_empty_config_returns_empty_array(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		providers: map[api.ProviderID]api.ProviderCfg{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/providers", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleProviders() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Empty config produces empty slice, which JSON-encodes as "[]".
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleProviders(empty config) body = %q, want %q", body, "[]")
	}
}

// --- handleSearchTargets no targets ---

func TestHandleSearchTargets_no_targets_returns_empty(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		targets: nil, // No targets configured.
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/targets?orig_lang=en", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// targets should be null when no targets configured.
	if result["targets"] != nil {
		t.Errorf("targets = %v, want nil when no targets configured", result["targets"])
	}
}

// --- isValidMediaPrefix ---

func TestIsValidMediaPrefix_valid_formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{"tvdb_with_trailing_dash", "tvdb-81189-"},
		{"tvdb_large_id", "tvdb-999999999-"},
		{"tvdb_single_digit", "tvdb-1-"},
		{"tmdb_with_trailing_dash", "tmdb-1271-"},
		{"tmdb_without_trailing_dash", "tmdb-1271"},
		{"tmdb_single_digit", "tmdb-1"},
		{"imdb_standard", "imdb-tt1234567"},
		{"imdb_short_id", "imdb-tt1"},
		{"imdb_long_id", "imdb-tt12345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !api.IsValidMediaPrefix(tt.prefix) {
				t.Errorf("IsValidMediaPrefix(%q) = false, want true", tt.prefix)
			}
		})
	}
}

func TestIsValidMediaPrefix_invalid_formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{"empty_string", ""},
		{"arbitrary_text", "hello-world"},
		{"tvdb_no_digits", "tvdb--"},
		{"tvdb_no_trailing_dash", "tvdb-81189"},
		{"tmdb_no_digits", "tmdb-"},
		{"imdb_no_tt", "imdb-1234567"},
		{"imdb_no_digits", "imdb-tt"},
		{"just_prefix", "tvdb"},
		{"numeric_only", "12345"},
		{"wrong_case_tvdb", "TVDB-81189-"},
		{"wrong_case_tmdb", "TMDB-1271"},
		{"wrong_case_imdb", "IMDB-tt1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if api.IsValidMediaPrefix(tt.prefix) {
				t.Errorf("IsValidMediaPrefix(%q) = true, want false", tt.prefix)
			}
		})
	}
}

// --- handleConfigParsed ignored_codecs ---

func TestHandleConfigParsed_includes_ignored_codecs(t *testing.T) {
	t.Parallel()
	cfg := &qhMockConfig{
		languages: []string{"fr"},
		providers: map[api.ProviderID]api.ProviderCfg{
			"embedded": {
				Enabled: true,
				Settings: map[string]any{
					"ignore_pgs":    true,
					"ignore_vobsub": true,
				},
			},
		},
		sonarrCfg: api.ArrConfig{},
		radarrCfg: api.ArrConfig{},
	}
	s := newTestServer(&qhMockStore{}, cfg)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/parsed", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigParsed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleConfigParsed() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	codecs, ok := result["ignored_codecs"].([]any)
	if !ok {
		t.Fatal("ignored_codecs not an array in response")
	}
	if len(codecs) != 2 {
		t.Errorf("ignored_codecs count = %d, want 2", len(codecs))
	}
	codecSet := make(map[string]bool)
	for _, c := range codecs {
		codecSet[c.(string)] = true
	}
	if !codecSet["pgs"] {
		t.Error("ignored_codecs missing 'pgs'")
	}
	if !codecSet["vobsub"] {
		t.Error("ignored_codecs missing 'vobsub'")
	}
}
