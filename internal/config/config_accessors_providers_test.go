package config

import (
	"strconv"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- Accessors ---

func TestAccessors_return_configured_values(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(t.Context(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}

	t.Run("SonarrConfig", func(t *testing.T) {
		t.Parallel()
		got := cfg.Sonarr()
		if got.URL != "http://sonarr:8989" {
			t.Errorf("Sonarr().URL = %q, want %q", got.URL, "http://sonarr:8989")
		}
		if got.APIKey != "test" {
			t.Errorf("Sonarr().APIKey = %q, want %q", got.APIKey, "test")
		}
	})

	t.Run("RadarrConfig_empty", func(t *testing.T) {
		t.Parallel()
		got := cfg.Radarr()
		if got.URL != "" {
			t.Errorf("Radarr().URL = %q, want empty", got.URL)
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
		SonarrCfg: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "key"},
	}

	got := cfg.Sonarr()
	if got.URL != "http://sonarr:8989" {
		t.Errorf("Sonarr().URL = %q, want %q", got.URL, "http://sonarr:8989")
	}
	if got.PublicURL != "http://sonarr:8989" {
		t.Errorf("Sonarr().PublicURL = %q, want %q (fallback from URL)", got.PublicURL, "http://sonarr:8989")
	}
}

func TestArrConfig_public_url_only_fills_url(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		RadarrCfg: yamlArrConfig{PublicURL: "http://radarr.example.com", APIKey: "key"},
	}

	got := cfg.Radarr()
	if got.URL != "http://radarr.example.com" {
		t.Errorf("Radarr().URL = %q, want %q (fallback from PublicURL)", got.URL, "http://radarr.example.com")
	}
	if got.PublicURL != "http://radarr.example.com" {
		t.Errorf("Radarr().PublicURL = %q, want %q", got.PublicURL, "http://radarr.example.com")
	}
}

func TestArrConfig_both_urls_preserved(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrCfg: yamlArrConfig{
			URL:       "http://sonarr:8989",
			PublicURL: "http://sonarr.example.com",
			APIKey:    "key",
		},
	}

	got := cfg.Sonarr()
	if got.URL != "http://sonarr:8989" {
		t.Errorf("Sonarr().URL = %q, want %q", got.URL, "http://sonarr:8989")
	}
	if got.PublicURL != "http://sonarr.example.com" {
		t.Errorf("Sonarr().PublicURL = %q, want %q", got.PublicURL, "http://sonarr.example.com")
	}
}

func TestArrConfig_neither_url_set(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrCfg: yamlArrConfig{APIKey: "key"},
	}

	got := cfg.Sonarr()
	if got.URL != "" {
		t.Errorf("Sonarr().URL = %q, want empty", got.URL)
	}
	if got.PublicURL != "" {
		t.Errorf("Sonarr().PublicURL = %q, want empty", got.PublicURL)
	}
}

// --- warnArrURLs coverage: public_url only branch ---

func TestValidate_radarr_public_url_only_passes(t *testing.T) {
	t.Parallel()
	// warnArrURLs "public_url set, url empty" branch.
	cfg := &Config{
		RadarrCfg: yamlArrConfig{PublicURL: "http://radarr.example.com", APIKey: "test-key"},
		Languages: LanguageRules{
			Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
		},
		PollIntervalCfg: Duration{D: 30 * time.Second},
		Providers:       map[api.ProviderID]yamlProviderCfg{"test": {Enabled: true}},
		SearchCfg:       yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}, UpgradeWindowDays: 7},
	}
	if err := validate(t.Context(), cfg); err != nil {
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

	got := cfg.PostProcess()
	if got.StripHI != true {
		t.Errorf("PostProcess().StripHI = %v, want true", got.StripHI)
	}
	if got.StripTags != false {
		t.Errorf("PostProcess().StripTags = %v, want false", got.StripTags)
	}
	if got.NormalizeUTF8 != true {
		t.Errorf("PostProcess().NormalizeUTF8 = %v, want true", got.NormalizeUTF8)
	}
	if got.CleanWhitespace != false {
		t.Errorf("PostProcess().CleanWhitespace = %v, want false", got.CleanWhitespace)
	}
	if got.NormalizeEndings != true {
		t.Errorf("PostProcess().NormalizeEndings = %v, want true", got.NormalizeEndings)
	}
	if got.RemoveEmpty != false {
		t.Errorf("PostProcess().RemoveEmpty = %v, want false", got.RemoveEmpty)
	}
}

