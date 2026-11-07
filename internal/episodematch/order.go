package episodematch

import (
	"slices"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Series is what the steps read about the item's series.
type Series struct {
	// Reading is the root's bound reading, nil when there is none. A reading
	// lacking the item's aired episode counts as none.
	Reading *mediasource.SeriesEvidence
	// Languages are the ISO 639-2/T codes of the reading's title sets to read:
	// the subtitle's language and English. A set the reading lacks is skipped.
	Languages []string
	// Items are the root's items, with their titles as the source lists them.
	Items []mediasource.Item
	Order mediasource.Order
}

type place struct{ season, episode int }

func placeOf(m epmarker.Marker) place { return place{m.Season, m.Episode} }

type episode struct {
	titles []string
	// aired is the episode's aired place, its own place with no reading.
	aired place
}

func (e *episode) special() bool { return e.aired.season == 0 }

// order is one known order: places is parallel to series.episodes, at its
// inverse over listed places, and extra the places it gives an episode the
// aired list lacks.
type order struct {
	at     map[place][]int
	extra  map[place]bool
	places []mediasource.Place
}

// single is the one aired episode o places at p, false when it places none
// or more than one there.
func (o *order) single(p place) (int, bool) {
	eps := o.at[p]
	if len(eps) != 1 || o.extra[p] {
		return 0, false
	}
	return eps[0], true
}

// another reports whether o places at p an episode other than e.
func (o *order) another(p place, e int) bool {
	return o.extra[p] || slices.ContainsFunc(o.at[p], func(x int) bool { return x != e })
}

// series is a Series read once: its episodes (the reading's aired list, else
// the items), its known orders, the series' own among them, and items[i],
// the episode of Items[i] (-1 when the reading lacks it).
type series struct {
	episodes []episode
	orders   []order
	items    []int
	own      int
	index    index
}

// index is whether some known order moves a regular episode; unknown when
// the series has no catalogue reading.
type index uint8

const (
	indexUnknown index = iota
	indexFalse
	indexTrue
)

// newSeries reads s for Items[item].
func newSeries(s *Series, item int) *series {
	if s.Reading == nil {
		return fromItems(s)
	}
	c := &s.Reading.Catalogue
	byAired := make(map[place]int, len(c.Episodes))
	out := &series{episodes: make([]episode, len(c.Episodes)), items: make([]int, len(s.Items))}
	for i, e := range c.Episodes {
		p := place{e.Season, e.Episode}
		out.episodes[i].aired = p
		if _, dup := byAired[p]; !dup {
			byAired[p] = i
		}
	}
	for i := range s.Items {
		n := &s.Items[i].Numbering
		e, ok := byAired[place{n.Season, n.Episode}]
		if !ok {
			e = -1
		}
		out.items[i] = e
	}
	if out.items[item] < 0 {
		return fromItems(s)
	}
	orders := slices.Clip(c.Orders)
	out.own = slices.IndexFunc(orders, func(o mediasource.CatalogueOrder) bool { return o.Type == s.Order.Type }) + 1
	switch {
	case s.Order.Aired:
		out.own = 0
	case out.own == 0:
		orders = append(orders, ownOrder(s, out.items, len(c.Episodes)))
		out.own = len(orders)
	}
	read := *c
	read.Orders = orders
	out.index = indexFalse
	if read.Reordered() {
		out.index = indexTrue
	}
	aired := make([]mediasource.Place, len(c.Episodes))
	for i, e := range out.episodes {
		aired[i] = mediasource.Place{Season: e.aired.season, Episode: e.aired.episode, Listed: true}
	}
	out.orders = append(out.orders, newOrder(aired, nil))
	for i := range orders {
		out.orders = append(out.orders, newOrder(orders[i].Places, orders[i].Extra))
	}
	out.readTitles(s, c)
	return out
}

// ownOrder is the series' own order built from its items' places, for an
// order the reading does not list.
func ownOrder(s *Series, items []int, episodes int) mediasource.CatalogueOrder {
	o := mediasource.CatalogueOrder{Type: s.Order.Type, Name: s.Order.Name, Places: make([]mediasource.Place, episodes)}
	for i, e := range items {
		if e >= 0 {
			n := s.Items[i].SeriesNumbering
			o.Places[e] = mediasource.Place{Season: n.Season, Episode: n.Episode, Listed: true}
		}
	}
	return o
}

func newOrder(places []mediasource.Place, extra []mediasource.ExtraPlace) order {
	o := order{places: places, at: make(map[place][]int, len(places)), extra: make(map[place]bool, len(extra))}
	for i, p := range places {
		if p.Listed {
			o.at[place{p.Season, p.Episode}] = append(o.at[place{p.Season, p.Episode}], i)
		}
	}
	for _, x := range extra {
		o.extra[place{x.Season, x.Episode}] = true
	}
	return o
}

func (s *series) readTitles(src *Series, c *mediasource.Catalogue) {
	byID := make(map[uint64]int, len(c.Episodes))
	for i, e := range c.Episodes {
		byID[e.ID] = i
	}
	for _, set := range src.Reading.Titles {
		if !set.Found || !slices.Contains(src.Languages, set.Language) {
			continue
		}
		for _, t := range set.Titles {
			if e, ok := byID[t.ID]; ok {
				s.episodes[e].titles = append(s.episodes[e].titles, t.Title)
			}
		}
	}
	for i, e := range s.items {
		if e >= 0 && src.Items[i].Title != "" {
			s.episodes[e].titles = append(s.episodes[e].titles, src.Items[i].Title)
		}
	}
}

// fromItems is a series with no reading: its episodes are the items, with
// their own titles, and the series' own order is the only order known.
func fromItems(s *Series) *series {
	out := &series{episodes: make([]episode, len(s.Items)), items: make([]int, len(s.Items)), index: indexUnknown}
	places := make([]mediasource.Place, len(s.Items))
	for i := range s.Items {
		n := s.Items[i].SeriesNumbering
		out.items[i] = i
		out.episodes[i].aired = place{n.Season, n.Episode}
		if t := s.Items[i].Title; t != "" {
			out.episodes[i].titles = []string{t}
		}
		places[i] = mediasource.Place{Season: n.Season, Episode: n.Episode, Listed: true}
	}
	out.orders = []order{newOrder(places, nil)}
	return out
}

// ownSeason reports whether the series' own order places episode e in season.
func (s *series) ownSeason(e, season int) bool {
	p := s.orders[s.own].places[e]
	return p.Listed && p.Season == season
}

// assign returns each name's identity: the episode its title unit is
// confidently assigned to, else unassigned. On an index-false series only an
// assignment to a special stands, but every episode stays a candidate: a
// regular episode sharing a special's title (Game of Thrones' "You Win or You
// Die") must leave a member so titled unassigned, not assign it the special.
func (s *series) assign(names []Name) []int {
	titles := make([][]string, len(s.episodes))
	for e := range s.episodes {
		titles[e] = s.episodes[e].titles
	}
	ident := assignTitles(names, titles)
	if s.index == indexFalse {
		for i, e := range ident {
			if e != unassigned && !s.episodes[e].special() {
				ident[i] = unassigned
			}
		}
	}
	return ident
}

// compatible lists the orders no confidently assigned regular member
// contradicts by claiming a number at which the order places another episode.
func (s *series) compatible(names []Name, ident []int) []int {
	var out []int
	for o := range s.orders {
		if !s.contradicted(o, names, ident) {
			out = append(out, o)
		}
	}
	return out
}

func (s *series) contradicted(o int, names []Name, ident []int) bool {
	for i, e := range ident {
		if e == unassigned || s.episodes[e].special() {
			continue
		}
		for _, c := range names[i].Claims {
			if s.orders[o].another(placeOf(c), e) {
				return true
			}
		}
	}
	return false
}

// count is the pack's distinct regular episodes in the series' own season
// under order o: each episode a member is assigned to, each one a
// provisional claim resolves to through o and no member is assigned to, both
// only where the own order places them in season, and each claim of season
// that o places at no single aired episode and no assigned member claims.
// Members labelled specials or assigned to one are left out.
func (s *series) count(o int, names []Name, ident []int, season int) int {
	t := packCount{
		series: s, order: &s.orders[o], season: season,
		assigned: make(map[int]bool), assignedClaims: make(map[place]bool),
		episodes: make(map[int]bool), numbers: make(map[place]bool),
	}
	for i, e := range ident {
		if e != unassigned {
			t.assigned[e] = true
			for _, c := range names[i].Claims {
				t.assignedClaims[placeOf(c)] = true
			}
		}
	}
	for i, e := range ident {
		switch {
		case names[i].Special || e != unassigned && s.episodes[e].special():
		case e != unassigned:
			t.episode(e)
		default:
			for _, c := range names[i].Claims {
				t.claim(placeOf(c))
			}
		}
	}
	return len(t.episodes) + len(t.numbers)
}

// packCount is one count's distinct episodes and unplaced numbers.
type packCount struct {
	series         *series
	order          *order
	assigned       map[int]bool
	assignedClaims map[place]bool
	episodes       map[int]bool
	numbers        map[place]bool
	season         int
}

func (t *packCount) episode(e int) {
	if t.series.ownSeason(e, t.season) {
		t.episodes[e] = true
	}
}

// claim counts a provisional claim at p: the episode the order places there
// unless a member is assigned to it, else the number itself in the season
// when no assigned member claims it.
func (t *packCount) claim(p place) {
	if x, ok := t.order.single(p); ok {
		if !t.assigned[x] {
			t.episode(x)
		}
	} else if p.season == t.season && !t.assignedClaims[p] {
		t.numbers[p] = true
	}
}

// seasonItems counts the root's items the series' own order places in season.
func seasonItems(items []mediasource.Item, season int) int {
	n := 0
	for i := range items {
		if items[i].SeriesNumbering.Season == season {
			n++
		}
	}
	return n
}
