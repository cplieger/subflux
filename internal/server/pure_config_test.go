package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- enabledProviders ---

func TestEnabledProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		providers map[subflux.ProviderID]subflux.ProviderCfg
		want      []subflux.ProviderID
	}{
		{"all enabled", map[subflux.ProviderID]subflux.ProviderCfg{
			"beta":  {Enabled: true},
			"alpha": {Enabled: true},
		}, []subflux.ProviderID{"alpha", "beta"}},
		{"mixed enabled and disabled", map[subflux.ProviderID]subflux.ProviderCfg{
			"os":   {Enabled: true},
			"yify": {Enabled: false},
			"bs":   {Enabled: true},
		}, []subflux.ProviderID{"bs", "os"}},
		{"none enabled", map[subflux.ProviderID]subflux.ProviderCfg{
			"os": {Enabled: false},
		}, nil},
		{"empty providers", map[subflux.ProviderID]subflux.ProviderCfg{}, nil},
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
// itself, which left internal/subflux because that package implements no registry
// and consumes no schema.

// --- configStateView ---

// TestConfigStateView pins the unconfigured branch: the config handlers' live
// view must survive a nil cfg, which is what every new install saves from.
func TestConfigStateView(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured yields a zero view", func(t *testing.T) {
		t.Parallel()
		s := &Server{}
		s.live.Store(&liveState{}) // cfg nil: no activation yet

		got := s.configStateView()

		if want := (confighandlers.StateView{}); got != want {
			t.Errorf("configStateView(unconfigured) = %+v, want %+v", got, want)
		}
	})

	t.Run("configured projects both arr endpoints", func(t *testing.T) {
		t.Parallel()
		// The base fixture declares sonarr; radarr is this test's addition,
		// so a projection that read one block twice would show up here.
		s := &Server{}
		s.live.Store(&liveState{cfg: testConfig(t,
			"radarr:\n  url: \"http://radarr:7878\"\n  api_key: \"k-rad\"\n",
		)})

		got := s.configStateView()

		if got.Sonarr.URL != "http://sonarr:8989" || got.Sonarr.APIKey != "test" {
			t.Errorf("configStateView().Sonarr = %+v, want url http://sonarr:8989 key test", got.Sonarr)
		}
		if got.Radarr.URL != "http://radarr:7878" || got.Radarr.APIKey != "k-rad" {
			t.Errorf("configStateView().Radarr = %+v, want url http://radarr:7878 key k-rad", got.Radarr)
		}
	})
}

// --- provider.ClearCaches ---

// mockCacheClearer tracks whether ClearCache was called.
type mockCacheClearer struct {
	stubProvider

	cleared bool
}

func (m *mockCacheClearer) ClearCache() { m.cleared = true }

func TestClearCaches_calls_cache_clearers(t *testing.T) {
	t.Parallel()
	cc := &mockCacheClearer{name: "hdbits"}
	plain := &stubProvider{name: "os"}

	provider.ClearCaches([]provider.Provider{plain, cc})

	if !cc.cleared {
		t.Error("ClearCache not called on provider implementing cacheClearer")
	}
}

func TestClearCaches_no_clearers(t *testing.T) {
	t.Parallel()
	plain := &stubProvider{name: "os"}
	// Should not panic with no cacheClearer providers.
	provider.ClearCaches([]provider.Provider{plain})
}

func TestClearCaches_nil_providers(t *testing.T) {
	t.Parallel()
	// Should not panic with nil slice.
	provider.ClearCaches(nil)
}

func TestEnabledProviders_output_is_sorted(t *testing.T) {
	t.Parallel()
	got := enabledProviders(map[subflux.ProviderID]subflux.ProviderCfg{
		"zulu":    {Enabled: true},
		"alpha":   {Enabled: true},
		"charlie": {Enabled: true},
		"bravo":   {Enabled: false},
	})
	want := []subflux.ProviderID{"alpha", "charlie", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("enabledProviders len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("enabledProviders[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
