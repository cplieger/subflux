package classify

import (
	"testing"
	"unicode"
)

func FuzzAlpha2FromAlpha3(f *testing.F) {
	f.Add("eng")
	f.Add("fre")
	f.Add("spa")
	f.Add("pt")
	f.Add("")
	f.Add("xx")
	f.Add("abcdefghijklmnopqrstuvwxyz")

	f.Fuzz(func(t *testing.T, input string) {
		result := Alpha2FromAlpha3(input)
		if result == "" {
			return
		}
		if len(result) != 2 {
			t.Fatalf("non-empty result must be 2 chars, got %d: %q", len(result), result)
		}
		for _, r := range result {
			if !unicode.IsLower(r) && !unicode.IsLetter(r) {
				t.Fatalf("result char %q is not a lowercase letter", r)
			}
		}
	})
}

func FuzzSanitizeImdbID(f *testing.F) {
	f.Add("tt1234567")
	f.Add("1234567")
	f.Add("tt0000001")
	f.Add("")
	f.Add("ttabc")
	f.Add("tt00000000000000000000000000000000")

	f.Fuzz(func(t *testing.T, input string) {
		// Must not panic on any input.
		_ = SanitizeImdbID(input)
	})
}
