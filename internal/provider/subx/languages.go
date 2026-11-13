package subx

import (
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

// languages are SubX's two Spanish varieties. SubX labels every item
// Spanish, so the adapter reads the variety from its description and sends
// no label.
var languages = language.MustProviderMap(provider.WirePairs("es-ES", "es-ES", "es-419", "es-419"))

var (
	spainSpanish = mapped("es-ES")
	latinSpanish = mapped("es-419")
)

func mapped(label string) language.Tag {
	t, ok := languages.Tag(label)
	if !ok {
		panic("subx: " + label + " is not mapped")
	}
	return t
}
