package opensubtitles

import (
	"strings"
	"testing"
)

func FuzzIsValidServerHost(f *testing.F) {
	f.Add("opensubtitles.com")
	f.Add("api.opensubtitles.com")
	f.Add("vip-api.opensubtitles.com")
	f.Add("")
	f.Add("evil.com")
	f.Add("opensubtitles.com.evil.com")
	f.Add("opensubtitles.com/path")
	f.Add("user:pass@opensubtitles.com")
	f.Add("opensubtitles.com:8080")
	f.Add("127.0.0.1")
	f.Add("10.0.0.1")
	f.Add("opensubtitles.com.")

	f.Fuzz(func(t *testing.T, host string) {
		result := isValidServerHost(host)
		if result {
			// Invariant: accepted hosts must end with opensubtitles.com suffix.
			h := strings.ToLower(strings.TrimSuffix(host, "."))
			if h != "opensubtitles.com" && !strings.HasSuffix(h, ".opensubtitles.com") {
				t.Fatalf("isValidServerHost accepted %q but it doesn't end with opensubtitles.com", host)
			}
		}
	})
}
