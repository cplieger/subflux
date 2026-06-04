package classify

import "testing"

func FuzzLookupLangName(f *testing.F) {
	f.Add("eng")
	f.Add("en")
	f.Add("")
	f.Add("xx")
	f.Add("pob")
	f.Add("abcdefghij")
	f.Add("\x00\x01\x02")

	f.Fuzz(func(t *testing.T, code string) {
		// Test with nil overrides.
		result := LookupLangName(code, nil)
		if result != "" {
			if _, ok := LangRegistry[Alpha2FromAlpha3(code)]; !ok {
				t.Fatalf("LookupLangName returned %q for unknown code %q", result, code)
			}
		}

		// Test with overrides map.
		overrides := map[string]string{"pb": "Brazillian Portuguese"}
		_ = LookupLangName(code, overrides)
	})
}
