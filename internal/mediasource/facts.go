// Package mediasource is what subflux learns from its media sources (Sonarr,
// Radarr): the facts a source reports about its series and movies, its change
// cursor, and the errors every source call answers with.
package mediasource

import (
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// NativeID is a source's own id for a root or item: 1 to 64 bytes of
// [A-Za-z0-9._:-], canonical decimal on Sonarr and Radarr. It is never
// identity; media.Key is.
type NativeID string

// NativeRef names a root in its source.
type NativeRef struct {
	ID   NativeID
	Kind media.Kind
}

// TagRef is a source's own id for one of its tags.
type TagRef NativeID

// Root is a series or a movie as its source reports it.
type Root struct {
	// Evidence is the root's SkyHook evidence from the aired join, nil when
	// none.
	Evidence  *RootEvidence
	AltTitles []string
	Title     string
	// Link is the root's page in the source's web UI, under public_url, else
	// url: an absolute http or https URL, or "".
	Link string
	// Language is the series' metadata language, zero when the source reports
	// none; the aired join asks SkyHook in it.
	Language language.Tag
	Order    Order
	Native   NativeRef
	External media.ExternalIDs
	// Year is the series' or movie's year, 0 when the source gives none.
	Year int
}

// Order is a series' episode order as its source names it; a movie's is
// zero. Name is "" while the source names none, and Aired means the order's
// numbers are TVDB's aired order.
type Order struct {
	Type, Name string
	Aired      bool
}

// Item is one episode or movie of a Root, whose kind it shares.
type Item struct {
	// AudioLangs are the file's audio tracks, in track order.
	AudioLangs []language.Tag
	Tags       []TagRef
	Native     NativeID
	Title      string
	// AirDate is YYYY-MM-DD as the source lists it; "" while unaired.
	AirDate              string
	VideoPath, SceneName string
	OriginalLang         language.Tag
	External             media.ExternalIDs
	// Numbering is the episode's place in TVDB's aired order, the one its key
	// is chosen by; the Set sets it from the aired join, overwriting any
	// adapter's value.
	Numbering media.Numbering
	// SeriesNumbering is the episode's place in Root.Order, as the source
	// lists and names it; it equals Numbering under the aired order.
	SeriesNumbering media.Numbering
	// Absolute is the aired absolute number, 0 when none: the source's own
	// under the aired order, the join's under any other.
	Absolute int
	// Excluded is set by the Set alone, from the source's exclude tags.
	Excluded bool
}

// Change names a root whose items may have changed, a deletion included.
type Change struct{ Root NativeRef }

// Capabilities are what a source can do beyond listing its library.
type Capabilities struct {
	Kinds                          []media.Kind
	Changes, Rescan, Tags, Posters bool
}
