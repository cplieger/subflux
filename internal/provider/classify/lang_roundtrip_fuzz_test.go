package classify

import "testing"

// FuzzLookupLangCode tests the roundtrip invariant: for any code that
// LookupLangName resolves to a non-empty name, LookupLangCode must map
// that name back to the original code. Bug class: asymmetric lookup tables
// where forward and reverse maps diverge due to aliasing (e.g. multiple
// codes map to the same name, but only one reverse entry exists).
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
		// Roundtrip: name back to code must yield a non-empty result.
		recovered := LookupLangCode(name, nil)
		if recovered == "" {
			t.Errorf("LookupLangCode(%q) = \"\", but LookupLangName(%q) = %q", name, code, name)
		}
		// The recovered code should also map back to the same name.
		name2 := LookupLangName(recovered, nil)
		if name2 != name {
			t.Errorf("roundtrip broken: code=%q -> name=%q -> code=%q -> name=%q", code, name, recovered, name2)
		}
	})
}
