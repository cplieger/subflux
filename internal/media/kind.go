// Package media is subflux's identity: the key an episode or movie is stored,
// matched and served by. A key derives from an external id (TVDB, TMDB or
// IMDb) and an episode's aired numbering, never from a source's own ids, so
// two sources naming one item agree on its key.
package media

import "math"

// Kind is what a key names.
type Kind uint8

// The kinds, from 1, so the zero Kind is none.
const (
	Episode Kind = iota + 1
	Movie
)

// Scheme is the external id a key is built from. None is a movie's numbering
// scheme, and an episode numbered under None has no key.
type Scheme uint8

// The schemes; IMDb is spelled imdb in text.
const (
	None Scheme = iota
	TVDB
	TMDB
	IMDb
)

// ExternalIDs are an item's ids per scheme; 0 is unknown. IMDb's is numeric:
// tt0111161 is 111161.
type ExternalIDs struct{ TVDB, TMDB, IMDb uint64 }

// ValidExternalID reports whether id lies in 1 to 2^53-1, the ids a key
// holds, so every id survives a round trip through a JavaScript number on the
// wire.
func ValidExternalID(id uint64) bool { return id >= 1 && id < 1<<53 }

// Numbering is an episode's season and episode and the Scheme whose id keys
// it; each number passes ValidEpisodeNumber. A key's numbering is TVDB's aired
// order. A movie's is zero and ignored.
type Numbering struct {
	Scheme          Scheme
	Season, Episode int
}

// ValidEpisodeNumber reports whether n lies in 0 to 65,535, the seasons and
// episodes a key holds; season 0 holds specials.
func ValidEpisodeNumber(n int) bool { return n >= 0 && n <= math.MaxUint16 }
