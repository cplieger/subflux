package cliparse

import (
	"strings"
	"testing"
)

func FuzzHelpRequested(f *testing.F) {
	f.Add("--help --lang en")
	f.Add("-h")
	f.Add("--lang en --download")
	f.Add("")
	f.Add("--hel --help")

	f.Fuzz(func(t *testing.T, raw string) {
		args := strings.Fields(raw)
		result := HelpRequested(args)

		// Property: result must be true iff --help or -h is present
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

func FuzzSortByName_Sorted(f *testing.F) {
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

		// Invariant: result must be sorted by Name
		for i := 1; i < len(sorted); i++ {
			if sorted[i].Name < sorted[i-1].Name {
				t.Errorf("not sorted: [%d]=%q < [%d]=%q", i, sorted[i].Name, i-1, sorted[i-1].Name)
			}
		}

		// Invariant: same length
		if len(sorted) != len(specs) {
			t.Errorf("length mismatch: got %d, want %d", len(sorted), len(specs))
		}
	})
}
