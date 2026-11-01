package mediasource

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/media"
)

// officialOrder is SkyHook's and Sonarr's code for TVDB's aired order, which
// Catalogue.Episodes holds; absoluteOrder is a numbering, not another order.
const (
	officialOrder = "official"
	absoluteOrder = "absolute"
)

// Catalogue is one SkyHook reading of a TVDB series' episode orders. Places
// and air dates are the same in every language, so it holds no title.
type Catalogue struct {
	// Fetched is when SkyHook answered this reading's order requests.
	Fetched time.Time
	// Orders lists every order but the aired one and "absolute".
	Orders []CatalogueOrder
	// Episodes are the aired order, SkyHook's "official" answer.
	Episodes []CatalogueEpisode
	TVDB     uint64
}

// CatalogueOrder is one order's places for the catalogue's episodes.
type CatalogueOrder struct {
	Type, Name string
	// Places is parallel to Catalogue.Episodes.
	Places []Place
	// Extra are the order's episodes the aired list lacks, ascending by season
	// and episode.
	Extra []ExtraPlace
}

// Place is an episode's season and episode in one order; Listed is false
// when the order leaves the episode out.
type Place struct {
	Season, Episode int
	Listed          bool
}

// ExtraPlace is an order's place for an episode the aired list lacks; such an
// episode can agree with a source's list but has no aired key.
type ExtraPlace struct {
	// AirDate is YYYY-MM-DD, or "" when SkyHook lists none.
	AirDate         string
	Season, Episode int
}

// CatalogueEpisode is one episode of the aired order.
type CatalogueEpisode struct {
	// AirDate is YYYY-MM-DD, or "" when SkyHook lists none.
	AirDate string
	// ID is the TVDB episode id, SkyHook's metadataId, distinct in a catalogue.
	ID uint64
	// Season, Episode and Absolute are aired; Absolute is 0 when none.
	Season, Episode, Absolute int
}

// TitleSet is one language's episode titles of a reading; an episode with no
// title in Language is absent.
type TitleSet struct {
	// Language is an ISO 639-2/T code.
	Language string
	// Titles ascend by ID.
	Titles []EpisodeTitle
	// Found is false when SkyHook answered 404 for Language.
	Found bool
}

// EpisodeTitle is one episode's title in a TitleSet's language.
type EpisodeTitle struct {
	Title string
	ID    uint64
}

// SeriesEvidence is one reading, never a mix: its catalogue and its title
// sets, ascending by Language, each holding the catalogue's aired episodes.
type SeriesEvidence struct {
	Titles    []TitleSet
	Catalogue Catalogue
}

// RootEvidence is one root's evidence: Bound, the chosen reading (under any
// order but the aired one, the reading its stored keys came from), and Held,
// a later reading its source's list has not agreed with, nil when none.
type RootEvidence struct {
	Held  *SeriesEvidence
	Bound SeriesEvidence
}

// moved counts the regular episodes (season above 0 in both orders) that
// Orders[order] lists at another place than aired.
func (c *Catalogue) moved(order int) int {
	n := 0
	for i, p := range c.Orders[order].Places {
		e := c.Episodes[i]
		if p.Listed && p.Season > 0 && e.Season > 0 && (p.Season != e.Season || p.Episode != e.Episode) {
			n++
		}
	}
	return n
}

// Reordered is the order index of a catalogue Validate accepts: some order
// moves a regular episode. Whether an order is listed says nothing; most DVD
// orders move none.
func (c Catalogue) Reordered() bool { //nolint:gocritic // hugeParam: Catalogue is a value type, read where it is stored
	for i := range c.Orders {
		if c.moved(i) > 0 {
			return true
		}
	}
	return false
}

// Validate refuses a series or episode id [media.ValidExternalID] refuses, a
// repeated episode id, a season or episode [media.ValidEpisodeNumber]
// refuses, an air date neither "" nor YYYY-MM-DD, a repeated or empty order
// type, an "official" or "absolute" one, Places not parallel to Episodes, and
// Extra out of order. Two Extra entries may share a place, as
// two Places may.
func (c Catalogue) Validate() error { return refusal(c.validate()) } //nolint:gocritic // hugeParam: Catalogue is a value type, checked where it is decoded

// Validate refuses a Language that is not three ASCII lowercase letters, a
// title when Found is false, an empty title, and ids [media.ValidExternalID]
// refuses or not strictly ascending.
func (t TitleSet) Validate() error { return refusal(t.validate()) }

// Validate refuses an invalid reading, title sets repeated or out of order by
// Language, a title for an id its catalogue lacks, and a Held for another
// TVDB id or fetched no later than Bound.
func (r RootEvidence) Validate() error { //nolint:gocritic // hugeParam: RootEvidence is a value type, checked where it is decoded
	if err := r.Bound.validate(); err != nil {
		return refusal(fmt.Errorf("bound reading: %w", err))
	}
	if r.Held == nil {
		return nil
	}
	if err := r.Held.validate(); err != nil {
		return refusal(fmt.Errorf("held reading: %w", err))
	}
	switch bound, held := r.Bound.Catalogue, r.Held.Catalogue; {
	case held.TVDB != bound.TVDB:
		return refusal(fmt.Errorf("held reading is of series %d, bound of %d", held.TVDB, bound.TVDB))
	case !held.Fetched.After(bound.Fetched):
		return refusal(errors.New("held reading was fetched no later than the bound one"))
	}
	return nil
}

