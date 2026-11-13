// Package providers is the table of every provider subflux can search. It is
// its own package because each adapter imports internal/provider, which so
// cannot import them back.
package providers

import (
	"context"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/assrt"
	"github.com/cplieger/subflux/internal/provider/betaseries"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/hdbits"
	"github.com/cplieger/subflux/internal/provider/jimaku"
	"github.com/cplieger/subflux/internal/provider/opensubtitles"
	"github.com/cplieger/subflux/internal/provider/subdl"
	"github.com/cplieger/subflux/internal/provider/subsource"
	"github.com/cplieger/subflux/internal/provider/subsro"
	"github.com/cplieger/subflux/internal/provider/subx"
	"github.com/cplieger/subflux/internal/provider/tsukihime"
	"github.com/cplieger/subflux/internal/provider/yifysubtitles"
)

// Table is the provider table: every provider's row, in id order, the anime
// adapters sharing one anime map. A process builds one, so an engine swap
// keeps the map and its refresh clock, and closes it at its end.
type Table struct {
	m       *animemap.Map
	Entries []provider.Entry
}

// New builds a Table.
func New() *Table {
	m := animemap.New()
	return &Table{m: m, Entries: []provider.Entry{
		animetosho.NewEntry(m),
		assrt.NewEntry(),
		betaseries.NewEntry(),
		gestdown.NewEntry(),
		hdbits.NewEntry(),
		jimaku.NewEntry(),
		opensubtitles.NewEntry(),
		subdl.NewEntry(),
		subsource.NewEntry(),
		subsro.NewEntry(),
		subx.NewEntry(),
		tsukihime.NewEntry(m),
		yifysubtitles.NewEntry(),
	}}
}

// Close ends the anime map's refresh in flight and waits for it, or for
// ctx's end, whose error it returns. The engines built over t's entries are
// ended first.
func (t *Table) Close(ctx context.Context) error { return t.m.Close(ctx) }
