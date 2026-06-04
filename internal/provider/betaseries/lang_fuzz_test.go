package betaseries

import (
	"testing"
)

// FuzzBetaLangToISO exercises language code conversion with arbitrary input
// to ensure it never panics and always returns a known-valid code or empty.
func FuzzBetaLangToISO(f *testing.F) {
	f.Add("vo")
	f.Add("vf")
	f.Add("en")
	f.Add("fr")
	f.Add("")
	f.Add("VF")
	f.Add("unknown")
	f.Add("VO\x00")

	f.Fuzz(func(t *testing.T, code string) {
		result := betaLangToISO(code)

		// Invariant 1: never panics (implicit).

		// Invariant 2: result is either empty, "en", or "fr".
		if result != "" && result != "en" && result != "fr" {
			t.Fatalf("betaLangToISO(%q) = %q, want \"\"|\"en\"|\"fr\"", code, result)
		}
	})
}
