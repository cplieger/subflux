package language

import (
	"cmp"
	"slices"
)

// AutoQuery is the offered tags a provider is asked for automatically: each
// offered tag Automatic for some target in needing, once, ordered by the
// position in needing of the first target it serves, then its closeness to
// that target, then offered order. It never returns a ManualOnly tag.
func (p Policy) AutoQuery(needing, offered []Tag) []Tag {
	return query(offered, func(have Tag) (int, Closeness, bool) {
		for i, target := range needing {
			if g := p.Grade(target, have); p.Automatic(g) {
				return i, g.Closeness, true
			}
		}
		return 0, 0, false
	})
}

// ManualQuery is the offered tags a person may pick for target, whatever the
// close-varieties switch: each graded Exact, Substitute or ManualOnly, once,
// ordered by class, then closeness, then offered order.
func (p Policy) ManualQuery(target Tag, offered []Tag) []Tag {
	return query(offered, func(have Tag) (int, Closeness, bool) {
		g := p.Grade(target, have)
		return int(g.Class), g.Closeness, g.Class != ClassUnrelated
	})
}

// query keeps the offered tags rank accepts, deduped, sorted by rank's key
// then offered order.
func query(offered []Tag, rank func(Tag) (int, Closeness, bool)) []Tag {
	type hit struct {
		tag       Tag
		key       int
		closeness Closeness
	}
	var hits []hit
	seen := map[Tag]bool{}
	for _, have := range offered {
		if seen[have] {
			continue
		}
		seen[have] = true
		if key, c, ok := rank(have); ok {
			hits = append(hits, hit{tag: have, key: key, closeness: c})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(a.key, b.key), cmp.Compare(a.closeness, b.closeness))
	})
	out := make([]Tag, len(hits))
	for i, h := range hits {
		out[i] = h.tag
	}
	return out
}
