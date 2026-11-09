package seriesorder

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"

	"github.com/cplieger/subflux/internal/mediasource"
)

// placeholderTitle is Sonarr's title for one SkyHook leaves null
// (NzbDrone.Core/Tv/RefreshEpisodeService.cs:86), agreeing with an absent one.
const placeholderTitle = "TBA"

type place struct{ season, episode int }

// listing is a JoinQuery prepared for the agreement checks of design 4.4.
type listing struct {
	q    *mediasource.JoinQuery
	lang string // the title language the source's titles are in
	hash [32]byte
}

func newListing(q *mediasource.JoinQuery) listing {
	return listing{q: q, lang: metadataCode(q.Language), hash: listHash(q)}
}

// listHash is the list fact's key: the series, its order, its language and
// every listed episode, each string length-prefixed so no two lists collide.
func listHash(q *mediasource.JoinQuery) [32]byte {
	h := sha256.New()
	var b []byte
	str := func(s string) { b = binary.AppendUvarint(b, uint64(len(s))); b = append(b, s...) }
	num := func(n int) { b = binary.AppendVarint(b, int64(n)) }
	b = binary.AppendUvarint(b, q.TVDB)
	str(q.Order.Type)
	str(q.Order.Name)
	if q.Order.Aired {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	str(q.Language.String())
	for _, e := range q.Episodes {
		num(e.Season)
		num(e.Episode)
		num(e.Absolute)
		str(e.AirDate)
		str(e.Title)
	}
	h.Write(b)
	return [32]byte(h.Sum(nil))
}

// readingIndex is one reading's entries at each place of the listing's order.
type readingIndex struct {
	r       *mediasource.SeriesEvidence
	titles  *mediasource.TitleSet // nil: the reading holds none in the listing's language
	aired   map[place][]int       // indexes into Catalogue.Episodes
	extra   map[place][]string    // the air dates of Extra entries
	noDates bool
	known   bool // the reading holds the listing's order
}

func (l *listing) index(r *mediasource.SeriesEvidence) readingIndex {
	c := &r.Catalogue
	ix := readingIndex{r: r, aired: map[place][]int{}, extra: map[place][]string{}, noDates: true}
	if i := slices.IndexFunc(r.Titles, func(t mediasource.TitleSet) bool { return t.Language == l.lang }); i >= 0 {
		ix.titles = &r.Titles[i]
	}
	for _, e := range c.Episodes {
		ix.noDates = ix.noDates && e.AirDate == ""
	}
	for _, o := range c.Orders {
		for _, x := range o.Extra {
			ix.noDates = ix.noDates && x.AirDate == ""
		}
	}
	if l.q.Order.Aired {
		ix.known = true
		for i, e := range c.Episodes {
			p := place{e.Season, e.Episode}
			ix.aired[p] = append(ix.aired[p], i)
		}
		return ix
	}
	o := slices.IndexFunc(c.Orders, func(o mediasource.CatalogueOrder) bool { return o.Type == l.q.Order.Type })
	if o < 0 {
		return ix
	}
	ix.known = true
	for i, p := range c.Orders[o].Places {
		if p.Listed {
			k := place{p.Season, p.Episode}
			ix.aired[k] = append(ix.aired[k], i)
		}
	}
	for _, x := range c.Orders[o].Extra {
		k := place{x.Season, x.Episode}
		ix.extra[k] = append(ix.extra[k], x.AirDate)
	}
	return ix
}

func (ix *readingIndex) dateOK(listed, held string) bool { return ix.noDates || listed == held }

// titleOK is check 3: the title set holds the listed title, or none while
// the source lists Sonarr's placeholder.
func (ix *readingIndex) titleOK(id uint64, listed string) bool {
	ts := ix.titles.Titles
	i, found := slices.BinarySearchFunc(ts, id, func(t mediasource.EpisodeTitle, id uint64) int {
		switch {
		case t.ID < id:
			return -1
		case t.ID > id:
			return 1
		}
		return 0
	})
	if found {
		return ts[i].Title == listed
	}
	return listed == placeholderTitle
}

// agrees is design 4.4's three checks for one listed episode.
func (ix *readingIndex) agrees(l *listing, e *mediasource.Listed) bool {
	if !ix.known || (!l.q.Order.Aired && ix.titles == nil) {
		return false
	}
	p := place{e.Season, e.Episode}
	for _, i := range ix.aired[p] {
		ep := &ix.r.Catalogue.Episodes[i]
		if ix.dateOK(e.AirDate, ep.AirDate) && (l.q.Order.Aired || ix.titleOK(ep.ID, e.Title)) {
			return true
		}
	}
	return slices.ContainsFunc(ix.extra[p], func(date string) bool { return ix.dateOK(e.AirDate, date) })
}

// disagreeing counts the listed episodes the reading does not agree with;
// with no reading, every one.
func (l *listing) disagreeing(r *mediasource.SeriesEvidence) int {
	if r == nil {
		return len(l.q.Episodes)
	}
	ix := l.index(r)
	n := 0
	for i := range l.q.Episodes {
		if !ix.agrees(l, &l.q.Episodes[i]) {
			n++
		}
	}
	return n
}

// joined answers each listed episode from r, which it agrees with: under the
// aired order its own place, else the one aired episode holding its place,
// OK false when two do or only an Extra entry does.
func (l *listing) joined(r *mediasource.SeriesEvidence) (out []mediasource.Joined, unplaced int) {
	out = make([]mediasource.Joined, len(l.q.Episodes))
	if l.q.Order.Aired {
		for i, e := range l.q.Episodes {
			out[i] = mediasource.Joined{Season: e.Season, Episode: e.Episode, Absolute: e.Absolute, OK: true}
		}
		return out, 0
	}
	ix := l.index(r)
	for i, e := range l.q.Episodes {
		p := place{e.Season, e.Episode}
		if at := ix.aired[p]; len(at) == 1 && len(ix.extra[p]) == 0 {
			a := r.Catalogue.Episodes[at[0]]
			out[i] = mediasource.Joined{Season: a.Season, Episode: a.Episode, Absolute: a.Absolute, OK: true}
			continue
		}
		unplaced++
	}
	return out, unplaced
}
