package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

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
