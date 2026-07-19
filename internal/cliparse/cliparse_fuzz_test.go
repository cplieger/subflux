package cliparse

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// fuzzSpec declares every flag the seed corpus uses so fuzzed inputs reach
// the interesting parse paths (typed values, bools, `=` forms) instead of
// all dying on the unknown-flag check.
func fuzzSpec() *Spec {
	return &Spec{
		Name: "test",
		Flags: []Flag{
			{Name: "host"},
			{Name: "lang"},
			{Name: "imdb"},
			{Name: "key"},
			{Name: "flag"},
			{Name: "another"},
			{Name: "value"},
			{Name: "port", Type: "int"},
			{Name: "season", Type: "int"},
			{Name: "download", Type: "bool"},
		},
	}
}

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
		params, err := ParseAndValidate(args, fuzzSpec())
		if err != nil {
			return
		}
		// Bool consistency: the parser must never report download=true
		// unless a --download token (bare or =value form) is present.
		hasDownloadToken := false
		for _, a := range args {
			if a == "--download" || strings.HasPrefix(a, "--download=") {
				hasDownloadToken = true
				break
			}
		}
		if params.Bool("download") && !hasDownloadToken {
			t.Errorf("download set but --download absent from args: %q", args)
		}
		// Typed access must not panic and must round-trip validated ints.
		if v := params.String("port"); v != "" {
			if _, atoiErr := strconv.Atoi(v); atoiErr != nil {
				t.Errorf("validated --port value %q does not re-parse: %v", v, atoiErr)
			}
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
	f.Add("--timeout 30s --verbose --count 3")
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
		// Must never panic; errors are expected for arbitrary input.
		params, err := ParseAndValidate(args, spec)
		if err != nil {
			return
		}
		// Success implies every typed accessor is safe to call.
		_ = params.Int("port")
		_ = params.Int("season")
		_ = params.Bool("verbose")
		_ = params.String("host")
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

func FuzzHelpRequested(f *testing.F) {
	f.Add("--help --lang en")
	f.Add("-h")
	f.Add("--lang en --download")
	f.Add("")
	f.Add("--hel --help")

	f.Fuzz(func(t *testing.T, raw string) {
		args := strings.Fields(raw)
		result := HelpRequested(args)

		// HelpRequested must be true iff --help or -h is present.
		hasHelp := false
		for _, a := range args {
			if a == "--help" || a == "-h" {
				hasHelp = true
				break
			}
		}
		if result != hasHelp {
			t.Errorf("HelpRequested(%v) = %v, want %v", args, result, hasHelp)
		}
	})
}

func FuzzSortByName(f *testing.F) {
	f.Add("search,scan,backup,health,sync")
	f.Add("")
	f.Add("a")
	f.Add("z,a,m")

	f.Fuzz(func(t *testing.T, namesStr string) {
		names := strings.Split(namesStr, ",")
		specs := make([]Spec, 0, len(names))
		for _, n := range names {
			if n != "" {
				specs = append(specs, Spec{Name: n})
			}
		}

		sorted := SortByName(specs)

		// Output is ordered ascending by Name...
		for i := 1; i < len(sorted); i++ {
			if sorted[i].Name < sorted[i-1].Name {
				t.Errorf("not sorted: [%d]=%q < [%d]=%q", i, sorted[i].Name, i-1, sorted[i-1].Name)
			}
		}
		// ...and preserves the element count.
		if len(sorted) != len(specs) {
			t.Errorf("length mismatch: got %d, want %d", len(sorted), len(specs))
		}
	})
}
