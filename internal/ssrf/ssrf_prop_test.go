package ssrf

import (
	"testing"

	"pgregory.net/rapid"
)

func TestValidateURL_properties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		scheme := rapid.SampledFrom([]string{schemeHTTPS, "http", "ftp", ""}).Draw(t, "scheme")
		host := rapid.StringMatching(`[a-z0-9]{1,20}\.(com|org|net|io)`).Draw(t, "host")
		path := rapid.StringMatching(`/[a-z0-9/]{0,20}`).Draw(t, "path")

		url := scheme + "://" + host + path
		err := ValidateURL(url)

		// Non-https schemes must always be rejected.
		if scheme != schemeHTTPS && err == nil {
			t.Fatalf("ValidateURL(%q) = nil, want error for non-https", url)
		}
	})
}
