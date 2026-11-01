package media

// Choose keys an item: an episode by the id of its Numbering.Scheme with its
// season and episode, a movie by TMDB, then IMDb. An id ValidExternalID
// refuses counts as unknown. False means unidentified: a zero or unknown kind,
// an episode under None or with a number ValidEpisodeNumber refuses, or no
// usable id.
func Choose(kind Kind, ids ExternalIDs, n Numbering) (Key, bool) {
	switch kind {
	case Episode:
		id, ok := ids.of(n.Scheme)
		season, sok := toUint16(n.Season)
		episode, eok := toUint16(n.Episode)
		if !ok || !sok || !eok {
			return Key{}, false
		}
		return Key{root: RootKey{kind: Episode, scheme: n.Scheme, id: id}, season: season, episode: episode}, true
	case Movie:
		for _, s := range [...]Scheme{TMDB, IMDb} {
			if id, ok := ids.of(s); ok {
				return Key{root: RootKey{kind: Movie, scheme: s, id: id}}, true
			}
		}
	}
	return Key{}, false
}

func (e ExternalIDs) of(s Scheme) (uint64, bool) {
	var id uint64
	switch s {
	case TVDB:
		id = e.TVDB
	case TMDB:
		id = e.TMDB
	case IMDb:
		id = e.IMDb
	default:
		return 0, false
	}
	return id, ValidExternalID(id)
}

// toUint16 checks the int before converting it, so no number wraps into range.
func toUint16(n int) (uint16, bool) {
	if !ValidEpisodeNumber(n) {
		return 0, false
	}
	return uint16(n), true //nolint:gosec // G115: ValidEpisodeNumber bounds n to 0..65,535
}
