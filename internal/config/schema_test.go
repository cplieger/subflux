package config

import (
	"testing"
	"time"
)

func TestSchemaDefaults_MatchExpected(t *testing.T) {
	defaults := newWithDefaults()

	tests := []struct {
		got      any
		expected any
		name     string
	}{
		{name: "poll interval", got: defaults.PollIntervalCfg.D, expected: 30 * time.Second},
		{name: "scan interval", got: defaults.SearchCfg.ScanInterval.D, expected: 24 * time.Hour},
		{name: "scan delay", got: defaults.SearchCfg.ScanDelay.D, expected: 5 * time.Second},
		{name: "provider timeout", got: defaults.SearchCfg.ProviderTimeout.D, expected: time.Hour},
		{name: "adaptive enabled", got: defaults.AdaptiveCfg.Enabled, expected: true},
		{name: "adaptive multiplier", got: defaults.AdaptiveCfg.BackoffMultiplier, expected: float64(2)},
		{name: "upgrade enabled", got: defaults.SearchCfg.UpgradeEnabled, expected: true},
		{name: "upgrade window days", got: defaults.SearchCfg.UpgradeWindowDays, expected: 7},
		{name: "max sse clients", got: defaults.SearchCfg.MaxSSEClients, expected: 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %v, want %v", tt.got, tt.expected)
			}
		})
	}
}
