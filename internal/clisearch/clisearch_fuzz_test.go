package clisearch

import (
	"slices"
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

		// params is always allocated, never nil (dereferenced below).
		if params == nil {
			t.Fatalf("parseArgs(%q) returned a nil params map", args)
		}

		// Every extracted pair is traceable to the input: a non-empty key
		// whose "--" form is one of the args, paired with a value that is also
		// one of the args. This pins the parser's contract without
		// reimplementing it (which would be tautological).
		for k, v := range params {
			if k == "" {
				t.Errorf("parseArgs(%q) produced an empty key", args)
			}
			if !slices.Contains(args, "--"+k) {
				t.Errorf("parseArgs(%q) key %q has no matching %q arg", args, k, "--"+k)
			}
			if !slices.Contains(args, v) {
				t.Errorf("parseArgs(%q) value %q for key %q is not among the args", args, v, k)
			}
		}

		// The --download flag is never captured as a key (it is consumed by
		// the flag branch before the key/value path runs).
		if _, ok := params[strings.TrimPrefix(flagDownload, "--")]; ok {
			t.Errorf("parseArgs(%q) captured %q as a key", args, flagDownload)
		}

		// download being set implies the flag token actually appeared.
		if download && !slices.Contains(args, flagDownload) {
			t.Errorf("parseArgs(%q) download = true but %q is not among the args", args, flagDownload)
		}
	})
}
