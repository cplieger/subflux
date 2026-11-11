package language

import (
	"slices"
	"strings"
	"testing"
)

func TestProviderMapRefusals(t *testing.T) {
	en, de := mustParse(t, "en"), mustParse(t, "de")
	for _, r := range []struct {
		name  string
		pairs []WirePair
		want  string
	}{
		{"empty label", []WirePair{{"de", de}, {"", en}}, `pair 1 ("", "en"): label must be`},
		{"label over 64 bytes", []WirePair{{strings.Repeat("a", 65), en}}, "pair 0"},
		{"invalid UTF-8", []WirePair{{"\xff", en}}, "label must be 1 to 64 bytes of UTF-8"},
		{"zero tag", []WirePair{{"xx", Tag{}}}, `pair 0 ("xx", ""): tag is the zero tag`},
		{"repeated label", []WirePair{{"en", en}, {"de", de}, {"en", de}}, `pair 2 ("en", "de"): label is listed twice`},
		{"first bad pair named", []WirePair{{"", Tag{}}, {"en", en}, {"en", en}}, "pair 0"},
	} {
		t.Run(r.name, func(t *testing.T) {
			m, err := newProviderMap(r.pairs)
			if err == nil || !strings.Contains(err.Error(), r.want) {
				t.Errorf("newProviderMap = %v; want an error containing %q", err, r.want)
			}
			if len(m.Tags()) != 0 {
				t.Errorf("a refused map holds %v", texts(m.Tags()))
			}
		})
	}
	t.Run("a 64-byte label", func(t *testing.T) {
		if _, err := newProviderMap([]WirePair{{strings.Repeat("é", 32), en}}); err != nil {
			t.Error(err)
		}
	})
}

func TestProviderMapTagWithTwoLabels(t *testing.T) {
	hant := mustParse(t, "zh-Hant")
	m, err := newProviderMap([]WirePair{{"chinese_traditional", hant}, {"big_5_code", hant}})
	if err != nil {
		t.Fatalf("newProviderMap = %v; a tag may carry two labels", err)
	}
	want := []string{"chinese_traditional", "big_5_code"}
	got := m.Wires(hant)
	if !slices.Equal(got, want) {
		t.Fatalf("Wires(zh-Hant) = %q; want %q in pair order", got, want)
	}
	got[0] = "changed"
	if again := m.Wires(hant); !slices.Equal(again, want) {
		t.Errorf("changing an answer changed the next: Wires(zh-Hant) = %q", again)
	}
	for _, w := range want {
		if tg, ok := m.Tag(w); !ok || tg != hant {
			t.Errorf("Tag(%q) = %q, %v; want zh-Hant", w, tg, ok)
		}
	}
	if tags := texts(m.Tags()); !slices.Equal(tags, []string{"zh-Hant"}) {
		t.Errorf("Tags() = %v; want zh-Hant once", tags)
	}
	var z ProviderMap
	if w := z.Wires(hant); w != nil {
		t.Errorf("zero map Wires = %q; want nil", w)
	}
	if _, err := newProviderMap([]WirePair{{"big_5_code", hant}, {"big_5_code", mustParse(t, "zh-Hans")}}); err == nil ||
		!strings.Contains(err.Error(), `pair 1 ("big_5_code", "zh-Hans"): label is listed twice`) {
		t.Errorf("a repeated label: newProviderMap = %v; want the pair named", err)
	}
}

func TestProviderMapLookups(t *testing.T) {
	m := MustProviderMap([]WirePair{{"pob", mustParse(t, "pt-BR")}, {"por", mustParse(t, "pt-PT")}, {"no", mustParse(t, "no")}, {"pb", mustParse(t, "pt-BR")}})
	t.Run("Wires", func(t *testing.T) {
		if w := m.Wires(mustParse(t, "pt-BR")); !slices.Equal(w, []string{"pob", "pb"}) {
			t.Errorf("Wires(pt-BR) = %q; want [pob pb]", w)
		}
		if w := m.Wires(mustParse(t, "nb")); w != nil {
			t.Errorf("Wires(nb) = %q; a lookup is exact", w)
		}
	})
	t.Run("Tag", func(t *testing.T) {
		if tag, ok := m.Tag("por"); !ok || tag.String() != "pt-PT" {
			t.Errorf("Tag(por) = %q, %v", tag, ok)
		}
		if tag, ok := m.Tag("POR"); ok {
			t.Errorf("Tag(POR) = %q; a lookup is exact", tag)
		}
	})
	t.Run("Tags once each in first-pair order, a copy", func(t *testing.T) {
		got := m.Tags()
		if want := []string{"pt-BR", "pt-PT", "no"}; !slices.Equal(texts(got), want) {
			t.Errorf("Tags() = %v; want %v", texts(got), want)
		}
		got[0] = Tag{}
		if m.Tags()[0].String() != "pt-BR" {
			t.Error("mutating Tags() changed the map")
		}
	})
	t.Run("zero map", func(t *testing.T) {
		var z ProviderMap
		if w := z.Wires(mustParse(t, "en")); w != nil {
			t.Errorf("zero map Wires = %q", w)
		}
		if _, ok := z.Tag("en"); ok {
			t.Error("zero map Tag found")
		}
		if len(z.Tags()) != 0 {
			t.Error("zero map has tags")
		}
	})
}

func TestMustProviderMapPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustProviderMap of a repeated label did not panic")
		}
	}()
	en := mustParse(t, "en")
	MustProviderMap([]WirePair{{"en", en}, {"en", mustParse(t, "de")}})
}
