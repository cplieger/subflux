package config

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- Accessors ---

func TestAccessors_return_configured_values(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}

	t.Run("SonarrConfig", func(t *testing.T) {
		t.Parallel()
		got := cfg.SonarrConfig()
		if got.URL != "http://sonarr:8989" {
			t.Errorf("SonarrConfig().URL = %q, want %q", got.URL, "http://sonarr:8989")
		}
		if got.APIKey != "test" {
			t.Errorf("SonarrConfig().APIKey = %q, want %q", got.APIKey, "test")
		}
	})

	t.Run("RadarrConfig_empty", func(t *testing.T) {
		t.Parallel()
		got := cfg.RadarrConfig()
		if got.URL != "" {
			t.Errorf("RadarrConfig().URL = %q, want empty", got.URL)
		}
	})

	t.Run("Adaptive", func(t *testing.T) {
		t.Parallel()
		got := cfg.Adaptive()
		if !got.Enabled {
			t.Error("Adaptive().Enabled = false, want true")
		}
		if got.BackoffMultiplier != 2 {
			t.Errorf("Adaptive().BackoffMultiplier = %v, want 2", got.BackoffMultiplier)
		}
	})

	t.Run("Search", func(t *testing.T) {
		t.Parallel()
		got := cfg.Search()
		if len(got.ExcludeArrTags) != 1 || got.ExcludeArrTags[0] != "no-subflux" {
			t.Errorf("Search().ExcludeArrTags = %v, want [no-subflux]", got.ExcludeArrTags)
		}
	})

	t.Run("UpgradeInSearch", func(t *testing.T) {
		t.Parallel()
		got := cfg.Search()
		if !got.UpgradeEnabled {
			t.Error("Search().UpgradeEnabled = false, want true")
		}
		if got.UpgradeWindowDays != 7 {
			t.Errorf("Search().UpgradeWindowDays = %d, want 7", got.UpgradeWindowDays)
		}
	})

	t.Run("ProviderConfigs", func(t *testing.T) {
		t.Parallel()
		got := cfg.ProviderConfigs()
		p, ok := got["opensubtitles"]
		if !ok {
			t.Fatal("ProviderConfigs() missing opensubtitles")
		}
		if !p.Enabled {
			t.Error("ProviderConfigs()[opensubtitles].Enabled = false, want true")
		}
	})

	t.Run("LoggingLevel", func(t *testing.T) {
		t.Parallel()
		got := cfg.LoggingLevel()
		if got != "info" {
			t.Errorf("LoggingLevel() = %q, want %q", got, "info")
		}
	})

	t.Run("LoggingFormat", func(t *testing.T) {
		t.Parallel()
		got := cfg.LoggingFormat()
		if got != "json" {
			t.Errorf("LoggingFormat() = %q, want %q", got, "json")
		}
	})

	t.Run("ServerPort", func(t *testing.T) {
		t.Parallel()
		if got := cfg.ServerPort(); got != 8374 {
			t.Errorf("ServerPort() = %d, want %d", got, 8374)
		}
	})

	t.Run("PollInterval", func(t *testing.T) {
		t.Parallel()
		if got := cfg.PollInterval(); got != 30*time.Second {
			t.Errorf("PollInterval() = %v, want 30s", got)
		}
	})
}

// --- ProviderPriority ---

func TestProviderPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		providers map[api.ProviderID]yamlProviderCfg
		query     api.ProviderID
		want      int
	}{
		{
			name: "configured_positive",
			providers: map[api.ProviderID]yamlProviderCfg{
				"opensubtitles": {Enabled: true, Priority: 1},
				"yify":          {Enabled: true, Priority: 5},
			},
			query: "opensubtitles",
			want:  1,
		},
		{
			name: "configured_positive_second",
			providers: map[api.ProviderID]yamlProviderCfg{
				"opensubtitles": {Enabled: true, Priority: 1},
				"yify":          {Enabled: true, Priority: 5},
			},
			query: "yify",
			want:  5,
		},
		{
			name: "zero_returns_default",
			providers: map[api.ProviderID]yamlProviderCfg{
				"opensubtitles": {Enabled: true, Priority: 0},
			},
			query: "opensubtitles",
			want:  99,
		},
		{
			name: "unknown_provider_returns_default",
			providers: map[api.ProviderID]yamlProviderCfg{
				"opensubtitles": {Enabled: true, Priority: 1},
			},
			query: "nonexistent",
			want:  99,
		},
		{
			name:      "nil_providers_returns_default",
			providers: nil,
			query:     "anything",
			want:      99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{Providers: tt.providers}
			got := cfg.ProviderPriority(tt.query)
			if got != tt.want {
				t.Errorf("ProviderPriority(%q) = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

// --- arrConfig URL fallback ---

func TestArrConfig_url_only_fills_public_url(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "key"},
	}

	got := cfg.SonarrConfig()
	if got.URL != "http://sonarr:8989" {
		t.Errorf("SonarrConfig().URL = %q, want %q", got.URL, "http://sonarr:8989")
	}
	if got.PublicURL != "http://sonarr:8989" {
		t.Errorf("SonarrConfig().PublicURL = %q, want %q (fallback from URL)", got.PublicURL, "http://sonarr:8989")
	}
}

func TestArrConfig_public_url_only_fills_url(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Radarr: yamlArrConfig{PublicURL: "http://radarr.example.com", APIKey: "key"},
	}

	got := cfg.RadarrConfig()
	if got.URL != "http://radarr.example.com" {
		t.Errorf("RadarrConfig().URL = %q, want %q (fallback from PublicURL)", got.URL, "http://radarr.example.com")
	}
	if got.PublicURL != "http://radarr.example.com" {
		t.Errorf("RadarrConfig().PublicURL = %q, want %q", got.PublicURL, "http://radarr.example.com")
	}
}

func TestArrConfig_both_urls_preserved(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Sonarr: yamlArrConfig{
			URL:       "http://sonarr:8989",
			PublicURL: "http://sonarr.example.com",
			APIKey:    "key",
		},
	}

	got := cfg.SonarrConfig()
	if got.URL != "http://sonarr:8989" {
		t.Errorf("SonarrConfig().URL = %q, want %q", got.URL, "http://sonarr:8989")
	}
	if got.PublicURL != "http://sonarr.example.com" {
		t.Errorf("SonarrConfig().PublicURL = %q, want %q", got.PublicURL, "http://sonarr.example.com")
	}
}

func TestArrConfig_neither_url_set(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Sonarr: yamlArrConfig{APIKey: "key"},
	}

	got := cfg.SonarrConfig()
	if got.URL != "" {
		t.Errorf("SonarrConfig().URL = %q, want empty", got.URL)
	}
	if got.PublicURL != "" {
		t.Errorf("SonarrConfig().PublicURL = %q, want empty", got.PublicURL)
	}
}

// --- warnArrURLs coverage: public_url only branch ---

func TestValidate_radarr_public_url_only_passes(t *testing.T) {
	t.Parallel()
	// warnArrURLs "public_url set, url empty" branch.
	cfg := &Config{
		Radarr: yamlArrConfig{PublicURL: "http://radarr.example.com", APIKey: "test-key"},
		Languages: LanguageRules{
			Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
		},
		PollIntervalCfg: Duration{D: 30 * time.Second},
		Providers:       map[api.ProviderID]yamlProviderCfg{"test": {Enabled: true}},
		SearchCfg:       yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}, UpgradeWindowDays: 7},
	}
	if err := validate(context.Background(), cfg); err != nil {
		t.Errorf("validate() unexpected error for radarr with public_url only: %v", err)
	}
}

// --- PostProcessConfig ---

