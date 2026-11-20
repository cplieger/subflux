// Package servertest is the contract suite every mediaserver adapter passes,
// the library a harness serves, and decision 16's spelling-table case over a
// server's recorded ingest probe.
package servertest

import (
	"maps"
	"slices"

	"github.com/cplieger/subflux/internal/mediaserver"
)

// Library is what a fake server holds: its items, and each file's sidecar
// subtitles by server path, as the adapter's Labels must answer them.
type Library struct {
	Sidecars map[string][]mediaserver.Label
	Items    []Item
}

// Item is one item a fake server holds, in the server's key scheme: an
// episode of Show, or a movie when Show is "". Files are the server paths of
// its versions, one file each.
type Item struct {
	Key   string
	Show  string
	Files []string
}

func (l *Library) clone() *Library {
	out := &Library{Sidecars: maps.Clone(l.Sidecars), Items: slices.Clone(l.Items)}
	for f, ls := range out.Sidecars {
		out.Sidecars[f] = slices.Clone(ls)
	}
	for i := range out.Items {
		out.Items[i].Files = slices.Clone(out.Items[i].Files)
	}
	return out
}
