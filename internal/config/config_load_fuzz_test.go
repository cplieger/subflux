package config

import (
	"context"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzLoadFromBytes feeds arbitrary (untrusted) config text through the full
// load pipeline. Beyond "never panics", a config that loads successfully must
// be self-consistent: the embedded provider is always force-enabled and the
// returned config re-validates cleanly.
func FuzzLoadFromBytes(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("sonarr:\n  url: http://localhost:8989\n  api_key: abc123\n"))
	f.Add([]byte("poll_interval: 5m\n"))
	f.Add([]byte(minimalValidYAML())) // a config that actually loads

	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := LoadFromBytes(context.Background(), data)
		if err != nil {
			return // rejected input: nothing further to assert
		}
		if cfg == nil {
			t.Fatal("LoadFromBytes returned nil config with nil error")
		}
		defer func() { _ = cfg.Close() }()

		// forceEmbeddedProvider runs on every successful load, so the embedded
		// provider can never end up disabled.
		if p, ok := cfg.Providers[api.ProviderNameEmbedded]; !ok || !p.Enabled {
			t.Errorf("embedded provider present=%v enabled=%v, want always enabled", ok, p.Enabled)
		}

		// A successful load means validation passed; re-validating must agree.
		if verr := cfg.Validate(); verr != nil {
			t.Errorf("LoadFromBytes succeeded but Validate() = %v, want nil", verr)
		}
	})
}

// FuzzIsAllowedEnvVar exercises the environment-variable allowlist with
// arbitrary keys to ensure the security boundary cannot be bypassed via
// unicode tricks, null bytes, or prefix manipulation that would leak
// sensitive host environment variables.
func FuzzIsAllowedEnvVar(f *testing.F) {
	f.Add("SUBFLUX_API_KEY")
	f.Add("CONFIG_ROOT")
	f.Add("HOME")
	f.Add("PATH")
	f.Add("")
	f.Add("SUBFLUX_")
	f.Add("subflux_lower")

	f.Fuzz(func(t *testing.T, key string) {
		result := isAllowedEnvVar(key)
		// If allowed without the SUBFLUX_ prefix, the key must be one of the
		// explicitly whitelisted deployment variables.
		if result && !strings.HasPrefix(key, "SUBFLUX_") {
			switch key {
			case "CONFIG_ROOT", "MEDIA_FOLDER", "PUID", "PGID", "TZ", "LAN_IP", "HOSTNAME":
				// expected
			default:
				t.Fatalf("unexpected allowed key: %q", key)
			}
		}
	})
}
