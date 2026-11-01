package media

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// RootKey names a movie or a series. Only Choose, ParseRootKey and Key.Root
// build one, so every non-zero RootKey is valid.
type RootKey struct {
	kind   Kind
	scheme Scheme
	id     uint64
}

// Key names a movie or one episode of a series. Only Choose and ParseKey build
// one, so every non-zero Key is valid; a movie's season and episode are zero.
type Key struct {
	root            RootKey
	season, episode uint16
}

const (
	rootKeyBytes = 10
	keyBytes     = rootKeyBytes + 4
	maxKeyText   = 64
)

// keyPrefixes spell each kind and scheme a key can hold; no prefix is a prefix
// of another's text, so parsing takes the first that matches.
var keyPrefixes = [...]struct {
	text   string
	kind   Kind
	scheme Scheme
}{
	{"tmdb-", Movie, TMDB},
	{"tt", Movie, IMDb},
	{"tvdb-", Episode, TVDB},
	{"tmdbtv-", Episode, TMDB},
	{"imdbtv-tt", Episode, IMDb},
}

// Root is the movie itself, or the series an episode belongs to.
func (k Key) Root() RootKey { return k.root }

// Bytes is kind, scheme and the id as a big-endian uint64: ten bytes, so a
// prefix scan over one root never reaches another (tmdb-12 against tmdb-120).
func (r RootKey) Bytes() []byte {
	b := make([]byte, rootKeyBytes)
	b[0], b[1] = byte(r.kind), byte(r.scheme)
	binary.BigEndian.PutUint64(b[2:], r.id)
	return b
}

// Bytes is the root's ten bytes, then season and episode as big-endian
// uint16s, zero for a movie: fourteen bytes.
func (k Key) Bytes() []byte {
	b := k.root.Bytes()
	b = binary.BigEndian.AppendUint16(b, k.season)
	return binary.BigEndian.AppendUint16(b, k.episode)
}

// String is the canonical text: tmdb-<id> or tt<id> for a movie; tvdb-<id>,
// tmdbtv-<id> or imdbtv-tt<id> for a series. Ids are decimal with no leading
// zero, IMDb's padded to seven digits. "" for the zero RootKey.
func (r RootKey) String() string {
	for _, p := range keyPrefixes {
		if p.kind == r.kind && p.scheme == r.scheme {
			return p.text + idText(r.scheme, r.id)
		}
	}
	return ""
}

// String is a movie's root text, or a series' root text followed by
// -s<season>e<episode>, each padded to two digits. "" for the zero Key.
func (k Key) String() string {
	if k.root.kind != Episode {
		return k.root.String()
	}
	return fmt.Sprintf("%s-s%02de%02d", k.root, k.season, k.episode)
}

// ParseKey is String's inverse, refusing every other spelling, a series root
// and any text over 64 bytes.
func ParseKey(s string) (Key, error) {
	if len(s) > maxKeyText {
		return Key{}, errTextTooLong
	}
	r, rest, ok := parseRoot(s)
	switch {
	case ok && r.kind == Movie && rest == "":
		return Key{root: r}, nil
	case ok && r.kind == Episode:
		if season, episode, ok := parseEpisode(rest); ok {
			return Key{root: r, season: season, episode: episode}, nil
		}
	}
	return Key{}, fmt.Errorf("media: %s is not an episode or movie key", strconv.Quote(s))
}

// ParseRootKey is RootKey.String's inverse, refusing every other spelling, an
// episode key and any text over 64 bytes.
func ParseRootKey(s string) (RootKey, error) {
	if len(s) > maxKeyText {
		return RootKey{}, errTextTooLong
	}
	if r, rest, ok := parseRoot(s); ok && rest == "" {
		return r, nil
	}
	return RootKey{}, fmt.Errorf("media: %s is not a series or movie key", strconv.Quote(s))
}

var errTextTooLong = fmt.Errorf("media: a key's text is at most %d bytes", maxKeyText)

func parseRoot(s string) (RootKey, string, bool) {
	for _, p := range keyPrefixes {
		rest, ok := strings.CutPrefix(s, p.text)
		if !ok {
			continue
		}
		digits, rest := cutDigits(rest)
		id, err := strconv.ParseUint(digits, 10, 64)
		if err != nil || !ValidExternalID(id) || idText(p.scheme, id) != digits {
			return RootKey{}, "", false
		}
		return RootKey{kind: p.kind, scheme: p.scheme, id: id}, rest, true
	}
	return RootKey{}, "", false
}

func parseEpisode(s string) (season, episode uint16, ok bool) {
	if s, ok = strings.CutPrefix(s, "-s"); !ok {
		return 0, 0, false
	}
	if season, s, ok = cutNumber(s); !ok {
		return 0, 0, false
	}
	if s, ok = strings.CutPrefix(s, "e"); !ok {
		return 0, 0, false
	}
	if episode, s, ok = cutNumber(s); !ok || s != "" {
		return 0, 0, false
	}
	return season, episode, true
}

// cutNumber reads a season or episode written with at least two digits and
// no other leading zero.
func cutNumber(s string) (n uint16, rest string, ok bool) {
	digits, rest := cutDigits(s)
	v, err := strconv.ParseUint(digits, 10, 16)
	if err != nil || fmt.Sprintf("%02d", v) != digits {
		return 0, "", false
	}
	return uint16(v), rest, true
}

func cutDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}

func idText(s Scheme, id uint64) string {
	if s == IMDb {
		return fmt.Sprintf("%07d", id)
	}
	return strconv.FormatUint(id, 10)
}
