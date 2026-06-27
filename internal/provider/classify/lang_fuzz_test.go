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
		got := SanitizeImdbID(input)
		// The outermost operation is strings.TrimLeft(_, "0"), so a non-empty
		// result can never retain a leading zero, for any input.
		if got != "" && got[0] == '0' {
			t.Fatalf("SanitizeImdbID(%q) = %q, must not start with a leading zero", input, got)
		}
	})
}

func FuzzLookupLangName(f *testing.F) {
	f.Add("eng")
	f.Add("en")
	f.Add("")
	f.Add("xx")
	f.Add("pob")
	f.Add("abcdefghij")
	f.Add("\x00\x01\x02")

	f.Fuzz(func(t *testing.T, code string) {
		// A non-empty result must correspond to a real registry entry for the
		// canonicalized code.
		result := LookupLangName(code, nil)
		if result != "" {
			if _, ok := LangRegistry[Alpha2FromAlpha3(code)]; !ok {
				t.Fatalf("LookupLangName returned %q for unknown code %q", result, code)
			}
		}
		// The override path must not panic.
		_ = LookupLangName(code, map[string]string{"pb": "Brazillian Portuguese"})
	})
}

// FuzzLookupLangCode pins the round-trip invariant: any code that
// LookupLangName resolves to a name must map back through LookupLangCode to a
// non-empty code that resolves to the same name. Guards against forward and
// reverse language tables drifting apart through aliasing.
func FuzzLookupLangCode(f *testing.F) {
	f.Add("en")
	f.Add("fr")
	f.Add("pb")
	f.Add("eng")
	f.Add("zz")
	f.Add("")

	f.Fuzz(func(t *testing.T, code string) {
		name := LookupLangName(code, nil)
		if name == "" {
			return
		}
		recovered := LookupLangCode(name, nil)
		if recovered == "" {
			t.Errorf("LookupLangCode(%q) = \"\", but LookupLangName(%q) = %q", name, code, name)
		}
		if name2 := LookupLangName(recovered, nil); name2 != name {
			t.Errorf("roundtrip broken: code=%q -> name=%q -> code=%q -> name=%q", code, name, recovered, name2)
		}
	})
}
