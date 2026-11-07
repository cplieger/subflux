package animetosho

import (
	"sync"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
)

// ID is AnimeTosho's provider id.
const ID provider.ProviderID = "animetosho"

// name is how the map's alert names this provider.
const name = "AnimeTosho"

// languages lists every pickable tag, so every target is asked; an
// attachment's ISO 639-2 language is read through language.Parse, not this
// map, whose labels are never sent.
var languages = sync.OnceValue(func() language.ProviderMap {
	names := language.Names()
	pairs := make([]language.WirePair, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, language.WirePair{Wire: n.Tag.String(), Tag: n.Tag})
	}
	return language.MustProviderMap(pairs)
})

// NewEntry is AnimeTosho's row of the provider table, reading m, the
// process's anime map, from every adapter it builds.
func NewEntry(m *animemap.Map) provider.Entry {
	return provider.Entry{
		ID:        ID,
		Summary:   "Anime subtitles from the releases AnimeTosho indexes, found by AniDB episode; needs no account.",
		Hosts:     append([]string{feedHost, storageHost}, animemap.Hosts()...),
		Schema:    formschema.Schema{},
		Languages: languages(),
		Data:      func(report func(text string, active bool)) { m.Report(name, report) },
		New: func(c provider.Client, _ formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, m: m}, nil
		},
	}
}
