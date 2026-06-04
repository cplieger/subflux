package cliparse

import (
	"bytes"
	"strings"
	"testing"
)

func FuzzParseArgs(f *testing.F) {
	f.Add("--lang en --imdb tt1234567")
	f.Add("--download")
	f.Add("")
	f.Add("--flag value --another thing")
	f.Fuzz(func(t *testing.T, input string) {
		args := splitArgs(input)
		// Must not panic.
		_, _ = ParseArgs(args)
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("--lang en --imdb tt1234567")
	f.Add("--unknown-flag value")
	f.Add("--hlp")
	f.Add("")
	f.Fuzz(func(t *testing.T, input string) {
		args := splitArgs(input)
		params, _ := ParseArgs(args)
		spec := &Spec{
			Name: "test",
			Flags: []Flag{
				{Name: "lang", Type: "string"},
				{Name: "imdb", Type: "string"},
				{Name: "count", Type: "int"},
				{Name: "timeout", Type: "duration"},
				{Name: "download", Type: "bool"},
			},
		}
		// Must not panic.
		_ = Validate(args, params, spec)
	})
}

func FuzzEditDistance(f *testing.F) {
	f.Add("hello", "helo")
	f.Add("", "abc")
	f.Add("abc", "")
	f.Add("abc", "abc")
	f.Fuzz(func(t *testing.T, a, b string) {
		d := editDistance(a, b)
		if d < 0 {
			t.Fatalf("negative edit distance: %d", d)
		}
	})
}

func FuzzPrintHelp(f *testing.F) {
	f.Add("search", "Search for subtitles", "lang", "string")
	f.Fuzz(func(t *testing.T, name, synopsis, flagName, flagType string) {
		spec := &Spec{
			Name:     name,
			Synopsis: synopsis,
			Flags:    []Flag{{Name: flagName, Type: flagType}},
		}
		var buf bytes.Buffer
		// Must not panic.
		PrintHelp(&buf, spec)
	})
}

func FuzzSuggestName(f *testing.F) {
	f.Add("serch", "search,sync,scan,backup,health")
	f.Fuzz(func(t *testing.T, input, candidatesStr string) {
		candidates := splitArgs(candidatesStr)
		// Must not panic.
		_, _ = SuggestName(input, candidates)
	})
}

// splitArgs splits a string into args on spaces, filtering empty strings.
func splitArgs(s string) []string {
	var args []string
	for part := range strings.FieldsSeq(s) {
		args = append(args, part)
	}
	return args
}
