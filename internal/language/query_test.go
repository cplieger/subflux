package language

import (
	"slices"
	"testing"
)

func texts(ts []Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.String()
	}
	return out
}

var (
	switchOff = Policy{}
	switchOn  = Policy{&policy{closeVarieties: true}}
)

func TestAutoQueryNorwegianAndPortuguese(t *testing.T) {
	for _, r := range []struct {
		name    string
		p       Policy
		needing []string
		offered []string
		want    []string
	}{
		{"nb asks a map holding only no", switchOff, []string{"nb"}, []string{"no"}, []string{"no"}},
		{"bare pt asks both varieties", switchOff, []string{"pt"}, []string{"pt-PT", "pt-BR"}, []string{"pt-PT", "pt-BR"}},
		{"pt-BR with the switch off", switchOff, []string{"pt-BR"}, []string{"pt-PT", "pt-BR"}, []string{"pt-BR"}},
		{"pt-BR with the switch on", switchOn, []string{"pt-BR"}, []string{"pt-PT", "pt-BR"}, []string{"pt-BR", "pt-PT"}},
	} {
		t.Run(r.name, func(t *testing.T) {
			if got := texts(r.p.AutoQuery(tags(t, r.needing...), tags(t, r.offered...))); !slices.Equal(got, r.want) {
				t.Errorf("AutoQuery = %v; want %v", got, r.want)
			}
		})
	}
}

func TestAutoQueryTraditional(t *testing.T) {
	for _, p := range []Policy{switchOff, switchOn} {
		if got := texts(p.AutoQuery(tags(t, "zh-Hant"), tags(t, "zh-Hans", "zh-Hant"))); !slices.Equal(got, []string{"zh-Hant"}) {
			t.Errorf("switch %v: AutoQuery = %v; want [zh-Hant]", p.CloseVarieties(), got)
		}
	}
}

func TestManualQuery(t *testing.T) {
	offered := tags(t, "de", "pt-PT", "pt", "pt-BR", "es")
	for _, p := range []Policy{switchOff, switchOn} {
		if got, want := texts(p.ManualQuery(mustParse(t, "pt-BR"), offered)), []string{"pt-BR", "pt", "pt-PT"}; !slices.Equal(got, want) {
			t.Errorf("switch %v: ManualQuery = %v; want %v", p.CloseVarieties(), got, want)
		}
	}
	t.Run("never an Unrelated tag", func(t *testing.T) {
		all := corpusTags(t)
		for _, target := range all {
			for _, have := range switchOff.ManualQuery(target, all) {
				if c := switchOff.Grade(target, have).Class; c == ClassUnrelated {
					t.Errorf("ManualQuery(%q) holds %q, graded Unrelated", target, have)
				}
			}
		}
	})
	t.Run("zero target", func(t *testing.T) {
		if got := switchOn.ManualQuery(Tag{}, offered); len(got) != 0 {
			t.Errorf("ManualQuery(zero) = %v", texts(got))
		}
	})
}

func TestAutoQueryNeverManualOnly(t *testing.T) {
	offered := corpusTags(t)
	for _, r := range gradeTable {
		offered = append(offered, mustParse(t, r.target), mustParse(t, r.have))
	}
	for _, p := range []Policy{switchOff, switchOn} {
		for _, target := range offered {
			needing := []Tag{target}
			got := p.AutoQuery(needing, offered)
			for _, have := range offered {
				automatic := p.Automatic(p.Grade(target, have))
				if automatic != slices.Contains(got, have) {
					t.Errorf("switch %v: AutoQuery([%q]) holds %q = %v; its grade %+v is automatic = %v",
						p.CloseVarieties(), target, have, !automatic, p.Grade(target, have), automatic)
				}
				if slices.Contains(got, have) && p.Grade(target, have).Class == ManualOnly {
					t.Errorf("AutoQuery([%q]) holds ManualOnly %q", target, have)
				}
			}
		}
	}
}

func TestQueryOrdering(t *testing.T) {
	got := texts(switchOn.AutoQuery(
		tags(t, "en", "pt-BR"),
		tags(t, "pt-PT", "pt", "en-CA", "en-GB", "pt-BR", "en", "en", "de"),
	))
	if want := []string{"en", "en-CA", "en-GB", "pt-BR", "pt", "pt-PT"}; !slices.Equal(got, want) {
		t.Errorf("AutoQuery = %v; want %v (needing order, then closeness, then offered order, deduped)", got, want)
	}
}

func TestManualQueryOrdering(t *testing.T) {
	got := texts(switchOff.ManualQuery(mustParse(t, "sr-Latn"), tags(t, "sr-Cyrl", "bs", "hr", "sr-Latn-ME", "sr", "sr-Latn")))
	if want := []string{"sr-Latn", "sr-Latn-ME", "bs", "hr", "sr-Cyrl", "sr"}; !slices.Equal(got, want) {
		t.Errorf("ManualQuery = %v; want %v (class, then closeness, then offered order)", got, want)
	}
}
