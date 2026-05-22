package config

import (
	"context"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
)

// TestValidate consolidates validation tests into a single table-driven test.
func TestValidate(t *testing.T) {
	t.Parallel()

	// validBase returns a minimal valid Config for boundary testing.
	validBase := func() *Config {
		return &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			Providers:       map[api.ProviderID]yamlProviderCfg{"test": {Enabled: true}},
			PollIntervalCfg: Duration{D: 30 * time.Second},
			SearchCfg:       yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}, UpgradeWindowDays: 7},
		}
	}

	tests := []struct {
		name        string
		cfg         *Config
		wantErr     bool
		errContains string
	}{
		// arr configuration
		{"no arr configured", &Config{
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, ""},
		{"sonarr missing api_key", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, "sonarr"},
		{"radarr missing api_key", &Config{
			Radarr: yamlArrConfig{URL: "http://radarr:7878"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, "radarr"},
		{"both arr missing api_key", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989"},
			Radarr: yamlArrConfig{URL: "http://radarr:7878"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, "sonarr"},
		{"sonarr only passes", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, false, ""},
		{"radarr only passes", &Config{
			Radarr: yamlArrConfig{URL: "http://radarr:7878", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, false, ""},
		{"both arr passes", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Radarr: yamlArrConfig{URL: "http://radarr:7878", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, false, ""},

		// language rules
		{"no default fails", &Config{
			Sonarr:          yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, ""},
		{"rules without default fails", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, true, ""},
		{"empty audio in rule", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, ""},
		{"empty subtitle code in rule", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: ""}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, ""},
		{"empty subtitle code in default", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Default: []yamlSubtitleTarget{{Code: ""}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, ""},
		{"default rules only passes", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, false, ""},
		{"duplicate audio rule", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{
					{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}},
					{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "de"}}},
				},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, true, "duplicate"},

		// providers
		{"no enabled providers", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: false}},
		}, true, ""},
		{"empty providers map", &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{},
		}, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validate(context.Background(), tt.cfg)
			if tt.wantErr && err == nil {
				t.Fatalf("validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
			if tt.errContains != "" && err != nil && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("validate() error = %q, want substring %q", err, tt.errContains)
			}
		})
	}

	// Boundary-value subtests using mutate pattern for cleaner expression.
	boundaryTests := []struct {
		name        string
		mutate      func(*Config)
		wantErr     bool
		errContains string
	}{
		// provider_timeout boundaries
		{"provider_timeout below minimum", func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: 30 * time.Minute}
		}, true, ""},
		{"provider_timeout one below minimum", func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: time.Hour - time.Nanosecond}
		}, true, ""},
		{"provider_timeout zero disables", func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: 0}
		}, false, ""},
		{"provider_timeout at minimum", func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: time.Hour}
		}, false, ""},

		// scan_delay boundaries
		{"scan_delay below minimum", func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: time.Second}
		}, true, ""},
		{"scan_delay one below minimum", func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: 5*time.Second - time.Nanosecond}
		}, true, ""},
		{"scan_delay exact minimum", func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: 5 * time.Second}
		}, false, ""},
		{"scan_delay zero", func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: 0}
		}, true, ""},

		// scan_interval boundaries
		{"scan_interval below minimum", func(c *Config) {
			c.SearchCfg.ScanInterval = Duration{D: 30 * time.Minute}
		}, true, "scan_interval"},
		{"scan_interval one below minimum", func(c *Config) {
			c.SearchCfg.ScanInterval = Duration{D: time.Hour - time.Nanosecond}
		}, true, "scan_interval"},
		{"scan_interval at minimum", func(c *Config) {
			c.SearchCfg.ScanInterval = Duration{D: time.Hour}
		}, false, ""},

		// upgrade_window_days boundaries
		{"upgrade zero window_days", func(c *Config) {
			c.SearchCfg.UpgradeEnabled = true
			c.SearchCfg.UpgradeWindowDays = 0
		}, true, ""},
		{"upgrade negative window_days", func(c *Config) {
			c.SearchCfg.UpgradeEnabled = true
			c.SearchCfg.UpgradeWindowDays = -1
		}, true, ""},
		{"upgrade window_days one", func(c *Config) {
			c.SearchCfg.UpgradeEnabled = true
			c.SearchCfg.UpgradeWindowDays = 1
		}, false, ""},

		// adaptive_backoff boundaries
		{"adaptive backoff below one", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 0.5,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
			}
		}, true, ""},
		{"adaptive backoff exactly one", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 1.0,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
			}
		}, false, ""},

		// adaptive_initial_delay boundaries
		{"adaptive initial_delay zero", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 0}, MaxDelay: Duration{D: 48 * time.Hour},
			}
		}, true, ""},

		// adaptive_max_delay boundaries
		{"adaptive max_delay less than initial", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 48 * time.Hour}, MaxDelay: Duration{D: 24 * time.Hour},
			}
		}, true, ""},
		{"adaptive max_delay equals initial", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 24 * time.Hour},
			}
		}, false, ""},

		// adaptive disabled skips validation
		{"adaptive disabled skips checks", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: false, BackoffMultiplier: 0,
				InitialDelay: Duration{D: 0}, MaxDelay: Duration{D: 0},
			}
		}, false, ""},

		// poll_interval boundaries
		{"poll_interval too short", func(c *Config) {
			c.PollIntervalCfg = Duration{D: 5 * time.Second}
		}, true, "poll_interval"},
		{"poll_interval exact minimum", func(c *Config) {
			c.PollIntervalCfg = Duration{D: 10 * time.Second}
		}, false, ""},
		{"poll_interval one below minimum", func(c *Config) {
			c.PollIntervalCfg = Duration{D: 10*time.Second - time.Nanosecond}
		}, true, "poll_interval"},

		// adaptive_max_attempts boundaries
		{"adaptive negative max_attempts", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
				MaxAttempts: -1,
			}
		}, true, "max_attempts"},
		{"adaptive zero max_attempts valid", func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
				MaxAttempts: 0,
			}
		}, false, ""},

		// min_score boundaries
		{"min_score negative", func(c *Config) {
			c.SearchCfg.MinScore = -1
		}, true, "min_score"},
		{"min_score over 100", func(c *Config) {
			c.SearchCfg.MinScore = 101
		}, true, "min_score"},

		// audio_sync_fallback boundaries
		{"audio_sync_fallback without sync_subtitles", func(c *Config) {
			c.PostProcessing = yamlPostProcessConfig{SyncSubtitles: false, AudioSyncFallback: true}
		}, true, "audio_sync_fallback"},
		{"audio_sync_fallback with sync_subtitles", func(c *Config) {
			c.PostProcessing = yamlPostProcessConfig{SyncSubtitles: true, AudioSyncFallback: true}
		}, false, ""},

		// logging boundaries
		{"invalid logging level", func(c *Config) {
			c.Logging = LoggingConfig{Level: "banana", Format: "json"}
		}, true, "logging.level"},
		{"invalid logging format", func(c *Config) {
			c.Logging = LoggingConfig{Level: "info", Format: "xml"}
		}, true, "logging.format"},

		// per-target min_score boundaries
		{"per-target min_score negative", func(c *Config) {
			c.Languages.Rules = []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr", MinScore: new(-1)}}}}
		}, true, "min_score"},
		{"per-target min_score over 100", func(c *Config) {
			c.Languages.Rules = []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr", MinScore: new(101)}}}}
		}, true, "min_score"},
		{"default target min_score over 100", func(c *Config) {
			c.Languages.Default = []yamlSubtitleTarget{{Code: "en", MinScore: new(200)}}
		}, true, "min_score"},
	}

	for _, tt := range boundaryTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validBase()
			tt.mutate(cfg)
			err := validate(context.Background(), cfg)
			if tt.wantErr && err == nil {
				t.Fatalf("validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
			if tt.errContains != "" && err != nil && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("validate() error = %q, want substring %q", err, tt.errContains)
			}
		})
	}
}
