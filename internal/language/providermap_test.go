package language

import (
	"slices"
	"strings"
	"testing"
)

func TestNewProviderMapRefusals(t *testing.T) {
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
		{"repeated tag", []WirePair{{"en", en}, {"eng", en}}, `pair 1 ("eng", "en"): tag is listed twice`},
		{"first bad pair named", []WirePair{{"", Tag{}}, {"en", en}, {"en", en}}, "pair 0"},
	} {
		t.Run(r.name, func(t *testing.T) {
			m, err := NewProviderMap(r.pairs)
			if err == nil || !strings.Contains(err.Error(), r.want) {
				t.Errorf("NewProviderMap = %v; want an error containing %q", err, r.want)
			}
			if len(m.Tags()) != 0 {
				t.Errorf("a refused map holds %v", texts(m.Tags()))
			}
		})
	}
	t.Run("a 64-byte label", func(t *testing.T) {
		if _, err := NewProviderMap([]WirePair{{strings.Repeat("é", 32), en}}); err != nil {
			t.Error(err)
		}
	})
}

func TestProviderMapLookups(t *testing.T) {
	m := MustProviderMap([]WirePair{{"pob", mustParse(t, "pt-BR")}, {"por", mustParse(t, "pt-PT")}, {"no", mustParse(t, "no")}})
	t.Run("Wire", func(t *testing.T) {
		if w, ok := m.Wire(mustParse(t, "pt-BR")); !ok || w != "pob" {
			t.Errorf("Wire(pt-BR) = %q, %v", w, ok)
		}
		if w, ok := m.Wire(mustParse(t, "nb")); ok {
			t.Errorf("Wire(nb) = %q; a lookup is exact", w)
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
	t.Run("Tags in pair order, a copy", func(t *testing.T) {
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
		if _, ok := z.Wire(mustParse(t, "en")); ok {
			t.Error("zero map Wire found")
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
