package episodematch

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// skyhookList is testdata/skyhook/<tvdb>.json, SkyHook's recorded English
// answers reduced to what a reading holds: the aired episodes as [id,
// season, episode, absolute, title] and every other order as [id, season,
// episode].
type skyhookList struct {
	Orders   map[string][][3]uint64 `json:"orders"`
	Title    string                 `json:"title"`
	Episodes [][5]any               `json:"episodes"`
	TVDB     uint64                 `json:"tvdb"`
}

// recorded is a series read from its recorded SkyHook lists: the reading
// and, as the source lists them in the order own names, its items.
func recorded(t testing.TB, tvdb int, own string) Series {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("testdata/skyhook/%d.json", tvdb))
	if err != nil {
		t.Fatal(err)
	}
	var l skyhookList
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	c := mediasource.Catalogue{TVDB: l.TVDB}
	titles := mediasource.TitleSet{Language: "eng", Found: true}
	index := map[uint64]int{}
	for i, e := range l.Episodes {
		id := uint64(e[0].(float64))
		index[id] = i
		c.Episodes = append(c.Episodes, mediasource.CatalogueEpisode{ID: id, Season: int(e[1].(float64)), Episode: int(e[2].(float64)), Absolute: int(e[3].(float64))})
		if title := e[4].(string); title != "" {
			titles.Titles = append(titles.Titles, mediasource.EpisodeTitle{ID: id, Title: title})
		}
	}
	slices.SortFunc(titles.Titles, func(a, b mediasource.EpisodeTitle) int { return cmp.Compare(a.ID, b.ID) })
	for _, typ := range slices.Sorted(maps.Keys(l.Orders)) {
		o := mediasource.CatalogueOrder{Type: typ, Name: typ, Places: make([]mediasource.Place, len(c.Episodes))}
		for _, x := range l.Orders[typ] {
			if i, ok := index[x[0]]; ok {
				o.Places[i] = mediasource.Place{Season: int(x[1]), Episode: int(x[2]), Listed: true}
			} else {
				o.Extra = append(o.Extra, mediasource.ExtraPlace{Season: int(x[1]), Episode: int(x[2])})
			}
		}
		slices.SortFunc(o.Extra, func(a, b mediasource.ExtraPlace) int {
			return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
		})
		c.Orders = append(c.Orders, o)
	}
	ev := mediasource.SeriesEvidence{Catalogue: c, Titles: []mediasource.TitleSet{titles}}
	if err := (mediasource.RootEvidence{Bound: ev}).Validate(); err != nil {
		t.Fatal(err)
	}
	s := Series{Reading: &ev, Languages: []string{"eng"}, Order: mediasource.Order{Type: own, Name: own, Aired: own == "official"}}
	ownAt := func(i int) (mediasource.Place, bool) {
		e := c.Episodes[i]
		if own == "official" {
			return mediasource.Place{Season: e.Season, Episode: e.Episode, Listed: true}, true
		}
		o := c.Orders[slices.IndexFunc(c.Orders, func(o mediasource.CatalogueOrder) bool { return o.Type == own })]
		return o.Places[i], o.Places[i].Listed
	}
	for i, e := range c.Episodes {
		p, ok := ownAt(i)
		if !ok {
			continue
		}
		s.Items = append(s.Items, mediasource.Item{
			Title:           l.Episodes[i][4].(string),
			Numbering:       media.Numbering{Scheme: media.TVDB, Season: e.Season, Episode: e.Episode},
			SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: p.Season, Episode: p.Episode},
			Absolute:        e.Absolute,
		})
	}
	return s
}

func item(season, episode int, title string) mediasource.Item {
	n := media.Numbering{Scheme: media.TVDB, Season: season, Episode: episode}
	return mediasource.Item{Title: title, Numbering: n, SeriesNumbering: n}
}

// synthetic is a series whose reading lists eps in aired order and orders
// by type, one place per episode; own names the series' own order.
func synthetic(t testing.TB, own string, eps []mediasource.Item, orders map[string][][2]int) Series {
	t.Helper()
	c := mediasource.Catalogue{TVDB: 1}
	titles := mediasource.TitleSet{Language: "eng", Found: true}
	for i, e := range eps {
		id := uint64(1000 + i)
		c.Episodes = append(c.Episodes, mediasource.CatalogueEpisode{ID: id, Season: e.Numbering.Season, Episode: e.Numbering.Episode})
		titles.Titles = append(titles.Titles, mediasource.EpisodeTitle{ID: id, Title: e.Title})
	}
	for _, typ := range slices.Sorted(maps.Keys(orders)) {
		o := mediasource.CatalogueOrder{Type: typ, Name: typ}
		for _, p := range orders[typ] {
			o.Places = append(o.Places, mediasource.Place{Season: p[0], Episode: p[1], Listed: true})
		}
		c.Orders = append(c.Orders, o)
	}
	ev := mediasource.SeriesEvidence{Catalogue: c, Titles: []mediasource.TitleSet{titles}}
	if err := (mediasource.RootEvidence{Bound: ev}).Validate(); err != nil {
		t.Fatal(err)
	}
	s := Series{Reading: &ev, Languages: []string{"eng"}, Order: mediasource.Order{Type: own, Aired: own == "official"}}
	for i, e := range eps {
		if own != "official" {
			p := orders[own][i]
			e.SeriesNumbering.Season, e.SeriesNumbering.Episode = p[0], p[1]
		}
		s.Items = append(s.Items, e)
	}
	return s
}

// itemAt is the index of the item the series' own order places at season
// and episode.
func itemAt(t testing.TB, s *Series, season, episode int) int {
	t.Helper()
	i := slices.IndexFunc(s.Items, func(it mediasource.Item) bool {
		return it.SeriesNumbering.Season == season && it.SeriesNumbering.Episode == episode
	})
	if i < 0 {
		t.Fatalf("no item at S%02dE%02d", season, episode)
	}
	return i
}

// members are srt members of a pack, their flags read from an ".sdh" or
// ".forced" suffix.
func members(names ...string) []Member {
	out := make([]Member, len(names))
	for i, n := range names {
		out[i] = Member{Name: n, Flags: language.Flags{HI: strings.Contains(n, ".sdh."), Forced: strings.Contains(n, ".forced.")}}
	}
	return out
}
