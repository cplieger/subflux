package animemap

import (
	"github.com/cplieger/subflux/internal/language"
)

// Languages is every pickable tag and the tag of each of labels, each its
// own label: an anime adapter reads each track's language from the tag its
// release carries, so every target is asked and no label is ever sent.
// labels are the provider's recorded labels a pickable tag does not cover
// (a region, a script); one language.Parse refuses panics.
func Languages(labels ...string) language.ProviderMap {
	names := language.Names()
	pairs := make([]language.WirePair, 0, len(names)+len(labels))
	have := map[language.Tag]bool{}
	add := func(t language.Tag) {
		if !have[t] {
			have[t] = true
			pairs = append(pairs, language.WirePair{Wire: t.String(), Tag: t})
		}
	}
	for _, n := range names {
		add(n.Tag)
	}
	for _, l := range labels {
		t, ok := language.Parse(l)
		if !ok || t == (language.Tag{}) {
			panic("animemap: label " + l + " does not parse")
		}
		add(t)
	}
	return language.MustProviderMap(pairs)
}
