package language

import "testing"

func FuzzRefineFromTitle_keepsLanguage(f *testing.F) {
	for _, r := range refinements {
		for _, lang := range r.languages {
			f.Add(lang, r.token)
			f.Add(lang, "x"+r.token+" "+r.token)
		}
	}
	for _, s := range []string{"srp", "chi", "yue", "por", "spa", "fre", "zh-Hans", "pt-BR", "", "en"} {
		f.Add(s, "簡繁中文 Brazil Latin")
	}
	f.Fuzz(func(t *testing.T, raw, title string) {
		tag, ok := Parse(raw)
		if !ok {
			return
		}
		got := RefineFromTitle(tag, title)
		if got.t.Language() != tag.t.Language() {
			t.Fatalf("RefineFromTitle(%q, %q) = %q changed the language", tag, title, got)
		}
		if again, ok := Parse(got.String()); !ok || again != got {
			t.Fatalf("RefineFromTitle(%q, %q) = %q; Parse of its text = %q, %v", tag, title, got, again, ok)
		}
	})
}
