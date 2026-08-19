// Package mediaid builds the stable identifier subflux addresses one media item
// by: "tmdb-27205" for a movie, "tvdb-121361-s01e09" for an episode, plus the
// prefixes that select a whole season or series in one scan.
//
// The identifier is the bbolt key for every per-item row (search_attempts,
// subtitle_state, scan_state, backoff) and a path segment in the HTTP API, so
// the format here is storage and wire contract in both directions: Episode and
// SeasonPrefix must agree through the episode separator or a season's rows stop
// being one prefix scan, and ValidPrefix is what keeps a client-supplied prefix
// from selecting arbitrary rows.
//
// Separate from the subtitle FILENAME builders even though both spell a media
// item's name: those address a file on a filesystem shared with Windows clients
// and this addresses a row in a database, and of the nine consumers only
// manualops and search use both.
package mediaid

import (
	"regexp"
	"strconv"

	"github.com/cplieger/subflux/internal/subflux"
)

// mediaPrefixRe validates media ID prefix parameters. Accepts:
//   - tvdb-{digits}- (series episode prefix)
//   - tmdb-{digits} (movie ID, with optional trailing dash)
//   - imdb-tt{digits} (IMDB-based, with optional trailing dash/season suffix)
//
// Rejects arbitrary strings that could produce confusing query results.
var mediaPrefixRe = regexp.MustCompile(
	`^(?:tvdb-\d+-|tmdb-\d+-?|imdb-tt\d+)`)

// ValidPrefix checks that a prefix parameter matches expected
// media ID formats. Prevents arbitrary prefix queries that could
// produce confusing results if the app is exposed without auth.
func ValidPrefix(prefix string) bool {
	return mediaPrefixRe.MatchString(prefix)
}

// Build creates a stable identifier for a media item from a search request.
// Movies use TMDB ID (canonical for Radarr), episodes use TVDB ID (canonical for Sonarr).
// IMDB is only used as a last resort fallback.
func Build(req *subflux.SearchRequest) string {
	if req == nil {
		return ""
	}
	switch req.MediaType {
	case subflux.MediaTypeMovie:
		return Movie(req.TmdbID, req.ImdbID)
	case subflux.MediaTypeEpisode:
		return Episode(req.TvdbID, req.ImdbID, req.Season, req.Episode)
	default:
		// Legacy fallthrough: treat unknown types as episodes so older
		// scan state with missing subflux.MediaType still resolves.
		return Episode(req.TvdbID, req.ImdbID, req.Season, req.Episode)
	}
}

// Movie returns the canonical media ID for a movie.
// Prefer TMDB (Radarr's canonical source), fall back to IMDB.
func Movie(tmdbID int, imdbID string) string {
	if tmdbID != 0 {
		return "tmdb-" + strconv.Itoa(tmdbID)
	}
	if imdbID != "" {
		return imdbID
	}
	return ""
}

// Episode returns the canonical media ID for an episode.
// Prefer TVDB (Sonarr's canonical source), fall back to IMDB.
func Episode(tvdbID int, imdbID string, season, episode int) string {
	if tvdbID != 0 {
		var buf [32]byte
		b := buf[:0]
		b = append(b, "tvdb-"...)
		b = strconv.AppendInt(b, int64(tvdbID), 10)
		b = append(b, "-s"...)
		b = appendPadded2(b, season)
		b = append(b, 'e')
		b = appendPadded2(b, episode)
		return string(b)
	}
	if imdbID != "" {
		var buf [32]byte
		b := buf[:0]
		b = append(b, imdbID...)
		b = append(b, "-s"...)
		b = appendPadded2(b, season)
		b = append(b, 'e')
		b = appendPadded2(b, episode)
		return string(b)
	}
	var buf [8]byte
	b := buf[:0]
	b = append(b, 's')
	b = appendPadded2(b, season)
	b = append(b, 'e')
	b = appendPadded2(b, episode)
	return string(b)
}

// appendPadded2 appends a zero-padded 2-digit integer to b.
func appendPadded2(b []byte, n int) []byte {
	switch {
	case n < 10:
		b = append(b, '0', byte('0'+n)) //nolint:gosec // G115: n is 0-9, fits in byte
	case n < 100:
		b = append(b, byte('0'+n/10), byte('0'+n%10))
	default:
		b = strconv.AppendInt(b, int64(n), 10)
	}
	return b
}

// SeasonPrefix returns the media ID prefix shared by every episode of
// one season, mirroring Episode's format through the episode
// separator (e.g. "tvdb-123-s05e"). Episode(tvdb, imdb, season, N)
// has this prefix for every N in 0-99, so backoff and state rows for a
// season are one prefix scan. Returns "" when neither ID is available
// (matching Episode's unidentified-media fallback shape only per
// season would be meaninglessly broad).
func SeasonPrefix(tvdbID int, imdbID string, season int) string {
	if tvdbID == 0 && imdbID == "" {
		return ""
	}
	var buf [32]byte
	b := buf[:0]
	if tvdbID != 0 {
		b = append(b, "tvdb-"...)
		b = strconv.AppendInt(b, int64(tvdbID), 10)
	} else {
		b = append(b, imdbID...)
	}
	b = append(b, "-s"...)
	b = appendPadded2(b, season)
	b = append(b, 'e')
	return string(b)
}

// SeriesPrefix returns the media ID prefix for all episodes of a series.
// Used by coverage and missing-count queries to match all episodes via LIKE.
func SeriesPrefix(tvdbID int, imdbID string) string {
	if tvdbID != 0 {
		return "tvdb-" + strconv.Itoa(tvdbID) + "-"
	}
	if imdbID != "" {
		return imdbID + "-"
	}
	return ""
}
