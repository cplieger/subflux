package api

import (
	"regexp"
	"strconv"
)

// mediaPrefixRe validates media ID prefix parameters. Accepts:
//   - tvdb-{digits}- (series episode prefix)
//   - tmdb-{digits} (movie ID, with optional trailing dash)
//   - imdb-tt{digits} (IMDB-based, with optional trailing dash/season suffix)
//
// Rejects arbitrary strings that could produce confusing query results.
var mediaPrefixRe = regexp.MustCompile(
	`^(?:tvdb-\d+-|tmdb-\d+-?|imdb-tt\d+)`)

// IsValidMediaPrefix checks that a prefix parameter matches expected
// media ID formats. Prevents arbitrary prefix queries that could
// produce confusing results if the app is exposed without auth.
func IsValidMediaPrefix(prefix string) bool {
	return mediaPrefixRe.MatchString(prefix)
}

// BuildMediaID creates a stable identifier for a media item from a search request.
// Movies use TMDB ID (canonical for Radarr), episodes use TVDB ID (canonical for Sonarr).
// IMDB is only used as a last resort fallback.
func BuildMediaID(req *SearchRequest) string {
	if req == nil {
		return ""
	}
	switch req.MediaType {
	case mediaTypeMovie:
		return BuildMovieID(req.TmdbID, req.ImdbID)
	case mediaTypeEpisode:
		return BuildEpisodeID(req.TvdbID, req.ImdbID, req.Season, req.Episode)
	default:
		// Legacy fallthrough: treat unknown types as episodes so older
		// scan state with missing MediaType still resolves.
		return BuildEpisodeID(req.TvdbID, req.ImdbID, req.Season, req.Episode)
	}
}

// BuildMovieID returns the canonical media ID for a movie.
// Prefer TMDB (Radarr's canonical source), fall back to IMDB.
func BuildMovieID(tmdbID int, imdbID string) string {
	if tmdbID != 0 {
		return "tmdb-" + strconv.Itoa(tmdbID)
	}
	if imdbID != "" {
		return imdbID
	}
	return ""
}

// BuildEpisodeID returns the canonical media ID for an episode.
// Prefer TVDB (Sonarr's canonical source), fall back to IMDB.
func BuildEpisodeID(tvdbID int, imdbID string, season, episode int) string {
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
		b = append(b, '0', byte('0'+n))
	case n < 100:
		b = append(b, byte('0'+n/10), byte('0'+n%10))
	default:
		b = strconv.AppendInt(b, int64(n), 10)
	}
	return b
}

// BuildSeriesPrefix returns the media ID prefix for all episodes of a series.
// Used by coverage and missing-count queries to match all episodes via LIKE.
func BuildSeriesPrefix(tvdbID int, imdbID string) string {
	if tvdbID != 0 {
		return "tvdb-" + strconv.Itoa(tvdbID) + "-"
	}
	if imdbID != "" {
		return imdbID + "-"
	}
	return ""
}
