package provider

import (
	"fmt"

	"github.com/cplieger/subflux/internal/language"
)

// WirePairs builds an adapter's language table from alternating labels and
// tags ("English", "en", ...), for language.MustProviderMap; it panics on an
// odd count or a tag language.Parse refuses, since both are typos in a
// package-level table.
func WirePairs(labelTag ...string) []language.WirePair {
	if len(labelTag)%2 != 0 {
		panic("provider: WirePairs needs label and tag pairs")
	}
	pairs := make([]language.WirePair, 0, len(labelTag)/2)
	for i := 0; i < len(labelTag); i += 2 {
		t, ok := language.Parse(labelTag[i+1])
		if !ok || t == (language.Tag{}) {
			panic(fmt.Sprintf("provider: WirePairs: %q is no language tag", labelTag[i+1]))
		}
		pairs = append(pairs, language.WirePair{Wire: labelTag[i], Tag: t})
	}
	return pairs
}
