package api

import "testing"

func FuzzParseAudioLangs(f *testing.F) {
	f.Add("en")
	f.Add("en,fr")
	f.Add("en/fr/de")
	f.Add("English,French")
	f.Add("")
	f.Add("en,en,fr")
	f.Add("日本語")
	f.Add(string(make([]byte, 1000)))

	f.Fuzz(func(t *testing.T, raw string) {
		result := ParseAudioLangs(raw)
		// All returned codes must be 2-letter lowercase.
		for _, code := range result {
			if len(code) != 2 {
				t.Errorf("ParseAudioLangs(%q) returned code %q with len != 2", raw, code)
			}
			for _, r := range code {
				if r < 'a' || r > 'z' {
					t.Errorf("ParseAudioLangs(%q) returned code %q with non-lowercase char", raw, code)
				}
			}
		}
		// No duplicates.
		seen := make(map[string]bool, len(result))
		for _, code := range result {
			if seen[code] {
				t.Errorf("ParseAudioLangs(%q) returned duplicate code %q", raw, code)
			}
			seen[code] = true
		}
	})
}

// FuzzLangNameToISO verifies LangNameToISO never panics and that any non-empty
// result is a 2-letter lowercase ASCII code.
func FuzzLangNameToISO(f *testing.F) {
	f.Add("english")
	f.Add("en")
	f.Add("")
	f.Add("ZZ")
	f.Add("日本語")
	f.Fuzz(func(t *testing.T, name string) {
		result := LangNameToISO(name)
		if result == "" {
			return
		}
		if len(result) != 2 {
			t.Errorf("LangNameToISO(%q) = %q, want a 2-byte code", name, result)
		}
		if result[0] < 'a' || result[0] > 'z' || result[1] < 'a' || result[1] > 'z' {
			t.Errorf("LangNameToISO(%q) = %q, want lowercase ASCII", name, result)
		}
	})
}
