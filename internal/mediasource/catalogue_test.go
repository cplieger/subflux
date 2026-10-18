package mediasource

import (
	"strings"
	"testing"
	"time"
)

// catalogue lists n aired episodes of season 1 with ids 101 onward, and the
// orders given.
func catalogue(n int, orders ...CatalogueOrder) Catalogue {
	c := Catalogue{TVDB: 78874, Fetched: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), Orders: orders}
	for i := range n {
		c.Episodes = append(c.Episodes, CatalogueEpisode{ID: uint64(101 + i), Season: 1, Episode: i + 1, Absolute: i + 1, AirDate: "2002-09-20"})
	}
	return c
}

func dvd(places []Place) CatalogueOrder {
	return CatalogueOrder{Type: "dvd", Name: "DVD Order", Places: places}
}

// reversed places season 1's n episodes last to first, so every one moves
// when n is even.
func reversed(n int) []Place {
	p := make([]Place, n)
	for i := range p {
		p[i] = Place{Season: 1, Episode: n - i, Listed: true}
	}
	return p
}

func listed(n int) []Place {
	p := make([]Place, n)
	for i := range p {
		p[i] = Place{Season: 1, Episode: i + 1, Listed: true}
	}
	return p
}

func TestReorderedFromMovedNotListing(t *testing.T) {
	cases := []struct {
		name string
		c    Catalogue
		want bool
	}{
		{"a DVD order moving none (Game of Thrones)", catalogue(10, dvd(listed(10))), false},
		{"a DVD order moving 14 (Firefly)", catalogue(14, dvd(reversed(14))), true},
		{"no order but the aired one", catalogue(14), false},
		{"a second order moving 4", catalogue(4, dvd(listed(4)), CatalogueOrder{Type: "alternate", Places: reversed(4)}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.c.Reordered(); got != c.want {
				t.Errorf("Reordered() = %v; want %v", got, c.want)
			}
		})
	}
}

func TestMovedCountsRegularEpisodes(t *testing.T) {
	c := catalogue(6, dvd([]Place{
		{Season: 1, Episode: 2, Listed: true}, // moved
		{Season: 1, Episode: 1, Listed: true}, // moved
		{Season: 1, Episode: 3, Listed: true}, // where it aired
		{Season: 0, Episode: 1, Listed: true}, // a special in this order
		{Season: 1, Episode: 9},               // not listed
		{Season: 2, Episode: 1, Listed: true}, // moved
	}))
	c.Episodes[5].Season, c.Episodes[5].Episode = 0, 1 // an aired special
	if got := c.moved(0); got != 2 {
		t.Errorf("moved(dvd) = %d; want 2 (specials and unlisted episodes never count)", got)
	}
	firefly := catalogue(14, dvd(reversed(14)))
	if got := firefly.moved(0); got != 14 {
		t.Errorf("moved(Firefly's shape) = %d; want 14", got)
	}
}

func TestPlaceParallelToEpisodes(t *testing.T) {
	refused := map[string]func(*Catalogue){
		"an order with a place too few":     func(c *Catalogue) { c.Orders[0].Places = c.Orders[0].Places[:2] },
		"an order with a place too many":    func(c *Catalogue) { c.Orders[0].Places = append(c.Orders[0].Places, Place{}) },
		"episode id 0":                      func(c *Catalogue) { c.Episodes[0].ID = 0 },
		"episode id 2^53":                   func(c *Catalogue) { c.Episodes[1].ID = 1 << 53 },
		"series id 0":                       func(c *Catalogue) { c.TVDB = 0 },
		"aired season 65,536":               func(c *Catalogue) { c.Episodes[0].Season = 65536 },
		"aired episode -1":                  func(c *Catalogue) { c.Episodes[0].Episode = -1 },
		"an order's place at episode 65536": func(c *Catalogue) { c.Orders[0].Places[0].Episode = 65536 },
		"an order's place at season -1":     func(c *Catalogue) { c.Orders[0].Places[2].Season = -1 },
		"a repeated episode id":             func(c *Catalogue) { c.Episodes[2].ID = c.Episodes[0].ID },
		"a repeated order type":             func(c *Catalogue) { c.Orders = append(c.Orders, dvd(listed(3))) },
		"an official order":                 func(c *Catalogue) { c.Orders[0].Type = "official" },
		"an absolute order":                 func(c *Catalogue) { c.Orders[0].Type = "absolute" },
		"an empty order type":               func(c *Catalogue) { c.Orders[0].Type = "" },
		"an air date with a newline":        func(c *Catalogue) { c.Episodes[1].AirDate = "2002-09-20\n" },
		"an air date not YYYY-MM-DD":        func(c *Catalogue) { c.Episodes[1].AirDate = "2002-9-20" },
		"an air date of no day":             func(c *Catalogue) { c.Episodes[1].AirDate = "2002-02-30" },
	}
	for name, mutate := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			c := catalogue(3, dvd(listed(3)))
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("Validate() = nil; want a refusal")
			} else if !strings.HasPrefix(err.Error(), "mediasource: ") || strings.Contains(err.Error(), "\n") {
				t.Errorf("Validate() = %q; want one plain line", err)
			}
		})
	}
	t.Run("accepts the bounds and an unlisted place", func(t *testing.T) {
		c := catalogue(3, dvd([]Place{{Season: 65535, Episode: 0, Listed: true}, {}, {Season: 0, Episode: 65535, Listed: true}}))
		c.Episodes[2].ID = 1<<53 - 1
		c.Episodes[1].AirDate = ""
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v", err)
		}
	})
	t.Run("accepts no order but the aired one", func(t *testing.T) {
		if err := catalogue(3).Validate(); err != nil {
			t.Errorf("Validate() = %v", err)
		}
	})

	titles := func(found bool, ids ...uint64) TitleSet {
		s := TitleSet{Language: "eng", Found: found}
		for _, id := range ids {
			s.Titles = append(s.Titles, EpisodeTitle{ID: id, Title: "Serenity"})
		}
		return s
	}
	refusedTitles := map[string]TitleSet{
		"a two-letter code":        {Language: "en", Found: true},
		"an uppercase code":        {Language: "ENG", Found: true},
		"a code with a digit":      {Language: "e1g", Found: true},
		"titles under Found false": titles(false, 101),
		"id 0":                     titles(true, 0, 101),
		"id 2^53":                  titles(true, 1<<53),
		"ids out of order":         titles(true, 102, 101),
		"a repeated id":            titles(true, 101, 101),
		"an empty title":           {Language: "eng", Found: true, Titles: []EpisodeTitle{{ID: 101}}},
	}
	for name, s := range refusedTitles {
		t.Run("title set refuses "+name, func(t *testing.T) {
			if err := s.Validate(); err == nil {
				t.Errorf("Validate() = nil; want a refusal")
			}
		})
	}
	t.Run("title set accepts ascending ids and an empty 404", func(t *testing.T) {
		for _, s := range []TitleSet{titles(true, 101, 102, 1<<53-1), titles(false)} {
			if err := s.Validate(); err != nil {
				t.Errorf("Validate(%+v) = %v", s, err)
			}
		}
	})
}

