package language

import (
	"strings"
	"testing"
)

func refined(t *testing.T, raw, title string) string {
	t.Helper()
	return RefineFromTitle(mustParse(t, raw), title).String()
}

func TestRefineSerbian(t *testing.T) {
	p := Policy{}
	sr := mustParse(t, "srp")
	t.Run("Serbian (Latin) refines to sr-Latn", func(t *testing.T) {
		got := RefineFromTitle(sr, "Serbian (Latin)")
		if got.String() != "sr-Latn" {
			t.Fatalf("RefineFromTitle(srp, Serbian (Latin)) = %q, want sr-Latn", got)
		}
		if g := p.Grade(mustParse(t, "sr-Cyrl"), got); g.Class != ManualOnly {
			t.Errorf("Grade(sr-Cyrl, %s) = %+v, want ManualOnly", got, g)
		}
	})
	for title, want := range map[string]string{
		"Srpski ћирилица":    "sr-Cyrl",
		"Latinica":           "sr-Latn",
		"Ćirilica":           "sr-Cyrl",
		"Latinicaa":          "sr",
		"xĆirilica":          "sr",
		"Latin and Cyrillic": "sr",
	} {
		if got := refined(t, "srp", title); got != want {
			t.Errorf("RefineFromTitle(srp, %q) = %q, want %q", title, got, want)
		}
	}
	t.Run("unrefined srp grades as bare sr", func(t *testing.T) {
		if g := p.Grade(mustParse(t, "sr-Cyrl"), sr); g.Class != Exact {
			t.Errorf("Grade(sr-Cyrl, srp) = %+v, want Exact", g)
		}
		if g := p.Grade(mustParse(t, "sr-Latn"), sr); g.Class != ManualOnly {
			t.Errorf("Grade(sr-Latn, srp) = %+v, want ManualOnly", g)
		}
	})
}

func TestRefineTokenGaps(t *testing.T) {
	for _, c := range []struct{ tag, title, want string }{
		{"chi", "正體中文", "zh-Hant"},
		{"chi", "正体", "zh-Hant"},
		{"fre", "Québec", "fr-CA"},
		{"fre", "Quebec", "fr-CA"},
		{"fre", "Québécois", "fr-CA"},
		{"fre", "VFQ", "fr-CA"},
		{"por", "PT-BR", "pt-BR"},
		{"yue", "粵語简体", "yue-Hans"},
	} {
		if got := refined(t, c.tag, c.title); got != c.want {
			t.Errorf("RefineFromTitle(%s, %q) = %q, want %q", c.tag, c.title, got, c.want)
		}
	}
}

func TestRefineTable(t *testing.T) {
	cases := []struct{ tag, title, want string }{
		{"chi", "Traditional", "zh-Hant"},
		{"chi", "繁體", "zh-Hant"},
		{"chi", "CHT", "zh-Hant"},
		{"chi", "[Big5]", "zh-Hant"},
		{"chi", "Simplified", "zh-Hans"},
		{"chi", "简体", "zh-Hans"},
		{"chi", "簡體", "zh-Hans"},
		{"chi", "CHS", "zh-Hans"},
		{"srp", "латиница", "sr-Latn"},
		{"srp", "Cirilica", "sr-Cyrl"},
		{"srp", "Cyrillic", "sr-Cyrl"},
		{"por", "Brasil", "pt-BR"},
		{"por", "Portugal", "pt-PT"},
		{"por", "European", "pt-PT"},
		{"spa", "Latino", "es-419"},
		{"spa", "Latin America", "es-419"},
		{"spa", "Castellano", "es-ES"},
		{"spa", "Castilian", "es-ES"},
		{"spa", "ESPAÑA", "es-ES"},
		{"spa", "Spain", "es-ES"},
		{"fre", "Canadian", "fr-CA"},
		{"chi", "Yacht", "zh"},
		{"chi", "Big5x", "zh"},
		{"chi", "簡繁中文", "zh"},
		{"por", "Brazil vs Portugal", "pt"},
		{"zh-Hans", "Traditional", "zh-Hans"},
		{"pt-PT", "Brazil", "pt-PT"},
		{"sr-Cyrl", "Latin", "sr-Cyrl"},
		{"eng", "Traditional Chinese", "en"},
		{"spa", "Spanish", "es"},
		{"", "Traditional", ""},
	}
	for _, c := range cases {
		if got := refined(t, c.tag, c.title); got != c.want {
			t.Errorf("RefineFromTitle(%s, %q) = %q, want %q", c.tag, c.title, got, c.want)
		}
	}
	t.Run("Brazilian on por grades Exact for pt-BR", func(t *testing.T) {
		got := RefineFromTitle(mustParse(t, "por"), "Brazilian")
		if g := (Policy{}).Grade(mustParse(t, "pt-BR"), got); g.Class != Exact {
			t.Errorf("Grade(pt-BR, %s) = %+v, want Exact", got, g)
		}
	})
	t.Run("tokens anywhere in a long title", func(t *testing.T) {
		for name, title := range map[string]string{
			"at byte 255":                    strings.Repeat("a", 254) + " CHT",
			"at byte 256":                    strings.Repeat("a", 255) + " CHT",
			"after a rune spanning byte 256": strings.Repeat("a", 254) + "é CHT",
		} {
			if got := refined(t, "chi", title); got != "zh-Hant" {
				t.Errorf("%s: RefineFromTitle(chi, …) = %q, want zh-Hant", name, got)
			}
		}
	})
}
