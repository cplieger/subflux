package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/statedb"
)

// seasonStop is how many other episodes of a season holding a live
// no-result row for a provider and target stop an automatic run asking it
// for the season's next episode.
const seasonStop = 3

// ask is one provider a run asks: the tags it is asked for and the targets
// they serve automatically.
type ask struct {
	from    *bound
	langs   []language.Tag
	targets []language.Tag
}

// answer is one ask's answer: the candidates it returned, and its error,
// nil when it answered completely.
type answer struct {
	ask   *ask
	err   error
	cands []provider.Candidate
}

// planAsks reads the items' no-result rows and lists the providers the run
// asks: each enabled provider, once, for the wanted targets it is eligible
// for, through AutoQuery (8.2 steps 4 and 5).
func (r *run) planAsks(ctx context.Context, e *Engine) error {
	var targets []language.Tag
	for _, w := range r.wants {
		if !slices.Contains(targets, w.target) {
			targets = append(targets, w.target)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	if err := r.readNoResults(ctx, e); err != nil {
		return err
	}
	for i := range e.providers {
		if a := r.askOf(e, &e.providers[i], targets); len(a.targets) > 0 {
			r.asks = append(r.asks, a)
		}
	}
	return nil
}

// askOf is what b is asked for the targets: the tags AutoQuery offers for
// those it is eligible for, every target under a user's run.
func (r *run) askOf(e *Engine, b *bound, targets []language.Tag) *ask {
	var needing []language.Tag
	for _, t := range targets {
		if r.cause == statedb.Item || !r.stopped(b.entry.ID, t) {
			needing = append(needing, t)
		}
	}
	a := &ask{from: b, langs: e.policy.AutoQuery(needing, b.entry.Languages.Tags())}
	for _, t := range needing {
		if slices.ContainsFunc(a.langs, func(l language.Tag) bool { return e.policy.Automatic(e.policy.Grade(t, l)) }) {
			a.targets = append(a.targets, t)
		}
	}
	return a
}

// readNoResults reads the rows of each season the items are in, once per
// season, keeping each item's live rows and its season stops.
func (r *run) readNoResults(ctx context.Context, e *Engine) error {
	type season struct {
		root   media.RootKey
		number int
	}
	read := map[season][]statedb.NoResult{}
	for i := range r.items {
		it := &r.items[i]
		s := season{root: it.key.Root(), number: it.item.Numbering.Season}
		rows, done := read[s]
		if !done {
			var err error
			rows, err = e.deps.Store.NoResults(ctx, s.root, uint16(s.number)) //nolint:gosec // G115: a season passes media.ValidEpisodeNumber
			if err != nil {
				return fmt.Errorf("search: read the no-result rows of %s: %w", it.key, err)
			}
			read[s] = rows
		}
		it.keepRows(rows, r.now, r.root.Native.Kind == media.Episode)
	}
	return nil
}

// keepRows keeps the item's own rows among its season's and, for an episode,
// each provider and target seasonStop other episodes hold live at now.
func (it *runItem) keepRows(rows []statedb.NoResult, now time.Time, episode bool) {
	it.noResults = map[noResultKey]statedb.NoResult{}
	it.seasonStopped = map[noResultKey]bool{}
	others := map[noResultKey]int{}
	for i := range rows {
		row := &rows[i]
		k := noResultKey{provider: row.Provider, tag: row.Tag}
		switch {
		case row.Key == it.key:
			it.noResults[k] = *row
		case episode && row.Next.After(now):
			others[k]++
			if others[k] == seasonStop {
				it.seasonStopped[k] = true
			}
		}
	}
}

// stopped reports whether every item holds a live no-result row for id
// and target, or sits in a season where seasonStop other episodes do.
func (r *run) stopped(id provider.ProviderID, target language.Tag) bool {
	k := noResultKey{provider: string(id), tag: target}
	for i := range r.items {
		it := &r.items[i]
		row, held := it.noResults[k]
		if (!held || !row.Next.After(r.now)) && !it.seasonStopped[k] {
			return false
		}
	}
	return true
}

// sweep asks every provider r planned at once, under the funnel's limits;
// it returns ctx's error when ctx ended.
func (r *run) sweep(ctx context.Context) ([]answer, error) {
	answers := make([]answer, len(r.asks))
	var wg sync.WaitGroup
	for i, a := range r.asks {
		wg.Go(func() {
			q := r.query
			q.Langs = a.langs
			cands, err := a.from.search(ctx, providercalls.Automatic, &q)
			answers[i] = answer{ask: a, cands: cands, err: err}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return answers, nil
}

// search is the engine's one path to a provider's Search: through b's
// binding as class c. Its candidates beside a *provider.Failure are the
// pages read before the failing one.
func (b *bound) search(ctx context.Context, c providercalls.Class, q *provider.Query) ([]provider.Candidate, error) {
	var cands []provider.Candidate
	err := b.binding.Call(ctx, providercalls.Search, c, func(ctx context.Context) error {
		var err error
		cands, err = b.adapter.Search(ctx, *q)
		return err
	})
	if fail := (*provider.Failure)(nil); err != nil && !errors.As(err, &fail) {
		return nil, err
	}
	return cands, err
}
