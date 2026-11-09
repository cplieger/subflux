package seriesorder

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/skyhook"
)

// airedOrder is SkyHook's type for TVDB's aired order; absoluteOrder is a
// numbering, never asked as another order (DECISIONS 40).
const (
	airedOrder    = "official"
	absoluteOrder = "absolute"
)

// fetch reads one new reading of q's series: the aired order, then every
// other order SkyHook lists and the series' own, each in English. Its other
// title languages are the caller's to complete. after is the newest kept
// reading's Fetched, which the new one must follow.
func (s *Service) fetch(ctx context.Context, q *mediasource.JoinQuery, after time.Time) (mediasource.SeriesEvidence, error) {
	tvdb := int(q.TVDB) // #nosec G115 -- a TVDB id is at most 2^53-1; skyhook refuses any above 2^31-1
	official, err := s.show(ctx, tvdb, english, airedOrder)
	if err != nil {
		return mediasource.SeriesEvidence{}, err
	}
	c := mediasource.Catalogue{TVDB: q.TVDB, Episodes: airedEpisodes(official.Episodes)}
	for _, o := range orderTypes(official.Orders, q.Order.Type) {
		show, err := s.show(ctx, tvdb, english, o.Type)
		if err != nil {
			return mediasource.SeriesEvidence{}, err
		}
		c.Orders = append(c.Orders, joinOrder(o, c.Episodes, show.Episodes))
	}
	// RootEvidence.Validate refuses a Held fetched no later than Bound, so a
	// wall clock stepping back would leave the root's evidence unstorable.
	c.Fetched = time.Now()
	if !c.Fetched.After(after) {
		c.Fetched = after.Add(time.Nanosecond)
	}
	r := mediasource.SeriesEvidence{Catalogue: c, Titles: []mediasource.TitleSet{titleSet(english, official.Episodes)}}
	if err := (mediasource.RootEvidence{Bound: r}).Validate(); err != nil {
		return mediasource.SeriesEvidence{}, fmt.Errorf("skyhook: series %d: %w", tvdb, err)
	}
	return r, nil
}

// show is one Show whose 404 fails the fetch.
func (s *Service) show(ctx context.Context, tvdb int, lang, order string) (skyhook.Show, error) {
	show, found, err := s.sky.Show(ctx, tvdb, lang, order)
	switch {
	case err != nil:
		return skyhook.Show{}, err
	case !found:
		return skyhook.Show{}, fmt.Errorf("skyhook: series %d, order %s in %s: SkyHook lists no such series", tvdb, order, lang)
	}
	return show, nil
}

// orderTypes are the orders a fetch asks besides the aired one, in
// SkyHook's order, then the series' own when SkyHook lists it not.
func orderTypes(listed []skyhook.Order, own string) []skyhook.Order {
	var out []skyhook.Order
	add := func(o skyhook.Order) {
		if o.Type != "" && o.Type != airedOrder && o.Type != absoluteOrder &&
			!slices.ContainsFunc(out, func(x skyhook.Order) bool { return x.Type == o.Type }) {
			out = append(out, o)
		}
	}
	for _, o := range listed {
		add(o)
	}
	add(skyhook.Order{Type: own})
	return out
}

func airedEpisodes(eps []skyhook.Episode) []mediasource.CatalogueEpisode {
	out := make([]mediasource.CatalogueEpisode, len(eps))
	for i, e := range eps {
		out[i] = mediasource.CatalogueEpisode{AirDate: e.AirDate, ID: e.ID, Season: e.Season, Episode: e.Number, Absolute: e.Absolute}
	}
	return out
}

// joinOrder places an order's episodes against the aired list by TVDB
// episode id; an episode the aired list lacks, or a second place of an aired
// one, goes to Extra.
func joinOrder(o skyhook.Order, aired []mediasource.CatalogueEpisode, eps []skyhook.Episode) mediasource.CatalogueOrder {
	at := make(map[uint64]int, len(aired))
	for i, e := range aired {
		at[e.ID] = i
	}
	out := mediasource.CatalogueOrder{Type: o.Type, Name: o.Name, Places: make([]mediasource.Place, len(aired))}
	for _, e := range eps {
		if i, ok := at[e.ID]; ok && !out.Places[i].Listed {
			out.Places[i] = mediasource.Place{Season: e.Season, Episode: e.Number, Listed: true}
			continue
		}
		out.Extra = append(out.Extra, mediasource.ExtraPlace{AirDate: e.AirDate, Season: e.Season, Episode: e.Number})
	}
	slices.SortStableFunc(out.Extra, func(a, b mediasource.ExtraPlace) int {
		return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
	})
	return out
}

// titleSet is an aired answer's titles in lang; an episode SkyHook titles
// null is absent.
func titleSet(lang string, eps []skyhook.Episode) mediasource.TitleSet {
	ts := mediasource.TitleSet{Language: lang, Found: true}
	for _, e := range eps {
		if e.Title != "" {
			ts.Titles = append(ts.Titles, mediasource.EpisodeTitle{Title: e.Title, ID: e.ID})
		}
	}
	slices.SortFunc(ts.Titles, func(a, b mediasource.EpisodeTitle) int { return cmp.Compare(a.ID, b.ID) })
	return ts
}

// sameAired reports whether an aired answer lists exactly the reading's aired
// episodes: ids, seasons, episodes and air dates.
func sameAired(c *mediasource.Catalogue, eps []skyhook.Episode) bool {
	if len(eps) != len(c.Episodes) {
		return false
	}
	type key struct {
		date            string
		id              uint64
		season, episode int
	}
	want := make(map[key]bool, len(c.Episodes))
	for _, e := range c.Episodes {
		want[key{e.AirDate, e.ID, e.Season, e.Episode}] = true
	}
	for _, e := range eps {
		k := key{e.AirDate, e.ID, e.Season, e.Number}
		if !want[k] {
			return false
		}
		delete(want, k)
	}
	return true
}