// --- SonarrConfig/RadarrConfig disabled branch ---

func TestSonarrConfig_disabled_returns_empty(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SonarrCfg: yamlArrConfig{
			Enabled: new(false),
			URL:     "http://sonarr:8989",
			APIKey:  "key",
		},
	}

	got := cfg.Sonarr()
	if got.URL != "" {
		t.Errorf("Sonarr().URL = %q, want empty (disabled)", got.URL)
	}
	if got.APIKey != "" {
		t.Errorf("Sonarr().APIKey = %q, want empty (disabled)", got.APIKey)
	}
}

func TestRadarrConfig_disabled_returns_empty(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		RadarrCfg: yamlArrConfig{
			Enabled: new(false),
			URL:     "http://radarr:7878",
			APIKey:  "key",
		},
	}

	got := cfg.Radarr()
	if got.URL != "" {
		t.Errorf("Radarr().URL = %q, want empty (disabled)", got.URL)
	}
	if got.APIKey != "" {
		t.Errorf("Radarr().APIKey = %q, want empty (disabled)", got.APIKey)
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
	got := cfg.Sync()
	if !got.SyncSubtitles {
		t.Error("Sync().SyncSubtitles = false, want true")
	}
}

func TestSyncConfig_returns_false_when_disabled(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		PostProcessing: yamlPostProcessConfig{
			SyncSubtitles: false,
		},
	}
	got := cfg.Sync()
	if got.SyncSubtitles {
		t.Error("Sync().SyncSubtitles = true, want false")
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
	got := cfg.Sync()
	if !got.AudioSyncFallback {
		t.Error("Sync().AudioSyncFallback = false, want true")
	}
}

func TestValidate_min_score_boundary_values(t *testing.T) {
	t.Parallel()
	for _, score := range []int{0, 50, 100} {
		t.Run("min_score="+strconv.Itoa(score), func(t *testing.T) {
			t.Parallel()
			cfg := &Config{
				SonarrCfg: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
				Languages: LanguageRules{
					Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
				},
				Providers:       map[api.ProviderID]yamlProviderCfg{"test": {Enabled: true}},
				PollIntervalCfg: Duration{D: 30 * time.Second},
				SearchCfg:       yamlSearchConfig{MinScore: score, ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}, UpgradeWindowDays: 7},
			}
			if err := validate(t.Context(), cfg); err != nil {
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
	cfg, err := LoadFromBytes(t.Context(), []byte(yaml))
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

// --- SyncConfig min-confidence defaulting ---

func TestSyncConfig_zero_confidence_uses_default(t *testing.T) {
	t.Parallel()
	// A zero SyncMinConfidence is replaced by the default (0.6).
	czero := &Config{}
	czero.PostProcessing.SyncMinConfidence = 0
	if got := czero.Sync().SyncMinConfidence; got != 0.6 {
		t.Errorf("Sync().SyncMinConfidence(0) = %v, want 0.6 (DefaultSyncMinConfidence)", got)
	}
	// A non-zero value is preserved unchanged.
	cset := &Config{}
	cset.PostProcessing.SyncMinConfidence = 0.42
	if got := cset.Sync().SyncMinConfidence; got != 0.42 {
		t.Errorf("Sync().SyncMinConfidence(0.42) = %v, want 0.42", got)
	}
}

// --- ProviderConfigs cache ---

func TestProviderConfigs_uses_cache_when_present(t *testing.T) {
	t.Parallel()
	c := &Config{}
	c.cachedProviderConfigs = map[api.ProviderID]api.ProviderCfg{
		api.ProviderID("cached"): {Priority: 7, Enabled: true},
	}
	// A distinct fallback source makes cache-vs-fallback observable.
	c.Providers = map[api.ProviderID]yamlProviderCfg{
		api.ProviderID("fallback"): {Enabled: true, Priority: 9},
	}

	got := c.ProviderConfigs()
	if _, ok := got[api.ProviderID("cached")]; !ok {
		t.Errorf("ProviderConfigs() = %v, want the cached map (key \"cached\" present)", got)
	}
	if _, ok := got[api.ProviderID("fallback")]; ok {
		t.Errorf("ProviderConfigs() = %v, want the cached map, not the fallback built from Providers", got)
	}
}
