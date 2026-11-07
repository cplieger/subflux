package episodematch

import (
	"slices"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Member is one pack member: its name as the archive lists it, folders
// included, and the variant flags the caller read from it.
type Member struct {
	Name  string
	Flags language.Flags
}

// Input is one pack offered for one item and variant.
type Input struct {
	Series Series
	// Members are in archive order.
	Members []Member
	// Item is the index in Series.Items of the episode the pack is for.
	Item    int
	Variant language.Variant
	// Manual is a user's pick, which no count or undetermined order refuses:
	// past a title it takes the group the orders agree on, else the one the
	// series' own order names.
	Manual bool
	// NoReference is set once the reference comparison found no reference
	// track, so a disagreement between the orders refuses the pack.
	NoReference bool
}

// Refusal is why a pack gives the item no member.
type Refusal uint8

const (
	_ Refusal = iota
	// NoMember means no member is the item's, or none of them serves the
	// variant.
	NoMember
	// EpisodeCount means under every compatible order the pack's distinct regular
	// episodes differ from the items of the item's season.
	EpisodeCount
	// OrderUndetermined means no order is compatible with the pack, or the
	// compatible ones disagree at the item and there is no reference track.
	OrderUndetermined
)

// Choice is Select's answer: one member taken, members to compare against
// the reference track (the one with the highest confidence is taken), or a
// refusal.
type Choice struct {
	// Compare lists, in archive order, every serving member of each group the
	// disagreeing orders name.
	Compare []int
	// Member is the member taken, -1 when none.
	Member  int
	Refusal Refusal
	// ByNumber is set when a number, not a confident title, chose Member or
	// Compare.
	ByNumber bool
}

func taken(i int, byNumber bool) Choice { return Choice{Member: i, ByNumber: byNumber} }

func refused(r Refusal) Choice { return Choice{Member: -1, Refusal: r} }

// pack is one Select call's members as read: each name, and ident, each
// member's identity (an episode, else unassigned and identified by its
// claims).
type pack struct {
	names   []Name
	members []Member
	ident   []int
	target  int
	variant language.Variant
}

// Select runs design 8.11's steps over in, first matching step: a confident
// title (the only evidence for a special), the count under each compatible
// order, then the number the orders agree on, or the members they disagree
// over.
func Select(in *Input) Choice {
	item := &in.Series.Items[in.Item]
	s := newSeries(&in.Series, in.Item)
	target := s.items[in.Item]
	names := make([]string, len(in.Members))
	for i, m := range in.Members {
		names[i] = m.Name
	}
	p := pack{names: ReadPack(names, item.SeriesNumbering.Season), members: in.Members, variant: in.Variant, target: target}
	p.ident = s.assign(p.names)
	if i, ok := p.firstServing(p.titled()); ok {
		return taken(i, false)
	}
	if item.Numbering.Season == 0 || item.SeriesNumbering.Season == 0 {
		return refused(NoMember)
	}
	compat := s.compatible(p.names, p.ident)
	kept := p.counted(s, compat, in.Series.Items, item.SeriesNumbering.Season)
	switch {
	case in.Manual:
	case len(compat) == 0:
		return refused(OrderUndetermined)
	case len(kept) == 0:
		return refused(EpisodeCount)
	}
	if s.index == indexFalse {
		return p.take(p.group(s.episodes[target].aired))
	}
	groups, agree := p.named(s, kept)
	switch {
	case agree:
		return p.take(groups[0])
	case in.Manual:
		return p.take(p.group(place{item.SeriesNumbering.Season, item.SeriesNumbering.Episode}))
	case len(groups) == 0:
		return refused(NoMember)
	case in.NoReference:
		return refused(OrderUndetermined)
	}
	return p.compare(groups)
}

// counted keeps the orders under which the pack's count equals the root's
// items in season.
func (p *pack) counted(s *series, orders []int, items []mediasource.Item, season int) []int {
	want := seasonItems(items, season)
	var kept []int
	for _, o := range orders {
		if s.count(o, p.names, p.ident, season) == want {
			kept = append(kept, o)
		}
	}
	return kept
}

// compare answers every serving member of groups, in archive order.
func (p *pack) compare(groups [][]int) Choice {
	var compare []int
	for i := range p.members {
		if p.serves(i) && slices.ContainsFunc(groups, func(g []int) bool { return slices.Contains(g, i) }) {
			compare = append(compare, i)
		}
	}
	if compare == nil {
		return refused(NoMember)
	}
	return Choice{Member: -1, Compare: compare, ByNumber: true}
}

func (p *pack) serves(i int) bool { return p.members[i].Flags.Serves(p.variant) }

func (p *pack) titled() []int {
	var out []int
	for i, e := range p.ident {
		if e == p.target {
			out = append(out, i)
		}
	}
	return out
}

func (p *pack) firstServing(members []int) (int, bool) {
	i := slices.IndexFunc(members, p.serves)
	if i < 0 {
		return 0, false
	}
	return members[i], true
}

// group is the members claiming at, less every member assigned to another
// episode than the item's.
func (p *pack) group(at place) []int {
	var out []int
	for i, n := range p.names {
		if (p.ident[i] == unassigned || p.ident[i] == p.target) &&
			slices.ContainsFunc(n.Claims, func(c epmarker.Marker) bool { return placeOf(c) == at }) {
			out = append(out, i)
		}
	}
	return out
}

func (p *pack) take(group []int) Choice {
	if i, ok := p.firstServing(group); ok {
		return taken(i, true)
	}
	return refused(NoMember)
}

// named returns the distinct non-empty groups the orders listing the item's
// episode name at its place in each, and whether every one of those orders
// names the same group.
func (p *pack) named(s *series, orders []int) (groups [][]int, agree bool) {
	none := false
	for _, o := range orders {
		at := s.orders[o].places[p.target]
		if !at.Listed {
			continue
		}
		g := p.group(place{at.Season, at.Episode})
		switch {
		case g == nil:
			none = true
		case !slices.ContainsFunc(groups, func(h []int) bool { return slices.Equal(h, g) }):
			groups = append(groups, g)
		}
	}
	return groups, len(groups) == 1 && !none
}

// Identity is what a single name says about the item it is offered for.
type Identity struct {
	// Refuted: its title is confidently another episode's, or, with no such
	// title, its numbers match none of the item's.
	Refuted bool
	// Titled: its title is confidently the item's episode.
	Titled bool
	// ByNumber: on a series whose numbers may follow another order, nothing
	// but its numbers says it is the item's episode.
	ByNumber bool
}

// Identify reads one release or file name offered for Items[item]. Its
// numbers match the item when a claim is its Numbering or SeriesNumbering,
// or, with no claim, the number it states with no season is its aired
// absolute number or either numbering's episode. On an index-false series
// the numbers are trusted and the title is never read.
func Identify(s *Series, item int, name string) Identity {
	it := &s.Items[item]
	n := ReadName(name)
	fits := numbersFit(it, &n, baseName(name))
	ser := newSeries(s, item)
	if ser.index == indexFalse {
		return Identity{Refuted: !fits}
	}
	switch e := ser.assign([]Name{n})[0]; e {
	case ser.items[item]:
		return Identity{Titled: true}
	case unassigned:
		return Identity{Refuted: !fits, ByNumber: fits}
	}
	return Identity{Refuted: true}
}

func numbersFit(it *mediasource.Item, n *Name, base string) bool {
	if len(n.Claims) > 0 {
		return slices.ContainsFunc(n.Claims, func(c epmarker.Marker) bool {
			p := placeOf(c)
			return p == place{it.Numbering.Season, it.Numbering.Episode} ||
				p == place{it.SeriesNumbering.Season, it.SeriesNumbering.Episode}
		})
	}
	a, ok := epmarker.Absolute(base)
	return !ok || it.Absolute > 0 && a == it.Absolute || a == it.Numbering.Episode || a == it.SeriesNumbering.Episode
}
