package search

import (
	"cmp"
	"strings"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
)

// standing is the head of the rank key, the part an upgrade must beat
// strictly: language class, a human file before a machine translation,
// closeness, the variant's own flags before flags that merely serve it, and
// fit. A tie-break never moves a candidate across it.
type standing struct {
	class     language.Class
	closeness language.Closeness
	fit       rank.Fit
	machine   bool
	ownFlags  bool
}

func standingOf(g language.Grade, machine bool, f language.Flags, v language.Variant, fit rank.Fit) standing {
	return standing{class: g.Class, machine: machine, closeness: g.Closeness, ownFlags: f == v.Flags(), fit: fit}
}

// compareStanding is negative when a ranks before b.
func compareStanding(a, b standing) int {
	return cmp.Or(
		cmp.Compare(a.class, b.class),
		boolFirst(!a.machine, !b.machine),
		cmp.Compare(a.closeness, b.closeness),
		boolFirst(a.ownFlags, b.ownFlags),
		cmp.Compare(b.fit, a.fit),
	)
}

// compareRank is design section 4's key, negative when a ranks before b:
// the standing, then the tie-breaks (rate not differing, the same service,
// the same cut, one episode before a season pack, provider priority, then
// id, then the provider's own order).
func compareRank(a, b *judged) int {
	return cmp.Or(
		compareStanding(a.standing, b.standing),
		boolFirst(a.facts.Rate != rank.Differs, b.facts.Rate != rank.Differs),
		boolFirst(a.facts.Service == rank.Same, b.facts.Service == rank.Same),
		boolFirst(a.facts.Cut == rank.Same, b.facts.Cut == rank.Same),
		boolFirst(!a.facts.SeasonPack, !b.facts.SeasonPack),
		cmp.Compare(a.from.config.Priority, b.from.config.Priority),
		strings.Compare(string(a.from.entry.ID), string(b.from.entry.ID)),
		cmp.Compare(a.order, b.order),
	)
}

// boolFirst orders true before false.
func boolFirst(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}
	return 1
}
