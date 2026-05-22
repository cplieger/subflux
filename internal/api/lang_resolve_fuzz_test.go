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
