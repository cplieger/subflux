package api

import (
	"regexp"
	"strings"
	"testing"
)

func FuzzBuildEpisodeID(f *testing.F) {
	f.Add(12345, "tt1234567", 1, 1)
	f.Add(0, "tt0000001", 0, 0)
	f.Add(0, "", 99, 99)
	f.Add(-1, "", -1, -1)
	f.Add(999999, "", 100, 100)
	f.Fuzz(func(t *testing.T, tvdbID int, imdbID string, season, episode int) {
		result := BuildEpisodeID(tvdbID, imdbID, season, episode)
		if tvdbID != 0 && !strings.HasPrefix(result, "tvdb-") {
			t.Errorf("tvdbID=%d but result=%q does not start with tvdb-", tvdbID, result)
		}
	})
}

func FuzzBuildMovieID(f *testing.F) {
	f.Add(100, "tt1234567")
	f.Add(0, "tt0000001")
	f.Add(0, "")
	f.Add(-5, "")
	f.Fuzz(func(t *testing.T, tmdbID int, imdbID string) {
		result := BuildMovieID(tmdbID, imdbID)
		if tmdbID != 0 && !strings.HasPrefix(result, "tmdb-") {
			t.Errorf("tmdbID=%d but result=%q does not start with tmdb-", tmdbID, result)
		}
		if tmdbID == 0 && imdbID != "" && result != imdbID {
			t.Errorf("tmdbID=0, imdbID=%q but result=%q", imdbID, result)
		}
		if tmdbID == 0 && imdbID == "" && result != "" {
			t.Errorf("both zero but result=%q", result)
		}
	})
}

func FuzzBuildSeriesPrefix(f *testing.F) {
	f.Add(12345, "tt1234567")
	f.Add(0, "tt0000001")
	f.Add(0, "")
	f.Add(-1, "")
	f.Fuzz(func(t *testing.T, tvdbID int, imdbID string) {
		result := BuildSeriesPrefix(tvdbID, imdbID)
		if result != "" && !strings.HasSuffix(result, "-") {
			t.Errorf("non-empty result=%q does not end with -", result)
		}
		if tvdbID != 0 && !strings.HasPrefix(result, "tvdb-") {
			t.Errorf("tvdbID=%d but result=%q does not start with tvdb-", tvdbID, result)
		}
	})
}

// episodeIDSuffixRe matches the "-s<season>e<episode>" suffix that
// BuildEpisodeID appends. A constructed movie ID ("tmdb-<n>") must never carry
// this suffix; a bare "-s" inside a garbage IMDB fallback is not an episode
// marker.
var episodeIDSuffixRe = regexp.MustCompile(`-s\d+e\d+$`)

// FuzzBuildMediaID cross-checks the dispatcher against the individual ID
// builders and pins the movie-path invariant: a constructed movie ID never
// grows an episode "-sNNeNN" suffix.
func FuzzBuildMediaID(f *testing.F) {
	f.Add("movie", 100, 0, "tt1234567", 0, 0)
	f.Add("episode", 0, 12345, "tt0000001", 1, 5)
	f.Add("episode", 0, 0, "", 0, 0)
	f.Add("movie", 0, 0, "", 0, 0)
	f.Add("unknown", 50, 999, "tt9999999", 10, 20)
	f.Add("", 0, 0, "", 0, 0)

	f.Fuzz(func(t *testing.T, mediaType string, tmdbID, tvdbID int, imdbID string, season, episode int) {
		req := &SearchRequest{
			MediaType: MediaType(mediaType),
			TmdbID:    tmdbID,
			TvdbID:    tvdbID,
			ImdbID:    imdbID,
			Season:    season,
			Episode:   episode,
		}
		result := BuildMediaID(req)

		// The dispatcher must agree with the individual builder for each type.
		switch MediaType(mediaType) {
		case MediaTypeMovie:
			if want := BuildMovieID(tmdbID, imdbID); result != want {
				t.Errorf("BuildMediaID movie: got %q, want %q", result, want)
			}
		case MediaTypeEpisode:
			if want := BuildEpisodeID(tvdbID, imdbID, season, episode); result != want {
				t.Errorf("BuildMediaID episode: got %q, want %q", result, want)
			}
		default:
			// Unknown types fall through to the episode builder.
			if want := BuildEpisodeID(tvdbID, imdbID, season, episode); result != want {
				t.Errorf("BuildMediaID default: got %q, want %q", result, want)
			}
		}

		// A nil request always yields the empty ID.
		if BuildMediaID(nil) != "" {
			t.Error("BuildMediaID(nil) should return empty")
		}

		// A constructed movie ID is either "tmdb-<n>" or the verbatim IMDB
		// fallback. The verbatim fallback can be arbitrary input that happens
		// to look like an episode suffix (the corpus carries "-s0e0"), so only
		// the constructed form is held to the no-episode-marker invariant.
		if MediaType(mediaType) == MediaTypeMovie && result != "" && result != imdbID {
			if episodeIDSuffixRe.MatchString(result) {
				t.Errorf("constructed movie ID %q contains episode marker", result)
			}
		}
	})
}

// FuzzIsValidMediaPrefix verifies IsValidMediaPrefix never panics and pins the
// prefix-preservation invariant that follows from its ^-anchored, un-$-anchored
// regex: if a string is accepted, the same string with ANY suffix appended is
// still accepted (a match anchored at position 0 cannot be broken by trailing
// bytes). This is the property the security gate relies on as a caller appends
// season/episode segments to an already-validated prefix.
func FuzzIsValidMediaPrefix(f *testing.F) {
	f.Add("tvdb-12345", "")
	f.Add("tmdb-67890", "-s01e02")
	f.Add("imdb-tt1234567", "garbage")
	f.Add("", "tvdb-1-")
	f.Add("invalid", "tvdb-1-")
	f.Add("tvdb-", "9-")
	f.Fuzz(func(t *testing.T, prefix, suffix string) {
		if IsValidMediaPrefix(prefix) && !IsValidMediaPrefix(prefix+suffix) {
			t.Errorf("IsValidMediaPrefix(%q) = true but IsValidMediaPrefix(%q) = false; appending a suffix must not invalidate an accepted prefix", prefix, prefix+suffix)
		}
	})
}
