package skyhook

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cplieger/subflux/internal/media"
)

// MaxBody bounds one answer; a larger one is refused, never cut.
const MaxBody = 16 << 20

// Show is the decoded subset of one answer.
type Show struct {
	Orders   []Order // the answer's seasonTypes
	Episodes []Episode
}

// Order is one of the series' episode orders.
type Order struct{ Type, Name string }

// Episode is one episode where the requested order places it. ID passes
// media.ValidExternalID and each number media.ValidEpisodeNumber.
type Episode struct {
	AirDate, Title           string // "" when SkyHook sends null
	ID                       uint64 // the TVDB episode id
	Season, Number, Absolute int    // Absolute 0 when SkyHook sends none
}

// wireEpisode reads the fields Sonarr's own SkyHook resource reads
// (NzbDrone.Core/MetadataSource/SkyHook/Resource/EpisodeResource.cs).
type wireEpisode struct {
	Season   *int    `json:"seasonNumber"`
	Number   *int    `json:"episodeNumber"`
	Absolute *int    `json:"absoluteEpisodeNumber"`
	AirDate  *string `json:"airDate"`
	Title    *string `json:"title"`
	ID       uint64  `json:"tvdbId"`
}

type wireShow struct {
	SeasonTypes []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"seasonTypes"`
	Episodes []wireEpisode `json:"episodes"`
}

func decode(body []byte) (Show, error) {
	var w wireShow
	if err := json.Unmarshal(body, &w); err != nil {
		return Show{}, decodeError(err)
	}
	s := Show{Orders: make([]Order, len(w.SeasonTypes)), Episodes: make([]Episode, len(w.Episodes))}
	for i, t := range w.SeasonTypes {
		s.Orders[i] = Order{Type: t.Type, Name: t.Name}
	}
	for i, e := range w.Episodes {
		ep, err := e.episode()
		if err != nil {
			return Show{}, fmt.Errorf("episode %d: %w", i, err)
		}
		s.Episodes[i] = ep
	}
	return s, nil
}

func (e *wireEpisode) episode() (Episode, error) {
	switch {
	case !media.ValidExternalID(e.ID):
		return Episode{}, fmt.Errorf("tvdbId %d is out of range", e.ID)
	case e.Season == nil || e.Number == nil:
		return Episode{}, errors.New("no season or episode number")
	case !media.ValidEpisodeNumber(*e.Season) || !media.ValidEpisodeNumber(*e.Number):
		return Episode{}, fmt.Errorf("season %d, episode %d is out of range", *e.Season, *e.Number)
	case e.Absolute != nil && !media.ValidEpisodeNumber(*e.Absolute):
		return Episode{}, fmt.Errorf("absolute number %d is out of range", *e.Absolute)
	}
	return Episode{
		AirDate:  deref(e.AirDate),
		Title:    deref(e.Title),
		ID:       e.ID,
		Season:   *e.Season,
		Number:   *e.Number,
		Absolute: deref(e.Absolute),
	}, nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// decodeError names where the answer broke, never what it held, so no
// SkyHook text reaches a log line.
func decodeError(err error) error {
	var (
		syntax *json.SyntaxError
		typ    *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &syntax):
		return fmt.Errorf("the answer is not JSON at byte %d", syntax.Offset)
	case errors.As(err, &typ):
		return fmt.Errorf("the answer's %s is not a %s", typ.Field, typ.Type)
	}
	return errors.New("the answer is not a SkyHook show")
}
