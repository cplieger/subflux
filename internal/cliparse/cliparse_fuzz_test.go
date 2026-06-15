package cliparse

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func FuzzParseArgs(f *testing.F) {
	f.Add("--host localhost --port 8080")
	f.Add("--download --lang en")
	f.Add("--lang en --season 1")
	f.Add("--lang en --imdb tt1234567")
	f.Add("--download")
	f.Add("")
	f.Add("--key=value")
	f.Add("--flag")
	f.Add("-- --value")
	f.Add("--flag value --another thing")

	f.Fuzz(func(t *testing.T, raw string) {
		var args []string
		if raw != "" {
			cur := ""
			for _, c := range raw {
				if c == ' ' {
					if cur != "" {
						args = append(args, cur)
						cur = ""
					}
				} else {
					cur += string(c)
				}
			}
			if cur != "" {
				args = append(args, cur)
			}
		}
		params, dl := ParseArgs(args)
		if params == nil {
			t.Fatal("params should never be nil")
		}
		for k := range params {
			if k == "" {
				t.Error("empty key in params")
			}
		}
		// download flag consistency. The parser pairs `--key value` greedily,
		// so a "--download" token immediately after another flag is consumed
		// as that flag's value (a deliberate contract — see clisearch's
		// TestParseArgs_preserves_all_key_value_pairs). The honest invariant
		// is therefore one-directional: the parser must never report
		// download=true unless the token is actually present.
		if dl && !slices.Contains(args, "--download") {
			t.Errorf("download set but --download absent from args: %q", args)
		}
	})
}

func FuzzEditDistance(f *testing.F) {
	f.Add("abc", "abc")
	f.Add("", "hello")
	f.Add("kitten", "sitting")
	f.Add("a", "b")
	f.Add("host", "hst")
	f.Add("", "abc")
	f.Add("abc", "")
	f.Add("same", "same")
	f.Add("hello", "helo")

	f.Fuzz(func(t *testing.T, a, b string) {
		d := editDistance(a, b)
		if d < 0 {
			t.Errorf("editDistance(%q,%q) = %d < 0", a, b, d)
		}
		if editDistance(a, a) != 0 {
			t.Errorf("editDistance(%q,%q) != 0", a, a)
		}
		if a == "" && d != len(b) {
			t.Errorf("editDistance(\"\",%q) = %d, want %d", b, d, len(b))
		}
		// symmetry
		dba := editDistance(b, a)
		if d != dba {
			t.Errorf("editDistance(%q,%q)=%d != editDistance(%q,%q)=%d", a, b, d, b, a, dba)
		}
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("--host localhost --port 8080")
	f.Add("--lang en --season 1")
	f.Add("--lang en --imdb tt1234567")
	f.Add("--unknown foo")
	f.Add("--unknown-flag value")
	f.Add("")
	f.Add("--help")
	f.Add("--hlp")
	spec := &Spec{
		Name: "test",
		Flags: []Flag{
			{Name: "host", Type: "string"},
			{Name: "lang", Type: "string"},
			{Name: "imdb", Type: "string"},
			{Name: "port", Type: "int"},
			{Name: "season", Type: "int"},
			{Name: "count", Type: "int"},
			{Name: "timeout", Type: "duration"},
			{Name: "verbose", Type: "bool"},
			{Name: "download", Type: "bool"},
		},
	}
	f.Fuzz(func(t *testing.T, raw string) {
		args := strings.Fields(raw)
		params, _ := ParseArgs(args)
		// Should never panic
		_ = Validate(args, params, spec)
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
		candidates := strings.Fields(candidatesStr)
		// Must not panic.
		_, _ = SuggestName(input, candidates)
	})
}
