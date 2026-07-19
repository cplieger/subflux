package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- enabledProviders ---

func TestEnabledProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		providers map[api.ProviderID]api.ProviderCfg
		want      []api.ProviderID
	}{
		{"all enabled", map[api.ProviderID]api.ProviderCfg{
			"beta":  {Enabled: true},
			"alpha": {Enabled: true},
		}, []api.ProviderID{"alpha", "beta"}},
		{"mixed enabled and disabled", map[api.ProviderID]api.ProviderCfg{
			"os":   {Enabled: true},
			"yify": {Enabled: false},
			"bs":   {Enabled: true},
		}, []api.ProviderID{"bs", "os"}},
		{"none enabled", map[api.ProviderID]api.ProviderCfg{
			"os": {Enabled: false},
		}, nil},
		{"empty providers", map[api.ProviderID]api.ProviderCfg{}, nil},
		{"nil providers", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &epMock{providers: tt.providers}
			got := enabledProviders(mock)
			if !slices.Equal(got, tt.want) {
				t.Errorf("enabledProviders() = %v, want %v", got, tt.want)
			}
		})
	}
}

// epMock satisfies the interface{ ProviderConfigs() map[api.ProviderID]api.ProviderCfg }.
type epMock struct {
	providers map[api.ProviderID]api.ProviderCfg
}

func (m *epMock) ProviderConfigs() map[api.ProviderID]api.ProviderCfg { return m.providers }

// --- requireConfigured middleware ---

func TestRequireConfigured_blocks_unconfigured(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{})
	// configured is false by default (zero value).

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := s.requireConfigured(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("requireConfigured(unconfigured) status = %d, want %d",
			rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRequireConfigured_passes_when_configured(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{})
	s.configured.Store(true)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := s.requireConfigured(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("requireConfigured(configured) status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if !called {
		t.Error("inner handler was not called when configured")
	}
}

// The handleResetConfig and handleConfigSchema tests formerly in this file
// moved to internal/server/confighandlers/handler_test.go with the rest of
// the config HTTP surface.

// --- buildProviderSchemas ---

func TestBuildProviderSchemas_empty_registry(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	schemas := api.BuildProviderSchemas(reg)
	if len(schemas) != 0 {
		t.Errorf("BuildProviderSchemas(empty) = %d schemas, want 0", len(schemas))
	}
}

func TestBuildProviderSchemas_with_providers(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("gestdown", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "gestdown"}, nil
	})
	reg.Register("opensubtitles", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "opensubtitles"}, nil
	})
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", []api.ProviderSchemaField{
		{Key: "api_key", Label: "API Key", Type: "secret", Secret: true},
		{Key: "username", Label: "Username", Type: "text"},
	})
	// gestdown has no schema registered; label should fall back to name.

	schemas := api.BuildProviderSchemas(reg)

	if len(schemas) != 2 {
		t.Fatalf("BuildProviderSchemas() = %d schemas, want 2", len(schemas))
	}

	// Schemas should be in sorted order (from ProviderNames).
	if schemas[0].Name != "gestdown" {
		t.Errorf("schemas[0].Name = %q, want %q", schemas[0].Name, "gestdown")
	}
	if schemas[1].Name != "opensubtitles" {
		t.Errorf("schemas[1].Name = %q, want %q", schemas[1].Name, "opensubtitles")
	}

	// gestdown: label falls back to name.
	if schemas[0].Label != "gestdown" {
		t.Errorf("gestdown.Label = %q, want %q (fallback to name)", schemas[0].Label, "gestdown")
	}

	if schemas[1].Label != "OpenSubtitles" {
		t.Errorf("opensubtitles.Label = %q, want %q", schemas[1].Label, "OpenSubtitles")
	}
	if len(schemas[1].Settings) != 2 {
		t.Fatalf("opensubtitles.Settings = %d fields, want 2", len(schemas[1].Settings))
	}
	if schemas[1].Settings[0].Key != "api_key" {
		t.Errorf("settings[0].Key = %q, want %q", schemas[1].Settings[0].Key, "api_key")
	}
	if !schemas[1].Settings[0].Secret {
		t.Error("settings[0].Secret = false, want true")
	}
}

// --- provider.ClearProviderCaches ---

// mockCacheClearer tracks whether ClearCache was called.
type mockCacheClearer struct {
	stubProvider

	cleared bool
}

func (m *mockCacheClearer) ClearCache() { m.cleared = true }

func TestClearProviderCaches_calls_cache_clearers(t *testing.T) {
	t.Parallel()
	cc := &mockCacheClearer{stubProvider: stubProvider{name: "hdbits"}}
	plain := &stubProvider{name: "os"}

	provider.ClearProviderCaches([]api.Provider{plain, cc})

	if !cc.cleared {
		t.Error("ClearCache not called on provider implementing cacheClearer")
	}
}

func TestClearProviderCaches_no_clearers(t *testing.T) {
	t.Parallel()
	plain := &stubProvider{name: "os"}
	// Should not panic with no cacheClearer providers.
	provider.ClearProviderCaches([]api.Provider{plain})
}

func TestClearProviderCaches_nil_providers(t *testing.T) {
	t.Parallel()
	// Should not panic with nil slice.
	provider.ClearProviderCaches(nil)
}

func TestBuildProviderSchemas_excludes_mock_provider(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("mock", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "mock"}, nil
	})
	reg.RegisterSchema("mock", "Mock Provider", nil)
	reg.Register("opensubtitles", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "opensubtitles"}, nil
	})
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", nil)

	schemas := api.BuildProviderSchemas(reg, "mock")
	for _, s := range schemas {
		if s.Name == "mock" {
			t.Error("BuildProviderSchemas should exclude 'mock' provider")
		}
	}
	if len(schemas) != 1 {
		t.Errorf("BuildProviderSchemas len = %d, want 1 (mock excluded)", len(schemas))
	}
}

func TestEnabledProviders_output_is_sorted(t *testing.T) {
	t.Parallel()
	cfg := &epMock{providers: map[api.ProviderID]api.ProviderCfg{
		"zulu":    {Enabled: true},
		"alpha":   {Enabled: true},
		"charlie": {Enabled: true},
		"bravo":   {Enabled: false},
	}}
	got := enabledProviders(cfg)
	want := []api.ProviderID{"alpha", "charlie", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("enabledProviders len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("enabledProviders[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
