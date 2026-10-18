package media

import (
	"errors"
	"fmt"
)

var (
	kindNames   = [...]string{Episode: "episode", Movie: "movie"}
	schemeNames = [...]string{None: "none", TVDB: "tvdb", TMDB: "tmdb", IMDb: "imdb"}
)

var errZeroKey = errors.New("media: the zero key has no text")

// MarshalText is String; the zero Key, which UnmarshalText would refuse, is
// an error rather than "".
func (k Key) MarshalText() ([]byte, error) {
	if k == (Key{}) {
		return nil, errZeroKey
	}
	return []byte(k.String()), nil
}

// UnmarshalText is ParseKey, leaving k unchanged on a refusal.
func (k *Key) UnmarshalText(b []byte) error {
	v, err := ParseKey(string(b))
	if err == nil {
		*k = v
	}
	return err
}

// MarshalText is String; the zero RootKey is an error rather than "".
func (r RootKey) MarshalText() ([]byte, error) {
	if r == (RootKey{}) {
		return nil, errZeroKey
	}
	return []byte(r.String()), nil
}

// UnmarshalText is ParseRootKey, leaving r unchanged on a refusal.
func (r *RootKey) UnmarshalText(b []byte) error {
	v, err := ParseRootKey(string(b))
	if err == nil {
		*r = v
	}
	return err
}

// MarshalText is episode or movie.
func (k Kind) MarshalText() ([]byte, error) { return marshalName(kindNames[:], "kind", k, Episode) }

// UnmarshalText refuses every spelling but MarshalText's.
func (k *Kind) UnmarshalText(b []byte) error {
	return unmarshalName(kindNames[:], "kind", b, k, Episode)
}

// MarshalText is none, tvdb, tmdb or imdb.
func (s Scheme) MarshalText() ([]byte, error) { return marshalName(schemeNames[:], "scheme", s, None) }

// UnmarshalText refuses every spelling but MarshalText's.
func (s *Scheme) UnmarshalText(b []byte) error {
	return unmarshalName(schemeNames[:], "scheme", b, s, None)
}

func marshalName[E ~uint8](names []string, what string, v, first E) ([]byte, error) {
	if v < first || int(v) >= len(names) {
		return nil, fmt.Errorf("media: invalid %s %d", what, v)
	}
	return []byte(names[v]), nil
}

func unmarshalName[E ~uint8](names []string, what string, b []byte, v *E, first E) error {
	for i := int(first); i < len(names); i++ {
		if string(b) == names[i] {
			*v = E(i)
			return nil
		}
	}
	return fmt.Errorf("media: unknown %s", what)
}