func TestExtraPlacesValidated(t *testing.T) {
	office := func(extra ...ExtraPlace) Catalogue {
		o := dvd(listed(3))
		o.Extra = extra
		return catalogue(3, o)
	}
	var nine []ExtraPlace
	for i := range 9 {
		nine = append(nine, ExtraPlace{Season: 1 + i/4, Episode: 10 + i%4, AirDate: "2005-03-24"})
	}
	if err := office(nine...).Validate(); err != nil {
		t.Errorf("The Office's 9 DVD-only episodes: Validate() = %v", err)
	}
	if err := office(ExtraPlace{Season: 2, Episode: 1}, ExtraPlace{Season: 2, Episode: 1}).Validate(); err != nil {
		t.Errorf("two extras at one place: Validate() = %v", err)
	}
	refused := map[string][]ExtraPlace{
		"episodes out of order":      {{Season: 1, Episode: 11}, {Season: 1, Episode: 10}},
		"seasons out of order":       {{Season: 2, Episode: 1}, {Season: 1, Episode: 12}},
		"season 65,536":              {{Season: 65536, Episode: 1}},
		"episode -1":                 {{Season: 1, Episode: -1}},
		"an air date not YYYY-MM-DD": {{Season: 1, Episode: 10, AirDate: "24 March 2005"}},
	}
	for name, extra := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if err := office(extra...).Validate(); err == nil {
				t.Errorf("Validate() = nil; want a refusal")
			}
		})
	}
}

func TestRootEvidenceValidated(t *testing.T) {
	reading := func(fetched time.Time) SeriesEvidence {
		c := catalogue(3, dvd(listed(3)))
		c.Fetched = fetched
		return SeriesEvidence{Catalogue: c, Titles: []TitleSet{
			{Language: "eng", Found: true, Titles: []EpisodeTitle{{ID: 101, Title: "Serenity"}, {ID: 103, Title: "Bushwhacked"}}},
			{Language: "fra", Found: false},
		}}
	}
	t0 := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(7 * 24 * time.Hour)
	held := func(mutate func(*SeriesEvidence)) *SeriesEvidence {
		r := reading(t1)
		mutate(&r)
		return &r
	}
	accepted := map[string]RootEvidence{
		"no held reading":              {Bound: reading(t0)},
		"a held reading fetched later": {Bound: reading(t0), Held: held(func(*SeriesEvidence) {})},
		"no title sets":                {Bound: SeriesEvidence{Catalogue: catalogue(1)}},
	}
	for name, r := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			if err := r.Validate(); err != nil {
				t.Errorf("Validate() = %v", err)
			}
		})
	}
	refused := map[string]RootEvidence{
		"a title set with titles SkyHook did not find": {Bound: func() SeriesEvidence {
			r := reading(t0)
			r.Titles[1].Titles = []EpisodeTitle{{ID: 101, Title: "Serenity"}}
			return r
		}()},
		"a held reading of another series":            {Bound: reading(t0), Held: held(func(r *SeriesEvidence) { r.Catalogue.TVDB = 121 })},
		"a held reading fetched with the bound one":   {Bound: reading(t0), Held: held(func(r *SeriesEvidence) { r.Catalogue.Fetched = t0 })},
		"a held reading fetched before the bound one": {Bound: reading(t1), Held: held(func(r *SeriesEvidence) { r.Catalogue.Fetched = t0 })},
		"a repeated title language": {Bound: func() SeriesEvidence {
			r := reading(t0)
			r.Titles[1] = r.Titles[0]
			return r
		}()},
		"title languages out of order": {Bound: func() SeriesEvidence {
			r := reading(t0)
			r.Titles[0], r.Titles[1] = r.Titles[1], r.Titles[0]
			return r
		}()},
		"a title for an id the catalogue lacks": {Bound: func() SeriesEvidence {
			r := reading(t0)
			r.Titles[0].Titles[1].ID = 104
			return r
		}()},
		"an invalid bound catalogue": {Bound: func() SeriesEvidence {
			r := reading(t0)
			r.Catalogue.Orders[0].Places = nil
			return r
		}()},
		"an invalid held title set": {Bound: reading(t0), Held: held(func(r *SeriesEvidence) { r.Titles[0].Language = "en" })},
	}
	for name, r := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if err := r.Validate(); err == nil {
				t.Errorf("Validate() = nil; want a refusal")
			}
		})
	}
}
