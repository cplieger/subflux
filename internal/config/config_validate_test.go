package config

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
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
		cfg         *Config
		name        string
		errContains string
		wantErr     bool
	}{
		// arr configuration
		{name: "no arr configured", cfg: &Config{
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: ""},
		{name: "sonarr missing api_key", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: "sonarr"},
		{name: "radarr missing api_key", cfg: &Config{
			Radarr: yamlArrConfig{URL: "http://radarr:7878"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: "radarr"},
		{name: "both arr missing api_key", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989"},
			Radarr: yamlArrConfig{URL: "http://radarr:7878"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: "sonarr"},
		{name: "sonarr only passes", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, wantErr: false, errContains: ""},
		{name: "radarr only passes", cfg: &Config{
			Radarr: yamlArrConfig{URL: "http://radarr:7878", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, wantErr: false, errContains: ""},
		{name: "both arr passes", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Radarr: yamlArrConfig{URL: "http://radarr:7878", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, wantErr: false, errContains: ""},

		// language rules
		{name: "no default fails", cfg: &Config{
			Sonarr:          yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: ""},
		{name: "rules without default fails", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, wantErr: true, errContains: ""},
		{name: "empty audio in rule", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: ""},
		{name: "empty subtitle code in rule", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: ""}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: ""},
		{name: "empty subtitle code in default", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Default: []yamlSubtitleTarget{{Code: ""}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: ""},
		{name: "default rules only passes", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
			SearchCfg: yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}},
		}, wantErr: false, errContains: ""},
		{name: "duplicate audio rule", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{
					{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}},
					{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "de"}}},
				},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: true}},
		}, wantErr: true, errContains: "duplicate"},

		// providers
		{name: "no enabled providers", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{"os": {Enabled: false}},
		}, wantErr: true, errContains: ""},
		{name: "empty providers map", cfg: &Config{
			Sonarr: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
			Languages: LanguageRules{
				Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}}, Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			PollIntervalCfg: Duration{D: 30 * time.Second}, Providers: map[api.ProviderID]yamlProviderCfg{},
		}, wantErr: true, errContains: ""},
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
		mutate      func(*Config)
		name        string
		errContains string
		wantErr     bool
	}{
		// provider_timeout boundaries
		{name: "provider_timeout below minimum", mutate: func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: 30 * time.Minute}
		}, wantErr: true, errContains: ""},
		{name: "provider_timeout one below minimum", mutate: func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: time.Hour - time.Nanosecond}
		}, wantErr: true, errContains: ""},
		{name: "provider_timeout zero disables", mutate: func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: 0}
		}, wantErr: false, errContains: ""},
		{name: "provider_timeout at minimum", mutate: func(c *Config) {
			c.SearchCfg.ProviderTimeout = Duration{D: time.Hour}
		}, wantErr: false, errContains: ""},

		// scan_delay boundaries
		{name: "scan_delay below minimum", mutate: func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: time.Second}
		}, wantErr: true, errContains: ""},
		{name: "scan_delay one below minimum", mutate: func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: 5*time.Second - time.Nanosecond}
		}, wantErr: true, errContains: ""},
		{name: "scan_delay exact minimum", mutate: func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: 5 * time.Second}
		}, wantErr: false, errContains: ""},
		{name: "scan_delay zero", mutate: func(c *Config) {
			c.SearchCfg.ScanDelay = Duration{D: 0}
		}, wantErr: true, errContains: ""},

		// scan_interval boundaries
		{name: "scan_interval below minimum", mutate: func(c *Config) {
			c.SearchCfg.ScanInterval = Duration{D: 30 * time.Minute}
		}, wantErr: true, errContains: "scan_interval"},
		{name: "scan_interval one below minimum", mutate: func(c *Config) {
			c.SearchCfg.ScanInterval = Duration{D: time.Hour - time.Nanosecond}
		}, wantErr: true, errContains: "scan_interval"},
		{name: "scan_interval at minimum", mutate: func(c *Config) {
			c.SearchCfg.ScanInterval = Duration{D: time.Hour}
		}, wantErr: false, errContains: ""},

		// upgrade_window_days boundaries
		{name: "upgrade zero window_days", mutate: func(c *Config) {
			c.SearchCfg.UpgradeEnabled = true
			c.SearchCfg.UpgradeWindowDays = 0
		}, wantErr: true, errContains: ""},
		{name: "upgrade negative window_days", mutate: func(c *Config) {
			c.SearchCfg.UpgradeEnabled = true
			c.SearchCfg.UpgradeWindowDays = -1
		}, wantErr: true, errContains: ""},
		{name: "upgrade window_days one", mutate: func(c *Config) {
			c.SearchCfg.UpgradeEnabled = true
			c.SearchCfg.UpgradeWindowDays = 1
		}, wantErr: false, errContains: ""},

		// adaptive_backoff boundaries
		{name: "adaptive backoff below one", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 0.5,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
			}
		}, wantErr: true, errContains: ""},
		{name: "adaptive backoff exactly one", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 1.0,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
			}
		}, wantErr: false, errContains: ""},

		// adaptive_initial_delay boundaries
		{name: "adaptive initial_delay zero", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 0}, MaxDelay: Duration{D: 48 * time.Hour},
			}
		}, wantErr: true, errContains: ""},

		// adaptive_max_delay boundaries
		{name: "adaptive max_delay less than initial", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 48 * time.Hour}, MaxDelay: Duration{D: 24 * time.Hour},
			}
		}, wantErr: true, errContains: ""},
		{name: "adaptive max_delay equals initial", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 24 * time.Hour},
			}
		}, wantErr: false, errContains: ""},

		// adaptive disabled skips validation
		{name: "adaptive disabled skips checks", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: false, BackoffMultiplier: 0,
				InitialDelay: Duration{D: 0}, MaxDelay: Duration{D: 0},
			}
		}, wantErr: false, errContains: ""},

		// poll_interval boundaries
		{name: "poll_interval too short", mutate: func(c *Config) {
			c.PollIntervalCfg = Duration{D: 5 * time.Second}
		}, wantErr: true, errContains: "poll_interval"},
		{name: "poll_interval exact minimum", mutate: func(c *Config) {
			c.PollIntervalCfg = Duration{D: 10 * time.Second}
		}, wantErr: false, errContains: ""},
		{name: "poll_interval one below minimum", mutate: func(c *Config) {
			c.PollIntervalCfg = Duration{D: 10*time.Second - time.Nanosecond}
		}, wantErr: true, errContains: "poll_interval"},

		// adaptive_max_attempts boundaries
		{name: "adaptive negative max_attempts", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
				MaxAttempts: -1,
			}
		}, wantErr: true, errContains: "max_attempts"},
		{name: "adaptive zero max_attempts valid", mutate: func(c *Config) {
			c.AdaptiveCfg = yamlAdaptiveConfig{
				Enabled: true, BackoffMultiplier: 2,
				InitialDelay: Duration{D: 24 * time.Hour}, MaxDelay: Duration{D: 48 * time.Hour},
				MaxAttempts: 0,
			}
		}, wantErr: false, errContains: ""},

		// min_score boundaries
		{name: "min_score negative", mutate: func(c *Config) {
			c.SearchCfg.MinScore = -1
		}, wantErr: true, errContains: "min_score"},
		{name: "min_score over 100", mutate: func(c *Config) {
			c.SearchCfg.MinScore = 101
		}, wantErr: true, errContains: "min_score"},

		// audio_sync_fallback boundaries
		{name: "audio_sync_fallback without sync_subtitles", mutate: func(c *Config) {
			c.PostProcessing = yamlPostProcessConfig{SyncSubtitles: false, AudioSyncFallback: true}
		}, wantErr: true, errContains: "audio_sync_fallback"},
		{name: "audio_sync_fallback with sync_subtitles", mutate: func(c *Config) {
			c.PostProcessing = yamlPostProcessConfig{SyncSubtitles: true, AudioSyncFallback: true}
		}, wantErr: false, errContains: ""},

		// logging boundaries
		{name: "invalid logging level", mutate: func(c *Config) {
			c.Logging = LoggingConfig{Level: "banana", Format: "json"}
		}, wantErr: true, errContains: "logging.level"},
		{name: "invalid logging format", mutate: func(c *Config) {
			c.Logging = LoggingConfig{Level: "info", Format: "xml"}
		}, wantErr: true, errContains: "logging.format"},

		// per-target min_score boundaries
		{name: "per-target min_score negative", mutate: func(c *Config) {
			c.Languages.Rules = []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr", MinScore: new(-1)}}}}
		}, wantErr: true, errContains: "min_score"},
		{name: "per-target min_score over 100", mutate: func(c *Config) {
			c.Languages.Rules = []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr", MinScore: new(101)}}}}
		}, wantErr: true, errContains: "min_score"},
		{name: "default target min_score over 100", mutate: func(c *Config) {
			c.Languages.Default = []yamlSubtitleTarget{{Code: "en", MinScore: new(200)}}
		}, wantErr: true, errContains: "min_score"},
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
