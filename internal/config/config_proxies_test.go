package config

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func TestParseTrustedProxies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		in          []string
		wantErr     bool
		wantLen     int
		contains    string // an IP the parsed set must contain
		notContains string // an IP the parsed set must NOT contain
	}{
		{name: "empty is nil", in: nil, wantLen: 0},
		{name: "blank entries skipped", in: []string{"", "  "}, wantLen: 0},
		{
			name: "single /32", in: []string{"192.168.1.5/32"},
			wantLen: 1, contains: "192.168.1.5", notContains: "192.168.1.6",
		},
		{
			name: "range /8", in: []string{"10.0.0.0/8"},
			wantLen: 1, contains: "10.4.3.2", notContains: "11.0.0.1",
		},
		{
			name: "multiple", in: []string{"10.0.0.0/8", "192.168.0.0/16"},
			wantLen: 2, contains: "192.168.42.1", notContains: "172.16.0.1",
		},
		{name: "ipv6 range", in: []string{"2001:db8::/32"}, wantLen: 1, contains: "2001:db8::1"},
		{name: "invalid: bare IP without mask", in: []string{"10.0.0.5"}, wantErr: true},
		{name: "invalid: garbage", in: []string{"not-a-cidr"}, wantErr: true},
		{name: "invalid: one bad among good", in: []string{"10.0.0.0/8", "bad"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nets, err := parseTrustedProxies(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseTrustedProxies(%v) = nil error, want error", tt.in)
				}
				// Invalid CIDRs must surface as a field-tagged validation error.
				var ve *ValidationError
				if !errors.As(err, &ve) || ve.Field != "trusted_proxies" {
					t.Errorf("error = %v, want *ValidationError with Field=trusted_proxies", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTrustedProxies(%v) unexpected error: %v", tt.in, err)
			}
			if len(nets) != tt.wantLen {
				t.Fatalf("parseTrustedProxies(%v) len = %d, want %d", tt.in, len(nets), tt.wantLen)
			}
			if tt.contains != "" && !containsIP(nets, tt.contains) {
				t.Errorf("parsed set %v does not contain %s", tt.in, tt.contains)
			}
			if tt.notContains != "" && containsIP(nets, tt.notContains) {
				t.Errorf("parsed set %v unexpectedly contains %s", tt.in, tt.notContains)
			}
		})
	}
}

func containsIP(nets []*net.IPNet, ip string) bool {
	parsed := net.ParseIP(ip)
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// TestConfig_trusted_proxies_load wires the field through validate + buildCaches
// (the load path) and confirms the parsed set is cached, that an invalid entry
// is rejected at validation, and that the unset default is an empty set.
func TestConfig_trusted_proxies_load(t *testing.T) {
	t.Parallel()

	base := func() *Config {
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

	t.Run("valid caches parsed set", func(t *testing.T) {
		t.Parallel()
		cfg := base()
		cfg.TrustedProxies = []string{"10.0.0.0/8", "192.168.1.5/32"}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
		cfg.buildCaches(context.Background())
		got := cfg.TrustedProxyNets()
		if len(got) != 2 {
			t.Fatalf("TrustedProxyNets() len = %d, want 2", len(got))
		}
		if !containsIP(got, "10.9.8.7") {
			t.Error("parsed set should contain 10.9.8.7 (inside 10.0.0.0/8)")
		}
	})

	t.Run("invalid rejected at validate", func(t *testing.T) {
		t.Parallel()
		cfg := base()
		cfg.TrustedProxies = []string{"10.0.0.0/8", "nonsense"}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate() = nil, want error for invalid trusted_proxies")
		}
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "trusted_proxies" {
			t.Errorf("Validate() error = %v, want *ValidationError Field=trusted_proxies", err)
		}
	})

	t.Run("unset is empty", func(t *testing.T) {
		t.Parallel()
		cfg := base()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
		cfg.buildCaches(context.Background())
		if got := cfg.TrustedProxyNets(); len(got) != 0 {
			t.Errorf("TrustedProxyNets() = %v, want empty (trust-nothing default)", got)
		}
	})
}
