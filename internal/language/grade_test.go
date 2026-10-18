package language

import (
	"fmt"
	"testing"

	"github.com/cplieger/langtag/v3"
)

type gradeRow struct {
	target, have string
	closeness    Closeness
	class        Class
}

// gradeTable is the design's case table, then the pairs of the langtag gap
// study's proximity and precision tables.
var gradeTable = []gradeRow{
	{"sr-Latn", "sr-Cyrl", SameLanguage, ManualOnly},
	{"pt-BR", "pt", SameLanguage, Substitute},
	{"pt-BR", "pt-PT", OtherRegion, Substitute},
	{"pt", "pt-BR", SameLanguage, Exact},
	{"es-419", "es-MX", SameLanguage, Exact},
	{"zh-Hans", "zh", SameLanguage, Exact},
	{"zh-Hant", "zh", OtherScript, ManualOnly},
	{"sr-Latn", "hr", Intelligible, Substitute},
	{"sr-Cyrl", "hr", Intelligible, ManualOnly},
	{"zh-Hant", "zh-Hant-HK", SameLanguage, Exact},
	{"zh-Hant", "zh-MY", OtherScript, ManualOnly},
	{"ku", "ku-IQ", OtherScript, ManualOnly},
	{"cnr", "cnr-Latn", SameLanguage, ManualOnly},
	{"nb", "no", SameLanguage, Exact},
	{"nb", "nn", Intelligible, Substitute},
	{"ca", "es", ClosenessUnrelated, ClassUnrelated},

	{"nb", "da", Intelligible, Substitute},
	{"nn", "da", ClosenessUnrelated, ClassUnrelated},
	{"es-419", "es-ES", OtherRegion, Substitute},
	{"sr-Latn", "bs", Intelligible, Substitute},
	{"zh-Hant", "zh-Hans", OtherScript, ManualOnly},
	{"ms", "id", Intelligible, Substitute},
	{"zh-Hans", "zh-Hant", OtherScript, ManualOnly},
	{"sr-Cyrl", "sr-Latn", SameLanguage, ManualOnly},
	{"fr-CA", "fr", SameLanguage, Substitute},
	{"fr", "fr-CA", SameLanguage, Exact},
	{"fr-CA", "fr-FR", SameLanguage, Substitute},
	{"yue", "zh-Hant", ClosenessUnrelated, ClassUnrelated},

	{"pt-BR", "pt-BR", Identical, Exact},
	{"es-MX", "es-419", SameLanguage, Substitute},
	{"es-MX", "es-ZZ", SameLanguage, Substitute},
	{"sr", "sr-Latn", SameLanguage, Exact},
	{"sr-Cyrl", "sr", SameLanguage, Exact},
	{"uz", "uz-Cyrl", OtherScript, ManualOnly},
	{"cnr", "cnr", Identical, Exact},
	{"cnr-Cyrl", "cnr", SameLanguage, ManualOnly},
}

func TestGradeTable(t *testing.T) {
	for _, r := range gradeTable {
		t.Run(r.target+" offered "+r.have, func(t *testing.T) {
			g := Policy{}.Grade(mustParse(t, r.target), mustParse(t, r.have))
			if g.Closeness != r.closeness {
				t.Errorf("Closeness = %d; want %d", g.Closeness, r.closeness)
			}
			if g.Class != r.class {
				t.Errorf("Class = %d; want %d", g.Class, r.class)
			}
		})
	}
}

func TestReasonRows(t *testing.T) {
	nn := langtag.MustParse("nn")
	langtagReason, _ := langtag.Prefer(langtag.MustParse("nb")).Reason(nn)
	for _, r := range []struct{ target, have, want string }{
		{"en", "", "No language tag"},
		{"", "en", "No language tag"},
		{"ca", "es", "Different language"},
		{"cnr", "cnr-Latn", "Script unknown"},
		{"cnr-Cyrl", "cnr", "Script unknown"},
		{"zh-Hant", "zh", "No script stated"},
		{"sr-Cyrl", "hr", "Other script"},
		{"sr-Latn", "sr-Cyrl", "Other script"},
		{"nb", "nn", langtagReason},
		{"pt-BR", "pt", "No region stated"},
		{"es-MX", "es-ZZ", "No region stated"},
		{"pt-BR", "pt-PT", "Other region"},
		{"fr-CA", "fr-FR", "Other region"},
		{"pt", "pt-BR", "Same language"},
		{"pt-BR", "pt-BR", "Same language"},
	} {
		t.Run(r.target+" offered "+r.have, func(t *testing.T) {
			var target, have Tag
			if r.target != "" {
				target = mustParse(t, r.target)
			}
			if r.have != "" {
				have = mustParse(t, r.have)
			}
			if got := (Policy{}).Grade(target, have).Reason; got != r.want {
				t.Errorf("Reason = %q; want %q", got, r.want)
			}
		})
	}
	if langtagReason == "" {
		t.Error("langtag records no reason for nb offered nn")
	}
}

func TestClassOfEveryCombination(t *testing.T) {
	want := map[Closeness][4]Class{ // coversScript, coversRegion: tt, tf, ft, ff
		Identical:          {Exact, Substitute, ManualOnly, ManualOnly},
		SameLanguage:       {Exact, Substitute, ManualOnly, ManualOnly},
		OtherRegion:        {Substitute, Substitute, ManualOnly, ManualOnly},
		Intelligible:       {Substitute, Substitute, ManualOnly, ManualOnly},
		OtherScript:        {ManualOnly, ManualOnly, ManualOnly, ManualOnly},
		ClosenessUnrelated: {ClassUnrelated, ClassUnrelated, ClassUnrelated, ClassUnrelated},
	}
	for c, classes := range want {
		for i, covers := range [4][2]bool{{true, true}, {true, false}, {false, true}, {false, false}} {
			t.Run(fmt.Sprintf("closeness %d script %v region %v", c, covers[0], covers[1]), func(t *testing.T) {
				if got := classOf(c, covers[0], covers[1]); got != classes[i] {
					t.Errorf("classOf = %d; want %d", got, classes[i])
				}
			})
		}
	}
}

func TestAutomaticBothSwitchStates(t *testing.T) {
	off, on := Policy{}, Policy{&policy{closeVarieties: true}}
	for class, want := range map[Class][2]bool{
		Exact: {true, true}, Substitute: {false, true}, ManualOnly: {false, false}, ClassUnrelated: {false, false}, 0: {false, false},
	} {
		t.Run(fmt.Sprint(class), func(t *testing.T) {
			g := Grade{Class: class}
			if got := off.Automatic(g); got != want[0] {
				t.Errorf("switch off: Automatic = %v; want %v", got, want[0])
			}
			if got := on.Automatic(g); got != want[1] {
				t.Errorf("switch on: Automatic = %v; want %v", got, want[1])
			}
		})
	}
}
