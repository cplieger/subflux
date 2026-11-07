// Package providers is the table of every provider subflux can search. It is
// its own package because each adapter imports internal/provider, which so
// cannot import them back.
package providers

import (
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/betaseries"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/hdbits"
	"github.com/cplieger/subflux/internal/provider/opensubtitles"
	"github.com/cplieger/subflux/internal/provider/subdl"
	"github.com/cplieger/subflux/internal/provider/subsource"
	"github.com/cplieger/subflux/internal/provider/yifysubtitles"
)

// Entries is every provider's row, in id order, freshly built on each call,
// the anime adapters sharing one anime map. A process calls it once, so an
// engine swap keeps the map and its refresh clock.
func Entries() []provider.Entry {
	m := animemap.New()
	return []provider.Entry{
		animetosho.NewEntry(m),
		betaseries.NewEntry(),
		gestdown.NewEntry(),
		hdbits.NewEntry(),
		opensubtitles.NewEntry(),
		subdl.NewEntry(),
		subsource.NewEntry(),
		yifysubtitles.NewEntry(),
	}
}