func TestPostProcessConfig_returns_configured_values(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		PostProcessing: yamlPostProcessConfig{
			StripHI:          true,
			StripTags:        false,
			NormalizeUTF8:    true,
			CleanWhitespace:  false,
			NormalizeEndings: true,
			RemoveEmpty:      false,
		},
	}

	got := cfg.PostProcessConfig()
	if got.StripHI != true {
		t.Errorf("PostProcessConfig().StripHI = %v, want true", got.StripHI)
	}
	if got.StripTags != false {
		t.Errorf("PostProcessConfig().StripTags = %v, want false", got.StripTags)
	}
	if got.NormalizeUTF8 != true {
		t.Errorf("PostProcessConfig().NormalizeUTF8 = %v, want true", got.NormalizeUTF8)
	}
	if got.CleanWhitespace != false {
		t.Errorf("PostProcessConfig().CleanWhitespace = %v, want false", got.CleanWhitespace)
	}
	if got.NormalizeEndings != true {
		t.Errorf("PostProcessConfig().NormalizeEndings = %v, want true", got.NormalizeEndings)
	}
	if got.RemoveEmpty != false {
		t.Errorf("PostProcessConfig().RemoveEmpty = %v, want false", got.RemoveEmpty)
	}
}

// --- SonarrConfig/RadarrConfig disabled branch ---

func TestSonarrConfig_disabled_returns_empty(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Sonarr: yamlArrConfig{
			Enabled: new(false),
			URL:     "http://sonarr:8989",
			APIKey:  "key",
		},
	}

	got := cfg.SonarrConfig()
	if got.URL != "" {
		t.Errorf("SonarrConfig().URL = %q, want empty (disabled)", got.URL)
	}
	if got.APIKey != "" {
		t.Errorf("SonarrConfig().APIKey = %q, want empty (disabled)", got.APIKey)
	}
}

func TestRadarrConfig_disabled_returns_empty(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Radarr: yamlArrConfig{
			Enabled: new(false),
			URL:     "http://radarr:7878",
			APIKey:  "key",
		},
	}

	got := cfg.RadarrConfig()
	if got.URL != "" {
		t.Errorf("RadarrConfig().URL = %q, want empty (disabled)", got.URL)
	}
	if got.APIKey != "" {
		t.Errorf("RadarrConfig().APIKey = %q, want empty (disabled)", got.APIKey)
	}
}

// --- SyncConfig ---

func TestSyncConfig_returns_configured_value(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		PostProcessing: yamlPostProcessConfig{
			SyncSubtitles: true,
		},
	}
	got := cfg.SyncConfig()
	if !got.SyncSubtitles {
		t.Error("SyncConfig().SyncSubtitles = false, want true")
	}
}

func TestSyncConfig_returns_false_when_disabled(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		PostProcessing: yamlPostProcessConfig{
			SyncSubtitles: false,
		},
	}
	got := cfg.SyncConfig()
	if got.SyncSubtitles {
		t.Error("SyncConfig().SyncSubtitles = true, want false")
	}
}

func TestSyncConfig_audio_sync_fallback(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		PostProcessing: yamlPostProcessConfig{
			SyncSubtitles:     true,
			AudioSyncFallback: true,
		},
	}
	got := cfg.SyncConfig()
	if !got.AudioSyncFallback {
		t.Error("SyncConfig().AudioSyncFallback = false, want true")
	}
}

func TestValidate_min_score_boundary_values(t *testing.T) {
	t.Parallel()
	for _, score := range []int{0, 50, 100} {
		t.Run("min_score="+strconv.Itoa(score), func(t *testing.T) {
			t.Parallel()
			cfg := &Config{
				Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
				Languages: LanguageRules{
					Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
				},
				Providers:       map[api.ProviderID]yamlProviderCfg{"test": {Enabled: true}},
				PollIntervalCfg: Duration{D: 30 * time.Second},
				SearchCfg:       yamlSearchConfig{MinScore: score, ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}, UpgradeWindowDays: 7},
			}
			if err := validate(context.Background(), cfg); err != nil {
				t.Errorf("validate() unexpected error for min_score=%d: %v", score, err)
			}
		})
	}
}

// TestConfig_Validate_property verifies that Config.Validate() (the exported
// method satisfying the Validator interface) agrees with the package-level
// validate() function for any valid config loaded from bytes.
func TestConfig_Validate_property(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML()
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Config.Validate() = %v, want nil for valid config", err)
	}
	// Confirm interface satisfaction at runtime.
	var v Validator = cfg
	if err := v.Validate(); err != nil {
		t.Errorf("Validator.Validate() = %v, want nil", err)
	}
}
