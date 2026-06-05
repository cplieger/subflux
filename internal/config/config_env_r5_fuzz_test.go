package config

import (
	"strings"
	"testing"
)

// FuzzIsAllowedEnvVar exercises the environment variable allowlist with
// arbitrary key strings to ensure security boundary correctness.
//
// Bug class: bypass of env var restriction via unicode tricks, null bytes,
// or prefix manipulation that could leak sensitive host environment variables.
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
		// If allowed and not in whitelist, must have SUBFLUX_ prefix.
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
