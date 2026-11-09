package mediasource

import (
	"context"
	"errors"
	"fmt"

	"github.com/cplieger/subflux/internal/media"
)

// OrderJoin is the aired join the Set keys every episode root through
// (design 3.6): one Joined per listed episode, in order, with the evidence
// the yielded root carries; or an error wrapping ErrAiredOrderUnavailable, a
// passed deadline's included, or the store's error, which fails the call. It
// is a NewSet argument, not a Deps field, so no adapter or media server gets
// it.
type OrderJoin interface {
	Join(ctx context.Context, q JoinQuery) ([]Joined, *RootEvidence, error)
}

// key gives a checked root's items their aired keys and the root its
// evidence from the join; titles are the adapter's own, parallel to items.
// A root the join cannot key answers its unread report; any other failure
// fails the call.
func (a *answer) key(ctx context.Context, orders OrderJoin, r *Root, titles []string, items []Item) (*SourceError, error) {
	r.Evidence = nil
	if r.Native.Kind != media.Episode || r.External.TVDB == 0 {
		for i := range items {
			items[i].Numbering = media.Numbering{}
			if !r.Order.Aired {
				items[i].Absolute = 0
			}
		}
		return nil, nil
	}
	q := JoinQuery{
		Episodes: make([]Listed, len(items)), Source: a.src.id, Root: r.Native,
		Language: r.Language, Order: r.Order, TVDB: r.External.TVDB,
	}
	for i := range items {
		it := &items[i]
		q.Episodes[i] = Listed{AirDate: it.AirDate, Title: titles[i], Season: it.SeriesNumbering.Season, Episode: it.SeriesNumbering.Episode, Absolute: it.Absolute}
	}
	joined, ev, err := orders.Join(ctx, q)
	switch {
	case errors.Is(err, ErrAiredOrderUnavailable):
		return &SourceError{Source: a.src.id, Op: a.op, Cause: Unreachable, Err: &AiredOrderError{Title: r.Title, Err: err}}, nil
	case err != nil:
		return nil, fmt.Errorf("mediasource: source %s: aired join of root %s: %w", a.src.id, r.Native.ID, err)
	case len(joined) != len(items):
		return nil, fmt.Errorf("mediasource: source %s: aired join of root %s answered %d episodes for %d", a.src.id, r.Native.ID, len(joined), len(items))
	}
	for i, j := range joined {
		items[i].Numbering, items[i].Absolute = media.Numbering{}, 0
		if j.OK {
			items[i].Numbering = media.Numbering{Scheme: media.TVDB, Season: j.Season, Episode: j.Episode}
			items[i].Absolute = j.Absolute
		}
	}
	r.Evidence = ev
	return nil, nil
}

func titlesOf(items []Item) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Title
	}
	return out
}
