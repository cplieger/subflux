// Package tsukihime is the TsukiHime provider: the subtitle tracks of the
// anime releases TsukiHime indexes, found through the anime map by AniDB
// anime and episode, and fetched from TsukiHime's storage.
package tsukihime

import (
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
)

// ID is TsukiHime's provider id.
const ID provider.ProviderID = "tsukihime"

// name is how the map's alert names this provider.
const name = "TsukiHime"

const (
	apiHost     = "api.tsukihime.org"
	storageHost = "storage.tsukihime.org"
	apiURL      = "https://" + apiHost + "/v1"
)

type adapter struct {
	m *animemap.Map
	c provider.Client
}

// NewEntry is TsukiHime's row of the provider table, reading m, the process's
// anime map, from every adapter it builds.
func NewEntry(m *animemap.Map) provider.Entry {
	return provider.Entry{
		ID:        ID,
		Summary:   "Anime subtitles from the releases TsukiHime indexes, found by AniDB episode; needs no account.",
		Hosts:     append([]string{apiHost, storageHost}, animemap.Hosts()...),
		Schema:    formschema.Schema{},
		Languages: languages(),
		Data:      func(report func(text string, active bool)) { m.Report(name, report) },
		New: func(c provider.Client, _ formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, m: m}, nil
		},
	}
}
