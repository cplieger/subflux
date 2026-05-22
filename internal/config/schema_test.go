package config

import (
	"testing"
	"time"
)

func TestSchemaDefaults_MatchExpected(t *testing.T) {
	defaults := newWithDefaults()

	tests := []struct {
		name     string
		got      any
		expected any
	}{
		{"poll interval", defaults.PollIntervalCfg.D, 30 * time.Second},
		{"scan interval", defaults.SearchCfg.ScanInterval.D, 24 * time.Hour},
		{"scan delay", defaults.SearchCfg.ScanDelay.D, 5 * time.Second},
		{"provider timeout", defaults.SearchCfg.ProviderTimeout.D, time.Hour},
		{"adaptive enabled", defaults.AdaptiveCfg.Enabled, true},
		{"adaptive multiplier", defaults.AdaptiveCfg.BackoffMultiplier, float64(2)},
		{"upgrade enabled", defaults.SearchCfg.UpgradeEnabled, true},
		{"upgrade window days", defaults.SearchCfg.UpgradeWindowDays, 7},
		{"max sse clients", defaults.SearchCfg.MaxSSEClients, 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %v, want %v", tt.got, tt.expected)
			}
		})
	}
}