func refusal(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("mediasource: %w", err)
}

func (c *Catalogue) validate() error {
	if !media.ValidExternalID(c.TVDB) {
		return fmt.Errorf("series id %d is outside 1 to 2^53-1", c.TVDB)
	}
	ids := make(map[uint64]bool, len(c.Episodes))
	for i, e := range c.Episodes {
		switch {
		case !media.ValidExternalID(e.ID):
			return fmt.Errorf("episode %d: id %d is outside 1 to 2^53-1", i, e.ID)
		case ids[e.ID]:
			return fmt.Errorf("episode %d is listed twice", e.ID)
		case !validPlace(e.Season, e.Episode):
			return fmt.Errorf("episode %d: %w", e.ID, placeError(e.Season, e.Episode))
		case !validAirDate(e.AirDate):
			return fmt.Errorf("episode %d: air date %q is not YYYY-MM-DD", e.ID, e.AirDate)
		}
		ids[e.ID] = true
	}
	types := make(map[string]bool, len(c.Orders))
	for i := range c.Orders {
		o := &c.Orders[i]
		switch {
		case o.Type == "" || o.Type == officialOrder || o.Type == absoluteOrder:
			return fmt.Errorf("order type %q is not an order besides the aired one", o.Type)
		case types[o.Type]:
			return fmt.Errorf("order %q is listed twice", o.Type)
		}
		types[o.Type] = true
		if err := o.validate(len(c.Episodes)); err != nil {
			return fmt.Errorf("order %q: %w", o.Type, err)
		}
	}
	return nil
}

func (o *CatalogueOrder) validate(episodes int) error {
	if len(o.Places) != episodes {
		return fmt.Errorf("%d places for %d episodes", len(o.Places), episodes)
	}
	for _, p := range o.Places {
		if !validPlace(p.Season, p.Episode) {
			return placeError(p.Season, p.Episode)
		}
	}
	for i, x := range o.Extra {
		switch {
		case !validPlace(x.Season, x.Episode):
			return fmt.Errorf("extra place: %w", placeError(x.Season, x.Episode))
		case !validAirDate(x.AirDate):
			return fmt.Errorf("extra place S%02dE%02d: air date %q is not YYYY-MM-DD", x.Season, x.Episode, x.AirDate)
		case i > 0 && cmpExtra(o.Extra[i-1], x) > 0:
			return fmt.Errorf("extra place S%02dE%02d is listed after S%02dE%02d", x.Season, x.Episode, o.Extra[i-1].Season, o.Extra[i-1].Episode)
		}
	}
	return nil
}

func (t TitleSet) validate() error {
	if !iso3Code(t.Language) {
		return fmt.Errorf("title language %q is not three lowercase letters", t.Language)
	}
	if !t.Found && len(t.Titles) > 0 {
		return fmt.Errorf("title set %s holds titles SkyHook did not find", t.Language)
	}
	for i, e := range t.Titles {
		switch {
		case !media.ValidExternalID(e.ID):
			return fmt.Errorf("title set %s: id %d is outside 1 to 2^53-1", t.Language, e.ID)
		case i > 0 && t.Titles[i-1].ID >= e.ID:
			return fmt.Errorf("title set %s: id %d follows %d", t.Language, e.ID, t.Titles[i-1].ID)
		case e.Title == "":
			return fmt.Errorf("title set %s: episode %d has an empty title", t.Language, e.ID)
		}
	}
	return nil
}

func (s *SeriesEvidence) validate() error {
	if err := s.Catalogue.validate(); err != nil {
		return err
	}
	ids := make(map[uint64]bool, len(s.Catalogue.Episodes))
	for _, e := range s.Catalogue.Episodes {
		ids[e.ID] = true
	}
	for i, t := range s.Titles {
		if err := t.validate(); err != nil {
			return err
		}
		if i > 0 && s.Titles[i-1].Language >= t.Language {
			return fmt.Errorf("title set %s follows %s", t.Language, s.Titles[i-1].Language)
		}
		for _, e := range t.Titles {
			if !ids[e.ID] {
				return fmt.Errorf("title set %s titles episode %d, which the catalogue lacks", t.Language, e.ID)
			}
		}
	}
	return nil
}

func validPlace(season, episode int) bool {
	return media.ValidEpisodeNumber(season) && media.ValidEpisodeNumber(episode)
}

func placeError(season, episode int) error {
	return fmt.Errorf("season %d episode %d is outside 0 to 65,535", season, episode)
}

func validAirDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

func cmpExtra(a, b ExtraPlace) int {
	return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode))
}

func iso3Code(s string) bool {
	return len(s) == 3 && !slices.ContainsFunc([]byte(s), func(c byte) bool { return c < 'a' || c > 'z' })
}
