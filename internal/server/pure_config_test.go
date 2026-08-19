package server

import (
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
			got := enabledProviders(tt.providers)
			if !slices.Equal(got, tt.want) {
				t.Errorf("enabledProviders() = %v, want %v", got, tt.want)
			}
		})
	}
}

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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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

// The BuildProviderSchemas tests moved to
// internal/server/confighandlers/provider_schema_test.go with the function
// itself, which left internal/api because that package implements no registry
// and consumes no schema.

// --- provider.ClearCaches ---

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

	provider.ClearCaches([]api.Provider{plain, cc})

	if !cc.cleared {
		t.Error("ClearCache not called on provider implementing cacheClearer")
	}
}

func TestClearProviderCaches_no_clearers(t *testing.T) {
	t.Parallel()
	plain := &stubProvider{name: "os"}
	// Should not panic with no cacheClearer providers.
	provider.ClearCaches([]api.Provider{plain})
}

func TestClearProviderCaches_nil_providers(t *testing.T) {
	t.Parallel()
	// Should not panic with nil slice.
	provider.ClearCaches(nil)
}

func TestEnabledProviders_output_is_sorted(t *testing.T) {
	t.Parallel()
	got := enabledProviders(map[api.ProviderID]api.ProviderCfg{
		"zulu":    {Enabled: true},
		"alpha":   {Enabled: true},
		"charlie": {Enabled: true},
		"bravo":   {Enabled: false},
	})
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
