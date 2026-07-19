package queryhandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/testsupport"
)

// stubProvider implements api.Provider for provider-listing tests.
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

// newStateHandler builds a Handler whose LiveState carries the given config
// and providers, with Configured reporting true.
func newStateHandler(cfg *testsupport.NopConfig, providers []api.Provider) *Handler {
	return New(Deps{
		QueryDB: &mockQueryStore{},
		State: func() *LiveState {
			return &LiveState{Cfg: cfg, Providers: providers}
		},
		Configured: func() bool { return true },
	})
}

func TestHandleProviders_returns_provider_info(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		ProviderCfgs: map[api.ProviderID]api.ProviderCfg{
			"opensubtitles": {Enabled: true},
			"yify":          {Enabled: false},
		},
	}
	h := newStateHandler(cfg, []api.Provider{&stubProvider{name: "opensubtitles"}})

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rec := httptest.NewRecorder()
	h.HandleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleProviders() status = %d, want %d", rec.Code, http.StatusOK)
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
		t.Fatalf("HandleProviders() returned %d providers, want 2", len(result))
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
	h := newStateHandler(&testsupport.NopConfig{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/providers", nil)
	rec := httptest.NewRecorder()
	h.HandleProviders(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleProviders(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProviders_empty_config_returns_empty_array(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		ProviderCfgs: map[api.ProviderID]api.ProviderCfg{},
	}
	h := newStateHandler(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rec := httptest.NewRecorder()
	h.HandleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleProviders() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Empty config produces empty slice, which JSON-encodes as "[]".
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("HandleProviders(empty config) body = %q, want %q", body, "[]")
	}
}

// --- HandleProviderTimeout ---

func TestHandleProviderTimeout_rejects_non_get(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodPost, "/api/providers/timeout", nil)
	rec := httptest.NewRecorder()
	h.HandleProviderTimeout(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleProviderTimeout(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProviderTimeout_not_configured_returns_disabled(t *testing.T) {
	t.Parallel()
	// No ProviderTimeout in the search config: the engine's timeout
	// tracker is disabled and the handler reports enabled=false.
	h := newEngineHandler(&testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/providers/timeout", nil)
	rec := httptest.NewRecorder()
	h.HandleProviderTimeout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleProviderTimeout() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["enabled"] != false {
		t.Errorf("enabled = %v, want false when timeout not configured", result["enabled"])
	}
}

func TestHandleProviderTimeout_enabled_returns_providers(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		SearchConfig: api.SearchConfig{ProviderTimeout: 2 * time.Hour},
	}
	h := newEngineHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/providers/timeout", nil)
	rec := httptest.NewRecorder()
	h.HandleProviderTimeout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleProviderTimeout() status = %d, want %d",
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

// --- HandleProviderTimeoutReset ---

func TestHandleProviderTimeoutReset_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/providers/timeout/reset", nil)
	rec := httptest.NewRecorder()
	h.HandleProviderTimeoutReset(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleProviderTimeoutReset(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleProviderTimeoutReset_not_configured_returns_disabled(t *testing.T) {
	t.Parallel()
	h := newEngineHandler(&testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodPost, "/api/providers/timeout/reset", nil)
	rec := httptest.NewRecorder()
	h.HandleProviderTimeoutReset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleProviderTimeoutReset() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["enabled"] != false {
		t.Errorf("enabled = %v, want false when timeout not configured", result["enabled"])
	}
}

func TestHandleProviderTimeoutReset_enabled_resets(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		SearchConfig: api.SearchConfig{ProviderTimeout: 2 * time.Hour},
	}
	h := newEngineHandler(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/timeout/reset", nil)
	rec := httptest.NewRecorder()
	h.HandleProviderTimeoutReset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleProviderTimeoutReset() status = %d, want %d",
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

// --- HandleSearchTargets ---

func TestHandleSearchTargets_returns_targets(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		Targets: []api.SubtitleTarget{
			{Code: "fr"},
			{Code: "en", Variant: "hi"},
		},
	}
	h := newStateHandler(cfg, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/search/targets?orig_lang=en&audio_langs=en,fr", nil)
	rec := httptest.NewRecorder()
	h.HandleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
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
	h := newStateHandler(&testsupport.NopConfig{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/search/targets", nil)
	rec := httptest.NewRecorder()
	h.HandleSearchTargets(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleSearchTargets(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleSearchTargets_filters_empty_audio_langs(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		Targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	h := newStateHandler(cfg, nil)

	// Send audio_langs with empty segments.
	req := httptest.NewRequest(http.MethodGet,
		"/api/search/targets?orig_lang=en&audio_langs=en,,fr,", nil)
	rec := httptest.NewRecorder()
	h.HandleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
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
	cfg := &testsupport.NopConfig{
		Targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	h := newStateHandler(cfg, nil)

	// Send empty audio_langs.
	req := httptest.NewRequest(http.MethodGet,
		"/api/search/targets?orig_lang=en&audio_langs=", nil)
	rec := httptest.NewRecorder()
	h.HandleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
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

func TestHandleSearchTargets_no_targets_returns_empty(t *testing.T) {
	t.Parallel()
	h := newStateHandler(&testsupport.NopConfig{}, nil) // No targets configured.

	req := httptest.NewRequest(http.MethodGet, "/api/search/targets?orig_lang=en", nil)
	rec := httptest.NewRecorder()
	h.HandleSearchTargets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchTargets() status = %d, want %d", rec.Code, http.StatusOK)
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

// --- HandleConfigParsed ---

func TestHandleConfigParsed_rejects_non_get(t *testing.T) {
	t.Parallel()
	h := newStateHandler(&testsupport.NopConfig{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/config/parsed", nil)
	rec := httptest.NewRecorder()
	h.HandleConfigParsed(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleConfigParsed(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleConfigParsed_returns_structured_config(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		Languages: []string{"fr", "en"},
		ProviderCfgs: map[api.ProviderID]api.ProviderCfg{
			"os": {Enabled: true},
		},
		SearchConfig: api.SearchConfig{
			UpgradeEnabled: true, UpgradeWindowDays: 7,
		},
		AdaptiveCfg: api.AdaptiveConfig{Enabled: true},
		SonarrCfg:   api.ArrConfig{URL: "http://sonarr:8989"},
	}
	h := newStateHandler(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/config/parsed", nil)
	rec := httptest.NewRecorder()
	h.HandleConfigParsed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleConfigParsed() status = %d, want %d", rec.Code, http.StatusOK)
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

func TestHandleConfigParsed_includes_ignored_codecs(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		Languages: []string{"fr"},
		Embedded:  api.EmbeddedPolicy{IgnorePGS: true, IgnoreVobSub: true},
	}
	h := newStateHandler(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/config/parsed", nil)
	rec := httptest.NewRecorder()
	h.HandleConfigParsed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleConfigParsed() status = %d, want %d", rec.Code, http.StatusOK)
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

func TestHandleConfigParsed_unconfigured_returns_defaults(t *testing.T) {
	t.Parallel()
	h := New(Deps{
		QueryDB:    &mockQueryStore{},
		State:      func() *LiveState { return &LiveState{} },
		Configured: func() bool { return false },
	})

	req := httptest.NewRequest(http.MethodGet, "/api/config/parsed", nil)
	rec := httptest.NewRecorder()
	h.HandleConfigParsed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleConfigParsed(unconfigured) status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["configured"] != false {
		t.Errorf("configured = %v, want false", result["configured"])
	}
}
