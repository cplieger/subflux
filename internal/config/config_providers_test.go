package config

import (
	"context"
	"slices"
	"testing"

	"subflux/internal/api"
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

// --- forceEmbeddedProvider ---

func TestLoadFromBytes_embedded_provider_always_enabled(t *testing.T) {
	t.Parallel()
	data := `
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
  embedded:
    enabled: false
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	p, ok := cfg.Providers[api.ProviderID("embedded")]
	if !ok {
		t.Fatal("embedded provider missing from config")
	}
	if !p.Enabled {
		t.Error("embedded provider should be force-enabled, got disabled")
	}
}

func TestLoadFromBytes_embedded_provider_default_settings(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	p, ok := cfg.Providers[api.ProviderID("embedded")]
	if !ok {
		t.Fatal("embedded provider missing from config")
	}
	if !p.Enabled {
		t.Error("embedded provider should be enabled")
	}
	if p.Settings["ignore_pgs"] != true {
		t.Errorf("embedded.ignore_pgs = %v, want true", p.Settings["ignore_pgs"])
	}
	if p.Settings["ignore_vobsub"] != true {
		t.Errorf("embedded.ignore_vobsub = %v, want true", p.Settings["ignore_vobsub"])
	}
	if p.Settings["ignore_ass"] != false {
		t.Errorf("embedded.ignore_ass = %v, want false", p.Settings["ignore_ass"])
	}
}

func TestLoadFromBytes_embedded_provider_explicit_bool_settings(t *testing.T) {
	t.Parallel()
	yaml := `
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
  embedded:
    settings:
      ignore_pgs: true
      ignore_vobsub: false
      ignore_ass: true
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
      use_hash: false
      include_ai_translated: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}

	emb := cfg.Providers[api.ProviderID("embedded")]
	if emb.Settings["ignore_pgs"] != true {
		t.Errorf("embedded.ignore_pgs = %v (%T), want bool true",
			emb.Settings["ignore_pgs"], emb.Settings["ignore_pgs"])
	}
	if emb.Settings["ignore_vobsub"] != false {
		t.Errorf("embedded.ignore_vobsub = %v (%T), want bool false",
			emb.Settings["ignore_vobsub"], emb.Settings["ignore_vobsub"])
	}
	if emb.Settings["ignore_ass"] != true {
		t.Errorf("embedded.ignore_ass = %v (%T), want bool true",
			emb.Settings["ignore_ass"], emb.Settings["ignore_ass"])
	}

	osCfg := cfg.Providers[api.ProviderID("opensubtitles")]
	if osCfg.Settings["use_hash"] != false {
		t.Errorf("opensubtitles.use_hash = %v (%T), want bool false",
			osCfg.Settings["use_hash"], osCfg.Settings["use_hash"])
	}
	if osCfg.Settings["include_ai_translated"] != true {
		t.Errorf("opensubtitles.include_ai_translated = %v (%T), want bool true",
			osCfg.Settings["include_ai_translated"], osCfg.Settings["include_ai_translated"])
	}
	if osCfg.Settings["api_key"] != "test" {
		t.Errorf("opensubtitles.api_key = %v (%T), want string \"test\"",
			osCfg.Settings["api_key"], osCfg.Settings["api_key"])
	}
}

func TestLoadFromBytes_embedded_provider_preserves_user_settings(t *testing.T) {
	t.Parallel()
	yaml := `
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
  embedded:
    settings:
      ignore_pgs: false
      ignore_vobsub: false
      ignore_ass: true
  yifysubtitles:
    enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	emb := cfg.Providers[api.ProviderID("embedded")]
	if emb.Settings["ignore_pgs"] != false {
		t.Errorf("embedded.ignore_pgs = %v (%T), want bool false (user override preserved)",
			emb.Settings["ignore_pgs"], emb.Settings["ignore_pgs"])
	}
	if emb.Settings["ignore_vobsub"] != false {
		t.Errorf("embedded.ignore_vobsub = %v (%T), want bool false (user override preserved)",
			emb.Settings["ignore_vobsub"], emb.Settings["ignore_vobsub"])
	}
	if emb.Settings["ignore_ass"] != true {
		t.Errorf("embedded.ignore_ass = %v (%T), want bool true (user override preserved)",
			emb.Settings["ignore_ass"], emb.Settings["ignore_ass"])
	}
}

func TestLoadFromBytes_yaml_quoted_bools_parsed_as_strings(t *testing.T) {
	t.Parallel()
	yaml := `
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
  embedded:
    settings:
      ignore_pgs: "true"
      ignore_vobsub: "false"
  yifysubtitles:
    enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	emb := cfg.Providers[api.ProviderID("embedded")]
	if _, ok := emb.Settings["ignore_pgs"].(string); !ok {
		t.Errorf("embedded.ignore_pgs type = %T, want string (YAML quoted bool)", emb.Settings["ignore_pgs"])
	}
	if emb.Settings["ignore_pgs"] != "true" {
		t.Errorf("embedded.ignore_pgs = %v, want string \"true\"", emb.Settings["ignore_pgs"])
	}
	if emb.Settings["ignore_vobsub"] != "false" {
		t.Errorf("embedded.ignore_vobsub = %v, want string \"false\"", emb.Settings["ignore_vobsub"])
	}
}
