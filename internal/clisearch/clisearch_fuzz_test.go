package clisearch

import (
	"strings"
	"testing"
)

// FuzzParseArgs exercises parseArgs with arbitrary inputs to catch panics,
// off-by-one errors, and unicode edge cases.
func FuzzParseArgs(f *testing.F) {
	// Seed corpus: known shapes.
	f.Add("")
	f.Add("--download")
	f.Add("--imdb tt1234567")
	f.Add("--title Inception --lang fr --download")
	f.Add("--key")
	f.Add("-- --value")
	f.Add("--a b --c d --e f")
	f.Add("--unicode 日本語")
	f.Add("----double-dash value")
	f.Add("--empty-value ")

	f.Fuzz(func(t *testing.T, input string) {
		args := strings.Fields(input)
		params, download := parseArgs(args)

		// Invariant 1: never panics (implicit by reaching here).

		// Invariant 2: download is a bool (always valid).
		_ = download

		// Invariant 3: params is never nil.
		if params == nil {
			t.Error("params is nil")
		}
	})
}
