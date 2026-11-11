package search

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
)

// The no-result back-off.
const (
	noResultInitial = 7 * 24 * time.Hour
	noResultMax     = 2190 * time.Hour
)

// bookEvidence writes 8.4's no-result evidence in one store write: for each
// item, each provider that answered completely and each target it was
// eligible for and asked, a row (count + 1) when none of its candidates is
// usable for any of the item's wanted pairs under that target, and the
// row's deletion when one is. A failed, refused, skipped or partial answer
// books nothing (B01-F09).
func (e *Engine) bookEvidence(ctx context.Context, r *run, answers []answer, picks []pick) error {
	var rows []statedb.NoResult
	for i := range r.items {
		it := &r.items[i]
		for j := range answers {
			a := &answers[j]
			if a.err != nil {
				continue
			}
			for _, t := range a.ask.targets {
				k := noResultKey{provider: string(a.ask.from.entry.ID), tag: t}
				prev, held := it.noResults[k]
				switch {
				case !usableFrom(picks, it, t, a.ask):
					count := prev.Count + 1
					rows = append(rows, statedb.NoResult{Key: it.key, Provider: k.provider, Tag: t, Count: count, Next: r.now.Add(noResultWait(count))})
				case held:
					rows = append(rows, statedb.NoResult{Key: it.key, Provider: k.provider, Tag: t})
				}
			}
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if err := e.deps.Store.PutNoResults(ctx, rows); err != nil {
		return fmt.Errorf("search: book instance %d's no-result evidence: %w", r.inst.ID, err)
	}
	return nil
}

// usableFrom reports whether a's provider returned a usable candidate for
// one of the item's wanted pairs under target.
func usableFrom(picks []pick, it *runItem, target language.Tag, a *ask) bool {
	for i := range picks {
		p := &picks[i]
		if p.want.target != target || !slices.Contains(it.pairs, p.want.pair) {
			continue
		}
		if slices.ContainsFunc(p.ranked, func(j *judged) bool { return j.ask == a }) {
			return true
		}
	}
	return false
}

// noResultWait is how long the count-th consecutive empty answer holds a
// provider off: noResultInitial, doubling, at most noResultMax.
func noResultWait(count int) time.Duration {
	d := noResultInitial
	for i := 1; i < count && d < noResultMax; i++ {
		d *= 2
	}
	return min(d, noResultMax)
}
