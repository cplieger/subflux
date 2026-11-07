package animemap

import (
	"slices"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
)

// Landings are the AniDB episodes q is, in mapread's order (AniDB id, kind,
// number): for a TVDB-numbered episode every AniDB episode landing on it,
// only the stated ones when any is, since a stated landing is evidence a
// default's "the numbers match" does not hold there; for a movie the stated
// landings of its TMDB id on the one regular episode of a one-episode anime.
// Every other query, and an id at 0, has none.
func Landings(x *mapread.Index, q *provider.Query) []mapread.Landing {
	if !Keyed(q) {
		return nil
	}
	var ls []mapread.Landing
	if q.Kind == media.Movie {
		for _, l := range x.AtTMDBMovie(mapID(q.External.TMDB)) {
			if l.Kind == mapread.Regular && l.Number == 1 && Single(x, l.AniDB) {
				ls = append(ls, l)
			}
		}
	} else {
		ls = x.AtTVDB(mapread.TVDBEpisode{Series: mapID(q.External.TVDB), Season: q.Numbering.Season, Episode: q.Numbering.Episode})
	}
	if stated := slices.DeleteFunc(slices.Clone(ls), func(l mapread.Landing) bool { return l.Default }); len(stated) > 0 {
		return stated
	}
	return ls
}

// Keyed reports whether q carries an id the map is keyed by: a movie's TMDB
// id, or a TVDB-numbered episode's series. mapread maps TMDB movies only, so a
// TMDB-numbered episode, like an IMDb-only query, has none.
func Keyed(q *provider.Query) bool {
	switch q.Kind {
	case media.Movie:
		return media.ValidExternalID(q.External.TMDB)
	case media.Episode:
		return q.Numbering.Scheme == media.TVDB && media.ValidExternalID(q.External.TVDB)
	}
	return false
}

// Single reports whether anime aid has exactly one regular episode, so a
// file of it claiming no number is that episode: a film or a one-episode OVA.
func Single(x *mapread.Index, aid int) bool {
	a, ok := x.Anime(aid)
	return ok && a.Last.Regular == 1
}

// mapID is an external id as mapread keys it. Keyed holds every id it is
// called on below 2^53 (media.ValidExternalID), so none overflows an int.
func mapID(id uint64) int { return int(id) } //nolint:gosec // G115: bounded by media.ValidExternalID

// Covers are the episodes of an episode query's TVDB series AniDB episode
// ref lands on, in TVDB numbering; nil when it lands on none there, and for
// any other query. A placement on another series is left out, since a
// Numbering names no series.
func Covers(x *mapread.Index, ref mapread.Ref, q *provider.Query) []media.Numbering {
	ep, ok := x.Episode(ref)
	if !Keyed(q) || q.Kind != media.Episode || !ok || ep.TVDB == nil || ep.TVDB.State != mapread.Lands {
		return nil
	}
	var out []media.Numbering
	for _, p := range ep.TVDB.Placements {
		if p.Series == mapID(q.External.TVDB) {
			out = append(out, media.Numbering{Scheme: media.TVDB, Season: p.Season, Episode: p.Episode})
		}
	}
	return out
}
