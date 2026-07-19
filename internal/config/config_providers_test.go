package config

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// --- ProvidersForTarget ---

func TestProvidersForTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		providers []api.ProviderID
		exclude   []api.ProviderID
		all       []api.ProviderID
		want      []api.ProviderID
	}{
		{
			name:      "include_list",
			providers: []api.ProviderID{"opensubtitles", "embedded"},
			all:       []api.ProviderID{"opensubtitles", "embedded", "yify"},
			want:      []api.ProviderID{"opensubtitles", "embedded"},
		},
		{
			name:    "exclude_preserves_order",
			exclude: []api.ProviderID{"yify"},
			all:     []api.ProviderID{"opensubtitles", "embedded", "yify"},
			want:    []api.ProviderID{"opensubtitles", "embedded"},
		},
		{
			name:    "exclude_all",
			exclude: []api.ProviderID{"opensubtitles", "yify"},
			all:     []api.ProviderID{"opensubtitles", "yify"},
			want:    []api.ProviderID{},
		},
		{
			name:    "empty_exclude_falls_through",
			exclude: []api.ProviderID{},
			all:     []api.ProviderID{"opensubtitles", "embedded", "yify"},
			want:    []api.ProviderID{"opensubtitles", "embedded", "yify"},
		},
		{
			name: "all_providers",
			all:  []api.ProviderID{"opensubtitles", "embedded", "yify"},
			want: []api.ProviderID{"opensubtitles", "embedded", "yify"},
		},
		{
			name:      "boundary_empty_providers",
			providers: []api.ProviderID{},
			all:       []api.ProviderID{"os", "yify", "embedded"},
			want:      []api.ProviderID{"os", "yify", "embedded"},
		},
		{
			name:      "non_empty_providers",
			providers: []api.ProviderID{"os"},
			all:       []api.ProviderID{"os", "yify", "embedded"},
			want:      []api.ProviderID{"os"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{}
			target := &api.SubtitleTarget{
				Code:      "fr",
				Providers: tt.providers,
				Exclude:   tt.exclude,
			}
			got := cfg.ProvidersForTarget(target, tt.all)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ProvidersForTarget() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- embedded_subtitles section (typed policy) ---

func TestLoadFromBytes_embedded_subtitles_defaults(t *testing.T) {
	t.Parallel()
	// Absent section: defaults (true/true/false) via the pre-defaulted decode.
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	want := api.EmbeddedPolicy{IgnorePGS: true, IgnoreVobSub: true, IgnoreASS: false}
	if got := cfg.EmbeddedPolicy(); got != want {
		t.Errorf("EmbeddedPolicy() = %+v, want %+v (defaults)", got, want)
	}
}

func TestLoadFromBytes_embedded_subtitles_full_section(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
embedded_subtitles:
  ignore_pgs: false
  ignore_vobsub: false
  ignore_ass: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	want := api.EmbeddedPolicy{IgnorePGS: false, IgnoreVobSub: false, IgnoreASS: true}
	if got := cfg.EmbeddedPolicy(); got != want {
		t.Errorf("EmbeddedPolicy() = %+v, want %+v", got, want)
	}
}

func TestLoadFromBytes_embedded_subtitles_partial_section(t *testing.T) {
	t.Parallel()
	// One field set: the absent fields keep their defaults (vobsub=true,
	// ass=false) through the standard pre-defaulted decode — no
	// presence-detection machinery.
	yaml := minimalValidYAML() + `
embedded_subtitles:
  ignore_pgs: false
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	want := api.EmbeddedPolicy{IgnorePGS: false, IgnoreVobSub: true, IgnoreASS: false}
	if got := cfg.EmbeddedPolicy(); got != want {
		t.Errorf("EmbeddedPolicy() = %+v, want %+v (partial overlay)", got, want)
	}
}

// --- Hard cutover: legacy embedded shapes fail validation (R3.2/R3.3) ---

func TestLoadFromBytes_rejects_providers_embedded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		block string
	}{
		{name: "with_settings", block: `
  embedded:
    settings:
      ignore_pgs: true
`},
		{name: "enabled_only", block: `
  embedded:
    enabled: true
`},
		{name: "empty_block", block: `
  embedded: {}
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// A standalone document (minimalValidYAML already carries a
			// providers: key; appending another would be a duplicate map key).
			yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  default:
    - code: en
providers:` + tt.block + `
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
`
			_, err := LoadFromBytes(context.Background(), []byte(yaml))
			if err == nil {
				t.Fatal("LoadFromBytes() = nil error, want targeted providers.embedded rejection")
			}
			if !errors.Is(err, ErrEmbeddedProviderRemoved) {
				t.Errorf("error = %v, want errors.Is(ErrEmbeddedProviderRemoved)", err)
			}
			if !strings.Contains(err.Error(), "providers.embedded has been replaced by the top-level embedded_subtitles section") {
				t.Errorf("error text = %q, want the targeted guidance message", err)
			}
		})
	}
}

func TestLoadFromBytes_rejects_embedded_in_provider_filters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "include_list_in_rule",
			yaml: `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          providers: [opensubtitles, embedded]
  default:
    - code: en
providers:
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
`,
		},
		{
			name: "exclude_list_in_default",
			yaml: `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  default:
    - code: en
      exclude: [embedded]
providers:
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadFromBytes(context.Background(), []byte(tt.yaml))
			if err == nil {
				t.Fatal("LoadFromBytes() = nil error, want targeted filter-list rejection")
			}
			if !errors.Is(err, ErrEmbeddedProviderRemoved) {
				t.Errorf("error = %v, want errors.Is(ErrEmbeddedProviderRemoved)", err)
			}
		})
	}
}

// --- Zero acquisition providers: valid config, startup WARN (R3.5) ---

func TestLoadFromBytes_zero_providers_loads_with_warn(t *testing.T) {
	// captureLogs swaps the process-global logger: no t.Parallel().
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  default:
    - code: en
`
	var cfg *Config
	var err error
	logs := captureLogs(t, func() {
		cfg, err = LoadFromBytes(context.Background(), []byte(yaml))
	})
	if err != nil {
		t.Fatalf("LoadFromBytes(zero providers) unexpected error: %v", err)
	}
	defer func() { _ = cfg.Close() }()
	if !strings.Contains(logs, "no acquisition providers enabled") {
		t.Errorf("startup WARN missing from logs:\n%s", logs)
	}
}
